package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// negbin.go is REG_GLM's negative binomial family (NB2, Var = μ + μ²/θ;
// U65 E2-S2): θ estimated by maximum likelihood as MASS glm.nb does, or
// held at RegressionSpec.Theta as R's glm(family = negative.binomial(θ)).
//
// The estimate alternates (fitNegBin):
//
//  1. a poisson fit on the same link (glm.nb's initial fit);
//  2. θ ← thetaML(y, μ): Newton on the θ score at the fitted means
//     (MASS theta.ml), started at the moment estimate N*/Σw*(y/μ − 1)²
//     the first time and at the previous θ after;
//  3. β ← the IRLS fit at that θ, warm-started at the previous β;
//
// repeating 2–3 until a round moves θ by at most tol relative and β by
// a relative ||Δβ|| / (||β|| + ε) below tol — the IRLS convergence rule,
// on the same tol — then one more IRLS fit at the final θ, so β, μ, the
// deviances, the working weights and the vcov are all evaluated at the
// θ̂ the result reports. That is the joint MLE glm.nb approximates: its
// own stopping rule (theta.ml's Newton step below eps^0.25 ≈ 1.2e-4
// absolute, the alternation on a log-likelihood change of 1e-8) is far
// looser, so a glm.nb fit sits ~1e-6 off it (the oracle pushes glm.nb to
// the fixed point, glm_nb.json). θ's Newton runs to a relative step of
// thetaNewtonTol (quadratic convergence), never glm.nb's absolute 1e-4.
//
// β and θ are treated as orthogonal (MASS summary.negbin): the
// coefficient covariance is (XᵀWX)⁻¹ at θ̂ with the dispersion fixed at
// 1, W = w*·μ'²/(μ + μ²/θ̂), and SE(θ̂) = 1/√I(θ̂), I the observed
// information of θ at the fitted means. A robust covariance's sandwich
// holds θ fixed at θ̂ (as R's sandwich on a glm.nb fit does).
//
// Refusals, each PROCESSING_REGRESSION_NO_CONVERGE (a data-dependent
// slot failure) with details.reason: theta_unbounded (θ̂ grows past
// thetaUnbounded, or stalls past thetaLarge — the data show no
// over-dispersion, so the MLE lies at θ = ∞, the poisson fit),
// theta_not_finite (the score or θ became
// non-finite: a negative count, say), theta_max_iters (θ's Newton did
// not settle) and theta_alternation (the alternation did not settle
// within MaxIters rounds).
//
// FMA-free: every product feeding an add or subtract is wrapped in an
// explicit float64(...) barrier, and the special functions are detmath's
// (Log, Lgamma) or the portable series below (digamma, trigamma).

const (
	// thetaNewtonMaxIters bounds θ's Newton iterations (theta.ml's
	// limit); quadratic convergence needs a handful.
	thetaNewtonMaxIters = 100
	// thetaNewtonTol: θ's Newton has converged once a step moves θ by at
	// most this much relative.
	thetaNewtonTol = 1e-12
	// thetaStallTol: a step no smaller than the one before is the
	// round-off floor of the score, accepted once it is this small.
	thetaStallTol = 1e-9
	// thetaUnbounded: a θ̂ beyond it is the MLE at infinity (no
	// over-dispersion) — the negative binomial is then the poisson.
	thetaUnbounded = 1e10
	// thetaLarge: a Newton run that stalls with θ at least this large is
	// the same unbounded MLE, its score lost in round-off before θ
	// reached thetaUnbounded.
	thetaLarge = 1e6
)

// negBinFit is a converged negative binomial fit.
type negBinFit struct {
	sol *irlsSolution
	fam glmFamily // negBinFamily at theta
	// theta is θ̂ (estimated) or the fixed θ; thetaSE is 1/√I(θ̂) when
	// estimated, NaN when I(θ̂) ≤ 0, 0 when θ was fixed.
	theta, thetaSE float64
	estimated      bool
	// iters is every IRLS iteration the fit ran, the alternation's
	// rounds summed.
	iters int
}

