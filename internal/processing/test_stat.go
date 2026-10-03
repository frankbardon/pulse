package processing

import "math"

// Statistical helpers shared by the TEST_* operators. Keeping these
// dependency-free (no external math packages) avoids pulling in a large
// stats SDK for a handful of well-known distributions.
//
// Every primitive here is checked against R to a relative 1e-10 by
// reference_oracle_test.go (goldens: testdata/reference/, regenerate
// with `make reference`).

// studentTTwoSidedP returns the two-sided p-value of a Student's t
// statistic with df degrees of freedom: P(|T| ≥ |t|).
//
// Derivation: the survival function of |T| equals the regularized
// incomplete beta I_x(df/2, 1/2) evaluated at x = df / (df + t²). Both
// x and its complement y = t² / (df + t²) are formed directly (never
// y = 1 - x), so a tiny t at large df keeps full precision.
//
// Returns NaN for df ≤ 0 and 1 for t = 0.
func studentTTwoSidedP(t, df float64) float64 {
	if df <= 0 || math.IsNaN(t) || math.IsNaN(df) {
		return math.NaN()
	}
	if math.IsInf(t, 0) {
		return 0
	}
	if t == 0 {
		return 1
	}
	x, y, lx, ly := studentTBetaArgs(t, df)
	return regularizedIncompleteBetaLog(df/2.0, 0.5, x, y, lx, ly)
}

// studentTBetaArgs returns x = df/(df+t²), y = t²/(df+t²) and their
// logs, without forming either as one minus the other and without
// overflowing t². The logs stay finite even when x underflows (|t| past
// ~1e154), which keeps the far tail of qt-style inversions reachable.
func studentTBetaArgs(t, df float64) (x, y, lx, ly float64) {
	at := math.Abs(t)
	if at > math.Sqrt(df) {
		r := (df / at) / at // df / t², may underflow
		lr := math.Log(df) - 2*math.Log(at)
		l1 := math.Log1p(r)
		return r / (1 + r), 1 / (1 + r), lr - l1, -l1
	}
	r := (at / df) * at // t² / df, may underflow
	lr := 2*math.Log(at) - math.Log(df)
	l1 := math.Log1p(r)
	return 1 / (1 + r), r / (1 + r), -l1, lr - l1
}

