package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E7-S11 runtime handler for OVERLAY_PROP_Z_PANEL — the multi-reference
// COMPOSE-host per-cell pairwise two-proportion z-test across N + 1
// slots (the reference slot plus every target slot).
//
// Output shape decision (per the story's "flattened upper-triangular
// slice with documented ordering"): each MATRIX cell carries a
// []float64 (length M*(M-1)/2, M = N+1) holding the pairwise p-values in
// canonical row-major upper-triangular order — see pairIndex below.
// The diagonal (every p[i, i] is 1.0 — self-vs-self) is implicit and
// the lower triangular (p[j, i] == p[i, j]) is implicit. The
// MatrixCell.Value `any` slot accepts the slice verbatim — same
// precedent the RichAggregator family uses for map / slice cell values.
// Documented in skills/overlay-system.md.
//
// Cap enforcement: ComposeOverlaySpec.Options.MaxPanelTargets defaults
// to 16 when nil or zero (per the interview risk paragraph
// "Multi-reference combinatorics"). len(targets) > cap fires
// PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP at handler entry — the
// orchestrator surfaces the offending observed/cap pair so renderers
// can show the cap-vs-observed fix-up immediately. Note the cap counts
// TARGET slots only — the reference is always present and does not
// count against the cap (so an authoring shape with 16 targets remains
// valid against the default 16 cap; 17 targets fail).

// defaultMaxPanelTargets is the explicit default for
// OverlayOptions.MaxPanelTargets when the slot is zero / unset. Per
// the interview risk paragraph "Multi-reference combinatorics".
const defaultMaxPanelTargets = 16

// pairIndex maps a (i, j) pair with i < j into the flattened
// upper-triangular slice index for an M-slot panel. The flattening is
// row-major (excluding the diagonal):
//
//	row 0:  (0,1), (0,2), ..., (0,M-1)        — (M-1) entries
//	row 1:  (1,2), (1,3), ..., (1,M-1)        — (M-2) entries
//	...
//	row i:  (i,i+1), ..., (i,M-1)             — (M-1-i) entries
//	...
//	row M-2: (M-2,M-1)                        — 1 entry
//
// Cumulative offset to the start of row i is i*(2*M - i - 1) / 2, so:
//
//	pairIndex(i, j, M) = i*(2*M - i - 1)/2 + (j - i - 1)
//
// Total slice length: M*(M-1)/2. Caller MUST ensure 0 <= i < j < M.
func pairIndex(i, j, m int) int {
	return i*(2*m-i-1)/2 + (j - i - 1)
}

// pairCount returns the length of the flattened upper-triangular slice
// for an M-slot panel. Equivalent to M*(M-1)/2.
func pairCount(m int) int {
	if m < 2 {
		return 0
	}
	return m * (m - 1) / 2
}

// resolveMaxPanelTargets returns the effective cap for one
// OVERLAY_PROP_Z_PANEL spec. nil Options OR zero MaxPanelTargets ⇒ 16.
// Negative values are ignored at this layer — the type slot rejects
// them via JSON unmarshal mismatch and the predict-time gate (E7-S14)
// will surface them; the runtime safe-defaults to 16 if a negative
// value slips through.
func resolveMaxPanelTargets(opts *types.OverlayOptions) int {
	if opts == nil || opts.MaxPanelTargets <= 0 {
		return defaultMaxPanelTargets
	}
	return opts.MaxPanelTargets
}

