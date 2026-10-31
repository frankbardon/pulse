package processing

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// The two-sample Kolmogorov-Smirnov p-value, ported from R 4.6.1's
// ks.test.default and src/library/stats/src/ks.c (GPL-2 | GPL-3,
// Copyright (C) 1999-2023 The R Core Team), so TEST_KS's p equals
// ks.test(x, y)$p.value at its defaults:
//
//   - exact (psmirnov_exact) when n₁·n₂ < 10000 — R's `exact = NULL`
//     rule, which since R 4.3 holds with ties too: the exact distribution
//     is then conditional on the observed tie pattern (Schröer & Trenkler
//     1995; Viehmann 2021's upper-tail recursion);
//   - otherwise the limiting Kolmogorov distribution at √(n₁n₂/(n₁+n₂))·D
//     (pkolmogorov_two_limit, R's K2l) at the tolerance ks.test passes,
//     1e-6, truncation included — so the figure is R's to the last few
//     ulps, not the untruncated limit (for √en·D < 1 the alternative
//     series keeps a single term).
//
// Only the two-sided alternative is ported: TEST_KS has no one-sided form.

// ksExactMaxProduct is ks.test's switch: exact iff n₁·n₂ < 10000.
const ksExactMaxProduct = 10000

// ksAsymptoticTol is the series tolerance ks.test hands
// C_pkolmogorov_two_limit (psmirnov_asymp's tol = 1e-06).
const ksAsymptoticTol = 1e-6

// ksUsesExact reports whether ks.test's default picks the exact
// distribution for sample sizes n1, n2 (Σw under frequency weights).
func ksUsesExact(n1, n2 float64) bool {
	return n1*n2 < ksExactMaxProduct
}

// ksTwoSampleP is ks.test's two-sided p for statistic D over the sorted
// samples a and b with row weights wa / wb (nil: every row weighs 1).
// Weights are integer frequency weights (a fractional frequency weight
// is an invalid row upstream), so the samples are their expansion: the
// sizes are Σw and every weight above 1 is a tie run.
func ksTwoSampleP(a, wa, b, wb []float64, D float64) float64 {
	m := ksSampleSize(a, wa)
	n := ksSampleSize(b, wb)
	// psmirnov: P(D ≥ q) is 1 for q ≤ 0 and 0 for q > 1 before any
	// branch runs.
	if D <= 0 {
		return 1
	}
	if D > 1 {
		return 0
	}
	md, nd := float64(m), float64(n)
	if !ksUsesExact(md, nd) {
		return ksAsymptoticP(md, nd, D)
	}
	p := psmirnovExactUpper(D, m, n, ksTieBoundaries(a, wa, b, wb, m+n))
	// ks.test: PVAL <- min(1, max(0, PVAL)).
	return math.Min(1, math.Max(0, p))
}

// ksAsymptoticP is ks.test's limiting two-sided p for sizes m, n —
// psmirnov(D, c(m, n), exact = FALSE, lower.tail = FALSE): 1 at D ≤ 0,
// 0 past 1, else the limiting Kolmogorov survival at √(mn/(m+n))·D
// truncated at ks.test's tol, clamped to [0, 1]. ksTwoSampleP's
// asymptotic arm, and OVERLAY_KS_VS_POP's percentile path, which keeps
// no tie pattern.
func ksAsymptoticP(m, n, D float64) float64 {
	if D <= 0 {
		return 1
	}
	if D > 1 {
		return 0
	}
	p := kolmogorovTwoLimitUpper(math.Sqrt(m*n/(m+n))*D, ksAsymptoticTol)
	return math.Min(1, math.Max(0, p))
}

// ksSampleSize is a sample's row count, Σw when weighted (integers).
func ksSampleSize(xs, ws []float64) int {
	if ws == nil {
		return len(xs)
	}
	var t float64
	for _, w := range ws {
		t += w
	}
	return int(math.Round(t))
}

