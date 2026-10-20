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
// The two-sided convention is the "p-value method" of R's fisher.test:
// sum the hypergeometric probabilities of every feasible cell[0][0]
// value whose probability d is at most d_obs·(1 + 10⁻⁷) — fisher.test's
// relErr, so a table within 10⁻⁷ relative of the observed one counts as
// a tie — over the sum of all of them. The densities are exp(log P −
// max log P), max-shifted as fisher.test's dnhyper() is, so no weight
// underflows. TEST_FISHER_EXACT's Finalize and the overlay call this one
// function, so both surfaces return identical p-values for one 2×2.
//
// Returns 1.0 for a fully-degenerate table (n == 0, or every cell is
// zero) — no rejection is possible under a degenerate hypergeometric.
// Returns a value clamped to [0, 1] so floating-point sum overshoot
// never escapes the unit interval.
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
	lo, logdc := fisherLogDensities(a, b, c, d)
	maxD := math.Inf(-1)
	for _, l := range logdc {
		maxD = math.Max(maxD, l)
	}
	dObs := math.Exp(logdc[a-lo]-maxD) * fisherRelErr
	var sum, total float64
	for _, l := range logdc {
		w := math.Exp(l - maxD)
		total += w
		if w <= dObs {
			sum += w
		}
	}
	p := sum / total
	if p > 1 {
		p = 1
	}
	if p < 0 {
		p = 0
	}
	return p
}

// fisherRelErr is fisher.test's relErr, 1 + 10⁻⁷: a table whose
// probability is within that factor of the observed one is a tie.
const fisherRelErr = 1 + 1e-7

// fisherLogDensities returns the bottom lo of cell a's support
// [max(0, R₁−C₂), min(R₁, C₁)] with every margin of (a, b, c, d) held
// fixed, and the central hypergeometric log-density at each support
// point (index s ↔ a = lo + s). Inputs must be non-negative.
func fisherLogDensities(a, b, c, d int) (int, []float64) {
	row1 := a + b
	col1 := a + c
	col2 := b + d
	n := a + b + c + d
	lo := max(0, row1-col2)
	hi := min(row1, col1)
	logdc := make([]float64, hi-lo+1)
	for s := range logdc {
		logdc[s] = logHypergeometric(lo+s, row1, col1, n)
	}
	return lo, logdc
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
	lo, logdc := fisherLogDensities(a, b, c, d)
	switch {
	case a == lo:
		return 0
	case a == lo+len(logdc)-1:
		return math.Inf(1)
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
			if thetaHi >= thetaCap {
				return math.Inf(1)
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
			if thetaLo <= -thetaCap {
				return 0
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

// fisherConditionalCI returns the exact conditional confidence interval
// on the odds ratio of the 2×2 table (a, b, c, d) at coverage conf —
// fisher.test(x, conf.level = conf)$conf.int, two-sided. With every
// margin fixed, cell a follows Fisher's noncentral hypergeometric
// distribution with odds ratio ψ; the lower bound solves
// P(A ≥ a; ψ) = (1 − conf)/2, the upper P(A ≤ a; ψ) = (1 − conf)/2.
//
// Boundary cases follow fisher.test's ncp.L / ncp.U: a at the bottom of
// its support gives a lower bound of exactly 0 (so a one-point support,
// an empty margin, is [0, +Inf]); a at the top gives an upper bound of
// +Inf (null on the wire); a tail equal to (1 − conf)/2 at ψ = 1 gives
// exactly 1.
//
// Each root is found in θ = log ψ, where both tails are monotone with
// derivative ±Cov(1{tail}, A): a bracket by doubling steps, then Newton
// steps safeguarded by bisection, to |Δθ| ≤ 4ε·max(1, |θ|). R's
// uniroot() stops at a ~1.2e-4 tolerance on ψ (or 1/ψ), so the R oracle
// (scripts/reference/gen_test_outputs.R) refines both bounds before
// pinning them at 1e-10 and keeps fisher.test's values beside.
//
// Inputs must be non-negative and 0 < conf < 1.
func fisherConditionalCI(a, b, c, d int, conf float64) (lower, upper float64) {
	lo, logdc := fisherLogDensities(a, b, c, d)
	hi := lo + len(logdc) - 1
	alpha := (1 - conf) / 2
	// tail returns P(A ≤ a) (upperTail false) or P(A ≥ a) at θ, minus
	// alpha, and its derivative in θ: Σ_tail p_s·(s − E[A]).
	tail := func(theta float64, upperTail bool) (float64, float64) {
		maxD := math.Inf(-1)
		for s, l := range logdc {
			maxD = math.Max(maxD, l+float64(theta*float64(s)))
		}
		var w0, w1, t0, t1 float64
		for s, l := range logdc {
			w := math.Exp(l + float64(theta*float64(s)) - maxD)
			v := float64(s) // centred on lo, as in the MLE
			w0 += w
			w1 += float64(w * v)
			if (upperTail && lo+s >= a) || (!upperTail && lo+s <= a) {
				t0 += w
				t1 += float64(w * v)
			}
		}
		p := t0 / w0
		return p - alpha, t1/w0 - p*(w1/w0)
	}
	lower, upper = 1, math.Inf(1)
	if a == lo {
		lower = 0
	} else {
		lower = fisherSolveTheta(func(theta float64) (float64, float64) { return tail(theta, true) })
	}
	if a != hi {
		upper = fisherSolveTheta(func(theta float64) (float64, float64) {
			f, v := tail(theta, false)
			return -f, -v
		})
	}
	return lower, upper
}

// fisherSolveTheta returns e^θ for the root of g, a function of θ that
// is strictly increasing and crosses zero (returning g and dg/dθ): a
// bracket by doubling steps from θ = 0, then Newton steps safeguarded by
// bisection, to |Δθ| ≤ 4ε·max(1, |θ|). A root exactly at θ = 0 returns
// exactly 1. Callers keep the support boundaries (where no root exists)
// out; should one slip through, the bracket stops at |θ| = 2¹³, past
// where e^θ is 0 or +Inf, rather than doubling forever.
func fisherSolveTheta(g func(theta float64) (float64, float64)) float64 {
	f0, _ := g(0)
	if f0 == 0 {
		return 1
	}
	thetaLo, thetaHi := 0.0, 0.0
	step := 1.0
	if f0 < 0 {
		for {
			thetaHi += step
			if f, _ := g(thetaHi); f >= 0 {
				if f == 0 {
					return math.Exp(thetaHi)
				}
				break
			}
			if thetaHi >= thetaCap {
				return math.Inf(1)
			}
			thetaLo = thetaHi
			step *= 2
		}
	} else {
		for {
			thetaLo -= step
			if f, _ := g(thetaLo); f <= 0 {
				if f == 0 {
					return math.Exp(thetaLo)
				}
				break
			}
			if thetaLo <= -thetaCap {
				return 0
			}
			thetaHi = thetaLo
			step *= 2
		}
	}
	theta := (thetaLo + thetaHi) / 2
	for range 400 {
		f, v := g(theta)
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

// thetaCap bounds the doubling bracket of the θ = log ψ solvers: past
// |θ| = 2¹³ e^θ is 0 or +Inf, so a bracket that has not closed there
// (unreachable while callers keep the support boundaries out) returns
// that boundary instead of doubling forever.
const thetaCap = 1 << 13

// epsilon64 is the float64 machine epsilon, 2⁻⁵².
const epsilon64 = 0x1p-52
