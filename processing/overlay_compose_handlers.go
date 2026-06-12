package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Per-kind handlers for the COMPOSE-host overlay catalog (E7-S9).
// Replaces the chassis `applyComposeStub` for the six crosstab-shape
// Compose-only kinds: OVERLAY_INDEX_VS_REF, OVERLAY_DELTA_VS_REF,
// OVERLAY_PROP_Z_CELL, OVERLAY_T_CELL, OVERLAY_CHISQ_VS_REF, and
// OVERLAY_RANK.
//
// Dispatch model:
//
//   - Each handler emits one OverlayLayer per target slot — multi-target
//     specs produce a single OverlayLayer per (spec, target) pair via
//     the composeOverlayHandler signature established in E7-S4. The
//     ApplyComposeOverlays chassis loops per spec and the handler
//     loops per target inside that spec, returning a SINGLE OverlayLayer
//     whose payload aggregates the per-target results (typically the
//     first target's payload for single-target authoring). For multi-
//     target authoring the E7-S15+ multi-ref kinds will land separately;
//     the v1 single-target convention keeps the per-kind handlers simple
//     and matches the chassis stub's single-layer-per-spec contract.
//
//   - Every handler runs AFTER the chassis-side gates accept the spec:
//     resolution (E7-S5), key alignment (E7-S6), schema match (E7-S7),
//     and optional dict-prefix drift (E7-S8). Handlers can rely on
//     byte-equal `(rowKey, colKey)` key sets between reference and
//     target matrices and on structurally identical axis schemas.
//
//   - Output payload mirrors the target slot's matrix shape (RowKeys /
//     ColumnKeys / headers) so renderers can lay the overlay grid on
//     top of the target base with the same header machinery as the
//     per-Request MATRIX-host overlay family.
//
// Structural invariants:
//
//   - Pure functions. No I/O. No goroutines. No mutation of inputs.
//   - MUST NOT import service/ or descriptor/.
//   - No fmt.Sprintf in any JSON-bearing path.
//   - Statistical primitives reuse the existing helpers
//     (`chiSquareSurvival`, `studentTTwoSidedP`, `standardNormalCDF`)
//     so per-cell p-values match the per-Request inferential overlay
//     family + the TEST_* row test surface byte-equal.
//
// Default labels: when spec.Name is empty the layer's renderer-facing
// Name falls back to the kind string (e.g. "OVERLAY_INDEX_VS_REF").
// Mirrors the chassis stub fallback.

// composeOverlayLayerName resolves the renderer-facing label for one
// COMPOSE overlay layer. Mirrors `chainOverlayLayerName` (the CHAIN-
// host equivalent) but reads off `ComposeOverlaySpec` instead of
// `ChainOverlaySpec`.
func composeOverlayLayerName(spec *types.ComposeOverlaySpec) string {
	if spec.Name != "" {
		return spec.Name
	}
	return string(spec.Kind)
}

// composeFirstTarget returns the first target slot's *Response from the
// per-kind handler's `targets` slice plus the first target's index in
// the parallel `targetIdxs` slice. Every v1 single-target Compose kind
// reads from the first target; multi-target kinds (E7-S15+) walk the
// full slice.
//
// Returns (nil, -1) when targets is empty — the chassis already
// rejects empty Targets via PULSE_OVERLAY_TARGET_UNKNOWN before
// dispatching, so this guard is defense in depth.
func composeFirstTarget(targets []*types.Response, targetIdxs []int) (*types.Response, int) {
	if len(targets) == 0 {
		return nil, -1
	}
	idx := -1
	if len(targetIdxs) > 0 {
		idx = targetIdxs[0]
	}
	return targets[0], idx
}

// composeFirstTargetLabel returns the first target slot's label from
// the spec — used in warning Details so renderers can address the
// offending slot by its caller-supplied name.
func composeFirstTargetLabel(spec *types.ComposeOverlaySpec) string {
	if len(spec.Targets) == 0 {
		return ""
	}
	return spec.Targets[0]
}

// readMatrix returns the *MatrixPayload pointer from a *Response, or
// nil when the response does not carry a crosstab matrix. The chassis-
// side schema-match gate (E7-S7 + kindRequiresMatrix) rejects non-
// matrix slots BEFORE the per-kind handler dispatches, so this guard
// is defense in depth.
func readMatrix(resp *types.Response) *types.MatrixPayload {
	if resp == nil || resp.Crosstab == nil {
		return nil
	}
	return resp.Crosstab.Matrix
}

