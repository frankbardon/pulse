package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestComposeOverlayWelfordComponentsRefusal: the rule's slot and
// aggregation selection, nil instance (hide nothing). Root
// TestComposeOverlay_WelfordComponentsDisabledRefused is the
// predict = runtime parity half.
func TestComposeOverlayWelfordComponentsRefusal(t *testing.T) {
	slot := func(label string, aggs ...types.AggregationType) *types.Request {
		r := &types.Request{Label: label, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}}
		for i, a := range aggs {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: a, Field: "x", Label: string(rune('a' + i))})
		}
		return r
	}
	spec := func(kind types.OverlayKind, params map[string]any) []types.ComposeOverlaySpec {
		return []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Reference: "r", Targets: []string{"t"}, Params: params}}
	}
	all := func(*types.Request) bool { return true }
	none := func(*types.Request) bool { return false }
	welf := func() []*types.Request {
		return []*types.Request{slot("r", types.AGG_WELFORD), slot("t", types.AGG_WELFORD), slot("u", types.AGG_WELFORD)}
	}
	crosstab := welf()
	crosstab[0].Crosstab, crosstab[1].Crosstab = &types.CrosstabSpec{}, &types.CrosstabSpec{}
	labels := []string{"r", "t", "u"}
	cases := []struct {
		name     string
		overlays []types.ComposeOverlaySpec
		requests []*types.Request
		disabled func(*types.Request) bool
		host     string
	}{
		{"t_vs_ref", spec(types.OverlayKindTVsRef, nil), welf(), all, "requests[0].aggregations[0]"},
		{"z_vs_ref", spec(types.OverlayKindZVsRef, nil), welf(), all, "requests[0].aggregations[0]"},
		{"field scores welford", spec(types.OverlayKindTVsRef, map[string]any{"field": "b"}),
			[]*types.Request{slot("r", types.AGG_SUM, types.AGG_WELFORD), slot("t", types.AGG_SUM, types.AGG_WELFORD)}, all, "requests[0].aggregations[1]"},
		{"field scores scalar", spec(types.OverlayKindTVsRef, map[string]any{"field": "a"}),
			[]*types.Request{slot("r", types.AGG_SUM, types.AGG_WELFORD), slot("t", types.AGG_SUM, types.AGG_WELFORD)}, all, ""},
		{"unknown field left to the field rule", spec(types.OverlayKindTVsRef, map[string]any{"field": "zz"}), welf(), all, ""},
		{"components on", spec(types.OverlayKindTVsRef, nil), welf(), none, ""},
		{"crosstab slots", spec(types.OverlayKindTVsRef, nil), crosstab, all, ""},
		{"kind without a payload edge", spec(types.OverlayKindIndexVsRef, nil), welf(), all, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ComposeOverlayWelfordComponentsRefusal(tc.overlays, tc.requests, labels, nil, tc.disabled)
			if tc.host == "" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != errors.PULSE_OVERLAY_COMPONENTS_REQUIRED || ce.Details["host"] != tc.host ||
				ce.Details["reason"] != "components_disabled" {
				t.Fatalf("got %v, want PULSE_OVERLAY_COMPONENTS_REQUIRED on %s", err, tc.host)
			}
		})
	}
}
