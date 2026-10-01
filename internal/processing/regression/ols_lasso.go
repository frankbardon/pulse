package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
)

// Default coordinate-descent hyperparameters. Overridable through the
// RegressionSpec.MaxIters / Tol fields.
const (
	defaultLassoMaxIters = 1000
	defaultLassoTol      = 1e-6
)

// solveLasso fits the L1-penalized OLS coefficients via coordinate
// descent on standardized predictors.
//
// Algorithm (cyclic coordinate descent with soft-thresholding):
//
//	repeat:
//	    for j = 0..p-1:
//	        r_j      = M2_xy_std[j] − Σ_{k≠j} M2_xx_std[j,k] · β_std[k]
//	        z        = r_j / n
//	        β_new[j] = soft_threshold(z, λ) · n / M2_xx_std[j,j]
//	    until max|Δβ| < tol  or  iter == MaxIters
//
// soft_threshold(z, γ) = sign(z) · max(|z| − γ, 0).
//
// All inner products derive from the standardized streaming Gram —
// coordinate descent never re-reads the data. After convergence the
// standardized coefficients are de-standardized to the original scale
// via destandardizeCoeffs.
//
// Standard errors are emitted only for the non-zero active set using a
// naive plug-in σ² estimate (RSS over the unpenalized active set) so
// callers see a usable approximate SE; the engine attaches a warning
// to the response flagging the limitation. Rigorous lasso SE/CI ships
// in Phase 5 via bootstrap / jackknife resampling.
//
// Returns:
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA: n < p + 1.
//   - PROCESSING_REGRESSION_RANK_DEFICIENT:    a predictor has zero
//     centered variance (constant column).
//   - PROCESSING_REGRESSION_NO_CONVERGE:       coordinate descent
//     exceeded MaxIters without reaching Tol.
//
// reference: scikit-learn Lasso(alpha=λ, fit_intercept=True,
// normalize=False) with the same λ produces identical coefficients
// modulo numerical tolerance once Pulse's per-predictor standardization
// is applied. The sparse-recovery fixture in ols_lasso_test.go cites
// the scikit-learn reference values it asserts against.
func solveLasso(a *olsAccumulator, alpha float64, maxIters int, tol float64) (*olsSolveResult, int, error) {
	if maxIters <= 0 {
		maxIters = defaultLassoMaxIters
	}
	if tol <= 0 {
		tol = defaultLassoTol
	}

	// l1Ratio=1.0 selects pure L1 inside the shared coordinateDescent
	// loop: the L2 contribution to the denominator vanishes and the
	// soft-threshold parameter reduces to plain alpha.
	const l1Ratio = 1.0
	res, iters, err := coordinateDescent(a, alpha, l1Ratio, maxIters, tol)
	if err != nil {
		return nil, iters, err
	}
	return res, iters, nil
}

