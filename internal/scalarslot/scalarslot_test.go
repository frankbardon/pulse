package scalarslot_test

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/types"
)

// testEnv has numeric, string and integer params and one regression fit
// `demand` of units on price.
func testEnv(t *testing.T) scalarslot.Env {
	t.Helper()
	params, cerr := exprenv.NormalizeParams(map[string]any{"tv": 200.0, "share": 0.25, "lags": 4, "tag": "x", "on": true})
	if cerr != nil {
		t.Fatal(cerr)
	}
	fits := model.CatalogFromFits([]types.FitSpec{{
		Name: "demand",
		Request: &types.Request{
			Cohort:      &types.Cohort{Filename: "sales.pulse"},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "units", Predictors: []string{"price"}}},
		},
	}})
	return scalarslot.Env{Params: params, Fits: fits, Extensions: []string{"boost"}}
}

// TestEval_StaticExpressionsResolve: an expression over literals, params
// and the built-ins is evaluated even on a declaration-only compile —
// the value predict reports — and an Integer slot keeps an exact
// integer.
func TestEval_StaticExpressionsResolve(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	cases := []struct {
		src  string
		kind scalarslot.Kind
		want float64
	}{
		{"params.tv", scalarslot.Real, 200},
		{"params.tv * params.share", scalarslot.Real, 50},
		{"2 ^ 3 + 1", scalarslot.Real, 9},
		{"exp(0) + params.lags", scalarslot.Real, 5},
		{"params.on ? params.tv : 0", scalarslot.Real, 200},
		{"params.lags * 2", scalarslot.Integer, 8},
		{"params.tv / 4", scalarslot.Integer, 50},
	}
	for _, c := range cases {
		v, r := scalarslot.Eval(c.src, c.kind, env, env.Declared(), false)
		if r != nil {
			t.Fatalf("%q refused: %v", c.src, r)
		}
		if !v.Resolved || v.Number != c.want || v.Reads != nil {
			t.Errorf("%q = %+v, want resolved %v with no reads", c.src, v, c.want)
		}
	}
}

// TestEval_BoundCallsAreNotStaticallyResolved: an expression calling a
// request-bound function compiles and is checked, but on a
// declaration-only compile it is not run (Resolved false, the functions
// under Reads); the runtime's run Eval evaluates it.
func TestEval_BoundCallsAreNotStaticallyResolved(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	for src, reads := range map[string][]string{
		`coef("demand", "price") * params.tv`:                   {"coef"},
		`boost(params.tv) + coef("demand", "price")`:            {"boost", "coef"},
		`sweep_axis("demand", "decay")`:                         nil, // refused below: demand is not sweep-bound
		`params.on ? 1 : range_label("bands", 2) == "" ? 1 : 0`: {"range_label"},
	} {
		v, r := scalarslot.Eval(src, scalarslot.Real, env, env.Declared(), false)
		if reads == nil {
			if r == nil || r.Coded == nil || r.Coded.Code != errors.PULSE_MODEL_TERM_UNKNOWN {
				t.Errorf("%q: %v, want PULSE_MODEL_TERM_UNKNOWN (not sweep-bound)", src, r)
			}
			continue
		}
		if r != nil {
			t.Fatalf("%q refused: %v", src, r)
		}
		if v.Resolved || !reflect.DeepEqual(v.Reads, reads) {
			t.Errorf("%q = %+v, want unresolved reading %v", src, v, reads)
		}
	}

	bound := exprenv.Bound{
		Coef:       func(...any) (any, error) { return -2.5, nil },
		Extensions: []exprenv.NamedFunc{{Name: "boost", Fn: func(args ...any) (any, error) { return float64(args[0].(int64)) * 2, nil }}},
	}
	v, r := scalarslot.Eval(`boost(params.tv) + coef("demand", "price")`, scalarslot.Real, env, bound, true)
	if r != nil || !v.Resolved || v.Number != 397.5 || !reflect.DeepEqual(v.Reads, []string{"boost", "coef"}) {
		t.Fatalf("run Eval = %+v, %v; want 397.5 resolved", v, r)
	}
}

// TestEval_Refusals: every slot-neutral reason, and the shared codes
// kept as Coded.
func TestEval_Refusals(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	cases := []struct {
		src    string
		kind   scalarslot.Kind
		reason string
		code   errors.Code
	}{
		{"params.tv * (", scalarslot.Real, scalarslot.ReasonInvalid, ""},
		{"spend * 2", scalarslot.Real, scalarslot.ReasonInvalid, ""},
		{"saturate(1)", scalarslot.Real, scalarslot.ReasonInvalid, ""},
		{"params.tag", scalarslot.Real, scalarslot.ReasonNotNumeric, ""},
		{"params.tv > 1", scalarslot.Real, scalarslot.ReasonNotNumeric, ""},
		{"params.tv / 0", scalarslot.Real, scalarslot.ReasonNotFinite, ""},
		{"log(-1)", scalarslot.Real, scalarslot.ReasonNotFinite, ""},
		{"params.share * 2", scalarslot.Integer, scalarslot.ReasonNotInteger, ""},
		{"2 ^ 60", scalarslot.Integer, scalarslot.ReasonNotInteger, ""},
		{"params.cap", scalarslot.Real, "", errors.PULSE_PARAMS_UNDECLARED},
		{`coef("supply", "price")`, scalarslot.Real, "", errors.PULSE_MODEL_UNKNOWN},
		{`coef("demand", "cost")`, scalarslot.Real, "", errors.PULSE_MODEL_TERM_UNKNOWN},
	}
	for _, c := range cases {
		_, r := scalarslot.Eval(c.src, c.kind, env, env.Declared(), false)
		if r == nil {
			t.Errorf("%q: not refused", c.src)
			continue
		}
		if c.code != "" {
			if r.Coded == nil || r.Coded.Code != c.code {
				t.Errorf("%q: %v, want %s", c.src, r, c.code)
			}
			continue
		}
		if r.Coded != nil || r.Reason != c.reason {
			t.Errorf("%q: %v, want reason %s", c.src, r, c.reason)
		}
	}
}

