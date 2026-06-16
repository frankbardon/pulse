package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestValidateOverlay_YoYIncompatibleFrequency(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			// "yearly" is not in the supported set
			// (annual|quarterly|monthly|weekly|daily|hourly).
			Params: json.RawMessage(`{"frequency": "yearly"}`),
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY) {
		t.Fatalf("expected PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY for unsupported frequency")
	}
}

func TestValidateOverlay_YoYNonDateGrouper(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// siblingHostReq uses GROUP_CATEGORY over "region" — not a DATE host.
	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for non-GROUP_DATE host")
	}
}
