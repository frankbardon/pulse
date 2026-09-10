package synth

import "math"

// This file gives a captured Gaussian mixture (DistMixture, written by
// `profile create --fit-shape`) the two things the Gaussian-copula
// construction in synth/copula.go demands of any marginal it drives:
// closed-form moments, and a quantile function Q.
//
// # Why a shape-fitted field needs a Q at all
//
// A modelled numeric is drawn as value = Q(Phi(mu(row) + sigma*z))
// (synth/model_draw.go). Q is the ONLY place the field's marginal
// shape enters, which is exactly what makes shape and conditioning
// compose rather than compete: the predictors move the latent Gaussian
// argument, and Q maps that latent back onto whatever distribution the
// field is supposed to have. `lognormal` already rides this — a
// lognormal target keeps its skew while its linear predictor drives it
// — and a fitted mixture is the same trick with a harder Q.
//
// Before E4-S1 the two were held apart by force: resolveConflicts
// pre-claimed every DistMixture field before any conditional stage
// could bid, so a `--fit-shape` numeric accepted no predictors at all.
// On the motivating cohort that silently stripped every conditioning
// relationship from the four fields whose shapes were most worth
// fitting. The exclusivity was never a statement about the
// construction; it was a statement about this file not existing.
//
// # The cost: effects are LATENT-scale, not value-scale
//
// A coefficient shifts mu, which lives on the standard-normal latent
// scale, and the value the row carries is Q of that shifted latent. For
// a plain `normal` Q the two scales coincide — Q(u) = mean + std*u is
// affine, so a coefficient of +30 moves the drawn value by exactly +30
// — but for a NON-normal Q (a mixture, a lognormal) the map is
// non-linear, so the same coefficient moves the value by an amount that
// depends on where in the distribution the row landed. Under a bimodal
// mixture a latent shift near the trough between the modes moves mass
// from one mode to the other and barely moves the values inside either;
// the same shift out in a tail moves the value a lot. The realized
// group means still differ in the captured DIRECTION, and by an amount
// monotone in the coefficient, but a reader must NOT read a coefficient
// as "this many units of the field".
//
// That is the honest cost of preserving the fitted marginal, and it was
// chosen deliberately. The alternative — applying the effect in value
// space, i.e. drawing from the mixture and then adding the linear
// prediction — was considered and rejected: it destroys the fitted
// shape it was supposed to protect (adding a level-dependent offset to
// a bimodal draw smears both modes across the offsets, and the
// aggregate marginal is no longer the mixture that was fitted), which
// is the exact failure this story exists to prevent. Do not quietly
// re-introduce it. E6-S1 surfaces the non-linearity to users; this
// comment is its statement in code.

// mixtureQuantileBisections is the FIXED number of bisection steps
// mixtureComponents.quantile takes. It is fixed, and there is no
// convergence test, because determinism is a hard contract here: the
// same spec and seed must produce a byte-identical .pulse file, and a
// "loop until |F(x)-p| < eps" inverse makes the answer depend on how
// many steps that particular p happened to need — still deterministic
// on one machine, but a fragile thing to promise across builds, and
// impossible to reason about when the tolerance interacts with a
// component std spanning several orders of magnitude. A fixed count is
// a pure function of its inputs by construction.
//
// 64 is chosen to exhaust float64 rather than to hit a tolerance. Each
// step halves the bracket, so after k steps the interval is
// width/2^k; the bracket below spans at most a few hundred data units
// for any realistic fit, and 2^64 divides that far below the ULP of the
// endpoints. The last handful of steps therefore return the same
// midpoint over and over — deliberately wasted work in exchange for an
// inverse that is exact to the last representable bit and never varies.
const mixtureQuantileBisections = 64

// mixtureQuantileTailStds sets the bisection bracket at
// min(mu_i) - k*max(sigma_i) .. max(mu_i) + k*max(sigma_i). At k = 40
// the standard normal CDF has underflowed to exactly 0 and rounded to
// exactly 1 respectively (Phi(-40) is ~1e-350, below the smallest
// normal float64), so the bracket provably encloses every p in [0,1]
// and no root can escape it — which is what lets the bisection dispense
// with a bracket-expansion loop whose iteration count would depend on
// the data.
const mixtureQuantileTailStds = 40

// moments returns the mixture's analytic mean and standard deviation —
// the law-of-total-variance form, mean = sum(w_i mu_i) and
// E[X^2] = sum(w_i (sigma_i^2 + mu_i^2)), over weights normalised here
// (see mixtureComponents on why they are not normalised at parse time).
//
// These are exact for the mixture, not an approximation of it, which is
// what makes DistMixture a legitimate member of fieldMoments' supported
// set rather than the "quietly distorts any distribution a little"
// shape that function's own doc comment refuses.
func (mc mixtureComponents) moments() (mean, std float64) {
	var m, m2 float64
	for i := range mc.means {
		w := mc.weights[i] / mc.weightTotal
		m += w * mc.means[i]
		m2 += w * (mc.stds[i]*mc.stds[i] + mc.means[i]*mc.means[i])
	}
	variance := m2 - m*m
	if variance < 0 {
		// Catastrophic cancellation only: E[X^2] >= (E[X])^2 always, so
		// a negative here is float noise on a mixture whose components
		// sit far from zero relative to their spread. Zero is the
		// truthful reading, and buildModelDrawers refuses a zero scale
		// by name rather than dividing by it.
		variance = 0
	}
	return m, math.Sqrt(variance)
}

// cdf is F(x) for the mixture: the weight-averaged component CDFs,
// using the same phi (synth/copula.go) the copula's forward direction
// uses, so the inverse below and the forward map cannot disagree about
// what the standard normal CDF is.
func (mc mixtureComponents) cdf(x float64) float64 {
	var acc float64
	for i := range mc.means {
		acc += (mc.weights[i] / mc.weightTotal) * phi((x-mc.means[i])/mc.stds[i])
	}
	return acc
}

// bracket returns the fixed [lo, hi] interval mixtureQuantileTailStds
// describes, computed once at construction rather than per row.
func (mc mixtureComponents) bracket() (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	maxStd := 0.0
	for i := range mc.means {
		if mc.means[i] < lo {
			lo = mc.means[i]
		}
		if mc.means[i] > hi {
			hi = mc.means[i]
		}
		if mc.stds[i] > maxStd {
			maxStd = mc.stds[i]
		}
	}
	pad := mixtureQuantileTailStds * maxStd
	return lo - pad, hi + pad
}

// quantile inverts cdf at p by fixed-count bisection over [lo, hi].
//
// Bisection rather than Newton: a mixture CDF is smooth and strictly
// increasing, so Newton would converge in a handful of steps — but its
// step count depends on the starting point and on the local density,
// and in the flat region between two well-separated modes the
// derivative underflows and the step explodes. Bisection cannot leave
// the bracket, cannot diverge, and takes the identical number of
// identical arithmetic operations for every p, which is the property
// this construction is being bought for.
//
// p outside [0,1] needs no special case: F is bounded by 0 and 1 on the
// bracket, so the loop simply walks to the corresponding endpoint.
func (mc mixtureComponents) quantile(p, lo, hi float64) float64 {
	for i := 0; i < mixtureQuantileBisections; i++ {
		mid := lo + (hi-lo)/2
		if mc.cdf(mid) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo + (hi-lo)/2
}