// scaleFromParams reads `Params["scale"]` as a float64. Defaults to
// `100` when the slot is missing OR the value is not a known numeric
// type. JSON numbers decode as float64 so the float64 arm is the hot
// path; the int / int32 / int64 / float32 arms handle round-trip
// shapes from programmatic callers.
func scaleFromParams(params map[string]any, dflt float64) float64 {
	if params == nil {
		return dflt
	}
	v, ok := params["scale"]
	if !ok {
		return dflt
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	}
	return dflt
}

// populationFromParams reads `Params["population"]` for OVERLAY_RANK.
// Returns the default ("matrix") when the slot is missing or empty.
// Unknown values fall back to "matrix" so a typo never produces a
// surprise — the canonical-code dispatch surfaces unknown population
// modes via a layer-level warning at handler entry instead.
func populationFromParams(params map[string]any) string {
	if params == nil {
		return "matrix"
	}
	v, ok := params["population"]
	if !ok {
		return "matrix"
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "matrix"
	}
	return s
}

// sampleSizeFromParams reads an integer sample-size param from the spec's
// Params map. Used by OVERLAY_T_CELL to read per-side sample sizes via
// the "sample_size_target" / "sample_size_ref" keys (or the symmetric
// "sample_size" fallback). Defaults to dflt (2) when missing.
func sampleSizeFromParams(params map[string]any, key string, dflt float64) float64 {
	if params == nil {
		return dflt
	}
	v, ok := params[key]
	if !ok {
		// Try the symmetric "sample_size" fallback.
		v, ok = params["sample_size"]
		if !ok {
			return dflt
		}
	}
	switch x := v.(type) {
	case float64:
		if x > 0 {
			return x
		}
	case float32:
		if x > 0 {
			return float64(x)
		}
	case int:
		if x > 0 {
			return float64(x)
		}
	case int32:
		if x > 0 {
			return float64(x)
		}
	case int64:
		if x > 0 {
			return float64(x)
		}
	}
	return dflt
}

// varianceFromParams reads a per-side variance param. Defaults to dflt
// (1.0) when missing.
func varianceFromParams(params map[string]any, key string, dflt float64) float64 {
	if params == nil {
		return dflt
	}
	v, ok := params[key]
	if !ok {
		// Try the symmetric "variance" fallback.
		v, ok = params["variance"]
		if !ok {
			return dflt
		}
	}
	switch x := v.(type) {
	case float64:
		if x > 0 {
			return x
		}
	case float32:
		if x > 0 {
			return float64(x)
		}
	case int:
		if x > 0 {
			return float64(x)
		}
	case int32:
		if x > 0 {
			return float64(x)
		}
	case int64:
		if x > 0 {
			return float64(x)
		}
	}
	return dflt
}

// buildEmptyMatrixLike constructs a [rows][cols] MatrixCell grid
// pre-populated with `Present: false` so handlers can write present
// cells on demand and leave the rest absent. Used by every matrix-
// emitting Compose handler.
func buildEmptyMatrixLike(rowCount, colCount int) [][]types.MatrixCell {
	cells := make([][]types.MatrixCell, rowCount)
	for i := range cells {
		cells[i] = make([]types.MatrixCell, colCount)
	}
	return cells
}

// composeMatrixOverlayLayer assembles a MATRIX-shape OverlayLayer
// from the target slot's matrix payload + the handler-computed cells
// + optional summary. The output payload mirrors the target's
// RowKeys / ColumnKeys / headers exactly so renderers lay the overlay
// on top of the target base with no re-keying.
func composeMatrixOverlayLayer(spec *types.ComposeOverlaySpec, targetMx *types.MatrixPayload, cells [][]types.MatrixCell, summary *types.OverlaySummary) types.OverlayLayer {
	overlayPayload := &types.MatrixPayload{
		RowHeader:        targetMx.RowHeader,
		ColumnHeader:     targetMx.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), targetMx.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), targetMx.ColumnKeys...),
		Cells:            cells,
		CellLabel:        composeOverlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}
	return types.OverlayLayer{
		Name:  composeOverlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: overlayPayload,
		},
		Summary: summary,
	}
}

// updateMinMaxCount tracks the per-layer min / max / seen-count
// triple as the handler walks present cells. Mirrors the
// applyIndexVsStageMatrix accumulator pattern.
func updateMinMaxCount(score float64, seen *int, minV, maxV *float64) {
	if *seen == 0 {
		*minV, *maxV = score, score
	} else {
		if score < *minV {
			*minV = score
		}
		if score > *maxV {
			*maxV = score
		}
	}
	*seen++
}

