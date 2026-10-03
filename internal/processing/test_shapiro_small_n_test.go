package processing

import (
	"strings"
	"testing"
)

// TestShapiroFrancia_SmallNAdvisory: Royston's (1993) Shapiro-Francia
// z-transform is calibrated for 5 ≤ n ≤ 5000, so n = 3 or 4 still gets
// a p-value but carries an advisory warning, as n > 5000 does (U08
// statistics review S-19). n = 5 is inside the calibrated range.
func TestShapiroFrancia_SmallNAdvisory(t *testing.T) {
	cases := []struct {
		sample   []float64
		wantWarn bool
	}{
		{[]float64{1.0, 2.5, 4.0}, true},
		{[]float64{1.0, 2.5, 3.1, 4.0}, true},
		{[]float64{1.0, 2.5, 3.1, 4.0, 6.2}, false},
	}
	for _, c := range cases {
		_, _, p, warn := shapiroFranciaStat(c.sample)
		if p <= 0 || p > 1 {
			t.Errorf("n=%d: p = %g, want a p-value in (0, 1]", len(c.sample), p)
		}
		got := strings.Contains(warn, "below 5")
		if got != c.wantWarn {
			t.Errorf("n=%d: warning %q, want small-n advisory = %v", len(c.sample), warn, c.wantWarn)
		}
	}
}
