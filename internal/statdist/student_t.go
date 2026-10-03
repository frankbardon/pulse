package statdist

import "math"

// StudentTTwoSidedP returns the two-sided p-value of a Student's t
// statistic with df degrees of freedom: P(|T| ≥ |t|).
//
// Derivation: the survival function of |T| equals the regularized
// incomplete beta I_x(df/2, 1/2) evaluated at x = df / (df + t²). Both
// x and its complement y = t² / (df + t²) are formed directly (never
// y = 1 - x), so a tiny t at large df keeps full precision.
//
// Returns NaN for df ≤ 0 or a NaN input, 0 for |t| = ∞ and 1 for t = 0.
func StudentTTwoSidedP(t, df float64) float64 {
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

// StudentTCDF returns P(T ≤ t) for T ~ t(df). Built on the same
// incomplete-beta identity as StudentTTwoSidedP — symmetry around zero
// pins the t < 0 branch.
func StudentTCDF(t, df float64) float64 {
	if df <= 0 || math.IsNaN(t) || math.IsNaN(df) {
		return math.NaN()
	}
	if math.IsInf(t, 1) {
		return 1
	}
	if math.IsInf(t, -1) {
		return 0
	}
	if t == 0 {
		return 0.5
	}
	x, y, lx, ly := studentTBetaArgs(t, df)
	// I_x(df/2, 1/2) is the two-sided tail mass; halve it for one tail.
	tail := 0.5 * regularizedIncompleteBetaLog(df/2.0, 0.5, x, y, lx, ly)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

// StudentTQuantile returns the inverse Student-t CDF at probability p:
// the t* with P(T ≤ t*) = p for T ~ t(df). By symmetry t* is ∓ the
// two-sided critical value at alpha = 2·min(p, 1−p) (1−p is exact for
// p ≥ ½).
//
// Degenerate inputs: NaN p or df, or df ≤ 0 → NaN; p ≤ 0 → −Inf;
// p ≥ 1 → +Inf; p = 0.5 → 0 exactly.
func StudentTQuantile(p, df float64) float64 {
	if math.IsNaN(p) || math.IsNaN(df) || df <= 0 {
		return math.NaN()
	}
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	if p == 0.5 {
		return 0
	}
	if p < 0.5 {
		return -StudentTInverseTwoSided(2*p, df)
	}
	return StudentTInverseTwoSided(2*(1-p), df)
}

// StudentTInverseTwoSided returns the q > 0 with P(|T| ≥ q) = alpha for
// df degrees of freedom — the two-sided critical value used for
// confidence-interval bounds.
//
// Solved on log(p) by a bracketed Newton iteration (bisection fallback)
// on StudentTTwoSidedP, so the root is as accurate as the p-value
// itself in every tail — including df = 1, where q ≈ 2/(π·alpha) is far
// past any fixed search bracket. Returns NaN on degenerate inputs
// (df ≤ 0, alpha outside (0, 1), NaN).
func StudentTInverseTwoSided(alpha, df float64) float64 {
	if df <= 0 || math.IsNaN(alpha) || math.IsNaN(df) || alpha <= 0 || alpha >= 1 {
		return math.NaN()
	}
	logAlpha := math.Log(alpha)
	// g(t) = log P(|T| ≥ t) − log alpha, strictly decreasing in t > 0.
	g := func(t float64) float64 { return math.Log(StudentTTwoSidedP(t, df)) - logAlpha }
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
		p := StudentTTwoSidedP(t, df)
		return math.Log(p) - logAlpha, dg(t, p)
	})
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
