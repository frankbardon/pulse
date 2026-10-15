package overlayhost

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestSampleVariance(t *testing.T) {
	for _, c := range []struct {
		pop, m2 float64
		hasM2   bool
		n, want float64
	}{
		{26.0 / 4, 26, true, 4, 26.0 / 3},
		{26.0 / 4, 0, false, 4, 26.0 / 4 * 4 / 3},
		{0, 0, true, 1, math.NaN()},
		{math.NaN(), 0, true, 0, math.NaN()},
	} {
		got := SampleVariance(c.pop, c.m2, c.hasM2, c.n)
		if math.IsNaN(c.want) != math.IsNaN(got) || (!math.IsNaN(got) && got != c.want) {
			t.Errorf("SampleVariance(%v, %v, %v, %v) = %v, want %v", c.pop, c.m2, c.hasM2, c.n, got, c.want)
		}
	}
	for ct, want := range map[types.AggregationType]bool{
		types.AGG_VARIANCE: true, types.AGG_STDDEV: true, types.AGG_WELFORD: false, "AGG_ACME_X": false,
	} {
		if PopulationVarianceCell(ct) != want {
			t.Errorf("PopulationVarianceCell(%s) != %v", ct, want)
		}
	}
	if ComponentsRequiredMessage(types.OverlayKindPairwisePropZ) == "" {
		t.Error("empty message")
	}
}
