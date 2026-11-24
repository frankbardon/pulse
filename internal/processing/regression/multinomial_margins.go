package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/internal/regmargins"
	"github.com/frankbardon/pulse/internal/regmultinom"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// multinomial_margins.go is REG_MULTINOMIAL's marginal effects
// (RegressionSpec.Margins) and joint term tests (RegressionSpec.TermTests),
// U68 E4-S2, both read off a converged baseline-category fit.
//
// Margins, per category c and term j, with π_ic the row's category
// probabilities, β_c the category's coefficients (β_r = 0 for the
// reference) and b̄_ij = Σ_m π_im·β_mj:
//
//	response: ∂π_c/∂x_j = π_c·(β_cj − b̄_j)
//	          ame the prior-weighted mean over the fitted rows, mem at the
//	          weighted predictor means x̄ (Stata's atmeans), one effect
//	          per fitted category (the K effects of a term sum to 0);
//	link:     ∂η_c/∂x_j = β_cj — the category's log-odds against the
//	          reference, so the effect is the coefficient and its
//	          standard error the coefficient's (J = e exactly), one per
//	          non-reference category.
//
// The delta-method gradient of a row's response effect in β_la (block
// l, design column a) is
//
//	x_ia·π_ic·[(δ_cl − π_il)(β_cj − b̄_ij) − π_il(β_lj − b̄_ij)]
//	  + δ_aj·π_ic·(δ_cl − π_il)
//
// and Var = J·V·Jᵀ on the fit's own covariance (the robust sandwich, or
// the inverse information). Inference is the coefficient table's Wald z
// at the fit's conf_level. Keys are "<category>:<term>" with Outcomes
// the categories in fitted (dictionary) order. The penalty, selection
// and resample are refused by internal/regmargins (no delta-method
// distribution), so the block always reads the plain MLE. marginaleffects
// avg_slopes / slopes(type = "probs") on multinom is the oracle
// (gen_margins.R).
//
// binary contrast (U69 #641): a 0/1 term reports, per category,
// π_c(x_j=1) − π_c(x_j=0) (other predictors as observed under ame, at x̄
// under mem), gradient Σw·[x1_a·π_c(x1)(δ_cl − π_l(x1)) −
// x0_a·π_c(x0)(δ_cl − π_l(x0))]/Σw in β_la; on the link scale the
// log-odds contrast is the coefficient (multinomialContrast).
//
// Term tests, per predictor j over its K − 1 coefficients b_j (one per
// non-reference block): the Wald χ² b_jᵀ·V_jj⁻¹·b_j on the fit's
// covariance, and the likelihood-ratio χ² — the deviance of the refit
// without j (same rows, categories and reference; Newton from the
// intercept-only MLE) less the fit's — car::Anova(multinom) type II;
// each on K − 1 degrees of freedom. The LR figures are absent under a
// probability weight (no likelihood, as two_log_lik).
//
// FMA-free like the fit: every product feeding a sum is barriered.

