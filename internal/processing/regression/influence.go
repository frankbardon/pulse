package regression

import "math"

// influence.go: the per-observation OLS influence figures behind
// ATTR_REG_RESIDUAL kind standardized / studentized and
// ATTR_REG_INFLUENCE (U65 E1-S2), R's rstandard / rstudent /
// cooks.distance / dffits on an unpenalized lm (statsmodels
// OLSInfluence). With p′ = p + 1 parameters, df = N* − p′, s² = RSS/df,
// h one observation's leverage and e = y − ŷ:
//
//	r      = e / (s·√(1 − h))             standardized (internal)
//	t      = r·√((df − 1)/(df − r²))      studentized (external)
//	cooks  = r²·h / (p′·(1 − h))
//	dffits = t·√(h/(1 − h))
//
// t is the leave-one-out form without a refit: deleting the
// observation leaves s²₍ᵢ₎ = (df − r²)·s²/(df − 1).
//
// Under a frequency weight a record is w identical observations
// (the rep() expansion) and each figure is ONE copy's: h is
// LeverageForRow at unit weight — 1/Σw + (x − μ_w)ᵀM2_w⁻¹(x − μ_w), the
// record's grouped hat divided by w — df = Σw − p′, RSS the weighted Σw·e².
// Unweighted that is w = 1 exactly. A probability weight is refused
// upstream (weighting.AttributeClassOf).
//
// FMA-free: every product feeding an add or subtract is wrapped in
// float64(...) (TestOLSQR_NoFMAContraction also gates this file and
// coeffs.go). The centered-Gram inverse is a gonum Cholesky inverse,
// so compare across architectures within tolerance, never bit for bit.

// RowInfluence is one observation's influence figures. A figure the
// fit leaves undefined — df ≤ 0 (standardized, cooks) or df ≤ 1
// (studentized, dffits), h ≥ 1, a zero residual variance — is NaN,
// never ±Inf.
type RowInfluence struct {
	Leverage     float64
	Standardized float64
	Studentized  float64
	Cooks        float64
	DFFITS       float64
}

// InfluenceForRow computes the influence figures of one observation
// with predictors x and residual e = y − ŷ, from an unpenalized fit
// that carries its centered-Gram inverse (FitForAttribute with
// needLeverage) and RSS / DF. Every figure is NaN when fit lacks them.
func InfluenceForRow(fit *AttributeFit, x []float64, e float64) RowInfluence {
	nan := math.NaN()
	out := RowInfluence{Leverage: nan, Standardized: nan, Studentized: nan, Cooks: nan, DFFITS: nan}
	if fit == nil || fit.GramInverse == nil || fit.NObs == 0 {
		return out
	}
	h := LeverageForRow(fit, x, 1)
	out.Leverage = h
	df := fit.DF
	if !(df > 0) || !(h < 1) {
		return out
	}
	p1 := float64(len(fit.PredictorOrder) + 1)
	s := math.Sqrt(fit.RSS / df)
	r := finiteOrNaN(e / float64(s*math.Sqrt(1-h)))
	out.Standardized = r
	r2 := float64(r * r)
	out.Cooks = finiteOrNaN(float64(r2*h) / float64(p1*(1-h)))
	if !(df > 1) {
		return out
	}
	t := finiteOrNaN(r * math.Sqrt((df-1)/(df-r2)))
	out.Studentized = t
	out.DFFITS = finiteOrNaN(t * math.Sqrt(h/(1-h)))
	return out
}

// finiteOrNaN maps ±Inf to NaN: an influence figure is undefined, not
// infinite, when its denominator vanishes.
func finiteOrNaN(v float64) float64 {
	if math.IsInf(v, 0) {
		return math.NaN()
	}
	return v
}