// coordinateDescent is the shared coordinate-descent loop for lasso
// (l1Ratio=1) and elastic-net (0 < l1Ratio < 1). The combined penalty
// is λ·(α·||β||₁ + (1−α)/2·||β||₂²) where α is l1Ratio:
//
//	β_new[j] = soft_threshold(r_j/n, λ·α) / (M2_xx_std[j,j]/n + λ·(1−α))
//
// The pure-lasso update collapses to the same expression with the
// l2 term vanishing (1−α=0). Both solvers share this implementation
// so the soft-threshold + denominator-augmentation algebra lives in
// exactly one place.
//
// Returns the solve result, the iteration count actually executed,
// and any error.
func coordinateDescent(a *olsAccumulator, alpha, l1Ratio float64, maxIters int, tol float64) (*olsSolveResult, int, error) {
	p := a.p
	if a.n < p+1 {
		return nil, 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression requires n ≥ p + 1 observations after filtering",
			map[string]any{"n": a.n, "p": p, "required": p + 1},
		)
	}

	a.finalize()
	sg := standardizeGram(a)
	if sg.degenerate {
		return nil, 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"a predictor has zero centered variance; coordinate descent cannot standardize a constant column",
			map[string]any{"n": a.n, "p": p},
		)
	}

	n := float64(a.n)
	lambdaL1 := alpha * l1Ratio
	lambdaL2 := alpha * (1 - l1Ratio)

	betaStd := make([]float64, p)
	// Precompute per-coordinate denominators (M2_xx_std[j,j]/n + λ·(1−α)).
	// On standardized predictors M2_xx_std[j,j] == n exactly, so denom_j
	// simplifies to 1 + λ·(1−α). Compute via the explicit form anyway so
	// the algebra survives any future change to the standardization
	// convention.
	denom := make([]float64, p)
	for j := 0; j < p; j++ {
		denom[j] = sg.m2xxStd[j*p+j]/n + lambdaL2
	}

	var iters int
	for iters = 0; iters < maxIters; iters++ {
		maxDelta := 0.0
		for j := 0; j < p; j++ {
			// r_j = M2_xy_std[j] − Σ_{k≠j} M2_xx_std[j,k] · β_std[k]
			r := sg.m2xyStd[j]
			for k := 0; k < p; k++ {
				if k == j {
					continue
				}
				r -= sg.m2xxStd[j*p+k] * betaStd[k]
			}
			z := r / n
			newBeta := softThreshold(z, lambdaL1) / denom[j]
			delta := math.Abs(newBeta - betaStd[j])
			if delta > maxDelta {
				maxDelta = delta
			}
			betaStd[j] = newBeta
		}
		if maxDelta < tol {
			iters++ // count the final converged cycle
			break
		}
	}

	if iters >= maxIters {
		return nil, iters, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_NO_CONVERGE,
			"coordinate descent did not converge within MaxIters",
			map[string]any{
				"max_iters": maxIters,
				"tol":       tol,
				"alpha":     alpha,
				"l1_ratio":  l1Ratio,
			},
		)
	}

	coeffs, intercept := destandardizeCoeffs(betaStd, sg)

	// RSS on the original scale: m2YY − 2·β·m2XY + βᵀ·m2XX·β.
	rss := a.m2YY
	for j := 0; j < p; j++ {
		rss -= 2.0 * coeffs[j] * a.m2XY[j]
	}
	for i := 0; i < p; i++ {
		row := 0.0
		for j := 0; j < p; j++ {
			row += a.m2XX[i*p+j] * coeffs[j]
		}
		rss += coeffs[i] * row
	}
	if rss < 0 {
		if rss > -1e-9*a.m2YY {
			rss = 0
		} else {
			return nil, iters, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"residual sum of squares is negative; coordinate-descent fit numerically degenerate",
				map[string]any{"rss": rss, "tss": a.m2YY},
			)
		}
	}

	df := a.n - p - 1
	sigma2 := rss / float64(df)
	if sigma2 < 0 {
		sigma2 = 0
	}
	tss := a.m2YY
	var r2, adjR2 float64
	if tss > 0 {
		r2 = 1 - rss/tss
		adjR2 = 1 - (1-r2)*float64(a.n-1)/float64(df)
	}

	// Naive plug-in SE for the active set. The SE of a coordinate that
	// was shrunk to zero is undefined; emit zeros (the engine layer
	// populates the SE map only for the active set so consumers see the
	// gap explicitly).
	stdErrors := make([]float64, p+1)
	for j := 0; j < p; j++ {
		if coeffs[j] == 0 {
			stdErrors[j+1] = 0
			continue
		}
		// Plug-in: SE_j ≈ √(σ² / (n · M2_xx[j,j]/n)) = √(σ² / M2_xx[j,j])
		// — the diagonal of (M2_xx)⁻¹ falls back to this approximation
		// when off-diagonal terms are ignored. This is a coarse plug-in
		// only valid for an orthogonal active set; documented in the
		// skill doc + envelope warning.
		if a.m2XX[j*p+j] <= 0 {
			stdErrors[j+1] = 0
			continue
		}
		stdErrors[j+1] = sqrt(sigma2 / a.m2XX[j*p+j])
	}
	// Intercept SE: σ²/n + plug-in quad term over the active set.
	quad := 0.0
	for j := 0; j < p; j++ {
		if coeffs[j] == 0 || a.m2XX[j*p+j] <= 0 {
			continue
		}
		quad += sigma2 * a.meanX[j] * a.meanX[j] / a.m2XX[j*p+j]
	}
	seInt := sigma2/float64(a.n) + quad
	if seInt < 0 {
		seInt = 0
	}
	stdErrors[0] = sqrt(seInt)

	return &olsSolveResult{
		Coefficients:   coeffs,
		Intercept:      intercept,
		StdErrors:      stdErrors,
		RSS:            rss,
		TSS:            tss,
		R2:             r2,
		AdjR2:          adjR2,
		Sigma2:         sigma2,
		ResidualStdErr: sqrt(sigma2),
		DF:             df,
	}, iters, nil
}

// softThreshold implements the proximal operator of the L1 norm:
//
//	soft(z, γ) = sign(z) · max(|z| − γ, 0)
//
// Coordinate descent applies this to the (per-feature) least-squares
// update to shrink small effects to exact zero. Inlined as a free
// function so the elastic-net solver shares the same implementation.
func softThreshold(z, gamma float64) float64 {
	switch {
	case z > gamma:
		return z - gamma
	case z < -gamma:
		return z + gamma
	default:
		return 0
	}
}
