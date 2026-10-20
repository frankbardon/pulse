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
// descent on the raw centred streaming Gram, minimizing glmnet's
// objective with standardize = FALSE — the one α meaning every REG_OLS
// penalty and REG_GLM share:
//
//	(1/2N*)·Σw·(y − β₀ − Xβ)² + λ·[(1−l1_ratio)/2·‖β‖² + l1_ratio·‖β‖₁]
//
// (l1_ratio = 1 here), the intercept unpenalized, N* = Σw. Algorithm
// (cyclic coordinate descent with soft-thresholding):
//
//	repeat:
//	    for j = 0..p-1:
//	        r_j      = M2_xy[j] − Σ_{k≠j} M2_xx[j,k] · β[k]
//	        β_new[j] = soft_threshold(r_j/N*, λ) / (M2_xx[j,j]/N*)
//	    until max_j sd_j·|Δβ_j| < tol  or  iter == MaxIters
//
// soft_threshold(z, γ) = sign(z) · max(|z| − γ, 0). Every inner product
// comes from the streaming Gram — coordinate descent never re-reads the
// data — and the intercept is ȳ − Σ β_j·x̄_j.
//
// Standard errors are emitted only for the non-zero active set using a
// naive plug-in σ² estimate so callers see a usable approximate SE;
// predict warns PROCESSING_REGRESSION_APPROXIMATE_SE. Resample gives
// the rigorous one.
//
// Returns:
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA: n < p + 1.
//   - PROCESSING_REGRESSION_RANK_DEFICIENT:    a predictor has zero
//     centered variance (constant column).
//   - PROCESSING_REGRESSION_NO_CONVERGE:       coordinate descent
//     exceeded MaxIters without reaching Tol.
//
// reference: R glmnet(standardize = FALSE) on a unit-SD response (glmnet
// standardizes a gaussian y internally, which rescales its ridge term;
// at sd(y) = 1 that is the identity) — ols_penalty_reference_test.go,
// oracle scripts/reference/gen_ols_penalty.R.
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
// (l1Ratio=1) and elastic-net (0 < l1Ratio < 1) on the raw centred
// Gram (see solveLasso for the objective):
//
//	β_new[j] = soft_threshold(r_j/N*, λ·α) / (M2_xx[j,j]/N* + λ·(1−α))
//
// with α = l1Ratio. At l1Ratio = 0 the fixed point is ridge's
// (M2_xx + N*·λ·I)β = M2_xy (TestRegOLS_RidgeEqualsElasticNetAtZeroL1).
//
// Returns the solve result, the iteration count actually executed,
// and any error.
func coordinateDescent(a *olsAccumulator, alpha, l1Ratio float64, maxIters int, tol float64) (*olsSolveResult, int, error) {
	p := a.p
	if a.floorN() < float64(p+1) {
		return nil, 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression requires n ≥ p + 1 observations after filtering",
			map[string]any{"n": a.n, "p": p, "required": p + 1},
		)
	}

	a.finalize()
	// A constant predictor (zero centred variance) has no coordinate
	// update under a pure l1 penalty (0/0); refused as before.
	for j := 0; j < p; j++ {
		if a.m2XX[j*p+j]/a.sumW <= 1e-300 {
			return nil, 0, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"a predictor has zero centered variance; coordinate descent cannot fit a constant column",
				map[string]any{"n": a.n, "p": p},
			)
		}
	}

	// Σw in place of n (exactly n unweighted): the objective is
	// (1/2Σw)·Σw·r² + penalty, scale-free in w.
	n := a.sumW
	lambdaL1 := alpha * l1Ratio
	lambdaL2 := alpha * (1 - l1Ratio)

	// Coordinate descent on the RAW centred Gram (glmnet
	// standardize = FALSE): the penalty reads each coefficient on its
	// predictor's own units, the same α meaning as REG_GLM's penalties
	// and ridge. denom_j = M2_xx[j,j]/n + λ·(1−α).
	coeffs := make([]float64, p)
	denom := make([]float64, p)
	scale := make([]float64, p)
	for j := 0; j < p; j++ {
		denom[j] = a.m2XX[j*p+j]/n + lambdaL2
		// The column SD: convergence judges sd_j·|Δβ_j| (the move in
		// outcome units per predictor SD), the scale-free test the
		// standardized solver applied.
		scale[j] = math.Sqrt(a.m2XX[j*p+j] / n)
	}

	var iters int
	for iters = 0; iters < maxIters; iters++ {
		maxDelta := 0.0
		for j := 0; j < p; j++ {
			// r_j = M2_xy[j] − Σ_{k≠j} M2_xx[j,k] · β[k]
			r := a.m2XY[j]
			for k := 0; k < p; k++ {
				if k == j {
					continue
				}
				r -= a.m2XX[j*p+k] * coeffs[k]
			}
			newBeta := softThreshold(r/n, lambdaL1) / denom[j]
			delta := scale[j] * math.Abs(newBeta-coeffs[j])
			if delta > maxDelta {
				maxDelta = delta
			}
			coeffs[j] = newBeta
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

	intercept := a.meanY
	for j := 0; j < p; j++ {
		intercept -= coeffs[j] * a.meanX[j]
	}

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

	df := a.residualDF()
	// NaN when df ≤ 0 (residualVariances): no clamp, so the standard
	// errors below stay NaN rather than claiming perfect precision.
	sigma2Gram, sigma2 := a.residualVariances(rss)
	tss := a.m2YY
	var r2, adjR2 float64
	if tss > 0 {
		r2 = 1 - rss/tss
		adjR2 = a.adjustedR2(r2, df)
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
		stdErrors[j+1] = sqrt(sigma2Gram / a.m2XX[j*p+j])
	}
	// Intercept SE: σ²/n + plug-in quad term over the active set.
	quad := 0.0
	for j := 0; j < p; j++ {
		if coeffs[j] == 0 || a.m2XX[j*p+j] <= 0 {
			continue
		}
		quad += sigma2Gram * a.meanX[j] * a.meanX[j] / a.m2XX[j*p+j]
	}
	seInt := sigma2Gram/a.sumW + quad
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
