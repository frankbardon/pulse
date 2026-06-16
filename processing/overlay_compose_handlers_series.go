package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// COMPOSE-host SERIES-shape overlay handlers.
//
// Dual-shape kinds (OVERLAY_INDEX_VS_REF, OVERLAY_DELTA_VS_REF) +
// the series-only OVERLAY_T_VS_REF dispatch into the series arms
// below when the chassis detects a SERIES host. The MATRIX arms
// continue to live alongside in overlay_compose_handlers.go.
//
// Shape dispatch: each dual-shape handler
// in overlay_compose_handlers.go starts with a `composeHostIsSeries`
// arm — when the reference and target slots both carry SERIES-shape
// host results (resp.Data populated, resp.Crosstab nil/empty), the
// matrix lookup is replaced with the buildSeriesRowLookup pattern
// the CHAIN-host overlay handlers (overlay_index_vs_stage.go +
// overlay_delta_vs_stage.go) already use.
//
// Streamability subtlety: the per-kind streamability flag
// (types/overlay_streamability.go) declares the kind's INTRINSIC
// streaming capability. For the dual-shape kinds the MATRIX arm
// rides through the slot barrier in service.Compose /
// service.ComposeParallel (which forces buffered execution by
// construction); the SERIES arm is fold-only (single accumulator per
// group; no peer-cell lookup) and matches the kind-catalog-v1
// "Streaming-capable subset". The runtime gate (canStreamOverlays
// per CLAUDE.md) keeps the matrix-host routes buffered regardless of
// the per-kind flag.
//
// Per-handler tests live in overlay_compose_handlers_series_test.go.

// composeHostIsSeries reports whether the per-spec reference + first
// target both carry SERIES-shape host responses. The chassis schema-
// match gate already enforces shape parity between reference
// and target slots — when this returns true the per-kind handler
// dispatches into its series arm.
//
// A nil response (failed slot under FailFast=false) returns false so
// the matrix arm's defense-in-depth nil check surfaces a coded error
// rather than the series arm silently producing an empty layer.
func composeHostIsSeries(reference *types.Response, target *types.Response) bool {
	if reference == nil || target == nil {
		return false
	}
	if reference.Crosstab != nil && reference.Crosstab.Matrix != nil {
		return false
	}
	if target.Crosstab != nil && target.Crosstab.Matrix != nil {
		return false
	}
	return len(reference.Data) > 0 || len(target.Data) > 0
}

