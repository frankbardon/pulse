package statdist

import (
	"math"
	"testing"
)

// TestStatdist_StudentTBitsPinned is the cross-architecture gate on the
// Student-t family behind every engine ci_lower / ci_upper and t-based
// p-value: StudentTQuantile, StudentTTwoSidedP and StudentTCDF must
// equal the pinned bits exactly on every GOARCH. They run on
// linalg/detmath and barriered products; with math.Log / Exp / Lgamma
// and contractible products they sat up to ~100 ulp apart between arm64
// and amd64. Pinned on darwin/arm64 and checked identical on
// linux/amd64 (docker); CI re-runs it on linux/amd64.
func TestStatdist_StudentTBitsPinned(t *testing.T) {
	t.Parallel()

	quantiles := []struct {
		p, df float64
		want  uint64
	}{
		{0.975, 1, 0x40296993aacc4d1b},
		{0.975, 2, 0x401135ea98e146b9},
		{0.975, 5, 0x4004908d359dff38},
		{0.975, 9, 0x400218e5dac50b24},
		{0.975, 29, 0x40005ca15bce2873},
		{0.975, 120, 0x3fffadcb8122f64e},
		{0.995, 7, 0x400bfef119582619},
		{0.95, 15, 0x3ffc0c7e879f0d5e},
		{0.025, 3, 0xc00975a66893c1a9},
		{0.9, 4.5, 0x3ff803a0529beeef},
		{0.999, 50, 0x400a175da18c4190},
		{0.6, 11, 0x3fd09c902f0ec427},
		{0.975, 10000, 0x3fff5cfbf9ae8ac6},
		{1e-10, 3, 0xc0a16389dfae9667},
	}
	for _, c := range quantiles {
		if got := StudentTQuantile(c.p, c.df); math.Float64bits(got) != c.want {
			t.Errorf("StudentTQuantile(%v, %v) = %v (%#016x), want %v (%#016x)",
				c.p, c.df, got, math.Float64bits(got), math.Float64frombits(c.want), c.want)
		}
	}
	tails := []struct {
		t, df      float64
		twoSided   uint64
		cumulative uint64
	}{
		{2.1, 5, 0x3fb6fa11a8f6cd24, 0x3fee905ee570932e},
		{0.3, 12, 0x3fe89e30ffd5bcd4, 0x3fe3b0e780152196},
		{4.7, 30, 0x3f0c75b1331216cc, 0x3fefffc7149d99dc},
		{1.96, 1000, 0x3fa9bd682a805cc7, 0x3fef3214beabfd1a},
		{-1.2, 7, 0x3fd13a1cee6b2006, 0x3fc13a1cee6b2006},
	}
	for _, c := range tails {
		if got := StudentTTwoSidedP(c.t, c.df); math.Float64bits(got) != c.twoSided {
			t.Errorf("StudentTTwoSidedP(%v, %v) = %v (%#016x), want %#016x", c.t, c.df, got, math.Float64bits(got), c.twoSided)
		}
		if got := StudentTCDF(c.t, c.df); math.Float64bits(got) != c.cumulative {
			t.Errorf("StudentTCDF(%v, %v) = %v (%#016x), want %#016x", c.t, c.df, got, math.Float64bits(got), c.cumulative)
		}
	}
}
