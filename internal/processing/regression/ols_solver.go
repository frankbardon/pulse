package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
)

// olsSolveResult bundles the post-solve quantities the OLS engine needs
// to populate a RegressionResult. All quantities are computed at
// finalize time from the centered sufficient statistics; the design
// matrix X is never materialized.
type olsSolveResult struct {
	Coefficients   []float64 // length p — slopes (β_centered)
	Intercept      float64
	StdErrors      []float64 // length p+1 — [intercept, β_1 … β_p]
	RSS            float64
	TSS            float64 // m2YY (total sum of squares about the mean)
	R2             float64
	AdjR2          float64
	Sigma2         float64
	ResidualStdErr float64
	DF             float64 // N* − p − 1 (n − p − 1 unweighted; fractional under probability)

	// Covariance inputs for the opt-in Vcov (vcov.go olsVcov), set by
	// the closed-form solvers only (nil slopeCov on the l1 solvers):
	// slopeCov(i, j) is the unscaled slope covariance (M2_xx⁻¹, or the
	// ridge sandwich), sigma2Gram its scale, and variances the clamped
	// pre-square-root variances of [intercept, β_1 … β_p] — StdErrors[j]
	// is exactly √variances[j].
	slopeCov   func(i, j int) float64
	sigma2Gram float64
	variances  []float64
	// throughOrigin marks a fit with no intercept: the intercept row and
	// column of the covariance are NaN (not estimated).
	throughOrigin bool
}