// summaryWithBaseline builds an OverlaySummary with the per-layer
// baseline + min / max / count populated when seen > 0. When seen == 0
// the count slot reports zero so the renderer can distinguish "no
// present cells" from "summary not computed".
func summaryWithBaseline(baseline float64, seen int, minV, maxV float64) *types.OverlaySummary {
	b := baseline
	summary := &types.OverlaySummary{Baseline: &b}
	if seen > 0 {
		mn, mx, count := minV, maxV, seen
		summary.Min = &mn
		summary.Max = &mx
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}
	return summary
}

// applyIndexVsRef is the COMPOSE-host runtime handler for
// OVERLAY_INDEX_VS_REF. Dual-shape dispatcher (E7-S10) — detects the
// host shape from the reference + first target slots and routes into
// either the MATRIX arm (per-cell lookup + emit) or the SERIES arm
// (per-row lookup + emit). The chassis schema-match gate (E7-S7)
// guarantees the reference and target slots share the same shape
// before this handler is dispatched.
//
// Math (per host coordinate `(i, j)` for MATRIX or `i` for SERIES):
//
//	scale = Params["scale"] (default 100)
//	if ref_val == 0 ⇒ NaN + PULSE_OVERLAY_REF_ZERO warning
//	otherwise       ⇒ (target / ref) * scale
//
// Missing reference rows / cells (target carries a key the reference
// did not surface) emit PULSE_OVERLAY_REF_ZERO with a `ref_missing`
// Detail flag.
//
// MATRIX arm output payload mirrors the target matrix's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// target base matrix with the same header machinery as MATRIX-host
// INDEX_VS_MARGIN. SERIES arm output payload is a SeriesPayload whose
// entries match the target's Response.Data row order element-for-
// element (modulo missing-ref drops).
func applyIndexVsRef(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []OverlayWarning, error) {
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	if composeHostIsSeries(reference, target) {
		return applyIndexVsRefSeries(spec, reference, target, refIdx, targetIdx)
	}
	refMx := readMatrix(reference)
	targetMx := readMatrix(target)
	if refMx == nil || targetMx == nil {
		// Defense in depth — the chassis schema-match gate already
		// rejects non-MATRIX slots, so this branch should not fire in
		// practice. We surface a coded error so the failure mode stays
		// observable.
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires MATRIX-shape or SERIES-shape slots for reference and target",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	scale := scaleFromParams(spec.Params, 100.0)
	refLookup := buildMatrixCellLookup(refMx)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i := 0; i < rowCount; i++ {
		rowKeyStr := axisKeyToString(targetMx.RowKeys[i])
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
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			refVal, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell missing for target (row, col) key pair",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"ref_index":    refIdx,
						"target_index": targetIdx,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"ref_missing":  true,
					},
				})
				continue
			}
			if refVal == 0 {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell is zero; substituting NaN",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"ref_index":    refIdx,
						"target_index": targetIdx,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
					},
				})
				cells[i][j] = types.MatrixCell{Value: math.NaN(), Present: true}
				continue
			}
			score := (targetVal / refVal) * scale
			if math.IsNaN(score) || math.IsInf(score, 0) {
				cells[i][j] = types.MatrixCell{Value: math.NaN(), Present: true}
				continue
			}
			cells[i][j] = types.MatrixCell{Value: score, Present: true}
			updateMinMaxCount(score, &seen, &minV, &maxV)
		}
	}

	summary := summaryWithBaseline(scale, seen, minV, maxV)
	layer := composeMatrixOverlayLayer(spec, targetMx, cells, summary)
	return layer, warnings, nil
}

