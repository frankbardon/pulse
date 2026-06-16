package processing

import (
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// applyIndexVsStage is the CHAIN-host runtime handler for
// OVERLAY_INDEX_VS_STAGE — the first whole-chain overlay kind to ship
// real arithmetic. Replaces the E6-S3 applyChainStub in the
// chainOverlayHandlers dispatch table.
//
// Math (per host coordinate):
//
//	overlay_value = target_value / reference_value * 100.0
//
// The handler inherits the SHAPE of the target stage's host result:
//
//   - target carries a Crosstab.Matrix payload ⇒ OverlayShapeMatrix —
//     per-cell index folded over the target's row × column grid against
//     the reference matrix's matching (rowAxisKey, colAxisKey) cell.
//   - target carries non-empty Data with no Crosstab payload ⇒
//     OverlayShapeSeries — per-row index against the reference's
//     row-key-keyed lookup; reference rows missing their key in the
//     reference table fire PULSE_OVERLAY_REF_ZERO (the canonical
//     "denominator absent" warning per E1's catalog).
//   - target carries empty Data ⇒ OverlayShapeScalar — single
//     reference-divided index emitted as the layer's Payload.Scalar.
//
// The Index handler reuses the same E4 INDEX_VS_MARGIN arithmetic kernel
// (indexKernel — see overlay.go) so per-coordinate arithmetic is
// byte-identical to the MATRIX-host INDEX_VS_MARGIN family. Reference
// stage resolution happens BEFORE this handler is reached (the
// chainOverlayHandlers dispatcher resolves Target + Ref via the
// StageRef resolver in overlay_chain_dispatch.go), so the handler
// receives finalised *Response objects for both slots.
//
// Shape divergence defence (the story description "handler MAY assume
// shapes match but emit PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT as
// defence if it sees divergence at runtime"): when the target and
// reference stages disagree on host shape (target matrix, ref series;
// target series, ref scalar; etc) the handler emits ONE warning per
// spec under PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT (the canonical
// code landed with E6-S6) and falls back to an empty payload that
// inherits the target stage's shape.
//
// Zero / missing-reference policy: zero or missing reference values
// emit PULSE_OVERLAY_REF_ZERO (matrix per-cell, series per-entry,
// scalar single warning) and substitute NaN for the affected
// coordinate. Mirrors the MATRIX-host INDEX_VS_MARGIN handler's
// per-cell warning shape exactly.
//
// Per the story's "No re-traversal of source cohort" acceptance: this
// handler reads only the two *Response objects' top-level slots
// (Crosstab.Matrix, Data) — no record stream is opened, no schema is
// re-read.
func applyIndexVsStage(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// Shape-divergence defence: when target and reference disagree on
	// host shape, emit a single warning (canonical code lands with
	// E6-S6; fallback per story rule) and surface an empty overlay
	// payload that inherits the target's shape — the renderer sees the
	// divergence diagnostic + an empty layer instead of a malformed
	// arithmetic crash.
	if targetShape != refShape {
		layer := types.OverlayLayer{
			Name:  chainOverlayLayerName(spec),
			Kind:  spec.Kind,
			Scope: spec.Scope,
			Payload: types.OverlayPayload{
				Shape: targetShape,
			},
		}
		warnings := []types.OverlayWarning{{
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
		return applyIndexVsStageMatrix(spec, target, ref, targetIdx, refIdx)
	case types.OverlayShapeSeries:
		return applyIndexVsStageSeries(spec, target, ref, targetIdx, refIdx)
	}
	return applyIndexVsStageScalar(spec, target, ref, targetIdx, refIdx)
}

// chainStageShape resolves a stage's *Response into the OverlayShape
// the CHAIN-host overlay handlers branch on. Mirrors the precedence
// inferChainStubShape uses in overlay_chain_dispatch.go — codified
// here so the real handler and the E6-S3 stub stay byte-equivalent on
// shape selection.
//
// Precedence (top-down):
//
//   - non-nil Crosstab.Matrix ⇒ OverlayShapeMatrix.
//   - non-empty Data ⇒ OverlayShapeSeries (canonical grouped Process
//     shape, even when the row count is 1 — the inference looks at
//     populated-Data, NOT at row count; a scalar Process result that
//     carries `Data: nil` falls through to OverlayShapeScalar below).
//   - otherwise ⇒ OverlayShapeScalar.
func chainStageShape(resp *types.Response) types.OverlayShape {
	if resp == nil {
		return types.OverlayShapeScalar
	}
	if resp.Crosstab != nil && resp.Crosstab.Matrix != nil {
		return types.OverlayShapeMatrix
	}
	if len(resp.Data) > 0 {
		return types.OverlayShapeSeries
	}
	return types.OverlayShapeScalar
}

// chainOverlayLayerName resolves the layer's renderer-facing Name from
// a ChainOverlaySpec. When the spec sets Name explicitly it is
// returned verbatim; otherwise the kind string (e.g.
// "OVERLAY_INDEX_VS_STAGE") becomes the default label so the renderer
// always sees a populated Name field. Mirrors the chassis stub
// (overlay_chain_dispatch.go) which uses the same fallback.
func chainOverlayLayerName(spec *types.ChainOverlaySpec) string {
	if spec.Name != "" {
		return spec.Name
	}
	return string(spec.Kind)
}

// indexKernel computes the INDEX overlay arithmetic for one
// (target, reference) pair shared by both the MATRIX-host
// applyIndexVsMargin handler and the CHAIN-host applyIndexVsStage
// handler. Returns:
//
//   - value: target / reference * 100.0 when the kernel can produce
//     a meaningful number; math.NaN otherwise.
//   - ok: true when the kernel produced a finite scalar; false when
//     the reference is zero, missing, or the result is non-finite
//     (NaN / +Inf / -Inf). The caller surfaces PULSE_OVERLAY_REF_ZERO
//     against the host coordinate when ok is false.
//
// The kernel intentionally does NOT take present flags — callers
// short-circuit missing-target / missing-reference cases before
// reaching the kernel. This keeps the kernel a pure float64 →
// float64 transform: arithmetic only, no branching on existence.
//
// Extracted from the inline INDEX_VS_MARGIN arithmetic so the
// per-coordinate math stays parity-true between MATRIX-host and
// CHAIN-host kinds. E6-S5 will add a sibling deltaKernel for
// OVERLAY_DELTA_VS_STAGE.
func indexKernel(target, reference float64) (float64, bool) {
	if reference == 0 {
		return math.NaN(), false
	}
	v := target / reference * 100.0
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return math.NaN(), false
	}
	return v, true
}

// applyIndexVsStageMatrix implements the matrix arm of
// OVERLAY_INDEX_VS_STAGE. Walks the target stage's MatrixPayload
// (row-major), looks up each cell's matching (rowAxisKey, colAxisKey)
// in the reference stage's MatrixPayload via a precomputed
// rowKey × colKey → cell lookup, and folds the indexKernel result
// into the overlay's output cells.
//
// Missing reference cells (target carries a cell with row + col keys
// the reference matrix did not surface) emit
// PULSE_OVERLAY_REF_ZERO with a "ref_missing" Detail flag. Zero
// reference cells emit the same code with the indexKernel's NaN
// substitution. Per the story's acceptance, missing keys contribute
// silent zero to the denominator (no spurious accumulation — the
// per-cell warning is the diagnostic surface).
//
// Output payload mirrors the target matrix's RowKeys / ColumnKeys /
// headers so renderers can lay the overlay on top of the target base
// matrix with the same header machinery as MATRIX-host
// INDEX_VS_MARGIN.
func applyIndexVsStageMatrix(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []types.OverlayWarning, error) {
	targetMx := target.Crosstab.Matrix
	refMx := ref.Crosstab.Matrix

	// Build a (rowKeyString, colKeyString) → cell scalar lookup on the
	// reference matrix. Using canonical string-keyed tuples keeps the
	// lookup independent of axis-tuple element types (numeric bins,
	// categorical strings, date strings each get a faithful
	// representation through axisKeyToString).
	refLookup := buildMatrixCellLookup(refMx)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []types.OverlayWarning
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
				// Map-valued / rich cell — INDEX is scalar-only; the
				// per-kind validator at E6-S7 will reject this, but the
				// handler stays defensive.
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			refVal, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				warnings = append(warnings, types.OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell missing for target (row, col) key pair",
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
				continue
			}
			score, okScore := indexKernel(targetVal, refVal)
			if !okScore {
				warnings = append(warnings, types.OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell is zero (or non-finite quotient); substituting NaN",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"target_index": targetIdx,
						"ref_index":    refIdx,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
					},
				})
				row[j] = types.MatrixCell{Value: score, Present: true}
				continue
			}
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

// matrixCellLookupKey pairs a row-axis canonical-string key with a
// column-axis canonical-string key for the reference-cell lookup. The
// (row, col) tuple is sufficient because matrix cells are unambiguously
// identified by their (rowKey, colKey) pair across both target and
// reference matrices (per the parallel-slice contract MatrixPayload.RowKeys
// + ColumnKeys preserve element-for-element with Cells).
type matrixCellLookupKey struct {
	row string
	col string
}

// buildMatrixCellLookup folds a MatrixPayload's present cells into a
// `(rowKey, colKey) → scalar` lookup table. Used by
// applyIndexVsStageMatrix to resolve per-cell reference values without
// re-walking the reference matrix for every target cell.
//
// Cells that are absent OR rich-valued (map-valued from RichAggregator
// outputs) are skipped — INDEX is scalar-only. The pre-built lookup is
// reused across every target cell so the matrix arm runs in
// O(target_cells + ref_cells) instead of O(target_cells × ref_cells).
func buildMatrixCellLookup(mx *types.MatrixPayload) map[matrixCellLookupKey]float64 {
	if mx == nil {
		return map[matrixCellLookupKey]float64{}
	}
	out := make(map[matrixCellLookupKey]float64, len(mx.RowKeys)*len(mx.ColumnKeys))
	for i := 0; i < len(mx.RowKeys) && i < len(mx.Cells); i++ {
		rowKeyStr := axisKeyToString(mx.RowKeys[i])
		for j := 0; j < len(mx.ColumnKeys) && j < len(mx.Cells[i]); j++ {
			cell := mx.Cells[i][j]
			if !cell.Present {
				continue
			}
			v, ok := scalarFromCell(cell)
			if !ok {
				continue
			}
			out[matrixCellLookupKey{row: rowKeyStr, col: axisKeyToString(mx.ColumnKeys[j])}] = v
		}
	}
	return out
}

// axisKeyToString renders an AxisKey (composite axis tuple) into a
// stable canonical string for keyed lookup. Pipe-separated stringified
// elements; the same tuple always renders the same way so two AxisKeys
// that compare element-equal also string-equal.
//
// The encoding intentionally avoids fmt.Sprintf in JSON-bearing paths
// (CLAUDE.md "Output Format Contract") — this string never lands in
// the envelope directly; it is a private lookup-table key. We still
// stay strconv-based for clarity since the chain-handler key is
// internal to the runtime.
func axisKeyToString(key types.AxisKey) string {
	if len(key) == 0 {
		return ""
	}
	parts := make([]string, len(key))
	for i, v := range key {
		parts[i] = anyToCanonicalString(v)
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += "|" + parts[i]
	}
	return out
}

// anyToCanonicalString stringifies an `any` JSON-shaped value into a
// stable canonical form. Used by axisKeyToString (matrix lookups) and
// dataRowKey (series lookups) so the same value always renders the
// same way regardless of whether it came through Go's `interface{}`
// via JSON unmarshal (where every number is float64) or via the
// in-process grouper (where numeric values may be int / int64).
//
// Float64 values that hold integer-valued doubles render as their
// integer form to keep "1.0" and "1" indistinguishable from the
// renderer's POV (e.g. an `int(1)` group key from the grouper and the
// `float64(1)` shape it lands in after JSON round-trip both yield
// "1"). Non-integer floats use strconv.FormatFloat with 'g' precision.
func anyToCanonicalString(v any) string {
	switch x := v.(type) {
	case nil:
		return "\x00null"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) && !math.IsNaN(x) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case float32:
		f := float64(x)
		if f == math.Trunc(f) && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'g', -1, 32)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	}
	// Fallback for unexpected types — keep deterministic so the lookup
	// table remains keyed-consistent.
	return "\x01unknown"
}