// fitNegBin fits pr (its Family a negative binomial: the link) at a
// fixed θ (fixed > 0) or with θ estimated (fixed == 0) — see the file
// comment. pr's Prior must be set (FitBuffered's prior weights).
func fitNegBin(pr *irlsProblem, fixed float64) (*negBinFit, error) {
	link := pr.Family.Link
	if fixed > 0 {
		p := *pr
		p.Family = negBinFamily(link, fixed)
		sol, err := irlsFit(&p)
		if err != nil {
			return nil, err
		}
		return &negBinFit{sol: sol, fam: p.Family, theta: fixed, iters: sol.Iters}, nil
	}
	maxRounds := pr.MaxIters
	if maxRounds <= 0 {
		maxRounds = defaultGLMMaxIters
	}
	tol := pr.Tol
	if tol <= 0 {
		tol = defaultGLMTol
	}
	// 1. glm.nb's initial fit: poisson on the same link.
	p0 := *pr
	p0.Family = glmFamilies["poisson"][link]
	p0.WantInverse = false
	sol, err := irlsFit(&p0)
	if err != nil {
		return nil, asNegBin(err)
	}
	iters := sol.Iters
	theta, err := thetaML(pr, sol.Mu, 0)
	if err != nil {
		return nil, err
	}
	beta := sol.Beta
	for round := 0; ; round++ {
		if round == maxRounds {
			return nil, negBinNoConverge(pr, "theta_alternation", theta, iters)
		}
		p := *pr
		p.Family = negBinFamily(link, theta)
		p.Start, p.WantInverse = beta, false
		if sol, err = irlsFit(&p); err != nil {
			return nil, err
		}
		iters += sol.Iters
		next, err := thetaML(pr, sol.Mu, theta)
		if err != nil {
			return nil, err
		}
		done := math.Abs(next-theta) <= float64(tol*next) && relativeChange(sol.Beta, beta) < tol
		theta, beta = next, sol.Beta
		if done {
			break
		}
	}
	// The final IRLS fit at θ̂: β, μ, the deviance and (XᵀWX)⁻¹ at the
	// θ the result reports.
	p := *pr
	p.Family = negBinFamily(link, theta)
	p.Start = beta
	if sol, err = irlsFit(&p); err != nil {
		return nil, err
	}
	iters += sol.Iters
	se := math.NaN()
	if info := thetaInfo(theta, pr.Y, sol.Mu, pr.Prior); info > 0 {
		se = 1 / math.Sqrt(info)
	}
	return &negBinFit{sol: sol, fam: p.Family, theta: theta, thetaSE: se, estimated: true, iters: iters}, nil
}

// attachNegBin stamps a negative binomial fit's own figures on res: θ,
// SE(θ̂) when estimated, 2·log-likelihood and AIC (MASS glm.nb's aic
// −2ℓ + 2·rank + 2 counting θ; R's AIC(glm) −2ℓ + 2·rank when θ is
// fixed), and the IRLS iterations summed over the alternation.
func attachNegBin(res *types.RegressionResult, nb *negBinFit, y, mu, prior []float64) {
	res.Theta = nb.theta
	if nb.estimated {
		res.ThetaStdErr = nb.thetaSE
	}
	res.TwoLogLik = negBinTwoLogLik(nb.theta, y, mu, prior)
	params := float64(len(res.Coefficients))
	if nb.estimated {
		params++
	}
	res.AIC = -res.TwoLogLik + float64(2*params)
	res.ConvergedIters = nb.iters
}

// asNegBin restamps a refusal of the poisson start as the negative
// binomial fit's own (details.family), so a NO_CONVERGE or SEPARATION
// raised there names the family the request fitted.
func asNegBin(err error) error {
	if cerr, ok := err.(*errors.CodedError); ok && cerr.Details != nil {
		cerr.Details["family"] = reglink.NegativeBinomial
		cerr.Details["stage"] = "poisson_start"
	}
	return err
}

