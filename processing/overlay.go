package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Overlay execution — runtime side of the overlay catalog.
//
// E1 scope (kind-catalog-v1 milestone S5):
//
//   - CrosstabHostView wraps a fully-materialised *types.MatrixPayload
//     and exposes cell + per-axis margin lookups so handlers read from
//     the already-computed matrix and never re-scan records (per PRD §6:
//     "buffered execution — O(cells × layers)").
//   - ApplyOverlays walks the request spec list, dispatches each spec
//     via overlayHandlers, and returns one OverlayLayer per spec in
//     matching order plus a flat warnings slice for cross-cutting
//     diagnostics (today: PULSE_OVERLAY_REF_ZERO when a margin
//     denominator is zero or absent).
//   - applyIndexVsMargin implements OVERLAY_INDEX_VS_MARGIN: every
//     present host cell becomes (cell / margin × 100); zero / missing
//     denominators emit NaN cells plus the warning. The host view's
//     MarginFor(axis, rowIdx, colIdx) returns the matching margin slot
//     for the spec's MarginAxis (ROW → RowMargins[rowIdx], COLUMN →
//     ColumnMargins[colIdx], GRAND → GrandTotal).
//
// Fused-path integration is deferred — E1 only wires the buffered
// crosstab exit; later epics lift handler dispatch into
// crosstab_fused.go.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban.

// OverlayWarning is the in-process diagnostic emitted by an overlay
// handler when it could not produce a meaningful value for some
// subset of cells (zero denominator, missing margin slot). Carries the
// canonical error code (today: errors.PULSE_OVERLAY_REF_ZERO), a
// renderer-friendly message, and structured details so the
// orchestrator can promote each entry into a types.ResponseWarning on
// the envelope.
type OverlayWarning struct {
	// Code is the canonical overlay error code (today: errors.PULSE_OVERLAY_REF_ZERO).
	Code string
	// Message is the human-readable summary.
	Message string
	// Details carries structured context for the warning — overlay
	// index, kind, axis, the offending row / column key indices, etc.
	Details map[string]any
}

// CrosstabHostView is the read-only window an overlay handler uses to
// fold a derived projection over a fully-materialised MatrixPayload.
// Exposing margins through a view (rather than recomputing) is the
// PRD §6 buffered contract: "O(cells × layers) per PRD §6 — handlers
// never re-scan records".
//
// Construction: NewCrosstabHostView(payload). The view holds a pointer
// to the underlying payload; lifetime is the caller's responsibility
// and the view does not mutate the payload.
//
// All margin lookups are byte-equal to the matching slot on the
// underlying MatrixPayload — handlers read what the buffered crosstab
// orchestrator already computed and emitted, never re-deriving the
// math. RowKeys / ColumnKeys / Cells dimensions echo the payload
// verbatim so the handler addresses cells by integer index pairs.
type CrosstabHostView struct {
	// payload is the underlying matrix the host produced. Read-only.
	payload *types.MatrixPayload
}

// NewCrosstabHostView wraps a MatrixPayload as a host view. payload
// must be non-nil; the view does not copy the payload, callers must
// not mutate it during overlay execution.
func NewCrosstabHostView(payload *types.MatrixPayload) *CrosstabHostView {
	return &CrosstabHostView{payload: payload}
}

// Payload returns the underlying MatrixPayload pointer so handlers
// can read RowKeys / ColumnKeys headers when shaping their output
// payload. Read-only — handlers must not mutate the returned payload.
func (h *CrosstabHostView) Payload() *types.MatrixPayload {
	if h == nil {
		return nil
	}
	return h.payload
}

// RowCount returns the number of row tuples on the host. Zero when the
// view is nil or the underlying payload is nil.
func (h *CrosstabHostView) RowCount() int {
	if h == nil || h.payload == nil {
		return 0
	}
	return len(h.payload.RowKeys)
}

// ColumnCount returns the number of column tuples on the host. Zero
// when the view is nil or the underlying payload is nil.
func (h *CrosstabHostView) ColumnCount() int {
	if h == nil || h.payload == nil {
		return 0
	}
	return len(h.payload.ColumnKeys)
}

