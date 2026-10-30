package integration_test

import (
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The trigonometry set and the constants (U58 E1-S2): sin, cos, tan,
// atan2, pi(), e() join the shared expression environment, so they
// reach ATTR_FORMULA, FILTER_EXPRESSION, total() arguments and predict's
// compile check through the one registry.

// trigCohort writes an all-f64 cohort, one column per name, row r
// holding vals(r)[i] in column i, and returns the instance and the
// file's bytes.
func trigCohort(t *testing.T, path string, names []string, rows int, vals func(r int) []float64) (*pulse.Pulse, []byte) {
	t.Helper()
	fields := make([]encoding.Field, len(names))
	for i, n := range names {
		fields[i] = encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: 8 * i}
	}
	s := &encoding.Schema{Fields: fields}
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < rows; r++ {
		rec := make([]byte, s.RecordByteSize())
		for i, v := range vals(r) {
			binary.LittleEndian.PutUint64(rec[8*i:], math.Float64bits(v))
		}
		buf = append(buf, rec...)
	}
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, buf
}

// TestFormulaTrig_FourierAndFilterRun: the Fourier seasonality term in
// an ATTR_FORMULA and a cosine in a FILTER_EXPRESSION run, predict
// accepts both, and the figures are the textbook ones: over 52 weekly
// rows sin(2π·week/52) sums to 0 and squares-plus-cos-squares to 52.
func TestFormulaTrig_FourierAndFilterRun(t *testing.T) {
	t.Parallel()

	p, data := trigCohort(t, "w.pulse", []string{"week", "x"}, 52, func(r int) []float64 {
		return []float64{float64(r), float64(r) * 0.25}
	})
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "w.pulse"},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "s1", Expression: "sin(2*pi()*week/52)"},
				{Type: types.ATTR_FORMULA, Label: "c1", Expression: "cos(2 * pi() * week / 52)"},
				{Type: types.ATTR_FORMULA, Label: "one", Expression: "s1 ** 2 + c1 ** 2"},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "s1", Label: "sum_s1"},
				{Type: types.AGG_SUM, Field: "one", Label: "sum_one"},
			},
		}
	}
	if pe := predictCodedError(t, p, data, req()); pe != nil {
		t.Fatalf("predict refused the Fourier formula: %+v", pe)
	}
	resp, err := p.Process(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["sum_s1"].(float64); math.Abs(got) > 1e-12 {
		t.Errorf("Σ sin(2π·week/52) = %v, want 0", got)
	}
	if got := resp.Data[0]["sum_one"].(float64); math.Abs(got-52) > 1e-12 {
		t.Errorf("Σ sin² + cos² = %v, want 52", got)
	}

	// x = r/4 runs 0..12.75 rad; cos(x) > 0 keeps x in [0, π/2),
	// (3π/2, 5π/2) and (7π/2, 4π]: rows 0-6, 19-31 and 44-51.
	filter := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "w.pulse"},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cos(x) > 0"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		}
	}
	if pe := predictCodedError(t, p, data, filter()); pe != nil {
		t.Fatalf("predict refused the cosine filter: %+v", pe)
	}
	resp, err = p.Process(context.Background(), filter())
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for r := 0; r < 52; r++ {
		if math.Cos(float64(r)*0.25) > 0 {
			want++
		}
	}
	if got := toFloat(t, resp.Data[0]["n"]); got != float64(want) {
		t.Errorf("cos(x) > 0 kept %v rows, want %d", got, want)
	}
}

