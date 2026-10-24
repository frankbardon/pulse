package exprenv

import (
	"github.com/expr-lang/expr"
)

// The scalar-maths subset for environments outside the query family
// (U58).
//
// The synth row environment (internal/synth's rowExprOptions) compiles
// `set_expr`, `when` and `constraints[]` over a generated row, not over
// a cohort, so it must not see the query-only bindings: the vector
// family (no vectors in a row), the calendar family (epoch days are
// plain float64 there), set similarity, ref, total(), the decimal
// operators (a decimal128 field binds as float64), coef / lookup or any
// embedder ExprFunction. It does need the same maths, with the same
// implementations and so the same bits, as ATTR_FORMULA: [MathOptions]
// is that one shared constructor, so the two environments cannot drift
// apart in what `exp(x)` or `x % 2` answers.
//
// What it returns:
//
//   - the [MathNames] built-ins, each with the implementation the
//     query environment registers (builtinImpls), on internal/detmath
//     through internal/vecmath (sqrt is math.Sqrt, correctly rounded);
//   - the `%` patcher (mod.go): float `%` with math.Mod semantics,
//     int % int left to expr-lang;
//   - the `**` / `^` patcher (pow.go): the operator on detmath.Pow.
//
// Unlike the query registration, every built-in here carries a TYPED
// signature (float64 arguments, a float64 result), so a wrong argument
// count or a string argument is a COMPILE error, never a run-time one.
// The row environment promises eager refusal — a spec is rejected
// before a row is drawn — and the query side gets the same guarantee
// from its separate static pass (internal/vectors' BuiltinArity, which
// TestMathOptions_ArityMatchesStaticPass in internal/vectors pins
// against these signatures). Every value a row holds is a concrete
// float64, string or map, never nil, so the untyped built-ins'
// null-in-null-out branch is unreachable here and the float64 result
// type is exact.

// mathNames are the built-ins [MathOptions] registers, in [reserved]
// order. Every one is a [reserved] name (TestMathNames_AreReserved).
var mathNames = []string{
	"sqrt", "log", "exp", "pow", "log1p",
	"hill",
	FuncSin, FuncCos, FuncTan, FuncAtan2, FuncPi, FuncE,
}

// mathSignatures is each built-in's typed signature; a name absent here
// takes one float64.
var mathSignatures = map[string]any{
	"pow":     new(func(float64, float64) float64),
	"hill":    new(func(float64, float64, float64) float64),
	FuncAtan2: new(func(float64, float64) float64),
	FuncPi:    new(func() float64),
	FuncE:     new(func() float64),
}

// MathNames returns a copy of the built-in names [MathOptions]
// registers, in declaration order.
func MathNames() []string {
	out := make([]string, len(mathNames))
	copy(out, mathNames)
	return out
}

// MathOptions returns the scalar-maths built-ins and the `%` and
// `**` / `^` patchers — and nothing else (see the file comment). The
// caller supplies its own expr.Env.
func MathOptions() []expr.Option {
	opts := make([]expr.Option, 0, len(mathNames)+5)
	for _, name := range mathNames {
		sig, ok := mathSignatures[name]
		if !ok {
			sig = new(func(float64) float64)
		}
		opts = append(opts, expr.Function(name, builtinImpls[name], sig))
	}
	opts = append(opts, exprModOptions()...)
	opts = append(opts, exprPowOptions()...)
	return opts
}
