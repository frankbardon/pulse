package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_ZSCORE_VS_ROLLING — per-point windowed standardized z-score
// against the rolling-window mean + SAMPLE standard deviation of the W
// immediately preceding points of an ordered SERIES (grouped Process)
// host.
//
// E4-S6 scope:
//
//   - Fifth windowed-Process overlay in the catalog (siblings: E4-S2
//     INDEX_VS_BASELINE, E4-S3 DELTA_VS_BASELINE, E4-S4 INDEX_VS_PRIOR,
//     E4-S5 INDEX_VS_ROLLING_MEAN). Registered in
//     processing/overlay_series.go's seriesOverlayHandlers dispatch table.
//   - Second consumer of the `Ref.RollingMean` arm of the OverlayRef
//     discriminated union — sibling windowed-rolling kind to E4-S5
//     INDEX_VS_ROLLING_MEAN. The empty marker tags the ref family; the
//     window width lives on `OverlaySpec.Params["window"]` per the
//     `WIN_*` operator convention (`skills/window-operations.md`).
//   - Per-point math: `zscore_i = (point_value_i - rolling_mean(W)) /
//     rolling_sd(W)` where `rolling_sd = sqrt(M2 / (count - 1))` (SAMPLE
//     SD, n-1 denominator).
//
// Variance choice (SAMPLE, NOT population): the rolling z-score
// intentionally uses SAMPLE SD (n-1 denominator) — distinct from
// OVERLAY_ZSCORE_VS_TOTAL which uses POPULATION SD (N denominator). The
// rationale is structural: a rolling window of W observations IS a
// sample of the wider time series, so unbiased sample variance is the
// correct convention. By contrast OVERLAY_ZSCORE_VS_TOTAL standardises
// against the host's per-group aggregation set which IS the whole
// population. The two surfaces are orthogonal: rolling = local sample;
// total = global population. The contrast is documented in both kind's
// type-level prose (`types/overlay.go`) so wire authors pick the right
// surface for their semantic.
//
// Welford state shared semantically with E4-S5's INDEX_VS_ROLLING_MEAN:
// the rolling carrier (`rollingCarrier` in
// processing/overlay_index_vs_rolling_mean.go) stores the Welford
// `(count, mean, M2)` trio. INDEX_VS_ROLLING_MEAN reads only `mean`;
// this kind reads BOTH `mean` and `M2`. The shared carrier keeps the
// per-group accumulator footprint flat as more rolling-family kinds
// land. The carrier is intentionally NOT extracted to a separate file
// because the two kinds live in the same package and the carrier's
// only consumers are the rolling-family handlers; lifting it to a
// shared file would add an indirection without a payoff. Mirror the
// architectural pattern E3 used when sharing `computeSeriesGrandTotal`
// between INDEX_VS_TOTAL and SHARE_OF_TOTAL — package-level helper
// reused in-place.
//
// Chan-Welford merge: documented for parity with CLAUDE.md §
// Execution modes → Parallel buffered Process. The Welford triple
// combines under Chan-Welford with the standard recurrence
// `delta = mean_B - mean_A; mean = mean_A + delta * n_B/n;
// M2 = M2_A + M2_B + delta² * n_A*n_B/n` over the per-group
// accumulators. v1 ships serial-per-group execution so the merge path
// is NOT exercised today, but the carrier shape stays parallel-safe so
// a future story that lifts the rolling fold into the parallel
// buffered Process pipeline can reuse the existing merge plumbing.
//
// Carrier shape (ring buffer over the last W present values plus a
// Welford triple — reused from E4-S5):
//
//   - resolver returns (0, false)  ⇒ absent host point: emit SeriesEntry
//                                     with empty Summary, do NOT advance
//                                     the ring buffer.
//   - carrier count < 2            ⇒ Welford sample SD undefined: emit
//                                     NaN (no warning — "no comparison
//                                     available" is structurally
//                                     distinct from "denominator was
//                                     zero"). Push the value into the
//                                     ring.
//   - rolling_sd == 0              ⇒ emit NaN + PULSE_OVERLAY_REF_ZERO
//                                     warning (constant window: every
//                                     prior point in the window has the
//                                     same value). Then push the value
//                                     into the ring.
//   - else                         ⇒ emit (point - mean) / sd. Push the
//                                     value into the ring.
//
// Absent-point policy (ring does not advance): an absent host ordinal
// (resolver reports `(0, false)`) emits a present SeriesEntry whose
// Summary leaves Statistic unset and DOES NOT advance the ring buffer.
// Mirrors the OVERLAY_INDEX_VS_ROLLING_MEAN / OVERLAY_INDEX_VS_PRIOR
// absent-point carrier rule.
//
// Window param validation (runtime mirror of predict gate):
//
//   - Missing Params["window"]    ⇒ PULSE_OVERLAY_PARAM_MISSING with
//                                     {kind, param} Details.
//   - Non-integer / negative / 0  ⇒ PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
//                                     with {kind, window} Details (the
//                                     LEVEL_OUT_OF_RANGE code is reused
//                                     for "value out of valid range"
//                                     semantics on Params slots — keeps
//                                     the error code surface narrow).
//
// Both gates reuse the existing E4-S5 `extractWindowParam` helper
// (processing/overlay_index_vs_rolling_mean.go) so the predict gate +
// runtime emission shape stays aligned across the rolling family.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go / overlay_index_vs_rolling_mean.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban.

