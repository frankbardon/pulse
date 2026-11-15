package optgrad

import (
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// TestHill_PartialsAtTheEdges: at x = 0 hill's partials are their
// limits — ∂/∂x 0 for s > 1, 1/k at s = 1, and below s = 1 a vertical
// tangent the dual walk refuses as ErrEval (the solver takes that point
// by finite differences) — ∂/∂k and ∂/∂s 0; outside the domain the
// value is NaN and so is the gradient.
func TestHill_PartialsAtTheEdges(t *testing.T) {
	t.Parallel()

	vars := []string{"x", "k", "s"}
	p, u := Compile(compile(t, "hill(x, k, s)", vars, exprenv.Bound{}).Node(), vars, Options{})
	if p == nil {
		t.Fatalf("unsupported %v", u)
	}
	for _, tc := range []struct {
		s    float64
		want []float64
	}{{2, []float64{0, 0, 0}}, {1, []float64{0.25, 0, 0}}} {
		v, g, err := p.Eval([]float64{0, 4, tc.s})
		if err != nil || v != 0 || !reflect.DeepEqual(g, tc.want) {
			t.Errorf("s=%v at 0: %v %v %v, want 0 %v", tc.s, v, g, err, tc.want)
		}
	}
	if _, _, err := p.Eval([]float64{0, 4, 0.5}); err != ErrEval {
		t.Errorf("s=0.5 at 0: %v, want ErrEval (vertical tangent)", err)
	}
	v, g, err := p.Eval([]float64{-1, 4, 2})
	if err != nil || !math.IsNaN(v) || !math.IsNaN(g[0]) {
		t.Errorf("outside the domain: %v %v %v", v, g, err)
	}
}

// TestBoundRules: over a bound, coef / sweep_axis / result are
// constants (their string arguments never move), response's spend
// partial is the bound slope, an embedder function differentiates
// through its Derivative only when named differentiable, one without
// stays unsupported — even over a fit reader it shadows — and a
// reader the bound does not declare gets no rule.
func TestBoundRules(t *testing.T) {
	t.Parallel()

	vars := []string{"x"}
	fixed := func(v float64) func(args ...any) (any, error) {
		return func(args ...any) (any, error) { return v, nil }
	}
	var slopeArgs []any
	bound := exprenv.Bound{
		Coef: fixed(2), SweepAxis: fixed(3), Result: fixed(5),
		Response: func(args ...any) (any, error) { s := args[2].(float64); return float64(s * s), nil },
		ResponseSlope: func(args ...any) (float64, error) {
			slopeArgs = args
			return float64(2 * args[2].(float64)), nil
		},
		Extensions: []exprenv.NamedFunc{
			{Name: "cube", Fn: func(x float64) float64 { return float64(float64(x*x) * x) },
				Derivative: func(args ...any) ([]float64, error) {
					x := args[0].(float64)
					return []float64{float64(3 * float64(x*x))}, nil
				}},
			{Name: "plain", Fn: func(x float64) float64 { return x }},
		},
	}
	rules := BoundRules(bound, []string{"cube"})
	src := `coef("m", "t") * x + sweep_axis("m", "a") + result("s", "n") * x + response("m", "tv", x * 2) + cube(x)`
	p, u := Compile(compile(t, src, vars, bound).Node(), vars, Options{Rules: rules})
	if p == nil {
		t.Fatalf("unsupported %v", u)
	}
	// 2x + 3 + 5x + (2x)² + x³ at 1.5: 3 + 3 + 7.5 + 9 + 3.375; ∂ = 2 + 5 + 8x + 3x².
	v, g, err := p.Eval([]float64{1.5})
	if err != nil || v != 25.875 || g[0] != 2+5+12+6.75 {
		t.Fatalf("value %v gradient %v (%v)", v, g, err)
	}
	if !reflect.DeepEqual(slopeArgs, []any{"m", "tv", 3.0}) {
		t.Fatalf("slope read %v", slopeArgs)
	}
	if u := Unsupported(compile(t, "plain(x) + cube(x)", vars, bound).Node(), vars, Options{Rules: rules}); !reflect.DeepEqual(u, []string{"plain"}) {
		t.Fatalf("no Derivative: unsupported %v, want [plain]", u)
	}
	shadow := exprenv.Bound{Coef: fixed(2), Extensions: []exprenv.NamedFunc{{Name: "coef", Fn: fixed(9)}}}
	if r := BoundRules(shadow, nil); r[exprenv.CoefFunc].Impl != nil || len(r) != 0 {
		t.Fatalf("a shadowing function without Derivative kept coef's rule: %v", r)
	}
	if r := BoundRules(exprenv.Bound{Coef: fixed(2)}, nil); len(r) != 1 {
		t.Fatalf("undeclared readers got rules: %v", r)
	}
}

// TestBoundRules_DerivativeFaults: an embedder Derivative's error and a
// partial count that does not match the call abort the gradient with a
// PROCESSING_RUNTIME fault naming the function — never ErrEval, which
// would let the VM's clean value hide it.
func TestBoundRules_DerivativeFaults(t *testing.T) {
	t.Parallel()

	vars := []string{"x"}
	for name, hook := range map[string]func(args ...any) ([]float64, error){
		"error": func(args ...any) ([]float64, error) { return nil, stderrors.New("boom") },
		"count": func(args ...any) ([]float64, error) { return []float64{1, 2}, nil },
	} {
		bound := exprenv.Bound{Extensions: []exprenv.NamedFunc{{Name: "f", Fn: func(x float64) float64 { return x }, Derivative: hook}}}
		p, _ := Compile(compile(t, "f(x) * 2", vars, bound).Node(), vars, Options{Rules: BoundRules(bound, []string{"f"})})
		_, _, err := p.Eval([]float64{1})
		var ce *errors.CodedError
		if err == nil || stderrors.Is(err, ErrEval) || !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_RUNTIME || ce.Details["function"] != "f" {
			t.Errorf("%s: %v, want PROCESSING_RUNTIME naming f", name, err)
		}
	}
}
