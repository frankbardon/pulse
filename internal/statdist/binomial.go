package statdist

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// binomialRelErr is R binom.test's tie tolerance: an outcome on the far
// side of the mean counts as "at least as extreme" when its density is
// at most the observed density times 1 + 10⁻⁷, so two outcomes whose
// densities agree up to rounding (a symmetric p = 0.5 pair) are both in.
const binomialRelErr = 1 + 1e-7

// BinomialTwoSidedP returns the exact two-sided binomial p-value of x
// successes in n trials under success probability p — R's
// binom.test(x, n, p)$p.value, alternative "two.sided":
//
//	p = 0: 1 when x = 0, else 0;  p = 1: 1 when x = n, else 0
//	m = n·p; x = m ⇒ 1
//	x < m ⇒ P(X ≤ x) + P(X > n − y),  y = #{i ≥ ⌈m⌉ : d(i) ≤ d(x)·(1 + 10⁻⁷)}
//	x > m ⇒ P(X < y) + P(X ≥ x),      y = #{i ≤ ⌊m⌋ : d(i) ≤ d(x)·(1 + 10⁻⁷)}
//
// clamped to at most 1. The densities d are compared in log space
// (logBinomialDensity, Loader's saddle-point form — R's dbinom_raw),
// and since d is non-increasing on [⌈m⌉, n] and non-decreasing on
// [0, ⌊m⌋] each count y is a binary search, so the cost is O(log n)
// density evaluations plus two incomplete-beta tails
// (RegularizedIncompleteBetaXY). x < 0, x > n, n < 1 or p outside
// [0, 1] (or NaN) is NaN: binom.test refuses them.
//
// The FMA-free statdist rules hold (doc.go): every product feeding an
// add is an explicit float64(...) barrier on detmath's Log / Log1p, so
// the bits are the same on every architecture.
func BinomialTwoSidedP(x, n int, p float64) float64 {
	if n < 1 || x < 0 || x > n || !(p >= 0 && p <= 1) {
		return math.NaN()
	}
	switch p {
	case 0:
		if x == 0 {
			return 1
		}
		return 0
	case 1:
		if x == n {
			return 1
		}
		return 0
	}
	q := 1 - p
	m := float64(float64(n) * p)
	xf := float64(x)
	if xf == m {
		return 1
	}
	threshold := logBinomialDensity(x, n, p, q) + detmath.Log1p(binomialRelErr-1)
	in := func(i int) bool { return logBinomialDensity(i, n, p, q) <= threshold }
	var out float64
	if xf < m {
		// d is non-increasing on [lo, n]: find the first i there inside
		// the tolerance; every later one is inside too.
		lo := int(math.Ceil(m))
		first := searchFirst(lo, n, in)
		y := n - first + 1
		out = binomialCDF(x, n, p, q) + binomialUpper(n-y+1, n, p, q)
	} else {
		// d is non-decreasing on [0, hi]: the outcomes inside the
		// tolerance are a prefix 0 .. y−1.
		hi := int(math.Floor(m))
		y := searchFirst(0, hi, func(i int) bool { return !in(i) })
		out = binomialCDF(y-1, n, p, q) + binomialUpper(x, n, p, q)
	}
	return math.Min(1, out)
}

// searchFirst returns the smallest i in [lo, hi] with pred(i) true, or
// hi + 1 when there is none; pred must be monotone (false … true).
func searchFirst(lo, hi int, pred func(int) bool) int {
	l, h := lo, hi+1
	for l < h {
		mid := l + (h-l)/2
		if pred(mid) {
			h = mid
		} else {
			l = mid + 1
		}
	}
	return l
}

// binomialCDF is P(X ≤ k) = I_q(n − k, k + 1) for X ~ Binomial(n, p).
func binomialCDF(k, n int, p, q float64) float64 {
	switch {
	case k < 0:
		return 0
	case k >= n:
		return 1
	}
	return RegularizedIncompleteBetaXY(float64(n-k), float64(k+1), q, p)
}

// binomialUpper is P(X ≥ j) = I_p(j, n − j + 1) for X ~ Binomial(n, p).
func binomialUpper(j, n int, p, q float64) float64 {
	switch {
	case j <= 0:
		return 1
	case j > n:
		return 0
	}
	return RegularizedIncompleteBetaXY(float64(j), float64(n-j+1), p, q)
}

// logBinomialDensity returns log P(X = x) for X ~ Binomial(n, p),
// q = 1 − p, 0 < p < 1, in Loader's saddle-point form (R's dbinom_raw):
//
//	log d = stirlerr(n) − stirlerr(x) − stirlerr(n − x)
//	        − bd0(x, n·p) − bd0(n − x, n·q) − ½·log(2π·x·(n − x)/n)
//
// so no O(n log n) lgamma terms cancel; the ends are n·log q (x = 0)
// and n·log p (x = n).
func logBinomialDensity(x, n int, p, q float64) float64 {
	nf := float64(n)
	switch x {
	case 0:
		return float64(nf * detmath.Log1p(-p))
	case n:
		return float64(nf * detmath.Log(p))
	}
	xf, yf := float64(x), float64(n-x)
	lc := stirlerr(nf) - stirlerr(xf) - stirlerr(yf) - bd0(xf, float64(nf*p)) - bd0(yf, float64(nf*q))
	const log2Pi = 1.837877066409345483560659472811 // log(2π)
	lf := log2Pi + detmath.Log(xf) + detmath.Log1p(-xf/nf)
	return lc - float64(0.5*lf)
}

// stirlerr is the Stirling remainder log(k!) − ((k + ½)·log k − k +
// log √(2π)) at an integer k ≥ 1: LgammaCorrection(k) for k ≥ 10, and
// directly from detmath.Lgamma below (small arguments, no cancellation).
func stirlerr(k float64) float64 {
	if k >= 10 {
		return LgammaCorrection(k)
	}
	const lnSqrt2Pi = 0.918938533204672741780329736406 // log(sqrt(2π))
	lg, _ := detmath.Lgamma(k + 1)
	return lg - float64((k+0.5)*detmath.Log(k)) + k - lnSqrt2Pi
}