// applyIndexVsRefSeries is the SERIES-host arm of OVERLAY_INDEX_VS_REF.
// Walks the target Response.Data rows in order, looks up each row's
// canonical row-key against the reference data, and emits one
// SeriesEntry per present target row: `(target / ref) * scale` where
// scale defaults to 100.
//
// Math (per host group `i`):
//
//	scale = Params["scale"] (default 100)
//	if ref_val == 0 ⇒ NaN + PULSE_OVERLAY_REF_ZERO warning
//	otherwise       ⇒ (target / ref) * scale
//
// Missing reference rows (target row key not present in the reference
// table) emit PULSE_OVERLAY_REF_ZERO with a `ref_missing=true` Detail
// flag and surface as a SeriesEntry whose Summary leaves Statistic
// unset (the row is dropped from the output series — mirrors
// applyIndexVsStageSeries).
//
// Output payload is a SeriesPayload whose Entries match the target's
// Response.Data order element-for-element (modulo missing-ref drops).
// Layer-level Summary.Baseline = scale (default 100) for renderer-
// facing centring.
func applyIndexVsRefSeries(spec *types.ComposeOverlaySpec, reference *types.Response, target *types.Response, refIdx int, targetIdx int) (types.OverlayLayer, []types.OverlayWarning, error) {
	scale := scaleFromParams(spec.Params, 100.0)
	refIndex, _ := buildSeriesRowLookup(reference.Data)

	entries := make([]types.SeriesEntry, 0, len(target.Data))
	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []types.OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i, row := range target.Data {
		keyStr, _, value, hasValue := encodeSeriesRow(row)
		if !hasValue {
			// No numeric column on the target row — skip rather than
			// fabricate a NaN entry. The chassis schema-match gate already
			// rejects mismatched series schemas.
			continue
		}
		refVal, refPresent := refIndex[keyStr]
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
			continue
		}
		entry := types.SeriesEntry{Key: types.AxisKey{keyStr}}
		if refVal == 0 {
			warnings = append(warnings, types.OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) + " reference value is zero; substituting NaN",
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"reference":    spec.Reference,
					"target_label": targetLabel,
					"ref_index":    refIdx,
					"target_index": targetIdx,
					"row_index":    i,
					"row_key":      keyStr,
				},
			})
			nan := math.NaN()
			entry.Summary.Statistic = &nan
			entries = append(entries, entry)
			continue
		}
		score := (value / refVal) * scale
		if math.IsNaN(score) || math.IsInf(score, 0) {
			nan := math.NaN()
			entry.Summary.Statistic = &nan
			entries = append(entries, entry)
			continue
		}
		scoreCopy := score
		entry.Summary.Statistic = &scoreCopy
		entries = append(entries, entry)
		if seen == 0 {
			minV, maxV = score, score
		} else {
			if score < minV {
				minV = score
			}
			if score > maxV {
				maxV = score
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
	layer.Summary = summaryWithBaseline(scale, seen, minV, maxV)
	return layer, warnings, nil
}

// applyDeltaVsRefSeries is the SERIES-host arm of OVERLAY_DELTA_VS_REF.
// Per-group additive `target - ref`. Subtraction is total over the
// reals so there is no zero-denominator hazard — the handler never
// emits PULSE_OVERLAY_REF_ZERO for a zero reference. Missing reference
// rows still emit a warning per the PRD §4 missing-key contract (the
// entry is dropped from the output series).
func applyDeltaVsRefSeries(spec *types.ComposeOverlaySpec, reference *types.Response, target *types.Response, refIdx int, targetIdx int) (types.OverlayLayer, []types.OverlayWarning, error) {
	refIndex, _ := buildSeriesRowLookup(reference.Data)

	entries := make([]types.SeriesEntry, 0, len(target.Data))
	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []types.OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i, row := range target.Data {
		keyStr, _, value, hasValue := encodeSeriesRow(row)
		if !hasValue {
			continue
		}
		refVal, refPresent := refIndex[keyStr]
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
			continue
		}
		diff := value - refVal
		entry := types.SeriesEntry{Key: types.AxisKey{keyStr}}
		diffCopy := diff
		entry.Summary.Statistic = &diffCopy
		entries = append(entries, entry)
		if seen == 0 {
			minV, maxV = diff, diff
		} else {
			if diff < minV {
				minV = diff
			}
			if diff > maxV {
				maxV = diff
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
	layer.Summary = summaryWithBaseline(0.0, seen, minV, maxV)
	return layer, warnings, nil
}

// applyTVsRef is the COMPOSE-host runtime handler for OVERLAY_T_VS_REF.
// Per-group Welch t-test against the reference slot's matching group.
// Series-shape sibling of OVERLAY_T_CELL — reuses the same default-
// variance / default-sample-size policy and the same studentTTwoSidedP
// helper.
//
// Components-source: when both target and reference value
// columns carry a `{mean, variance, n}` triple map — emitted by
// AGG_WELFORD via the MetaAggregator path — the handler consumes the
// triple directly and bypasses the Params defaults. The overlay
// p-value matches TEST_WELCH / TEST_T against the same (mean,
// variance, n) inputs byte-equal. When both value columns are scalar
// numeric the handler falls back to the Params-default path
// (byte-identical to the pre-Components scalar baseline). The legacy
// processing.WelfordTriple type-assertion on the row's value column
// is no longer performed.
//
// Mixed value-column shapes (one triple, one scalar) are blocked
// upstream by the compose schema-match gate. The handler
// still emits a defensive PULSE_OVERLAY_SCHEMA_DIVERGENT if a mixed
// pair slips through.
//
// Math (per host group `i`):
//
//	target_val = target.Data[i].value
//	ref_val    = ref.Data[i].value
//	var_target = triple.Variance OR Params["variance_target"]    (default 1.0)
//	var_ref    = triple.Variance OR Params["variance_ref"]       (default 1.0)
//	n_target   = triple.N        OR Params["sample_size_target"] (default 2)
//	n_ref      = triple.N        OR Params["sample_size_ref"]    (default 2)
//	se         = sqrt(var_target/n_target + var_ref/n_ref)
//	t          = (target_val - ref_val) / se
//	df         = Welch-Satterthwaite recurrence (same as TEST_T)
//	p_value    = studentTTwoSidedP(t, df)
//
// Missing reference rows emit PULSE_OVERLAY_REF_ZERO with a
// ref_missing=true Detail flag; the affected entry's Statistic is
// NaN. Degenerate inputs (se == 0, n < 2) produce NaN p-values with
// the same warning. Reuses welchTTest (the shared MATRIX-arm helper
// in overlay_compose_handlers.go) so the SERIES and MATRIX surfaces
// produce identical p-values for the same (mean, variance, n) triple.
func applyTVsRef(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []types.OverlayWarning, error) {
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

		// Mixed value-column shapes — schema-match gate should
		// already reject this upstream; emit a defensive coded error
		// if a mismatched pair slips through.
		if hasTriple != refEntry.HasTriple {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay "+string(spec.Kind)+" detected divergent value-column shapes (triple vs scalar) at runtime",
				map[string]any{
					"code":         string(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT),
					"kind":         string(spec.Kind),
					"ref_index":    refIdx,
					"target_index": targetIdx,
					"row_index":    i,
					"row_key":      keyStr,
				})
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

		p, ok := welchTTest(targetMean, targetVar, targetN, refMean, refVar, refN)
		if !ok {
			warnings = append(warnings, types.OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) + " Welch t-test undefined for group (zero SE OR insufficient n)",
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
	// Inferential overlays do not surface a Baseline (mirrors T_CELL).
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
