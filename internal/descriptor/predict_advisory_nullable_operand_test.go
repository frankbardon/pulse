package descriptor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// nullableOperandCohort: x non-nullable, n / m nullable f64 columns, ts
// a datetime and tz a nullable per-row zone field.
func nullableOperandCohort(t *testing.T) []byte {
	t.Helper()
	tz := encoding.NewDictionary()
	for _, z := range []string{"UTC", "Europe/Paris"} {
		if _, err := tz.Add(z); err != nil {
			t.Fatal(err)
		}
	}
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Measured score per respondent"},
		{Name: "n", Type: encoding.FieldTypeF64, Nullable: true, Description: "Optional follow-up score"},
		{Name: "m", Type: encoding.FieldTypeF64, Nullable: true, Description: "Optional second follow-up score"},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Moment the response was recorded"},
		{Name: "tz", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: tz, Description: "Respondent's local time zone"},
	}})
}

func operandFormula(label, expr string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: label}
}

func operandRequest(attrs ...*types.Attribute) *types.Request {
	return &types.Request{Attributes: attrs, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
}

func operandAdvisories(res *descriptor.PredictResult) []descriptor.Advisory {
	var out []descriptor.Advisory
	for _, a := range res.Advisories {
		if a.Code == string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND) {
			out = append(out, a)
		}
	}
	return out
}

