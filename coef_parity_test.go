package pulse

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// coef_parity_test.go: every refusal predict can judge for coef(fit,
// term[, stat]) in ATTR_FORMULA and FILTER_EXPRESSION, and for an
// ATTR_REG_PREDICT params.bound, is reported by PredictCompose exactly as
// Compose and ComposeParallel return it (code, message, details); a
// computed coef() argument fails at runtime with the same error predict
// gives the literal; and the reads resolve the fit's figures exactly.

func coefFormulaSlot(expr string) *Request {
	return scoringSlot(fitsFacadeCohort, &types.Attribute{Type: types.ATTR_FORMULA, Label: "c", Expression: expr})
}

func coefFilterSlot(expr string) *Request {
	s := scoringSlot(fitsFacadeCohort)
	s.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
	return s
}

func TestCoef_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	p, fsys := scoringParityPulse(t)
	cases := []struct {
		name string
		slot *Request
		code perr.Code
	}{
		{"formula: unknown fit", coefFormulaSlot(`coef("nope", "x")`), perr.PULSE_MODEL_UNKNOWN},
		{"formula: unknown term", coefFormulaSlot(`coef("m", "week") * x`), perr.PULSE_MODEL_TERM_UNKNOWN},
		{"formula: unknown stat", coefFormulaSlot(`coef("m", "x", "median")`), perr.PULSE_MODEL_STAT_UNKNOWN},
		{"filter: unknown fit", coefFilterSlot(`x > coef("nope", "x")`), perr.PULSE_MODEL_UNKNOWN},
		{"filter: unknown term", coefFilterSlot(`x > coef("m", "(slope)")`), perr.PULSE_MODEL_TERM_UNKNOWN},
		{"filter: unknown stat", coefFilterSlot(`x > coef("m", "x", "p")`), perr.PULSE_MODEL_STAT_UNKNOWN},
		{"bound over the whole model", scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m","bound":"lower"}`)), perr.PULSE_MODEL_SCORE_INVALID},
		{"bound on the intercept", scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m","terms":["(intercept)"],"bound":"upper"}`)), perr.PULSE_MODEL_SCORE_INVALID},
		{"unknown bound", scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m","terms":["x"],"bound":"mid"}`)), perr.PULSE_MODEL_SCORE_INVALID},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: facadeFitRequest("0", "90")}}, Requests: []*Request{tc.slot}}
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if perr.Code(pe.Code) != tc.code || pe.Details["request"] != 0 {
				t.Fatalf("predict = %s %v, want %s located at request 0", pe.Code, pe.Details, tc.code)
			}
			runs := map[string]func() error{
				"Compose": func() error { _, err := p.Compose(ctx, c); return err },
				"ComposeParallel": func() error {
					_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
					return err
				},
			}
			for name, run := range runs {
				var ce *perr.CodedError
				if err := run(); !stderrors.As(err, &ce) {
					t.Fatalf("%s: uncoded %v", name, err)
				}
				if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
					t.Errorf("%s: runtime %s %q %v\n predict %s %q %v", name, ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
				}
			}
		})
	}

	// A computed argument is beyond predict; the runtime refuses it with
	// the error predict gives the literal call (minus the slot locator).
	t.Run("computed argument", func(t *testing.T) {
		fits := []types.FitSpec{{Name: "m", Request: facadeFitRequest("0", "90")}}
		literal := &ComposedRequest{Fits: fits, Requests: []*Request{coefFormulaSlot(`coef("m", "x", "median")`)}}
		env, err := p.PredictCompose(ctx, literal)
		if err != nil || len(env.Errors) != 1 {
			t.Fatalf("predict literal: %v %+v", err, env.Errors)
		}
		computed := &ComposedRequest{Fits: fits, Requests: []*Request{coefFormulaSlot(`let s = "median"; coef("m", "x", s)`)}}
		if env, _ := p.PredictCompose(ctx, computed); len(env.Errors) != 0 {
			t.Fatalf("predict judged a computed argument: %+v", env.Errors)
		}
		var ce *perr.CodedError
		if _, err := p.Compose(ctx, computed); !stderrors.As(err, &ce) {
			t.Fatalf("uncoded %v", err)
		}
		pe := env.Errors[0]
		want := map[string]any{}
		for k, v := range pe.Details {
			if k != "request" {
				want[k] = v
			}
		}
		got := map[string]any{}
		for k, v := range ce.Details {
			if k != "request" {
				got[k] = v
			}
		}
		if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(got, want) {
			t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
		}
	})

	// Outside a compose nothing is bound: Predict and Process refuse a
	// literal coef() identically.
	t.Run("plain request", func(t *testing.T) {
		req := coefFormulaSlot(`coef("m", "x")`)
		data, err := afero.ReadFile(fsys, fitsFacadeCohort)
		if err != nil {
			t.Fatal(err)
		}
		env, err := p.PredictBytes(ctx, data, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_MODEL_UNKNOWN) {
			t.Fatalf("predict errors = %+v", env.Errors)
		}
		var ce *perr.CodedError
		if _, err := p.Process(ctx, req); !stderrors.As(err, &ce) {
			t.Fatalf("process: uncoded %v", err)
		}
		pe := env.Errors[0]
		if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
			t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
		}
	})
}

// TestCoef_ComposeReadsTheFit: through the facade, coef() in a formula
// equals the fit's reported coefficient and bounds exactly, and a bound
// ATTR_REG_PREDICT column sums to ci × Σx.
func TestCoef_ComposeReadsTheFit(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	slot := scoringSlot(fitsFacadeCohort,
		&types.Attribute{Type: types.ATTR_FORMULA, Label: "c", Expression: `coef("m", "x")`},
		&types.Attribute{Type: types.ATTR_FORMULA, Label: "lo", Expression: `coef("m", "x", "lower")`},
		&types.Attribute{Type: types.ATTR_FORMULA, Label: "hi", Expression: `coef("m", "(intercept)", "upper")`},
	)
	slot.Aggregations = []*types.Aggregation{
		{Type: types.AGG_MAX, Field: "c", Label: "c"},
		{Type: types.AGG_MIN, Field: "lo", Label: "lo"},
		{Type: types.AGG_MAX, Field: "hi", Label: "hi"},
	}
	out, err := p.Compose(context.Background(), &ComposedRequest{
		Fits: []types.FitSpec{{Name: "m", Request: facadeFitRequest("0", "90")}}, Requests: []*Request{slot}})
	if err != nil {
		t.Fatal(err)
	}
	fit, row := out.Fits["m"], out.Responses[0].Data[0]
	if row["c"] != fit.Coefficients["x"] || row["lo"] != fit.CILower["x"] || row["hi"] != fit.CIUpper["(intercept)"] {
		t.Fatalf("coef reads %v %v %v, fit reports %v %v %v",
			row["c"], row["lo"], row["hi"], fit.Coefficients["x"], fit.CILower["x"], fit.CIUpper["(intercept)"])
	}
}