// regularizedIncompleteBetaXY returns I_x(a, b) given x and y = 1 - x
// computed independently by the caller. Uses the continued-fraction
// expansion from Numerical Recipes (3rd ed.) Section 6.4, with the
// prefactor x^a y^b / B(a, b) assembled in log space from logBeta
// (Stirling-corrected, so no lgamma cancellation at large a or b).
func regularizedIncompleteBetaXY(a, b, x, y float64) float64 {
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

// regularizedIncompleteBetaLog is regularizedIncompleteBetaXY with the
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
// Stirling-series correction (lgammaCorrection) so the three O(a log a)
// lgamma terms never cancel; this is the algorithm of R's lbeta().
func logBeta(a, b float64) float64 {
	p, q := math.Min(a, b), math.Max(a, b)
	const lnSqrt2Pi = 0.918938533204672741780329736406 // log(sqrt(2π))
	switch {
	case p >= 10:
		corr := lgammaCorrection(p) + lgammaCorrection(q) - lgammaCorrection(p+q)
		return -0.5*math.Log(q) + lnSqrt2Pi + corr +
			(p-0.5)*math.Log(p/(p+q)) + q*math.Log1p(-p/(p+q))
	case q >= 10:
		corr := lgammaCorrection(q) - lgammaCorrection(p+q)
		lgp, _ := math.Lgamma(p)
		return lgp + corr + p - p*math.Log(p+q) + (q-0.5)*math.Log1p(-p/(p+q))
	default:
		lgp, _ := math.Lgamma(p)
		lgq, _ := math.Lgamma(q)
		lgpq, _ := math.Lgamma(p + q)
		return lgp + lgq - lgpq
	}
}

// lgammaCorrection returns the Stirling remainder
//
//	δ(x) = lgamma(x) − ((x − ½)·log x − x + log √(2π))
//
// for x ≥ 10 via its asymptotic series Σ B₂ₙ / (2n(2n−1) x^{2n−1}).
// Eight terms leave a truncation error below 1e-15 at x = 10.
func lgammaCorrection(x float64) float64 {
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

// studentTInverseTwoSided returns the t quantile such that
// P(|T| ≥ q) = alpha for df degrees of freedom. Used to build the
// confidence interval bounds around a mean difference.
//
// Solved on log(p) by a bracketed Newton iteration (bisection fallback)
// on studentTTwoSidedP, so the root is as accurate as the p-value itself
// in every tail — including df = 1, where q ≈ 2/(π·alpha) is far past
// any fixed search bracket. Returns NaN on degenerate inputs.
func studentTInverseTwoSided(alpha, df float64) float64 {
	if df <= 0 || math.IsNaN(alpha) || math.IsNaN(df) || alpha <= 0 || alpha >= 1 {
		return math.NaN()
	}
	logAlpha := math.Log(alpha)
	// g(t) = log P(|T| ≥ t) − log alpha, strictly decreasing in t > 0.
	g := func(t float64) float64 { return math.Log(studentTTwoSidedP(t, df)) - logAlpha }
	// dg/dt = −2·f(t) / P(|T| ≥ t), f the Student-t density.
	logCoef := -logBeta(df/2, 0.5) - 0.5*math.Log(df)
	dg := func(t, p float64) float64 {
		logPdf := logCoef - ((df+1)/2)*math.Log1p((t/df)*t)
		return -2 * math.Exp(logPdf) / p
	}
	lo, hi := 0.0, 1.0
	for g(hi) > 0 {
		lo = hi
		hi *= 4
		if math.IsInf(hi, 0) {
			return math.Inf(1)
		}
	}
	return newtonBracketed(lo, hi, func(t float64) (float64, float64) {
		p := studentTTwoSidedP(t, df)
		return math.Log(p) - logAlpha, dg(t, p)
	})
}

// newtonBracketed finds the root of a strictly decreasing function on
// [lo, hi] (f(lo) > 0 ≥ f(hi)) with Newton steps, falling back to
// bisection whenever a step leaves the bracket. fd returns f and f'.
// Stops at a relative step of 4 ulp or after the bracket collapses.
func newtonBracketed(lo, hi float64, fd func(float64) (float64, float64)) float64 {
	x := 0.5 * (lo + hi)
	for range 200 {
		f, d := fd(x)
		if f == 0 {
			return x
		}
		if f > 0 {
			lo = x
		} else {
			hi = x
		}
		next := x - f/d
		if math.IsNaN(next) || next <= lo || next >= hi {
			next = 0.5 * (lo + hi)
		}
		if math.Abs(next-x) <= 4*epsilon*math.Abs(next) || hi-lo <= 4*epsilon*math.Abs(hi) {
			return next
		}
		x = next
	}
	return x
}

// epsilon is the float64 machine epsilon (2⁻⁵²).
const epsilon = 0x1p-52

// chiSquareSurvival returns P(X² ≥ x) for a chi-square variate with df
// degrees of freedom: the complementary CDF used as the chi-square test
// p-value. Computed via the regularized upper incomplete gamma function
// Q(df/2, x/2).
func chiSquareSurvival(x, df float64) float64 {
	if df <= 0 || math.IsNaN(x) {
		return math.NaN()
	}
	if x <= 0 {
		return 1
	}
	return regularizedGammaQ(df/2.0, x/2.0)
}

// fSurvival returns P(F ≥ f) for an F variate with (df1, df2) degrees
// of freedom: 1 - F_CDF(f) used as the ANOVA / F-test p-value. Computed
// via the regularized incomplete beta:
//
//	1 - F_CDF(f) = I_{df2/(df2 + df1*f)}(df2/2, df1/2)
func fSurvival(f, df1, df2 float64) float64 {
	if df1 <= 0 || df2 <= 0 || math.IsNaN(f) {
		return math.NaN()
	}
	if f <= 0 {
		return 1
	}
	// x = df2/(df2 + df1·f) and its complement y = df1·f/(df2 + df1·f),
	// each formed directly so neither tail loses precision.
	var x, y float64
	if df1*f > df2 {
		r := df2 / (df1 * f)
		x, y = r/(1+r), 1/(1+r)
	} else {
		r := (df1 * f) / df2
		x, y = 1/(1+r), r/(1+r)
	}
	return regularizedIncompleteBetaXY(df2/2.0, df1/2.0, x, y)
}

// standardNormalCDF returns Φ(z), the standard normal cumulative
// distribution function, via Φ(z) = ½ erfc(−z/√2). The erfc form keeps
// full relative precision in the lower tail (down to z ≈ −37.5), where
// ½ (1 + erf(z/√2)) cancels to zero.
func standardNormalCDF(z float64) float64 {
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

// regularizedGammaQ returns Q(a, x) = 1 - P(a, x), the regularized
// upper incomplete gamma function. Routes between the series expansion
// (x < a+1) and the continued fraction (otherwise) for fastest
// convergence — Numerical Recipes style — with the prefactor
// x^a e^{-x} / Γ(a) from gammaPrefactor.
func regularizedGammaQ(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x == 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammaSeries(a, x)
	}
	return gammaContinuedFraction(a, x)
}

// gammaPrefactor returns x^a e^{-x} / Γ(a). For a ≥ 10 it is assembled
// as √(a/2π) · exp(−δ(a) − bd0(a, x)) — Loader's saddle-point form, the
// one R's dpois_raw uses — so a·log x − x − lgamma(a) never cancels at
// large df.
func gammaPrefactor(a, x float64) float64 {
	if a < 10 {
		lga, _ := math.Lgamma(a)
		return math.Exp(a*math.Log(x) - x - lga)
	}
	return math.Sqrt(a/(2*math.Pi)) * math.Exp(-lgammaCorrection(a)-bd0(a, x))
}

// bd0 returns a·log(a/m) + m − a without cancellation when a ≈ m
// (Loader 2000, "Fast and accurate computation of binomial
// probabilities").
func bd0(a, m float64) float64 {
	if math.Abs(a-m) < 0.1*(a+m) {
		v := (a - m) / (a + m)
		s := (a - m) * v
		ej := 2 * a * v
		v *= v
		for j := 1; j < 1000; j++ {
			ej *= v
			s1 := s + ej/float64(2*j+1)
			if s1 == s {
				return s1
			}
			s = s1
		}
		return s
	}
	return a*math.Log(a/m) + m - a
}

// gammaSeries evaluates P(a, x) via its convergent series for x < a+1.
// The term ratio is x/(a+n), so convergence takes O(√a) terms near
// x ≈ a; the cap covers df ≈ 1e7.
func gammaSeries(a, x float64) float64 {
	const (
		maxIter = 100000
		eps     = 1e-17
	)
	ap := a
	sum := 1.0 / a
	del := sum
	for range maxIter {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*eps {
			break
		}
	}
	return sum * gammaPrefactor(a, x)
}

// gammaContinuedFraction evaluates Q(a, x) via Lentz's continued
// fraction for x ≥ a+1.
func gammaContinuedFraction(a, x float64) float64 {
	const (
		maxIter = 100000
		eps     = 1e-16
		tiny    = 1e-300
	)
	b := x + 1 - a
	c := 1.0 / tiny
	d := 1.0 / b
	h := d
	for i := 1; i <= maxIter; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
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
	return h * gammaPrefactor(a, x)
}

// kolmogorovSurvival approximates Q_KS(λ) = 2 Σ (-1)^(j-1) exp(-2 j² λ²),
// the survival function of the Kolmogorov distribution. Used to convert
// the Kolmogorov-Smirnov D statistic into a two-sided p-value:
//
//	p = Q_KS((√en + 0.12 + 0.11/√en) * D)
//
// where en = n1*n2/(n1+n2). The first few terms of the series converge
// quickly; the loop bails once a term drops below 10⁻¹². Returns 1 for
// λ ≤ 0.
func kolmogorovSurvival(lambda float64) float64 {
	if lambda <= 0 {
		return 1
	}
	const (
		eps1    = 1e-6
		eps2    = 1e-16
		maxIter = 100
	)
	a2 := -2 * lambda * lambda
	fac := 2.0
	sum := 0.0
	termPrev := 0.0
	for j := 1; j <= maxIter; j++ {
		term := fac * math.Exp(a2*float64(j*j))
		sum += term
		if math.Abs(term) <= eps1*termPrev || math.Abs(term) <= eps2*sum {
			return sum
		}
		fac = -fac
		termPrev = math.Abs(term)
	}
	return 1
}