// applyPropZPanel is the COMPOSE-host runtime handler for
// OVERLAY_PROP_Z_PANEL. Multi-reference per-cell pairwise
// two-proportion z-test across the reference slot plus every target
// slot.
//
// Cap check runs at handler entry; cells emit []float64 slices of
// flattened upper-triangular pairwise p-values (length M*(M-1)/2, M =
// 1 + len(targets)). Each pair's math reuses the shared twoProportionZ
// helper so per-pair p-values match OVERLAY_PROP_Z_CELL byte-for-byte
// for the same (success, n) pair.
//
// Slot ordering (panel index → physical slot):
//
//	panel[0] = reference
//	panel[i] = targets[i - 1]  for i in 1..N
//
// The output OverlayLayer's Payload.Matrix mirrors the reference
// matrix's RowKeys / ColumnKeys / headers so renderers lay the overlay
// on top of the reference grid with the same header machinery.
func applyPropZPanel(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []types.OverlayWarning, error) {
	// Cap enforcement runs FIRST so an over-cap panel fails loud
	// before any matrix work happens. The cap counts target slots
	// (reference is always present and does not count).
	cap := resolveMaxPanelTargets(spec.Options)
	if len(targets) > cap {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay "+string(spec.Kind)+" exceeded the per-spec MaxPanelTargets cap",
			map[string]any{
				"code":     string(errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP),
				"kind":     string(spec.Kind),
				"observed": len(targets),
				"cap":      cap,
			})
	}

	refMx := readMatrix(reference)
	if refMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a MATRIX-shape reference slot",
			map[string]any{
				"code":      string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"kind":      string(spec.Kind),
				"ref_index": refIdx,
			})
	}

	// Each target's matrix must be present too. The schema-match gate
	// already rejects non-MATRIX targets via kindRequiresMatrix; this is
	// defense in depth.
	targetMxs := make([]*types.MatrixPayload, len(targets))
	for i, target := range targets {
		mx := readMatrix(target)
		if mx == nil {
			tIdx := -1
			if i < len(targetIdxs) {
				tIdx = targetIdxs[i]
			}
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay "+string(spec.Kind)+" requires MATRIX-shape target slots",
				map[string]any{
					"code":          string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
					"kind":          string(spec.Kind),
					"target_index":  tIdx,
					"target_offset": i,
				})
		}
		targetMxs[i] = mx
	}

	// Build per-slot lookups keyed by canonical (rowKey, colKey) string.
	// panel[0] is the reference; panel[i] (i >= 1) is targets[i - 1].
	m := 1 + len(targets) // slot count
	cellLookups := make([]map[matrixCellLookupKey]float64, m)
	rowMarginLookups := make([]map[string]float64, m)
	cellLookups[0] = buildMatrixCellLookup(refMx)
	rowMarginLookups[0] = matrixRowMarginLookup(refMx)
	for i, tm := range targetMxs {
		cellLookups[i+1] = buildMatrixCellLookup(tm)
		rowMarginLookups[i+1] = matrixRowMarginLookup(tm)
	}

	// Use the reference matrix's axis keys as the canonical iteration
	// shape — the key-set alignment gate (E7-S6) already guaranteed
	// every target carries the same key set.
	rowCount := len(refMx.RowKeys)
	colCount := len(refMx.ColumnKeys)
	cells := buildEmptyMatrixLike(rowCount, colCount)

	totalPairs := pairCount(m)
	targetLabel := composeFirstTargetLabel(spec)
	var warnings []types.OverlayWarning

	for i := 0; i < rowCount; i++ {
		rowKeyStr := axisKeyToString(refMx.RowKeys[i])
		for j := 0; j < colCount; j++ {
			colKeyStr := axisKeyToString(refMx.ColumnKeys[j])
			lookupKey := matrixCellLookupKey{row: rowKeyStr, col: colKeyStr}

			// Pull per-slot (value, n) pairs. A missing slot value at
			// this coordinate makes the whole cell vector absent.
			values := make([]float64, m)
			ns := make([]float64, m)
			anyMissing := false
			for s := 0; s < m; s++ {
				v, ok := cellLookups[s][lookupKey]
				if !ok {
					anyMissing = true
					break
				}
				nSize := rowMarginLookups[s][rowKeyStr]
				if nSize <= 0 {
					// Same degenerate fallback OVERLAY_PROP_Z_CELL uses:
					// fall back to the cell value as the sample size so
					// the per-pair gate stays observable (the pooled
					// gate will surface NaN rather than silently
					// producing a meaningless statistic).
					nSize = v
				}
				values[s] = v
				ns[s] = nSize
			}
			if anyMissing {
				warnings = append(warnings, types.OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " absent slot value at coordinate; skipping cell",
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

			// Allocate the per-cell flattened upper-triangular slice and
			// fold the M*(M-1)/2 pairs.
			pairs := make([]float64, totalPairs)
			for a := 0; a < m; a++ {
				for b := a + 1; b < m; b++ {
					p, ok := twoProportionZ(values[a], ns[a], values[b], ns[b])
					idx := pairIndex(a, b, m)
					if !ok {
						pairs[idx] = math.NaN()
						warnings = append(warnings, types.OverlayWarning{
							Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
							Message: "overlay " + string(spec.Kind) + " z-statistic undefined for slot pair (pooled ∈ {0, 1} OR zero SE)",
							Details: map[string]any{
								"kind":         string(spec.Kind),
								"reference":    spec.Reference,
								"target_label": targetLabel,
								"row_index":    i,
								"col_index":    j,
								"row_key":      rowKeyStr,
								"col_key":      colKeyStr,
								"slot_a":       a,
								"slot_b":       b,
								"value_a":      values[a],
								"value_b":      values[b],
								"n_a":          ns[a],
								"n_b":          ns[b],
							},
						})
						continue
					}
					pairs[idx] = p
				}
			}
			cells[i][j] = types.MatrixCell{Value: pairs, Present: true}
		}
	}

	// Inferential overlays don't surface a Baseline (mirrors
	// PROP_Z_CELL / CHISQ_VS_REF). The summary carries a count of cells
	// for which a pairwise vector was emitted so the renderer can
	// distinguish "no present cells" from "summary not computed".
	summary := &types.OverlaySummary{}
	seen := 0
	for i := range cells {
		for j := range cells[i] {
			if cells[i][j].Present {
				seen++
			}
		}
	}
	count := seen
	summary.Count = &count

	layer := composeMatrixOverlayLayer(spec, refMx, cells, summary)
	return layer, warnings, nil
}
