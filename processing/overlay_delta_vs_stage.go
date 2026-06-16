package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// applyDeltaVsStage is the CHAIN-host runtime handler for
// OVERLAY_DELTA_VS_STAGE — the second of two whole-chain overlay kinds
// the E6 epic ships. Replaces the E6-S3 applyChainStub in the
// chainOverlayHandlers dispatch table for OverlayKindDeltaVsStage.
//
// Math (per host coordinate):
//
//	overlay_value = target_value - reference_value
//
// Sibling subtractive twin of OVERLAY_INDEX_VS_STAGE (applyIndexVsStage —
// see overlay_index_vs_stage.go). The handler inherits the SHAPE of the
// target stage's host result using the same precedence as
// applyIndexVsStage (matrix from crosstab target, series from grouped
// Process target, scalar from scalar target — chainStageShape).
//
// Key differences from OVERLAY_INDEX_VS_STAGE (per the E6-S5 acceptance):
//
//   - Per-key arithmetic is absolute difference `target - reference`
//     rather than ratio × 100. See deltaKernel below.
//   - Zero reference is a number, not a divisor — the handler does NOT
//     emit PULSE_OVERLAY_REF_ZERO on zero reference values (mirrors the
//     existing OVERLAY_DELTA_VS_BASELINE / _MARGIN / _SIBLING family).
//   - Missing reference key when the target key is present: treat the
//     reference as 0 (so delta == target) and emit
//     PULSE_OVERLAY_REFERENCE_UNKNOWN warning (canonical chain-overlay
//     missing-reference code, landed with E6-S6).
//   - Summary baseline is 0 (renderers centre diverging colour ramps on
//     zero for the DELTA family) rather than 100 (INDEX family).
//
// Shape divergence defence: same rule as applyIndexVsStage — when
// target / ref host shapes disagree the handler emits ONE warning per
// spec under PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT (the canonical
// code landed with E6-S6) and surfaces an empty payload that inherits
// the target's shape.
//
// Per the story's "No re-traversal of source cohort" acceptance: this
// handler reads only the two *Response objects' top-level slots
// (Crosstab.Matrix, Data) — no record stream is opened, no schema is
// re-read.
func applyDeltaVsStage(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []OverlayWarning, error) {
	if target == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil target stage response",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind":         string(spec.Kind),
				"target_index": targetIdx,
			})
	}
	if ref == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil reference stage response",
			map[string]any{
				"code":      string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind":      string(spec.Kind),
				"ref_index": refIdx,
			})
	}

	targetShape := chainStageShape(target)
	refShape := chainStageShape(ref)

	if targetShape != refShape {
		layer := types.OverlayLayer{
			Name:  chainOverlayLayerName(spec),
			Kind:  spec.Kind,
			Scope: spec.Scope,
			Payload: types.OverlayPayload{
				Shape: targetShape,
			},
		}
		warnings := []OverlayWarning{{
			Code:    string(errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT),
			Message: "overlay " + string(spec.Kind) + " target and reference stages disagree on host shape; surfacing empty payload",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"target_index": targetIdx,
				"ref_index":    refIdx,
				"target_shape": string(targetShape),
				"ref_shape":    string(refShape),
			},
		}}
		return layer, warnings, nil
	}

	switch targetShape {
	case types.OverlayShapeMatrix:
		return applyDeltaVsStageMatrix(spec, target, ref, targetIdx, refIdx)
	case types.OverlayShapeSeries:
		return applyDeltaVsStageSeries(spec, target, ref, targetIdx, refIdx)
	}
	return applyDeltaVsStageScalar(spec, target, ref, targetIdx, refIdx)
}

