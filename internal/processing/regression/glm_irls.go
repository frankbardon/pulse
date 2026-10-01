package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"gonum.org/v1/gonum/mat"
)

// irlsFit runs Iteratively Reweighted Least Squares to convergence on
// the supplied design (row-major xRows of shape n × (p+1), intercept
// column at index 0) and target yVec under the given GLM family.
//
// Returns the converged β vector (length p+1), the iteration count that
// produced it, and any blocking error. Same recurrences as
// glmEngine.FitBuffered but without the SE / deviance bookkeeping —
// modifier drivers (resample, selection) reconstruct those quantities
// from β themselves.
//
// Errors mirror the inline IRLS loop's contract:
//   - PROCESSING_REGRESSION_RANK_DEFICIENT: weighted normal equations
//     not positive-definite or Cholesky solve failure.
//   - PROCESSING_REGRESSION_NO_CONVERGE: did not reach the relative-
//     change tolerance within maxIters.
//
// Why we don't fold glmEngine.FitBuffered into a call to this helper
// today: the engine needs the final XᵀWX inverse for std errors, and
// reconstructing that here would round-trip the same Cholesky twice on
// the unmodified spec path. Keeping the engine's loop separate avoids
// that hot-path cost. Modifier drivers refit dozens to hundreds of
// times each — they call irlsFit directly.
func irlsFit(xRows, yVec []float64, p int, fam glmFamily, maxIters int, tol float64) ([]float64, int, error) {
	n := len(yVec)
	if maxIters <= 0 {
		maxIters = defaultGLMMaxIters
	}
	if tol <= 0 {
		tol = defaultGLMTol
	}

	yBar := mean(yVec)
	mu0 := fam.SafeMean(yBar)
	eta0 := fam.LinkFunc(mu0)
	beta := make([]float64, p+1)
	beta[0] = eta0
	eta := make([]float64, n)
	for i := range eta {
		eta[i] = eta0
	}

	w := make([]float64, n)
	z := make([]float64, n)
	mu := make([]float64, n)
	dmu := make([]float64, n)
	xtwx := make([]float64, (p+1)*(p+1))
	xtwz := make([]float64, p+1)
	convergedIters := 0

	for iter := 0; iter < maxIters; iter++ {
		for i := 0; i < n; i++ {
			mu[i] = fam.InverseLink(eta[i])
			dmu[i] = fam.DmuDeta(eta[i])
			v := fam.Variance(mu[i])
			if v < 1e-300 {
				v = 1e-300
			}
			w[i] = dmu[i] * dmu[i] / v
			if math.Abs(dmu[i]) < 1e-300 {
				z[i] = eta[i]
			} else {
				z[i] = eta[i] + (yVec[i]-mu[i])/dmu[i]
			}
		}

		// Zero accumulators.
		for k := range xtwx {
			xtwx[k] = 0
		}
		for k := range xtwz {
			xtwz[k] = 0
		}

		for i := 0; i < n; i++ {
			wi := w[i]
			zi := z[i]
			xi := xRows[i*(p+1) : (i+1)*(p+1)]
			for a := 0; a < p+1; a++ {
				wa := wi * xi[a]
				xtwz[a] += wa * zi
				row := a * (p + 1)
				for b := a; b < p+1; b++ {
					xtwx[row+b] += wa * xi[b]
				}
			}
		}
		// Mirror upper into lower.
		for a := 0; a < p+1; a++ {
			for b := a + 1; b < p+1; b++ {
				xtwx[b*(p+1)+a] = xtwx[a*(p+1)+b]
			}
		}

		sym := mat.NewSymDense(p+1, append([]float64(nil), xtwx...))
		var chol mat.Cholesky
		if ok := chol.Factorize(sym); !ok {
			return nil, convergedIters, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"REG_GLM refit: weighted normal equations not positive-definite",
				map[string]any{"n": n, "p": p, "iter": iter, "family": fam.Name, "link": fam.Link},
			)
		}
		rhs := mat.NewVecDense(p+1, append([]float64(nil), xtwz...))
		var betaNew mat.VecDense
		if err := chol.SolveVecTo(&betaNew, rhs); err != nil {
			return nil, convergedIters, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"REG_GLM refit: Cholesky solve failed inside IRLS",
				map[string]any{"n": n, "p": p, "iter": iter, "gonum_error": err.Error()},
			)
		}

		var dnum, dnorm float64
		for a := 0; a < p+1; a++ {
			bn := betaNew.AtVec(a)
			d := bn - beta[a]
			dnum += d * d
			dnorm += beta[a] * beta[a]
		}
		dnum = math.Sqrt(dnum)
		dnorm = math.Sqrt(dnorm)
		for a := 0; a < p+1; a++ {
			beta[a] = betaNew.AtVec(a)
		}
		for i := 0; i < n; i++ {
			s := 0.0
			xi := xRows[i*(p+1) : (i+1)*(p+1)]
			for a := 0; a < p+1; a++ {
				s += xi[a] * beta[a]
			}
			eta[i] = s
		}
		convergedIters = iter + 1
		if dnum/(dnorm+convergenceEps) < tol {
			return beta, convergedIters, nil
		}
	}
	return nil, convergedIters, errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_NO_CONVERGE,
		"REG_GLM refit IRLS failed to converge within MaxIters",
		map[string]any{
			"n": n, "p": p,
			"max_iters":       maxIters,
			"tol":             tol,
			"family":          fam.Name,
			"link":            fam.Link,
			"converged_iters": convergedIters,
		},
	)
}
