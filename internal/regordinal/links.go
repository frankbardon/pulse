package regordinal

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// links.go holds the cumulative links: F the latent error's cdf, f its
// density and f′ the density's derivative, MASS::polr's methods
// "logistic" (Pulse logit), "probit" and "cloglog" (polr's pGumbel,
// F(x) = 1 − exp(−exp(x))). Every product is barriered and every
// transcendental is detmath's, so a fit is bit-identical on every
// architecture.

// The params.link values, the default first.
const (
	LinkLogit   = "logit"
	LinkProbit  = "probit"
	LinkCloglog = "cloglog"
)

// Links lists the params.link values, the default first.
func Links() []string { return []string{LinkLogit, LinkProbit, LinkCloglog} }

// link is one cumulative link: eval returns F(x), f(x) and f′(x).
type link struct {
	name string
	eval func(x float64) (F, f, fp float64)
	// quantile is F⁻¹, read by the start (the thresholds-only fit).
	quantile func(p float64) float64
}

// invSqrt2Pi is 1/√(2π).
const invSqrt2Pi = 0.398942280401432677939946059934381868475858631164934657665925829670657925899301838501252333907306936430302558862635182685510991954555837242996212730625507599

var links = map[string]link{
	LinkLogit: {name: LinkLogit, eval: logistic, quantile: func(p float64) float64 {
		return detmath.Log(p / (1 - p))
	}},
	LinkProbit: {name: LinkProbit, eval: probit, quantile: normalQuantile},
	LinkCloglog: {name: LinkCloglog, eval: cloglog, quantile: func(p float64) float64 {
		return detmath.Log(-detmath.Log1p(-p))
	}},
}

// logistic is F = 1/(1 + e^−x), f = F(1 − F), f′ = f(1 − 2F), each
// formed from the tail that keeps its precision.
func logistic(x float64) (F, f, fp float64) {
	var s float64
	if x >= 0 {
		e := detmath.Exp(-x)
		F = 1 / (1 + e)
		s = e / (1 + e)
	} else {
		e := detmath.Exp(x)
		F = e / (1 + e)
		s = 1 / (1 + e)
	}
	f = float64(F * s)
	fp = float64(f * (s - F))
	return F, f, fp
}

// probit is F = Φ(x), f = φ(x), f′ = −x·φ(x).
func probit(x float64) (F, f, fp float64) {
	F = float64(0.5 * detmath.Erfc(-x/math.Sqrt2))
	f = float64(detmath.Exp(-float64(float64(x*x)*0.5)) * invSqrt2Pi)
	fp = -float64(x * f)
	return F, f, fp
}

// cloglog is F = 1 − exp(−eˣ), f = exp(x − eˣ), f′ = f(1 − eˣ).
func cloglog(x float64) (F, f, fp float64) {
	ex := detmath.Exp(x)
	F = -detmath.Expm1(-ex)
	f = detmath.Exp(x - ex)
	fp = float64(f * (1 - ex))
	return F, f, fp
}

// normalQuantile is Φ⁻¹(p) = √2·erf⁻¹(2p − 1), on detmath's inverse
// error function (only the start reads it).
func normalQuantile(p float64) float64 {
	return float64(math.Sqrt2 * detmath.Erfinv(float64(2*p)-1))
}
