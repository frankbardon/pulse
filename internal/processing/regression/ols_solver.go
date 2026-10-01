package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"gonum.org/v1/gonum/mat"
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
	DF             int // n − p − 1
}

// solveOLS performs the closed-form OLS fit from the centered
// sufficient statistics produced by olsAccumulator.finalize().
//
// Algorithm:
//
//  1. Cholesky-factor the centered Gram matrix M2_xx. A successful
//     factorization implies positive-definite and therefore full rank;
//     a failure means at least one predictor is a linear combination of
//     the others (rank-deficient).
//
//  2. Solve M2_xx · β = M2_xy for β via the Cholesky factor.
//
//  3. Intercept β_0 = μ_y − Σ β_j · μ_x[j].
//
//  4. Residual sum of squares (closed form, no per-row sweep):
//     RSS = M2_yy − β · M2_xy.
//
//  5. σ² = RSS / (n − p − 1). Var(β) = σ² · M2_xx⁻¹; standard errors
//     are the square roots of the diagonal. Intercept standard error:
//     SE(β_0)² = σ² · (1/n + μ_xᵀ · M2_xx⁻¹ · μ_x).
//
// Returns PROCESSING_REGRESSION_RANK_DEFICIENT when Cholesky fails or
// when the residual variance is non-positive (degenerate inputs).
// Returns PROCESSING_REGRESSION_INSUFFICIENT_DATA when n < p + 1.
func solveOLS(a *olsAccumulator) (*olsSolveResult, error) {
	p := a.p
	if a.n < p+1 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression requires n ≥ p + 1 observations after filtering",
			map[string]any{"n": a.n, "p": p, "required": p + 1},
		)
	}

	a.finalize()

	// Wrap m2XX as a symmetric matrix. gonum copies on Factorize so the
	// accumulator's backing slice stays untouched.
	sym := mat.NewSymDense(p, append([]float64(nil), a.m2XX...))
	var chol mat.Cholesky
	if ok := chol.Factorize(sym); !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"centered Gram matrix is not positive-definite (linearly dependent or constant predictors)",
			map[string]any{"n": a.n, "p": p},
		)
	}

	// Solve M2_xx · β = M2_xy.
	rhs := mat.NewVecDense(p, append([]float64(nil), a.m2XY...))
	var beta mat.VecDense
	if err := chol.SolveVecTo(&beta, rhs); err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky solve failed for the centered Gram matrix",
			map[string]any{"n": a.n, "p": p, "gonum_error": err.Error()},
		)
	}
	coeffs := make([]float64, p)
	for i := 0; i < p; i++ {
		coeffs[i] = beta.AtVec(i)
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

	df := a.n - p - 1
	sigma2 := rss / float64(df)
	if sigma2 < 0 {
		sigma2 = 0
	}

	// R² and adjusted R².
	tss := a.m2YY
	var r2, adjR2 float64
	if tss > 0 {
		r2 = 1 - rss/tss
		adjR2 = 1 - (1-r2)*float64(a.n-1)/float64(df)
	}

	// Standard errors: Var(β) = σ² · M2_xx⁻¹.
	// Invert M2_xx via Cholesky.InverseTo, then read diagonal entries.
	var invXX mat.SymDense
	if err := chol.InverseTo(&invXX); err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"Cholesky inverse failed for the centered Gram matrix",
			map[string]any{"gonum_error": err.Error()},
		)
	}
	stdErrors := make([]float64, p+1)
	// Slope SEs.
	for j := 0; j < p; j++ {
		v := sigma2 * invXX.At(j, j)
		if v < 0 {
			v = 0
		}
		stdErrors[j+1] = sqrt(v)
	}
	// Intercept SE: σ² · (1/n + μ_xᵀ · M2_xx⁻¹ · μ_x).
	// quad = μ_xᵀ · M2_xx⁻¹ · μ_x.
	quad := 0.0
	for i := 0; i < p; i++ {
		row := 0.0
		for j := 0; j < p; j++ {
			row += invXX.At(i, j) * a.meanX[j]
		}
		quad += a.meanX[i] * row
	}
	seInt := sigma2 * (1.0/float64(a.n) + quad)
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

// sqrt clamps non-positive inputs (from rounding) to zero before
// delegating to math.Sqrt so callers never see NaN std errors.
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Sqrt(x)
}