// CellAt returns the scalar form of the host cell at (rowIdx,
// colIdx) plus a present flag mirroring MatrixCell.Present. Returns
// (0, false) when the indices are out of range or the cell is
// structurally missing. Map-valued cells (RichAggregator outputs)
// fall through as (0, false) because the index-vs-margin handler is
// scalar-only — the validator already rejects map-valued cell
// aggregators paired with overlays that need division (the
// PULSE_CROSSTAB_NORMALIZE_MAP_VALUED gate covers the normalize=row
// case; INDEX_VS_MARGIN inherits the same shape).
func (h *CrosstabHostView) CellAt(rowIdx, colIdx int) (float64, bool) {
	if h == nil || h.payload == nil {
		return 0, false
	}
	if rowIdx < 0 || rowIdx >= len(h.payload.Cells) {
		return 0, false
	}
	row := h.payload.Cells[rowIdx]
	if colIdx < 0 || colIdx >= len(row) {
		return 0, false
	}
	cell := row[colIdx]
	if !cell.Present {
		return 0, false
	}
	v, ok := scalarFromCell(cell)
	return v, ok
}

// MarginFor resolves the margin slot named by axis at the given cell
// coordinates and returns (value, present). RowIdx is ignored when
// axis = column or grand; colIdx is ignored when axis = row or grand.
// An unknown axis returns (0, false); the validator rejects bad axes
// at predict time so this branch should not fire in practice (defense
// in depth per CLAUDE.md "Predict / Inspect contracts").
//
// Layout:
//
//   - MarginAxisRow    → payload.RowMargins[rowIdx]
//   - MarginAxisColumn → payload.ColumnMargins[colIdx]
//   - MarginAxisGrand  → payload.GrandTotal
//
// Missing or absent slots (no margin computed because the crosstab
// spec did not request and did not need them) return (0, false) so
// the handler can surface the matching PULSE_OVERLAY_REF_ZERO
// warning rather than silently divide by zero.
func (h *CrosstabHostView) MarginFor(axis types.MarginAxis, rowIdx, colIdx int) (float64, bool) {
	if h == nil || h.payload == nil {
		return 0, false
	}
	switch axis {
	case types.MarginAxisRow:
		if rowIdx < 0 || rowIdx >= len(h.payload.RowMargins) {
			return 0, false
		}
		cell := h.payload.RowMargins[rowIdx]
		if !cell.Present {
			return 0, false
		}
		return scalarFromCell(cell)
	case types.MarginAxisColumn:
		if colIdx < 0 || colIdx >= len(h.payload.ColumnMargins) {
			return 0, false
		}
		cell := h.payload.ColumnMargins[colIdx]
		if !cell.Present {
			return 0, false
		}
		return scalarFromCell(cell)
	case types.MarginAxisGrand:
		cell := h.payload.GrandTotal
		if !cell.Present {
			return 0, false
		}
		return scalarFromCell(cell)
	}
	return 0, false
}

// scalarFromCell extracts a float64 from a MatrixCell's Value union.
// Returns (0, false) when the cell is non-scalar (map-valued payloads
// from RichAggregator are not addressable as denominators). Mirrors
// types.MatrixCell.Scalar but distinguishes "0 because absent" from
// "0 because the cell is genuinely zero" — the second return tells
// the caller which case fired.
func scalarFromCell(cell types.MatrixCell) (float64, bool) {
	if !cell.Present {
		return 0, false
	}
	switch v := cell.Value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	}
	return 0, false
}

// overlayHandler is the per-kind execution signature. Handlers receive
// the spec the request defined plus the host view, and return a fully
// shaped OverlayLayer (Name + Kind + Scope + Ref + Payload + optional
// Summary) plus a slice of warnings for cells the handler could not
// resolve (zero denominator, missing margin slot). A handler error
// short-circuits ApplyOverlays — used today for unknown-kind dispatch;
// per-kind contracts (axis mismatch, scope unsupported) are caught at
// predict time and should not reach here, but ApplyOverlays still runs
// defensive guards.
type overlayHandler func(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error)

