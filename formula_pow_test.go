package pulse_test

import (
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The power operator on detmath (U58): expr-lang's `**` / `^` is
// rewritten into the detmath-backed builtin (internal/exprenv pow.go)
// at every query-expression site, so `x ** y` gives pow(x, y)'s bits in
// ATTR_FORMULA, FILTER_EXPRESSION, total() arguments and a regression
// holdout, and predict judges the same compile the runtime does.

// powCohort writes x (f64, nullable: null on every 9th row) and y (f64)
// and returns the instance and the cohort's bytes.
func powCohort(t *testing.T) (*pulse.Pulse, []byte) {
	t.Helper()
	fields := []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Power-operator test column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "pw.pulse", encoding.Schema{Fields: fields}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := range 200 {
		var x any = 0.5 + float64(r)*0.173
		if r%9 == 4 {
			x = nil
		}
		if err := b.Append(pulse.CohortRow{x, -3 + float64(r%37)*0.211}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(fsys, "pw.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

// TestFormulaPow_OperatorEqualsPowBuiltin: per row, `x ** y` and
// `x ^ y` are bit-equal to pow(x, y) (counted mismatches are 0), and a
// total() argument and a filter over the operator agree with pow()'s
// to the bit; predict accepts every request.
func TestFormulaPow_OperatorEqualsPowBuiltin(t *testing.T) {
	t.Parallel()

	p, data := powCohort(t)
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "pw.pulse"},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "neq", Expression: "x == nil ? 0 : (x ** y == pow(x, y) ? 0 : 1)"},
				{Type: types.ATTR_FORMULA, Label: "neqc", Expression: "x == nil ? 0 : (x ^ y == pow(x, y) ? 0 : 1)"},
				{Type: types.ATTR_FORMULA, Label: "tdiff", Expression: "total((y + 4) ** 1.37) == total(pow(y + 4, 1.37)) ? 0 : 1"},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "neq", Label: "neq"},
				{Type: types.AGG_SUM, Field: "neqc", Label: "neqc"},
				{Type: types.AGG_SUM, Field: "tdiff", Label: "tdiff"},
			},
		}
	}
	if pe := predictCodedError(t, p, data, req()); pe != nil {
		t.Fatalf("predict refused: %+v", pe)
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"neq", "neqc", "tdiff"} {
		if got := toFloat(t, resp.Data[0][k]); got != 0 {
			t.Errorf("%s: %v rows where the operator's bits differ from pow()'s, want 0", k, got)
		}
	}

	// Filters: the operator and pow() keep the same rows; a null x is
	// UNKNOWN under the operator (dropped), as before the rewrite.
	count := func(filter string) float64 {
		t.Helper()
		r := &types.Request{
			Cohort:       &types.Cohort{Filename: "pw.pulse"},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: filter}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}},
		}
		if pe := predictCodedError(t, p, data, r); pe != nil {
			t.Fatalf("predict refused %q: %+v", filter, pe)
		}
		resp, err := p.Process(ctx, r)
		if err != nil {
			t.Fatalf("%q: %v", filter, err)
		}
		return toFloat(t, resp.Data[0]["n"])
	}
	if a, b := count("x ** y > 2"), count("x != nil && pow(x, y) > 2"); a != b || a == 0 {
		t.Errorf("x ** y > 2 kept %v rows, pow() %v", a, b)
	}
	if a := count("x ** 0 == 1"); a != 200-22 {
		t.Errorf("x ** 0 == 1 kept %v rows, want 178 (the 22 null rows are UNKNOWN)", a)
	}
}

// TestFormulaPow_PredictMatchesRuntime: every refusal the operator can
// raise is the same code and message at predict and at run time.
func TestFormulaPow_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, data := powCohort(t)
	for _, c := range []struct {
		name string
		req  func() *types.Request
		want perr.Code
	}{
		{"non-bool filter", func() *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: "pw.pulse"},
				Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x ** 2"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}},
			}
		}, perr.PULSE_FORMULA_COMPILE_ERROR},
		{"string operand", func() *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: "pw.pulse"},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "v", Expression: "y ** 'a'"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
			}
		}, perr.PULSE_FORMULA_COMPILE_ERROR},
		{"string result", func() *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: "pw.pulse"},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "v", Expression: "y ^ 2 + 'a'"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
			}
		}, perr.PULSE_FORMULA_COMPILE_ERROR},
		{"non-bool holdout", func() *types.Request {
			return &types.Request{
				Cohort:      &types.Cohort{Filename: "pw.pulse"},
				Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Holdout: "y ** 2"}},
			}
		}, perr.PULSE_FORMULA_COMPILE_ERROR},
	} {
		t.Run(c.name, func(t *testing.T) {
			pe := predictCodedError(t, p, data, c.req())
			if pe == nil || pe.Code != string(c.want) {
				t.Fatalf("predict: %+v, want %s", pe, c.want)
			}
			_, err := p.Process(context.Background(), c.req())
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.want {
				t.Fatalf("runtime: %v, want %s", err, c.want)
			}
			if ce.Message != pe.Message {
				t.Errorf("message drift:\n predict: %s\n runtime: %s", pe.Message, ce.Message)
			}
		})
	}
}

// TestFormulaPow_HoldoutUsesOperator: a holdout predicate over `**`
// holds out exactly the rows its pow() spelling does.
func TestFormulaPow_HoldoutUsesOperator(t *testing.T) {
	t.Parallel()

	p, data := powCohort(t)
	run := func(holdout string) *types.RegressionHoldout {
		t.Helper()
		req := &types.Request{
			Cohort:      &types.Cohort{Filename: "pw.pulse"},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Holdout: holdout}},
		}
		if pe := predictCodedError(t, p, data, req); pe != nil {
			t.Fatalf("predict refused %q: %+v", holdout, pe)
		}
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Regressions) != 1 || resp.Regressions[0].Holdout == nil {
			t.Fatalf("%q: no holdout result: %+v", holdout, resp.Regressions)
		}
		return resp.Regressions[0].Holdout
	}
	a, b := run("y ** 2 > 4"), run("pow(y, 2) > 4")
	if a.N != b.N || a.N == 0 || math.Float64bits(a.RMSE) != math.Float64bits(b.RMSE) {
		t.Errorf("holdout `**` %+v, pow() %+v", a, b)
	}
}
