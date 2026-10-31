package regression

import "github.com/frankbardon/pulse/errors"

// solveOLSNormal is REG_OLS's solver "normal" (RegressionSpec.Solver):
// the closed-form fit on the normal equations, kept verbatim from
// before the QR solve (ols_qr.go) became the default, so a request can
// reproduce an older result bit for bit or match a package that solves
// the normal equations.
//
//  1. Cholesky-factor the centered Gram matrix M2_xx; a failure means
//     at least one predictor is a linear combination of the others.
//  2. Solve M2_xx · β = M2_xy; intercept β_0 = μ_y − Σ β_j · μ_x[j].
//  3. RSS = M2_yy − β · M2_xy; σ² = RSS / (n − p − 1).
//  4. Var(β) = σ² · M2_xx⁻¹; SE(β_0)² = σ² · (1/n + μ_xᵀ · M2_xx⁻¹ · μ_x).
//
// It squares the design's condition number: on an ill-conditioned
// design (NIST Filip, the Wampler polynomials) it loses digits the QR
// solve keeps, or refuses the fit as rank-deficient.
func solveOLSNormal(a *olsAccumulator) (*olsSolveResult, error) {
	p := a.p
	if a.floorN() < float64(p+1) {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression requires n ≥ p + 1 observations after filtering",
			map[string]any{"n": a.n, "p": p, "required": p + 1},
		)
	}

	a.finalize()

	// Factor m2XX (upper triangle). The factor owns a copy, so the
	// accumulator's backing slice stays untouched.
	chol, ok := factorSPD(p, a.m2XX)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"centered Gram matrix is not positive-definite (linearly dependent or constant predictors)",
			map[string]any{"n": a.n, "p": p},
		)
	}

	// Solve M2_xx · β = M2_xy.
	coeffs, err := solveSPD(chol, a.m2XY)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky solve failed for the centered Gram matrix",
			map[string]any{"n": a.n, "p": p, "gonum_error": backendErrorText(err)},
		)
	}

	// Intercept and residual sum of squares.
	intercept := a.meanY
	betaDotMeanX := 0.0
	for j := 0; j < p; j++ {
		intercept -= coeffs[j] * a.meanX[j]
		betaDotMeanX += coeffs[j] * a.meanX[j]
	}
	_ = betaDotMeanX // documented derivation; consumed by SE(intercept) below

	rss := a.m2YY
	for j := 0; j < p; j++ {
		rss -= coeffs[j] * a.m2XY[j]
	}
	// Numerical floor: tiny negative RSS from cancellation rounds to 0.
	if rss < 0 {
		if rss > -1e-9*a.m2YY {
			rss = 0
		} else {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"residual sum of squares is negative; predictors are nearly collinear",
				map[string]any{"rss": rss, "tss": a.m2YY},
			)
		}
	}

	df := a.residualDF()
	// NaN when df ≤ 0 (residualVariances): no clamp, so the standard
	// errors below stay NaN rather than claiming perfect precision.
	sigma2Gram, sigma2 := a.residualVariances(rss)

	// R² and adjusted R².
	tss := a.m2YY
	var r2, adjR2 float64
	if tss > 0 {
		r2 = 1 - rss/tss
		adjR2 = a.adjustedR2(r2, df)
	}

	// Standard errors: Var(β) = σ² · M2_xx⁻¹ (on w*: σ̂*²·(c·M2_xx)⁻¹ =
	// sigma2Gram · M2_xx⁻¹, the c cancelling).
	// Invert M2_xx via the Cholesky factor, then read diagonal entries.
	invXX, err := chol.Inverse()
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky inverse failed for the centered Gram matrix",
			map[string]any{"gonum_error": backendErrorText(err)},
		)
	}
	stdErrors := make([]float64, p+1)
	variances := make([]float64, p+1)
	// Slope SEs.
	for j := 0; j < p; j++ {
		v := sigma2Gram * invXX.At(j, j)
		if v < 0 {
			v = 0
		}
		variances[j+1] = v
		stdErrors[j+1] = sqrt(v)
	}
	// Intercept SE: σ² · (1/n + μ_xᵀ · M2_xx⁻¹ · μ_x); on w* the 1/N*
	// term carries c, so it reads 1/Σw (1/n unweighted).
	// quad = μ_xᵀ · M2_xx⁻¹ · μ_x.
	quad := 0.0
	for i := 0; i < p; i++ {
		row := 0.0
		for j := 0; j < p; j++ {
			row += invXX.At(i, j) * a.meanX[j]
		}
		quad += a.meanX[i] * row
	}
	seInt := sigma2Gram * (1.0/a.sumW + quad)
	if seInt < 0 {
		seInt = 0
	}
	variances[0] = seInt
	stdErrors[0] = sqrt(seInt)

	return &olsSolveResult{
		slopeCov:       invXX.At,
		sigma2Gram:     sigma2Gram,
		variances:      variances,
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
