package regression

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// solveRidge fits the ridge-penalized OLS coefficients in closed form
// from the streaming Gram. The penalty term λ·n·I is added to the
// (unstandardized) centered Gram before Cholesky factorization:
//
//	β = (M2_xx + n·λ·I)⁻¹ · M2_xy
//	intercept = μ_y − Σ β_j · μ_x[j]
//
// The n·λ scaling matches scikit-learn's Ridge(alpha=λ) convention
// (penalty added to Σ(y − Xβ)², not the mean). This way Phase 2's
// Alpha=0 boundary reproduces unpenalized OLS exactly: the augmented
// system reduces to the same Cholesky problem Phase 1 already solves.
//
// Standard errors are the classic ridge sandwich:
//
//	Var(β) = σ² · (M2_xx + n·λ·I)⁻¹ · M2_xx · (M2_xx + n·λ·I)⁻¹
//
// with σ² = RSS / (n − p − 1) on the original (un-standardized) scale.
// Operating on the unstandardized centered Gram keeps the SE
// derivation aligned with Phase 1's bookkeeping; standardization is
// reserved for the iterative l1 / elasticnet solvers where it changes
// the optimization landscape.
//
// Returns:
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA: n < p + 1.
//   - PROCESSING_REGRESSION_RANK_DEFICIENT:   Cholesky on the augmented
//     matrix fails (only possible when alpha is so small the system
//     remains effectively singular, e.g. alpha → 0 with duplicated
//     columns).
//
// reference: numpy.linalg.solve(XtX + n·alpha·I, Xty) on identical
// inputs produces the same β to machine precision; the closed-form
// fixture in ols_ridge_test.go documents one such case.
func solveRidge(a *olsAccumulator, alpha float64) (*olsSolveResult, error) {
	p := a.p
	if a.n < p+1 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression requires n ≥ p + 1 observations after filtering",
			map[string]any{"n": a.n, "p": p, "required": p + 1},
		)
	}

	a.finalize()

	// Build M2_xx + n·λ·I. Copy m2XX so we never mutate the accumulator.
	augmented := make([]float64, p*p)
	copy(augmented, a.m2XX)
	// The penalty scale is Σw (n unweighted): β is invariant to
	// rescaling the weights and, under frequency, equals the expanded
	// rows (glmnet's (1/2Σw)·Σw·r² objective).
	scaled := a.sumW * alpha
	for i := 0; i < p; i++ {
		augmented[i*p+i] += scaled
	}

	chol, ok := factorSPD(p, augmented)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"augmented Gram (M2_xx + n·λ·I) is not positive-definite; raise Alpha or drop a predictor",
			map[string]any{"n": a.n, "p": p, "alpha": alpha},
		)
	}

	coeffs, err := solveSPD(chol, a.m2XY)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky solve failed for the augmented ridge system",
			map[string]any{"n": a.n, "p": p, "alpha": alpha, "gonum_error": backendErrorText(err)},
		)
	}

	// Intercept and RSS. RSS is computed from the centered identity:
	//   Σ(y_i − β·x_i − intercept)² = M2_yy − 2 β·M2_xy + βᵀ · M2_xx · β
	// which avoids re-sweeping the rows.
	intercept := a.meanY
	for j := 0; j < p; j++ {
		intercept -= coeffs[j] * a.meanX[j]
	}

	// RSS = m2YY − 2·β·m2XY + βᵀ·m2XX·β
	rss := a.m2YY
	for j := 0; j < p; j++ {
		rss -= 2.0 * coeffs[j] * a.m2XY[j]
	}
	for i := 0; i < p; i++ {
		// row sum: Σ_j m2XX[i,j] · β_j
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
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"residual sum of squares is negative; ridge fit numerically singular",
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

	// Standard errors: Var(β) = σ² · (M2_xx + n·λ·I)⁻¹ · M2_xx · (M2_xx + n·λ·I)⁻¹.
	// Invert the augmented matrix once, then sandwich with M2_xx.
	invAug, err := chol.Inverse()
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky inverse failed for the augmented ridge system",
			map[string]any{"gonum_error": backendErrorText(err)},
		)
	}
	// sandwich = invAug · M2_xx · invAug, both products gonum-backed on
	// the full (both-triangle) dense copy of the inverse.
	sandwich, err := ridgeSandwich(invAug, p, a.m2XX)
	if err != nil {
		return nil, err
	}

	stdErrors := make([]float64, p+1)
	for j := 0; j < p; j++ {
		v := sigma2Gram * sandwich.At(j, j)
		if v < 0 {
			v = 0
		}
		stdErrors[j+1] = sqrt(v)
	}
	// Intercept SE: σ² · (1/n + μ_xᵀ · Var(β)/σ² · μ_x)
	//             = σ²/n + μ_xᵀ · sandwich · μ_x  (since sandwich already
	// absorbs σ²/σ² = 1 in this expression we apply it explicitly):
	//   SE(β_0)² = σ²/n + μ_xᵀ · sandwich · μ_x · σ² · ... wait — keep it
	// consistent with Phase 1's expansion:
	//   Var(β_0) = σ²·(1/n) + μ_xᵀ · Var(β) · μ_x
	//             = σ²/n + μ_xᵀ · sandwich · μ_x · σ²
	// where sandwich is the un-σ²-scaled inverse-sandwich. Here we stored
	// Var(β)/σ² in `sandwich`, so multiply by σ² on the quadratic term.
	quad := 0.0
	for i := 0; i < p; i++ {
		row := 0.0
		for j := 0; j < p; j++ {
			row += sandwich.At(i, j) * a.meanX[j]
		}
		quad += a.meanX[i] * row
	}
	seInt := sigma2Gram*(1.0/a.sumW) + sigma2Gram*quad
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
	}, nil
}

// ridgeSandwich returns inv · M · inv for the symmetric inv and the p×p
// row-major M, through linalg's gonum-backed Mul.
func ridgeSandwich(inv *linalg.Sym, p int, m []float64) (*linalg.Matrix, error) {
	invDense, err := linalg.NewMatrixFromRows(inv.ToRows())
	if err != nil {
		return nil, err
	}
	mDense, err := linalg.NewMatrix(p, p, m)
	if err != nil {
		return nil, err
	}
	tmp, err := linalg.Mul(invDense, mDense)
	if err != nil {
		return nil, err
	}
	return linalg.Mul(tmp, invDense)
}