// deltaKernel computes the DELTA overlay arithmetic for one
// (target, reference) pair. Returns the absolute difference
// target - reference. Sibling to indexKernel (overlay_index_vs_stage.go);
// the kernel is intentionally trivial — the per-coordinate degeneracy
// concerns INDEX has (zero denominator, non-finite quotient) do not
// apply to subtraction so the kernel is pure float64 arithmetic with no
// ok flag. Extracted as a one-liner for parity with the indexKernel
// pattern and so a future kernel-indirection refactor (story note "a
// chainOverlayKernel func indirection so S4 and S5 differ only in
// kernel registration") has a stable callee shape to point at.
func deltaKernel(target, reference float64) float64 {
	return target - reference
}

// applyDeltaVsStageMatrix implements the matrix arm of
// OVERLAY_DELTA_VS_STAGE. Walks the target stage's MatrixPayload
// (row-major), looks up each cell's matching (rowAxisKey, colAxisKey)
// in the reference stage's MatrixPayload via the shared
// buildMatrixCellLookup helper (overlay_index_vs_stage.go), and folds
// the deltaKernel result into the overlay's output cells.
//
// Missing reference cells (target carries a cell with row + col keys
// the reference matrix did not surface) emit
// PULSE_OVERLAY_REFERENCE_UNKNOWN with a "ref_missing" Detail flag AND
// surface the delta computed against an implicit zero reference (so the
// overlay cell value equals the target cell value verbatim per the
// E6-S5 acceptance "Missing reference key → delta defined as
// `target - 0`"). Zero reference cells are folded silently — zero is a
// number, not a divisor.
//
// Output payload mirrors the target matrix's RowKeys / ColumnKeys /
// headers so renderers can lay the overlay on top of the target base
// matrix with the same header machinery as MATRIX-host
// OVERLAY_DELTA_VS_MARGIN.
func applyDeltaVsStageMatrix(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []OverlayWarning, error) {
	targetMx := target.Crosstab.Matrix
	refMx := ref.Crosstab.Matrix

	refLookup := buildMatrixCellLookup(refMx)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		var rowKeyStr string
		if i < len(targetMx.Cells) {
			rowKeyStr = axisKeyToString(targetMx.RowKeys[i])
		}
		for j := 0; j < colCount; j++ {
			if i >= len(targetMx.Cells) || j >= len(targetMx.Cells[i]) {
				continue
			}
			cell := targetMx.Cells[i][j]
			if !cell.Present {
				continue
			}
			targetVal, ok := scalarFromCell(cell)
			if !ok {
				// Map-valued / rich cell — DELTA is scalar-only; the
				// per-kind validator at E6-S7 will reject this, but the
				// handler stays defensive.
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			refVal, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				// Per the E6-S5 acceptance: missing reference key →
				// delta defined as `target - 0` AND warning emitted.
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
					Message: "overlay " + string(spec.Kind) + " reference cell missing for target (row, col) key pair; using zero as implicit reference",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"target_index": targetIdx,
						"ref_index":    refIdx,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"ref_missing":  true,
					},
				})
				refVal = 0
			}
			score := deltaKernel(targetVal, refVal)
			row[j] = types.MatrixCell{Value: score, Present: true}
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
		cells[i] = row
	}

	overlayPayload := &types.MatrixPayload{
		RowHeader:        targetMx.RowHeader,
		ColumnHeader:     targetMx.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), targetMx.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), targetMx.ColumnKeys...),
		Cells:            cells,
		CellLabel:        chainOverlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  chainOverlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: overlayPayload,
		},
	}
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