// negBinNoConverge is the PROCESSING_REGRESSION_NO_CONVERGE refusal of a
// θ that did not settle (the file comment's reasons).
func negBinNoConverge(pr *irlsProblem, reason string, theta float64, iters int) error {
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_NO_CONVERGE,
		"REG_GLM negative_binomial: the maximum-likelihood θ did not converge ("+reason+"); "+negBinRemedy(reason),
		map[string]any{
			"n": len(pr.Y), "p": pr.K - 1,
			"family": reglink.NegativeBinomial, "link": pr.Family.Link,
			"reason": reason, "theta": finiteOrNil(theta), "converged_iters": iters,
		},
	)
}

func negBinRemedy(reason string) string {
	if reason == "theta_unbounded" {
		return "the counts show no over-dispersion (θ̂ → ∞ is the poisson fit), so fit family poisson"
	}
	return "check the target holds non-negative counts, or fix theta"
}

// finiteOrNil is v, or nil when it is not finite (a details value must
// encode).
func finiteOrNil(v float64) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return v
}

// thetaML is the maximum-likelihood θ at the fitted means mu (MASS
// theta.ml): Newton on thetaScore with thetaInfo, from start (> 0) or,
// with start 0, the moment estimate N*/Σw*(y/μ − 1)². |θ| is taken
// before every step, as theta.ml does; a step where the information is
// not positive (the log-likelihood not concave there) doubles or halves
// θ toward the score's root instead.
func thetaML(pr *irlsProblem, mu []float64, start float64) (float64, error) {
	y, w := pr.Y, pr.Prior
	t := start
	if !(t > 0) {
		n, s := 0.0, 0.0
		for i := range y {
			r := y[i]/mu[i] - 1
			n += w[i]
			s += float64(w[i] * float64(r*r))
		}
		t = n / s
	}
	prev := math.Inf(1)
	for it := 0; it < thetaNewtonMaxIters; it++ {
		if math.IsNaN(t) || math.IsInf(t, 0) || t == 0 {
			return 0, negBinNoConverge(pr, "theta_not_finite", t, 0)
		}
		t = math.Abs(t)
		if t > thetaUnbounded {
			return 0, negBinNoConverge(pr, "theta_unbounded", t, 0)
		}
		sc := thetaScore(t, y, mu, w)
		info := thetaInfo(t, y, mu, w)
		if math.IsNaN(sc) || math.IsNaN(info) {
			return 0, negBinNoConverge(pr, "theta_not_finite", t, 0)
		}
		if !(info > 0) {
			if sc > 0 {
				t *= 2
			} else {
				t /= 2
			}
			continue
		}
		del := sc / info
		t += del
		ad := math.Abs(del)
		if t > 0 && (ad <= float64(thetaNewtonTol*t) || ad >= prev && ad <= float64(thetaStallTol*t)) {
			if t > thetaUnbounded {
				return 0, negBinNoConverge(pr, "theta_unbounded", t, 0)
			}
			return t, nil
		}
		prev = ad
	}
	if t >= thetaLarge {
		// The score of an under-dispersed sample stays positive while θ
		// climbs until it is round-off noise, which then stalls Newton:
		// the same MLE at infinity.
		return 0, negBinNoConverge(pr, "theta_unbounded", t, 0)
	}
	return 0, negBinNoConverge(pr, "theta_max_iters", t, 0)
}

// thetaScore is ∂ℓ/∂θ, summed with the prior weights (theta.ml's score):
// Σ w·[ψ(θ + y) − ψ(θ) + log θ + 1 − log(θ + μ) − (y + θ)/(μ + θ)].
func thetaScore(theta float64, y, mu, w []float64) float64 {
	s := 0.0
	lt, dt := detmath.Log(theta), digamma(theta)
	for i := range y {
		term := digamma(theta+y[i]) - dt + lt + 1 - detmath.Log(theta+mu[i]) - (y[i]+theta)/(mu[i]+theta)
		s += float64(w[i] * term)
	}
	return s
}