// multinomialMargins is the block of a converged fit pr at beta, v the
// coefficient covariance (d × d, the keys' order) the standard errors
// read, res the fit's result (its coefficient table, which the link
// scale reports).
func (e *multinomialEngine) multinomialMargins(pr *multinomialProblem, beta []float64, v [][]float64, res *types.RegressionResult) *types.RegressionMarginsResult {
	spec := e.spec
	inf := marginsInference{level: resolvedConfLevel(spec)}
	terms := regmargins.Terms(spec)
	out := newMarginsResult(spec, terms, inf)
	q, kk, d := pr.q, len(pr.nonRef), pr.dim()
	mem := out.Type == types.MarginsMEM
	var xbar []float64
	if mem {
		xbar = designMeans(pr.x, pr.prior, q)
		out.At = make(map[string]float64, len(spec.Predictors))
		for j, p := range spec.Predictors {
			out.At[p] = xbar[j+1]
		}
	}
	out.Contrasts = contrastTerms(spec, terms, designBinary(pr.x, q))
	if out.Scale == types.MarginsScaleLink {
		for _, c := range pr.nonRef {
			out.Outcomes = append(out.Outcomes, pr.cats[c])
		}
		for _, c := range out.Outcomes {
			for _, t := range terms {
				key := regmultinom.Key(c, t)
				setMargin(out, regmargins.Key(c, t), res.Coefficients[key], res.StdErrors[key], inf)
			}
		}
		return out
	}
	out.Outcomes = slices.Clone(pr.cats)

	nc := len(pr.cats)
	// block[c] is category c's block index (−1 for the reference).
	block := make([]int, nc)
	for c := range block {
		block[c] = -1
	}
	for k, c := range pr.nonRef {
		block[c] = k
	}
	coef := func(c, a int) float64 {
		if block[c] < 0 {
			return 0
		}
		return beta[block[c]*q+a]
	}
	cols := make([]int, len(terms))
	for t, name := range terms {
		cols[t] = slices.Index(spec.Predictors, name) + 1
	}
	// est[t][c], jac[t][c][d] accumulate the weighted sums.
	est := make([][]float64, len(terms))
	jac := make([][][]float64, len(terms))
	for t := range terms {
		est[t] = make([]float64, nc)
		jac[t] = make([][]float64, nc)
		for c := range jac[t] {
			jac[t][c] = make([]float64, d)
		}
	}
	eta := make([]float64, nc)
	pi := make([]float64, nc)
	// accumulate adds one evaluation point x (q, intercept first) at
	// weight w.
	accumulate := func(x []float64, w float64) {
		for c := range eta {
			eta[c] = 0
		}
		for k, c := range pr.nonRef {
			b := beta[k*q : (k+1)*q]
			eta[c] = LinearPredictor(b[0], b[1:], x[1:])
		}
		multinomialSoftmax(eta, pi)
		for t, a0 := range cols {
			bbar := 0.0
			for m := 0; m < nc; m++ {
				bbar += float64(pi[m] * coef(m, a0))
			}
			for c := 0; c < nc; c++ {
				dc := coef(c, a0) - bbar
				wpc := float64(w * pi[c])
				est[t][c] += float64(wpc * dc)
				row := jac[t][c]
				for l := 0; l < kk; l++ {
					cl := pr.nonRef[l]
					delta := 0.0
					if cl == c {
						delta = 1
					}
					g := delta - pi[cl]
					// A is the x_ia coefficient, B the δ_aj one.
					aCoef := float64(wpc * (float64(g*dc) - float64(pi[cl]*(coef(cl, a0)-bbar))))
					bCoef := float64(wpc * g)
					base := l * q
					for a := 0; a < q; a++ {
						row[base+a] += float64(aCoef * x[a])
					}
					row[base+a0] += bCoef
				}
			}
		}
	}
	if mem {
		accumulate(xbar, 1)
	} else {
		sw := 0.0
		for i := 0; i < pr.n; i++ {
			accumulate(pr.x[i*q:(i+1)*q], pr.prior[i])
			sw += pr.prior[i]
		}
		for t := range est {
			for c := range est[t] {
				est[t][c] /= sw
				for a := range jac[t][c] {
					jac[t][c][a] /= sw
				}
			}
		}
	}
	for t, name := range terms {
		if slices.Contains(out.Contrasts, name) {
			est[t], jac[t] = multinomialContrast(pr, beta, xbar, cols[t])
		}
	}
	for c, cat := range pr.cats {
		for t, name := range terms {
			setMargin(out, regmargins.Key(cat, name), est[t][c], math.Sqrt(quadForm(v, jac[t][c])), inf)
		}
	}
	return out
}

// multinomialContrast is the response-scale discrete contrast of design
// column a0 (0/1) of a converged fit pr at beta, per category
// (est[c] = π_c(x_a0=1) − π_c(x_a0=0)) with its gradient in β (jac[c],
// the coefficient keys' order): averaged over the fitted rows with their
// prior weights, or at the single point xbar when non-nil (mem). A pure
// function of its arguments.
func multinomialContrast(pr *multinomialProblem, beta, xbar []float64, a0 int) ([]float64, [][]float64) {
	q, kk, d, nc := pr.q, len(pr.nonRef), pr.dim(), len(pr.cats)
	est := make([]float64, nc)
	jac := make([][]float64, nc)
	for c := range jac {
		jac[c] = make([]float64, d)
	}
	eta := make([]float64, nc)
	pi := make([]float64, nc)
	pt := make([]float64, q)
	// side adds sign·w·π_c(pt) to est and its gradient to jac, at the
	// point pt.
	side := func(sign, w float64) {
		for c := range eta {
			eta[c] = 0
		}
		for k, c := range pr.nonRef {
			b := beta[k*q : (k+1)*q]
			eta[c] = LinearPredictor(b[0], b[1:], pt[1:])
		}
		multinomialSoftmax(eta, pi)
		sw := float64(sign * w)
		for c := 0; c < nc; c++ {
			wpc := float64(sw * pi[c])
			est[c] += wpc
			row := jac[c]
			for l := 0; l < kk; l++ {
				delta := 0.0
				if pr.nonRef[l] == c {
					delta = 1
				}
				g := float64(wpc * (delta - pi[pr.nonRef[l]]))
				base := l * q
				for a := 0; a < q; a++ {
					row[base+a] += float64(g * pt[a])
				}
			}
		}
	}
	add := func(x []float64, w float64) {
		copy(pt, x)
		pt[a0] = 1
		side(1, w)
		pt[a0] = 0
		side(-1, w)
	}
	if xbar != nil {
		add(xbar, 1)
		return est, jac
	}
	sw := 0.0
	for i := 0; i < pr.n; i++ {
		add(pr.x[i*q:(i+1)*q], pr.prior[i])
		sw += pr.prior[i]
	}
	for c := range est {
		est[c] /= sw
		for a := range jac[c] {
			jac[c][a] /= sw
		}
	}
	return est, jac
}

