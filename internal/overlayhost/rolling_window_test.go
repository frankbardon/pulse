package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestRollingWindowFloorRefusal (U35 #431): the rolling z-score refuses
// a window below 2 (a one-point sample SD); the rolling-mean index
// takes window 1; a non-positive window is the caller's own refusal.
func TestRollingWindowFloorRefusal(t *testing.T) {
	z := &types.OverlaySpec{Kind: types.OverlayKindZScoreVsRolling}
	idx := &types.OverlaySpec{Kind: types.OverlayKindIndexVsRollingMean}
	if RollingWindowMin(types.OverlayKindZScoreVsRolling) != 2 || RollingWindowMin(types.OverlayKindIndexVsRollingMean) != 1 {
		t.Fatal("floors: want 2 (z-score) and 1 (index)")
	}
	ce := RollingWindowFloorRefusal(z, types.OverlayKindZScoreVsRolling, 1, 3)
	if ce == nil || ce.Code != errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE ||
		ce.Details["min"] != 2 || ce.Details["window"] != 1 || ce.Details["index"] != 3 || ce.Details["param"] != "window" {
		t.Fatalf("z-score window 1: %+v", ce)
	}
	if ce := RollingWindowFloorRefusal(z, types.OverlayKindZScoreVsRolling, 1, -1); ce == nil || ce.Details["index"] != nil {
		t.Fatalf("runtime form carries no index: %+v", ce)
	}
	for _, c := range []struct {
		spec   *types.OverlaySpec
		window int
	}{{z, 2}, {z, 0}, {z, -1}, {idx, 1}, {idx, 5}, {nil, 1}} {
		route := types.OverlayKindZScoreVsRolling
		if c.spec == idx {
			route = types.OverlayKindIndexVsRollingMean
		}
		if ce := RollingWindowFloorRefusal(c.spec, route, c.window, 0); ce != nil {
			t.Errorf("window %d: refused %v", c.window, ce)
		}
	}
}