// ksTieBoundaries is psmirnov_exact's z over the pooled sample of size
// N: z[k] is true iff the k-th and (k+1)-th pooled order statistics
// differ (1-based), z[0] false and z[N] true — R's
// c(0L, diff(sort(z)) != 0, 1L). Without ties every interior z[k] is
// true, where R's no-ties recursion (psmirnov_exact_uniq_upper) is the
// ties recursion with every test passing, operation for operation; with
// every value tied, D = 0 and the caller returns before reading z. A
// weight w puts w equal order statistics at its value. NaN runs (sorted
// first) count as one value, as ksTwoSampleDW consumes them.
func ksTieBoundaries(a, wa, b, wb []float64, N int) []bool {
	weight := func(ws []float64, i int) int {
		if ws == nil {
			return 1
		}
		return int(math.Round(ws[i]))
	}
	same := func(x, y float64) bool { return x == y || (x != x && y != y) }
	z := make([]bool, N+1)
	i, j, c := 0, 0, 0
	for i < len(a) || j < len(b) {
		// The next pooled value: the smaller head (NaN first, as sorted).
		var v float64
		switch {
		case j >= len(b):
			v = a[i]
		case i >= len(a):
			v = b[j]
		case !(a[i] > b[j]):
			v = a[i]
		default:
			v = b[j]
		}
		for i < len(a) && same(a[i], v) {
			c += weight(wa, i)
			i++
		}
		for j < len(b) && same(b[j], v) {
			c += weight(wb, j)
			j++
		}
		if c <= N {
			z[c] = true
		}
	}
	z[N] = true
	return z
}

// psmirnovExactUpper is R's psmirnov_exact for lower.tail = FALSE,
// two-sided, through psmirnov_exact_ties_upper: P(D ≥ q) for sample
// sizes m (the first sample, ks.test's x) and n, conditional on the
// pooled tie pattern z (ksTieBoundaries). It is Viehmann's (2021)
// recursion on C_{i,j} = 1 − A_{i,j} / choose(i + j, i): a lattice
// point whose gap |i/m − j/n| reaches q at a value boundary
// (z[i + j]) is absorbing (C = 1), every other point mixes its two
// predecessors with weights i/(i+j) and j/(i+j).
func psmirnovExactUpper(q float64, m, n int, z []bool) float64 {
	md, nd := float64(m), float64(n)
	// q has about 0.5/mn subtracted so rounding error never turns an
	// equality into an inequality (R's comment, verbatim arithmetic).
	q = (0.5 + math.Floor(float64(q*md*nd)-1e-7)) / (md * nd)
	test := func(r, s float64) bool { return math.Abs(r-s) >= q }
	u := make([]float64, n+1)
	u[0] = 0
	for j := 1; j <= n; j++ {
		if test(0, float64(j)/nd) && z[j] {
			u[j] = 1
		} else {
			u[j] = u[j-1]
		}
	}
	for i := 1; i <= m; i++ {
		if test(float64(i)/md, 0) && z[i] {
			u[0] = 1
		}
		for j := 1; j <= n; j++ {
			if test(float64(i)/md, float64(j)/nd) && z[i+j] {
				u[j] = 1
			} else {
				v := float64(i) / float64(i+j)
				w := float64(j) / float64(i+j) // 1 − v
				u[j] = float64(v*u[j]) + float64(w*u[j-1])
			}
		}
	}
	return u[n]
}

// kolmogorovTwoLimitUpper is R's K2l(x, lower = FALSE, tol): the
// limiting Kolmogorov survival 1 − Σ_k (−1)^k exp(−2k²x²), summed until
// successive partial sums move by at most tol (x ≥ 1), or 1 minus
// Durbin's alternative series √(2π)/x Σ_{k odd} exp(−k²π²/(8x²)) over
// k < ⌊√(2 − log tol)⌋ (x < 1). At ks.test's tol it reproduces
// ks.test's truncation; TestReferenceOracle_KolmogorovSurvival pins it
// untruncated (tol 1e-300) against R.
func kolmogorovTwoLimitUpper(x, tol float64) float64 {
	if x <= 0 {
		return 1
	}
	if x < 1 {
		const m1Sqrt2Pi = 0.398942280401432677939946059934 // R's M_1_SQRT_2PI
		kMax := int(math.Sqrt(2 - detmath.Log(tol)))
		w := detmath.Log(x)
		piOver2, piOver4 := math.Pi/2, math.Pi/4 // M_PI_2, M_PI_4: multiplied at run time, as in C
		z := -(piOver2 * piOver4) / (x * x)
		s := 0.0
		for k := 1; k < kMax; k += 2 {
			s += detmath.Exp(float64(float64(k*k)*z) - w)
		}
		return 1 - s/m1Sqrt2Pi
	}
	z := -2 * x * x
	s := -1.0
	k := 2
	old, cur := 0.0, 2*detmath.Exp(z)
	for math.Abs(old-cur) > tol {
		old = cur
		cur += float64(2 * s * detmath.Exp(z*float64(k)*float64(k))) // C's z * k * k, left to right
		s = -s
		k++
	}
	return cur
}