// TestPredict_AdvisoryFormulaNullableOperand: one
// PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND per ATTR_FORMULA feeding a
// nullable input unguarded to a nil-rejecting operand — a nullable
// field, or an earlier attribute whose output can be null by its
// declaration (a score over a nullable field, a nullable formula
// result, a date part under a nullable zone field) — naming slot,
// label, fields and operand_kind; guards, nil-safe operators,
// never-null outputs, non-nullable inputs and a hidden ATTR_FORMULA
// stay silent.
func TestPredict_AdvisoryFormulaNullableOperand(t *testing.T) {
	t.Parallel()

	type want struct {
		slot, label string
		fields      []string
		kinds       []string
	}
	zs := func(label, field string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_ZSCORE, Field: field, Label: label}
	}
	part := func(label string, a *types.Attribute) *types.Attribute {
		a.Label = label
		return a
	}
	hour := &types.Attribute{Type: types.ATTR_DATE_PART, Field: "ts", Params: json.RawMessage(`{"part":"hour"}`)}
	cases := []struct {
		name string
		req  *types.Request
		inst *InstanceSnapshot
		want []want
	}{
		{"arithmetic", operandRequest(operandFormula("f", "n * 2")), nil,
			[]want{{"attributes[0]", "f", []string{"n"}, []string{"arithmetic"}}}},
		{"two fields, two kinds", operandRequest(operandFormula("a", "x"), operandFormula("f", "abs(m) + (n > 1 ? 1 : 0)")), nil,
			[]want{{"attributes[1]", "f", []string{"m", "n"}, []string{"function", "compare"}}}},
		{"unary minus", operandRequest(operandFormula("f", "-n")), nil,
			[]want{{"attributes[0]", "f", []string{"n"}, []string{"unary_minus"}}}},
		{"ternary guard", operandRequest(operandFormula("f", "n != nil ? n * 2 : 0")), nil, nil},
		{"short-circuit guard", operandRequest(operandFormula("f", "n != nil && n > 1 ? 1 : 0")), nil, nil},
		{"coalesce guard", operandRequest(operandFormula("f", "(n ?? 0) * 2")), nil, nil},
		{"nil-safe compare", operandRequest(operandFormula("f", "n == 1 ? 1 : 0")), nil, nil},
		{"non-nullable input", operandRequest(operandFormula("f", "x * 2")), nil, nil},
		{"result-only read", operandRequest(operandFormula("f", "n")), nil, nil},
		{"hidden ATTR_FORMULA", operandRequest(operandFormula("f", "n * 2")), scopedExcept("ATTR_FORMULA"), nil},
		{"derived: score over a nullable field", operandRequest(zs("zn", "n"), operandFormula("f", "zn * 2")), nil,
			[]want{{"attributes[1]", "f", []string{"zn"}, []string{"arithmetic"}}}},
		{"derived: score over a non-nullable field", operandRequest(zs("zx", "x"), operandFormula("f", "zx * 2")), nil, nil},
		{"derived: default label", operandRequest(&types.Attribute{Type: types.ATTR_ZSCORE, Field: "n"}, operandFormula("f", "ATTR_ZSCORE_n * 2")), nil,
			[]want{{"attributes[1]", "f", []string{"ATTR_ZSCORE_n"}, []string{"arithmetic"}}}},
		{"derived: nullable formula result", operandRequest(operandFormula("a", "n"), operandFormula("f", "a + 1")), nil,
			[]want{{"attributes[1]", "f", []string{"a"}, []string{"arithmetic"}}}},
		{"derived: guarded formula result", operandRequest(operandFormula("a", "n ?? 0"), operandFormula("f", "a + 1")), nil, nil},
		{"derived: date part under a nullable zone field", operandRequest(part("h", &types.Attribute{Type: types.ATTR_DATE_PART, Field: "ts", TimeZoneField: "tz", Params: json.RawMessage(`{"part":"hour"}`)}), operandFormula("f", "h * 2")), nil,
			[]want{{"attributes[1]", "f", []string{"h"}, []string{"arithmetic"}}}},
		{"derived: date part under a fixed zone", operandRequest(part("h", hour), operandFormula("f", "h * 2")), nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := predictFromBytes(nullableOperandCohort(t), c.req, &PredictOptions{Instance: c.inst})
			res := env.Data.(*descriptor.PredictResult)
			if !res.Valid && c.inst == nil {
				t.Fatalf("invalid predict: %+v", env.Errors[0])
			}
			got := operandAdvisories(res)
			if len(got) != len(c.want) {
				t.Fatalf("advisories = %+v, want %d", got, len(c.want))
			}
			for i, w := range c.want {
				a := got[i]
				wantDetails := map[string]any{"slot": w.slot, "operator": "ATTR_FORMULA", "label": w.label,
					"fields": w.fields, "operand_kind": w.kinds}
				if !reflect.DeepEqual(a.Details, wantDetails) {
					t.Errorf("details = %v, want %v", a.Details, wantDetails)
				}
				if !strings.Contains(a.Message, w.slot+" ("+w.label+")") {
					t.Errorf("message %q does not name %s (%s)", a.Message, w.slot, w.label)
				}
				for _, f := range w.fields {
					if !strings.Contains(a.Message, f+" ?? 0") {
						t.Errorf("message %q does not suggest guarding %s", a.Message, f)
					}
				}
				if hits := LintGuidanceText(a.Message); len(hits) > 0 {
					t.Errorf("lint hits %+v", hits)
				}
			}
		})
	}
}

