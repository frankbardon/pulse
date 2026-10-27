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
	t.Parallel()

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

// TestRollingMinPeriods (U50 E2-S3): params.min_periods defaults to the
// window; an explicit value must be an integer in [RollingWindowMin,
// window] — 2 for the rolling z-score (a sample SD needs two points), 1
// for the rolling-mean index — else PULSE_OVERLAY_LEVEL_OUT_OF_RANGE with
// {kind, param: "min_periods", min_periods, min, max} (+ index when >= 0).
func TestRollingMinPeriods(t *testing.T) {
	t.Parallel()

	z := &types.OverlaySpec{Kind: types.OverlayKindZScoreVsRolling}
	idx := &types.OverlaySpec{Kind: types.OverlayKindIndexVsRollingMean}
	route := func(s *types.OverlaySpec) types.OverlayKind { return s.Kind }

	for _, c := range []struct {
		spec   *types.OverlaySpec
		params map[string]any
		window int
		want   int
	}{
		{z, map[string]any{"window": 28.0}, 28, 28},
		{idx, nil, 5, 5},
		{z, map[string]any{"min_periods": 2.0}, 28, 2},
		{z, map[string]any{"min_periods": 28.0}, 28, 28},
		{idx, map[string]any{"min_periods": 1.0}, 3, 1},
		{idx, map[string]any{"min_periods": 3}, 3, 3},
	} {
		got, ce := RollingMinPeriods(c.spec, route(c.spec), c.params, c.window, 0)
		if ce != nil || got != c.want {
			t.Errorf("%s %v window %d: got %d %v, want %d", c.spec.Kind, c.params, c.window, got, ce, c.want)
		}
	}

	for _, c := range []struct {
		spec   *types.OverlaySpec
		raw    any
		window int
		min    int
	}{
		{z, 1.0, 28, 2},
		{z, 29.0, 28, 2},
		{z, 2.5, 28, 2},
		{z, "3", 28, 2},
		{z, true, 28, 2},
		{idx, 0.0, 3, 1},
		{idx, -1.0, 3, 1},
		{idx, 4.0, 3, 1},
	} {
		_, ce := RollingMinPeriods(c.spec, route(c.spec), map[string]any{"min_periods": c.raw}, c.window, 7)
		if ce == nil || ce.Code != errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE {
			t.Fatalf("%s min_periods %v: want LEVEL_OUT_OF_RANGE, got %+v", c.spec.Kind, c.raw, ce)
		}
		if ce.Details["param"] != "min_periods" || ce.Details["min"] != c.min || ce.Details["max"] != c.window ||
			ce.Details["index"] != 7 || ce.Details["kind"] != string(c.spec.Kind) {
			t.Errorf("%s min_periods %v: details %#v", c.spec.Kind, c.raw, ce.Details)
		}
	}
	if _, ce := RollingMinPeriods(z, route(z), map[string]any{"min_periods": 1.0}, 28, -1); ce == nil || ce.Details["index"] != nil {
		t.Fatalf("runtime form carries no index: %+v", ce)
	}
}
