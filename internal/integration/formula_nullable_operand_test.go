package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// formula_nullable_operand_test.go pins PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND
// against the runtime: Pulse.Predict raises it for an ATTR_FORMULA
// exactly when Pulse.Process aborts on a row where a nullable input is
// null. The operand cohort holds every null / present combination of
// its nullable columns (x, y, c, s), so every guard branch runs on a
// null row.

const operandCohort = "operand.pulse"

// writeOperandCohort writes the operand cohort: x, y nullable f64, z a
// non-null f64, c a nullable categorical, s a nullable set, d a nullable
// decimal, u a nullable u64, g a categorical partition and day a date.
func writeOperandCohort(t testing.TB, p *pulse.Pulse) {
	t.Helper()
	fields := []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "z", Type: encoding.FieldTypeF64},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Nullable: true},
		{Name: "s", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: parityDict(t, "t", 2)},
		{Name: "d", Type: encoding.FieldTypeDecimal128, Nullable: true, Precision: 18, Scale: 2},
		{Name: "u", Type: encoding.FieldTypeU64, Nullable: true},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8},
		{Name: "day", Type: encoding.FieldTypeDate},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Nullable-operand parity column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	b, err := p.NewCohortBuilder(context.Background(), operandCohort, encoding.Schema{Fields: fields}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nums := []any{nil, 1.5, 5.5}
	r := 0
	for _, x := range nums {
		for _, y := range nums {
			for _, z := range []any{1.5, 5.5} {
				for _, c := range []any{nil, "north", "south"} {
					for _, s := range []any{nil, []string{"t000"}, []string{"t000", "t001"}} {
						var d, u any
						if r%4 != 0 {
							d = encoding.NewDecimal128FromInt(int64(r * 25))
						}
						if r%5 != 0 {
							u = uint64(r)
						}
						row := pulse.CohortRow{x, y, z, c, s, d, u, []string{"a", "b"}[r%2], int32(19000 + r%30)}
						if err := b.Append(row); err != nil {
							t.Fatal(err)
						}
						r++
					}
				}
			}
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// openOperandPulse is a Pulse over a fresh operand cohort, with the
// nullable / plain echo extension attributes and a lookup table.
func openOperandPulse(t testing.TB) *pulse.Pulse {
	t.Helper()
	ext := nullEchoExtensions()
	ext.LookupTables = map[string]pulse.LookupTable{
		"rates": {Rows: map[string]float64{"north": 1, "south": 2}},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	writeOperandCohort(t, p)
	return p
}

// operandVerdict predicts and runs req: whether predict raised the
// operand advisory (on a valid request) and whether Process aborted.
func operandVerdict(t testing.TB, p *pulse.Pulse, req *types.Request) (fired, aborted bool) {
	t.Helper()
	ctx := context.Background()
	pred, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !pred.Valid {
		t.Fatalf("Predict: invalid request %+v", req)
	}
	for _, a := range pred.Advisories {
		fired = fired || a.Code == string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND)
	}
	_, err = p.Process(ctx, req)
	if err != nil && !errors.HasCode(err, errors.PROCESSING_RUNTIME) && !errors.HasCode(err, errors.PULSE_LOOKUP_MISS) {
		t.Fatalf("Process failed for another reason: %v", err)
	}
	return fired, err != nil
}

// formulaOverOperands is a request deriving attrs, then the formula
// under label "out", summed.
func formulaOverOperands(expr string, attrs ...*types.Attribute) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: operandCohort},
		Attributes:   append(attrs, &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: "out"}),
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "out", Label: "s"}},
	}
}

