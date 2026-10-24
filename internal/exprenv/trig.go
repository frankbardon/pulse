package exprenv

import (
	"math"

	"github.com/frankbardon/pulse/internal/vecmath"
)

// Trigonometry and the two constants (U58).
//
// sin, cos and tan take one argument in radians, atan2(y, x) two (the
// angle of the point (x, y), in [-π, π]); every one runs on
// internal/detmath's portable kernels through internal/vecmath, so a
// call gives the same bits on every architecture and CPU (the `**`
// operator is expr-lang's own and still runs on math.Pow). A
// null argument (nil) answers nil, the convention log1p and hill share
// (calendar.go header); ±Inf and NaN answer NaN, which is null on the
// wire.
//
// The constants are zero-argument CALLS, pi() and e(), not bare
// identifiers. expr-lang has no constants of its own (its stock
// builtins, now() included, are all functions) and neither does this
// environment, and a bare identifier resolves from the per-site Env
// map, where it would collide with a cohort field of the same name and
// would have to be injected at every site that builds an Env (the
// schema prototype, the derived declarations, the overlay host
// figures). A reserved function is one change point, and the
// identifier walks that read a formula's bare names as field
// references (projection, predict's field checks, the filter-precompute
// purity table) need no exception. A field named pi or e keeps binding
// as a bare identifier exactly as before.

// The trigonometry and constant names.
const (
	FuncSin   = "sin"
	FuncCos   = "cos"
	FuncTan   = "tan"
	FuncAtan2 = "atan2"
	FuncPi    = "pi"
	FuncE     = "e"
)

// trigUnary is sin / cos / tan: one numeric argument, nil for nil.
func trigUnary(fn string, k func(float64) float64) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, builtinArityError(fn, 1, len(args))
		}
		if args[0] == nil {
			return nil, nil
		}
		x, err := scalarArg(fn, 0, args[0])
		if err != nil {
			return nil, err
		}
		return k(x), nil
	}
}

// atan2Builtin is atan2(y, x); nil when either argument is nil.
func atan2Builtin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, builtinArityError(FuncAtan2, 2, len(args))
	}
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	y, err := scalarArg(FuncAtan2, 0, args[0])
	if err != nil {
		return nil, err
	}
	x, err := scalarArg(FuncAtan2, 1, args[1])
	if err != nil {
		return nil, err
	}
	return vecmath.Atan2(y, x), nil
}

// constBuiltin is a zero-argument constant: pi(), e().
func constBuiltin(fn string, v float64) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 0 {
			return nil, builtinArityError(fn, 0, len(args))
		}
		return v, nil
	}
}

// trigImpls are the implementations [builtinImpls] adds for the names
// above.
var trigImpls = map[string]exprBuiltinFunc{
	FuncSin:   trigUnary(FuncSin, vecmath.Sin),
	FuncCos:   trigUnary(FuncCos, vecmath.Cos),
	FuncTan:   trigUnary(FuncTan, vecmath.Tan),
	FuncAtan2: atan2Builtin,
	FuncPi:    constBuiltin(FuncPi, math.Pi),
	FuncE:     constBuiltin(FuncE, math.E),
}
