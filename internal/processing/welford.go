package processing

import "math"

// WelfordStdDev computes the population standard deviation of values
// using the single-pass Welford recurrence over central moment M2:
//
//	mean_n = mean_{n-1} + (x_n - mean_{n-1}) / n
//	M2_n   = M2_{n-1}   + (x_n - mean_{n-1}) * (x_n - mean_n)
//
// and finalises sd = sqrt(M2 / n).
//
// Population (divide by N) — not sample (divide by N-1). Overlay
// margins describe the FULL population of cells contributing to a
// margin slice (every row cell within the row-margin slice, every
// column cell within the column-margin slice, every matrix cell within
// the grand-margin slice). A z-score against the full population uses
// the population sd; the sample sd would imply we are inferring a
// wider population from a sample, which is not the overlay's intent.
//
// Returns 0 for len(values) == 0 to keep callers branch-free. Returns
// 0 for len(values) == 1 because a single-observation slice has
// undefined variance — callers (e.g. ZSCORE_VS_MARGIN) treat the zero
// return identically to a zero-denominator slice and surface the
// matching PULSE_OVERLAY_REF_ZERO warning.
//
// Why this single function rather than the per-bucket welfordBucket
// machinery in test_t.go / test_z.go / test_anova_welch.go: those
// pre-existing helpers are coupled to per-key state across the
// streaming Process path (groups map, insertion order, sample-vs-
// population variance toggles). The overlay handler walks a closed
// margin slice synchronously and needs only a single number out; a
// purpose-built helper keeps the surface narrow and the call sites
// readable without re-deriving the recurrence inline. Future
// overlays that need streaming Welford may either reuse this helper
// against an accumulated slice or be lifted alongside the existing
// per-bucket state — both options remain open.
func WelfordStdDev(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	var (
		mean float64
		m2   float64
	)
	for i, v := range values {
		delta := v - mean
		mean += delta / float64(i+1)
		delta2 := v - mean
		m2 += delta * delta2
	}
	variance := m2 / float64(n)
	return math.Sqrt(variance)
}