// rollingSampleSD returns the sample standard deviation
// (`sqrt(M2 / (count - 1))`) the OVERLAY_ZSCORE_VS_ROLLING handler
// reads off the Welford-Pébaÿ trio stored in the shared rolling carrier.
// Returns 0 when count < 2 (sample variance is undefined for a single
// observation — caller treats this as the "window not yet filled"
// branch and emits NaN without warning).
//
// Sibling helper to `rollingCarrier.rollingMean` (which is the inline
// `mean` accessor). The two helpers diverge on which field they read
// off the carrier; the carrier itself stays the single source of truth
// for the Welford state.
func (c *rollingCarrier) rollingSampleSD() float64 {
	if c == nil || c.count < 2 {
		return 0
	}
	// Sample variance: divide by (n - 1), NOT n. This is the
	// distinguishing arithmetic from OVERLAY_ZSCORE_VS_TOTAL which uses
	// population SD (sqrt(M2 / N)). See the kind's variance-choice
	// rationale in the doc-comment above.
	return math.Sqrt(c.m2 / float64(c.count-1))
}

// applyZScoreVsRolling is the OVERLAY_ZSCORE_VS_ROLLING runtime handler.
// For every host group ordinal it surfaces
// `(point - rolling_mean(W)) / rolling_sample_sd(W)` on the
// SeriesEntry's Summary.Statistic. Walks the host group-key list once
// with a rolling-window carrier (the shared `rollingCarrier` struct
// from E4-S5).
//
// Window-fill semantics: the carrier needs at least 2 PRESENT prior
// points to compute a sample SD (the n-1 denominator is undefined for a
// single observation). The first present point hits `count == 0` at the
// divide step and emits NaN without warning; the second present point
// hits `count == 1` at the divide step and ALSO emits NaN without
// warning. From the third present point onward (when `count >= 2`) the
// handler emits the standardised z-score. This is the "no comparison
// available" path — structurally distinct from "denominator was zero"
// (mirrors the INDEX_VS_ROLLING_MEAN window-not-yet-filled rule but
// with the additional `count >= 2` requirement specific to sample
// variance).
//
// Zero-rolling-SD path: when the rolling SD is exactly `0` at the
// divide step (e.g. every prior point in the window has the same value
// — constant series), the handler emits ONE PULSE_OVERLAY_REF_ZERO
// warning per layer and surfaces NaN on the affected entry. Subsequent
// points may again hit the zero-SD path as the ring rolls; the layer
// emits ONE warning per layer regardless of how many points hit the
// zero-SD arm (mirrors the share / index / zscore family's
// one-warning-per-layer contract).
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// host (caller passed nil into ApplyOverlaysSeries) and against an
// invalid Params["window"] (the predict gate runs in parallel; the
// runtime defense is cheap and matches the INDEX_VS_ROLLING_MEAN safety
// pattern).
func applyZScoreVsRolling(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	window, err := extractWindowParam(spec)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	groupCount := host.GroupCount()
	carrier := newRollingCarrier(window)

	entries := make([]types.SeriesEntry, 0, groupCount)
	var (
		minV      float64
		maxV      float64
		seen      int
		zeroSDHit bool
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
			var z float64
			switch {
			case carrier.count < 2:
				// Sample variance undefined — need at least 2 prior
				// observations. Emit NaN without warning ("no comparison
				// available" rather than "denominator was zero"). The
				// carrier still consumes the slot.
				z = math.NaN()
			default:
				mean := carrier.rollingMean()
				sd := carrier.rollingSampleSD()
				if sd == 0 {
					z = math.NaN()
					zeroSDHit = true
				} else {
					z = (value - mean) / sd
				}
			}
			zCopy := z
			summary.Statistic = &zCopy
			if !math.IsNaN(z) {
				if seen == 0 {
					minV, maxV = z, z
				} else {
					if z < minV {
						minV = z
					}
					if z > maxV {
						maxV = z
					}
				}
				seen++
			}
			// Advance the ring ONLY for present ordinals (absent host
			// points do not advance the carrier — the next present
			// point will see the same ring contents). Mirrors the
			// INDEX_VS_ROLLING_MEAN / INDEX_VS_PRIOR absent-point lag
			// carrier rule.
			carrier.push(value)
		}
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}

	var warnings []OverlayWarning
	if zeroSDHit {
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " encountered a zero rolling sample SD; affected indices emitted as NaN",
			Details: map[string]any{
				"kind":        string(spec.Kind),
				"host":        "series",
				"group_count": groupCount,
				"window":      window,
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

	// Layer-level Summary: baseline = 0 (mirrors OVERLAY_ZSCORE_VS_TOTAL
	// / OVERLAY_ZSCORE_VS_MARGIN — every z-score family produces a
	// centred distribution, not a ratio; renderers centre diverging
	// colour ramps on 0 because positive = above-mean, negative =
	// below-mean). Distinct from the INDEX_VS_* family's baseline = 100
	// because z-scores are unitless deviations, not scaled ratios.
	// Min / Max / Count populate from present + non-NaN entries.
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
