package model

// RegressionFigures are the whole-fit figures a Compose nested-model
// comparison (types.ComposedRequest.Compare, internal/regcompare) reads
// off a REG_OLS / REG_GLM / REG_MULTINOMIAL fit — or an
// extension-shaped model's whose declaration carries Compare
// (extend.CompareRegression, mapped by internal/regadapt: dispersion 1,
// WaldDF 0) — beyond its wire result. They never reach a result or the
// public API: a fit request a comparison names runs
// with a VectorCapture on its bound set (Set.WithCapture), and the
// regression engine offers its figures there at finalize
// (VectorCapture.OfferRegressionFigures) — the same handle a vector or
// SEM fit closes its state through, so every arm that runs the request
// offers through one channel. A run without a capture computes none.
type RegressionFigures struct {
	// Terms are the fitted coefficients in Covariance order:
	// "(intercept)" when the fit has one, then the predictors (a
	// declared model's own order: REG_ORDINAL's predictors, then its
	// thresholds; REG_MULTINOMIAL's "<category>:<term>" keys,
	// category-major).
	Terms []string

	// TermKeys maps a term to its coefficient keys in Terms where a term
	// fits more than one coefficient — REG_MULTINOMIAL's one
	// "<category>:<term>" key per non-reference category, so a nested
	// comparison tests (K − 1)·q coefficients for q added predictors.
	// Nil when every term is its own key (REG_OLS, REG_GLM, a declared
	// model's).
	TermKeys map[string][]string

	// Coefficients are the estimates aligned with Terms.
	Coefficients []float64

	// Covariance is the coefficient covariance the standard errors read
	// (classical φ̂·(XᵀWX)⁻¹ or the robust sandwich), square over Terms.
	Covariance [][]float64

	// WaldDF is the df of the coefficient table's Student t (its
	// RegressionResult.DF; a cluster-robust REG_OLS fit's G − 1); 0 when
	// the table is Wald z.
	WaldDF float64

	// Deviance is the residual deviance: the weighted RSS on REG_OLS,
	// RegressionResult.Deviance on REG_GLM.
	Deviance float64

	// DFResidual is the residual df N* − rank.
	DFResidual float64

	// Dispersion is the estimated dispersion φ̂: σ̂² = RSS/DFResidual on
	// REG_OLS, the Pearson χ²/DFResidual on a REG_GLM family that
	// estimates it (computed under a robust covariance too); 1 on a
	// fixed-dispersion family. NaN when DFResidual ≤ 0.
	Dispersion float64
}

// OfferRegressionFigures records a regression fit's comparison figures.
// Nil-receiver-safe (a set without a capture ignores the offer).
func (c *VectorCapture) OfferRegressionFigures(f *RegressionFigures) {
	if c == nil || f == nil {
		return
	}
	c.mu.Lock()
	c.figures = append(c.figures, f)
	c.mu.Unlock()
}

// RegressionFigures returns the offered regression figures in offer
// order.
func (c *VectorCapture) RegressionFigures() []*RegressionFigures {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*RegressionFigures(nil), c.figures...)
}
