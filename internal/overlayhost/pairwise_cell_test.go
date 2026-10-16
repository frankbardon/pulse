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

// TestPairwiseMeanCellRefusal pins the omitted-p_source rule over an
// unnormalised host (survey-tables E4-S6): a built-in statistic cell is
// refused, a count / sum cell, an extension or unknown type, an explicit
// p_source and a normalised host are not.
func TestPairwiseMeanCellRefusal(t *testing.T) {
	k := types.OverlayKindPairwisePropZ
	cases := []struct {
		pSource string
		mode    types.CrosstabNormalize
		cell    types.AggregationType
		refused bool
	}{
		{"", "", types.AGG_AVERAGE, true},
		{"", types.CrosstabNormalizeNone, types.AGG_WELFORD, true},
		{"", types.CrosstabNormalizeNone, types.AGG_MEDIAN, true},
		{"", "", types.AGG_COUNT, false},
		{"", "", types.AGG_SUM, false},
		{"", "", types.AGG_FREQUENCY, false},
		{"", "", "", false},
		{"", "", "AGG_ACME_SHARE", false},
		{types.PairwisePSourceCellValue, "", types.AGG_AVERAGE, false},
		{types.PairwisePSourceCellValuePct, "", types.AGG_AVERAGE, false},
		{"", types.CrosstabNormalizeColumn, types.AGG_AVERAGE, false},
	}
	for _, c := range cases {
		msg := PairwiseMeanCellRefusal(k, c.pSource, c.mode, c.cell)
		if (msg != "") != c.refused {
			t.Errorf("p_source %q normalize %q cell %q: refused = %v, want %v (%q)", c.pSource, c.mode, c.cell, msg != "", c.refused, msg)
		}
	}
}