// TestRun_KeepsAFailedFitsCause: a bound function's coded error — a
// failed fit's, Cause model.ErrFitFailed — comes back unchanged, so the
// caller can fail its slot alone; an uncoded fault is PROCESSING_RUNTIME.
func TestRun_KeepsAFailedFitsCause(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	failed := model.FailedFitRefusal("demand", &types.SlotError{Code: string(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA), Message: "no rows"})
	bound := exprenv.Bound{Coef: func(...any) (any, error) { return nil, failed }}
	_, r := scalarslot.Eval(`coef("demand", "price")`, scalarslot.Real, env, bound, true)
	if r == nil || r.Coded == nil || !stderrors.Is(r.Coded, model.ErrFitFailed) || r.Coded.Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA {
		t.Fatalf("failed-fit read: %v", r)
	}
	bound.Coef = func(...any) (any, error) { return nil, stderrors.New("boom") }
	_, r = scalarslot.Eval(`coef("demand", "price")`, scalarslot.Real, env, bound, true)
	if r == nil || r.Coded == nil || r.Coded.Code != errors.PROCESSING_RUNTIME {
		t.Fatalf("uncoded fault: %v", r)
	}
	bound.Coef = func(...any) (any, error) { return math.Inf(-1), nil }
	_, r = scalarslot.Eval(`coef("demand", "price")`, scalarslot.Real, env, bound, true)
	if r == nil || r.Reason != scalarslot.ReasonNotFinite || r.Details["value"] != "-Inf" {
		t.Fatalf("non-finite coef: %v", r)
	}
}

// TestEval_SweepAxisString: a sweep_axis() value that is a string (a
// string axis) is not a number at run time.
func TestEval_SweepAxisString(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	env.Fits = model.CatalogFromFits([]types.FitSpec{{
		Name:      "best",
		FromSweep: &types.FitFromSweep{Regression: "m"},
		Request:   &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "units", Predictors: []string{"tv"}}}},
	}})
	bound := exprenv.Bound{SweepAxis: func(...any) (any, error) { return "geometric", nil }}
	_, r := scalarslot.Eval(`sweep_axis("best", "kernel")`, scalarslot.Real, env, bound, true)
	if r == nil || r.Reason != scalarslot.ReasonNotNumeric || r.Details["result_type"] != "string" {
		t.Fatalf("string axis: %v", r)
	}
	bound.SweepAxis = func(...any) (any, error) { return 0.6, nil }
	v, r := scalarslot.Eval(`sweep_axis("best", "decay") * params.tv`, scalarslot.Real, env, bound, true)
	if r != nil || !v.Resolved || v.Number != 120 || !reflect.DeepEqual(v.Reads, []string{"sweep_axis"}) {
		t.Fatalf("numeric axis: %+v %v", v, r)
	}
}

// TestInlineNumber: an inline model number reads literals, params and
// the pure built-ins only — a request-bound function (coef(),
// sweep_axis(), lookup()) does not compile, so one model never reads
// another — and keeps the scalar slot's reasons.
func TestInlineNumber(t *testing.T) {
	t.Parallel()

	params := types.Params{"a": json.Number("120"), "share": json.Number("0.25")}
	if v, r := scalarslot.InlineNumber(params, "params.a * (1 + params.share)", false); r != nil || v != 150 {
		t.Errorf("params expression = %v, %+v; want 150", v, r)
	}
	if v, r := scalarslot.InlineNumber(params, "exp(0)", true); r != nil || v != 1 {
		t.Errorf("built-in = %v, %+v", v, r)
	}
	for _, c := range []struct {
		src     string
		integer bool
		reason  string
		code    errors.Code
	}{
		{`coef("m", "x")`, false, scalarslot.ReasonInvalid, ""},
		{`sweep_axis("m", "decay")`, false, scalarslot.ReasonInvalid, ""},
		{`lookup("t", "k")`, false, scalarslot.ReasonInvalid, ""},
		{"1 / 0", false, scalarslot.ReasonNotFinite, ""},
		{"params.share", true, scalarslot.ReasonNotInteger, ""},
		{`"text"`, false, scalarslot.ReasonNotNumeric, ""},
		{"params.missing", false, "", errors.PULSE_PARAMS_UNDECLARED},
	} {
		_, r := scalarslot.InlineNumber(params, c.src, c.integer)
		switch {
		case r == nil:
			t.Errorf("%s: resolved", c.src)
		case c.code != "" && (r.Coded == nil || r.Coded.Code != c.code):
			t.Errorf("%s: %+v, want %s", c.src, r, c.code)
		case c.code == "" && r.Reason != c.reason:
			t.Errorf("%s: reason %q (%s), want %q", c.src, r.Reason, r.What, c.reason)
		}
	}
	if _, r := scalarslot.InlineNumber(types.Params{"bad name": 1}, "1", false); r == nil || r.Coded == nil || r.Coded.Code != errors.PULSE_PARAMS_INVALID {
		t.Errorf("invalid params: %+v", r)
	}
}
