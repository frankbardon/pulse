package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
)

// mwExactMaxWork caps the exact Mann-Whitney distribution's size:
// m·N ≤ 5000, m the smaller group's size and N the pooled size (Σw
// under a frequency weight: the expanded rows). The DP below costs
// about 2·m²·N² additions, so the cap holds it near 5·10⁷; it covers
// every sample R's own default runs exactly (both groups under 50).
// Beyond it the test keeps the normal approximation and warns
// PULSE_TEST_EXACT_UNAVAILABLE.
const mwExactMaxWork = 5000

// mannWhitneyExactP is the two-sided exact p of the rank sum of group
// a — R 4.6's wilcox.test(exact = TRUE): the permutation distribution
// of the sum of n_a scores drawn from the pooled mid-ranks, ties
// included (R's dpermdist2), so an untied sample is pwilcox's
// distribution. p = min(2·P(T ≤ t), 2·P(T ≥ t), 1), t the observed sum.
//
// ranks are the pooled rows' mid-ranks (weightedMidRanks: the expanded
// rows' under a frequency weight), weights their integer multiplicities
// (1 unweighted), inA marks group a's rows. Scores are doubled so a
// half-integer mid-rank is an integer; every count is a sum of
// non-negative terms, so the float64 counts carry relative error of
// order N·eps however large they grow. ok is false when the sample
// exceeds mwExactMaxWork.
func mannWhitneyExactP(ranks, weights []float64, inA []bool, nA, nB float64) (p float64, ok bool) {
	m, N := math.Min(nA, nB), nA+nB
	if m*N > mwExactMaxWork {
		return math.NaN(), false
	}
	// Draw the smaller group: its observed doubled score sum t.
	pickA := nA <= nB
	mi := int(m)
	maxS := 0
	t := 0
	for i, r := range ranks {
		s := int(math.Round(2 * r))
		maxS += s * int(weights[i])
		if inA[i] == pickA {
			t += s * int(weights[i])
		}
	}
	// h[k][s]: the number of k-subsets of the scores folded so far
	// summing to s.
	h := make([][]float64, mi+1)
	for k := range h {
		h[k] = make([]float64, maxS+1)
	}
	h[0][0] = 1
	folded, reach := 0, 0
	for i, r := range ranks {
		s := int(math.Round(2 * r))
		for range int(weights[i]) {
			folded++
			reach += s
			top := min(folded, mi)
			for k := top; k >= 1; k-- {
				dst, src := h[k], h[k-1]
				for u := reach; u >= s; u-- {
					if c := src[u-s]; c != 0 {
						dst[u] += c
					}
				}
			}
		}
	}
	var lower, upper, total float64
	for u, c := range h[mi] {
		if c == 0 {
			continue
		}
		total += c
		if u <= t {
			lower += c
		}
		if u >= t {
			upper += c
		}
	}
	return math.Min(math.Min(2*lower/total, 2*upper/total), 1), true
}

// exactUnavailableWarning is the PULSE_TEST_EXACT_UNAVAILABLE warning
// of a params.exact test whose sample exceeds the exact cap.
func exactUnavailableWarning(m, n float64) string {
	return fmt.Sprintf("%s: params.exact: the exact distribution is computed when min(n_a, n_b)·(n_a + n_b) ≤ %d, got %g; the p-value is the normal approximation",
		errors.PULSE_TEST_EXACT_UNAVAILABLE, mwExactMaxWork, m*n)
}
