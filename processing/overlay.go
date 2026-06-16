package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Overlay execution — runtime side of the overlay catalog.
//
// Catalog v1 scope:
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
// Fused-path integration is deferred — the buffered crosstab exit is
// the only overlay dispatch site today; later work lifts handler
// dispatch into crosstab_fused.go.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban.

// types.OverlayWarning is a value-only struct that lives alongside
// OverlayLayer in types/overlay.go so OverlayLayer can carry a
// `Warnings []types.OverlayWarning` field without fighting the
// package dependency graph (types/ does not import processing/). All
// call sites inside processing/ reference types.OverlayWarning
// directly.

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

// RowAxisDepth returns the per-row tuple length (the count of row
// groupers the host crosstab declared). Reads `len(RowKeys[0])` when
// the host has at least one row; zero otherwise. Used by the overlay
// Level / Within handlers to clamp configured depth values into the
// valid `[0, RowAxisDepth())` range via EffectiveAxisDepth (see
// processing/crosstab_normalize.go).
func (h *CrosstabHostView) RowAxisDepth() int {
	if h == nil || h.payload == nil || len(h.payload.RowKeys) == 0 {
		return 0
	}
	return len(h.payload.RowKeys[0])
}

// ColumnAxisDepth returns the per-column tuple length (the count of
// column groupers the host crosstab declared). Reads
// `len(ColumnKeys[0])` when the host has at least one column; zero
// otherwise. Used by the overlay Level / Within handlers to clamp
// configured depth values into the valid `[0, ColumnAxisDepth())`
// range via EffectiveAxisDepth (see processing/crosstab_normalize.go).
func (h *CrosstabHostView) ColumnAxisDepth() int {
	if h == nil || h.payload == nil || len(h.payload.ColumnKeys) == 0 {
		return 0
	}
	return len(h.payload.ColumnKeys[0])
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
type overlayHandler func(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error)

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
	types.OverlayKindChiSqCol:        applyChiSqCol,
	types.OverlayKindChiSqMatrix:     applyChiSqMatrix,
	types.OverlayKindChiSqRow:        applyChiSqRow,
	types.OverlayKindDeltaVsMargin:   applyDeltaVsMargin,
	types.OverlayKindFisherExactCell: applyFisherExactCell,
	types.OverlayKindFormula:         applyFormula,
	types.OverlayKindIndexVsMargin:   applyIndexVsMargin,
	types.OverlayKindShareOfCol:      applyShareOfCol,
	types.OverlayKindShareOfRow:      applyShareOfRow,
	types.OverlayKindShareOfTotal:    applyShareOfTotal,
	types.OverlayKindZScoreVsMargin:  applyZScoreVsMargin,
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
// crosstab overlay family expects a MATRIX-shaped host.
//
// Level / Within belt-and-suspenders gate: before dispatching
// each spec ApplyOverlays runs `validateOverlayLevelWithinRuntime` to
// surface PULSE_OVERLAY_LEVEL_OUT_OF_RANGE for out-of-range Level /
// Within values against the host's RowAxisDepth / ColumnAxisDepth.
// The same condition is caught at predict time
// (descriptor.ValidateOverlays); the runtime gate is defensive in case
// the predict step was bypassed (programmatic Process callers).
//
// Extension visibility: callers that need embedder-registered
// ExprFunctions (OVERLAY_FORMULA) to participate in the per-cell
// expression environment should use `ApplyOverlaysWithExtensions`
// instead — this no-extension entry point preserves backward
// compatibility for in-package tests + non-FORMULA call sites.
func ApplyOverlays(specs []types.OverlaySpec, host *CrosstabHostView) ([]types.OverlayLayer, []types.OverlayWarning, error) {
	return ApplyOverlaysWithExtensions(specs, host, nil)
}

// ApplyOverlaysWithExtensions is the registry-aware sibling of
// ApplyOverlays. Identical contract to ApplyOverlays except that the
// supplied `*ExtensionRegistry` (when non-nil) participates in the
// per-spec FORMULA compile step — embedder-registered ExprFunctions
// become reachable from `OVERLAY_FORMULA` Params["formula"] expressions
// via the same `ExprOptions()` slice that ATTR_FORMULA / FILTER_EXPRESSION
// already consume. Non-FORMULA kinds ignore the registry; the per-kind
// runtime handler signature is intentionally NOT widened so the existing
// 11 handlers stay byte-identical to their pre-FORMULA implementations.
//
// The buffered crosstab exit (`applyOverlaysToResponse`) routes
// every Request-host overlay fold through this entry point and forwards
// the Processor's live `*ExtensionRegistry` so a request that lists
// `OVERLAY_FORMULA` alongside any other kind picks up the registry's
// custom functions at compile time. The nil-registry no-extension
// behaviour is preserved when the caller does not have a registry on
// hand (tests, isolated handler exercises).
func ApplyOverlaysWithExtensions(specs []types.OverlaySpec, host *CrosstabHostView, exts *ExtensionRegistry) ([]types.OverlayLayer, []types.OverlayWarning, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	if host == nil || host.Payload() == nil {
		return nil, nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"ApplyOverlays requires a non-nil CrosstabHostView (no MATRIX host present)")
	}
	layers := make([]types.OverlayLayer, 0, len(specs))
	var warnings []types.OverlayWarning
	for i := range specs {
		spec := &specs[i]
		if err := validateOverlayLevelWithinRuntime(spec, host, i); err != nil {
			return nil, nil, err
		}
		// FORMULA dispatch bypasses the static handler table when the
		// caller supplied a registry — the registry-aware variant
		// threads embedder ExprFunctions into the compile-time
		// `[]expr.Option` slice. Every other kind (and FORMULA with a
		// nil registry) routes through the static dispatch table where
		// every handler shares the (spec, host) signature.
		if spec.Kind == types.OverlayKindFormula {
			layer, ws, err := applyFormulaWithExtensions(spec, host, exts)
			if err != nil {
				return nil, nil, err
			}
			layers = append(layers, layer)
			if len(ws) > 0 {
				warnings = append(warnings, ws...)
			}
			continue
		}
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

// validateOverlayLevelWithinRuntime is the runtime mirror of
// descriptor.ValidateOverlays' Level / Within out-of-range gate. The
// rules:
//
//   - For the share / index / delta / zscore family the handler
//     dispatches the per-axis prefix denominator off Level (same
//     axis the overlay is centerpoint-locked to) and Within (the
//     opposite axis). Level / Within must each be in `[0, axisDepth)`
//     for their respective axis. Out-of-range fires
//     PULSE_OVERLAY_LEVEL_OUT_OF_RANGE with Details carrying the
//     spec index, kind, level, within, and axis depths.
//
//   - For the χ² / Fisher inferential family Level and Within MUST be
//     zero — those handlers compute their own contingency from the
//     host margins and Level / Within would alter the implicit-margin
//     contract. Non-zero values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE.
//
// Zero defaults (Level == 0 && Within == 0) preserve byte-identity
// against the pre-Level/Within overlay handlers — the gate returns nil for
// every existing well-formed spec.
//
// The runtime gate is structurally distinct from the predict gate:
// predict only sees `req.Crosstab` (a CrosstabSpec) while runtime sees
// the materialised host. They agree on the failure code; the runtime
// gate uses host RowAxisDepth / ColumnAxisDepth for the upper bound
// where predict uses len(spec.Rows) / len(spec.Columns).
func validateOverlayLevelWithinRuntime(spec *types.OverlaySpec, host *CrosstabHostView, specIndex int) error {
	if spec == nil {
		return nil
	}
	switch spec.Kind {
	case types.OverlayKindChiSqCol,
		types.OverlayKindChiSqMatrix,
		types.OverlayKindChiSqRow,
		types.OverlayKindFisherExactCell:
		// Inferential family — Level / Within must both be zero. Non-
		// zero values alter the implicit-margin contract those kinds
		// rely on (they compute contingency from the host's row + col
		// margins inline).
		if spec.Level != 0 || spec.Within != 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay "+string(spec.Kind)+" does not support Level / Within (implicit-margin inferential kind)",
				map[string]any{
					"code":   string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
					"index":  specIndex,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return nil
	}
	// Share / index / delta / zscore family — Level and Within must each
	// be in `[0, axisDepth)` for their respective axis. The axis Level
	// truncates is the same axis the overlay is centerpoint-locked to;
	// Within truncates the OPPOSITE axis. Resolve the axis pair off the
	// kind so the gate stays kind-aware (mirrors the handler dispatch).
	levelAxisDepth, withinAxisDepth := overlayLevelWithinAxisDepths(spec, host)
	if spec.Level < 0 || (levelAxisDepth > 0 && spec.Level >= levelAxisDepth) {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Level is out of range for the host axis",
			map[string]any{
				"code":             string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"index":            specIndex,
				"kind":             string(spec.Kind),
				"level":            spec.Level,
				"level_axis_depth": levelAxisDepth,
			})
	}
	if spec.Within < 0 || (withinAxisDepth > 0 && spec.Within >= withinAxisDepth) {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Within is out of range for the host opposite axis",
			map[string]any{
				"code":              string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"index":             specIndex,
				"kind":              string(spec.Kind),
				"within":            spec.Within,
				"within_axis_depth": withinAxisDepth,
			})
	}
	return nil
}

// overlayLevelWithinAxisDepths resolves which host axis the spec's
// Level and Within slots address. Returns
// `(levelAxisDepth, withinAxisDepth)`:
//
//   - SHARE_OF_ROW: Level on ROW axis, Within on COLUMN axis.
//   - SHARE_OF_COL: Level on COLUMN axis, Within on ROW axis.
//   - SHARE_OF_TOTAL: Level / Within both nominally on the grand axis;
//     the helper returns row + column depths so the gate accepts only
//     zero values for both slots (grand axis has no per-cell prefix
//     truncation surface — see overlay_share_of_total_handler comments).
//   - INDEX_VS_MARGIN / DELTA_VS_MARGIN / ZSCORE_VS_MARGIN: axis is
//     driven by Ref.Margin.Axis; Level truncates that axis and Within
//     truncates the opposite one. When Ref.Margin is nil (predict
//     would have rejected) the helper returns (0, 0) so the gate falls
//     through gracefully — the per-kind handler still defends.
func overlayLevelWithinAxisDepths(spec *types.OverlaySpec, host *CrosstabHostView) (levelAxisDepth, withinAxisDepth int) {
	rowDepth := host.RowAxisDepth()
	colDepth := host.ColumnAxisDepth()
	switch spec.Kind {
	case types.OverlayKindShareOfRow:
		return rowDepth, colDepth
	case types.OverlayKindShareOfCol:
		return colDepth, rowDepth
	case types.OverlayKindShareOfTotal:
		// Grand axis has no per-cell prefix surface — the row /
		// column depths are both upper-bounded so non-zero Level /
		// Within fail the gate. SHARE_OF_TOTAL's denominator is the
		// grand total; Level / Within slots are inert for this kind
		// at v1.
		return rowDepth, colDepth
	case types.OverlayKindIndexVsMargin,
		types.OverlayKindDeltaVsMargin,
		types.OverlayKindZScoreVsMargin:
		if spec.Ref.Margin == nil {
			return 0, 0
		}
		switch spec.Ref.Margin.Axis {
		case types.MarginAxisRow:
			return rowDepth, colDepth
		case types.MarginAxisColumn:
			return colDepth, rowDepth
		case types.MarginAxisGrand:
			// Grand axis: same comment as SHARE_OF_TOTAL.
			return rowDepth, colDepth
		}
	}
	return 0, 0
}

// applyChiSqMatrix is the OVERLAY_CHISQ_MATRIX handler. It computes a
// whole-matrix χ² independence test across the host crosstab's row ×
// column contingency table and surfaces the statistic, degrees of
// freedom, and p-value through a SCALAR OverlayPayload + an extended
// OverlaySummary (Statistic / PValue / Parameters{"df"}).
//
// Math:
//
//	expected[r,c] = row_margin[r] * col_margin[c] / grand_total
//	chisq         = Σ_{r,c} (observed[r,c] - expected[r,c])² / expected[r,c]
//	df            = (rows - 1) * (cols - 1)
//	p_value       = chi2_survival(chisq, df)        // = 1 - chi2_cdf
//
// The χ² survival function (chiSquareSurvival) is the same helper that
// backs TEST_CHISQ — overlay and row-test surfaces produce identical
// p-values for the same contingency. The handler does NOT recompute
// margins; it consumes whatever the host crosstab produced (per PRD §6:
// "buffered execution — handlers never re-scan records").
//
// Absent-cell policy: a structurally absent host cell (Present=false)
// is treated as an observed count of 0. The matrix shape stays
// rectangular, the row / column / grand margins continue to drive the
// expected-count recurrence, and an absent observation does not invent
// a count. This is the conservative choice — the alternative ("drop
// the row / column containing the absent cell") would distort margins
// and is reserved for a future explicit-mode kind.
//
// Low-expected-count warning (canonical χ² rule): when any expected
// cell value is below 5 the handler emits one PULSE_OVERLAY_EXPECTED_LOW
// warning carrying the count of low-expected cells and the offending
// minimum. The canonical errors.PULSE_OVERLAY_EXPECTED_LOW constant is
// the source of truth; mirrors
// PULSE_TEST_EXPECTED_COUNT_TOO_LOW on the TEST_CHISQ surface.
//
// Output shape: SCALAR payload with Scalar populated to the χ²
// statistic; Matrix / Series slots stay nil. Layer.Summary carries the
// statistic, p-value, and {df} parameter map plus Min/Max/Count
// describing the input contingency (Count = observed cell count
// including absent-as-zero entries, Min/Max = observed-count extrema).
//
// Degenerate inputs:
//   - Rows < 2 OR Cols < 2: χ² independence requires at least a 2×2
//     contingency; smaller tables emit a stub-coded error via the
//     handler's CodedError return so the orchestrator surfaces the
//     same shape predict would have flagged.
//   - Grand total == 0: every expected cell is undefined; the handler
//     emits the same stub-coded error.
//   - Any expected cell zero (because either the row or column margin
//     is zero): the corresponding observed contribution is dropped
//     (consistent with TEST_CHISQ's "expected > 0" guard) and the
//     low-expected warning still fires.
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// crosstab payload (ApplyOverlays already gates that) and against
// degenerate contingencies.
func applyChiSqMatrix(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	rowCount := host.RowCount()
	colCount := host.ColumnCount()

	// Degenerate-shape guard: χ² independence requires ≥ 2 rows × 2 cols.
	// Mirrors the TEST_CHISQ guard (PULSE_TEST_CONTINGENCY_DEGENERATE)
	// but surfaces a stub-coded shape mismatch on the overlay layer so
	// the orchestrator gets a consistent failure mode.
	if rowCount < 2 || colCount < 2 {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a host contingency of at least 2 rows × 2 columns",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
				"rows": rowCount,
				"cols": colCount,
			})
	}

	// Build the observed contingency from the host cells. Absent cells
	// contribute zero (documented absent-cell policy). Sum into local
	// row / col / grand totals so we are independent of whether the
	// host populated RowMargins / ColumnMargins — the χ² test owns its
	// own margin recurrence to keep the math self-contained.
	observed := make([][]float64, rowCount)
	rowTotals := make([]float64, rowCount)
	colTotals := make([]float64, colCount)
	var grand float64
	var observedCount int
	var observedMin, observedMax float64
	for i := 0; i < rowCount; i++ {
		row := make([]float64, colCount)
		for j := 0; j < colCount; j++ {
			v, present := host.CellAt(i, j)
			if !present {
				// Absent host cell → observed[i][j] = 0; the cell does
				// not contribute to row / col / grand totals (correct
				// for absent-as-zero — a zero observation has count 0).
				continue
			}
			row[j] = v
			rowTotals[i] += v
			colTotals[j] += v
			grand += v
			if observedCount == 0 {
				observedMin, observedMax = v, v
			} else {
				if v < observedMin {
					observedMin = v
				}
				if v > observedMax {
					observedMax = v
				}
			}
			observedCount++
		}
		observed[i] = row
	}

	// Grand-total guard: every expected cell is undefined when N == 0.
	if grand <= 0 {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-zero grand total; got 0",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	// χ² recurrence. Mirrors processing/test_chisq.go Finalize() so the
	// overlay and the row-test surface produce identical statistics for
	// the same contingency. expected > 0 guard matches the same line in
	// test_chisq — when expected is zero (row or column margin is zero)
	// the cell's contribution is dropped; the low-expected warning
	// still fires.
	var stat float64
	expectedMin := math.Inf(1)
	lowExpectedCells := 0
	for i := 0; i < rowCount; i++ {
		for j := 0; j < colCount; j++ {
			expected := rowTotals[i] * colTotals[j] / grand
			if expected < expectedMin {
				expectedMin = expected
			}
			if expected < 5 {
				lowExpectedCells++
			}
			if expected > 0 {
				diff := observed[i][j] - expected
				stat += diff * diff / expected
			}
		}
	}
	df := float64((rowCount - 1) * (colCount - 1))
	pValue := chiSquareSurvival(stat, df)

	// SCALAR payload — Statistic + PValue + Parameters{"df"} ride on
	// the Summary so the renderer can surface the test result without
	// inspecting Payload.Scalar. We still populate Payload.Scalar with
	// the χ² statistic so the SCALAR shape contract holds: every SCALAR
	// overlay carries a meaningful single value in OverlayPayload.Scalar.
	scalar := stat
	df64 := df
	pv := pValue

	var warnings []types.OverlayWarning
	if lowExpectedCells > 0 {
		// Canonical χ² low-expected-count warning. Uses
		// errors.PULSE_OVERLAY_EXPECTED_LOW; mirrors
		// PULSE_TEST_EXPECTED_COUNT_TOO_LOW on the TEST_CHISQ surface.
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_EXPECTED_LOW),
			Message: "overlay " + string(spec.Kind) +
				" χ² approximation may be unreliable: cells with expected count < 5 detected",
			Details: map[string]any{
				"kind":               string(spec.Kind),
				"low_expected_cells": lowExpectedCells,
				"expected_min":       expectedMin,
			},
		})
	}

	summary := &types.OverlaySummary{
		Statistic: &scalar,
		PValue:    &pv,
		Parameters: map[string]float64{
			"df": df64,
		},
	}
	if observedCount > 0 {
		mn, mx, count := observedMin, observedMax, observedCount
		summary.Min = &mn
		summary.Max = &mx
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeScalar,
			Scalar: &scalar,
		},
		Summary: summary,
	}

	return layer, warnings, nil
}

