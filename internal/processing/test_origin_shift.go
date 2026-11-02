package processing

import "math"

// originShift is the accumulation origin of the per-group Welford
// tests and of AGG_WELFORD: the first observed value. A bucket
// accumulates v − origin, so its running mean is held at the
// deviations' magnitude, not the data's. Held at the data's magnitude,
// a mean rounds to that magnitude's ulp, and under a large common
// offset (NIST SmLs07–09, 1e12 + deviations of 0.1) differences of
// means lost three digits. Differences, variances and sums of squares
// are shift-invariant, so only a reported level (a mean, a weighted
// mean) adds the origin back, and a difference of two buckets' means
// reads both origins (shiftedMeanDiff). Rows stream in a fixed order
// and a merge re-expresses the other partial about the receiver's
// origin (welfordAggregator.MergeOnline), so the origin is
// deterministic. A non-finite first value leaves the origin at 0: a
// shift by ±Inf would turn every later deviation into NaN.
type originShift struct {
	origin float64
	set    bool
}

// of returns v relative to the origin, fixing the origin at the first
// call.
func (s *originShift) of(v float64) float64 {
	if !s.set {
		s.set = true
		if !math.IsInf(v, 0) && !math.IsNaN(v) {
			s.origin = v
		}
	}
	return v - s.origin
}

// level returns a shifted mean as a level on the data's scale.
func (s *originShift) level(m float64) float64 { return m + s.origin }

func (s *originShift) reset() { *s = originShift{} }

// shiftedMeanDiff is mean_a − mean_b of two buckets held about their
// own origins: oa, ob the origins, sa, sb the shifted means. Under a
// large common offset the origins lie within a factor of two of each
// other, so oa − ob is exact (Sterbenz) and the difference keeps the
// deviations' precision; subtracting the two levels instead would
// round each to the offset's ulp first. TEST_T / TEST_WELCH /
// TEST_Z_TWO_SAMPLE and the moment overlays (meanLeg.diff) both take
// the difference here, so their bytes agree (#637).
func shiftedMeanDiff(oa, sa, ob, sb float64) float64 {
	return (oa - ob) + (sa - sb)
}