// applyIndexVsStageSeries implements the series arm of
// OVERLAY_INDEX_VS_STAGE for grouped Process responses. The target's
// Response.Data is walked in order; each row's canonical row-key
// (every non-numeric column joined by pipe) is looked up against an
// identically-built map of the reference's Response.Data rows. The
// per-row numeric value (the aggregator output column) is divided
// through indexKernel.
//
// Heuristic row-shape rule: a Response.Data row from a grouped
// Process result holds:
//
//   - one entry per group field (string or other non-float-shaped value),
//   - one entry per aggregator (float64 / int — uniformly numeric).
//
// The handler treats every numeric column as a candidate "value" and
// every non-numeric column as a "group key" component. When the target
// and reference rows agree on which keys are non-numeric vs numeric
// (the same Request was run against both stages — the canonical
// per-stage shape contract), the row-key encoding stays parity-true.
// When multiple numeric columns are present, the FIRST (sorted by
// column name) is treated as the value column so the handler stays
// deterministic without per-kind configuration. The follow-up
// configuration surface (E6-S8 / S11 integration) may grow an
// explicit "value column" param; today the deterministic-first rule
// keeps the implementation parity-true with the common single-aggregator
// shape.
//
// Missing reference rows (target row key not present in the
// reference table) emit PULSE_OVERLAY_REF_ZERO with a "ref_missing"
// detail flag and surface as a SeriesEntry whose Summary leaves the
// statistic absent (NaN substitution preserved on a future renderer
// pass — for now the entry is dropped from the output series). Zero
// reference values emit the same code with NaN substitution carried on
// the entry's Summary.Statistic.
func applyIndexVsStageSeries(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []types.OverlayWarning, error) {
	refIndex, refValueCol := buildSeriesRowLookup(ref.Data)

	entries := make([]types.SeriesEntry, 0, len(target.Data))
	var (
		warnings []types.OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i, row := range target.Data {
		keyStr, _, value, hasValue := encodeSeriesRow(row)
		if !hasValue {
			// Target row had no numeric column to fold — skip rather
			// than fabricate a NaN entry. The validator at E6-S7 will
			// gate this at predict time.
			continue
		}
		refVal, refPresent := refIndex[keyStr]
		_ = refValueCol // kept for future per-kind diagnostics
		if !refPresent {
			warnings = append(warnings, types.OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) + " reference row missing for target row key",
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"target_index": targetIdx,
					"ref_index":    refIdx,
					"row_index":    i,
					"row_key":      keyStr,
					"ref_missing":  true,
				},
			})
			continue
		}
		score, ok := indexKernel(value, refVal)
		entryKey := types.AxisKey{keyStr}
		entry := types.SeriesEntry{
			Key: entryKey,
		}
		if !ok {
			warnings = append(warnings, types.OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) + " reference value is zero (or quotient non-finite); substituting NaN",
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"target_index": targetIdx,
					"ref_index":    refIdx,
					"row_index":    i,
					"row_key":      keyStr,
				},
			})
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

