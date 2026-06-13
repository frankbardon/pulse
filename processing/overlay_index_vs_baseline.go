package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_INDEX_VS_BASELINE — per-point ratio index against a single
// fixed positional baseline of an ordered SERIES (grouped Process) host.
//
// E4-S2 scope:
//
//   - Second windowed-Process overlay in the catalog (E4-S2; the first
//     windowed kind was OVERLAY_INDEX_VS_PRIOR / E4-S4). First kind to
//     consume the `Ref.BaselineIndex.Position` arm of the
//     OverlayBaselineIndexRef discriminated union landed at E4-S1.
//   - Per-point math: `index_i = point_value_i / baseline_value * 100`
//     where `baseline_value = host.ValueAt(Ref.BaselineIndex.Position)`.
//     The baseline is resolved ONCE at handler entry via
//     ResolveBaselineIndex (the E4-S1 foundation helper).
//   - Baseline-at-self: a present point at the baseline ordinal yields
//     `100.0` (self-vs-self under the ratio scaling). The first present
//     point with non-zero baseline emits `100` when `Position` is the
//     first ordinal — typical "anchor against first point" authoring.
//   - Zero-baseline path: when the resolved baseline value is `0` the
//     handler emits ONE PULSE_OVERLAY_REF_ZERO warning carrying the
//     overlay kind, the host group count, and the baseline ordinal; every
//     emitted entry's Summary.Statistic is NaN per the OverlayPayload
//     convention. Mirrors the share / index / zscore family's
//     zero-denominator contract (one warning per layer, not per cell).
//     Note: an absent host group at the baseline ordinal surfaces (0,
//     false) from `host.ValueAt`, which ResolveBaselineIndex returns as
//     baseline_value=0; the zero-baseline arm fires and the layer emits
//     NaN throughout. This matches the documented "absent baseline =
//     handler-owned PULSE_OVERLAY_REF_ZERO surface" decision from S1.
//   - Absent-point policy: a host that did not produce a value for group
//     i (the resolver returns `(0, false)`) surfaces a SeriesEntry whose
//     Summary leaves Statistic unset — the canonical "present slot,
//     empty summary" shape from the E3-S1 SERIES dispatch contract.
//     Absent groups do NOT participate in the index computation.
//
// Buffered (per kind-catalog-v1 "Streaming-capable subset"): resolving a
// single positional baseline requires the materialised host series; the
// handler runs at the buffered post-host-finalize exit via
// ApplyOverlaysSeries. Forward-compat: a future story may lift the
// baseline resolver into a streaming-aware shape (carrying the resolved
// baseline inline as a kind-specific accumulator advanced during the
// streaming pass once the orchestrator hits the baseline ordinal); when
// that lands the streamability row in types/overlay_streamability.go
// flips to true.
//
// Layer-level Summary: baseline = 100 (renderers centre diverging colour
// ramps on 100 because the index is a ratio scaled to 100). Min / Max /
// Count populate from present + non-NaN entries.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime overlay
//     execution rides inside processing/ alongside the aggregator /
//     attribute / grouper layers (mirrors overlay.go / overlay_series.go
//     / overlay_index_vs_total.go / overlay_index_vs_prior.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban.

// applyIndexVsBaseline is the OVERLAY_INDEX_VS_BASELINE runtime handler.
// For every host group ordinal it surfaces `point / baseline * 100` on
// the SeriesEntry's Summary.Statistic. The baseline is resolved ONCE at
// handler entry via ResolveBaselineIndex.
//
// Resolver propagation: a coded error from ResolveBaselineIndex (negative
// Position, out-of-range Position, nil host) short-circuits the handler
// and propagates the CodedError directly. The resolver's CodedError
// carries `code = PULSE_OVERLAY_REF_UNKNOWN` plus `{baseline_index,
// series_length}` Details — the runtime range-check surface for this
// kind.
//
// Zero-baseline path: when the resolved baseline is 0 the handler emits
// one PULSE_OVERLAY_REF_ZERO warning and surfaces NaN per the
// OverlayPayload convention. The handler still emits one SeriesEntry per
// host ordinal so the parallel-slice contract (FR-A2) holds regardless
// of denominator health.
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler defends against a nil host
// (caller passed nil into ApplyOverlaysSeries) by returning the resolver's
// coded PROCESSING_INTERNAL error — that branch is unreachable in
// practice because ApplyOverlaysSeries short-circuits empty specs before
// dispatch, but the defense matches the INDEX_VS_TOTAL / SHARE_OF_TOTAL
// SERIES / INDEX_VS_PRIOR safety pattern.
func applyIndexVsBaseline(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error) {
	baselineValue, err := ResolveBaselineIndex(host, spec.Ref.BaselineIndex)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	groupCount := host.GroupCount()
	var baselinePosition int
	if spec.Ref.BaselineIndex != nil {
		baselinePosition = spec.Ref.BaselineIndex.Position
	}

	// Zero-baseline path: the resolved baseline is zero. Emit ONE
	// PULSE_OVERLAY_REF_ZERO warning carrying the overlay kind, the host
	// group count, and the baseline ordinal; every present entry's
	// Statistic is NaN. The entries slice still carries one entry per host
	// ordinal so the parallel-slice contract holds.
	zeroDenominator := baselineValue == 0 || math.IsNaN(baselineValue)

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
			if zeroDenominator {
				index = math.NaN()
			} else {
				index = value / baselineValue * 100.0
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
		// Absent host point: emit a present SeriesEntry with an unset
		// Summary.Statistic (the canonical "present slot, empty summary"
		// shape from E3-S1).
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}

	var warnings []OverlayWarning
	if zeroDenominator {
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " resolved baseline value is zero; indices emitted as NaN",
			Details: map[string]any{
				"kind":           string(spec.Kind),
				"host":           "series",
				"group_count":    groupCount,
				"baseline_index": baselinePosition,
				"baseline_value": baselineValue,
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
	// INDEX_VS_TOTAL / INDEX_VS_SIBLING / INDEX_VS_PRIOR — the index
	// family centres diverging colour ramps on 100). Min / Max / Count
	// populate from present + non-NaN entries.
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
