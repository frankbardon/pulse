package overlayhost

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The rolling-window floor of the two Params["window"] SERIES kinds,
// shared by predict (descriptor.validateOverlayRollingWindowParam) and
// the runtime handlers (processing.extractWindowParam callers) so the
// refusal cannot drift (U35 #431).
//
//   - OVERLAY_INDEX_VS_ROLLING_MEAN: window >= 1. The mean of a
//     one-point window is that point, so window 1 indexes each point
//     against the one before it.
//   - OVERLAY_ZSCORE_VS_ROLLING: window >= 2. Its statistic divides by
//     the window's SAMPLE SD (n - 1 denominator), undefined over one
//     point — window 1 would answer null at every row, silently.

// RollingWindowMin is the smallest Params["window"] route accepts: 2
// for OVERLAY_ZSCORE_VS_ROLLING, else 1.
func RollingWindowMin(route types.OverlayKind) int {
	if route == types.OverlayKindZScoreVsRolling {
		return 2
	}
	return 1
}

// RollingWindowFloorRefusal refuses a positive window below
// RollingWindowMin(route) — PULSE_OVERLAY_LEVEL_OUT_OF_RANGE, Details
// {kind, param: "window", window, min} plus index when index >= 0. nil
// when window meets the floor; a non-positive window is the caller's
// own "must be > 0" refusal, judged first.
func RollingWindowFloorRefusal(spec *types.OverlaySpec, route types.OverlayKind, window, index int) *errors.CodedError {
	min := RollingWindowMin(route)
	if spec == nil || window <= 0 || window >= min {
		return nil
	}
	details := map[string]any{
		"kind":   string(spec.Kind),
		"param":  "window",
		"window": window,
		"min":    min,
	}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
		"overlay "+string(spec.Kind)+" Params[\"window\"] must be >= "+strconv.Itoa(min)+
			" (the rolling sample SD is undefined over fewer than two points)",
		details)
}
