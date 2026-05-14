package regression

import "math"

// aicBICFromOLS returns (AIC, BIC, logL) for an unpenalized / penalized
// OLS fit characterised by its residual sum of squares, observation
// count, and active-predictor count k0 (slopes only — the intercept and
// σ² estimate are added inside).
//
// Derivation. Under the OLS Gaussian-likelihood interpretation, the
// MLE for σ² is RSS/n and the maximised log-likelihood is
//
//	logL = -n/2 · log(2π · RSS/n) - n/2
//
// The information criteria adjust by the parameter count:
//
//	k    = k0 (slopes) + 1 (intercept) + 1 (σ² estimate)
//	AIC  = -2·logL + 2·k
//	BIC  = -2·logL + log(n) · k
//
// Selection compares models with the same target, so the
// k = (slopes + 2) bookkeeping is consistent across candidates. A
// degenerate RSS=0 fit (perfect interpolation) drives logL → +∞; we
// return -math.MaxFloat64 / 2 for AIC and BIC there so selection treats
// it as the best model without producing Inf-valued criteria that would
// poison downstream comparisons.
//
// Inputs:
//   - rss : residual sum of squares from the candidate fit.
//   - n   : observation count.
//   - k0  : number of *non-intercept* coefficients in the model.
//
// The intercept-only model (k0 = 0) is the natural baseline for forward
// selection; pass k0 = len(activeSlopes) for incremental candidates.
func aicBICFromOLS(rss float64, n int, k0 int) (aic, bic, logL float64) {
	if n <= 0 {
		return math.NaN(), math.NaN(), math.NaN()
	}
	nf := float64(n)
	if rss <= 0 {
		// Perfect fit (or rounding-induced zero). Treat as a strictly
		// better model than any finite-RSS candidate but keep the
		// criterion value bounded.
		const veryGoodCrit = -math.MaxFloat64 / 4
		return veryGoodCrit, veryGoodCrit, math.MaxFloat64 / 4
	}
	// Maximised Gaussian log-likelihood at MLE σ² = RSS/n.
	logL = -0.5*nf*math.Log(2*math.Pi*rss/nf) - 0.5*nf
	k := float64(k0 + 2) // slopes + intercept + σ²
	aic = -2*logL + 2*k
	bic = -2*logL + math.Log(nf)*k
	return aic, bic, logL
}

// aicBICFromGLM returns (AIC, BIC, logL-proxy) for a GLM fit summarised
// by its deviance, observation count, and active-predictor count k0
// (slopes only).
//
// For GLMs with fixed dispersion (binomial, poisson) the log-likelihood
// reduces to
//
//	logL = -Deviance/2 + C(y)
//
// where C(y) depends only on the response and therefore cancels in any
// model-comparison setting. We carry the per-row C(y) implicitly by
// dropping it: AIC differences between candidate models sharing the
// same target are unchanged, and selection cares only about
// differences. The reported AIC and BIC values are therefore
// *deviance-shifted* — interpret them as relative scores, not absolute.
//
// Parameter count:
//
//	k    = k0 (slopes) + 1 (intercept)
//
// No σ² term since the family fixes the dispersion. AIC = Deviance +
// 2·k; BIC = Deviance + log(n)·k. The returned logL is -Deviance/2 so
// the function shape (logL, AIC, BIC) parallels the OLS helper.
func aicBICFromGLM(deviance float64, n int, k0 int) (aic, bic, logL float64) {
	if n <= 0 {
		return math.NaN(), math.NaN(), math.NaN()
	}
	nf := float64(n)
	if deviance < 0 {
		// Tiny negatives from cancellation in the deviance accumulator;
		// clamp to zero and proceed.
		deviance = 0
	}
	logL = -0.5 * deviance
	k := float64(k0 + 1) // slopes + intercept (no dispersion estimate)
	aic = -2*logL + 2*k
	bic = -2*logL + math.Log(nf)*k
	return aic, bic, logL
}
