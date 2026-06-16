package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_INDEX_VS_TOTAL — per-group index score against the grand
// total of a SERIES host (grouped Process result). Per-group math:
// `index_i = (group_val[i] / grand_total) * 100.0` where
// `grand_total = Σ_j group_val[j]` over POST-FILTER host groups
// (AGG_SUM semantics — the resolver gates absent groups via the
// SeriesHostView (value, present) signature, so the accumulator never
// reads pre-filter row counts). Registered in
// processing/overlay_series.go's seriesOverlayHandlers dispatch table;
// the dispatch route is the post-host finalize entry point for the
// streaming-Process orchestrator AND the buffered fallback entry point
// for any callers that materialise the host series before calling into
// the overlay surface.
//
// Zero-grand-total path: when `grand_total == 0` the handler emits ONE
// PULSE_OVERLAY_REF_ZERO warning and populates every entry's
// Summary.Statistic with NaN (per OverlayPayload convention; see
// CLAUDE.md "Execution modes" → Overlays — the canonical
// PULSE_OVERLAY_REF_ZERO contract used by the share / index family).
//
// Absent-group policy: a host that did not produce a value for group i
// (resolver returns (0, false)) surfaces a SeriesEntry whose Summary
// leaves Statistic unset — the canonical "present slot, empty summary"
// shape established by the SERIES dispatch contract. Absent groups do
// NOT contribute to the grand total.
//
// Streaming finalize hook: the streaming-Process orchestrator's inner
// per-record streaming hook mirrors the host's per-group online
// aggregator and runs the grand-total accumulator as one f64 updated
// alongside each per-group fold. THIS file ships the SERIES-host
// POST-FINALIZE entry: given a finalized SeriesHostView, the handler
// computes the overlay. The streaming-vs-buffered byte-identity test
// asserts that running this handler against a streaming vs a buffered
// host produces byte-identical SeriesPayload output. The
// orchestrator-level wiring routes through this same entry point at
// the streaming-pass finalize call site (no second handler — the
// post-finalize entry IS the streaming finalize hook).
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban.

// applyIndexVsTotal is the OVERLAY_INDEX_VS_TOTAL runtime handler. For
// every host group ordinal it surfaces (group_val / grand_total) *
// 100.0 on the SeriesEntry's Summary.Statistic. Walks the host group-
// key list twice:
//
//  1. First pass folds the running grand total over present
//     host values. Absent host values (resolver reports (0, false))
//     are skipped — they do NOT contribute to the grand total and they
//     do NOT carry a Statistic on the emitted entry.
//  2. Second pass emits one SeriesEntry per host ordinal in host
//     order — the parallel-slice contract the SERIES dispatch
//     established. Present groups carry Statistic = index_i; absent
//     groups carry a nil Statistic.
//
// The two-pass shape over the SERIES host is the post-finalize fold
// the streaming-Process orchestrator drives at the end of the
// streaming pass — by the time ApplyOverlaysSeries calls into this
// handler the host series is finalised, so "two passes over the host"
// is structurally O(groups), not O(records). The streaming
// orchestrator's per-record fold runs ONCE over the cohort to build
// the host series + a sibling grand-total accumulator; THIS handler
// then runs once over the finalized host. Combined cost:
// O(records + groups × layers).
//
// Zero-grand-total path: grand_total == 0 (which covers every-group-
// absent, every-group-zero, and arithmetic underflow) yields one
// PULSE_OVERLAY_REF_ZERO warning carrying the overlay kind and the
// host group count; every emitted entry's Summary.Statistic is NaN per
// the OverlayPayload convention. The handler still surfaces one entry
// per host group ordinal — the parallel-slice contract is preserved
// regardless of denominator health.
//
// Defense in depth: the descriptor validator rejects ref / scope
// shape mismatches at predict time. The handler still defends against
// a nil host (caller passed nil into ApplyOverlaysSeries) by returning
// a coded PROCESSING_INTERNAL error — that branch is unreachable in
// practice because ApplyOverlaysSeries short-circuits empty specs
// before dispatch, but the defense is cheap.
func applyIndexVsTotal(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// Pass 1: running grand-total accumulator via the shared SERIES
	// helper. Absent host values (resolver reports (0, false)) skip the
	// addition — this is the AGG_SUM semantics (POST-FILTER only, never
	// reads pre-filter row count). The streaming-Process orchestrator
	// maintains the SAME f64 accumulator inline with the per-group fold
	// so the post-finalize value is byte-identical to the value this
	// pass computes against the materialised host. Sibling kind
	// OVERLAY_SHARE_OF_TOTAL shares the same helper so a request
	// carrying BOTH overlays folds the grand-total ONCE at the
	// streaming layer.
	grandTotal, presentMask, values := computeSeriesGrandTotal(host)

	// Zero-grand-total path: emit one PULSE_OVERLAY_REF_ZERO warning
	// and surface NaN per the OverlayPayload convention. The entries
	// slice still carries one entry per host ordinal so the parallel-
	// slice contract holds.
	var warnings []types.OverlayWarning
	zeroDenominator := grandTotal == 0 || math.IsNaN(grandTotal)
	if zeroDenominator {
		warnings = append(warnings, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " grand total is zero; indices emitted as NaN",
			Details: map[string]any{
				"kind":        string(spec.Kind),
				"host":        "series",
				"group_count": groupCount,
				"grand_total": grandTotal,
			},
		})
	}

	// Pass 2: per-host-ordinal entry emission. Absent groups carry a
	// nil Summary.Statistic (canonical "present slot, empty summary"
	// shape from the SERIES dispatch contract). Present groups carry
	// Statistic = (value / grand_total) * 100.0; in the
	// zero-grand-total path every present group carries NaN.
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
		if presentMask[i] {
			var index float64
			if zeroDenominator {
				index = math.NaN()
			} else {
				index = (values[i] / grandTotal) * 100.0
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

	// Layer-level Summary mirrors the share / index family's MATRIX-host
	// summary convention: baseline=100 (overperform > 100, underperform
	// < 100), Min/Max/Count populated from present + non-NaN entries.
	// The per-entry Statistic carries the renderer-facing detail; the
	// layer-level Summary surfaces the colour-ramp pivot.
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
