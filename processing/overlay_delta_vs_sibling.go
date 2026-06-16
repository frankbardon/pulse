package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_DELTA_VS_SIBLING — per-group additive delta against a sibling
// group named in `Ref.Sibling`.
//
// Behaviour:
//
//   - Sibling-reference SERIES-host handler (alphabetised between
//     DELTA_VS_MARGIN and FISHER_EXACT_CELL). Registered in
//     processing/overlay_series.go's
//     seriesOverlayHandlers dispatch table; the dispatch route is the
//     post-host-finalize entry point. Buffered (per kind-catalog-v1
//     "Streaming-capable subset"): the streaming Process pass cannot
//     resolve a `(Field, Value)` sibling lookup against the per-group
//     accumulators until they are finalised, so the handler runs at
//     the buffered post-host-finalize exit.
//   - Per-group math: `delta_i = group_val[i] - sibling_val` where the
//     sibling is a single fixed host group identified by
//     `Ref.Sibling.{Field, Value}`. The sibling group itself emits
//     `0` (self-vs-self under additive subtraction). Sibling resolves
//     via `resolveSibling` (processing/overlay_sibling_resolver.go) —
//     a single `(field, value)` lookup against the host's group-key
//     list.
//
// Unknown-sibling path: when the resolver returns `(_, false)` — the
// named field is not a grouper field on the host OR the named value
// does not match any observed axis-key value — the handler emits ONE
// `PULSE_OVERLAY_REF_UNKNOWN` warning carrying the offending
// `(field, value)` pair and surfaces NaN statistics across every
// present entry. Mirrors INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES'
// PULSE_OVERLAY_REF_ZERO emission shape (one warning per layer, not
// per cell). The handler still surfaces one entry per host group
// ordinal — the parallel-slice contract is preserved regardless of
// reference health.
//
// Zero-sibling path: DELTA is mathematically defined when the sibling
// resolves AND its value is zero — the delta becomes `group_val[i] -
// 0 = group_val[i]`, the host's raw value. The handler does NOT emit
// PULSE_OVERLAY_REF_ZERO on this kind (distinct from the
// INDEX_VS_SIBLING twin which divides by sibling and rejects zero).
//
// Absent-group policy: a host that did not produce a value for group
// i (resolver returns `(0, false)`) surfaces a SeriesEntry whose
// Summary leaves Statistic unset — the canonical "present slot, empty
// summary" shape from the SERIES dispatch contract. Absent
// groups do NOT participate in the delta computation. Critically, the
// sibling group's own entry surfaces a delta of `0.0` when present
// (self-vs-self) — NOT a nil Statistic.
//
// Output units: the layer's Statistic preserves the host cell's units
// — a $-valued AGG_SUM group minus a $-valued sibling AGG_SUM group
// yields a $-valued deviation in the same currency. Renderers centre
// diverging colour ramps on `baseline = 0` (mirrors DELTA_VS_MARGIN
// and ZSCORE_VS_*).
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go / overlay_index_vs_sibling.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays grep-
//     clean against the structural defense ban.

// applyDeltaVsSibling is the OVERLAY_DELTA_VS_SIBLING runtime handler.
// For every host group ordinal it surfaces
// `group_val[i] - sibling_val` on the SeriesEntry's
// Summary.Statistic. Walks the host group-key list once:
//
//  1. Resolve the sibling via `resolveSibling(host, Ref.Sibling.Field,
//     Ref.Sibling.Value)`. The resolver returns `(value, present)` —
//     present=false drives the unknown-sibling NaN path.
//  2. Walk the host once and emit one SeriesEntry per host ordinal in
//     host order — the parallel-slice contract (FR-A2) the SERIES
//     dispatch established. Present groups carry Statistic =
//     `value - sibling_val`; absent groups carry a nil Statistic.
//
// Cost: O(groups) per layer for the resolver pass (single scan over
// the host's group-key list) + O(groups) for the emission pass =
// O(groups). Acceptable because the kind is buffered by construction —
// the streaming-Process orchestrator does NOT short-circuit
// this path through a streaming accumulator.
//
// Defense in depth: the descriptor validator rejects ref / scope
// shape mismatches at predict time. The handler still defends against
// a nil host or empty `Sibling.{Field,Value}` (callers bypassing
// predict) by returning a coded PROCESSING_INTERNAL error — that
// branch is unreachable in practice but the defense is cheap and
// matches the INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES safety pattern.
func applyDeltaVsSibling(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	if spec.Ref.Sibling == nil || spec.Ref.Sibling.Field == "" || spec.Ref.Sibling.Value == "" {
		// Defense in depth — the descriptor validator rejects this at
		// predict time. A direct programmatic caller bypassing predict
		// gets a coded error that surfaces the same failure mode.
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Ref.Sibling.{Field,Value} both non-empty",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	groupCount := host.GroupCount()
	siblingField := spec.Ref.Sibling.Field
	siblingValue := spec.Ref.Sibling.Value
	siblingVal, siblingPresent := resolveSibling(host, siblingField, siblingValue)

	// Unknown-sibling path: emit one PULSE_OVERLAY_REF_UNKNOWN warning
	// and surface NaN per the OverlayPayload convention. Mirrors
	// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES' PULSE_OVERLAY_REF_ZERO
	// emission shape (one warning per layer, not per cell). The
	// entries slice still carries one entry per host ordinal so the
	// parallel-slice contract holds.
	var warnings []types.OverlayWarning
	if !siblingPresent {
		warnings = append(warnings, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_UNKNOWN),
			Message: "overlay " + string(spec.Kind) + " sibling reference does not resolve to a known host group",
			Details: map[string]any{
				"kind":        string(spec.Kind),
				"host":        "series",
				"field":       siblingField,
				"value":       siblingValue,
				"group_count": groupCount,
			},
		})
	}

	// Pass 2: per-host-ordinal entry emission. Absent groups carry a
	// nil Summary.Statistic (canonical "present slot, empty summary"
	// shape). Present groups carry Statistic = value -
	// sibling_val; in the unknown-sibling path every present group
	// carries NaN.
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
		var summary types.OverlaySummary
		val, present := host.ValueAt(i)
		if present {
			var delta float64
			if !siblingPresent {
				delta = math.NaN()
			} else {
				delta = val - siblingVal
			}
			deltaCopy := delta
			summary.Statistic = &deltaCopy
			if !math.IsNaN(delta) {
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
		}
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

	// Layer-level Summary: baseline = 0 (renderers centre diverging
	// colour ramps on 0 because deltas are signed deviations — positive
	// = above sibling, negative = below sibling — mirrors
	// DELTA_VS_MARGIN and ZSCORE_VS_MARGIN / ZSCORE_VS_TOTAL).
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

	return layer, warnings, nil
}
