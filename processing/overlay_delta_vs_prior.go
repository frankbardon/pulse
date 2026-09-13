package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_DELTA_VS_PRIOR — per-point windowed additive delta against the
// immediately preceding point of an ordered SERIES (grouped Process)
// host.
//
// Absolute-difference twin of OVERLAY_INDEX_VS_PRIOR, standing in the
// same relation to it that DELTA_VS_BASELINE stands to
// INDEX_VS_BASELINE: identical host shape, identical ref family,
// identical carrier semantics, subtraction where the index divides.
//
// Behaviour:
//
//   - Streamable windowed-Process handler. Registered in
//     processing/overlay_series.go's seriesOverlayHandlers dispatch
//     table; the dispatch route is the post-host finalize entry point
//     for the streaming-Process orchestrator AND the buffered fallback
//     entry point for any callers that materialise the host series
//     before calling into the overlay surface.
//   - Consumes the `Ref.Prior` arm of the OverlayRef discriminated
//     union, exactly as INDEX_VS_PRIOR does. v1 ships lag-1 only — the
//     OverlayPriorRef.Lag slot is reserved for future window-N priors
//     and rejected at predict time when non-zero
//     (descriptor.validateOverlayDeltaVsPrior).
//   - Per-point math: `delta_i = point_value - prior_value` where
//     `prior_value` is the most recently seen PRESENT host value walking
//     the ordered group-key list. The first present point has no prior
//     and emits NaN (no comparison available).
//
// ⚠ NO ZERO-REF BRANCH, AND THAT IS THE ONE REAL DIVERGENCE FROM
// INDEX_VS_PRIOR. A prior of exactly zero is a perfectly good
// subtrahend — `value - 0` is `value` — so this kind never emits
// PULSE_OVERLAY_REF_ZERO. The index twin raises it because a zero
// denominator is undefined; subtraction has no such degenerate input.
// A reader comparing the two handlers should find that branch missing
// here on purpose rather than assume it was forgotten.
//
// Carrier shape (single-state lag, streamable): the handler maintains
// ONE f64 lag carrier across the ordered host series. The carrier holds
// the most recently observed PRESENT value. On each ordinal:
//
//   - resolver returns (0, false)  ⇒ absent host point: emit NaN entry,
//                                     do NOT advance the carrier.
//   - carrier not yet armed        ⇒ first present point: emit NaN
//                                     entry, set carrier to point value.
//   - carrier armed                ⇒ delta = value - carrier, advance
//                                     carrier to current value.
//
// This single-state lag carrier is what makes the kind streamable: the
// streaming-Process orchestrator wiring carries one f64 alongside the
// per-group accumulators inside the streaming fold; the per-group
// finalize value is consumed here in the SAME order the host
// orchestrator emitted the group keys. The streaming-vs-buffered
// byte-identity test pins that the handler's output is purely a
// function of the ordered (keys, values) pair, independent of the
// host's internal resolver shape.
//
// Absent-point policy (lag carrier does not advance): an absent host
// ordinal (resolver reports `(0, false)`) emits a present SeriesEntry
// whose Summary leaves Statistic unset and DOES NOT advance the
// carrier. This matches the SERIES-host absent-group contract and the
// SeriesHostView resolver documentation. The next present ordinal will
// compare against the last PRESENT value, not the absent slot.
//
// Layer-level Summary: baseline = 0 (the additive-delta family centres
// diverging colour ramps on zero, mirroring DELTA_VS_BASELINE /
// DELTA_VS_MARGIN / DELTA_VS_SIBLING — NOT the index family's 100).
// Min / Max / Count populate from present + non-NaN entries.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime overlay
//     execution rides inside processing/ alongside the aggregator /
//     attribute / grouper layers (mirrors overlay.go / overlay_series.go
//     / overlay_index_vs_prior.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays grep-
//     clean against the structural defense ban.

// applyDeltaVsPrior is the OVERLAY_DELTA_VS_PRIOR runtime handler. For
// every host group ordinal it surfaces `point - prior` on the
// SeriesEntry's Summary.Statistic. Walks the host group-key list ONCE
// with a single-state lag carrier — no second pass over records is
// required and no auxiliary slice is built.
//
// First-present-point semantics: the first present entry emits NaN
// because no prior exists yet. Renderers typically surface the first
// entry as a blank cell rather than a degenerate signal — a delta of
// zero would be a CLAIM (that nothing changed) about a comparison that
// was never made.
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// host (caller passed nil into ApplyOverlaysSeries) by returning a
// coded PROCESSING_INTERNAL error — that branch is unreachable in
// practice because ApplyOverlaysSeries short-circuits empty specs
// before dispatch, but the defense is cheap and matches the
// INDEX_VS_PRIOR / INDEX_VS_TOTAL SERIES safety pattern.
func applyDeltaVsPrior(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// Single-state lag carrier: one f64 + a boolean "armed" flag. Unlike
	// the index twin the flag is not load-bearing for correctness of the
	// math (a zero prior subtracts fine); it distinguishes "no prior
	// yet" from "the prior was exactly zero" so the FIRST point emits
	// NaN rather than the value itself.
	var (
		priorValue float64
		priorArmed bool
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
			var delta float64
			if !priorArmed {
				// First present ordinal: no prior available. Emit NaN —
				// "no comparison" is not "no change".
				delta = math.NaN()
			} else {
				delta = value - priorValue
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
		// shape) and do NOT advance the carrier.
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

	// Layer-level Summary: baseline = 0 (the additive-delta family
	// centres diverging colour ramps on zero). Min / Max / Count
	// populate from present + non-NaN entries.
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

	// No warnings are reachable from this handler: the only warning the
	// index twin can raise is PULSE_OVERLAY_REF_ZERO, which subtraction
	// cannot hit. Returning a nil slice rather than an empty one matches
	// the "no warnings" shape every other handler returns.
	return layer, nil, nil
}
