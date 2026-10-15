package processing

import (
	"math"
	"testing"
)

// TestPairwiseWelfordKernels_UndefinedVarianceSkips: a Welford leg whose
// sample variance is undefined (NaN — a triple at N < 2 or Σw ≤ 1,
// matrix-extensions-hardening E4-S6) skips the pair as
// PULSE_STATS_SKIP_VARIANCE_INVALID even where its sample size reads
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
			if ok || reason != "PULSE_STATS_SKIP_VARIANCE_INVALID" {
				t.Errorf("%s(%+v) = (%v, %q, %v), want a VARIANCE_INVALID skip", name, in, p, reason, ok)
			}
		}
	}
}

// TestPairwiseProportionKernels_OutOfUnitProportionSkips: a proportion
// leg outside [0, 1] — a p_source of cell_value over a raw count cell —
// skips the pair as PULSE_STATS_SKIP_PROPORTION_INVALID on both
// proportion kernels. Before, the probit kernel clipped it to 1-ε and
// scored p = 1 for every pair, and prop-z scored a finite p whenever the
// pooled proportion happened to land inside (0, 1): a silent wrong
// answer rather than a coded skip.
func TestPairwiseProportionKernels_OutOfUnitProportionSkips(t *testing.T) {
	for name, k := range map[string]pwKernel{
		"prop_z":   pairwisePropZKernel,
		"probit_t": pairwiseProbitTKernel,
	} {
		for _, in := range []pwInputs{
			{p1: 12, p2: 30, nf1: 40, nf2: 60},   // raw counts read as proportions
			{p1: 1.2, p2: 0.3, nf1: 50, nf2: 50}, // pooled lands inside (0, 1)
			{p1: 0.4, p2: -0.1, nf1: 50, nf2: 50},
		} {
			p, reason, ok := k(in)
			if ok || reason != "PULSE_STATS_SKIP_PROPORTION_INVALID" {
				t.Errorf("%s(%+v) = (%v, %q, %v), want a PROPORTION_INVALID skip", name, in, p, reason, ok)
			}
		}
		// The unit interval's end points stay admissible (probit clips
		// them; prop-z scores them when the pooled SE is defined).
		if _, reason, _ := k(pwInputs{p1: 0, p2: 1, nf1: 50, nf2: 50}); reason == "PULSE_STATS_SKIP_PROPORTION_INVALID" {
			t.Errorf("%s: an end-point proportion skipped as PROPORTION_INVALID", name)
		}
	}
}