// applyDeltaVsRef is the COMPOSE-host runtime handler for
// OVERLAY_DELTA_VS_REF. Dual-shape dispatcher (E7-S10) — detects the
// host shape from the reference + first target slots and routes into
// either the MATRIX arm (per-cell subtract + emit) or the SERIES arm
// (per-row subtract + emit). Per-coordinate additive `target - ref`.
// Subtraction is total over the reals so there is no zero-denominator
// hazard — the handler never emits PULSE_OVERLAY_REF_ZERO for a zero
// reference. Missing reference rows / cells still emit a warning per
// the PRD §4 missing-key contract.
func applyDeltaVsRef(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []OverlayWarning, error) {
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	if composeHostIsSeries(reference, target) {
		return applyDeltaVsRefSeries(spec, reference, target, refIdx, targetIdx)
	}
	refMx := readMatrix(reference)
	targetMx := readMatrix(target)
	if refMx == nil || targetMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires MATRIX-shape or SERIES-shape slots for reference and target",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	refLookup := buildMatrixCellLookup(refMx)
	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i := 0; i < rowCount; i++ {
		rowKeyStr := axisKeyToString(targetMx.RowKeys[i])
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
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			refVal, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell missing for target (row, col) key pair",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"ref_index":    refIdx,
						"target_index": targetIdx,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"ref_missing":  true,
					},
				})
				continue
			}
			diff := targetVal - refVal
			cells[i][j] = types.MatrixCell{Value: diff, Present: true}
			updateMinMaxCount(diff, &seen, &minV, &maxV)
		}
	}

	summary := summaryWithBaseline(0.0, seen, minV, maxV)
	layer := composeMatrixOverlayLayer(spec, targetMx, cells, summary)
	return layer, warnings, nil
}

// twoProportionZ computes the two-sided p-value of a two-proportion
// z-test against the (successes, n) pairs for two groups. Mirrors the
// pooled-SE formula in processing/test_propz.go:Finalize:
//
//	pa, pb   = sa/na, sb/nb
//	pooled   = (sa + sb) / (na + nb)
//	se       = sqrt(pooled * (1 - pooled) * (1/na + 1/nb))
//	z        = (pa - pb) / se
//	p_value  = 2 * (1 - Φ(|z|))
//
// Returns (NaN, false) when na/nb <= 0, pooled ∈ {0, 1}, or se == 0.
// Reuses standardNormalCDF for the p-value calc so the overlay and
// TEST_PROP_Z produce identical p-values for the same (success, n)
// pair.
func twoProportionZ(sa, na, sb, nb float64) (float64, bool) {
	if na <= 0 || nb <= 0 {
		return math.NaN(), false
	}
	pooled := (sa + sb) / (na + nb)
	if pooled == 0 || pooled == 1 {
		return math.NaN(), false
	}
	se := math.Sqrt(pooled * (1 - pooled) * (1/na + 1/nb))
	if se == 0 {
		return math.NaN(), false
	}
	pa := sa / na
	pb := sb / nb
	z := (pa - pb) / se
	p := 2 * (1 - standardNormalCDF(math.Abs(z)))
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return math.NaN(), false
	}
	return p, true
}

// applyPropZCell is the COMPOSE-host runtime handler for
// OVERLAY_PROP_Z_CELL. Per-cell two-proportion z-test against the
// reference slot's matching cell. Cells are treated as success
// counts; each cell's matching row margin is treated as its sample
// size (target_n on the target side, ref_n on the reference side).
//
// Missing row margins fall back to the cell value as the sample size
// (a structurally degenerate case where p_target == 1 by
// construction — useful for debugging, surfaces NaN+warning rather
// than silently producing a meaningless statistic).
func applyPropZCell(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []OverlayWarning, error) {
	refMx := readMatrix(reference)
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	targetMx := readMatrix(target)
	if refMx == nil || targetMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires MATRIX-shape slots for reference and target",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	refLookup := buildMatrixCellLookup(refMx)
	// Row-margin lookups by row-key (canonical string). Both target +
	// reference matrices participate.
	targetRowMargins := matrixRowMarginLookup(targetMx)
	refRowMargins := matrixRowMarginLookup(refMx)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i := 0; i < rowCount; i++ {
		rowKeyStr := axisKeyToString(targetMx.RowKeys[i])
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
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			refVal, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell missing for target (row, col) key pair",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"ref_missing":  true,
					},
				})
				continue
			}
			nTarget := targetRowMargins[rowKeyStr]
			nRef := refRowMargins[rowKeyStr]
			// Fallback when row margins were not surfaced on the host
			// matrices: use the cell value itself as a degenerate sample
			// size so the handler is still well-defined. The resulting
			// p-value will be 1.0 (p_target == 1) but the gate stays
			// observable.
			if nTarget <= 0 {
				nTarget = targetVal
			}
			if nRef <= 0 {
				nRef = refVal
			}
			p, ok := twoProportionZ(targetVal, nTarget, refVal, nRef)
			if !ok {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " z-statistic undefined (pooled ∈ {0, 1} OR zero SE)",
					Details: map[string]any{
						"kind":          string(spec.Kind),
						"reference":     spec.Reference,
						"target_label":  targetLabel,
						"row_index":     i,
						"col_index":     j,
						"row_key":       rowKeyStr,
						"col_key":       colKeyStr,
						"target_value":  targetVal,
						"target_n":      nTarget,
						"ref_value":     refVal,
						"ref_n":         nRef,
					},
				})
				cells[i][j] = types.MatrixCell{Value: math.NaN(), Present: true}
				continue
			}
			cells[i][j] = types.MatrixCell{Value: p, Present: true}
			updateMinMaxCount(p, &seen, &minV, &maxV)
		}
	}

	// Inferential overlays don't surface a Baseline (per CHISQ_MATRIX
	// pattern); we still publish min / max / count for renderer-side
	// colour-ramp scaling.
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
	layer := composeMatrixOverlayLayer(spec, targetMx, cells, summary)
	return layer, warnings, nil
}

