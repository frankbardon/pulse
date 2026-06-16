package processing

import "math"

// Fisher's exact test — package-local helpers shared between the
// TEST_FISHER_EXACT row test (processing/test_fisher.go) and the
// OVERLAY_FISHER_EXACT_CELL overlay handler (processing/overlay.go).
//
// The two-sided p-value computation is factored into fisherExactTwoSided
// so the overlay surface and the row-test surface produce identical
// p-values for the same 2×2 contingency. The lgamma-backed
// hypergeometric primitive (logHypergeometric, logBinomial) lives in
// test_fisher.go; this file only adds the higher-level "compute the
// two-sided p" entry point.
//
// Naming follows the established factoring pattern in
// processing/welford.go (WelfordStdDev) — package-local lowercase
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