// applyDeltaVsStageSeries implements the series arm of
// OVERLAY_DELTA_VS_STAGE for grouped Process responses. The target's
// Response.Data is walked in order; each row's canonical row-key is
// looked up against an identically-built map of the reference's
// Response.Data rows via the shared buildSeriesRowLookup helper
// (overlay_index_vs_stage.go). The per-row numeric value is folded
// through deltaKernel.
//
// Missing reference rows (target row key not present in the reference
// table) emit PULSE_OVERLAY_REFERENCE_UNKNOWN with a "ref_missing"
// detail flag AND surface a SeriesEntry whose Statistic equals the
// target value (delta computed against implicit zero reference per
// acceptance). Zero reference values fold silently.
func applyDeltaVsStageSeries(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []OverlayWarning, error) {
	refIndex, refValueCol := buildSeriesRowLookup(ref.Data)
	_ = refValueCol // kept for future per-kind diagnostics

	entries := make([]types.SeriesEntry, 0, len(target.Data))
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i, row := range target.Data {
		keyStr, _, value, hasValue := encodeSeriesRow(row)
		if !hasValue {
			// Target row had no numeric column to fold — skip rather
			// than fabricate an entry. The validator at E6-S7 will gate
			// this at predict time.
			continue
		}
		refVal, refPresent := refIndex[keyStr]
		if !refPresent {
			warnings = append(warnings, OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
				Message: "overlay " + string(spec.Kind) + " reference row missing for target row key; using zero as implicit reference",
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"target_index": targetIdx,
					"ref_index":    refIdx,
					"row_index":    i,
					"row_key":      keyStr,
					"ref_missing":  true,
				},
			})
			refVal = 0
		}
		score := deltaKernel(value, refVal)
		entryKey := types.AxisKey{keyStr}
		entry := types.SeriesEntry{
			Key: entryKey,
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
		Name:  chainOverlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: entries,
			},
		},
	}
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

// applyDeltaVsStageScalar implements the scalar arm of
// OVERLAY_DELTA_VS_STAGE. A scalar host carries a single value (e.g.
// total of a single AGG_SUM with no Groups) — the handler extracts
// that single number from each of target / ref via the same
// singleScalarFromResponse path (overlay_index_vs_stage.go) and folds
// them through deltaKernel.
//
// Edge cases:
//
//   - Both stages have empty Data: emit an empty SCALAR layer with
//     a single PULSE_OVERLAY_REFERENCE_UNKNOWN warning ("no scalar value
//     to fold") and surface NaN in Payload.Scalar — there is nothing
//     meaningful to subtract.
//   - Target has Data but reference doesn't: emit
//     PULSE_OVERLAY_REFERENCE_UNKNOWN, treat ref as 0 (per acceptance),
//     and surface the target value as the delta.
//   - Reference value zero: no warning, delta equals target verbatim.
func applyDeltaVsStageScalar(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []OverlayWarning, error) {
	targetVal, targetHas := singleScalarFromResponse(target)
	refVal, refHas := singleScalarFromResponse(ref)

	var warnings []OverlayWarning
	layer := types.OverlayLayer{
		Name:  chainOverlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeScalar,
		},
	}
	baseline := 0.0
	layer.Summary = &types.OverlaySummary{Baseline: &baseline}

	if !targetHas {
		// No target scalar to fold — there is no meaningful delta to
		// emit. Surface the warning and NaN payload.
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
			Message: "overlay " + string(spec.Kind) + " missing scalar value on target stage",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"target_index": targetIdx,
				"ref_index":    refIdx,
				"target_has":   targetHas,
				"ref_has":      refHas,
			},
		})
		nan := math.NaN()
		layer.Payload.Scalar = &nan
		return layer, warnings, nil
	}
	if !refHas {
		// Missing reference scalar → treat as implicit zero per the
		// E6-S5 acceptance "Missing reference key → delta defined as
		// `target - 0`". Emit the warning so the renderer sees the
		// diagnostic.
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
			Message: "overlay " + string(spec.Kind) + " missing scalar value on reference stage; using zero as implicit reference",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"target_index": targetIdx,
				"ref_index":    refIdx,
				"target_has":   targetHas,
				"ref_has":      refHas,
				"ref_missing":  true,
			},
		})
		refVal = 0
	}
	score := deltaKernel(targetVal, refVal)
	scoreCopy := score
	layer.Payload.Scalar = &scoreCopy
	one := 1
	layer.Summary.Count = &one
	layer.Summary.Min = &scoreCopy
	layer.Summary.Max = &scoreCopy
	return layer, warnings, nil
}