// solveOLS performs the closed-form OLS fit from the centered
// sufficient statistics produced by olsAccumulator.finalize().
//
// Algorithm:
//
//  1. Read the streaming QR of the weighted design [1, X, y]
//     (olsQR): R_x·β = Qᵀy by back substitution, intercept included.
//     A diagonal of R_x below qrRankTol·‖x_j‖ means a predictor is a
//     linear combination of the others (rank-deficient).
//
//  2. Residual sum of squares: the last diagonal entry of R, squared.
//
//  3. σ² = RSS / (n − p − 1). Var(β) = σ² · (XᵀX)⁻¹ = σ²·R_x⁻¹R_x⁻ᵀ;
//     standard errors are the square roots of the diagonal. The slope
//     block is the centred M2_xx⁻¹; the intercept entry is
//     1/n + μ_xᵀ · M2_xx⁻¹ · μ_x.
//
// The moments (m2YY, the means, Σw) still supply TSS, N* and the
// intercept-free quantities; the penalized solvers read them in full.
//
// Weighted (.claude/reference/weighting.md, Weighted inference): the
// accumulator holds Σw-weighted moments, so β is WLS (kind-free); every
// inferential figure is the frequency formula on w* = w·N*/Σw — df =
// N* − p − 1, σ̂² = c·RSS/df, Var(β) = (RSS/df)·M2_xx⁻¹. Unweighted the
// figures are the classical ones bit for bit.
//
// Returns PROCESSING_REGRESSION_RANK_DEFICIENT when Cholesky fails or
// when the residual variance is non-positive (degenerate inputs).
// Returns PROCESSING_REGRESSION_INSUFFICIENT_DATA when n < p + 1.
func solveOLS(a *olsAccumulator) (*olsSolveResult, error) {
	p := a.p
	if a.throughOrigin {
		return solveOLSThroughOrigin(a)
	}
	if a.floorN() < float64(p+1) {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression requires n ≥ p + 1 observations after filtering",
			map[string]any{"n": a.n, "p": p, "required": p + 1},
		)
	}

	a.finalize()

	// Solve on the streaming QR of [1, X, y] (ols_qr.go): β, the
	// residual sum of squares and (XᵀWX)⁻¹ come from R, never from the
	// normal equations, so an ill-conditioned design keeps κ(X), not
	// κ(X)². A column R's diagonal marks as a linear combination of the
	// earlier ones refuses the fit, as a failed Cholesky did.
	beta, rss, ess, rinv, ok := a.qr.solve()
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"design matrix is rank-deficient (linearly dependent or constant predictors)",
			map[string]any{"n": a.n, "p": p},
		)
	}
	coeffs := append([]float64(nil), beta[1:]...)
	cov := covInverse(rinv, p+1) // shifted (XᵀWX)⁻¹; slope block = M2_xx⁻¹
	// The intercept from the Welford means, β₀ = μ_y − Σ β_j·μ_x[j]:
	// accurate at any offset, where the QR's shifted β₀′ would have to
	// be carried back through the origin.
	intercept := a.meanY
	for j := 0; j < p; j++ {
		intercept -= coeffs[j] * a.meanX[j]
	}

	df := a.residualDF()
	// NaN when df ≤ 0 (residualVariances): no clamp, so the standard
	// errors below stay NaN rather than claiming perfect precision.
	sigma2Gram, sigma2 := a.residualVariances(rss)

	// R² = ESS / (ESS + RSS), as R's summary.lm reads it, both from the
	// QR: no 1 − RSS/TSS cancellation when R² is near 0. TSS stays the
	// Welford-centred Σw(y − ȳ)².
	tss := a.m2YY
	var r2, adjR2 float64
	if ess+rss > 0 && tss > 0 {
		r2 = ess / (ess + rss)
		adjR2 = a.adjustedR2(r2, df)
	}

	// Var(β) = σ² · M2_xx⁻¹ (on w*: σ̂*²·(c·M2_xx)⁻¹ = sigma2Gram ·
	// M2_xx⁻¹, the c cancelling): the slope block of the shifted
	// (XᵀWX)⁻¹. Intercept: σ² · (1/Σw + μ_xᵀ · M2_xx⁻¹ · μ_x), a sum of
	// non-negative terms, so no cancellation at any offset.
	n1 := p + 1
	slopeCov := func(i, j int) float64 { return cov[(i+1)*n1+(j+1)] }
	stdErrors := make([]float64, n1)
	variances := make([]float64, n1)
	for j := 0; j < p; j++ {
		v := sigma2Gram * slopeCov(j, j)
		if v < 0 {
			v = 0
		}
		variances[j+1] = v
		stdErrors[j+1] = sqrt(v)
	}
	quad := 0.0
	for i := 0; i < p; i++ {
		row := 0.0
		for j := 0; j < p; j++ {
			row += slopeCov(i, j) * a.meanX[j]
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
		slopeCov:       slopeCov,
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

// sqrt clamps non-positive inputs (rounding residue on a fit with
// df > 0) to zero before delegating to math.Sqrt. A NaN input — the
// undefined σ² of a fit with df ≤ 0 — passes through as NaN.
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Sqrt(x)
}

// solveOLSThroughOrigin is solveOLS for a fit with no intercept
// (RegressionSpec.Intercept false), on the QR of the raw √w·[X, y]:
// β = R_x⁻¹·Qᵀy, Var(β) = σ²·(XᵀWX)⁻¹ with σ² = RSS/(N* − p), and the
// uncentred R² = Σŷ² / (Σŷ² + RSS) R's lm(y ~ 0 + x) reports. The
// intercept is pinned to 0 — not estimated — so its variance (and with
// it its standard error, p-value and interval) is NaN, null on the wire.
func solveOLSThroughOrigin(a *olsAccumulator) (*olsSolveResult, error) {
	p := a.p
	if a.floorN() < float64(p) {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression through the origin requires n ≥ p observations after filtering",
			map[string]any{"n": a.n, "p": p, "required": p},
		)
	}
	a.finalize()
	beta, rss, ess, rinv, ok := a.qr.solve()
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"design matrix is rank-deficient (linearly dependent or all-zero predictors)",
			map[string]any{"n": a.n, "p": p, "intercept": false},
		)
	}
	cov := covInverse(rinv, p)
	df := a.residualDF()
	sigma2Gram, sigma2 := a.residualVariances(rss)
	tss := ess + rss
	var r2, adjR2 float64
	if tss > 0 {
		r2 = ess / tss
		adjR2 = a.adjustedR2(r2, df)
	}
	slopeCov := func(i, j int) float64 { return cov[i*p+j] }
	stdErrors := make([]float64, p+1)
	variances := make([]float64, p+1)
	variances[0], stdErrors[0] = math.NaN(), math.NaN()
	for j := 0; j < p; j++ {
		v := sigma2Gram * slopeCov(j, j)
		if v < 0 {
			v = 0
		}
		variances[j+1] = v
		stdErrors[j+1] = sqrt(v)
	}
	return &olsSolveResult{
		slopeCov:       slopeCov,
		sigma2Gram:     sigma2Gram,
		variances:      variances,
		throughOrigin:  true,
		Coefficients:   beta,
		Intercept:      0,
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
