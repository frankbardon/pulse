package processing

import (
	"math"
	"testing"
)

// TestPairwiseWelfordKernels_UndefinedVarianceSkips: a Welford leg whose
// sample variance is undefined (NaN — a triple at N < 2 or Σw ≤ 1,
// matrix-extensions-hardening E4-S6) skips the pair as
// ORBIT_STATS_SKIP_VARIANCE_INVALID even where its sample size reads
// above 1 (an embedder-built triple); it never scores a NaN p-value as
// a present cell, nor reads the undefined variance as a 0.
func TestPairwiseWelfordKernels_UndefinedVarianceSkips(t *testing.T) {
	for name, k := range map[string]pwKernel{
		"welch_t":     pairwiseWelchTKernel,
		"two_means_z": pairwiseTwoMeansZKernel,
	} {
		for _, in := range []pwInputs{
			{m1: 1, m2: 2, v1: math.NaN(), v2: 4, nf1: 5, nf2: 5},
			{m1: 1, m2: 2, v1: 4, v2: math.NaN(), nf1: 5, nf2: 5},
		} {
			p, reason, ok := k(in)
			if ok || reason != "ORBIT_STATS_SKIP_VARIANCE_INVALID" {
				t.Errorf("%s(%+v) = (%v, %q, %v), want a VARIANCE_INVALID skip", name, in, p, reason, ok)
			}
		}
	}
}
