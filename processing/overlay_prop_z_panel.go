package processing

import (
	"math"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Runtime handler for OVERLAY_PROP_Z_PANEL — the multi-reference
// COMPOSE-host per-cell pairwise two-proportion z-test across N + 1
// slots (the reference slot plus every target slot).
//
// Output shape: each MATRIX cell carries a []float64 (length M*(M-1)/2,
// M = N+1) holding the pairwise p-values in canonical row-major
// upper-triangular order — see pairIndex below. The diagonal (every
// p[i, i] is 1.0 — self-vs-self) is implicit and the lower triangular
// (p[j, i] == p[i, j]) is implicit. The MatrixCell.Value `any` slot
// accepts the slice verbatim — same precedent the RichAggregator family
// uses for map / slice cell values. Documented in
// skills/overlay-system.md.
//
// Cap enforcement: ComposeOverlaySpec.Options.MaxPanelTargets defaults
// to 16 when nil or zero. len(targets) > cap fires
// PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP at handler entry — the
// orchestrator surfaces the offending observed/cap pair so renderers
// can show the cap-vs-observed fix-up immediately. Note the cap counts
// TARGET slots only — the reference is always present and does not
// count against the cap (so an authoring shape with 16 targets remains
// valid against the default 16 cap; 17 targets fail).
//
// Params: types.PanelOverlayParams, carried on
// ComposeOverlaySpec.Params (a map[string]any, decoded through
// types.DecodePanelParamsMap so this host and the per-Request host
// cannot disagree about what a blob means). `n_source` selects each
// slot's sample-size leg; empty means row_margin_n, the payload row
// margin the panel has always read, and the whole default path stays
// byte-identical to the pre-params baseline. Counted modes open a
// per-slot components channel (processing/overlay_compose_slot_view.go)
// and refuse a components-disabled slot with
// PULSE_OVERLAY_COMPONENTS_REQUIRED rather than falling back.

// defaultMaxPanelTargets is the explicit default for
// OverlayOptions.MaxPanelTargets when the slot is zero / unset.
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
// them via JSON unmarshal mismatch and the predict-time gate surfaces
// them; the runtime safe-defaults to 16 if a negative value slips
// through.
func resolveMaxPanelTargets(opts *types.OverlayOptions) int {
	if opts == nil || opts.MaxPanelTargets <= 0 {
		return defaultMaxPanelTargets
	}
	return opts.MaxPanelTargets
}

// matrixAxisIndexLookups builds the (row key string → row index) and
// (column key string → column index) maps for one slot's matrix, using
// the SAME axisKeyToString canonicalisation every other panel lookup
// uses. A duplicate key string keeps its FIRST index, matching the
// first-wins behaviour of buildMatrixCellLookup's map fill.
func matrixAxisIndexLookups(mx *types.MatrixPayload) (rows, cols map[string]int) {
	rows = map[string]int{}
	cols = map[string]int{}
	if mx == nil {
		return rows, cols
	}
	for i, key := range mx.RowKeys {
		s := axisKeyToString(key)
		if _, seen := rows[s]; !seen {
			rows[s] = i
		}
	}
	for j, key := range mx.ColumnKeys {
		s := axisKeyToString(key)
		if _, seen := cols[s]; !seen {
			cols[s] = j
		}
	}
	return rows, cols
}

// panelSlotRequestIndex maps a PANEL index back to the Compose request
// slot index the caller authored, so a diagnostic names the slot the
// caller can find in their own request rather than the panel's
// internal ordering. Returns -1 when the mapping is unavailable.
func panelSlotRequestIndex(panelIdx, refIdx int, targetIdxs []int) int {
	if panelIdx == 0 {
		return refIdx
	}
	if panelIdx-1 < len(targetIdxs) {
		return targetIdxs[panelIdx-1]
	}
	return -1
}

// panelSampleSize resolves ONE slot's sample-size leg at one
// coordinate, per the n_source mode.
//
// The two modes differ in more than where they read. row_margin_n
// always answers (it is a payload read with a value fallback), so the
// legacy path can never skip a coordinate it used to emit;
// cell_n_unweighted answers ok=false when the counted figure was not
// emitted, and the caller skips the coordinate with a warning. See
// types.PanelNSourceFallsBackToCellValue for why the fallback is not
// carried forward.
//
// `slots`, `rowIdxLookups` and `colIdxLookups` are nil for the legacy
// mode and are only touched by the components arm.
func panelSampleSize(
	nSource string,
	slots *ComposeHostView,
	panelIdx int,
	rowIdxLookups, colIdxLookups []map[string]int,
	rowKeyStr, colKeyStr string,
	rowMarginLookups []map[string]float64,
	cellValue float64,
) (float64, bool) {
	switch nSource {
	case types.PanelNSourceCellNUnweighted:
		if panelIdx < 0 || panelIdx >= len(rowIdxLookups) || panelIdx >= len(colIdxLookups) {
			return 0, false
		}
		r, rok := rowIdxLookups[panelIdx][rowKeyStr]
		c, cok := colIdxLookups[panelIdx][colKeyStr]
		if !rok || !cok {
			return 0, false
		}
		n, ok := slots.Slot(panelIdx).CellN(r, c)
		if !ok {
			return 0, false
		}
		return float64(n), true

	case "", types.PanelNSourceRowMarginN:
		nSize := rowMarginLookups[panelIdx][rowKeyStr]
		if nSize <= 0 {
			// Same degenerate fallback OVERLAY_PROP_Z_CELL uses:
			// fall back to the cell value as the sample size so
			// the per-pair gate stays observable (the pooled
			// gate will surface NaN rather than silently
			// producing a meaningless statistic).
			nSize = cellValue
		}
		return nSize, true

	default:
		// Unreachable — applyPropZPanel refuses an unknown n_source at
		// entry. Answering ok=false rather than falling through to the
		// legacy leg keeps a future mode that forgets its case here
		// from silently reporting the default's number under its name.
		return 0, false
	}
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
			errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP,
			"compose overlay "+string(spec.Kind)+" exceeded the per-spec MaxPanelTargets cap",
			map[string]any{
				"kind":     string(spec.Kind),
				"observed": len(targets),
				"cap":      cap,
			})
	}

	// Params, decoded and validated SECOND — after the cap, before any
	// matrix work. Mirrors descriptor/compose.go's gate order (Gate 3 =
	// cap, Gate 3b = params, Gate 4 = the per-slot shape walk), so a
	// spec that is both over-cap and misconfigured reports the same
	// failure on both arms.
	//
	// A runtime TWIN of the predict gate, not a duplicate of it for its
	// own sake: descriptor.ValidateCompose is reached only by predict,
	// and pulse.Compose does not run predict. Without the twin an
	// unknown n_source would fall through to the legacy leg and hand
	// back the default number while the caller believed they had moved
	// the n leg — the silent no-op this family now refuses.
	params, perr := types.DecodePanelParamsMap(spec.Params)
	if perr != nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" has malformed Params: "+perr.Error(),
			map[string]any{"kind": string(spec.Kind)})
	}
	if !types.ValidPanelNSource(params.NSource) {
		valid := types.PanelNSources()
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" has unknown n_source: "+params.NSource+
				" (valid: "+strings.Join(valid, ", ")+")",
			map[string]any{
				"kind":            string(spec.Kind),
				"n_source":        params.NSource,
				"valid_n_sources": valid,
			})
	}

	refMx := readMatrix(reference)
	if refMx == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB,
			"overlay "+string(spec.Kind)+" requires a MATRIX-shape reference slot",
			map[string]any{
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
				errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB,
				"overlay "+string(spec.Kind)+" requires MATRIX-shape target slots",
				map[string]any{
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

	// Components channel, opened only for the modes that read one.
	//
	// The panel-ordered view is built here rather than by the
	// dispatcher because panel index != Compose slot index: the
	// handler receives (reference, targets) already resolved, and
	// prepending the reference reproduces exactly the
	// panel[0]=reference / panel[i]=targets[i-1] convention the rest of
	// this function uses.
	//
	// It stays nil for the legacy leg. That is the whole reason the
	// default path cannot start refusing: a panel that has never
	// needed components must not begin demanding them when a caller
	// spells its existing behaviour out as `n_source: row_margin_n`.
	var slots *ComposeHostView
	var rowIdxLookups, colIdxLookups []map[string]int
	if types.PanelNSourceReadsComponents(params.NSource) {
		slots = NewComposeHostView(append([]*types.Response{reference}, targets...))
		for s := 0; s < m; s++ {
			state := slots.Slot(s).State()
			if state.Available() {
				continue
			}
			// SlotAbsent is STRUCTURAL — no components knob changes a
			// slot that did not resolve — so it keeps the structural
			// code. Answering it with a components diagnostic would
			// tell the caller to turn components on and watch nothing
			// change. Unreachable in practice: the MATRIX gates above
			// already refuse a nil slot. Defense in depth.
			code := errors.PULSE_OVERLAY_COMPONENTS_REQUIRED
			msg := "overlay " + string(spec.Kind) + " n_source " + params.NSource +
				" requires Response.Components.Crosstab on every slot"
			if state == ComposeComponentsSlotAbsent {
				code = errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB
				msg = "overlay " + string(spec.Kind) + " has an unresolved slot"
			}
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(code, msg,
				map[string]any{
					"kind":        string(spec.Kind),
					"n_source":    params.NSource,
					"slot_index":  panelSlotRequestIndex(s, refIdx, targetIdxs),
					"panel_index": s,
					"state":       state.String(),
				})
		}
		// Components are indexed POSITIONALLY per slot, but the panel
		// iterates the REFERENCE matrix's key order and every other
		// read here is by KEY — slots are guaranteed the same key SET,
		// never the same order. Resolving each slot's own coordinate by
		// key is what keeps a components read from silently addressing
		// a different cell than the value read beside it.
		rowIdxLookups = make([]map[string]int, m)
		colIdxLookups = make([]map[string]int, m)
		rowIdxLookups[0], colIdxLookups[0] = matrixAxisIndexLookups(refMx)
		for i, tm := range targetMxs {
			rowIdxLookups[i+1], colIdxLookups[i+1] = matrixAxisIndexLookups(tm)
		}
	}

	// Use the reference matrix's axis keys as the canonical iteration
	// shape — the key-set alignment gate already guaranteed every
	// target carries the same key set.
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
			nMissing := false
			missingSlot := -1
			for s := 0; s < m; s++ {
				v, ok := cellLookups[s][lookupKey]
				if !ok {
					anyMissing = true
					missingSlot = s
					break
				}
				nSize, nok := panelSampleSize(params.NSource, slots, s,
					rowIdxLookups, colIdxLookups, rowKeyStr, colKeyStr,
					rowMarginLookups, v)
				if !nok {
					anyMissing = true
					nMissing = true
					missingSlot = s
					break
				}
				values[s] = v
				ns[s] = nSize
			}
			if anyMissing {
				// One warning shape, two causes, told apart by
				// `n_missing`. A counted mode that cannot read its leg
				// is NOT the same condition as an absent cell value,
				// and a renderer that cannot tell them apart would
				// advise the caller to fix the wrong slot.
				msg := "overlay " + string(spec.Kind) + " absent slot value at coordinate; skipping cell"
				if nMissing {
					msg = "overlay " + string(spec.Kind) + " n_source " + params.NSource +
						" unreadable at coordinate; skipping cell"
				}
				details := map[string]any{
					"kind":         string(spec.Kind),
					"reference":    spec.Reference,
					"target_label": targetLabel,
					"row_index":    i,
					"col_index":    j,
					"row_key":      rowKeyStr,
					"col_key":      colKeyStr,
					"ref_missing":  true,
				}
				if nMissing {
					details["n_missing"] = true
					details["n_source"] = params.NSource
					details["panel_index"] = missingSlot
					details["slot_index"] = panelSlotRequestIndex(missingSlot, refIdx, targetIdxs)
				}
				warnings = append(warnings, types.OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: msg,
					Details: details,
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
