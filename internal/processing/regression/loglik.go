package regression

import (
	"math"

	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// loglik.go: the maximised log-likelihood and AIC a plain REG_OLS /
// REG_GLM fit reports (RegressionResult.TwoLogLik / AIC; U65 E2-S2,
// owner-delegated), each R 4.6.1's own definition:
//
//	REG_OLS    logLik.lm: ℓ = −N*/2·(log(2π·RSS/N*) + 1), df = rank + 1 (σ)
//	binomial   family$aic: ℓ = Σw·log dbinom(y; μ)        df = rank
//	poisson    family$aic: ℓ = Σw·log dpois(y; μ)         df = rank
//	gamma      family$aic: ℓ = Σw·log dgamma(y; 1/φ, μφ), φ = D/Σw,
//	           df = rank + 1 (φ counted, Gamma()$aic's + 2)
//	negbin     MASS glm.nb: negBinTwoLogLik, df = rank (+ 1 when θ is
//	           estimated)
//
// AIC = −2ℓ + 2·df; TwoLogLik = 2ℓ. The gamma likelihood is the one
// selection's gamma criterion scores (gammaLogLik, aicBICFromGamma) and
// the Gaussian one selection's OLS criterion (gaussianLogLik,
// aicBICFromOLS), so a reported AIC equals selection's score on the
// same rows.
//
// Weights: unweighted, and under kind frequency the rep() expansion's
// (frequency weights are integers; R's glm(weights = f) family aic is
// the expansion's too, logLik(lm(weights = f)) is not — it reads the
// weights as precision weights — so the OLS figure is the expanded
// lm's). Under kind probability the weighted likelihood is a
// pseudo-likelihood, not a likelihood, so no figure is set: the fields
// stay absent (attachLogLik's caller passes the basis). Penalized,
// bounds-constrained and Bayesian fits never call it.
//
// A poisson or binomial response that is not a count (non-integer y)
// has likelihood 0 in R (dpois / dbinom of a non-integer): ℓ = −Inf, so
// TwoLogLik is −Inf and AIC +Inf — null on the wire. An exact fit
// (RSS = 0) is ℓ = +Inf, AIC −Inf, likewise null.
//
// FMA-free: every product feeding an add or subtract is barriered.

// logLikDefined reports whether a fit on basis b reports a likelihood:
// unweighted or frequency-weighted, never probability-weighted.
func logLikDefined(b weighting.Basis) bool { return b != weighting.Probability }

// attachLogLik stamps 2ℓ and AIC = −2ℓ + 2·df on res.
func attachLogLik(res *types.RegressionResult, ll float64, df int) {
	res.TwoLogLik = float64(2 * ll)
	res.AIC = float64(-2*ll) + float64(2*df)
}

// gaussianLogLik is the maximised Gaussian log-likelihood at the MLE
// σ² = RSS/N*: −N*/2·log(2π·RSS/N*) − N*/2 (R's logLik.lm on unit
// weights).
func gaussianLogLik(rss, nStar float64) float64 {
	return float64(-0.5*nStar*detmath.Log(2*math.Pi*rss/nStar)) - float64(0.5*nStar)
}

// olsLogLik is a plain REG_OLS fit's ℓ and df: rank coefficients
// (p + 1, or p through the origin) plus σ.
func olsLogLik(rss, nStar float64, rank int) (float64, int) {
	if !(nStar > 0) {
		return math.NaN(), rank + 1
	}
	if rss <= 0 {
		return math.Inf(1), rank + 1
	}
	return gaussianLogLik(rss, nStar), rank + 1
}

// glmLogLik is a plain REG_GLM fit's ℓ and df at the fitted means mu:
// rows y with prior weights prior (nil = 1), deviance the fit's, rank
// its coefficient count, thetaEstimated a negative binomial whose θ was
// estimated (MASS counts it).
func glmLogLik(fam glmFamily, y, mu, prior []float64, deviance float64, rank int, theta float64, thetaEstimated bool) (float64, int) {
	w := func(i int) float64 {
		if prior == nil {
			return 1
		}
		return prior[i]
	}
	switch fam.Name {
	case "binomial":
		ll := 0.0
		for i := range y {
			ll += float64(w(i) * binomialLogDensity(y[i], mu[i]))
		}
		return ll, rank
	case "poisson":
		ll := 0.0
		for i := range y {
			ll += float64(w(i) * poissonLogDensity(y[i], mu[i]))
		}
		return ll, rank
	case "gamma":
		return gammaLogLik(y, mu, prior, deviance), rank + 1
	case reglink.NegativeBinomial:
		p := prior
		if p == nil {
			p = make([]float64, len(y))
			for i := range p {
				p[i] = 1
			}
		}
		df := rank
		if thetaEstimated {
			df++
		}
		return negBinTwoLogLik(theta, y, mu, p) / 2, df
	}
	return math.NaN(), rank
}

// binomialLogDensity is log dbinom(y; 1, μ) for a 0/1 response — log μ
// or log(1 − μ) — and −Inf for any other y (R's dbinom of a
// non-integer success count).
func binomialLogDensity(y, mu float64) float64 {
	switch y {
	case 1:
		return detmath.Log(mu)
	case 0:
		return detmath.Log(1 - mu)
	}
	return math.Inf(-1)
}

// poissonLogDensity is log dpois(y; μ) = y·log μ − μ − lgamma(y + 1),
// −μ at y = 0, −Inf for a negative or non-integer y (R's dpois).
func poissonLogDensity(y, mu float64) float64 {
	if y < 0 || y != math.Trunc(y) {
		return math.Inf(-1)
	}
	if y == 0 {
		return -mu
	}
	lg, _ := detmath.Lgamma(y + 1)
	return float64(y*detmath.Log(mu)) - mu - lg
}

// gammaLogLik is Σ w·log dgamma(y; shape 1/φ, scale μ·φ) at R's
// Gamma()$aic dispersion φ = D/N*, N* = Σw (n with prior nil).
func gammaLogLik(y, mu, prior []float64, deviance float64) float64 {
	nStar := float64(len(y))
	if prior != nil {
		nStar = sum(prior)
	}
	disp := deviance / nStar
	ll := 0.0
	for i := range y {
		w := 1.0
		if prior != nil {
			w = prior[i]
		}
		ll += float64(w * gammaLogDensity(y[i], mu[i], disp))
	}
	return ll
}
