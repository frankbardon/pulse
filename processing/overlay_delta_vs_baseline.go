package processing

import (
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_DELTA_VS_BASELINE — per-point absolute additive delta against a
// single fixed positional baseline of an ordered SERIES (grouped Process)
// host.
//
// Behaviour:
//
//   - Windowed-Process overlay consuming the
//     `Ref.BaselineIndex.Position` arm of the OverlayBaselineIndexRef
//     discriminated union. Absolute-difference sibling of
//     OVERLAY_INDEX_VS_BASELINE.
//   - Per-point math: `delta_i = point_value_i - baseline_value` where
//     `baseline_value = host.ValueAt(Ref.BaselineIndex.Position)`. The
//     baseline is resolved ONCE at handler entry via
//     ResolveBaselineIndex.
//   - Baseline-at-self: a present point at the baseline ordinal yields
//     `0.0` (self-vs-self under additive subtraction). Mirrors the
//     `OVERLAY_DELTA_VS_MARGIN` / `OVERLAY_DELTA_VS_SIBLING`
//     `baseline = 0` rendering convention.
//   - Zero-baseline semantics: unlike OVERLAY_INDEX_VS_BASELINE (which
//     divides by the baseline and rejects zero with
//     PULSE_OVERLAY_REF_ZERO), DELTA_VS_BASELINE performs subtraction and
//     is mathematically defined for every finite baseline value including
//     zero. The handler does NOT emit PULSE_OVERLAY_REF_ZERO; a zero
//     baseline simply yields `delta_i = point_value_i - 0 = point_value_i`
//     (the raw host value passes through). Mirrors the existing
//     OVERLAY_DELTA_VS_SIBLING rule against zero sibling values.
//   - Absent-point policy: a host that did not produce a value for group
//     i (the resolver returns `(0, false)`) surfaces a SeriesEntry whose
//     Summary leaves Statistic unset — the canonical "present slot,
//     empty summary" shape from the SERIES dispatch contract.
//     Absent groups do NOT participate in the delta computation. An
//     absent baseline (`present=false` at the baseline ordinal) yields a
//     baseline value of `0.0` from the host; subsequent points subtract
//     that zero and the layer carries the raw host values verbatim (no
//     warning — distinct from the OVERLAY_INDEX_VS_BASELINE zero-baseline
//     PULSE_OVERLAY_REF_ZERO arm).
//
// Buffered (per kind-catalog-v1 "Streaming-capable subset"): resolving a
// single positional baseline requires the materialised host series; the
// handler runs at the buffered post-host-finalize exit via
// ApplyOverlaysSeries. Forward-compat: a future story may lift the
// baseline resolver into a streaming-aware shape; when that lands the
// streamability row in types/overlay_streamability.go flips to true.
//
// Layer-level Summary: baseline = 0 (renderers centre diverging colour
// ramps on 0 because the delta is a unit-preserving deviation, not a
// ratio). Min / Max / Count populate from present entries.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime overlay
//     execution rides inside processing/ alongside the aggregator /
//     attribute / grouper layers (mirrors overlay.go / overlay_series.go
//     / overlay_index_vs_baseline.go).
//   - No fmt.Sprintf in any JSON-bearing path. The handler emits no
//     warnings today; if a future change adds one it must use string
//     concatenation against the structural defense ban.

// applyDeltaVsBaseline is the OVERLAY_DELTA_VS_BASELINE runtime handler.
// For every host group ordinal it surfaces `point - baseline` on the
// SeriesEntry's Summary.Statistic. The baseline is resolved ONCE at
// handler entry via ResolveBaselineIndex.
//
// Resolver propagation: a coded error from ResolveBaselineIndex (negative
// Position, out-of-range Position, nil host) short-circuits the handler
// and propagates the CodedError directly. The resolver's CodedError
// carries `code = PULSE_OVERLAY_REF_UNKNOWN` plus `{baseline_index,
// series_length}` Details — the runtime range-check surface for this
// kind, identical to OVERLAY_INDEX_VS_BASELINE.
//
// Zero-baseline path: NOT applicable. Subtraction is total — there is no
// zero-denominator hazard for DELTA_VS_BASELINE. A zero baseline simply
// yields `delta = point - 0 = point` and the handler emits no warning.
// This is the key contract contrast with OVERLAY_INDEX_VS_BASELINE.
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler defends against a nil host
// (caller passed nil into ApplyOverlaysSeries) by returning the resolver's
// coded PROCESSING_INTERNAL error — that branch is unreachable in
// practice because ApplyOverlaysSeries short-circuits empty specs before
// dispatch, but the defense matches the INDEX_VS_BASELINE / INDEX_VS_TOTAL
// safety pattern.
func applyDeltaVsBaseline(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	baselineValue, err := ResolveBaselineIndex(host, spec.Ref.BaselineIndex)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	groupCount := host.GroupCount()

	entries := make([]types.SeriesEntry, 0, groupCount)
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := 0; i < groupCount; i++ {
		key, _ := host.GroupKey(i)
		var keyCopy types.AxisKey
		if key != nil {
			keyCopy = append(types.AxisKey(nil), key...)
		}

		value, present := host.ValueAt(i)
		var summary types.OverlaySummary
		if present {
			delta := value - baselineValue
			deltaCopy := delta
			summary.Statistic = &deltaCopy
			if seen == 0 {
				minV, maxV = delta, delta
			} else {
				if delta < minV {
					minV = delta
				}
				if delta > maxV {
					maxV = delta
				}
			}
			seen++
		}
		// Absent host point: emit a present SeriesEntry with an unset
		// Summary.Statistic (the canonical "present slot, empty summary"
		// shape).
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: entries,
			},
		},
	}

	// Layer-level Summary: baseline = 0 (mirrors OVERLAY_DELTA_VS_MARGIN
	// / OVERLAY_DELTA_VS_SIBLING / OVERLAY_ZSCORE_VS_* — the delta family
	// centres diverging colour ramps on 0 because the output is a unit-
	// preserving deviation, not a ratio). Min / Max / Count populate from
	// present entries.
	baseline := 0.0
	summary := &types.OverlaySummary{Baseline: &baseline}
	if seen > 0 {
		mn, mx, count := minV, maxV, seen
		summary.Min = &mn
		summary.Max = &mx
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}
	layer.Summary = summary

	return layer, nil, nil
}
