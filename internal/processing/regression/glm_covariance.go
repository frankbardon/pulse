package regression

import (
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// glm_covariance.go is REG_GLM's robust covariance
// (RegressionSpec.Covariance: hc0–hc3, cluster, hac): the shared
// sandwich (covariance.go) over the IRLS score contributions at the
// converged weights — R's sandwich::estfun / bread on a glm.
//
// At the converged β, with μ = g⁻¹(η), μ' = dμ/dη and the variance
// function V(μ), a row's unit-weight score is g = μ'·(y − μ)/V(μ)·x
// (its working residual times its unit working weight μ'²/V — the
// gradient of its log-likelihood, dispersion 1); the bread is the
// classical covariance (XᵀW*X)⁻¹ the engine already inverted, W* =
// w*·μ'²/V; a row's unit leverage is h = μ'²/V·xᵀ·bread·x. A row's w*
// is its prior weight, so the frequency sandwich on w* equals the one
// on the rep()-expanded glm (weighting.md, the one rule). The
// dispersion cancels between bread and meat, so the estimator is the
// same for every family. Inference stays Wald z under every type
// (lmtest::coeftest on a glm), including cluster.

// glmRobust is a GLM fit's robust covariance in (β₀, β) coordinates.
type glmRobust struct {
	fit       *sandwichFit
	stdErrors []float64
}

// glmSandwich assembles the robust covariance of a converged IRLS fit:
// xRows (n × q, intercept first), y, eta / mu at β, prior weights w*,
// the rows' covariance keys, the classical inverse and N*.
func (e *glmEngine) glmSandwich(xRows, y, eta, mu, prior []float64, keys []robustRowKey, inv *linalg.Sym, nStar float64) (*glmRobust, error) {
	q := len(e.spec.Predictors) + 1
	bread := newSquare(q)
	for a := 0; a < q; a++ {
		for b := 0; b < q; b++ {
			bread[a][b] = inv.At(a, b)
		}
	}
	rows := make([]sandwichRow, len(y))
	bx := make([]float64, q)
	for i := range y {
		xi := xRows[i*q : (i+1)*q]
		dmu := e.family.DmuDeta(eta[i])
		v := e.family.Variance(mu[i])
		if v < 1e-300 {
			v = 1e-300
		}
		u := dmu * (y[i] - mu[i]) / v
		score := make([]float64, q)
		for a := range score {
			score[a] = u * xi[a]
		}
		for a := 0; a < q; a++ {
			s := 0.0
			for b := 0; b < q; b++ {
				s += float64(bread[a][b] * xi[b])
			}
			bx[a] = s
		}
		quad := 0.0
		for a := 0; a < q; a++ {
			quad += float64(xi[a] * bx[a])
		}
		rows[i] = sandwichRow{
			score:   score,
			weight:  prior[i],
			lev:     dmu * dmu / v * quad,
			cluster: keys[i].cluster,
			order:   keys[i].order,
		}
	}
	fit, err := sandwich(e.spec, bread, rows, nStar)
	if err != nil {
		return nil, err
	}
	return &glmRobust{fit: fit, stdErrors: robustStdErrors(fit.v)}, nil
}

// robustKeyReader is the covariance-key read a robust spec's design
// builder applies (nil for a classical spec, so its design is unchanged).
func robustKeyReader(spec *types.RegressionSpec) func(Record) (robustRowKey, bool) {
	if !spec.Covariance.Robust() {
		return nil
	}
	return func(rec Record) (robustRowKey, bool) {
		c, o, ok := robustRowKeys(spec, rec)
		return robustRowKey{cluster: c, order: o}, ok
	}
}