// thetaInfo is −∂²ℓ/∂θ² (theta.ml's info): Σ w·[−ψ′(θ + y) + ψ′(θ) −
// 1/θ + 2/(μ + θ) − (y + θ)/(μ + θ)²].
func thetaInfo(theta float64, y, mu, w []float64) float64 {
	s := 0.0
	tt := trigamma(theta)
	for i := range y {
		m := mu[i] + theta
		term := -trigamma(theta+y[i]) + tt - 1/theta + 2/m - (y[i]+theta)/float64(m*m)
		s += float64(w[i] * term)
	}
	return s
}

// negBinTwoLogLik is twice the prior-weighted log-likelihood at θ and
// the fitted means (MASS glm.nb's loglik):
// 2·Σ w·[lgamma(θ + y) − lgamma(θ) − lgamma(y + 1) + θ·log θ +
// y·log(μ + (y = 0)) − (θ + y)·log(θ + μ)].
func negBinTwoLogLik(theta float64, y, mu, w []float64) float64 {
	lgt, _ := detmath.Lgamma(theta)
	tlt := float64(theta * detmath.Log(theta))
	s := 0.0
	for i := range y {
		yi := y[i]
		a, _ := detmath.Lgamma(theta + yi)
		b, _ := detmath.Lgamma(yi + 1)
		m := mu[i]
		if yi == 0 {
			m++
		}
		term := a - lgt - b + tlt + float64(yi*detmath.Log(m)) - float64((theta+yi)*detmath.Log(theta+mu[i]))
		s += float64(w[i] * term)
	}
	return 2 * s
}

// digammaShift is the argument digamma / trigamma recur up to before
// their asymptotic series, which is then accurate to under 1e-16
// relative.
const digammaShift = 12

// digamma is ψ(x) for x > 0: the recurrence ψ(x) = ψ(x + 1) − 1/x up to
// x ≥ digammaShift, then the asymptotic series
// log x − 1/(2x) − Σ B₂ₖ/(2k·x²ᵏ) through k = 7. NaN for x ≤ 0 (never
// read: θ > 0, y ≥ 0).
func digamma(x float64) float64 {
	if !(x > 0) || math.IsInf(x, 1) {
		if math.IsInf(x, 1) {
			return x
		}
		return math.NaN()
	}
	acc := 0.0
	for x < digammaShift {
		acc += 1 / x
		x++
	}
	z := 1 / float64(x*x)
	// B₂ₖ/(2k): 1/12, −1/120, 1/252, −1/240, 1/132, −691/32760, 1/12.
	s := 1.0 / 12
	s = 691.0/32760 - float64(z*s)
	s = 1.0/132 - float64(z*s)
	s = 1.0/240 - float64(z*s)
	s = 1.0/252 - float64(z*s)
	s = 1.0/120 - float64(z*s)
	s = 1.0/12 - float64(z*s)
	return detmath.Log(x) - 0.5/x - float64(z*s) - acc
}

// trigamma is ψ′(x) for x > 0: the recurrence ψ′(x) = ψ′(x + 1) + 1/x²
// up to x ≥ digammaShift, then 1/x + 1/(2x²) + Σ B₂ₖ/x²ᵏ⁺¹ through
// k = 8. NaN for x ≤ 0.
func trigamma(x float64) float64 {
	if !(x > 0) || math.IsInf(x, 1) {
		if math.IsInf(x, 1) {
			return 0
		}
		return math.NaN()
	}
	acc := 0.0
	for x < digammaShift {
		acc += 1 / float64(x*x)
		x++
	}
	z := 1 / float64(x*x)
	// B₂ₖ: 1/6, −1/30, 1/42, −1/30, 5/66, −691/2730, 7/6, −3617/510.
	s := 3617.0 / 510
	s = 7.0/6 - float64(z*s)
	s = 691.0/2730 - float64(z*s)
	s = 5.0/66 - float64(z*s)
	s = 1.0/30 - float64(z*s)
	s = 1.0/42 - float64(z*s)
	s = 1.0/30 - float64(z*s)
	s = 1.0/6 - float64(z*s)
	return 1/x + float64(0.5*z) + float64(float64(z/x)*s) + acc
}