// buildSeriesRowLookup folds a slice of Response.Data rows into a
// `rowKey → value` lookup table. Used by applyIndexVsStageSeries to
// resolve per-row reference values without re-walking the reference
// table for every target row. Returns the canonical "value column" name
// alongside the lookup so the caller can surface it in diagnostics
// when needed.
//
// When the rows contain multiple numeric columns the first-by-sorted-
// column-name is treated as the value column. Empty data ⇒ empty
// lookup and empty value-column name.
func buildSeriesRowLookup(rows []map[string]any) (map[string]float64, string) {
	out := make(map[string]float64, len(rows))
	if len(rows) == 0 {
		return out, ""
	}
	var valueCol string
	for _, row := range rows {
		keyStr, vc, value, hasValue := encodeSeriesRow(row)
		if vc != "" {
			valueCol = vc
		}
		if !hasValue {
			continue
		}
		out[keyStr] = value
	}
	return out, valueCol
}

// encodeSeriesRow returns the canonical row-key string, the column
// name treated as the row's value column, the row's value, and an
// "has-value" flag for a single Response.Data row.
//
// Encoding rule: walk row entries in sorted column-name order; any
// entry whose stringified form is numeric (float64 / int / etc) is
// treated as a candidate value column. The FIRST numeric column
// (sort order) is the value; the remaining numeric columns are
// folded into the key alongside non-numeric columns to keep
// multi-aggregator rows uniquely keyed. Non-numeric columns are
// always part of the key.
//
// Returns hasValue=false when the row has no numeric column at all
// (no aggregator output to fold).
func encodeSeriesRow(row map[string]any) (key string, valueCol string, value float64, hasValue bool) {
	if len(row) == 0 {
		return "", "", 0, false
	}
	cols := make([]string, 0, len(row))
	for k := range row {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	keyParts := make([]string, 0, len(row))
	for _, col := range cols {
		v := row[col]
		if num, ok := coerceNumericValue(v); ok {
			if !hasValue {
				valueCol = col
				value = num
				hasValue = true
				continue
			}
			// Subsequent numeric columns participate in the key so
			// multi-aggregator rows still produce unique keys.
			keyParts = append(keyParts, col+"="+anyToCanonicalString(v))
			continue
		}
		keyParts = append(keyParts, col+"="+anyToCanonicalString(v))
	}
	if len(keyParts) == 0 {
		return "", valueCol, value, hasValue
	}
	out := keyParts[0]
	for i := 1; i < len(keyParts); i++ {
		out += "|" + keyParts[i]
	}
	return out, valueCol, value, hasValue
}

// coerceNumericValue tests whether a Response.Data column value is a
// numeric scalar suitable for the value-column heuristic. Mirrors the
// type coverage of coerceFloat in processing/chain.go but is private
// to the chain-overlay handler so the two surfaces evolve
// independently.
func coerceNumericValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

// applyIndexVsStageScalar implements the scalar arm of
// OVERLAY_INDEX_VS_STAGE. A scalar host carries a single value (e.g.
// total of a single AGG_SUM with no Groups) — the handler extracts
// that single number from each of target / ref via the same
// encodeSeriesRow path (which already handles the "one numeric
// column" case) and folds them through indexKernel.
//
// Edge cases:
//
//   - Both stages have empty Data: emit an empty SCALAR layer with
//     warning PULSE_OVERLAY_REF_ZERO ("no scalar value to fold").
//   - Target has Data but reference doesn't: same warning, target
//     value preserved in Summary for diagnostic purposes but the
//     overlay's Payload.Scalar is NaN.
//   - Reference value zero: indexKernel produces NaN; warning fires;
//     layer Payload.Scalar surfaces NaN per acceptance.
func applyIndexVsStageScalar(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []types.OverlayWarning, error) {
	targetVal, targetHas := singleScalarFromResponse(target)
	refVal, refHas := singleScalarFromResponse(ref)

	var warnings []types.OverlayWarning
	layer := types.OverlayLayer{
		Name:  chainOverlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeScalar,
		},
	}
	baseline := 100.0
	layer.Summary = &types.OverlaySummary{Baseline: &baseline}

	if !targetHas || !refHas {
		warnings = append(warnings, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " missing scalar value on target or reference stage",
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
	score, ok := indexKernel(targetVal, refVal)
	if !ok {
		warnings = append(warnings, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " reference scalar is zero (or quotient non-finite); substituting NaN",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"target_index": targetIdx,
				"ref_index":    refIdx,
			},
		})
		nan := math.NaN()
		layer.Payload.Scalar = &nan
		return layer, warnings, nil
	}
	scoreCopy := score
	layer.Payload.Scalar = &scoreCopy
	one := 1
	layer.Summary.Count = &one
	layer.Summary.Min = &scoreCopy
	layer.Summary.Max = &scoreCopy
	return layer, nil, nil
}

// singleScalarFromResponse extracts a single float64 from a Response
// shaped as a one-row × one-numeric-column Data slice. Used by the
// scalar arm of applyIndexVsStage. Returns (0, false) when:
//
//   - Response is nil or Response.Data is empty.
//   - The first row has zero numeric columns.
//
// When multiple rows are present, only the first is consulted (a
// "scalar" Process result is conventionally a single-row Data slice;
// multi-row Data falls through the series shape before reaching this
// handler).
func singleScalarFromResponse(resp *types.Response) (float64, bool) {
	if resp == nil || len(resp.Data) == 0 {
		return 0, false
	}
	row := resp.Data[0]
	if len(row) == 0 {
		return 0, false
	}
	// Walk in deterministic column order so the chosen scalar is
	// reproducible.
	cols := make([]string, 0, len(row))
	for k := range row {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	for _, col := range cols {
		if v, ok := coerceNumericValue(row[col]); ok {
			return v, true
		}
	}
	return 0, false
}
