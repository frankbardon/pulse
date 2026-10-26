package mmmpulse

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
)

// bayes_prior.go is REG_MMM_BAYES's prior families: the log density of
// each (normalised, so a prior's value is comparable across families),
// its derivative, the transform that maps the real line onto its
// support, and a central value for the default start. Every product
// feeding an add is float64()-barriered and the transcendentals are
// detmath's, so a density's bits are the same on every architecture.

// The prior families, as the params wire names them.
const (
	familyNormal      = "normal"      // Normal(A = μ, B = σ) on R
	familyHalfNormal  = "halfnormal"  // HalfNormal(B = σ) on (0, ∞)
	familyLogNormal   = "lognormal"   // LogNormal(A = μ, B = σ) on (0, ∞), μ σ of log x
	familyBeta        = "beta"        // Beta(A = α, B = β) on (0, 1)
	familyTruncNormal = "truncnormal" // Normal(A = μ, B = σ) truncated to (Lo, Hi)
)

// support is the set a parameter lives on.
type support int

const (
	supportReal     support = iota // the real line: intercept, controls
	supportPositive                // (0, ∞): k, s, ROI, β, σ
	supportUnit                    // (0, 1): adstock decay
)

func (s support) String() string {
	switch s {
	case supportPositive:
		return "(0, ∞)"
	case supportUnit:
		return "(0, 1)"
	}
	return "the real line"
}

// logSqrt2Pi is ½·log(2π).
var logSqrt2Pi = float64(0.5 * detmath.Log(2*math.Pi))

// prior is one parameter's prior. A and B are the family's two
// parameters (HalfNormal reads only B); Lo and Hi bound a truncnormal.
// Use it only through compile, which validates it and fills logZ.
type prior struct {
	Family string
	A, B   float64
	Lo, Hi float64
	// logZ is the family's log normalising constant, set by compile.
	logZ float64
}