// matrixRowMarginLookup builds a `row-key-string → row-margin-value`
// lookup over a MatrixPayload's RowMargins slot. Used by the per-cell
// inferential overlays (PROP_Z_CELL, T_CELL) to resolve per-row
// sample sizes without re-walking the matrix.
func matrixRowMarginLookup(mx *types.MatrixPayload) map[string]float64 {
	out := map[string]float64{}
	if mx == nil {
		return out
	}
	for i, key := range mx.RowKeys {
		if i >= len(mx.RowMargins) {
			break
		}
		v, ok := scalarFromCell(mx.RowMargins[i])
		if !ok {
			continue
		}
		out[axisKeyToString(key)] = v
	}
	return out
}

// welchTTest computes the two-sided p-value of a Welch t-test on two
// independent (mean, variance, n) triples. Mirrors the Welch-
// Satterthwaite recurrence in processing/test_t.go:Finalize:
//
//	se      = sqrt(va/na + vb/nb)
//	t       = (meanA - meanB) / se
//	df      = (va/na + vb/nb)² / ((va/na)²/(na-1) + (vb/nb)²/(nb-1))
//	p_value = studentTTwoSidedP(t, df)
//
// Returns (NaN, false) when sample sizes are below 2, when se == 0,
// or when the computation produces a non-finite result. Reuses
// studentTTwoSidedP so the overlay and TEST_T produce identical
// p-values for the same (mean, variance, n) triple.
func welchTTest(meanA, varA, nA, meanB, varB, nB float64) (float64, bool) {
	if nA < 2 || nB < 2 {
		return math.NaN(), false
	}
	if varA <= 0 && varB <= 0 {
		return math.NaN(), false
	}
	num := varA/nA + varB/nB
	se := math.Sqrt(num)
	if se == 0 {
		return math.NaN(), false
	}
	t := (meanA - meanB) / se
	den := (varA*varA)/(nA*nA*(nA-1)) + (varB*varB)/(nB*nB*(nB-1))
	if den == 0 {
		return math.NaN(), false
	}
	df := (num * num) / den
	p := studentTTwoSidedP(t, df)
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return math.NaN(), false
	}
	return p, true
}

// applyTCell is the COMPOSE-host runtime handler for OVERLAY_T_CELL.
// Per-cell Welch t-test against the reference slot's matching cell.
// Cells are treated as sample means; variance defaults to 1.0 and
// sample size defaults to 2 (callers override via
// `Params["variance_target"]`, `Params["variance_ref"]`,
// `Params["sample_size_target"]`, `Params["sample_size_ref"]`).
func applyTCell(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []OverlayWarning, error) {
	refMx := readMatrix(reference)
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	targetMx := readMatrix(target)
	if refMx == nil || targetMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires MATRIX-shape slots for reference and target",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	refLookup := buildMatrixCellLookup(refMx)
	varTarget := varianceFromParams(spec.Params, "variance_target", 1.0)
	varRef := varianceFromParams(spec.Params, "variance_ref", 1.0)
	nTarget := sampleSizeFromParams(spec.Params, "sample_size_target", 2.0)
	nRef := sampleSizeFromParams(spec.Params, "sample_size_ref", 2.0)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	targetLabel := composeFirstTargetLabel(spec)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	for i := 0; i < rowCount; i++ {
		rowKeyStr := axisKeyToString(targetMx.RowKeys[i])
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
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			refVal, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " reference cell missing for target (row, col) key pair",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"ref_missing":  true,
					},
				})
				continue
			}
			p, ok := welchTTest(targetVal, varTarget, nTarget, refVal, varRef, nRef)
			if !ok {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " Welch t-test undefined for cell (zero SE OR insufficient n)",
					Details: map[string]any{
						"kind":         string(spec.Kind),
						"reference":    spec.Reference,
						"target_label": targetLabel,
						"row_index":    i,
						"col_index":    j,
						"row_key":      rowKeyStr,
						"col_key":      colKeyStr,
						"target_value": targetVal,
						"ref_value":    refVal,
					},
				})
				cells[i][j] = types.MatrixCell{Value: math.NaN(), Present: true}
				continue
			}
			cells[i][j] = types.MatrixCell{Value: p, Present: true}
			updateMinMaxCount(p, &seen, &minV, &maxV)
		}
	}

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
	layer := composeMatrixOverlayLayer(spec, targetMx, cells, summary)
	return layer, warnings, nil
}

