package overlayhost

import (
	"math"
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
//
// params.min_periods (U50) is the present-point warm-up both kinds share:
// a point gets a figure only once the carrier holds at least min_periods
// present prior points (absent points never count). Absent, it is the
// window — a figure only over a full window. RollingMinPeriods resolves
// and bounds it, again for predict and runtime alike.

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

// MinPeriodsParam is the params key of the rolling warm-up.
const MinPeriodsParam = "min_periods"

// RollingMinPeriods resolves params.min_periods for a rolling kind whose
// window has already passed its own checks: absent → window; present →
// an integer in [RollingWindowMin(route), window], else
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE with Details {kind, param:
// "min_periods", min_periods, min, max} plus index when index >= 0.
// params is the decoded params object (a JSON number arrives float64).
func RollingMinPeriods(spec *types.OverlaySpec, route types.OverlayKind, params map[string]any, window, index int) (int, *errors.CodedError) {
	raw, present := params[MinPeriodsParam]
	if !present {
		return window, nil
	}
	min := RollingWindowMin(route)
	n, ok := integralParam(raw)
	if ok && n >= min && n <= window {
		return n, nil
	}
	var echo any = raw
	if ok {
		echo = n
	}
	kind := ""
	if spec != nil {
		kind = string(spec.Kind)
	}
	details := map[string]any{
		"kind":          kind,
		"param":         MinPeriodsParam,
		MinPeriodsParam: echo,
		"min":           min,
		"max":           window,
	}
	if index >= 0 {
		details["index"] = index
	}
	return 0, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
		"overlay "+kind+" Params[\"min_periods\"] must be an integer from "+strconv.Itoa(min)+
			" to the window ("+strconv.Itoa(window)+")",
		details)
}

// integralParam coerces a decoded params number to an int: an int kind,
// or a finite integral float. Anything else (string, bool, fraction) is
// (0, false).
func integralParam(v any) (int, bool) {
	var f float64
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		f = n
	case float32:
		f = float64(n)
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int(f), true
}