// applyChiSqRow is the OVERLAY_CHISQ_ROW handler. It computes a per-row
// χ² goodness-of-fit test across the host crosstab's column
// distribution and surfaces one (Statistic, df, p-value) triple per
// row tuple via a SERIES OverlayPayload — each SeriesEntry carries an
// OverlaySummary with the same {Statistic, PValue, Parameters{"df"}}
// shape CHISQ_MATRIX uses on the layer-level summary.
//
// Math (per row r):
//
//	observed[c] = host.Cell(r, c)              (absent cell → 0)
//	expected[c] = row_margin[r] * col_margin[c] / grand_total
//	chisq_r    = Σ_c (observed[c] - expected[c])² / expected[c]
//	df          = cols - 1                     (one free parameter per column)
//	p_value     = chiSquareSurvival(chisq_r, df)
//
// The χ² survival helper (chiSquareSurvival) is the same helper that
// backs TEST_CHISQ and CHISQ_MATRIX — per-row tests produce identical
// p-values for the same observed × expected pair. Margins are
// reconstructed from observed cells (absent-as-zero) so the test is
// self-contained: the handler does NOT depend on whether the host
// populated RowMargins / ColumnMargins.
//
// Absent-cell policy: a structurally absent host cell (Present=false)
// is treated as an observed count of 0. Matches the CHISQ_MATRIX
// policy. The matrix shape stays rectangular, the column count never
// collapses, and an absent observation does not invent a count.
//
// Low-expected-count warning: when ANY expected[c] in row r is below 5
// the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW warning per
// offending row (not per cell — the row is the diagnostic unit for
// goodness-of-fit). Canonical errors.PULSE_OVERLAY_EXPECTED_LOW.
//
// SERIES entry order: SeriesPayload.Entries[i].Key == host RowKeys[i]
// element-for-element. The parallel-slice contract is the renderer-
// facing guarantee — entries can be laid alongside the host's row keys
// without re-keying.
//
// Output shape: SERIES payload populated with Entries; Scalar / Matrix
// slots stay nil. Each entry's Summary carries {Statistic, PValue,
// Parameters{"df"}}; no layer-level Summary is populated (the per-row
// statistics ride on the entries, not the layer-level summary slot).
//
// Degenerate inputs:
//   - Cols < 2: per-row χ² requires at least 2 columns (df = cols - 1
//     ≥ 1); smaller matrices fail with a stub-coded shape mismatch.
//   - Rows == 0: SERIES payload comes back with an empty Entries slice
//     (no error — the spec is well-formed; there are simply no rows
//     to test).
//   - Per-row grand total = 0: the row contributes an entry with NaN
//     Statistic and NaN PValue (mirrors the CHISQ_MATRIX
//     degenerate-row policy without short-circuiting the whole layer).
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// crosstab payload (ApplyOverlays already gates that) and against
// degenerate matrix shapes.
func applyChiSqRow(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	rowCount := host.RowCount()
	colCount := host.ColumnCount()

	// Degenerate-shape guard: per-row χ² goodness-of-fit requires ≥ 2
	// columns so df = cols - 1 ≥ 1. Rows == 0 is acceptable (empty
	// Entries slice). Mirrors CHISQ_MATRIX's shape guard but only over
	// the column axis.
	if colCount < 2 {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a host contingency of at least 2 columns",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
				"rows": rowCount,
				"cols": colCount,
			})
	}

	// Build the observed matrix + per-row and per-column totals from
	// host cells (absent-as-zero per documented policy). Owning the
	// margin recurrence keeps the test self-contained — independent of
	// whether the host populated RowMargins / ColumnMargins.
	observed := make([][]float64, rowCount)
	rowTotals := make([]float64, rowCount)
	colTotals := make([]float64, colCount)
	var grand float64
	for i := 0; i < rowCount; i++ {
		row := make([]float64, colCount)
		for j := 0; j < colCount; j++ {
			v, present := host.CellAt(i, j)
			if !present {
				continue
			}
			row[j] = v
			rowTotals[i] += v
			colTotals[j] += v
			grand += v
		}
		observed[i] = row
	}

	payload := host.Payload()
	entries := make([]types.SeriesEntry, 0, rowCount)
	var warnings []types.OverlayWarning
	df := float64(colCount - 1)

	for i := 0; i < rowCount; i++ {
		// Per-row degenerate guards. A row whose total is zero (every
		// observation in the row is absent or zero) cannot produce a
		// meaningful test — surface NaN statistic / p-value but keep
		// the entry slot so the parallel-slice contract holds. Equally
		// when grand == 0 every expected cell is undefined.
		var (
			stat           float64
			rowLowExpected int
			rowExpectedMin = math.Inf(1)
		)
		degenerate := false
		if grand <= 0 || rowTotals[i] <= 0 {
			degenerate = true
		}
		if !degenerate {
			for c := 0; c < colCount; c++ {
				expected := rowTotals[i] * colTotals[c] / grand
				if expected < rowExpectedMin {
					rowExpectedMin = expected
				}
				if expected < 5 {
					rowLowExpected++
				}
				if expected > 0 {
					diff := observed[i][c] - expected
					stat += diff * diff / expected
				}
			}
		}

		// Build the per-row Summary. Mirror CHISQ_MATRIX's shape:
		// Statistic + PValue + Parameters{"df"}. Degenerate rows emit
		// NaN statistic / p-value so the parallel-slice contract holds
		// without short-circuiting the whole layer.
		entrySummary := types.OverlaySummary{
			Parameters: map[string]float64{
				"df": df,
			},
		}
		if degenerate {
			nan := math.NaN()
			entrySummary.Statistic = &nan
			pv := math.NaN()
			entrySummary.PValue = &pv
		} else {
			statCopy := stat
			pValue := chiSquareSurvival(stat, df)
			entrySummary.Statistic = &statCopy
			entrySummary.PValue = &pValue
		}

		// Resolve the row's composite axis-key tuple. payload.RowKeys
		// is the canonical source — entries align element-for-element
		// with RowKeys[i] (the documented parallel-slice contract).
		var key types.AxisKey
		if i < len(payload.RowKeys) {
			// Copy so the response is independent of the host payload
			// (callers may mutate the host matrix during downstream
			// rendering even though the overlay surface is read-only).
			key = append(types.AxisKey(nil), payload.RowKeys[i]...)
		}

		entries = append(entries, types.SeriesEntry{
			Key:     key,
			Summary: entrySummary,
		})

		// One warning per offending row (not per cell — the row is the
		// diagnostic unit for goodness-of-fit). Skip for degenerate
		// rows since the expected-count recurrence did not execute.
		if !degenerate && rowLowExpected > 0 {
			// Canonical χ² low-expected-count warning. Uses
			// errors.PULSE_OVERLAY_EXPECTED_LOW; mirrors
			// PULSE_TEST_EXPECTED_COUNT_TOO_LOW on the TEST_CHISQ
			// surface.
			warnings = append(warnings, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_EXPECTED_LOW),
				Message: "overlay " + string(spec.Kind) +
					" χ² approximation may be unreliable: row contains cells with expected count < 5",
				Details: map[string]any{
					"kind":               string(spec.Kind),
					"row_index":          i,
					"low_expected_cells": rowLowExpected,
					"expected_min":       rowExpectedMin,
				},
			})
		}
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

	return layer, warnings, nil
}

