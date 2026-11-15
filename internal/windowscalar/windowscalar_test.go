package windowscalar_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/distlagparams"
	"github.com/frankbardon/pulse/internal/ewmaparams"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/internal/windowscalar"
	"github.com/frankbardon/pulse/types"
)

func win(t types.WindowType, params string) *types.Window {
	w := &types.Window{Type: t, Field: "x", OrderBy: []types.OrderKey{{Field: "t"}}, Params: json.RawMessage(params)}
	if windowparams.FrameRequired(t) {
		w.Frame = &types.FrameSpec{Mode: "rows"}
	}
	return w
}

func paramsEnv(t *testing.T, raw map[string]any) scalarslot.Env {
	t.Helper()
	p, cerr := exprenv.NormalizeParams(raw)
	if cerr != nil {
		t.Fatal(cerr)
	}
	return scalarslot.Env{Params: p}
}

// TestResolve_LiteralWindowIsItself: a window with no expression param
// — every literal slot, and every operator without scalar params —
// comes back as the same pointer, so the pass is byte-identical for
// every request that writes numbers.
func TestResolve_LiteralWindowIsItself(t *testing.T) {
	t.Parallel()
	for _, w := range []*types.Window{
		win(types.WIN_DISTRIBUTED_LAG, `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`),
		win(types.WIN_EWMA, `{"alpha": 0.3}`),
		win(types.WIN_LAG, `{"offset": "params.k"}`),
		win(types.WIN_EWMA, ``),
		win(types.WIN_EWMA, `[1]`),
	} {
		res, refs := windowscalar.Resolve(w, scalarslot.Env{}, exprenv.Bound{}, true)
		if res.Window != w || len(refs) != 0 || res.Deferred != nil || res.Unresolved != nil {
			t.Errorf("%s %s: got %+v %v, want the window itself", w.Type, w.Params, res, refs)
		}
	}
}

// TestResolve_ParamsExpressionsMatchLiterals: every scalar param written
// as an expression over params reads exactly as the literal it yields —
// the kernel weights bit for bit, EWMA's alpha through the exact
// decimal (decay 0.7 → the double of 0.3, never 1 − 0.7) — and the
// caller's window is never mutated.
func TestResolve_ParamsExpressionsMatchLiterals(t *testing.T) {
	t.Parallel()
	env := paramsEnv(t, map[string]any{"d": json.Number("0.7"), "k": json.Number("3"), "s": json.Number("1.5"), "c": json.Number("2")})
	cases := []struct {
		typ          types.WindowType
		expr, lit    string
		weightsEqual bool
	}{
		{types.WIN_DISTRIBUTED_LAG, `{"kernel": "geometric", "decay": "params.d", "max_lag": "params.k + 1"}`, `{"kernel": "geometric", "decay": 0.7, "max_lag": 4}`, true},
		{types.WIN_DISTRIBUTED_LAG, `{"kernel": "delayed", "decay": "params.d", "peak": "params.s", "max_lag": 6}`, `{"kernel": "delayed", "decay": 0.7, "peak": 1.5, "max_lag": 6}`, true},
		{types.WIN_DISTRIBUTED_LAG, `{"kernel": "weibull_pdf", "shape": "params.s", "scale": "params.c * 2", "max_lag": 6}`, `{"kernel": "weibull_pdf", "shape": 1.5, "scale": 4, "max_lag": 6}`, true},
		{types.WIN_EWMA, `{"decay": "params.d"}`, `{"decay": 0.7}`, false},
		{types.WIN_EWMA, `{"alpha": "1 - params.d"}`, `{"alpha": 0.30000000000000004}`, false},
	}
	for _, tc := range cases {
		w := win(tc.typ, tc.expr)
		before := string(w.Params)
		res, refs := windowscalar.Resolve(w, env, env.Declared(), false)
		if len(refs) != 0 || res.Deferred != nil || res.Unresolved != nil {
			t.Fatalf("%s: refs %v, res %+v", tc.expr, refs, res)
		}
		if string(w.Params) != before {
			t.Fatalf("%s: the caller's window was mutated to %s", tc.expr, w.Params)
		}
		if tc.typ == types.WIN_DISTRIBUTED_LAG {
			got, r1 := distlagparams.Resolve(res.Window.Params)
			want, r2 := distlagparams.Resolve(json.RawMessage(tc.lit))
			if r1 != nil || r2 != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("%s: kernel %+v %v, literal %+v %v", tc.expr, got, r1, want, r2)
			}
			continue
		}
		got, r1 := ewmaparams.Resolve(res.Window.Params)
		want, r2 := ewmaparams.Resolve(json.RawMessage(tc.lit))
		if r1 != nil || r2 != nil || got != want {
			t.Errorf("%s: alpha %v (%s) %v, literal %v %v", tc.expr, got, res.Window.Params, r1, want, r2)
		}
	}
	// decay 0.7 reads as the literal alpha 0.3, not the subtraction.
	res, _ := windowscalar.Resolve(win(types.WIN_EWMA, `{"decay": "params.d"}`), env, env.Declared(), false)
	if a, _ := ewmaparams.Resolve(res.Window.Params); a != 0.3 {
		t.Errorf("decay params.d=0.7 gave alpha %v, want 0.3 (the exact decimal)", a)
	}
}

