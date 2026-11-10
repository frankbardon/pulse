package statdist

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// BowkerExactMaxTerms bounds the work of BowkerExactP: the number of
// (statistic value, mass) terms the convolution enumerates, summed over
// every pair step (2^20). Past it the caller refuses the exact test
// rather than fall back to the chi-square approximation. At the cap a
// run holds about 16 MB of terms for some 40 ms; it admits the tables
// the exact test is for — small discordant counts: up to about 200
// moves per pair on a 3 x 3, about 20 on a 4 x 4, about 8 on a 6 x 6 —
// and a table past it has counts large enough for the chi-square.
const BowkerExactMaxTerms = 1 << 20

// bowkerRelErr is the tolerance on the statistic comparison, R
// fisher.test's relErr convention: an outcome counts as at least as
// extreme as the observed table when its statistic times 1 + 10⁻⁷
// reaches the observed one, so outcomes equal up to rounding are in.
const bowkerRelErr = 1 + 1e-7

// bowkerMergeRelTol merges support values that agree to 10⁻¹²
// relative: the same rational sum reached through different additions
// can differ in its last bits, and must count as one value.
const bowkerMergeRelTol = 1e-12

// bowkerTerm is one support point of the statistic's distribution.
type bowkerTerm struct{ v, p float64 }

// BowkerExactP returns the exact conditional Bowker test of symmetry
// over the discordant pairs of a k × k table, each pair (n_ij, n_ji)
// with i < j given in order:
//
//	m_ij  = n_ij + n_ji      (a pair with m = 0 is skipped)
//	X²    = Σ (n_ij − n_ji)² / m_ij   (summed in the given pair order —
//	        the chi-square arm's uncorrected statistic, bit for bit)
//	H0    : n_ij ~ Binomial(m_ij, ½), independently, given every m_ij
//	p     = P(X² ≥ X²_obs), an outcome in when X²·(1 + 10⁻⁷) ≥ X²_obs
//
// computed exactly by convolving the pairs' contributions: a pair takes
// ⌊m/2⌋ + 1 values (|n_ij − n_ji| = m, m − 2, …), each with its
// symmetric binomial mass 2·C(m, a)/2^m (C(m, m/2)/2^m at a tie), from
// logBinomialDensity (R's dbinom_raw) through detmath.Exp. Pairs are
// convolved narrowest first; each step's terms are merged by value
// (bowkerMergeRelTol) in a deterministic order, and the tail is summed
// from the largest value down, clamped to at most 1. On one pair it is
// binom.test(n_12, m)'s two-sided p (BinomialTwoSidedP at ½): the
// outcomes at least as far from m/2 are exactly those of no greater
// density.
//
// terms counts the (value, mass) terms enumerated; when it would exceed
// maxTerms the convolution stops before allocating them and ok is false
// (stat is still returned, p is NaN). No pair kept (every m = 0) is
// stat = p = NaN with ok true: nothing to test. A negative count is NaN
// on both, ok true. FMA-free: every product feeding an add is an
// explicit float64(...) barrier, so the bits are the same on every
// architecture.
func BowkerExactP(pairs [][2]int, maxTerms int) (stat, p float64, terms int, ok bool) {
	widths := make([]int, 0, len(pairs))
	kept := make([]int, 0, len(pairs))
	for i, pr := range pairs {
		if pr[0] < 0 || pr[1] < 0 {
			return math.NaN(), math.NaN(), 0, true
		}
		m := pr[0] + pr[1]
		if m == 0 {
			continue
		}
		d := float64(pr[0] - pr[1])
		stat += float64(d*d) / float64(m)
		kept = append(kept, i)
		widths = append(widths, m/2+1)
	}
	if len(kept) == 0 {
		return math.NaN(), math.NaN(), 0, true
	}
	// Narrowest pair first (stable on the pair order): the support grows
	// slowest, so the enumerated terms stay fewest.
	order := make([]int, len(kept))
	for i := range order {
		order[i] = i
	}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && widths[order[j]] < widths[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	cur := []bowkerTerm{{v: 0, p: 1}}
	for _, o := range order {
		w := widths[o]
		if w > maxTerms || len(cur) > (maxTerms-terms)/w {
			return stat, math.NaN(), terms + len(cur)*w, false
		}
		terms += len(cur) * w
		pr := pairs[kept[o]]
		cur = bowkerConvolve(cur, bowkerPairTerms(pr[0]+pr[1]))
	}
	return stat, bowkerTail(cur, stat), terms, true
}

// bowkerTail is P(X² ≥ obs) over an ascending support: the mass of every
// value v with v·(1 + 10⁻⁷) ≥ obs (bowkerRelErr), summed from the largest
// value down, clamped to at most 1.
func bowkerTail(support []bowkerTerm, obs float64) float64 {
	var p float64
	for i := len(support) - 1; i >= 0; i-- {
		if float64(support[i].v*bowkerRelErr) < obs {
			break
		}
		p += support[i].p
	}
	return math.Min(1, p)
}

// bowkerPairTerms is one pair's contribution distribution, ascending in
// value: |d| = m − 2a for a = ⌊m/2⌋ down to 0, value d²/m, mass
// P(n_ij = a) + P(n_ij = m − a) (once when 2a = m).
func bowkerPairTerms(m int) []bowkerTerm {
	out := make([]bowkerTerm, 0, m/2+1)
	mf := float64(m)
	for a := m / 2; a >= 0; a-- {
		d := float64(m - 2*a)
		mass := detmath.Exp(logBinomialDensity(a, m, 0.5, 0.5))
		if 2*a != m {
			mass *= 2
		}
		out = append(out, bowkerTerm{v: float64(d*d) / mf, p: mass})
	}
	return out
}

// bowkerConvolve returns the distribution of X + Y for independent X
// (cur) and Y (pair), both ascending in value: the len(cur)·len(pair)
// sums are built as len(pair) runs, each ascending (adding a constant
// keeps the order under rounding), merged pairwise in run order (a
// stable merge, so the result is deterministic), then values within
// bowkerMergeRelTol of a group's first value are folded into it.
func bowkerConvolve(cur, pair []bowkerTerm) []bowkerTerm {
	n := len(cur)
	buf := make([]bowkerTerm, 0, n*len(pair))
	for _, y := range pair {
		for _, x := range cur {
			buf = append(buf, bowkerTerm{v: x.v + y.v, p: float64(x.p * y.p)})
		}
	}
	tmp := make([]bowkerTerm, len(buf))
	for width := n; width < len(buf); width *= 2 {
		for lo := 0; lo < len(buf); lo += 2 * width {
			mid := min(lo+width, len(buf))
			hi := min(lo+2*width, len(buf))
			i, j, k := lo, mid, lo
			for i < mid && j < hi {
				if buf[j].v < buf[i].v {
					tmp[k] = buf[j]
					j++
				} else {
					tmp[k] = buf[i]
					i++
				}
				k++
			}
			k += copy(tmp[k:], buf[i:mid])
			copy(tmp[k:], buf[j:hi])
		}
		buf, tmp = tmp, buf
	}
	out := buf[:0]
	for _, t := range buf {
		if last := len(out) - 1; last >= 0 && t.v-out[last].v <= float64(bowkerMergeRelTol*out[last].v) {
			out[last].p += t.p
			continue
		}
		out = append(out, t)
	}
	return out
}