// applyChiSqCol is the OVERLAY_CHISQ_COL handler. Mechanical column-axis
// twin of applyChiSqRow — computes a per-column χ² goodness-of-fit test
// across the host crosstab's row distribution and surfaces one
// (Statistic, df, p-value) triple per column tuple via a SERIES
// OverlayPayload — each SeriesEntry carries an OverlaySummary with the
// same {Statistic, PValue, Parameters{"df"}} shape CHISQ_MATRIX uses on
// the layer-level summary and CHISQ_ROW uses on its per-row entries.
//
// Math (per column c):
//
//	observed[r] = host.Cell(r, c)              (absent cell → 0)
//	expected[r] = row_margin[r] * col_margin[c] / grand_total
//	chisq_c    = Σ_r (observed[r] - expected[r])² / expected[r]
//	df          = rows - 1                     (one free parameter per row)
//	p_value     = chiSquareSurvival(chisq_c, df)
//
// The χ² survival helper (chiSquareSurvival) is the same helper that
// backs TEST_CHISQ, CHISQ_MATRIX, and CHISQ_ROW — per-column tests
// produce identical p-values for the same observed × expected pair.
// Margins are reconstructed from observed cells (absent-as-zero) so the
// test is self-contained: the handler does NOT depend on whether the
// host populated RowMargins / ColumnMargins.
//
// Absent-cell policy: a structurally absent host cell (Present=false)
// is treated as an observed count of 0. Matches the CHISQ_MATRIX /
// CHISQ_ROW policy. The matrix shape stays rectangular, the row count
// never collapses, and an absent observation does not invent a count.
//
// Low-expected-count warning: when ANY expected[r] in column c is below
// 5 the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW warning per
// offending column (not per cell — the column is the diagnostic unit
// for goodness-of-fit). Canonical errors.PULSE_OVERLAY_EXPECTED_LOW.
//
// SERIES entry order: SeriesPayload.Entries[i].Key == host ColumnKeys[i]
// element-for-element. The parallel-slice contract is the renderer-
// facing guarantee — entries can be laid alongside the host's column
// keys without re-keying.
//
// Output shape: SERIES payload populated with Entries; Scalar / Matrix
// slots stay nil. Each entry's Summary carries {Statistic, PValue,
// Parameters{"df"}}; no layer-level Summary is populated (the per-
// column statistics ride on the entries, not the layer-level summary
// slot — matches the CHISQ_ROW per-entry summary policy).
//
// Degenerate inputs:
//   - Rows < 2: per-column χ² requires at least 2 rows (df = rows - 1
//     ≥ 1); smaller matrices fail with a stub-coded shape mismatch.
//   - Cols == 0: SERIES payload comes back with an empty Entries slice
//     (no error — the spec is well-formed; there are simply no columns
//     to test).
//   - Per-column grand total = 0: the column contributes an entry with
//     NaN Statistic and NaN PValue (mirrors the CHISQ_ROW degenerate-
//     row policy without short-circuiting the whole layer).
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// crosstab payload (ApplyOverlays already gates that) and against
// degenerate matrix shapes.
func applyChiSqCol(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	rowCount := host.RowCount()
	colCount := host.ColumnCount()

	// Degenerate-shape guard: per-column χ² goodness-of-fit requires ≥ 2
	// rows so df = rows - 1 ≥ 1. Cols == 0 is acceptable (empty Entries
	// slice). Mirrors CHISQ_ROW's shape guard but only over the row axis.
	if rowCount < 2 {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a host contingency of at least 2 rows",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
				"rows": rowCount,
				"cols": colCount,
			})
	}

	// Build the observed matrix + per-row and per-column totals from
	// host cells (absent-as-zero per documented policy). Owning the
	// margin recurrence keeps the test self-contained — independent of
	// whether the host populated RowMargins / ColumnMargins.
	observed := make([][]float64, rowCount)
	rowTotals := make([]float64, rowCount)
	colTotals := make([]float64, colCount)
	var grand float64
	for i := 0; i < rowCount; i++ {
		row := make([]float64, colCount)
		for j := 0; j < colCount; j++ {
			v, present := host.CellAt(i, j)
			if !present {
				continue
			}
			row[j] = v
			rowTotals[i] += v
			colTotals[j] += v
			grand += v
		}
		observed[i] = row
	}

	payload := host.Payload()
	entries := make([]types.SeriesEntry, 0, colCount)
	var warnings []types.OverlayWarning
	df := float64(rowCount - 1)

	for c := 0; c < colCount; c++ {
		// Per-column degenerate guards. A column whose total is zero
		// (every observation in the column is absent or zero) cannot
		// produce a meaningful test — surface NaN statistic / p-value
		// but keep the entry slot so the parallel-slice contract holds.
		// Equally when grand == 0 every expected cell is undefined.
		var (
			stat           float64
			colLowExpected int
			colExpectedMin = math.Inf(1)
		)
		degenerate := false
		if grand <= 0 || colTotals[c] <= 0 {
			degenerate = true
		}
		if !degenerate {
			for r := 0; r < rowCount; r++ {
				expected := rowTotals[r] * colTotals[c] / grand
				if expected < colExpectedMin {
					colExpectedMin = expected
				}
				if expected < 5 {
					colLowExpected++
				}
				if expected > 0 {
					diff := observed[r][c] - expected
					stat += diff * diff / expected
				}
			}
		}

		// Build the per-column Summary. Mirror CHISQ_ROW's per-entry
		// shape: Statistic + PValue + Parameters{"df"}. Degenerate
		// columns emit NaN statistic / p-value so the parallel-slice
		// contract holds without short-circuiting the whole layer.
		entrySummary := types.OverlaySummary{
			Parameters: map[string]float64{
				"df": df,
			},
		}
		if degenerate {
			nan := math.NaN()
			entrySummary.Statistic = &nan
			pv := math.NaN()
			entrySummary.PValue = &pv
		} else {
			statCopy := stat
			pValue := chiSquareSurvival(stat, df)
			entrySummary.Statistic = &statCopy
			entrySummary.PValue = &pValue
		}

		// Resolve the column's composite axis-key tuple. payload.ColumnKeys
		// is the canonical source — entries align element-for-element
		// with ColumnKeys[i] (the documented parallel-slice contract).
		var key types.AxisKey
		if c < len(payload.ColumnKeys) {
			// Copy so the response is independent of the host payload
			// (callers may mutate the host matrix during downstream
			// rendering even though the overlay surface is read-only).
			key = append(types.AxisKey(nil), payload.ColumnKeys[c]...)
		}

		entries = append(entries, types.SeriesEntry{
			Key:     key,
			Summary: entrySummary,
		})

		// One warning per offending column (not per cell — the column
		// is the diagnostic unit for goodness-of-fit). Skip for
		// degenerate columns since the expected-count recurrence did
		// not execute.
		if !degenerate && colLowExpected > 0 {
			// Canonical χ² low-expected-count warning. Uses
			// errors.PULSE_OVERLAY_EXPECTED_LOW; mirrors
			// PULSE_TEST_EXPECTED_COUNT_TOO_LOW on the TEST_CHISQ
			// surface.
			warnings = append(warnings, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_EXPECTED_LOW),
				Message: "overlay " + string(spec.Kind) +
					" χ² approximation may be unreliable: column contains cells with expected count < 5",
				Details: map[string]any{
					"kind":               string(spec.Kind),
					"col_index":          c,
					"low_expected_cells": colLowExpected,
					"expected_min":       colExpectedMin,
				},
			})
		}
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

	return layer, warnings, nil
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
func applyDeltaVsMargin(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// Level / Within prefix-bucket denominator dispatch (same
	// shape as INDEX_VS_MARGIN — direction is driven by axis).
	buckets, bucketKeys := buildOverlayDenominators(spec, host, overlayDenominatorAxisFor(axis))

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
			marginVal, marginPresent := resolveOverlayDenominator(spec, host, axis, i, j, buckets, bucketKeys)
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

// applyFisherExactCell is the OVERLAY_FISHER_EXACT_CELL handler. For
// every present host cell it builds a 2×2 contingency table from the
// cell value, the row margin, the column margin, and the grand total
// — then computes Fisher's exact two-sided p-value via the shared
// fisherExactTwoSided helper (processing/fisher_exact.go). PRD § 4.C
// FR-C2 calls out this kind as the canonical low-count contingency
// overlay closing the inferential family.
//
// 2×2 layout (for cell at (i, j)):
//
//	           col=j               col≠j
//	row=i      a = cell            b = row_margin - cell
//	row≠i      c = col_margin -    d = grand - row_margin
//	              cell                 - col_margin + cell
//
// The 2×2 cells are non-negative because the buffered crosstab
// orchestrator computes every margin from the same filter-passing row
// set the cells were built from (row_margin >= cell, col_margin >=
// cell, grand >= row_margin + col_margin - cell). The handler still
// clamps each computed cell into the non-negative regime as defense in
// depth — a host that somehow surfaces a row_margin < cell value
// produces a saturated 2×2 (e.g. b = 0) instead of a negative entry.
//
// Output shape: MATRIX payload mirroring the host's RowKeys /
// ColumnKeys / headers so renderers can lay the overlay on top of the
// base matrix with the same header machinery as INDEX_VS_MARGIN. Each
// present host cell becomes a MatrixCell whose Value is the two-sided
// p-value as a float64. Missing host cells stay absent on the overlay;
// cells where either margin slot is missing become absent overlay cells
// (defense in depth — the buffered crosstab orchestrator already
// populates margins before the overlay fold).
//
// Absent-cell policy: a structurally absent host cell (Present=false)
// stays absent on the overlay. Mirrors the INDEX_VS_MARGIN / SHARE_OF_*
// policy.
//
// Ref handling: implicit-margin. The validator rejects any populated
// Ref-family pointer (Margin / Sibling / BaselineIndex / Population /
// Stage / Slot) at predict time with
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, mirroring the CHISQ_*
// implicit-margin contract. The handler reads MarginFor(Row, i, j) and
// MarginFor(Column, i, j) directly from the host view's resolver — no
// caller-side axis pointer needed.
//
// Low expected-cell warning (Cochran rule applied to the 2×2): when
// any of the four expected cells of the 2×2 falls below 1 OR when at
// least 20% (i.e. 1 out of 4 — the threshold is "≥ 20%" so a single
// sub-5 expected cell triggers the warning) of the four expected
// counts fall below 5, the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW
// warning per offending cell carrying the (row_index, col_index) plus
// the offending minimum expected count. Canonical
// errors.PULSE_OVERLAY_EXPECTED_LOW constant.
//
// The threshold runs on the OVERLAY itself (not the underlying χ²
// surface) because Fisher's exact is the SOLUTION to the low-expected-
// count problem — the warning's intent here is to flag the cells where
// Fisher's exact (rather than the cheaper χ² approximation) is
// structurally required. Renderers can use the warning to highlight
// low-count cells whose p-value is exact-but-conservative.
//
// Degenerate inputs:
//   - grand_total <= 0: every 2×2 is undefined; the handler emits
//     absent overlay cells everywhere and surfaces one
//     PULSE_OVERLAY_REF_ZERO warning describing the degenerate host
//     (defense in depth — the buffered crosstab orchestrator should
//     not produce a zero grand total when present cells exist).
//   - row_margin <= 0 OR col_margin <= 0 for some (i, j): the 2×2
//     collapses (a = 0 forces b = 0 OR c = 0 forces d = 0). Fisher's
//     exact still returns p = 1.0 on a fully-degenerate 2×2 so the
//     handler emits the cell with value 1.0 without an accompanying
//     warning (consistent with TEST_FISHER_EXACT's degenerate-table
//     policy at processing/test_fisher.go).
//
// Summary: Min / Max / Count / Baseline populated. Baseline is 0 — a
// p-value of 0 expresses absolute evidence against the null hypothesis,
// p of 1 expresses no evidence at all; renderers centre diverging
// colour ramps on the conventional 0 baseline (significance threshold
// renders as a fixed tick at α, not as the colour-ramp pivot).
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// crosstab payload (ApplyOverlays already gates that) and against
// degenerate margins (grand_total <= 0).
func applyFisherExactCell(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	// Grand total resolution. MarginFor(Grand, ...) ignores the row /
	// col indices and returns payload.GrandTotal. A missing or non-
	// positive grand total degenerates every 2×2 — surface a single
	// PULSE_OVERLAY_REF_ZERO warning and emit an empty overlay (no
	// cells). Mirrors the INDEX_VS_MARGIN missing-margin policy.
	grandTotal, grandPresent := host.MarginFor(types.MarginAxisGrand, 0, 0)
	if !grandPresent || grandTotal <= 0 {
		cells := make([][]types.MatrixCell, rowCount)
		for i := range cells {
			cells[i] = make([]types.MatrixCell, colCount)
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
		zeroCount := 0
		layer.Summary = &types.OverlaySummary{
			Baseline: &baseline,
			Count:    &zeroCount,
		}
		warnings := []types.OverlayWarning{{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " requires a positive grand total; got " + formatFloatForWarning(grandTotal),
			Details: map[string]any{
				"kind":        string(spec.Kind),
				"grand_total": grandTotal,
				"margin_set":  grandPresent,
			},
		}}
		return layer, warnings, nil
	}

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []types.OverlayWarning
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
			rowMarginVal, rowMarginPresent := host.MarginFor(types.MarginAxisRow, i, j)
			if !rowMarginPresent {
				// Defense in depth — the buffered crosstab orchestrator
				// should always populate row margins before the overlay
				// fold runs. An absent margin produces an absent overlay
				// cell, no warning (no division-by-zero hazard here —
				// Fisher's exact is structurally robust to degenerate
				// 2×2 inputs).
				continue
			}
			colMarginVal, colMarginPresent := host.MarginFor(types.MarginAxisColumn, i, j)
			if !colMarginPresent {
				continue
			}

			// Build the 2×2 contingency. The margins above are summed
			// from the same filter-passing rows the host cells were
			// built from, so they dominate the cell value — every 2×2
			// entry is non-negative in the well-formed case. Clamp into
			// the non-negative regime as defense in depth against a
			// host that somehow surfaces row_margin < cell (e.g. a
			// future overlay-aware host that pre-truncates margins).
			//
			// Convert to integer counts via math.Round. The host
			// crosstab is an AGG_COUNT-style integer aggregate when
			// Fisher's exact is meaningful — the validator allows the
			// kind only against MATRIX hosts and the runtime path here
			// rounds defensively so floating-point host values
			// (e.g. AGG_SUM of integer fields) still produce well-
			// formed integer 2×2 cells.
			a := int(math.Round(cellVal))
			b := int(math.Round(rowMarginVal)) - a
			c := int(math.Round(colMarginVal)) - a
			d := int(math.Round(grandTotal)) - int(math.Round(rowMarginVal)) - int(math.Round(colMarginVal)) + a
			if b < 0 {
				b = 0
			}
			if c < 0 {
				c = 0
			}
			if d < 0 {
				d = 0
			}
			if a < 0 {
				a = 0
			}

			p := fisherExactTwoSided(a, b, c, d)
			if math.IsNaN(p) || math.IsInf(p, 0) {
				// Should be unreachable — fisherExactTwoSided clamps
				// into [0, 1] and the lgamma-backed hypergeometric
				// returns finite values for any non-negative integer
				// inputs. Stay defensive so a future numerical edge
				// case surfaces as an absent overlay cell rather than
				// a non-finite renderable.
				continue
			}

			// Cochran low-expected-count rule applied to the 2×2.
			// Expected counts for the 2×2 are the standard outer-
			// product / grand-total formulation. Threshold: ANY
			// expected < 1 OR at least 20% of expected counts < 5
			// (i.e. 1 of 4 — every sub-5 expected cell triggers the
			// rule).
			row1F := float64(a + b)
			row2F := float64(c + d)
			col1F := float64(a + c)
			col2F := float64(b + d)
			grandF := row1F + row2F
			if grandF > 0 {
				expA := row1F * col1F / grandF
				expB := row1F * col2F / grandF
				expC := row2F * col1F / grandF
				expD := row2F * col2F / grandF
				expectedMin := expA
				for _, e := range []float64{expB, expC, expD} {
					if e < expectedMin {
						expectedMin = e
					}
				}
				lowCount := 0
				for _, e := range []float64{expA, expB, expC, expD} {
					if e < 5 {
						lowCount++
					}
				}
				anyBelowOne := expA < 1 || expB < 1 || expC < 1 || expD < 1
				// "at least 20% of expected counts < 5" — with four
				// cells the threshold is 1 of 4 (20%) so a single sub-5
				// expected cell triggers.
				lowFraction := float64(lowCount) / 4.0
				if anyBelowOne || lowFraction >= 0.20 {
					warnings = append(warnings, types.OverlayWarning{
						Code: string(errors.PULSE_OVERLAY_EXPECTED_LOW),
						Message: "overlay " + string(spec.Kind) +
							" Fisher's exact 2×2 contains low expected counts " +
							"(any < 1 OR ≥ 20% < 5); χ² approximation would be unreliable",
						Details: map[string]any{
							"kind":         string(spec.Kind),
							"row_index":    i,
							"col_index":    j,
							"expected_min": expectedMin,
							"low_count":    lowCount,
						},
					})
				}
			}

			row[j] = types.MatrixCell{Value: p, Present: true}
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

// formatFloatForWarning renders a float64 into a no-fmt.Sprintf-safe
// decimal string for warning messages. Mirrors the structural defense
// ban on fmt.Sprintf in JSON-bearing paths (CLAUDE.md "Output Format
// Contract"). strconv-free implementation: integers cast directly,
// other values surface as a NaN / Inf token or the string form of the
// rounded integer. Used by applyFisherExactCell's degenerate-host
// warning where the offending value is most often 0 or a small
// integer; coarse rendering is acceptable because the structured
// Details map carries the exact float64 alongside.
func formatFloatForWarning(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	if math.IsInf(v, -1) {
		return "-Inf"
	}
	// Integer-valued floats render as their integer form so "0" emits
	// "0" rather than "0.000000". Non-integer values fall back to a
	// rounded-integer token; the Details map carries the exact float.
	if v == math.Trunc(v) {
		// Integer rendering via repeated division — no strconv, no fmt.
		return intToString(int64(v))
	}
	return intToString(int64(math.Round(v))) + " (rounded)"
}

// intToString renders an int64 without fmt / strconv (the structural
// defense ban on fmt.Sprintf in JSON-bearing paths covers any
// formatting call site that touches an envelope). Used by
// formatFloatForWarning above; consider lifting to a shared helper if
// other overlay handlers grow similar needs.
func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// resolveOverlayDenominator returns the denominator value for cell
// (rowIdx, colIdx) under the spec's Level / Within configuration. When
// Level / Within are both zero (the default), the legacy MarginFor
// lookup is used and the returned value is byte-identical to the
// legacy handler output (preserving overlay-handler byte-identity).
// When either slot is non-zero, the per-cell denominator
// is sourced from the precomputed prefix-bucket map keyed by the
// effective (rowPrefix, colPrefix) bucket the cell belongs to.
//
// Returns (value, present). A missing margin slot (legacy path) or a
// missing prefix bucket (non-legacy path) returns present=false so the
// caller can surface the PULSE_OVERLAY_REF_ZERO warning consistently
// across both code paths.
//
// axis is the same MarginAxis the handler is centerpoint-locked to
// (e.g. SHARE_OF_ROW always passes types.MarginAxisRow); buckets is
// the prefix-bucket denominator map the caller pre-populated when
// OverlayLevelEnabled(spec) is true (nil otherwise).
func resolveOverlayDenominator(
	spec *types.OverlaySpec,
	host *CrosstabHostView,
	axis types.MarginAxis,
	rowIdx, colIdx int,
	buckets map[overlayPrefixBucketKey]float64,
	bucketKeys map[overlayCellLocator]overlayPrefixBucketKey,
) (float64, bool) {
	if !OverlayLevelEnabled(spec) {
		return host.MarginFor(axis, rowIdx, colIdx)
	}
	key, ok := bucketKeys[overlayCellLocator{rowIdx: rowIdx, colIdx: colIdx}]
	if !ok {
		return 0, false
	}
	v, present := buckets[key]
	if !present {
		return 0, false
	}
	return v, true
}

// overlayDenominatorAxisFor maps a MarginAxis to the direction enum
// the buildOverlayDenominators precompute uses. Used by INDEX /
// DELTA / ZSCORE handlers whose direction is driven by Ref.Margin.Axis
// at runtime (the share triad uses fixed directions because their
// axis is structurally locked).
func overlayDenominatorAxisFor(axis types.MarginAxis) overlayDenominatorAxis {
	switch axis {
	case types.MarginAxisRow:
		return overlayDenominatorAxisRow
	case types.MarginAxisColumn:
		return overlayDenominatorAxisColumn
	case types.MarginAxisGrand:
		return overlayDenominatorAxisGrand
	}
	return overlayDenominatorAxisRow
}

// buildOverlayDenominators precomputes the prefix-bucket denominator
// map for an axis-locked overlay handler honouring the spec's Level /
// Within slots. Returns:
//
//   - buckets: (rowPrefix, colPrefix) → sum of present cells in that
//     bucket, folded according to direction (row / column / grand).
//   - cellKeys: per-cell lookup (rowIdx, colIdx) → bucket key, so the
//     handler dispatch can find each cell's denominator without
//     re-deriving the prefix on every read.
//
// Per PRD § 4.C FR-C3 the function reuses the shared
// processing/crosstab_normalize.go helpers (SameAxisPrefixDepth /
// OppositeAxisPrefixDepth / AxisKeyPrefix) so the math stays parity-
// true with the buffered crosstab `normalize_level` / `normalize_within`
// path against the same host matrix.
//
// direction picks which cells fold into each bucket:
//
//   - overlayDenominatorAxisRow: per-cell denominator = sum of cells
//     sharing (rowPrefix, colPrefix). Used by SHARE_OF_ROW +
//     INDEX/DELTA/ZSCORE(row).
//   - overlayDenominatorAxisColumn: same bucket signature, same fold;
//     callers requesting column-axis dispatch get the same
//     (rowPrefix, colPrefix) → cell-sum buckets — the directional
//     name is purely documentation. The bucket signature is
//     symmetric because cells in the SAME (rowPrefix, colPrefix)
//     bucket form the cross-axis-partitioned denominator regardless of
//     which axis is "same" vs "opposite" from the handler's POV.
//   - overlayDenominatorAxisGrand: rowPrefix and colPrefix are both
//     the empty string — every present cell folds into the single
//     bucket. Same value as the host's GrandTotal.
//
// Returns (nil, nil) when the spec does not engage the prefix-bucket
// path (OverlayLevelEnabled is false) so the caller short-circuits
// the precompute. Out-of-range Level / Within are caught by the
// runtime gate before this function is reached; defensive clamping
// in the shared helpers keeps the function panic-free.
func buildOverlayDenominators(
	spec *types.OverlaySpec,
	host *CrosstabHostView,
	direction overlayDenominatorAxis,
) (
	map[overlayPrefixBucketKey]float64,
	map[overlayCellLocator]overlayPrefixBucketKey,
) {
	if !OverlayLevelEnabled(spec) {
		return nil, nil
	}
	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()
	if payload == nil {
		return nil, nil
	}

	// Resolve the prefix depths for SAME and OPPOSITE axes. Direction
	// picks which axis is which:
	//
	//   row direction    → same=ROW, opposite=COLUMN
	//   column direction → same=COLUMN, opposite=ROW
	//   grand direction  → no SAME/OPPOSITE split; both axes are
	//                      effectively un-bucketed (prefix length 0).
	var (
		rowPrefixLen int
		colPrefixLen int
	)
	rowDepth := host.RowAxisDepth()
	colDepth := host.ColumnAxisDepth()
	switch direction {
	case overlayDenominatorAxisRow:
		rowPrefixLen = SameAxisPrefixDepth(rowDepth, spec.Level)
		colPrefixLen = OppositeAxisPrefixDepth(colDepth, spec.Within)
	case overlayDenominatorAxisColumn:
		colPrefixLen = SameAxisPrefixDepth(colDepth, spec.Level)
		rowPrefixLen = OppositeAxisPrefixDepth(rowDepth, spec.Within)
	case overlayDenominatorAxisGrand:
		// Grand direction folds across every cell — no prefix
		// discrimination. Both prefix lengths stay zero so every
		// cell shares the empty-prefix bucket.
		rowPrefixLen = 0
		colPrefixLen = 0
	}

	buckets := make(map[overlayPrefixBucketKey]float64, rowCount*colCount)
	cellKeys := make(map[overlayCellLocator]overlayPrefixBucketKey, rowCount*colCount)

	for i := 0; i < rowCount; i++ {
		var rowPrefix string
		if rowPrefixLen > 0 && i < len(payload.RowKeys) {
			rowPrefix = AxisKeyPrefix(payload.RowKeys[i], rowPrefixLen-1)
		}
		for j := 0; j < colCount; j++ {
			var colPrefix string
			if colPrefixLen > 0 && j < len(payload.ColumnKeys) {
				colPrefix = AxisKeyPrefix(payload.ColumnKeys[j], colPrefixLen-1)
			}
			loc := overlayCellLocator{rowIdx: i, colIdx: j}
			bucketKey := overlayPrefixBucketKey{row: rowPrefix, col: colPrefix}
			cellKeys[loc] = bucketKey
			// Only present cells contribute to the bucket sum (mirrors
			// the buffered crosstab path: absent host cells contribute
			// nothing to the normalisation denominator).
			if v, ok := host.CellAt(i, j); ok {
				buckets[bucketKey] += v
			}
		}
	}

	// Direction filtering: when overlayDenominatorAxisRow the
	// denominator for cell (i, j) should fold ACROSS COLUMNS for fixed
	// rowPrefix. Folding across the full (rowPrefix, colPrefix) bucket
	// (as the loop above does) is correct when colPrefixLen > 0 (a
	// non-zero Within slot fixes the column prefix). When Within == 0
	// (colPrefixLen == 0) every cell shares colPrefix="" so the
	// bucket sum spans every column in the rowPrefix — which is
	// exactly the legacy row-margin behavior recovered through the
	// prefix-bucket path. Symmetric for overlayDenominatorAxisColumn.
	//
	// No additional filtering required.
	return buckets, cellKeys
}

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
func applyIndexVsMargin(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// Level / Within prefix-bucket denominator dispatch. The
	// direction is driven by Ref.Margin.Axis:
	//   row    → fold across columns within (rowPrefix, colPrefix)
	//   column → fold across rows within (rowPrefix, colPrefix)
	//   grand  → fold across every cell (Level / Within inert)
	// See applyShareOfRow for the zero-default byte-identity contract.
	buckets, bucketKeys := buildOverlayDenominators(spec, host, overlayDenominatorAxisFor(axis))

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []types.OverlayWarning
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
			marginVal, marginPresent := resolveOverlayDenominator(spec, host, axis, i, j, buckets, bucketKeys)
			if !marginPresent || marginVal == 0 {
				warnings = append(warnings, types.OverlayWarning{
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
				warnings = append(warnings, types.OverlayWarning{
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
func applyShareOfRow(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// When the spec engages the Level / Within prefix-axis denominator
	// path (either slot non-zero), precompute the per-cell (rowPrefix,
	// colPrefix) bucket map so the per-cell loop dispatches through
	// the prefix-bucket denominator instead of the legacy MarginFor
	// lookup. The zero-default code path keeps using the legacy
	// MarginFor lookup — byte-identical to the legacy handler output
	// (resolveOverlayDenominator short-circuits when
	// OverlayLevelEnabled returns false).
	buckets, bucketKeys := buildOverlayDenominators(spec, host, overlayDenominatorAxisRow)

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []types.OverlayWarning
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
			marginVal, marginPresent := resolveOverlayDenominator(spec, host, axis, i, j, buckets, bucketKeys)
			if !marginPresent || marginVal == 0 {
				warnings = append(warnings, types.OverlayWarning{
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
				warnings = append(warnings, types.OverlayWarning{
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
// source of truth (matching the SHARE_OF_ROW followup policy).
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
func applyShareOfCol(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// Level / Within prefix-bucket denominator dispatch (see
	// applyShareOfRow for the documented zero-default byte-identity
	// contract).
	buckets, bucketKeys := buildOverlayDenominators(spec, host, overlayDenominatorAxisColumn)

	rowCount := host.RowCount()
	colCount := host.ColumnCount()
	payload := host.Payload()

	cells := make([][]types.MatrixCell, rowCount)
	var (
		warnings []types.OverlayWarning
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
			marginVal, marginPresent := resolveOverlayDenominator(spec, host, axis, i, j, buckets, bucketKeys)
			if !marginPresent || marginVal == 0 {
				warnings = append(warnings, types.OverlayWarning{
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
				warnings = append(warnings, types.OverlayWarning{
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
// source of truth (matching the SHARE_OF_ROW / SHARE_OF_COL followup policy).
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
// NOTE: the same kind name also routes to a streamable series-shape
// handler under Process context; this site is the MATRIX dispatch
// only.
func applyShareOfTotal(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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
		warnings []types.OverlayWarning
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
				warnings = append(warnings, types.OverlayWarning{
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
				warnings = append(warnings, types.OverlayWarning{
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
func applyZScoreVsMargin(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
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

	// Level / Within prefix-bucket denominator dispatch — only
	// the margin centroid (cell - margin numerator) honours the prefix-
	// bucket path; the SD denominator continues to compute over the
	// full per-axis slice. The behaviour is intentional — the
	// statistical interpretation of a Z-score requires a stable slice
	// SD that does not shift when Level / Within engage, and the
	// prefix-bucket variance recurrence is reserved for future kinds.
	// Zero-default Level=0/Within=0 stays byte-identical: the
	// resolveOverlayDenominator short-circuits when
	// OverlayLevelEnabled is false.
	buckets, bucketKeys := buildOverlayDenominators(spec, host, overlayDenominatorAxisFor(axis))

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
		warnings []types.OverlayWarning
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
			marginVal, marginPresent := resolveOverlayDenominator(spec, host, axis, i, j, buckets, bucketKeys)
			if !marginPresent {
				warnings = append(warnings, types.OverlayWarning{
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
				warnings = append(warnings, types.OverlayWarning{
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
				warnings = append(warnings, types.OverlayWarning{
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
// deterministic default keyed by Kind + axis. Future kinds extend the
// synthesis branch.
func overlayLayerName(spec *types.OverlaySpec) string {
	if spec.Name != "" {
		return spec.Name
	}
	switch spec.Kind {
	case types.OverlayKindChiSqCol:
		// CHISQ_COL is a COLUMN-scoped overlay with no axis dispatch
		// (the column axis is structurally fixed); synthesised default
		// mirrors CHISQ_MATRIX / CHISQ_ROW's lower-case bare-kind shape.
		return "chisq_col"
	case types.OverlayKindChiSqMatrix:
		// CHISQ_MATRIX is a whole-matrix overlay with no axis
		// dispatch; the synthesised default is the bare kind string
		// in lower-case "chisq_matrix" shape so renderers can use it
		// directly as a tab / legend label without uppercasing.
		return "chisq_matrix"
	case types.OverlayKindChiSqRow:
		// CHISQ_ROW is a ROW-scoped overlay with no axis dispatch (the
		// row axis is structurally fixed); synthesised default mirrors
		// CHISQ_MATRIX's lower-case bare-kind shape.
		return "chisq_row"
	case types.OverlayKindDeltaVsBaseline:
		// DELTA_VS_BASELINE is the windowed positional-baseline absolute-
		// difference kind; the synthesised default surfaces the lower-case
		// bare-kind string "delta_vs_baseline" matching the INDEX_VS_BASELINE
		// / INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES / INDEX_VS_PRIOR
		// convention. The synthesiser intentionally does NOT echo the
		// baseline ordinal in the default name — renderers that need the
		// disambiguation populate spec.Name explicitly.
		return "delta_vs_baseline"
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
	case types.OverlayKindDeltaVsSibling:
		// DELTA_VS_SIBLING resolves a sibling via Ref.Sibling.{Field,
		// Value}; the synthesised default surfaces the bare lower-case
		// kind string "delta_vs_sibling" so renderers can use it
		// directly as a tab / legend label without uppercasing
		// (mirrors INDEX_VS_TOTAL / ZSCORE_VS_TOTAL — the sibling-
		// reference kinds intentionally do NOT echo the specific
		// (field, value) pair in the default name; renderers that
		// need the disambiguation populate spec.Name explicitly).
		return "delta_vs_sibling"
	case types.OverlayKindFisherExactCell:
		// FISHER_EXACT_CELL is implicit-margin (no axis dispatch — the
		// per-cell 2×2 always reads row + col margins from the host);
		// synthesised default mirrors the CHISQ_* family's lower-case
		// bare-kind shape so renderers can use it directly as a tab /
		// legend label without uppercasing.
		return "fisher_exact_cell"
	case types.OverlayKindIndexVsBaseline:
		// INDEX_VS_BASELINE is the windowed positional-baseline kind; the
		// synthesised default surfaces the lower-case bare-kind string
		// "index_vs_baseline" matching the INDEX_VS_TOTAL / SHARE_OF_TOTAL
		// SERIES / INDEX_VS_PRIOR convention. The synthesiser intentionally
		// does NOT echo the baseline ordinal in the default name — renderers
		// that need the disambiguation populate spec.Name explicitly.
		return "index_vs_baseline"
	case types.OverlayKindIndexVsMargin:
		if spec.Ref.Margin != nil {
			return string(spec.Kind) + "_" + string(spec.Ref.Margin.Axis)
		}
	case types.OverlayKindIndexVsPop:
		// INDEX_VS_POP is the FACET-host population-comparison kind;
		// synthesised default surfaces the lower-case bare-kind
		// string "index_vs_pop" matching the INDEX_VS_TOTAL / INDEX_VS_PRIOR /
		// INDEX_VS_BASELINE / INDEX_VS_ROLLING_MEAN convention (no axis
		// dispatch — the Facet field is fixed by the host shape).
		return "index_vs_pop"
	case types.OverlayKindIndexVsPrior:
		// INDEX_VS_PRIOR is the windowed lag-1 kind; synthesised default
		// surfaces the lower-case bare-kind string "index_vs_prior"
		// matching the INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES dispatch
		// convention (no axis dispatch — the ordered host axis is fixed).
		return "index_vs_prior"
	case types.OverlayKindIndexVsRollingMean:
		// INDEX_VS_ROLLING_MEAN is the windowed rolling-mean kind; the
		// synthesised default surfaces the lower-case bare-kind string
		// "index_vs_rolling_mean" matching the INDEX_VS_PRIOR /
		// INDEX_VS_BASELINE / INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES
		// convention (no axis dispatch — the ordered host axis is fixed).
		// The synthesiser intentionally does NOT echo the window width
		// in the default name — renderers that need the disambiguation
		// populate spec.Name explicitly.
		return "index_vs_rolling_mean"
	case types.OverlayKindIndexVsSibling:
		// INDEX_VS_SIBLING resolves a sibling via Ref.Sibling.{Field,
		// Value}; the synthesised default surfaces the bare lower-case
		// kind string "index_vs_sibling" so renderers can use it
		// directly as a tab / legend label without uppercasing
		// (mirrors DELTA_VS_SIBLING).
		return "index_vs_sibling"
	case types.OverlayKindIndexVsTotal:
		// INDEX_VS_TOTAL is implicit-grand-total (no Ref family); the
		// synthesised default is the bare kind string in lower-case so
		// renderers can use it directly as a tab / legend label without
		// uppercasing (mirrors the CHISQ_* / FISHER_EXACT_CELL family).
		return "index_vs_total"
	case types.OverlayKindKSVsPop:
		// KS_VS_POP is the FACET-host inferential numeric-arm
		// kind; synthesised default surfaces the lower-case bare-kind
		// string "ks_vs_pop" matching the INDEX_VS_POP / ZSCORE_VS_POP /
		// CHISQ_VS_POP convention (no axis dispatch — the Facet field is
		// fixed by the host shape).
		return "ks_vs_pop"
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
		// SHARE_OF_TOTAL dispatches both shapes: MATRIX (crosstab host,
		// grand-axis-locked, Ref.Margin populated) and SERIES (grouped
		// Process host, implicit-grand-total, Ref empty). The
		// synthesised default reflects the host shape — when Ref.Margin
		// is populated the MATRIX default surfaces
		// "OVERLAY_SHARE_OF_TOTAL_grand" (echoing the grand-axis lock
		// even if the caller passed a different axis); when Ref is empty
		// the SERIES default surfaces "share_of_total" matching the
		// INDEX_VS_TOTAL / CHISQ_* / FISHER_EXACT_CELL lower-case bare-
		// kind convention.
		if spec.Ref.Margin != nil {
			return string(spec.Kind) + "_" + string(types.MarginAxisGrand)
		}
		return "share_of_total"
	case types.OverlayKindZScoreVsMargin:
		// ZSCORE_VS_MARGIN dispatches all three axes; the synthesised
		// default surfaces whichever axis the caller asked for. Falls
		// through to the bare-kind string if Ref.Margin is unset (the
		// validator rejects that shape at predict time but the
		// synthesiser stays defensive).
		if spec.Ref.Margin != nil {
			return string(spec.Kind) + "_" + string(spec.Ref.Margin.Axis)
		}
	case types.OverlayKindYoY:
		// OVERLAY_YOY is the windowed year-over-year kind; synthesised
		// default surfaces the lower-case bare-kind string "yoy" matching
		// the INDEX_VS_ROLLING_MEAN / INDEX_VS_PRIOR / INDEX_VS_BASELINE
		// convention (no axis dispatch — the ordered host axis is fixed).
		// The synthesiser intentionally does NOT echo the frequency in
		// the default name — renderers that need the disambiguation
		// populate spec.Name explicitly.
		return "yoy"
	case types.OverlayKindZScoreVsPop:
		// ZSCORE_VS_POP is the FACET-host population-comparison
		// z-score kind; synthesised default surfaces the lower-case bare-
		// kind string "zscore_vs_pop" matching the INDEX_VS_POP / INDEX_VS_TOTAL /
		// ZSCORE_VS_TOTAL convention (no axis dispatch — the Facet field is
		// fixed by the host shape).
		return "zscore_vs_pop"
	case types.OverlayKindZScoreVsRolling:
		// ZSCORE_VS_ROLLING is the windowed rolling sample-SD z-score
		// kind; synthesised default surfaces the lower-case bare-kind
		// string "zscore_vs_rolling" matching the INDEX_VS_ROLLING_MEAN /
		// INDEX_VS_PRIOR / ZSCORE_VS_TOTAL convention (no axis dispatch —
		// the ordered host axis is fixed). The synthesiser intentionally
		// does NOT echo the window width in the default name — renderers
		// that need the disambiguation populate spec.Name explicitly.
		return "zscore_vs_rolling"
	case types.OverlayKindZScoreVsTotal:
		// ZSCORE_VS_TOTAL is implicit-grand-total (no Ref family); the
		// synthesised default is the bare kind string in lower-case so
		// renderers can use it directly as a tab / legend label without
		// uppercasing (mirrors the INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES
		// dispatch convention).
		return "zscore_vs_total"
	}
	return string(spec.Kind)
}