// TestFormulaTrig_FieldPrecedence pins today's resolution rule for a
// cohort field named like a new built-in: a BARE name is the field (the
// env resolves before any function), and a CALL of that name is then a
// call on a number — refused at compile, identically at predict. A
// field named pi / e therefore keeps working exactly as before, and the
// constant is reachable only where no such field exists.
func TestFormulaTrig_FieldPrecedence(t *testing.T) {
	t.Parallel()

	p, data := trigCohort(t, "f.pulse", []string{"sin", "pi", "e", "x"}, 8, func(r int) []float64 {
		return []float64{10 + float64(r), 3, 2, float64(r)}
	})
	mk := func(expr string) func() *types.Request {
		return func() *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: "f.pulse"},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "v", Expression: expr}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
			}
		}
	}
	// Bare names read the fields: Σ (sin + pi * e) = Σ (10 + r) + 8·6.
	resp, err := p.Process(context.Background(), mk("sin + pi * e")())
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"].(float64); got != 108+48 {
		t.Errorf("Σ (sin + pi * e) = %v, want %v (the fields, not the built-ins)", got, 108+48)
	}
	if pe := predictCodedError(t, p, data, mk("sin + pi * e")()); pe != nil {
		t.Fatalf("predict refused bare field names: %+v", pe)
	}
	// A call of a name a field shadows is a call on a number.
	for _, expr := range []string{"sin(x)", "pi() * x", "e() + x"} {
		t.Run(expr, func(t *testing.T) {
			assertFormulaCompileParity(t, p, data, mk(expr), true)
		})
	}
	// The other new built-ins stay reachable beside the shadowed ones.
	resp, err = p.Process(context.Background(), mk("cos(0 * x) + atan2(0, 1)")())
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"].(float64); got != 8 {
		t.Errorf("Σ cos(0) = %v, want 8", got)
	}
}

// TestFormulaTrig_CompileParity: the new names' compile verdicts agree
// at predict and at the run — valid calls are clean on both sides; a
// bare pi (a function used as a number), a misspelt or an unknown name
// are PULSE_FORMULA_COMPILE_ERROR on both with the same message and
// details.
func TestFormulaTrig_CompileParity(t *testing.T) {
	t.Parallel()

	p, data := formulaCompilePulse(t)
	for _, c := range []struct {
		name string
		body string
		bad  bool
	}{
		{"fourier", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"sin(2 * pi() * qty / 52) + cos(2 * pi() * qty / 52)"}]}`, false},
		{"tan atan2 e", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"tan(score / 1000) + atan2(score, qty + 1) * e()"}]}`, false},
		{"cosine filter", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"cos(score) > 0"}]}`, false},
		{"total argument", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"sin(qty) / total(abs(sin(qty)) + 1)"}]}`, false},
		{"bare pi", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty * pi"}]}`, true},
		{"unknown asin", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"asin(score)"}]}`, true},
		{"unknown sinh in a filter", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"sinh(score) > 0"}]}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := formulaCompileBody(c.body)
			assertFormulaCompileParity(t, p, data, func() *types.Request { return formulaCompileRequest(t, body) }, c.bad)
		})
	}
}

// TestExtensions_ExprFunctionReservedTrigNames: an embedder
// ExprFunction named sin, cos, tan, atan2, pi or e is refused at
// pulse.New as reserved (named explicitly, so dropping one from the
// reservation cannot pass by shrinking exprenv.Names()); the inverse
// trig names Pulse does not define stay free.
func TestExtensions_ExprFunctionReservedTrigNames(t *testing.T) {
	t.Parallel()

	fn := func(x float64) float64 { return x }
	for _, name := range []string{"sin", "cos", "tan", "atan2", "pi", "e"} {
		t.Run(name, func(t *testing.T) {
			_, err := pulse.New(newTestOptions(pulse.Extensions{
				ExprFunctions: []pulse.ExprFunction{{Name: name, Fn: fn}},
			}))
			assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_RESERVED)
		})
	}
	for _, name := range []string{"asin", "acos", "atan", "sinh", "tau"} {
		if _, err := pulse.New(newTestOptions(pulse.Extensions{
			ExprFunctions: []pulse.ExprFunction{{Name: name, Fn: fn}},
		})); err != nil {
			t.Errorf("embedder %q: %v, want accepted", name, err)
		}
	}
}
