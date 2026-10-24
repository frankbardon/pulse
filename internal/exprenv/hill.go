package exprenv

import (
	"math"

	"github.com/frankbardon/pulse/internal/vecmath"
)

// FuncHill is the Hill saturation curve's reserved name.
const FuncHill = "hill"

// hillBuiltin is hill(x, half_saturation, shape) = x^s / (x^s + k^s): the
// Hill saturation curve media-mix work applies to an adstocked spend
// column. It rises from 0 at x = 0 through 0.5 at x = k toward 1, with s
// setting how sharply.
//
// Null and domain follow the shared conventions (calendar.go header): a
// nil argument answers nil (a null result); an argument outside the
// curve's domain — x < 0, k <= 0, s <= 0, or a NaN anywhere — answers NaN,
// which is null on the wire. It is computed as 1 / (1 + (k/x)^s), the
// same value without the overflow x^s and k^s meet separately: x = 0
// gives (k/0)^s = +Inf and so 0, x = +Inf gives 1. The power is
// vecmath.Pow (internal/detmath's portable port), the one pow() uses.
func hillBuiltin(args ...any) (any, error) {
	if len(args) != 3 {
		return nil, builtinArityError(FuncHill, 3, len(args))
	}
	for _, a := range args {
		if a == nil {
			return nil, nil
		}
	}
	var v [3]float64
	for i, a := range args {
		x, err := scalarArg(FuncHill, i, a)
		if err != nil {
			return nil, err
		}
		v[i] = x
	}
	x, k, s := v[0], v[1], v[2]
	if !(x >= 0) || !(k > 0) || !(s > 0) {
		return math.NaN(), nil
	}
	return 1 / (1 + vecmath.Pow(k/x, s)), nil
}
