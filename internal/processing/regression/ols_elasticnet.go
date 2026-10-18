package regression

// solveElasticNet fits an Elastic-Net penalized OLS by reusing the
// shared coordinate-descent loop on the raw centred Gram, minimizing
// glmnet's standardize = FALSE objective
//
//	(1/2N*)·Σw·r² + λ·[(1−α)/2·‖β‖² + α·‖β‖₁]
//
// with α == L1Ratio (0 < α < 1) and λ == Alpha — the α meaning REG_GLM
// and ridge share. The per-coordinate update is
//
//	β_new[j] = soft_threshold(r_j/N*, λ·α) / (M2_xx[j,j]/N* + λ·(1−α))
//
// Returns the same error envelope as solveLasso:
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA
//   - PROCESSING_REGRESSION_RANK_DEFICIENT
//   - PROCESSING_REGRESSION_NO_CONVERGE
//
// reference: R glmnet(standardize = FALSE) on a unit-SD response
// (scripts/reference/gen_ols_penalty.R).
func solveElasticNet(a *olsAccumulator, alpha, l1Ratio float64, maxIters int, tol float64) (*olsSolveResult, int, error) {
	if maxIters <= 0 {
		maxIters = defaultLassoMaxIters
	}
	if tol <= 0 {
		tol = defaultLassoTol
	}
	return coordinateDescent(a, alpha, l1Ratio, maxIters, tol)
}
