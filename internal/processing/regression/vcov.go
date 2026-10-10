package regression

import (
	"math"

	"github.com/frankbardon/pulse/types"
)

// vcov.go builds the opt-in coefficient covariance (RegressionSpec.Vcov
// → RegressionResult.Vcov / .Correlation). Every engine already
// computes the covariance its standard errors are the square roots of
// the diagonal of; these helpers package the whole matrix. Sources:
//
//   - REG_OLS unpenalized: σ̂²·M2_xx⁻¹ for the slopes plus the intercept
//     row Cov(β₀, β_j) = −Σ_k μ_x[k]·Cov(β_k, β_j) (ȳ is uncorrelated
//     with the centred slopes) and Var(β₀) exactly as the SE uses it.
//   - REG_OLS ridge: the same assembly over the sandwich
//     σ̂²·(M2_xx + Σw·λ·I)⁻¹·M2_xx·(M2_xx + Σw·λ·I)⁻¹.
//   - REG_GLM: (XᵀWX)⁻¹ at the converged working weights (dispersion 1).
//   - REG_BAYES_LINEAR: the posterior covariance b_n/(a_n−1)·Λ_n⁻¹.
//
// On REG_OLS / REG_GLM the diagonal is the exact pre-square-root
// variance the standard error reads, so √diag == StdErrors bit for bit.
// A weighted fit inherits the inference basis N* (Σw under frequency,
// n_eff under probability) because σ̂² and the GLM prior weights
// already sit on it.

// coefficientKeys is the row / column key order of a regression's
// covariance: the intercept, then the predictors in spec order.
func coefficientKeys(predictors []string) []string {
	keys := make([]string, 0, len(predictors)+1)
	keys = append(keys, InterceptKey)
	return append(keys, predictors...)
}

// olsVcov assembles the (p+1)×(p+1) covariance of (β₀, β₁ … β_p) for a
// closed-form OLS / ridge solve. nil when the solve carries no slope
// covariance (the l1 solvers, which never reach here: validateVcov
// refuses them).
func olsVcov(s *olsSolveResult, meanX []float64) [][]float64 {
	if s.slopeCov == nil {
		return nil
	}
	p := len(s.Coefficients)
	v := make([][]float64, p+1)
	for i := range v {
		v[i] = make([]float64, p+1)
	}
	for i := 0; i < p; i++ {
		for j := i + 1; j < p; j++ {
			c := s.sigma2Gram * s.slopeCov(i, j)
			v[i+1][j+1], v[j+1][i+1] = c, c
		}
	}
	for j := 0; j < p; j++ {
		// Cov(β₀, β_j) = −Σ_k μ_x[k]·σ̂²·C[k, j].
		c := 0.0
		for k := 0; k < p; k++ {
			c -= meanX[k] * (s.sigma2Gram * s.slopeCov(k, j))
		}
		v[0][j+1], v[j+1][0] = c, c
	}
	for j := 0; j <= p; j++ {
		v[j][j] = s.variances[j]
	}
	return v
}

// symVcov copies a symmetric (q×q) covariance read through at, scaled
// by scale, with the diagonal replaced by diag when diag is non-nil
// (the clamped variances the standard errors read).
func symVcov(q int, at func(i, j int) float64, scale float64, diag []float64) [][]float64 {
	v := make([][]float64, q)
	for i := range v {
		v[i] = make([]float64, q)
	}
	for i := 0; i < q; i++ {
		for j := i; j < q; j++ {
			c := scale * at(i, j)
			v[i][j], v[j][i] = c, c
		}
		if diag != nil {
			v[i][i] = diag[i]
		}
	}
	return v
}

// attachVcov sets res.Vcov and res.Correlation from v (square, keys in
// coefficientKeys order). Correlation is R's cov2cor: r_ij =
// (s_i·V_ij)·s_j with s_i = √(1/V_ii) and a unit diagonal; an entry
// whose variance is zero or undefined is NaN (null on the wire).
func attachVcov(res *types.RegressionResult, predictors []string, v [][]float64) {
	if v == nil {
		return
	}
	keys := coefficientKeys(predictors)
	q := len(keys)
	is := make([]float64, q)
	for i := 0; i < q; i++ {
		is[i] = math.Sqrt(1 / v[i][i])
	}
	corr := make([][]float64, q)
	for i := range corr {
		corr[i] = make([]float64, q)
	}
	for i := 0; i < q; i++ {
		for j := i; j < q; j++ {
			r := is[i] * v[i][j] * is[j]
			if i == j {
				r = 1
				if !(v[i][i] > 0) || math.IsInf(v[i][i], 0) {
					r = math.NaN()
				}
			}
			if math.IsInf(r, 0) || math.IsInf(is[i], 0) || math.IsInf(is[j], 0) {
				r = math.NaN()
			}
			corr[i][j], corr[j][i] = r, r
		}
	}
	res.Vcov = vcovMatrix(keys, v)
	res.Correlation = vcovMatrix(keys, corr)
}

func vcovMatrix(keys []string, values [][]float64) *types.MatrixValues {
	return &types.MatrixValues{
		Kind:       types.MatrixKindSquareSymmetric,
		Encoding:   types.MatrixEncodingFull,
		RowKeys:    append([]string(nil), keys...),
		ColumnKeys: append([]string(nil), keys...),
		Values:     values,
	}
}