func normalPrior(mu, sigma float64) prior    { return prior{Family: familyNormal, A: mu, B: sigma} }
func halfNormalPrior(sigma float64) prior    { return prior{Family: familyHalfNormal, B: sigma} }
func logNormalPrior(mu, sigma float64) prior { return prior{Family: familyLogNormal, A: mu, B: sigma} }
func betaPrior(a, b float64) prior           { return prior{Family: familyBeta, A: a, B: b} }
func truncNormalPrior(mu, sigma, lo, hi float64) prior {
	return prior{Family: familyTruncNormal, A: mu, B: sigma, Lo: lo, Hi: hi}
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// stdNormalCDF is Φ(z) through detmath.Erf.
func stdNormalCDF(z float64) float64 { return float64(0.5 * (1 + detmath.Erf(z/math.Sqrt2))) }

// compile validates p for a parameter living on s and returns it with
// its normalising constant. A family whose support is not inside s (a
// halfnormal on a control, a lognormal on the decay) is refused, so the
// family's transform always lands inside the parameter's support.
func (p prior) compile(s support) (prior, error) {
	bad := func(msg string) (prior, error) {
		return prior{}, fmt.Errorf("prior %s(%v, %v): %s", p.Family, p.A, p.B, msg)
	}
	if !finite(p.A) || !finite(p.B) {
		return bad("parameters must be finite")
	}
	var fam support
	switch p.Family {
	case familyNormal:
		fam = supportReal
		if !(p.B > 0) {
			return bad("σ must be > 0")
		}
		p.logZ = detmath.Log(p.B) + logSqrt2Pi
	case familyHalfNormal:
		fam = supportPositive
		if !(p.B > 0) {
			return bad("σ must be > 0")
		}
		p.logZ = detmath.Log(p.B) + logSqrt2Pi - math.Ln2
	case familyLogNormal:
		fam = supportPositive
		if !(p.B > 0) {
			return bad("σ must be > 0")
		}
		p.logZ = detmath.Log(p.B) + logSqrt2Pi
	case familyBeta:
		fam = supportUnit
		if !(p.A > 0) || !(p.B > 0) {
			return bad("α and β must be > 0")
		}
		la, _ := detmath.Lgamma(p.A)
		lb, _ := detmath.Lgamma(p.B)
		lab, _ := detmath.Lgamma(p.A + p.B)
		p.logZ = la + lb - lab
	case familyTruncNormal:
		if !(p.B > 0) {
			return bad("σ must be > 0")
		}
		if !finite(p.Lo) || !finite(p.Hi) || !(p.Lo < p.Hi) {
			return bad("bounds must be finite with lo < hi")
		}
		switch {
		case p.Lo >= 0 && p.Hi <= 1:
			fam = supportUnit
		case p.Lo >= 0:
			fam = supportPositive
		default:
			fam = supportReal
		}
		mass := stdNormalCDF((p.Hi-p.A)/p.B) - stdNormalCDF((p.Lo-p.A)/p.B)
		if !(mass > 0) {
			return bad("the bounds hold no mass of the normal")
		}
		p.logZ = detmath.Log(p.B) + logSqrt2Pi + detmath.Log(mass)
	default:
		return prior{}, fmt.Errorf("prior family %q is not one of normal, halfnormal, lognormal, beta, truncnormal", p.Family)
	}
	if !fits(fam, s) {
		return bad("its support " + fam.String() + " is not inside the parameter's " + s.String())
	}
	return p, nil
}

// fits reports whether support inner lies inside outer.
func fits(inner, outer support) bool {
	switch outer {
	case supportReal:
		return true
	case supportPositive:
		return inner == supportPositive || inner == supportUnit
	}
	return inner == supportUnit
}

// logDensity returns log p(x) and d log p / dx; outside the support it
// returns −Inf (the derivative is then meaningless).
func (p prior) logDensity(x float64) (lp, dlp float64) {
	switch p.Family {
	case familyNormal:
		z := (x - p.A) / p.B
		return float64(-0.5*float64(z*z)) - p.logZ, -z / p.B
	case familyHalfNormal:
		if !(x > 0) {
			return math.Inf(-1), 0
		}
		z := x / p.B
		return float64(-0.5*float64(z*z)) - p.logZ, -z / p.B
	case familyLogNormal:
		if !(x > 0) {
			return math.Inf(-1), 0
		}
		lx := detmath.Log(x)
		z := (lx - p.A) / p.B
		return float64(-0.5*float64(z*z)) - lx - p.logZ, -(1 + z/p.B) / x
	case familyBeta:
		if !(x > 0 && x < 1) {
			return math.Inf(-1), 0
		}
		// A shape of exactly 1 adds nothing, so a decay that rounds to
		// 0 or 1 under a uniform prior never meets 0·(−Inf).
		lp, dlp = -p.logZ, 0
		if p.A != 1 {
			lp += float64((p.A - 1) * detmath.Log(x))
			dlp += (p.A - 1) / x
		}
		if p.B != 1 {
			lp += float64((p.B - 1) * detmath.Log1p(-x))
			dlp -= (p.B - 1) / (1 - x)
		}
		return lp, dlp
	case familyTruncNormal:
		if !(x >= p.Lo && x <= p.Hi) {
			return math.Inf(-1), 0
		}
		z := (x - p.A) / p.B
		return float64(-0.5*float64(z*z)) - p.logZ, -z / p.B
	}
	return math.NaN(), math.NaN()
}

// realLine is the identity transform, for a prior on the whole line.
type realLine struct{}

func (realLine) Forward(x float64) mcmc.Mapped { return mcmc.Mapped{Y: x, DyDx: 1} }
func (realLine) Inverse(y float64) float64     { return y }

// transform maps the real line onto the prior's support: identity for a
// normal, exp for a halfnormal or lognormal, logistic for a beta and the
// bounded logistic for a truncnormal — so a sampled value never leaves
// the support and the log-Jacobian is the transform's.
func (p prior) transform() mcmc.Transform {
	switch p.Family {
	case familyHalfNormal, familyLogNormal:
		return mcmc.Positive{}
	case familyBeta:
		return mcmc.Unit{}
	case familyTruncNormal:
		return mcmc.Bounded{Lo: p.Lo, Hi: p.Hi}
	}
	return realLine{}
}

// center is a central value inside the support, a start for the
// sampler: the mean of a normal or beta, the median of a halfnormal or
// lognormal, the clamped mean of a truncnormal (the midpoint when the
// mean sits on or past a bound).
func (p prior) center() float64 {
	switch p.Family {
	case familyHalfNormal:
		return float64(0.6744897501960817 * p.B)
	case familyLogNormal:
		return detmath.Exp(p.A)
	case familyBeta:
		return p.A / (p.A + p.B)
	case familyTruncNormal:
		if p.A > p.Lo && p.A < p.Hi {
			return p.A
		}
		return float64(0.5*p.Lo) + float64(0.5*p.Hi)
	}
	return p.A
}