// overlayHandlers is the per-kind dispatch table. The
// TestStreamability_OverlaysKnown / TestUpdateDemandTableCovers
// machinery enforces catalog completeness on the type-system side; this
// table is the runtime mirror — every kind in types.AllOverlayKinds()
// is expected to have a row here.
//
// Adding a new OverlayKind: declare the constant + streamability row
// (types/overlay.go + types/overlay_streamability.go), add the runtime
// handler in this package, and add the dispatch entry here.
var overlayHandlers = map[types.OverlayKind]overlayHandler{
	types.OverlayKindDeltaVsMargin:  applyDeltaVsMargin,
	types.OverlayKindIndexVsMargin:  applyIndexVsMargin,
	types.OverlayKindShareOfCol:     applyShareOfCol,
	types.OverlayKindShareOfRow:     applyShareOfRow,
	types.OverlayKindShareOfTotal:   applyShareOfTotal,
	types.OverlayKindZScoreVsMargin: applyZScoreVsMargin,
}

// ApplyOverlays executes every spec in specs against the host view
// and returns one OverlayLayer per spec in matching order, plus a
// flat warning slice. Returns (nil, nil, nil) when specs is empty.
//
// Per PRD §6: buffered execution — each handler reads the already
// materialised host MatrixPayload, never re-scans records. Cost is
// O(cells × layers) where cells = RowCount × ColumnCount.
//
// Defense in depth: the descriptor.ValidateOverlays gate rejects bad
// kinds at predict time, so a missing dispatch entry should never
// reach the runtime in practice; nonetheless ApplyOverlays guards
// against an unknown kind and returns a CodedError whose details carry
// errors.PULSE_OVERLAY_KIND_UNKNOWN so the orchestrator surfaces the
// same failure mode that predict would have flagged.
//
// host may be nil when specs is empty; ApplyOverlays short-circuits.
// When specs is non-empty but host is nil the call fails fast — every
// E1 overlay family expects a MATRIX-shaped host.
func ApplyOverlays(specs []types.OverlaySpec, host *CrosstabHostView) ([]types.OverlayLayer, []OverlayWarning, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	if host == nil || host.Payload() == nil {
		return nil, nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"ApplyOverlays requires a non-nil CrosstabHostView (no MATRIX host present)")
	}
	layers := make([]types.OverlayLayer, 0, len(specs))
	var warnings []OverlayWarning
	for i := range specs {
		spec := &specs[i]
		handler, ok := overlayHandlers[spec.Kind]
		if !ok {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay kind has no runtime handler: "+string(spec.Kind),
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
					"index": i,
					"kind":  string(spec.Kind),
				})
		}
		layer, ws, err := handler(spec, host)
		if err != nil {
			return nil, nil, err
		}
		layers = append(layers, layer)
		if len(ws) > 0 {
			warnings = append(warnings, ws...)
		}
	}
	return layers, warnings, nil
}

// applyDeltaVsMargin is the OVERLAY_DELTA_VS_MARGIN handler. For every
// present host cell it computes cell - margin where margin is the
// matching axis margin slot (ROW → RowMargins[rowIdx], COLUMN →
// ColumnMargins[colIdx], GRAND → GrandTotal). The output preserves the
// host cell's units — a $-valued cell minus a $-valued margin yields a
// $-valued deviation in the same currency. There is no division and no
// Welford recurrence, so PULSE_OVERLAY_REF_ZERO is never emitted —
// missing host cells stay absent on the overlay (existing null
// contract); missing margins also produce absent overlay cells but
// without an accompanying warning (no division-by-zero risk to flag).
//
// Unlike the SHARE_OF_* triad (each structurally axis-locked),
// DELTA_VS_MARGIN dispatches all three axes — the handler reads
// MarginFor(spec.Ref.Margin.Axis, ...) instead of forcing a fixed axis,
// mirroring the ZSCORE_VS_MARGIN / INDEX_VS_MARGIN pattern.
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery as INDEX_VS_MARGIN.
//
// Summary: Min / Max / Count / Baseline populated. Baseline is 0 — a
// delta of zero means "cell equals margin" (the no-deviation reference)
// and renderers centre diverging colour ramps on that point.
//
// Defense in depth: the descriptor validator rejects bad axes / refs /
// scopes at predict time. This handler still re-checks the Margin
// pointer + axis so a misconfigured caller fails closed.
func applyDeltaVsMargin(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec.Ref.Margin == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Ref.Margin",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	axis := spec.Ref.Margin.Axis

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			cellVal, cellPresent := host.CellAt(i, j)
			if !cellPresent {
				continue
			}
			marginVal, marginPresent := host.MarginFor(axis, i, j)
			if !marginPresent {
				// No division: a missing margin simply produces an absent
				// overlay cell. PULSE_OVERLAY_REF_ZERO does NOT fire —
				// there is no zero-denominator hazard for DELTA_VS_MARGIN.
				continue
			}
			delta := cellVal - marginVal
			if math.IsNaN(delta) || math.IsInf(delta, 0) {
				// Non-finite arithmetic should not be reachable for plain
				// subtraction of finite host values; defense in depth
				// against future host shapes that surface NaN cells.
				continue
			}
			row[j] = types.MatrixCell{Value: delta, Present: true}
			if seen == 0 {
				minV, maxV = delta, delta
			} else {
				if delta < minV {
					minV = delta
				}
				if delta > maxV {
					maxV = delta
				}
			}
			seen++
		}
		cells[i] = row
	}

	overlayPayload := &types.MatrixPayload{
		RowHeader:        payload.RowHeader,
		ColumnHeader:     payload.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
		Cells:            cells,
		CellLabel:        overlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
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

	return layer, nil, nil
}

