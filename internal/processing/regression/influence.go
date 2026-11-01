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
// It is InfluenceFigures at the Gaussian identity: the Pearson residual
// is e, φ = s² = RSS/df estimated, s = +1.
func InfluenceForRow(fit *AttributeFit, x []float64, e float64) RowInfluence {
	nan := math.NaN()
	out := RowInfluence{Leverage: nan, Standardized: nan, Studentized: nan, Cooks: nan, DFFITS: nan}
	if fit == nil || fit.GramInverse == nil || fit.NObs == 0 {
		return out
	}
	h := LeverageForRow(fit, x, 1)
	df := fit.DF
	if !(df > 0) {
		out.Leverage = h
		return out
	}
	p1 := float64(len(fit.PredictorOrder) + 1)
	return InfluenceFigures(e, h, fit.RSS/df, df, p1, true, 1)
}

// InfluenceFigures is the ONE influence kernel (R 4.6.1's
// lm.influence / rstandard / rstudent / cooks.distance / dffits on an
// lm or a glm), shared by the OLS refit (InfluenceForRow) and a bound
// REG_OLS / REG_GLM fit (BoundInfluence). rP is the observation's
// Pearson residual (y − μ)/√V(μ), h its leverage, phi the dispersion φ
// (σ̂² on OLS), df the residual df n − p′, p1 = p′ the coefficient
// count, estimated whether φ is estimated (OLS, gaussian, gamma) or
// fixed at 1 (binomial, poisson), and sign the sign of g′(μ) (−1 under
// a decreasing link: gamma inverse):
//
//	standardized = rP / (√φ·√(1 − h))
//	σ₍ᵢ₎         = √((φ·df − rP²/(1 − h))/(df − 1))      φ estimated
//	studentized  = rP / (σ₍ᵢ₎·√(1 − h)), the standardized when φ is fixed
//	cooks        = (rP/(1 − h))²·h / (φ·p′)
//	dffits       = sign·rP·√h / (σ·(1 − h)), σ = σ₍ᵢ₎ (estimated) or √φ
//
// R's dffits reads the weighted working residual √w·(y − μ)·g′(μ) =
// sign·rP: the change on the linear-predictor scale. A figure the fit
// leaves undefined (h ≥ 1, df ≤ 1 where σ₍ᵢ₎ is read, a non-positive
// σ₍ᵢ₎², ±Inf) is NaN.
func InfluenceFigures(rP, h, phi, df, p1 float64, estimated bool, sign float64) RowInfluence {
	nan := math.NaN()
	out := RowInfluence{Leverage: h, Standardized: nan, Studentized: nan, Cooks: nan, DFFITS: nan}
	if !(h < 1) || !(phi > 0) {
		return out
	}
	omh := 1 - h
	out.Standardized = finiteOrNaN(rP / float64(math.Sqrt(phi)*math.Sqrt(omh)))
	q := rP / omh
	out.Cooks = finiteOrNaN(float64(float64(q*q)*h) / float64(phi*p1))
	sigma := math.Sqrt(phi)
	if estimated {
		if !(df > 1) {
			return out
		}
		s2 := (float64(phi*df) - float64(rP*rP)/omh) / (df - 1)
		if !(s2 > 0) {
			return out
		}
		sigma = math.Sqrt(s2)
		out.Studentized = finiteOrNaN(rP / float64(sigma*math.Sqrt(omh)))
	} else {
		out.Studentized = out.Standardized
	}
	out.DFFITS = finiteOrNaN(float64(sign*rP) * math.Sqrt(h) / float64(sigma*omh))
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
