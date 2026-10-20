package processing

import "math"

// Fisher's exact test — package-local helpers shared between the
// TEST_FISHER_EXACT row test (internal/processing/test_fisher.go) and the
// OVERLAY_FISHER_EXACT_CELL overlay handler (internal/processing/overlay.go).
//
// The two-sided p-value computation is factored into fisherExactTwoSided
// so the overlay surface and the row-test surface produce identical
// p-values for the same 2×2 contingency. The lgamma-backed
// hypergeometric primitive (logHypergeometric, logBinomial) lives in
// test_fisher.go; this file only adds the higher-level "compute the
// two-sided p" entry point.
//
// Naming follows the established factoring pattern in
// internal/processing/welford.go (WelfordStdDev) — package-local lowercase
// helper, no exposure outside the processing package.

// fisherExactTwoSided returns the two-sided Fisher's exact p-value for a
// 2×2 contingency table whose cells are (a, b, c, d):
//
//	        col=c0   col=c1   | row_margin
//	row=r0    a        b      |   row1 = a + b
//	row=r1    c        d      |   row2 = c + d
//	col      col1     col2    |   grand_total = n = a + b + c + d
//
// The two-sided convention is the "p-value method": sum hypergeometric
// probabilities for every feasible cell[0][0] value whose log-probability
// is at most logPObs (the observed table's log-probability). The
// implementation mirrors the body of fisherExactRow.Finalize() so the
// overlay and the row-test surface return identical p-values for the
// same 2×2.
//
// Returns 1.0 for a fully-degenerate table (n == 0, or every cell is
// zero) — no rejection is possible under a degenerate hypergeometric.
// Returns a value clamped to [0, 1] so floating-point sum overshoot
// from the 1e-12 tolerance never escapes the unit interval.
//
// Inputs must be non-negative; the caller is responsible for clamping a
// computed cell of the 2×2 (e.g. grand - row1 - col1 + a) into the non-
// negative regime before invoking this helper.
func fisherExactTwoSided(a, b, c, d int) float64 {
	if a < 0 || b < 0 || c < 0 || d < 0 {
		// Defense in depth: the overlay handler clamps every 2×2 cell
		// into the non-negative regime before invoking this helper, but
		// a caller-passed negative count yields a degenerate result.
		return 1.0
	}
	n := a + b + c + d
	if n == 0 {
		return 1.0
	}
	row1 := a + b
	col1 := a + c
	col2 := b + d
	// Range of feasible cell[0][0] holding marginals fixed. Mirrors
	// fisherExactRow.Finalize() so the lookup table is identical.
	lo := 0
	if row1-col2 > 0 {
		lo = row1 - col2
	}
	hi := row1
	if col1 < hi {
		hi = col1
	}
	logPObs := logHypergeometric(a, row1, col1, n)
	var sum float64
	for x := lo; x <= hi; x++ {
		logP := logHypergeometric(x, row1, col1, n)
		// "At least as extreme" uses the two-sided convention: every
		// table whose hypergeometric probability is ≤ the observed.
		// 1e-12 slack mirrors the row-test surface so the two are
		// byte-equal on the same inputs.
		if logP <= logPObs+1e-12 {
			sum += math.Exp(logP)
		}
	}
	if sum > 1 {
		sum = 1
	}
	if sum < 0 {
		sum = 0
	}
	return sum
}

// fisherConditionalOddsRatio returns the conditional maximum-likelihood
// estimate of the odds ratio of the 2×2 table (a, b, c, d) — the
// estimate R's fisher.test reports — not the sample cross-product
// ratio ad/bc. With every margin held fixed, cell a follows Fisher's
// noncentral hypergeometric distribution with odds ratio ψ; the
// estimate solves E[a; ψ] = a (Cornfield 1956).
//
// Boundary cases follow fisher.test's mle() in its order: a at the
// bottom of its support [max(0, R₁−C₂), min(R₁, C₁)] gives 0 — checked
// first, so a one-point support (an empty row or column margin) is 0,
// never 0/0 — a at the top gives +Inf, and a equal to the central mean
// E[a; 1] gives exactly 1.
//
// The root is found in θ = log ψ, where E[a; e^θ] is strictly
// increasing with derivative Var[a; e^θ]: a bracket by doubling steps,
// then Newton steps safeguarded by bisection, to |Δθ| ≤ 4ε·max(1, |θ|).
// R's own uniroot() stops at a ~1.2e-4 tolerance on ψ (or 1/ψ), so the
// R oracle (scripts/reference/gen_test_outputs.R) refines the root
// before pinning it at 1e-10 and keeps fisher.test's value beside.
//
// Inputs must be non-negative.
func fisherConditionalOddsRatio(a, b, c, d int) float64 {
	row1 := a + b
	col1 := a + c
	col2 := b + d
	n := a + b + c + d
	lo := 0
	if row1-col2 > 0 {
		lo = row1 - col2
	}
	hi := min(row1, col1)
	switch {
	case a == lo:
		return 0
	case a == hi:
		return math.Inf(1)
	}
	logdc := make([]float64, hi-lo+1)
	for s := range logdc {
		logdc[s] = logHypergeometric(lo+s, row1, col1, n)
	}
	// moments returns E[a] − a and Var[a] at θ, via a max-shifted
	// exponent so no weight overflows.
	moments := func(theta float64) (float64, float64) {
		maxD := math.Inf(-1)
		for s, l := range logdc {
			maxD = math.Max(maxD, l+float64(theta*float64(lo+s)))
		}
		var w0, w1, w2 float64
		for s, l := range logdc {
			w := math.Exp(l + float64(theta*float64(lo+s)) - maxD)
			v := float64(s) // centred on lo: a smaller second moment
			w0 += w
			w1 += float64(w * v)
			w2 += float64(float64(w*v) * v)
		}
		mean := w1 / w0
		return mean + float64(lo) - float64(a), w2/w0 - mean*mean
	}
	f0, _ := moments(0)
	if f0 == 0 {
		return 1
	}
	// Bracket the root: f(θlo) < 0 < f(θhi).
	thetaLo, thetaHi := 0.0, 0.0
	step := 1.0
	if f0 < 0 {
		for {
			thetaHi += step
			if f, _ := moments(thetaHi); f >= 0 {
				if f == 0 {
					return math.Exp(thetaHi)
				}
				break
			}
			thetaLo = thetaHi
			step *= 2
		}
	} else {
		for {
			thetaLo -= step
			if f, _ := moments(thetaLo); f <= 0 {
				if f == 0 {
					return math.Exp(thetaLo)
				}
				break
			}
			thetaHi = thetaLo
			step *= 2
		}
	}
	theta := (thetaLo + thetaHi) / 2
	for range 200 {
		f, v := moments(theta)
		if f == 0 {
			break
		}
		if f < 0 {
			thetaLo = theta
		} else {
			thetaHi = theta
		}
		next := theta - f/v
		if !(next > thetaLo && next < thetaHi) || v <= 0 {
			next = (thetaLo + thetaHi) / 2
		}
		done := math.Abs(next-theta) <= 4*epsilon64*math.Max(1, math.Abs(theta))
		theta = next
		if done || thetaHi-thetaLo <= 4*epsilon64*math.Max(1, math.Abs(theta)) {
			break
		}
	}
	return math.Exp(theta)
}

// epsilon64 is the float64 machine epsilon, 2⁻⁵².
const epsilon64 = 0x1p-52
