package sem

import (
	"github.com/frankbardon/pulse/optimize"
)

// slsqpBounded runs the shared SLSQP solver (package optimize, a port of
// SciPy 1.18.1's) over simple bounds only — no general constraints —
// with semopy's settings: xl / xu carry NaN for an absent bound, and
// opt.Ftol / opt.MaxIter are SciPy's ftol / maxiter. A run that stops
// short is not an error here: the fit reports it through conv and the
// solver's status message. Only a refused problem (never reached by a
// well-formed model) is returned as err.
func slsqpBounded(f func([]float64) float64, grad func([]float64) []float64, x0, xl, xu []float64, opt Options) (x []float64, fun float64, nit int, conv bool, status string, err error) {
	res, err := optimize.Minimize(f, grad, x0, optimize.MinimizeOptions{
		Lower:   xl,
		Upper:   xu,
		Acc:     opt.Ftol,
		MaxIter: opt.MaxIter,
	})
	if res == nil {
		return nil, 0, 0, false, "", err
	}
	return res.X, res.F, res.Iterations, res.Status == optimize.StatusSuccess, res.Status.String(), nil
}
