package overlayhost

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFacetHostArmRefusal_Rules: CHISQ_VS_POP refuses a numeric host,
// KS_VS_POP a discrete one (naming the label binding when the field is a
// labelled integer-coded one), INDEX_VS_POP / ZSCORE_VS_POP read either.
func TestFacetHostArmRefusal_Rules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind            types.OverlayKind
		discrete, coded bool
		refused         bool
	}{
		{types.OverlayKindChiSqVsPop, false, false, true},
		{types.OverlayKindChiSqVsPop, true, false, false},
		{types.OverlayKindChiSqVsPop, true, true, false},
		{types.OverlayKindKSVsPop, false, false, false},
		{types.OverlayKindKSVsPop, true, false, true},
		{types.OverlayKindKSVsPop, true, true, true},
		{types.OverlayKindIndexVsPop, true, true, false},
		{types.OverlayKindIndexVsPop, false, false, false},
		{types.OverlayKindZScoreVsPop, true, true, false},
		{types.OverlayKindZScoreVsPop, false, false, false},
	}
	for _, tc := range cases {
		ce := FacetHostArmRefusal(tc.kind, "f", "u8", tc.discrete, tc.coded, 2)
		if (ce != nil) != tc.refused {
			t.Errorf("%s discrete=%v coded=%v: refusal %v, want refused=%v", tc.kind, tc.discrete, tc.coded, ce, tc.refused)
			continue
		}
		if ce == nil {
			continue
		}
		want := map[string]any{"index": 2, "kind": string(tc.kind), "field": "f", "type": "u8"}
		if tc.coded {
			want["label_coded"] = true
		}
		if ce.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || !reflect.DeepEqual(ce.Details, want) {
			t.Errorf("%s: %s %v, want %v", tc.kind, ce.Code, ce.Details, want)
		}
	}
}