// TestFormulaNullableOperand_AdvisoryMatchesRuntime: per case, the
// advisory fires ⇔ Process aborts — over schema fields of every
// nullable kind (number, categorical, set, decimal, u64), a lookup()
// key, a total() argument (drops, never aborts), and earlier derived
// columns (built-in scores, never-null indicators, a nullable formula
// result, a date part, extension attributes with and without
// extend.NullableAttribute).
func TestFormulaNullableOperand_AdvisoryMatchesRuntime(t *testing.T) {
	t.Parallel()

	p := openOperandPulse(t)
	attr := func(typ types.AttributeType, field, label, params string) *types.Attribute {
		a := &types.Attribute{Type: typ, Field: field, Label: label}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		return a
	}
	formula := func(label, expr string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: label}
	}
	cases := []struct {
		name  string
		req   *types.Request
		fires bool
	}{
		{"arithmetic", formulaOverOperands("x * 2"), true},
		{"ternary guard", formulaOverOperands("x != nil ? x * 2 : 0"), false},
		{"short-circuit guard", formulaOverOperands("x != nil && x > 1 ? 1 : 0"), false},
		{"short-circuit unguarded", formulaOverOperands("z > 2 && x > 1 ? 1 : 0"), true},
		{"coalesce", formulaOverOperands("(x ?? 0) * 2"), false},
		{"nil-safe compare", formulaOverOperands("x == 1.5 ? 1 : 0"), false},
		{"categorical len", formulaOverOperands("len(c)"), true},
		{"categorical matches", formulaOverOperands(`c matches "^n" ? 1 : 0`), false},
		{"categorical concat", formulaOverOperands(`len(c + "!")`), true},
		{"set membership", formulaOverOperands(`"t000" in s ? 1 : 0`), false},
		{"set len", formulaOverOperands("len(s)"), true},
		{"set index", formulaOverOperands(`s[0] == "t000" ? 1 : 0`), true},
		{"has_any candidates", formulaOverOperands(`has_any(["t000"], s) ? 1 : 0`), true},
		{"has_any set", formulaOverOperands(`has_any(s, ["t000"]) ? 1 : 0`), false},
		{"decimal arithmetic", formulaOverOperands("d * 2"), true},
		{"decimal compare", formulaOverOperands("d > 1 ? 1 : 0"), true},
		{"decimal equality", formulaOverOperands("d == 1 ? 1 : 0"), false},
		{"decimal guard", formulaOverOperands("d == nil ? 0 : d * 2"), false},
		{"u64 arithmetic", formulaOverOperands("u + 1"), true},
		{"u64 equality", formulaOverOperands("u == 5 ? 1 : 0"), false},
		{"u64 compare", formulaOverOperands("u > 5 ? 1 : 0"), true},
		{"lookup key", formulaOverOperands(`lookup("rates", c)`), true},
		{"lookup guarded key", formulaOverOperands(`c == nil ? 0 : lookup("rates", c)`), false},
		{"total argument", formulaOverOperands("total(x * 2) > 0 ? 1 : 0"), false},
		{"nil-taking function", formulaOverOperands("sin(x) ?? 0"), false},
		{"score over a nullable field", formulaOverOperands("zx * 2", attr(types.ATTR_ZSCORE, "x", "zx", "")), true},
		{"score over a non-null field", formulaOverOperands("zz * 2", attr(types.ATTR_ZSCORE, "z", "zz", "")), false},
		{"percentile over a nullable field", formulaOverOperands("px * 2", attr(types.ATTR_PERCENTILE, "x", "px", "")), true},
		{"popcount over a nullable set", formulaOverOperands("sp * 2", attr(types.ATTR_SET_POPCOUNT, "s", "sp", "")), true},
		{"set has over a nullable set", formulaOverOperands("sh * 2", attr(types.ATTR_SET_HAS, "s", "sh", `{"label": "t000"}`)), false},
		{"code in over a nullable field", formulaOverOperands("ci * 2", attr(types.ATTR_CODE_IN, "c", "ci", `{"codes": ["north"]}`)), false},
		{"date part over a non-null date", formulaOverOperands("dp * 2", attr(types.ATTR_DATE_PART, "day", "dp", `{"part": "month"}`)), false},
		{"previous state (a subject's first row)", formulaOverOperands("len(prev)", &types.Attribute{Type: types.ATTR_PREVIOUS_STATE, Field: "g", SubjectField: "z", OrderField: "day", Label: "prev"}), true},
		{"nullable formula result", formulaOverOperands("a + 1", formula("a", "x")), true},
		{"guarded formula result", formulaOverOperands("a + 1", formula("a", "x ?? 0")), false},
		{"extension emitting null", formulaOverOperands("e * 2", attr(attrNullEcho, "x", "e", "")), true},
		{"extension opting out", formulaOverOperands("e * 2", attr(attrNullEchoOff, "x", "e", "")), false},
		{"extension without the hook", formulaOverOperands("e * 2", attr(attrNullEchoPlain, "x", "e", "")), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fired, aborted := operandVerdict(t, p, c.req)
			if fired != c.fires || aborted != c.fires {
				t.Errorf("advisory fired=%v, Process aborted=%v; want both %v", fired, aborted, c.fires)
			}
		})
	}
}

// TestFormulaNullableOperand_NonAbortingSitesSilent: the other
// row-level expression sites never abort on a null operand —
// FILTER_EXPRESSION drops the row, a WIN_FORMULA over the record rows
// reads a null input as a null output, a regression holdout predicate
// skips the row — so none of them raises the advisory, and each runs.
func TestFormulaNullableOperand_NonAbortingSitesSilent(t *testing.T) {
	t.Parallel()

	p := openOperandPulse(t)
	base := func() *types.Request { return &types.Request{Cohort: &types.Cohort{Filename: operandCohort}} }
	filter := base()
	filter.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x * 2 > 3"}}
	filter.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "z", Label: "n"}}
	window := base()
	window.Windows = winFormulaWindows(`[{"type": "WIN_FORMULA", "label": "wf", "params": {"expression": "x * 2"}}]`)
	holdout := base()
	holdout.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Target: "z", Predictors: []string{"y"}, Holdout: "x > 3"}}
	for name, req := range map[string]*types.Request{"FILTER_EXPRESSION": filter, "WIN_FORMULA": window, "holdout": holdout} {
		t.Run(name, func(t *testing.T) {
			fired, aborted := operandVerdict(t, p, req)
			if fired || aborted {
				t.Errorf("advisory fired=%v, Process aborted=%v; want neither", fired, aborted)
			}
		})
	}
}
