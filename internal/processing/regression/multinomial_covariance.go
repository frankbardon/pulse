package regression

import (
	"github.com/frankbardon/pulse/linalg"
)

// multinomial_covariance.go is REG_MULTINOMIAL's robust covariance
// (RegressionSpec.Covariance: hc0, hc1, cluster — U68 E3-S1): the shared
// sandwich (covariance.go) over the per-row score contributions at the
// MLE.
//
// A row's unit-weight score over the K − 1 non-reference blocks,
// category-major (the coefficient keys' order), is
//
//	s_i = (y_ik − π_ik) ⊗ x_i
//
// (the gradient of its log π_{i, y_i}); the bread is the classical
// covariance, the inverse information the fit already inverted; a
// row's w* is its prior weight. So the meat is Σ w*·s·sᵀ (hc0; hc1 by
// N*/(N* − d), d = K·(p + 1)) or Σ_g S_g·S_gᵀ, S_g = Σ_{i∈g} w*·s_i
// (cluster CR1, G/(G − 1)·(N* − 1)/(N* − d)) — the frequency sandwich on
// w* every robust Pulse covariance is (weighting.md, the one rule), so
// under kind frequency it is the rep()-expanded fit's. hc2 / hc3 need a
// per-row leverage and hac a series order (regcov refuses them on this
// type). Inference stays Wald z (cluster included, as REG_GLM): the
// table never reads the sandwich's G − 1 df. R has no sandwich method
// for multinom; the oracle is the closed form (gen_multinomial.R).

// multinomialSandwich assembles the robust covariance of a converged
// fit: pr at beta, inv the classical inverse information, the rows'
// cluster keys (nil unless type cluster) and N*.
func (e *multinomialEngine) multinomialSandwich(pr *multinomialProblem, beta []float64, inv *linalg.Sym, keys []robustRowKey, nStar float64) (*sandwichFit, []float64, error) {
	d, q := pr.dim(), pr.q
	bread := newSquare(d)
	for a := 0; a < d; a++ {
		for b := 0; b < d; b++ {
			bread[a][b] = inv.At(a, b)
		}
	}
	eta := make([]float64, len(pr.cats))
	pi := make([]float64, len(pr.cats))
	rows := make([]sandwichRow, pr.n)
	for i := 0; i < pr.n; i++ {
		pr.rowEval(beta, i, eta, pi)
		xi := pr.x[i*q : (i+1)*q]
		score := make([]float64, d)
		for k, ck := range pr.nonRef {
			yk := 0.0
			if pr.y[i] == ck {
				yk = 1
			}
			r := yk - pi[ck]
			for a := 0; a < q; a++ {
				score[k*q+a] = float64(r * xi[a])
			}
		}
		rows[i] = sandwichRow{score: score, weight: pr.prior[i]}
		if keys != nil {
			rows[i].cluster = keys[i].cluster
		}
	}
	fit, err := sandwich(e.spec, bread, rows, nStar)
	if err != nil {
		return nil, nil, err
	}
	return fit, robustStdErrors(fit), nil
}
