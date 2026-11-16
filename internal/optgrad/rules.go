package optgrad

import (
	"maps"
	"math"

	exprruntime "github.com/expr-lang/expr/vm/runtime"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/vecmath"
)

// The derivative rules. builtinFuncRules covers the deterministic maths
// set of the expression built-ins (exprenv's MathNames, hill included)
// and the builtins the `**` / `^`
// and float `%` patchers rewrite into; builtinRules covers expr-lang's
// own numeric built-ins. Each partial is computed on the kernels the
// value runs on (vecmath → linalg/detmath), every product feeding an add
// barriered.
//
// Adding a rule: a pure function of its numeric arguments only (a
// function reading request state — coef(), response(), result() — needs
// its value through Rule.Impl bound at the call site, so it comes in
// through Options.Rules), its partials index-aligned with the
// arguments, and an agreement test against central differences
// (TestRules_AgreeWithFiniteDifferences).
var builtinFuncRules = map[string]Rule{
	"sqrt": {Partials: func(a []float64, v float64) []float64 { return []float64{1 / float64(2*v)} }},
	"log":  {Partials: func(a []float64, v float64) []float64 { return []float64{1 / a[0]} }},
	"exp":  {Partials: func(a []float64, v float64) []float64 { return []float64{v} }},
	"log1p": {Partials: func(a []float64, v float64) []float64 {
		return []float64{1 / (1 + a[0])}
	}},
	"pow": {Partials: powPartials},
	exprenv.FuncSin: {Partials: func(a []float64, v float64) []float64 {
		return []float64{vecmath.Cos(a[0])}
	}},
	exprenv.FuncCos: {Partials: func(a []float64, v float64) []float64 {
		return []float64{-vecmath.Sin(a[0])}
	}},
	exprenv.FuncTan: {Partials: func(a []float64, v float64) []float64 {
		return []float64{1 + float64(v*v)}
	}},
	exprenv.FuncAtan2: {Partials: func(a []float64, v float64) []float64 {
		y, x := a[0], a[1]
		r2 := float64(x*x) + float64(y*y)
		return []float64{x / r2, -y / r2}
	}},
	exprenv.FuncHill: {Partials: hillPartials},
	exprenv.FuncPi:   {Partials: func([]float64, float64) []float64 { return nil }},
	exprenv.FuncE:    {Partials: func([]float64, float64) []float64 { return nil }},
	// `**` / `^`: exprPower's body (runtime.ToFloat64, detmath Pow).
	exprenv.PowFuncName:      {Impl: powImpl, Partials: powPartials},
	exprenv.PowCaretFuncName: {Impl: powImpl, Partials: powPartials},
	// Float `%` (math.Mod): a − trunc(a/b)·b.
	exprenv.ModFuncName: {Impl: modImpl, Partials: func(a []float64, v float64) []float64 {
		return []float64{1, -math.Trunc(a[0] / a[1])}
	}},
}

// hillPartials are hill(x, k, s) = h = 1 / (1 + u), u = (k/x)^s:
// with q = u·h² (= h·(1 − h), without the cancellation near h = 1),
// ∂h/∂x = s·q / x, ∂h/∂k = −s·q / k and ∂h/∂s = −q·ln(k/x). At x = 0
// (u = +Inf, h = 0) they are the limits — ∂h/∂x 0 for s > 1, 1/k at
// s = 1 and +Inf below (a vertical tangent: the solver takes that point
// by finite differences), ∂h/∂k and ∂h/∂s 0; at x = +Inf (h = 1) all
// three are 0. Outside the curve's domain the value is NaN and so is
// every partial.
func hillPartials(a []float64, h float64) []float64 {
	x, k, s := a[0], a[1], a[2]
	if math.IsNaN(h) {
		return []float64{math.NaN(), math.NaN(), math.NaN()}
	}
	switch {
	case x == 0:
		dx := 0.0
		switch {
		case s == 1:
			dx = 1 / k
		case s < 1:
			dx = math.Inf(1)
		}
		return []float64{dx, 0, 0}
	case math.IsInf(x, 1):
		return []float64{0, 0, 0}
	}
	u := vecmath.Pow(k/x, s)
	q := float64(float64(u*h) * h)
	return []float64{
		float64(s*q) / x,
		-float64(s*q) / k,
		-float64(q * vecmath.Log(k/x)),
	}
}

// powPartials are ∂(aᵇ)/∂a = b·aᵇ⁻¹ and ∂(aᵇ)/∂b = aᵇ·ln a.
func powPartials(a []float64, v float64) []float64 {
	return []float64{float64(a[1] * vecmath.Pow(a[0], a[1]-1)), float64(v * vecmath.Log(a[0]))}
}

func powImpl(args ...any) (out any, err error) {
	if len(args) != 2 {
		return nil, ErrEval
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, ErrEval
		}
	}()
	return vecmath.Pow(exprruntime.ToFloat64(args[0]), exprruntime.ToFloat64(args[1])), nil
}

func modImpl(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, ErrEval
	}
	a, aok := args[0].(float64)
	b, bok := args[1].(float64)
	if !aok || !bok {
		return nil, ErrEval
	}
	return math.Mod(a, b), nil
}

// builtinRules are expr-lang's numeric built-ins: the partials of
// abs (sign, 0 at 0), the step functions (0), float (1) and min / max
// (1 for the first argument the result equals).
var builtinRules = map[string]func([]float64, float64) []float64{
	"abs": func(a []float64, v float64) []float64 {
		switch {
		case a[0] > 0:
			return []float64{1}
		case a[0] < 0:
			return []float64{-1}
		}
		return []float64{0}
	},
	"ceil":  func([]float64, float64) []float64 { return []float64{0} },
	"floor": func([]float64, float64) []float64 { return []float64{0} },
	"round": func([]float64, float64) []float64 { return []float64{0} },
	"float": func([]float64, float64) []float64 { return []float64{1} },
	"min":   pickPartials,
	"max":   pickPartials,
}

// pickPartials is min / max: the result is one of the arguments, so its
// partial is 1 there (the first that equals it) and 0 elsewhere.
func pickPartials(a []float64, v float64) []float64 {
	out := make([]float64, len(a))
	for i, x := range a {
		if x == v {
			out[i] = 1
			break
		}
	}
	return out
}

// ruleTable is the built-in function rules with opts.Rules over them.
func ruleTable(opts Options) map[string]Rule {
	if len(opts.Rules) == 0 {
		return builtinFuncRules
	}
	out := maps.Clone(builtinFuncRules)
	maps.Copy(out, opts.Rules)
	return out
}

// Rules returns the names the built-in rule table differentiates
// (function rules, then expr-lang built-ins), for documentation and
// tests.
func Rules() (functions, builtins []string) {
	for k := range builtinFuncRules {
		functions = append(functions, k)
	}
	for k := range builtinRules {
		builtins = append(builtins, k)
	}
	return functions, builtins
}