// applyIndexVsMargin is the OVERLAY_INDEX_VS_MARGIN handler. For every
// present host cell it computes (cell / margin × 100) where margin is
// the matching axis slot (ROW → RowMargins[rowIdx], COLUMN →
// ColumnMargins[colIdx], GRAND → GrandTotal). Missing or zero
// denominators emit NaN cells plus one PULSE_OVERLAY_REF_ZERO warning
// per failing cell so the orchestrator can promote them to envelope
// warnings.
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery. Missing host cells
// (Present=false) stay absent on the overlay; cells where only the
// margin is missing become absent overlay cells plus the warning.
//
// Summary: Min / Max / Count / Baseline populated. Baseline is always
// 100 (the value at which cell == margin); renderers centre diverging
// colour ramps on that point.
//
// Defense in depth: the descriptor validator rejects bad axes / refs /
// scopes at predict time. This handler still re-checks the Margin
// pointer + axis so a misconfigured caller fails closed rather than
// dividing by an unset slot.
func applyIndexVsMargin(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec.Ref.Margin == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Ref.Margin",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	axis := spec.Ref.Margin.Axis

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			cellVal, cellPresent := host.CellAt(i, j)
			if !cellPresent {
				continue
			}
			marginVal, marginPresent := host.MarginFor(axis, i, j)
			if !marginPresent || marginVal == 0 {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " denominator missing or zero on " + string(axis) + " axis",
					Details: map[string]any{
						"kind":       string(spec.Kind),
						"axis":       string(axis),
						"row_index":  i,
						"col_index":  j,
						"margin_set": marginPresent,
					},
				})
				continue
			}
			score := cellVal / marginVal * 100.0
			if math.IsNaN(score) || math.IsInf(score, 0) {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " produced non-finite value on " + string(axis) + " axis",
					Details: map[string]any{
						"kind":      string(spec.Kind),
						"axis":      string(axis),
						"row_index": i,
						"col_index": j,
					},
				})
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
		RowHeader:        payload.RowHeader,
		ColumnHeader:     payload.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
		Cells:            cells,
		CellLabel:        overlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
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

