package regression

import (
	"github.com/frankbardon/pulse/linalg"
)

// bayes_covariance.go is REG_BAYES_LINEAR's robust covariance
// (RegressionSpec.Covariance: hc0–hc3, cluster, hac): Müller's (2013)
// sandwich posterior. The coefficients stay the conjugate posterior
// mean μ_n; the posterior covariance (b_n/a_n)·Λ_n⁻¹ is REPLACED by
// the shared sandwich (covariance.go) with bread Λ_n⁻¹ — the posterior
// precision Λ₀ + XᵀWX, σ²-free: the log posterior's curvature is
// Λ_n/σ² and each row's likelihood score e·x/σ², so σ² cancels — over
// the rows' unit-weight scores g = (y − xᵀμ_n)·x and unit leverage
// h = xᵀΛ_n⁻¹x, in the raw (1, x) coordinates. The sandwich posterior
// is N(μ_n, V), so the credible and confidence intervals read the
// normal quantile and carry no df. With the prior precision → 0 it is
// sandwich::vcovHC / vcovCL / NeweyWest on lm; REG_BAYES_LINEAR is
// frequency-weighted only, so w* = w and every weighted figure is the
// rep()-expanded one.

// bayesRobust is a Bayes fit's sandwich-posterior covariance in
// (β₀, β) coordinates.
type bayesRobust struct {
	fit       *sandwichFit
	stdErrors []float64
}

// bayesSandwich runs the second pass over records at the posterior mean
// muN (intercept first) with bread invLambda = Λ_n⁻¹.
func (e *bayesLinearEngine) bayesSandwich(muN []float64, invLambda *linalg.Sym, records []Record) (*bayesRobust, error) {
	q := len(muN)
	bread := newSquare(q)
	for a := 0; a < q; a++ {
		for b := 0; b < q; b++ {
			bread[a][b] = invLambda.At(a, b)
		}
	}
	c := e.acc.scale()
	rows := make([]sandwichRow, 0, e.acc.n)
	z := make([]float64, q)
	z[0] = 1
	for _, rec := range records {
		x, y, w, ok := e.fitRow(rec)
		if !ok {
			continue
		}
		cluster, order, _ := robustRowKeys(e.spec, rec)
		copy(z[1:], x)
		resid := y - LinearPredictor(muN[0], muN[1:], x)
		score := make([]float64, q)
		quad := 0.0
		for a := 0; a < q; a++ {
			score[a] = resid * z[a]
			s := 0.0
			for b := 0; b < q; b++ {
				s += float64(bread[a][b] * z[b])
			}
			quad += float64(z[a] * s)
		}
		rows = append(rows, sandwichRow{
			score:   score,
			weight:  c * w,
			lev:     quad,
			cluster: cluster,
			order:   order,
		})
	}
	fit, err := sandwich(e.spec, bread, rows, e.acc.nStar())
	if err != nil {
		return nil, err
	}
	return &bayesRobust{fit: fit, stdErrors: robustStdErrors(fit.v)}, nil
}