// applyChiSqVsRef is the COMPOSE-host runtime handler for
// OVERLAY_CHISQ_VS_REF. Whole-matrix χ² test against the reference
// slot's matrix: the two matrices are treated as two independent
// contingency tables and the reference distribution (scaled to the
// target's grand total) provides the expected counts.
//
// Math:
//
//	target_N  = Σ target_cells
//	ref_N     = Σ ref_cells
//	scale     = target_N / ref_N
//	expected[i,j] = ref_cell[i,j] * scale
//	chisq     = Σ (target - expected)² / expected   (only where expected > 0)
//	df        = (cells with expected > 0) - 1
//	p_value   = chiSquareSurvival(chisq, df)
//
// SCALAR payload — the layer carries the p-value on
// `OverlayPayload.Scalar` plus `OverlaySummary{Statistic, PValue,
// Parameters{"df"}}`. Reuses the chiSquareSurvival helper backing
// TEST_CHISQ + the MATRIX-host CHISQ family.
func applyChiSqVsRef(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []OverlayWarning, error) {
	refMx := readMatrix(reference)
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	targetMx := readMatrix(target)
	if refMx == nil || targetMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires MATRIX-shape slots for reference and target",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"target_index": targetIdx,
			})
	}

	refLookup := buildMatrixCellLookup(refMx)

	// Walk both matrices to compute target_N + ref_N. The
	// per-coordinate iteration uses the target's RowKeys / ColumnKeys
	// because the key-set gate (E7-S6) guarantees both matrices share
	// the same key set.
	var (
		targetN     float64
		refN        float64
		warnings    []OverlayWarning
		lowExpected int
	)

	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	type cellPair struct {
		targetVal float64
		refVal    float64
	}
	pairs := make([]cellPair, 0, rowCount*colCount)

	for i := 0; i < rowCount; i++ {
		rowKeyStr := axisKeyToString(targetMx.RowKeys[i])
		for j := 0; j < colCount; j++ {
			if i >= len(targetMx.Cells) || j >= len(targetMx.Cells[i]) {
				continue
			}
			cell := targetMx.Cells[i][j]
			if !cell.Present {
				continue
			}
			tv, ok := scalarFromCell(cell)
			if !ok {
				continue
			}
			colKeyStr := axisKeyToString(targetMx.ColumnKeys[j])
			rv, refPresent := refLookup[matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}]
			if !refPresent {
				continue
			}
			targetN += tv
			refN += rv
			pairs = append(pairs, cellPair{targetVal: tv, refVal: rv})
		}
	}

	if targetN == 0 || refN == 0 || len(pairs) == 0 {
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " cannot compute χ² (empty target or reference)",
			Details: map[string]any{
				"kind":      string(spec.Kind),
				"reference": spec.Reference,
				"target_n":  targetN,
				"ref_n":     refN,
			},
		})
		return composeScalarOverlayLayer(spec, nil, &types.OverlaySummary{
			Statistic:  nanPtr(),
			PValue:     nanPtr(),
			Parameters: map[string]float64{"df": math.NaN()},
		}), warnings, nil
	}

	scale := targetN / refN
	var chisq float64
	dfCells := 0
	for _, p := range pairs {
		expected := p.refVal * scale
		if expected <= 0 {
			continue
		}
		if expected < 5 {
			lowExpected++
		}
		diff := p.targetVal - expected
		chisq += (diff * diff) / expected
		dfCells++
	}
	df := float64(dfCells - 1)
	if df < 0 {
		df = 0
	}

	pValue := chiSquareSurvival(chisq, df)
	if lowExpected > 0 {
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_EXPECTED_LOW),
			Message: "overlay " + string(spec.Kind) + " contingency table has cells with expected < 5",
			Details: map[string]any{
				"kind":               string(spec.Kind),
				"reference":          spec.Reference,
				"low_expected_cells": lowExpected,
				"total_cells":        len(pairs),
			},
		})
	}

	stat := chisq
	pv := pValue
	summary := &types.OverlaySummary{
		Statistic:  &stat,
		PValue:     &pv,
		Parameters: map[string]float64{"df": df},
	}
	scalarValue := pValue
	return composeScalarOverlayLayer(spec, &scalarValue, summary), warnings, nil
}