// TestPredict_AdvisoryFormulaNullableOperandNeverEscalated: strict
// predict keeps the advisory (valid, no error, no warning); suppressing
// the code drops it, and SuppressAdvisories accepts the code.
func TestPredict_AdvisoryFormulaNullableOperandNeverEscalated(t *testing.T) {
	t.Parallel()

	code := string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND)
	req := operandRequest(operandFormula("f", "n * 2"))
	env := predictFromBytes(nullableOperandCohort(t), req, &PredictOptions{Strict: true})
	res := env.Data.(*descriptor.PredictResult)
	if len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Errorf("strict: valid=%v errors=%v warnings=%v, want a clean valid result", res.Valid, env.Errors, env.Warnings)
	}
	if err := ValidateSuppressAdvisories([]string{code}); err != nil {
		t.Fatalf("SuppressAdvisories refuses %s: %v", code, err)
	}
	suppressed := &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})}
	b, err := json.Marshal(predictFromBytes(nullableOperandCohort(t), req, suppressed))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"advisories"`) {
		t.Errorf("suppressed predict carries the key: %s", b)
	}
}

// TestPredict_AdvisoryFormulaNullableOperandExtension: an earlier
// extension attribute's label is nullable exactly when the snapshot's
// hook (the instance's extend.NullableAttribute) says so; the hook runs
// only for a label a formula reads, and never on a refused request.
func TestPredict_AdvisoryFormulaNullableOperandExtension(t *testing.T) {
	t.Parallel()

	const ext = types.AttributeType("ATTR_ACME_SCORE")
	cases := []struct {
		name     string
		emits    bool
		formula  string
		refuse   bool
		advisory bool
		calls    int
	}{
		{"emits null", true, "e * 2", false, true, 1},
		{"opts out", false, "e * 2", false, false, 1},
		{"label not read", true, "x * 2", false, false, 0},
		{"refused request", true, "e * 2", true, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			snap := &ExtensionsSnapshot{
				Attributes: []descriptor.OperatorMeta{{Name: string(ext), Namespace: "ACME"}},
				AttributeEmitsNull: map[types.AttributeType]func(*types.Attribute, *encoding.Schema) bool{
					ext: func(spec *types.Attribute, schema *encoding.Schema) bool {
						calls++
						return c.emits && spec.Field == "x" && schema.Field("x") != nil
					},
				},
			}
			req := operandRequest(&types.Attribute{Type: ext, Field: "x", Label: "e"}, operandFormula("f", c.formula))
			if c.refuse {
				req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "nope", Values: []string{"a"}}}
			}
			env := predictFromBytes(nullableOperandCohort(t), req, &PredictOptions{Extensions: snap, Instance: NewInstanceSnapshot(snap, FeatureSet{})})
			if (len(env.Errors) > 0) != c.refuse {
				t.Fatalf("errors %d, want refused=%v", len(env.Errors), c.refuse)
			}
			res := env.Data.(*descriptor.PredictResult)
			if got := len(operandAdvisories(res)) == 1; got != c.advisory {
				t.Errorf("advisory=%v (%+v), want %v", got, res.Advisories, c.advisory)
			}
			if calls != c.calls {
				t.Errorf("hook ran %d times, want %d", calls, c.calls)
			}
		})
	}
}

// TestPredict_AdvisoryFormulaNullableOperandEarlierLabelsOnly: an
// attribute's nullability is judged over the labels declared before
// it, so a score over a label declared after it (a refused request,
// whose advisories still run) is not nullable through that label.
func TestPredict_AdvisoryFormulaNullableOperandEarlierLabelsOnly(t *testing.T) {
	t.Parallel()

	req := operandRequest(&types.Attribute{Type: types.ATTR_ZSCORE, Field: "a", Label: "zs"}, operandFormula("a", "n"), operandFormula("f", "zs * 2"))
	env := predictFromBytes(nullableOperandCohort(t), req, nil)
	if len(env.Errors) == 0 {
		t.Fatal("vacuous: a score over a later label is not refused")
	}
	if got := operandAdvisories(env.Data.(*descriptor.PredictResult)); len(got) != 0 {
		t.Errorf("advisories %+v, want none: zs is judged before a exists", got)
	}
}

// TestAttributeNullOutputs_Complete: every built-in attribute declares
// when its output can be null.
func TestAttributeNullOutputs_Complete(t *testing.T) {
	t.Parallel()

	for _, at := range types.AllAttributeTypes() {
		if _, ok := attributeNullOutputs[at]; !ok {
			t.Errorf("%s has no attributeNullOutputs declaration", at)
		}
	}
	if len(attributeNullOutputs) != len(types.AllAttributeTypes()) {
		t.Errorf("attributeNullOutputs has %d entries, want %d", len(attributeNullOutputs), len(types.AllAttributeTypes()))
	}
}
