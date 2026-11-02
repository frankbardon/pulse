package regression

import (
	"math"

	"github.com/frankbardon/pulse/internal/collinearity"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// vif.go is the collinearity block (RegressionSpec.Collinearity, U65
// E3-S2): per term the variance inflation factor of the fit's predictor
// columns — Fox & Monette's generalized VIF for a group of columns — and
// its tolerance, read off a centered weighted cross-product C of the
// predictors and handed to the arithmetic MAT_COLLINEARITY shares
// (internal/collinearity: R = cov2cor(C), GVIF over R).
//
//	basis model (default):  C = the centered XᵀWX of the fit — W the
//	                        prior weights on REG_OLS, the prior × IRLS
//	                        working weights at the converged β on REG_GLM
//	                        (car::vif on the fit, whose vcov is that
//	                        matrix's inverse up to φ, which cov2cor drops)
//	basis predictors:       C = the prior-weighted centered cross-product
//	                        (car::vif on lm over the same predictors and
//	                        weights; MAT_COLLINEARITY's VIF). REG_OLS's
//	                        two bases are one matrix.
//
// The centering is the intercept's: the Schur complement of XᵀWX on its
// intercept is Σw(x − x̄_w)(x − x̄_w)ᵀ, x̄_w the W-weighted means. A
// probability weight's scale c = N*/Σw cancels in the correlation, so
// both weight kinds give the same figures, and a frequency weight's are
// the rep()-expanded fit's. The robust Covariance, the dispersion φ and
// a resample's replicate covariance never enter: the VIF is a property
// of the design (and, on REG_GLM, of its fitted weights), not of the
// error model.
//
// Execution: REG_OLS reads its streaming accumulator's centered moments
// (m2XX — the same Welford fold the fit reads), so the block streams and
// merges with the fit. REG_GLM takes one two-pass weighted pass over the
// fitted rows in record order after IRLS converges, so the figures are
// identical under every DecodeWorkers / ShardWorkers count. Every
// product is barriered (float64(a*b)).
//
// A correlation that is not positive definite in double precision (a
// collinearity the QR solve survived but the cross-product does not)
// nulls every figure — NaN, null on the wire — rather than refusing the
// fit, which stands.

// olsCollinearity is REG_OLS's block over its accumulator's centered
// moments m2XX (row-major p × p, upper triangle).
func olsCollinearity(spec *types.RegressionSpec, acc *olsAccumulator) *types.RegressionCollinearityResult {
	p := acc.p
	c, _ := linalg.NewSym(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			c.Set(i, j, acc.m2XX[i*p+j])
		}
	}
	return collinearityResult(spec, c)
}

// glmCollinearity is REG_GLM's block over the fitted design xRows (n × k
// row-major, intercept first) weighted by w — the working weights under
// basis model, the prior weights under basis predictors.
func glmCollinearity(spec *types.RegressionSpec, xRows, w []float64, k int) *types.RegressionCollinearityResult {
	return collinearityResult(spec, centeredCross(xRows, w, k))
}

// centeredCross is Σw·(x − x̄_w)(x − x̄_w)ᵀ over the predictor columns
// 1 … k−1 of xRows, two-pass: the weighted means first, then the
// centered products, rows in order.
func centeredCross(xRows, w []float64, k int) *linalg.Sym {
	p := k - 1
	mean := make([]float64, p)
	sw := 0.0
	for i := range w {
		row := xRows[i*k+1 : (i+1)*k]
		for a := 0; a < p; a++ {
			mean[a] += float64(w[i] * row[a])
		}
		sw += w[i]
	}
	for a := range mean {
		mean[a] /= sw
	}
	c, _ := linalg.NewSym(p, nil)
	d := make([]float64, p)
	acc := make([]float64, p*p)
	for i := range w {
		row := xRows[i*k+1 : (i+1)*k]
		for a := 0; a < p; a++ {
			d[a] = row[a] - mean[a]
		}
		for a := 0; a < p; a++ {
			wd := float64(w[i] * d[a])
			for b := a; b < p; b++ {
				acc[a*p+b] += float64(wd * d[b])
			}
		}
	}
	for a := 0; a < p; a++ {
		for b := a; b < p; b++ {
			c.Set(a, b, acc[a*p+b])
		}
	}
	return c
}

// collinearityResult renders the block over the centered cross-product
// c of the predictors (Predictors order).
func collinearityResult(spec *types.RegressionSpec, c *linalg.Sym) *types.RegressionCollinearityResult {
	terms := collinearity.Terms(spec)
	out := &types.RegressionCollinearityResult{
		Basis:     spec.Collinearity.EffectiveBasis(),
		Terms:     make([]string, len(terms)),
		DF:        make(map[string]int, len(terms)),
		VIF:       make(map[string]float64, len(terms)),
		Tolerance: make(map[string]float64, len(terms)),
	}
	grouped := len(spec.Collinearity.Groups) != 0
	if grouped {
		out.GVIFScaled = make(map[string]float64, len(terms))
	}
	cols := make([][]int, len(terms))
	for t, term := range terms {
		cols[t] = term.Columns
	}
	var gvif []float64
	if r, ok := collinearity.Correlation(c); ok {
		gvif, _ = collinearity.GVIF(r, cols)
	}
	for t, term := range terms {
		v := math.NaN()
		if gvif != nil {
			v = gvif[t]
		}
		out.Terms[t] = term.Name
		out.DF[term.Name] = len(term.Columns)
		out.VIF[term.Name] = v
		out.Tolerance[term.Name] = 1 / v
		if grouped {
			out.GVIFScaled[term.Name] = collinearity.Scaled(v, len(term.Columns))
		}
	}
	return out
}
