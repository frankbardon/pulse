package statdist

import "math"

// RegularizedIncompleteBetaXY returns I_x(a, b) given x and y = 1 - x
// computed independently by the caller. Uses the continued-fraction
// expansion from Numerical Recipes (3rd ed.) Section 6.4, with the
// prefactor x^a y^b / B(a, b) assembled in log space from logBeta
// (Stirling-corrected, so no lgamma cancellation at large a or b).
func RegularizedIncompleteBetaXY(a, b, x, y float64) float64 {
	if x <= 0 {
		return 0
	}
	if y <= 0 {
		return 1
	}
	lx := math.Log(x)
	if x > 0.5 {
		lx = math.Log1p(-y)
	}
	ly := math.Log(y)
	if y > 0.5 {
		ly = math.Log1p(-x)
	}
	return regularizedIncompleteBetaLog(a, b, x, y, lx, ly)
}

// regularizedIncompleteBetaLog is RegularizedIncompleteBetaXY with the
// caller also supplying lx = log x and ly = log y, so x (or y) may have
// underflowed to 0 while the tail it implies is still representable.
func regularizedIncompleteBetaLog(a, b, x, y, lx, ly float64) float64 {
	if math.IsInf(lx, -1) {
		return 0
	}
	if math.IsInf(ly, -1) {
		return 1
	}
	bt := math.Exp(a*lx + b*ly - logBeta(a, b))
	if x < (a+1)/(a+b+2) {
		return bt * betacf(a, b, x) / a
	}
	return 1 - bt*betacf(b, a, y)/b
}

// logBeta returns log B(a, b) for a, b > 0. Large arguments use the
// Stirling-series correction (LgammaCorrection) so the three O(a log a)
// lgamma terms never cancel; this is the algorithm of R's lbeta().
func logBeta(a, b float64) float64 {
	p, q := math.Min(a, b), math.Max(a, b)
	const lnSqrt2Pi = 0.918938533204672741780329736406 // log(sqrt(2π))
	switch {
	case p >= 10:
		corr := LgammaCorrection(p) + LgammaCorrection(q) - LgammaCorrection(p+q)
		return -0.5*math.Log(q) + lnSqrt2Pi + corr +
			(p-0.5)*math.Log(p/(p+q)) + q*math.Log1p(-p/(p+q))
	case q >= 10:
		corr := LgammaCorrection(q) - LgammaCorrection(p+q)
		lgp, _ := math.Lgamma(p)
		return lgp + corr + p - p*math.Log(p+q) + (q-0.5)*math.Log1p(-p/(p+q))
	default:
		lgp, _ := math.Lgamma(p)
		lgq, _ := math.Lgamma(q)
		lgpq, _ := math.Lgamma(p + q)
		return lgp + lgq - lgpq
	}
}

// LgammaCorrection returns the Stirling remainder
//
//	δ(x) = lgamma(x) − ((x − ½)·log x − x + log √(2π))
//
// for x ≥ 10 via its asymptotic series Σ B₂ₙ / (2n(2n−1) x^{2n−1}).
// Eight terms leave a truncation error below 1e-15 at x = 10.
// Exported for processing's incomplete-gamma prefactor (chi-square).
func LgammaCorrection(x float64) float64 {
	coef := [...]float64{
		1.0 / 12, -1.0 / 360, 1.0 / 1260, -1.0 / 1680,
		1.0 / 1188, -691.0 / 360360, 1.0 / 156, -3617.0 / 122400,
	}
	inv := 1 / x
	inv2 := inv * inv
	sum := 0.0
	for i := len(coef) - 1; i >= 0; i-- {
		sum = sum*inv2 + coef[i]
	}
	return sum * inv
}

// betacf is the Lentz continued-fraction evaluation of the incomplete
// beta. Mirrors the canonical Numerical Recipes routine; the iteration
// count grows like O(√max(a, b)), so the cap leaves room for df ≈ 1e6.
func betacf(a, b, x float64) float64 {
	const (
		maxIter = 20000
		eps     = 1e-16
		tiny    = 1e-300
	)
	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		mf := float64(m)
		m2 := float64(2 * m)
		// Even step
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		// Odd step
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) <= eps {
			break
		}
	}
	return h
}