// composeScalarOverlayLayer assembles a SCALAR-shape OverlayLayer.
// Used by OVERLAY_CHISQ_VS_REF which emits a single whole-matrix
// p-value rather than a per-cell payload.
func composeScalarOverlayLayer(spec *types.ComposeOverlaySpec, scalar *float64, summary *types.OverlaySummary) types.OverlayLayer {
	return types.OverlayLayer{
		Name:  composeOverlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeScalar,
			Scalar: scalar,
		},
		Summary: summary,
	}
}

func nanPtr() *float64 {
	v := math.NaN()
	return &v
}

// applyRank is the COMPOSE-host runtime handler for OVERLAY_RANK.
// Per-cell average-tie rank of each target cell within a configurable
// population per `Params["population"]` (`"row" | "column" | "matrix"`;
// defaults to `"matrix"`). Reads only target cells — the reference
// matrix anchors the resolution + key-set gates but does not
// participate in the rank computation itself.
//
// Output cells carry 1-based ranks (1 = largest). Ties take the
// average of their would-be ranks (canonical "average" tie-breaking,
// matches scipy.stats.rankdata default).
func applyRank(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []OverlayWarning, error) {
	_ = reference // RANK does not consume reference values; the slot anchors gates only.
	_ = refIdx
	target, targetIdx := composeFirstTarget(targets, targetIdxs)
	targetMx := readMatrix(target)
	if targetMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a MATRIX-shape target slot",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":         string(spec.Kind),
				"target_index": targetIdx,
			})
	}

	population := populationFromParams(spec.Params)
	rowCount := len(targetMx.RowKeys)
	colCount := len(targetMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)

	switch population {
	case "row":
		for i := 0; i < rowCount; i++ {
			values := collectRowValues(targetMx, i)
			ranks := computeAverageRanks(values)
			writeRowRanks(targetMx, cells, i, ranks, &seen, &minV, &maxV)
		}
	case "column":
		for j := 0; j < colCount; j++ {
			values := collectColumnValues(targetMx, j)
			ranks := computeAverageRanks(values)
			writeColumnRanks(targetMx, cells, j, ranks, &seen, &minV, &maxV)
		}
	default:
		// "matrix" mode (also the fallback for unknown population values).
		if population != "matrix" {
			warnings = append(warnings, OverlayWarning{
				Code:    string(errors.PULSE_OVERLAY_PARAM_MISSING),
				Message: "overlay " + string(spec.Kind) + " unknown population mode; defaulting to \"matrix\"",
				Details: map[string]any{
					"kind":       string(spec.Kind),
					"population": population,
				},
			})
		}
		// Collect every present cell across the whole matrix.
		type coord struct {
			i, j int
		}
		coords := make([]coord, 0, rowCount*colCount)
		values := make([]float64, 0, rowCount*colCount)
		for i := 0; i < rowCount; i++ {
			if i >= len(targetMx.Cells) {
				continue
			}
			for j := 0; j < colCount; j++ {
				if j >= len(targetMx.Cells[i]) {
					continue
				}
				cell := targetMx.Cells[i][j]
				if !cell.Present {
					continue
				}
				v, ok := scalarFromCell(cell)
				if !ok {
					continue
				}
				coords = append(coords, coord{i: i, j: j})
				values = append(values, v)
			}
		}
		ranks := computeAverageRanks(values)
		for k, c := range coords {
			cells[c.i][c.j] = types.MatrixCell{Value: ranks[k], Present: true}
			updateMinMaxCount(ranks[k], &seen, &minV, &maxV)
		}
	}

	summary := summaryWithBaseline(1.0, seen, minV, maxV)
	layer := composeMatrixOverlayLayer(spec, targetMx, cells, summary)
	return layer, warnings, nil
}

