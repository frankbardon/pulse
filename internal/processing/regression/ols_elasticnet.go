package regression

// solveElasticNet fits an Elastic-Net penalized OLS by reusing the
// shared coordinate-descent loop. The combined penalty is
//
//	λ·(α·||β||₁ + (1−α)/2·||β||₂²)
//
// with α == L1Ratio (0 < α < 1). The per-coordinate update specializes
// to
//
//	β_new[j] = soft_threshold(r_j/n, λ·α) / (M2_xx_std[j,j]/n + λ·(1−α))
//
// which the shared coordinateDescent helper already implements. This
// wrapper exists so the engine dispatch table reads cleanly
// (Penalty="elasticnet" → solveElasticNet) and so future changes
// specific to elasticnet (warm-starts, regularization paths) have a
// clear home without disturbing the lasso entry point.
//
// Returns the same error envelope as solveLasso:
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA
//   - PROCESSING_REGRESSION_RANK_DEFICIENT
//   - PROCESSING_REGRESSION_NO_CONVERGE
//
// reference: scikit-learn ElasticNet(alpha=λ, l1_ratio=α,
// fit_intercept=True, normalize=False) on identical inputs produces
// the same coefficient vector modulo coordinate-descent ordering
// numerics; sparse-recovery fixtures in ols_elasticnet_test.go cite
// their reference values inline.
func solveElasticNet(a *olsAccumulator, alpha, l1Ratio float64, maxIters int, tol float64) (*olsSolveResult, int, error) {
	if maxIters <= 0 {
		maxIters = defaultLassoMaxIters
	}
	if tol <= 0 {
		tol = defaultLassoTol
	}
	return coordinateDescent(a, alpha, l1Ratio, maxIters, tol)
}
