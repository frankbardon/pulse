package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// applyZVsRef is the COMPOSE-host SERIES-shape runtime handler for
// OVERLAY_Z_VS_REF (E1-S18). Per-group two-sample z-test on the means
// against the reference slot's matching group. Series sibling of
// `OVERLAY_Z_CELL` — same Welch-style SE recurrence, same
// `standardNormalCDF` finaliser, same default-variance /
// default-sample-size policy. Mirrors the SERIES arm of
// `applyTVsRef` (overlay_compose_handlers_series.go) — only the
// finaliser swaps from `studentTTwoSidedP` to `standardNormalCDF`.
//
// Components-source (E3-S7): when a target row OR a reference row
// carries a `map[string]any{"mean", "variance", "n", ...}` value
// column — the components-source emission from AGG_WELFORD via the
// MetaAggregator path — the handler reads `(mean, variance, n)` off
// the map and bypasses the per-side `Params` defaults for that
// group. Scalar rows fall back to the Params defaults
// (`variance_target` / `variance_ref` default 1.0,
// `sample_size_target` / `sample_size_ref` default 2). Mixed rows
// (one triple, one scalar) pull `(variance, n)` from each side
// independently per the additive contract. Triple-aware row encoding
// / lookup routes through `encodeSeriesRowAnyMap` +
// `buildSeriesRowLookupAnyMap` so the SERIES arm and the MATRIX arm
// of the Z parity pair stay symmetric with the T parity pair
// (E3-S7). The legacy processing.WelfordTriple type-assertion on the
// row's value column is no longer performed.
//
// Math (per host group `i`):
//
//	se      = sqrt(var_target/n_target + var_ref/n_ref)
//	z       = (target_mean - ref_mean) / se
//	p_value = 2 * (1 - Φ(|z|))
//
// Missing reference rows emit `PULSE_OVERLAY_REF_ZERO` with a
// `ref_missing=true` Detail flag; the affected entry's Statistic is
// NaN (the entry stays in the output series so renderers align with
// the target series row-for-row — mirrors `applyTVsRef`). Degenerate
// inputs (`n < 2`, both variances 0, `se == 0`) emit the same
// warning + NaN statistic.
//
// Reuses `welchZTest` (the shared MATRIX-arm helper in
// overlay_z_cell.go) so the SERIES and MATRIX surfaces produce
// identical p-values for the same (mean, variance, n) triple.
func applyZVsRef(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []types.OverlayWarning, error) {
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	if reference == nil || target == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires non-nil reference and target slots",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	varTargetDefault := varianceFromParams(spec.Params, "variance_target", 1.0)
	varRefDefault := varianceFromParams(spec.Params, "variance_ref", 1.0)
	nTargetDefault := sampleSizeFromParams(spec.Params, "sample_size_target", 2.0)
	nRefDefault := sampleSizeFromParams(spec.Params, "sample_size_ref", 2.0)

	refIndex := buildSeriesRowLookupAnyMap(reference.Data)

	entries := make([]types.SeriesEntry, 0, len(target.Data))
	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []types.OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i, row := range target.Data {
		keyStr, _, scalar, mean, variance, n, hasScalar, hasTriple := encodeSeriesRowAnyMap(row)
		if !hasScalar && !hasTriple {
			// No numeric / triple column on the target row — skip rather
			// than fabricate a NaN entry. The chassis schema-match gate
			// rejects mismatched series schemas before dispatch reaches
			// this handler.
			continue
		}
		var (
			targetMean float64
			targetVar  float64
			targetN    float64
		)
		if hasTriple {
			targetMean = mean
			targetVar = variance
			targetN = n
		} else {
			targetMean = scalar
			targetVar = varTargetDefault
			targetN = nTargetDefault
		}

		entry := types.SeriesEntry{Key: types.AxisKey{keyStr}}
		refEntry, refPresent := refIndex[keyStr]
		if !refPresent {
			warnings = append(warnings, types.OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) + " reference row missing for target row key",
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"reference":    spec.Reference,
					"target_label": targetLabel,
					"ref_index":    refIdx,
					"target_index": targetIdx,
					"row_index":    i,
					"row_key":      keyStr,
					"ref_missing":  true,
				},
			})
			nan := math.NaN()
			entry.Summary.Statistic = &nan
			entries = append(entries, entry)
			continue
		}
		var (
			refMean float64
			refVar  float64
			refN    float64
		)
		if refEntry.HasTriple {
			refMean = refEntry.Mean
			refVar = refEntry.Variance
			refN = refEntry.N
		} else {
			refMean = refEntry.Scalar
			refVar = varRefDefault
			refN = nRefDefault
		}

		p, ok := welchZTest(targetMean, targetVar, targetN, refMean, refVar, refN)
		if !ok {
			warnings = append(warnings, types.OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) + " z-test undefined for group (zero SE OR insufficient n)",
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"reference":    spec.Reference,
					"target_label": targetLabel,
					"ref_index":    refIdx,
					"target_index": targetIdx,
					"row_index":    i,
					"row_key":      keyStr,
					"target_value": targetMean,
					"ref_value":    refMean,
				},
			})
			nan := math.NaN()
			entry.Summary.Statistic = &nan
			entries = append(entries, entry)
			continue
		}
		pCopy := p
		entry.Summary.Statistic = &pCopy
		entries = append(entries, entry)
		if seen == 0 {
			minV, maxV = p, p
		} else {
			if p < minV {
				minV = p
			}
			if p > maxV {
				maxV = p
			}
		}
		seen++
	}

	layer := types.OverlayLayer{
		Name:  composeOverlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: entries,
			},
		},
	}
	// Inferential overlays do not surface a Baseline (mirrors T_VS_REF).
	summary := &types.OverlaySummary{}
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
