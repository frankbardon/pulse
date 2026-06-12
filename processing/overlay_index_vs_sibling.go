package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_INDEX_VS_SIBLING — per-group ratio index against a sibling
// group named in `Ref.Sibling`, scaled to ×100.
//
// E3-S5 scope:
//
//   - Second sibling-reference SERIES-host handler in the catalog
//     (alphabetised between INDEX_VS_MARGIN and INDEX_VS_TOTAL).
//     Registered in processing/overlay_series.go's
//     seriesOverlayHandlers dispatch table; the dispatch route is the
//     post-host-finalize entry point. Buffered (per kind-catalog-v1
//     "Streaming-capable subset"): the streaming Process pass cannot
//     resolve a `(Field, Value)` sibling lookup against the per-group
//     accumulators until they are finalised, so the handler runs at
//     the buffered post-host-finalize exit.
//   - Per-group math: `index_i = (group_val[i] / sibling_val) * 100.0`
//     where the sibling is a single fixed host group identified by
//     `Ref.Sibling.{Field, Value}`. The sibling group itself emits
//     `100.0` (self-vs-self under the ratio scaling — `sibling /
//     sibling * 100 = 100`). Sibling resolves via `resolveSibling`
//     (processing/overlay_sibling_resolver.go) — a single
//     `(field, value)` lookup against the host's group-key list.
//
// Unknown-sibling path: when the resolver returns `(_, false)` — the
// named field is not a grouper field on the host OR the named value
// does not match any observed axis-key value — the handler emits ONE
// `PULSE_OVERLAY_REF_UNKNOWN` warning carrying the offending
// `(field, value)` pair and surfaces NaN statistics across every
// present entry. Mirrors DELTA_VS_SIBLING's unknown-sibling emission
// shape (one warning per layer, not per cell).
//
// Zero-sibling path: when the sibling resolves but its value is zero
// (legitimate group with a zero post-filter sum) the handler emits ONE
// `PULSE_OVERLAY_REF_ZERO` warning and surfaces NaN across every
// present entry — division by zero is mathematically undefined and
// the same PULSE_OVERLAY_REF_ZERO contract the SERIES INDEX_VS_TOTAL /
// SHARE_OF_TOTAL kinds use applies here. DELTA_VS_SIBLING does NOT
// emit this warning (subtraction by zero is well-defined and just
// recovers the host's raw value).
//
// Absent-group policy: a host that did not produce a value for group
// i (resolver returns `(0, false)`) surfaces a SeriesEntry whose
// Summary leaves Statistic unset — the canonical "present slot, empty
// summary" shape from the E3-S1 SERIES dispatch contract. Absent
// groups do NOT participate in the index computation. The sibling
// group's own entry surfaces a statistic of `100.0` when present
// (self-vs-self under the ratio scaling).
//
// Output units: the layer's Statistic is unitless — a ratio scaled to
// 100. Renderers centre diverging colour ramps on `baseline = 100`
// (mirrors INDEX_VS_MARGIN and INDEX_VS_TOTAL).
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go / overlay_delta_vs_sibling.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays grep-
//     clean against the structural defense ban.

// applyIndexVsSibling is the OVERLAY_INDEX_VS_SIBLING runtime handler.
// For every host group ordinal it surfaces
// `(group_val[i] / sibling_val) * 100.0` on the SeriesEntry's
// Summary.Statistic. Walks the host group-key list twice:
//
//  1. Resolve the sibling via `resolveSibling(host, Ref.Sibling.Field,
//     Ref.Sibling.Value)`. The resolver returns `(value, present)` —
//     present=false drives the unknown-sibling NaN path;
//     present=true with value==0 drives the zero-sibling NaN path
//     (analogous to INDEX_VS_TOTAL's zero-grand-total branch).
//  2. Walk the host once and emit one SeriesEntry per host ordinal in
//     host order — the parallel-slice contract (FR-A2) the E3-S1
//     dispatch established. Present groups carry Statistic =
//     `(value / sibling_val) * 100.0`; absent groups carry a nil
//     Statistic.
//
// Cost: O(groups) per layer for the resolver pass (single scan over
// the host's group-key list) + O(groups) for the emission pass =
// O(groups). Acceptable because the kind is buffered by construction —
// the streaming-Process orchestrator (E3-S6) does NOT short-circuit
// this path through a streaming accumulator.
//
// Defense in depth: the descriptor validator rejects ref / scope
// shape mismatches at predict time. The handler still defends against
// a nil host or empty `Sibling.{Field,Value}` (callers bypassing
// predict) by returning a coded PROCESSING_INTERNAL error — that
// branch is unreachable in practice but the defense is cheap and
// matches the DELTA_VS_SIBLING safety pattern.
func applyIndexVsSibling(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error) {
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

	// Distinguish two failure modes that share the NaN-statistics shape:
	//
	//   - unknown sibling: resolver returns present=false. Emits
	//     PULSE_OVERLAY_REF_UNKNOWN.
	//   - zero sibling: resolver returns present=true with value==0.
	//     Emits PULSE_OVERLAY_REF_ZERO (division by zero is undefined,
	//     mirrors INDEX_VS_TOTAL).
	//
	// Both paths surface NaN per present entry — the entries slice still
	// carries one entry per host ordinal so the parallel-slice contract
	// holds regardless of denominator health.
	var warnings []OverlayWarning
	zeroSibling := siblingPresent && (siblingVal == 0 || math.IsNaN(siblingVal))
	unknownSibling := !siblingPresent
	switch {
	case unknownSibling:
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_UNKNOWN),
			Message: "overlay " + string(spec.Kind) + " sibling reference does not resolve to a known host group",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"host":         "series",
				"field":        siblingField,
				"value":        siblingValue,
				"group_count":  groupCount,
			},
		})
	case zeroSibling:
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " sibling value is zero; indices emitted as NaN",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"host":         "series",
				"field":        siblingField,
				"value":        siblingValue,
				"sibling_val":  siblingVal,
				"group_count":  groupCount,
			},
		})
	}

	// Pass 2: per-host-ordinal entry emission. Absent groups carry a
	// nil Summary.Statistic (canonical "present slot, empty summary"
	// shape from E3-S1). Present groups carry Statistic = (value /
	// sibling_val) * 100; in the unknown-sibling or zero-sibling paths
	// every present group carries NaN.
	emitNaN := unknownSibling || zeroSibling
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
			var index float64
			if emitNaN {
				index = math.NaN()
			} else {
				index = (val / siblingVal) * 100.0
			}
			indexCopy := index
			summary.Statistic = &indexCopy
			if !math.IsNaN(index) {
				if seen == 0 {
					minV, maxV = index, index
				} else {
					if index < minV {
						minV = index
					}
					if index > maxV {
						maxV = index
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

	// Layer-level Summary: baseline = 100 (renderers centre diverging
	// colour ramps on 100 because indices >100 overperform the sibling
	// and indices <100 underperform — mirrors INDEX_VS_MARGIN and
	// INDEX_VS_TOTAL).
	baseline := 100.0
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