// TestResolve_Refusals: each slot-neutral reason under
// PULSE_WINDOW_INVALID details.reason param_<reason>, max_lag's exact
// integer rule, and a shared code located at the param — keeping its
// code — with the runtime's "TYPE: " and predict's "window[i] (TYPE): "
// prefixes on one rule sentence.
func TestResolve_Refusals(t *testing.T) {
	t.Parallel()
	env := paramsEnv(t, map[string]any{"k": json.Number("2.5"), "label": "a"})
	cases := []struct {
		params string
		code   errors.Code
		reason string
	}{
		{`{"kernel": "geometric", "decay": "0.5 +", "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, windowscalar.ReasonParamInvalid},
		{`{"kernel": "geometric", "decay": "x * 2", "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, windowscalar.ReasonParamInvalid},
		{`{"kernel": "geometric", "decay": "params.label", "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, windowscalar.ReasonParamNotNumeric},
		{`{"kernel": "weibull_cdf", "shape": 2, "scale": "params.label", "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, windowscalar.ReasonParamNotNumeric},
		{`{"kernel": "geometric", "decay": "1.0 / 0", "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, windowscalar.ReasonParamNotFinite},
		{`{"kernel": "geometric", "decay": 0.5, "max_lag": "params.k"}`, errors.PULSE_WINDOW_INVALID, windowscalar.ReasonParamNotInteger},
		{`{"kernel": "geometric", "decay": "params.nope", "max_lag": 4}`, errors.PULSE_PARAMS_UNDECLARED, ""},
		{`{"kernel": "geometric", "decay": "sweep_axis(\"best\", \"tv\")", "max_lag": 4}`, errors.PULSE_MODEL_UNKNOWN, ""},
	}
	for _, tc := range cases {
		res, refs := windowscalar.Resolve(win(types.WIN_DISTRIBUTED_LAG, tc.params), env, env.Declared(), true)
		if len(refs) != 1 {
			t.Fatalf("%s: refusals %v", tc.params, refs)
		}
		rt, pr := refs[0].Coded(), refs[0].At(3)
		if rt.Code != tc.code || pr.Code != tc.code {
			t.Errorf("%s: codes %s / %s, want %s", tc.params, rt.Code, pr.Code, tc.code)
		}
		if tc.reason != "" && rt.Details["reason"] != tc.reason {
			t.Errorf("%s: reason %v, want %s", tc.params, rt.Details["reason"], tc.reason)
		}
		rule := refs[0].Message
		if rt.Message != "WIN_DISTRIBUTED_LAG: "+rule || pr.Message != "window[3] (WIN_DISTRIBUTED_LAG): "+rule {
			t.Errorf("%s: messages %q / %q", tc.params, rt.Message, pr.Message)
		}
		if pr.Details["window_index"] != 3 || rt.Details["param"] == nil {
			t.Errorf("%s: details %v / %v", tc.params, rt.Details, pr.Details)
		}
		// The refused key is deferred: the reading still judges the
		// rest of the slot without reading its stand-in.
		if len(res.Deferred) != 1 || windowparams.CheckDeferred(res.Window, res.Deferred) != nil {
			t.Errorf("%s: deferred %v, reading %v", tc.params, res.Deferred, windowparams.CheckDeferred(res.Window, res.Deferred))
		}
	}
}

// TestResolve_StaticDefersBoundFunctions: on a static judgement a param
// calling sweep_axis() / coef() is checked but not run — Unresolved,
// Deferred, the reading skipping its range and the kernel weights —
// while a runtime judgement runs the bound function and the reading
// judges the value it returns.
func TestResolve_StaticDefersBoundFunctions(t *testing.T) {
	t.Parallel()
	w := win(types.WIN_DISTRIBUTED_LAG, `{"kernel": "weibull_cdf", "shape": "coef(\"m\", \"x\")", "scale": "params.k", "max_lag": "round(abs(coef(\"m\", \"x\"))) + 1"}`)
	env := paramsEnv(t, map[string]any{"k": json.Number("5")})
	env.Fits = model.CatalogFromFits([]types.FitSpec{{Name: "m", Request: &types.Request{
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}}}}})
	res, refs := windowscalar.Resolve(w, env, env.Declared(), false)
	if len(refs) != 0 {
		t.Fatalf("static refusals %v", refs)
	}
	want := []windowscalar.Unresolved{
		{Key: "shape", Expression: `coef("m", "x")`, Reads: []string{"coef"}},
		{Key: "max_lag", Expression: `round(abs(coef("m", "x"))) + 1`, Reads: []string{"coef"}},
	}
	if !reflect.DeepEqual(res.Unresolved, want) || !res.Deferred["shape"] || !res.Deferred["max_lag"] || res.Deferred["scale"] {
		t.Fatalf("unresolved %+v deferred %v", res.Unresolved, res.Deferred)
	}
	if refs := windowparams.CheckDeferred(res.Window, res.Deferred); refs != nil {
		t.Fatalf("the reading judged a deferred shape: %v", refs[0].Message)
	}
	// The same check with nothing deferred reads the stand-ins (-1).
	if windowparams.Check(res.Window) == nil {
		t.Fatal("vacuous: the stand-in shape 0 is not refused without deferral")
	}

	for _, tc := range []struct {
		coef   float64
		refuse bool
	}{{1.5, false}, {-1, true}} {
		bound := env.Declared()
		bound.Coef = func(...any) (any, error) { return tc.coef, nil }
		res, refs := windowscalar.Resolve(w, env, bound, true)
		if len(refs) != 0 || res.Unresolved != nil || res.Deferred != nil {
			t.Fatalf("runtime coef %v: %v %+v", tc.coef, refs, res)
		}
		if got := windowparams.Check(res.Window) != nil; got != tc.refuse {
			t.Errorf("runtime coef %v: refused %v, want %v (%s)", tc.coef, got, tc.refuse, res.Window.Params)
		}
	}
}

// TestLiteralParamsStayLiteral: WIN_LAG / WIN_LEAD offset and
// WIN_PCT_CHANGE / WIN_DELTA periods are not scalar slots — an
// expression string there is the reading's malformed-params refusal,
// as before.
func TestLiteralParamsStayLiteral(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		typ    types.WindowType
		params string
	}{
		{types.WIN_LAG, `{"offset": "params.k"}`},
		{types.WIN_LEAD, `{"offset": "1 + 1"}`},
		{types.WIN_PCT_CHANGE, `{"periods": "params.k"}`},
		{types.WIN_DELTA, `{"periods": "2"}`},
	} {
		if len(windowscalar.Params(tc.typ)) != 0 {
			t.Errorf("%s declares scalar params", tc.typ)
		}
		w := &types.Window{Type: tc.typ, Field: "x", OrderBy: []types.OrderKey{{Field: "t"}}, Params: json.RawMessage(tc.params)}
		res, _ := windowscalar.Resolve(w, scalarslot.Env{}, exprenv.Bound{}, true)
		ref := windowparams.First(res.Window)
		if ref == nil || len(ref.Message) < 16 || ref.Message[:16] != "malformed params" {
			t.Errorf("%s %s: refusal %v, want malformed params", tc.typ, tc.params, ref)
		}
	}
}

// TestResolve_StaticDefersEWMA: an EWMA alpha or decay calling a
// request-bound function is deferred on a static judgement — the
// exactly-one rule still runs, the range waits for the runtime.
func TestResolve_StaticDefersEWMA(t *testing.T) {
	t.Parallel()
	env := scalarslot.Env{Fits: model.CatalogFromFits([]types.FitSpec{{Name: "m", Request: &types.Request{
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}}}}})}
	for _, tc := range []struct {
		params string
		refuse bool
	}{
		{`{"alpha": "coef(\"m\", \"x\")"}`, false},
		{`{"decay": "coef(\"m\", \"x\")"}`, false},
		{`{"alpha": "coef(\"m\", \"x\")", "decay": 0.5}`, true},
	} {
		res, refs := windowscalar.Resolve(win(types.WIN_EWMA, tc.params), env, env.Declared(), false)
		if len(refs) != 0 || len(res.Unresolved) != 1 {
			t.Fatalf("%s: %v %+v", tc.params, refs, res)
		}
		if got := windowparams.CheckDeferred(res.Window, res.Deferred) != nil; got != tc.refuse {
			t.Errorf("%s: refused %v, want %v", tc.params, got, tc.refuse)
		}
	}
}