// applyShareOfRow is the OVERLAY_SHARE_OF_ROW handler. For every
// present host cell it computes cell / row_margin (no ×100 scale —
// the layer is a ratio, not an indexed percentage). Missing or zero
// row-margin denominators emit absent overlay cells plus one
// PULSE_OVERLAY_REF_ZERO warning per failing cell so the orchestrator
// can promote them to envelope warnings.
//
// The kind is structurally locked to the ROW axis — unlike
// INDEX_VS_MARGIN (which lets the caller pick row / column / grand),
// SHARE_OF_ROW is row-share-only by definition. Specs always populate
// Ref.Margin, but the handler reads MarginFor(MarginAxisRow, ...)
// regardless of any axis the caller wrote. The validator gate may
// later evolve to reject mismatched axes; today the handler is the
// source of truth.
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery. Missing host cells
// (Present=false) stay absent on the overlay; cells where the row
// margin is missing become absent overlay cells plus the warning.
//
// Summary: Min / Max / Count / Baseline populated. Baseline is always
// 1 / row_count (the value each cell takes when the row distribution
// is uniform), but for simplicity and renderer compatibility we
// surface Baseline = 0 — share ratios cluster near zero on wide rows
// and renderers centre diverging colour ramps on the population
// median rather than the uniform baseline. Future renderer-side
// metadata may refine this.
func applyShareOfRow(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec.Ref.Margin == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Ref.Margin",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	// SHARE_OF_ROW is structurally locked to the ROW axis — the spec
	// must reference an axis, but the handler reads the row margin
	// regardless. Echoing the spec's axis on the layer keeps the
	// response Ref.Margin.Axis faithful to what the caller requested.
	axis := types.MarginAxisRow

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			cellVal, cellPresent := host.CellAt(i, j)
			if !cellPresent {
				continue
			}
			marginVal, marginPresent := host.MarginFor(axis, i, j)
			if !marginPresent || marginVal == 0 {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " denominator missing or zero on row axis",
					Details: map[string]any{
						"kind":       string(spec.Kind),
						"axis":       string(axis),
						"row_index":  i,
						"col_index":  j,
						"margin_set": marginPresent,
					},
				})
				continue
			}
			share := cellVal / marginVal
			if math.IsNaN(share) || math.IsInf(share, 0) {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " produced non-finite value on row axis",
					Details: map[string]any{
						"kind":      string(spec.Kind),
						"axis":      string(axis),
						"row_index": i,
						"col_index": j,
					},
				})
				continue
			}
			row[j] = types.MatrixCell{Value: share, Present: true}
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
		cells[i] = row
	}

	overlayPayload := &types.MatrixPayload{
		RowHeader:        payload.RowHeader,
		ColumnHeader:     payload.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
		Cells:            cells,
		CellLabel:        overlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
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

// applyShareOfCol is the OVERLAY_SHARE_OF_COL handler. For every
// present host cell it computes cell / col_margin (no ×100 scale —
// the layer is a ratio, not an indexed percentage). Missing or zero
// column-margin denominators emit absent overlay cells plus one
// PULSE_OVERLAY_REF_ZERO warning per failing cell so the orchestrator
// can promote them to envelope warnings.
//
// The kind is structurally locked to the COLUMN axis — unlike
// INDEX_VS_MARGIN (which lets the caller pick row / column / grand),
// SHARE_OF_COL is column-share-only by definition. Specs always
// populate Ref.Margin, but the handler reads MarginFor(MarginAxisColumn,
// ...) regardless of any axis the caller wrote. The validator gate
// may later evolve to reject mismatched axes; today the handler is the
// source of truth (matching the E2-S1 SHARE_OF_ROW followup policy).
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery. Missing host cells
// (Present=false) stay absent on the overlay; cells where the column
// margin is missing become absent overlay cells plus the warning.
//
// Summary: Min / Max / Count / Baseline populated. Baseline is set to
// 0 to match the SHARE_OF_ROW convention — share ratios cluster near
// zero on tall columns and renderers centre diverging colour ramps on
// the population median rather than the uniform baseline.
func applyShareOfCol(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec.Ref.Margin == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Ref.Margin",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	// SHARE_OF_COL is structurally locked to the COLUMN axis — the spec
	// must reference an axis, but the handler reads the column margin
	// regardless. Echoing the spec's axis on the layer keeps the
	// response Ref.Margin.Axis faithful to what the caller requested.
	axis := types.MarginAxisColumn

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			cellVal, cellPresent := host.CellAt(i, j)
			if !cellPresent {
				continue
			}
			marginVal, marginPresent := host.MarginFor(axis, i, j)
			if !marginPresent || marginVal == 0 {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " denominator missing or zero on column axis",
					Details: map[string]any{
						"kind":       string(spec.Kind),
						"axis":       string(axis),
						"row_index":  i,
						"col_index":  j,
						"margin_set": marginPresent,
					},
				})
				continue
			}
			share := cellVal / marginVal
			if math.IsNaN(share) || math.IsInf(share, 0) {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " produced non-finite value on column axis",
					Details: map[string]any{
						"kind":      string(spec.Kind),
						"axis":      string(axis),
						"row_index": i,
						"col_index": j,
					},
				})
				continue
			}
			row[j] = types.MatrixCell{Value: share, Present: true}
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
		cells[i] = row
	}

	overlayPayload := &types.MatrixPayload{
		RowHeader:        payload.RowHeader,
		ColumnHeader:     payload.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
		Cells:            cells,
		CellLabel:        overlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
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

// applyShareOfTotal is the OVERLAY_SHARE_OF_TOTAL handler. For every
// present host cell it computes cell / grand_total (no ×100 scale —
// the layer is a ratio, not an indexed percentage). A missing or zero
// grand-total denominator emits absent overlay cells plus one
// PULSE_OVERLAY_REF_ZERO warning per failing cell so the orchestrator
// can promote them to envelope warnings.
//
// The kind is structurally locked to the GRAND axis — unlike
// INDEX_VS_MARGIN (which lets the caller pick row / column / grand),
// SHARE_OF_TOTAL is grand-share-only by definition. Specs always
// populate Ref.Margin, but the handler reads MarginFor(MarginAxisGrand,
// ...) regardless of any axis the caller wrote. The validator gate
// may later evolve to reject mismatched axes; today the handler is the
// source of truth (matching the E2-S1 / E2-S2 followup policy).
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery. Missing host cells
// (Present=false) stay absent on the overlay; cells where the grand
// total is missing or zero become absent overlay cells plus the
// warning.
//
// Summary: Min / Max / Count / Baseline populated. Baseline is set to
// 0 to match the SHARE_OF_ROW / SHARE_OF_COL convention — share
// ratios cluster near zero on populated matrices and renderers centre
// diverging colour ramps on the population median rather than the
// uniform baseline.
//
// NOTE per story description: the same kind name will later route to
// a streamable series-shape handler under Process context (E3); this
// story lands only the MATRIX dispatch.
func applyShareOfTotal(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec.Ref.Margin == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Ref.Margin",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	// SHARE_OF_TOTAL is structurally locked to the GRAND axis — the
	// spec must reference an axis, but the handler reads the grand
	// total regardless. Echoing the spec's axis on the layer keeps the
	// response Ref.Margin.Axis faithful to what the caller requested.
	axis := types.MarginAxisGrand

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			cellVal, cellPresent := host.CellAt(i, j)
			if !cellPresent {
				continue
			}
			// MarginFor with MarginAxisGrand ignores the row / col
			// indices and returns payload.GrandTotal (see MarginFor
			// implementation above); passing i / j here keeps the
			// signature uniform with the row / col handlers.
			marginVal, marginPresent := host.MarginFor(axis, i, j)
			if !marginPresent || marginVal == 0 {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " denominator missing or zero on grand axis",
					Details: map[string]any{
						"kind":       string(spec.Kind),
						"axis":       string(axis),
						"row_index":  i,
						"col_index":  j,
						"margin_set": marginPresent,
					},
				})
				continue
			}
			share := cellVal / marginVal
			if math.IsNaN(share) || math.IsInf(share, 0) {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " produced non-finite value on grand axis",
					Details: map[string]any{
						"kind":      string(spec.Kind),
						"axis":      string(axis),
						"row_index": i,
						"col_index": j,
					},
				})
				continue
			}
			row[j] = types.MatrixCell{Value: share, Present: true}
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
		cells[i] = row
	}

	overlayPayload := &types.MatrixPayload{
		RowHeader:        payload.RowHeader,
		ColumnHeader:     payload.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
		Cells:            cells,
		CellLabel:        overlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
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

// applyZScoreVsMargin is the OVERLAY_ZSCORE_VS_MARGIN handler. For
// every present host cell it computes (cell - margin) / sd where:
//
//   - margin is the matching axis margin slot (ROW → RowMargins[rowIdx],
//     COLUMN → ColumnMargins[colIdx], GRAND → GrandTotal) — the same
//     centerpoint INDEX_VS_MARGIN reads. The handler does NOT
//     recompute the margin; it consumes whatever value the host
//     produced (mean for AGG_MEAN, sum for AGG_SUM, etc).
//   - sd is the population standard deviation of the cell values in
//     the same margin slice (per-row cells for axis=row, per-column
//     cells for axis=column, every matrix cell for axis=grand). Only
//     present cells contribute to the slice — absent host cells do
//     not pollute the variance recurrence. Computed via the shared
//     WelfordStdDev helper (processing/welford.go) using the
//     numerically-stable Welford-Pébaÿ recurrence over central moment
//     M2.
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery as INDEX_VS_MARGIN.
// Missing host cells (Present=false) stay absent on the overlay;
// cells where the margin is missing OR where the slice's sd is zero
// (every cell in the slice was equal — degenerate variance) become
// absent overlay cells plus one PULSE_OVERLAY_REF_ZERO warning per
// failing cell so the orchestrator can promote them to envelope
// warnings.
//
// Unlike the SHARE_OF_* triad (each structurally axis-locked),
// ZSCORE_VS_MARGIN dispatches all three axes — the handler reads
// MarginFor(spec.Ref.Margin.Axis, ...) instead of forcing a fixed
// axis. The validator (descriptor/overlay.go) gates Ref.Margin /
// known axes / scope at predict time.
//
// Summary: Min / Max / Count / Baseline populated. Baseline is 0 —
// a z-score expresses cells in standard-deviation units away from
// the margin centerpoint, so zero is the no-deviation reference and
// renderers centre diverging colour ramps on that point.
//
// Defense in depth: the descriptor validator rejects bad axes / refs
// / scopes at predict time. This handler still re-checks the Margin
// pointer + axis so a misconfigured caller fails closed rather than
// dividing by an unset slot.
func applyZScoreVsMargin(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec.Ref.Margin == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Ref.Margin",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	axis := spec.Ref.Margin.Axis

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	// Pre-compute the per-slice sd values. Axis dispatch:
	//
	//   row    → one sd per row (over present cells in that row).
	//   column → one sd per column (over present cells in that column).
	//   grand  → a single sd over every present cell in the matrix.
	//
	// Slices intentionally exclude absent host cells — a structurally
	// missing observation cannot contribute to a population variance.
	// Slices with fewer than two present cells return sd == 0 (the
	// helper's contract); the per-cell loop treats that the same as a
	// missing-margin condition and surfaces PULSE_OVERLAY_REF_ZERO.
	var (
		rowSDs  []float64
		colSDs  []float64
		grandSD float64
	)
	switch axis {
	case types.MarginAxisRow:
		rowSDs = make([]float64, rowCount)
		for i := 0; i < rowCount; i++ {
			slice := make([]float64, 0, colCount)
			for j := 0; j < colCount; j++ {
				if v, ok := host.CellAt(i, j); ok {
					slice = append(slice, v)
				}
			}
			rowSDs[i] = WelfordStdDev(slice)
		}
	case types.MarginAxisColumn:
		colSDs = make([]float64, colCount)
		for j := 0; j < colCount; j++ {
			slice := make([]float64, 0, rowCount)
			for i := 0; i < rowCount; i++ {
				if v, ok := host.CellAt(i, j); ok {
					slice = append(slice, v)
				}
			}
			colSDs[j] = WelfordStdDev(slice)
		}
	case types.MarginAxisGrand:
		slice := make([]float64, 0, rowCount*colCount)
		for i := 0; i < rowCount; i++ {
			for j := 0; j < colCount; j++ {
				if v, ok := host.CellAt(i, j); ok {
					slice = append(slice, v)
				}
			}
		}
		grandSD = WelfordStdDev(slice)
	}

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []OverlayWarning
		minV     float64
		maxV     float64
		seen     int
	)
	for i := 0; i < rowCount; i++ {
		row := make([]types.MatrixCell, colCount)
		for j := 0; j < colCount; j++ {
			cellVal, cellPresent := host.CellAt(i, j)
			if !cellPresent {
				continue
			}
			marginVal, marginPresent := host.MarginFor(axis, i, j)
			if !marginPresent {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " denominator missing on " + string(axis) + " axis",
					Details: map[string]any{
						"kind":       string(spec.Kind),
						"axis":       string(axis),
						"row_index":  i,
						"col_index":  j,
						"margin_set": marginPresent,
					},
				})
				continue
			}
			var sd float64
			switch axis {
			case types.MarginAxisRow:
				sd = rowSDs[i]
			case types.MarginAxisColumn:
				sd = colSDs[j]
			case types.MarginAxisGrand:
				sd = grandSD
			}
			if sd == 0 {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " standard deviation is zero on " + string(axis) + " axis (degenerate slice)",
					Details: map[string]any{
						"kind":      string(spec.Kind),
						"axis":      string(axis),
						"row_index": i,
						"col_index": j,
						"sd":        sd,
					},
				})
				continue
			}
			score := (cellVal - marginVal) / sd
			if math.IsNaN(score) || math.IsInf(score, 0) {
				warnings = append(warnings, OverlayWarning{
					Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
					Message: "overlay " + string(spec.Kind) + " produced non-finite value on " + string(axis) + " axis",
					Details: map[string]any{
						"kind":      string(spec.Kind),
						"axis":      string(axis),
						"row_index": i,
						"col_index": j,
					},
				})
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
		RowHeader:        payload.RowHeader,
		ColumnHeader:     payload.ColumnHeader,
		RowKeys:          append([]types.AxisKey(nil), payload.RowKeys...),
		ColumnKeys:       append([]types.AxisKey(nil), payload.ColumnKeys...),
		Cells:            cells,
		CellLabel:        overlayLayerName(spec),
		NormalizeApplied: types.CrosstabNormalizeNone,
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
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

// overlayLayerName returns the renderer-facing label for a layer.
// Honours an explicit Name on the spec; otherwise synthesises a
// deterministic default keyed by Kind + axis (the only ref family E1
// consumes). Future kinds extend the synthesis branch.
func overlayLayerName(spec *types.OverlaySpec) string {
	if spec.Name != "" {
		return spec.Name
	}
	switch spec.Kind {
	case types.OverlayKindDeltaVsMargin:
		// DELTA_VS_MARGIN dispatches all three axes; the synthesised
		// default surfaces whichever axis the caller asked for. Falls
		// through to the bare-kind string if Ref.Margin is unset (the
		// validator rejects that shape at predict time but the
		// synthesiser stays defensive). Mirrors INDEX_VS_MARGIN /
		// ZSCORE_VS_MARGIN.
		if spec.Ref.Margin != nil {
			return string(spec.Kind) + "_" + string(spec.Ref.Margin.Axis)
		}
	case types.OverlayKindIndexVsMargin:
		if spec.Ref.Margin != nil {
			return string(spec.Kind) + "_" + string(spec.Ref.Margin.Axis)
		}
	case types.OverlayKindShareOfRow:
		// SHARE_OF_ROW is row-axis-locked; the synthesised default
		// reflects that even if the spec's Ref.Margin.Axis happens to
		// echo a different value.
		return string(spec.Kind) + "_" + string(types.MarginAxisRow)
	case types.OverlayKindShareOfCol:
		// SHARE_OF_COL is column-axis-locked; the synthesised default
		// reflects that even if the spec's Ref.Margin.Axis happens to
		// echo a different value.
		return string(spec.Kind) + "_" + string(types.MarginAxisColumn)
	case types.OverlayKindShareOfTotal:
		// SHARE_OF_TOTAL is grand-axis-locked; the synthesised default
		// reflects that even if the spec's Ref.Margin.Axis happens to
		// echo a different value.
		return string(spec.Kind) + "_" + string(types.MarginAxisGrand)
	case types.OverlayKindZScoreVsMargin:
		// ZSCORE_VS_MARGIN dispatches all three axes; the synthesised
		// default surfaces whichever axis the caller asked for. Falls
		// through to the bare-kind string if Ref.Margin is unset (the
		// validator rejects that shape at predict time but the
		// synthesiser stays defensive).
		if spec.Ref.Margin != nil {
			return string(spec.Kind) + "_" + string(spec.Ref.Margin.Axis)
		}
	}
	return string(spec.Kind)
}
