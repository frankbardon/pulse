package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_SHARE_OF_TOTAL (SERIES-host dispatch) — per-group share of the
// host series' grand total: `group_val / grand_total` per group, scale
// 1.0 (no ×100 — emits the raw share so cells over a complete partition
// sum to 1.0 within ULP).
//
// E3-S3 scope:
//
//   - Sibling SERIES-host handler to OVERLAY_INDEX_VS_TOTAL (E3-S2).
//     Same grand-total accumulator (`computeSeriesGrandTotal` in
//     processing/overlay_series.go) — the streaming-Process orchestrator
//     (E3-S6) keeps ONE grand-total f64 alongside the per-group
//     accumulators regardless of how many SHARE / INDEX overlays a
//     Request carries.
//   - Per-group math: `share_i = group_val[i] / grand_total` where
//     `grand_total = Σ_j group_val[j]` over POST-FILTER host groups
//     (AGG_SUM semantics — the resolver gates absent groups so the
//     accumulator never reads pre-filter row counts). Distinct dispatch
//     from INDEX_VS_TOTAL even though the math overlaps; the kind-
//     catalog-v1 interview's resolved-decision rule on the catalog kept
//     SHARE and INDEX as separate kind names ("readable kind names beat
//     scale-param overloading at JSON-authoring time"), so the runtime
//     dispatch stays distinct too — callers cannot confuse `share` with
//     `index / 100`.
//   - Zero-grand-total path: when `grand_total == 0` (or NaN) the handler
//     emits ONE PULSE_OVERLAY_REF_ZERO warning and populates every
//     present entry's Summary.Statistic with NaN per the OverlayPayload
//     convention. Mirrors INDEX_VS_TOTAL.
//   - Absent-group policy: a host that did not produce a value for group
//     i surfaces a SeriesEntry whose Summary leaves Statistic unset (the
//     canonical "present slot, empty summary" shape established by the
//     E3-S1 SERIES dispatch contract). Absent groups do NOT contribute to
//     the grand total.
//
// Streaming finalize hook: same shape as INDEX_VS_TOTAL — the streaming-
// Process orchestrator wiring lands in E3-S6, and the inner per-record
// hook stays one grand-total accumulator inside the streaming fold. This
// file ships the SERIES-host POST-FINALIZE entry; given a finalized
// SeriesHostView, the handler divides each group value by the running
// grand total. The streaming-vs-buffered byte-identity test asserts that
// running this handler against a streaming vs a buffered host produces
// byte-identical SeriesPayload output.
//
// Layer-level Summary uses `baseline = 1.0` (renderers center diverging
// colour ramps on 1.0 because shares sum to 1.0 across a complete
// partition) — distinct from INDEX_VS_TOTAL's `baseline = 100.0`.
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

// applyShareOfTotalSeries is the OVERLAY_SHARE_OF_TOTAL SERIES-host
// runtime handler. For every host group ordinal it surfaces
// `group_val / grand_total` on the SeriesEntry's Summary.Statistic. The
// two-pass shape mirrors INDEX_VS_TOTAL — pass 1 folds the grand total
// via the shared `computeSeriesGrandTotal` helper, pass 2 emits one
// SeriesEntry per host ordinal in host order (the parallel-slice
// contract from E3-S1).
//
// Zero-grand-total path: `grand_total == 0` (which covers every-group-
// absent, every-group-zero, and arithmetic underflow) yields one
// PULSE_OVERLAY_REF_ZERO warning carrying the overlay kind and the host
// group count; every emitted entry's Summary.Statistic is NaN per the
// OverlayPayload convention. The handler still surfaces one entry per
// host group ordinal — the parallel-slice contract is preserved
// regardless of denominator health.
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// host (caller passed nil into ApplyOverlaysSeries) by returning a coded
// PROCESSING_INTERNAL error — that branch is unreachable in practice
// because ApplyOverlaysSeries short-circuits empty specs before
// dispatch, but the defense is cheap and matches the INDEX_VS_TOTAL
// safety pattern.
func applyShareOfTotalSeries(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error) {
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
	// helper. Sibling kind OVERLAY_INDEX_VS_TOTAL (E3-S2) walks the same
	// helper so a Request carrying BOTH overlays folds the grand-total
	// ONCE at the streaming layer (the E3-S6 orchestrator wiring keeps
	// ONE accumulator alongside the per-group accumulators regardless of
	// overlay count).
	grandTotal, presentMask, values := computeSeriesGrandTotal(host)

	// Zero-grand-total path: emit one PULSE_OVERLAY_REF_ZERO warning and
	// surface NaN per the OverlayPayload convention. The entries slice
	// still carries one entry per host ordinal so the parallel-slice
	// contract holds.
	var warnings []OverlayWarning
	zeroDenominator := grandTotal == 0 || math.IsNaN(grandTotal)
	if zeroDenominator {
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " grand total is zero; shares emitted as NaN",
			Details: map[string]any{
				"kind":        string(spec.Kind),
				"host":        "series",
				"group_count": groupCount,
				"grand_total": grandTotal,
			},
		})
	}

	// Pass 2: per-host-ordinal entry emission. Absent groups carry a nil
	// Summary.Statistic (canonical "present slot, empty summary" shape
	// from E3-S1). Present groups carry Statistic = value / grand_total;
	// in the zero-grand-total path every present group carries NaN.
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
			var share float64
			if zeroDenominator {
				share = math.NaN()
			} else {
				share = values[i] / grandTotal
			}
			shareCopy := share
			summary.Statistic = &shareCopy
			if !math.IsNaN(share) {
				if seen == 0 {
					minV, maxV = share, share
				} else {
					if share < minV {
						minV = share
					}
					if share > maxV {
						maxV = share
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

	// Layer-level Summary mirrors the share family convention:
	// baseline = 1.0 (renderers centre diverging colour ramps on 1.0
	// because shares over a complete partition sum to 1.0), Min / Max /
	// Count populated from present + non-NaN entries. Distinct from
	// INDEX_VS_TOTAL's baseline = 100 — keeping the SHARE / INDEX
	// renderer surfaces aligned with the catalog's "readable kind names"
	// rule.
	baseline := 1.0
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
