package synth

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestEffectBands_LabelsCohenBoundaries pins the band edges: each
// threshold is the inclusive lower bound of its band, the region below
// "small" is "very small", and the sign of a d never changes its band.
func TestEffectBands_LabelsCohenBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		b    effectBands
		v    float64
		want string
	}{
		{effectBandsD, 0.0, "very small"},
		{effectBandsD, 0.199, "very small"},
		{effectBandsD, 0.2, "small"},
		{effectBandsD, -0.5, "medium"},
		{effectBandsD, 0.79, "medium"},
		{effectBandsD, -1.3, "large"},
		{effectBandsW, 0.05, "very small"},
		{effectBandsW, 0.1, "small"},
		{effectBandsW, 0.3, "medium"},
		{effectBandsW, 0.5, "large"},
	}
	for _, c := range cases {
		if got := c.b.label(c.v); got != c.want {
			t.Errorf("%s label(%v) = %q, want %q", c.b.convention, c.v, got, c.want)
		}
	}
}

// TestCohensD_KnownValue: two partitions one pooled SD apart give d = 1,
// signed synthetic minus source, and a constant pair gives no d at all.
func TestCohensD_KnownValue(t *testing.T) {
	t.Parallel()

	var src, syn marginalMoments
	for _, v := range []float64{1000, 1002, 1004, 1006} {
		src.add(v)
		syn.add(v - 2.5819888974716112) // one sample SD below
	}
	d, ok := cohensD(&src, &syn)
	if !ok || math.Abs(d+1) > 1e-9 {
		t.Fatalf("d = %v (ok %v), want -1", d, ok)
	}
	var c1, c2 marginalMoments
	c1.add(5)
	c1.add(5)
	c2.add(5)
	c2.add(5)
	if _, ok := cohensD(&c1, &c2); ok {
		t.Fatal("constant partitions produced a d")
	}
}

// TestChiSqCramersV_ReadsTheTestsOwnFigure: the categorical effect size
// is the χ² test's own details.effect_size.cramers_v, and an absent or
// non-finite one yields none.
func TestChiSqCramersV_ReadsTheTestsOwnFigure(t *testing.T) {
	t.Parallel()

	r := &types.TestResult{Details: map[string]any{"effect_size": map[string]any{"cramers_v": 0.04}}}
	if v, ok := chiSqCramersV(r); !ok || v != 0.04 {
		t.Fatalf("V = %v (ok %v), want 0.04", v, ok)
	}
	if _, ok := chiSqCramersV(&types.TestResult{Details: map[string]any{}}); ok {
		t.Fatal("V read from a result without one")
	}
	if _, ok := chiSqCramersV(nil); ok {
		t.Fatal("V read from a nil result")
	}
}