// collectRowValues returns the present cell values along row i as a
// dense slice. Used by the "row" population mode of OVERLAY_RANK.
func collectRowValues(mx *types.MatrixPayload, i int) []float64 {
	if i >= len(mx.Cells) {
		return nil
	}
	out := make([]float64, 0, len(mx.Cells[i]))
	for j := range mx.Cells[i] {
		cell := mx.Cells[i][j]
		if !cell.Present {
			out = append(out, math.NaN())
			continue
		}
		v, ok := scalarFromCell(cell)
		if !ok {
			out = append(out, math.NaN())
			continue
		}
		out = append(out, v)
	}
	return out
}

// collectColumnValues returns the present cell values down column j
// as a dense slice. Used by the "column" population mode of
// OVERLAY_RANK.
func collectColumnValues(mx *types.MatrixPayload, j int) []float64 {
	rowCount := len(mx.RowKeys)
	out := make([]float64, 0, rowCount)
	for i := 0; i < rowCount; i++ {
		if i >= len(mx.Cells) || j >= len(mx.Cells[i]) {
			out = append(out, math.NaN())
			continue
		}
		cell := mx.Cells[i][j]
		if !cell.Present {
			out = append(out, math.NaN())
			continue
		}
		v, ok := scalarFromCell(cell)
		if !ok {
			out = append(out, math.NaN())
			continue
		}
		out = append(out, v)
	}
	return out
}

// writeRowRanks writes the per-row rank values back to the overlay
// cells matrix. NaN ranks (absent target cells) leave the overlay
// cell absent.
func writeRowRanks(mx *types.MatrixPayload, cells [][]types.MatrixCell, rowIdx int, ranks []float64, seen *int, minV, maxV *float64) {
	if rowIdx >= len(cells) {
		return
	}
	for j, rank := range ranks {
		if j >= len(cells[rowIdx]) {
			continue
		}
		if math.IsNaN(rank) {
			continue
		}
		if rowIdx >= len(mx.Cells) || j >= len(mx.Cells[rowIdx]) || !mx.Cells[rowIdx][j].Present {
			continue
		}
		cells[rowIdx][j] = types.MatrixCell{Value: rank, Present: true}
		updateMinMaxCount(rank, seen, minV, maxV)
	}
}

// writeColumnRanks writes the per-column rank values back to the
// overlay cells matrix. Mirrors writeRowRanks for the column axis.
func writeColumnRanks(mx *types.MatrixPayload, cells [][]types.MatrixCell, colIdx int, ranks []float64, seen *int, minV, maxV *float64) {
	for i, rank := range ranks {
		if i >= len(cells) || colIdx >= len(cells[i]) {
			continue
		}
		if math.IsNaN(rank) {
			continue
		}
		if i >= len(mx.Cells) || colIdx >= len(mx.Cells[i]) || !mx.Cells[i][colIdx].Present {
			continue
		}
		cells[i][colIdx] = types.MatrixCell{Value: rank, Present: true}
		updateMinMaxCount(rank, seen, minV, maxV)
	}
}

// computeAverageRanks returns the average-tie ranks of `values` —
// rank 1 is the largest value (descending order; the convention the
// PRD calls for is "1 = top performer"). NaN inputs propagate to NaN
// outputs (absent cells stay absent). Ties at the same value receive
// the average of their would-be ranks (canonical "average" tie-
// breaking; matches scipy.stats.rankdata default).
//
// Example: [10, 20, 20, 30] → [4, 2.5, 2.5, 1] (30 is rank 1; the
// two 20s share ranks 2+3 = 2.5; 10 is rank 4).
func computeAverageRanks(values []float64) []float64 {
	n := len(values)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	type idxVal struct {
		idx int
		val float64
	}
	present := make([]idxVal, 0, n)
	for i, v := range values {
		if math.IsNaN(v) {
			out[i] = math.NaN()
			continue
		}
		present = append(present, idxVal{idx: i, val: v})
	}
	// Sort descending so rank 1 = largest. Ties preserve input order
	// (stable sort) for deterministic average-rank assignment.
	sort.SliceStable(present, func(a, b int) bool {
		return present[a].val > present[b].val
	})
	// Walk the sorted list, computing average ranks for tied groups.
	i := 0
	for i < len(present) {
		j := i + 1
		for j < len(present) && present[j].val == present[i].val {
			j++
		}
		// Tie group spans [i, j). Ranks i+1..j (1-based). Average rank
		// = ((i+1) + j) / 2.
		avgRank := float64((i+1)+j) / 2.0
		for k := i; k < j; k++ {
			out[present[k].idx] = avgRank
		}
		i = j
	}
	return out
}
