package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_INDEX_VS_PRIOR — per-point windowed index against the
// immediately preceding point of an ordered SERIES (grouped Process)
// host.
//
// E4-S4 scope:
//
//   - First streamable windowed-Process handler in the catalog (per
//     kind-catalog-v1 "Streaming-capable subset"). Registered in
//     processing/overlay_series.go's seriesOverlayHandlers dispatch
//     table; the dispatch route is the post-host finalize entry point
//     for the streaming-Process orchestrator AND the buffered fallback
//     entry point for any callers that materialise the host series
//     before calling into the overlay surface.
//   - First kind to consume the `Ref.Prior` arm of the OverlayRef
//     discriminated union. v1 ships lag-1 only — the OverlayPriorRef.Lag
//     slot is reserved for future window-N priors and rejected at
//     predict time when non-zero (descriptor.validateOverlayIndexVsPrior).
//   - Per-point math: `index_i = point_value / prior_value * 100` where
//     `prior_value` is the most recently seen PRESENT host value walking
//     the ordered group-key list. The first present point has no prior
//     and emits NaN (no comparison available — this is NOT
//     PULSE_OVERLAY_REF_ZERO, since the denominator slot is unfilled
//     rather than zero). Subsequent present points with a present prior
//     of zero emit NaN + a PULSE_OVERLAY_REF_ZERO warning.
//
// Carrier shape (single-state lag, streamable): the handler maintains
// ONE f64 lag carrier across the ordered host series. The carrier holds
// the most recently observed PRESENT value. On each ordinal:
//
//   - resolver returns (0, false)  ⇒ absent host point: emit NaN entry,
//                                     do NOT advance the carrier.
//   - i == 0 (first ordinal)       ⇒ first present point: emit NaN
//                                     entry, set carrier to point value.
//   - carrier not yet armed        ⇒ same as first present point: emit
//                                     NaN entry, set carrier to value.
//   - carrier == 0                 ⇒ divide-by-zero: emit NaN +
//                                     PULSE_OVERLAY_REF_ZERO warning,
//                                     advance carrier to current value.
//   - carrier != 0                 ⇒ index = value / carrier * 100,
//                                     advance carrier to current value.
//
// This single-state lag carrier is what makes the kind streamable: the
// streaming-Process orchestrator wiring will carry one f64 alongside
// the per-group accumulators inside the streaming fold; the per-group
// finalize value is consumed here in the SAME order the host orchestrator
// emitted the group keys. The streaming-vs-buffered byte-identity test
// pins that the handler's output is purely a function of the ordered
// (keys, values) pair, independent of the host's internal resolver
// shape.
//
// Absent-point policy (lag carrier does not advance): an absent host
// ordinal (resolver reports `(0, false)`) emits a present SeriesEntry
// whose Summary leaves Statistic unset and DOES NOT advance the carrier.
// This matches the E3-S2 / E3-S3 / E3-S4 absent-group contract and the
// SeriesHostView resolver documentation. The next present ordinal will
// compare against the last PRESENT value, not the absent slot.
//
// Layer-level Summary: baseline = 100 (renderers centre diverging
// colour ramps on 100 because the index is a ratio scaled to 100).
// Min / Max / Count populate from present + non-NaN entries.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime overlay
//     execution rides inside processing/ alongside the aggregator /
//     attribute / grouper layers (mirrors overlay.go / overlay_series.go
//     / overlay_index_vs_total.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays grep-
//     clean against the structural defense ban.

// applyIndexVsPrior is the OVERLAY_INDEX_VS_PRIOR runtime handler. For
// every host group ordinal it surfaces `point / prior * 100` on the
// SeriesEntry's Summary.Statistic. Walks the host group-key list ONCE
// with a single-state lag carrier — no second pass over records is
// required and no auxiliary slice is built. The kind is streamable
// because the carrier is one f64 carried alongside the per-group
// accumulators in the streaming-Process fold; this handler is the post-
// host-finalize divide step.
//
// First-present-point semantics: the first present entry emits NaN
// because no prior exists yet. The handler does NOT raise
// PULSE_OVERLAY_REF_ZERO on this branch — "no comparison available" is
// distinct from "denominator was zero". Renderers typically surface the
// first entry as a blank cell rather than a degenerate signal.
//
// Zero-prior path: when the lag carrier is `0` at the divide step (the
// most recent present point had a value of zero), the handler emits one
// PULSE_OVERLAY_REF_ZERO warning carrying the overlay kind and the host
// group count; the affected entry's Summary.Statistic is NaN. The
// handler keeps walking — subsequent present points still update the
// carrier (so if the next present point also follows a zero, it will
// again hit the zero-prior branch). Mirrors the existing
// PULSE_OVERLAY_REF_ZERO contract used by the share / index / zscore
// family (one warning per layer, not per cell).
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// host (caller passed nil into ApplyOverlaysSeries) by returning a
// coded PROCESSING_INTERNAL error — that branch is unreachable in
// practice because ApplyOverlaysSeries short-circuits empty specs
// before dispatch, but the defense is cheap and matches the
// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES safety pattern.
func applyIndexVsPrior(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	groupCount := host.GroupCount()

	// Single-state lag carrier: one f64 + a boolean "armed" flag. The
	// flag distinguishes "no prior yet" (carrier value undefined) from
	// "the prior was exactly zero" (carrier value = 0). The streaming-
	// Process orchestrator wiring will keep the SAME shape alongside the
	// per-group accumulators in the streaming fold.
	var (
		priorValue   float64
		priorArmed   bool
		zeroPriorHit bool
	)

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
			var index float64
			switch {
			case !priorArmed:
				// First present ordinal: no prior available. Emit NaN
				// without a warning — "no comparison" is structurally
				// distinct from "zero denominator".
				index = math.NaN()
			case priorValue == 0:
				// Zero prior: divide-by-zero. Emit NaN and flag the
				// PULSE_OVERLAY_REF_ZERO branch (one warning per layer,
				// not per cell).
				index = math.NaN()
				zeroPriorHit = true
			default:
				index = value / priorValue * 100.0
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
			// Advance the lag carrier ONLY for present ordinals. Absent
			// host points leave the carrier untouched — the next present
			// point will compare against the most recent PRESENT value,
			// not an absent slot. This is the single-state lag semantics
			// the kind's documentation calls out.
			priorValue = value
			priorArmed = true
		}
		// Absent host point: emit a present SeriesEntry with an unset
		// Summary.Statistic (the canonical "present slot, empty summary"
		// shape from E3-S1) and do NOT advance the carrier.
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}

	var warnings []OverlayWarning
	if zeroPriorHit {
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " encountered a zero prior value; affected indices emitted as NaN",
			Details: map[string]any{
				"kind":        string(spec.Kind),
				"host":        "series",
				"group_count": groupCount,
			},
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

	// Layer-level Summary: baseline = 100 (mirrors INDEX_VS_MARGIN /
	// INDEX_VS_TOTAL / INDEX_VS_SIBLING — the index family centres
	// diverging colour ramps on 100). Min / Max / Count populate from
	// present + non-NaN entries.
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
