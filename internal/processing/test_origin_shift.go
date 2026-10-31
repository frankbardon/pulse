package processing

// originShift is the accumulation origin of the per-group Welford
// tests: the first observed value. A bucket accumulates v − origin, so
// its running mean is held at the deviations' magnitude, not the
// data's. Held at the data's magnitude, a mean rounds to that
// magnitude's ulp, and under a large common offset (NIST SmLs07–09,
// 1e12 + deviations of 0.1) differences of means lost three digits.
// Differences, variances and sums of squares are shift-invariant, so
// only a reported level (a mean, a weighted mean) adds the origin back.
// Rows stream in a fixed order and the state never merges, so the
// origin is deterministic.
type originShift struct {
	origin float64
	set    bool
}

// of returns v relative to the origin, fixing the origin at the first
// call.
func (s *originShift) of(v float64) float64 {
	if !s.set {
		s.origin, s.set = v, true
	}
	return v - s.origin
}

// level returns a shifted mean as a level on the data's scale.
func (s *originShift) level(m float64) float64 { return m + s.origin }

func (s *originShift) reset() { *s = originShift{} }
