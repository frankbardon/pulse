package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPriorMeasureRefusal: the shared type rule of the prior-period
// kinds — T / Z admit the Welford family, the proportion kind
// AGG_AVERAGE; an empty or extension type is never judged here; every
// other kind is untouched.
func TestPriorMeasureRefusal(t *testing.T) {
	cases := []struct {
		route   types.OverlayKind
		measure types.AggregationType
		refused bool
	}{
		{types.OverlayKindTVsPrior, types.AGG_WELFORD, false},
		{types.OverlayKindTVsPrior, types.AGG_VARIANCE, false},
		{types.OverlayKindZVsPrior, types.AGG_STDDEV, false},
		{types.OverlayKindZVsPrior, types.AGG_AVERAGE, true},
		{types.OverlayKindTVsPrior, types.AGG_SUM, true},
		{types.OverlayKindPropZVsPrior, types.AGG_AVERAGE, false},
		{types.OverlayKindPropZVsPrior, types.AGG_WELFORD, true},
		{types.OverlayKindPropZVsPrior, types.AGG_COUNT, true},
		{types.OverlayKindTVsPrior, "", false},
		{types.OverlayKindPropZVsPrior, "AGG_ACME_SHARE", false},
		{types.OverlayKindDeltaVsPrior, types.AGG_SUM, false},
	}
	for _, c := range cases {
		cerr := PriorMeasureRefusal(c.route, c.route, c.measure, 2)
		if (cerr != nil) != c.refused {
			t.Errorf("%s over %q: refused = %v, want %v", c.route, c.measure, cerr != nil, c.refused)
			continue
		}
		if cerr == nil {
			continue
		}
		if cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || cerr.Details["index"] != 2 ||
			cerr.Details["measure_type"] != string(c.measure) || cerr.Details["kind"] != string(c.route) {
			t.Errorf("%s over %s: %v %v", c.route, c.measure, cerr.Code, cerr.Details)
		}
	}
}

// TestPriorShapeRefusals: the column-grouper and normalized refusals
// fire only for their kinds, and index -1 (a runtime call that does not
// know the slot index) omits the index detail.
func TestPriorShapeRefusals(t *testing.T) {
	if PriorColumnsRefusal(types.OverlayKindPairwisePropZ, types.OverlayKindPairwisePropZ, 0) != nil {
		t.Error("PriorColumnsRefusal refused a pairwise kind")
	}
	cerr := PriorColumnsRefusal(types.OverlayKindTVsPrior, types.OverlayKindTVsPrior, -1)
	if cerr == nil || cerr.Details["reason"] != "column_grouper" {
		t.Fatalf("PriorColumnsRefusal = %v", cerr)
	}
	if _, ok := cerr.Details["index"]; ok {
		t.Errorf("index -1 kept the index detail: %v", cerr.Details)
	}
	for _, mode := range []types.CrosstabNormalize{"", types.CrosstabNormalizeNone} {
		if PriorNormalizedRefusal(types.OverlayKindPropZVsPrior, types.OverlayKindPropZVsPrior, mode, 0) != nil {
			t.Errorf("normalize %q refused", mode)
		}
	}
	if PriorNormalizedRefusal(types.OverlayKindTVsPrior, types.OverlayKindTVsPrior, types.CrosstabNormalizeColumn, 0) != nil {
		t.Error("the normalized rule refused a mean kind")
	}
	cerr = PriorNormalizedRefusal(types.OverlayKindPropZVsPrior, types.OverlayKindPropZVsPrior, types.CrosstabNormalizeRow, 1)
	if cerr == nil || cerr.Details["normalize"] != string(types.CrosstabNormalizeRow) || cerr.Details["index"] != 1 {
		t.Errorf("PriorNormalizedRefusal = %v", cerr)
	}
}