// quadForm is J·V·Jᵀ for a row gradient j, clamped at 0.
func quadForm(v [][]float64, j []float64) float64 {
	s := 0.0
	for a := range j {
		row := 0.0
		for b := range j {
			row += float64(v[a][b] * j[b])
		}
		s += float64(j[a] * row)
	}
	if s < 0 {
		return 0
	}
	return s
}

// multinomialTermTests is the joint test of every predictor of a
// converged fit pr at beta: v the coefficient covariance the Wald
// statistic reads, ll the fit's log-likelihood and withLR whether the
// likelihood is defined (no probability weight).
func (e *multinomialEngine) multinomialTermTests(pr *multinomialProblem, beta []float64, v [][]float64, ll float64, withLR bool) *types.RegressionTermTests {
	spec := e.spec
	q, kk := pr.q, len(pr.nonRef)
	out := &types.RegressionTermTests{
		Terms:       slices.Clone(spec.Predictors),
		DF:          make(map[string]int, len(spec.Predictors)),
		WaldChi2:    make(map[string]float64, len(spec.Predictors)),
		WaldPValues: make(map[string]float64, len(spec.Predictors)),
	}
	if withLR {
		out.LRChi2 = make(map[string]float64, len(spec.Predictors))
		out.LRPValues = make(map[string]float64, len(spec.Predictors))
	}
	df := float64(kk)
	for j, name := range spec.Predictors {
		a := j + 1
		idx := make([]int, kk)
		b := make([]float64, kk)
		for k := range idx {
			idx[k] = k*q + a
			b[k] = beta[idx[k]]
		}
		sub := make([]float64, kk*kk)
		for r, ir := range idx {
			for c, ic := range idx {
				sub[r*kk+c] = v[ir][ic]
			}
		}
		w := math.NaN()
		if f, ok := factorSPD(kk, sub); ok {
			if x, err := solveSPD(f, b); err == nil {
				w = 0
				for k := range b {
					w += float64(b[k] * x[k])
				}
			}
		}
		out.DF[name] = kk
		out.WaldChi2[name] = w
		out.WaldPValues[name] = chiSquareUpper(w, df)
		if withLR {
			lr := e.dropTermLR(pr, j, ll)
			out.LRChi2[name] = lr
			out.LRPValues[name] = chiSquareUpper(lr, df)
		}
	}
	return out
}

// chiSquareUpper is the upper χ²(df) tail of stat (NaN when stat is).
func chiSquareUpper(stat, df float64) float64 {
	if math.IsNaN(stat) {
		return math.NaN()
	}
	if stat < 0 {
		stat = 0
	}
	return statdist.ChiSquareSurvival(stat, df)
}

// dropTermLR is the likelihood-ratio statistic of predictor j: the refit
// of pr without design column j + 1 (the same rows, weights, categories
// and reference), 2·(ll − ℓ_reduced); NaN when the refit fails.
func (e *multinomialEngine) dropTermLR(pr *multinomialProblem, j int, ll float64) float64 {
	q := pr.q
	drop := j + 1
	x := make([]float64, 0, pr.n*(q-1))
	for i := 0; i < pr.n; i++ {
		row := pr.x[i*q : (i+1)*q]
		x = append(x, row[:drop]...)
		x = append(x, row[drop+1:]...)
	}
	inner := *e.spec
	inner.Predictors = slices.Delete(slices.Clone(e.spec.Predictors), j, j+1)
	sub := &multinomialEngine{spec: &inner, levels: e.levels}
	reduced := &multinomialProblem{x: x, y: pr.y, prior: pr.prior, n: pr.n, q: q - 1, cats: pr.cats, ref: pr.ref, nonRef: pr.nonRef}
	beta, _, err := sub.newton(reduced)
	if err != nil {
		return math.NaN()
	}
	lr := float64(2 * (ll - reduced.logLik(beta)))
	if lr < 0 {
		// The nested fit's optimum cannot beat the full one's; a
		// round-off negative is zero.
		lr = 0
	}
	return lr
}
