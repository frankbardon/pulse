package types

import "encoding/json"

// Overlay system — universal foundational types.
//
// The overlay layer is an additive, request-driven family of derived
// computations that decorate a primary result (today: crosstab matrices;
// future: regressions, time series, group results) with one or more
// secondary projections — index-vs-margin scores, sibling comparisons,
// baseline lifts, population deltas, etc. Every overlay shares one
// declarative surface (OverlaySpec) and one structured response surface
// (OverlayLayer). Downstream renderers can lay an overlay on top of a
// base result without re-deriving the projection.
//
// File scope (E1-S1):
//   - Universal kind/shape/scope enums + the OverlayRef discriminated
//     union (E1-S1).
//   - Request-side OverlaySpec (E1-S1) and response-side OverlayLayer
//     wrapper (E1-S1).
//   - OverlayPayload scalar/series/matrix union + minimal SeriesPayload
//     placeholder (E1-S1; SeriesPayload may grow as future families
//     surface time-series overlays).
//
// Subsequent stories layer descriptor validation (E1-S2), processing
// dispatch + INDEX_VS_MARGIN math (E1-S3), MCP schema bindings (E1-S7),
// canonical-hash extension (E1-S8), and the remaining overlay families
// (subsequent epics). No execution logic ships in this file.

// OverlayKind identifies one entry in the overlay catalog. On the wire
// every kind is SCREAMING_SNAKE and prefixed `OVERLAY_`; the exported
// Go identifier uses mixed case.
type OverlayKind string

const (
	// OverlayKindIndexVsMargin produces an index score per cell (or per
	// row/column, depending on Scope) by comparing the cell value against
	// the matching axis margin: 100 * cell / margin. Scope=CELL emits one
	// scalar per cell; Scope=ROW or COLUMN emits one scalar per axis key
	// when the comparison degenerates (e.g. row-share index vs grand
	// total). The default reference (Ref.Margin) names the axis whose
	// margin is the denominator. Inherently buffered because margins are
	// always recomputed from raw rows in the crosstab path.
	OverlayKindIndexVsMargin OverlayKind = "OVERLAY_INDEX_VS_MARGIN"
)

// OverlayShape declares the structural footprint of an overlay's
// rendered payload. Downstream renderers branch on this to lay the
// overlay grid on top of the base result.
type OverlayShape string

const (
	// OverlayShapeScalar carries a single float64 — a Total-scoped index,
	// a single sibling-vs-baseline delta, etc.
	OverlayShapeScalar OverlayShape = "scalar"

	// OverlayShapeSeries carries one float64 per axis key — a row-wise
	// index strip, a per-column deviation strip. SeriesPayload below
	// carries the keys + values in matching order.
	OverlayShapeSeries OverlayShape = "series"

	// OverlayShapeMatrix carries a full row × column grid of float64
	// cells — most commonly produced by Scope=CELL overlays where every
	// cell of the base matrix receives a derived score. MatrixPayload
	// (from crosstab.go) is reused so renderers handle both layers with
	// one shape.
	OverlayShapeMatrix OverlayShape = "matrix"
)

// OverlayScope declares where an overlay's computation lands in the
// base result. It is independent of OverlayShape — a CELL-scoped
// overlay typically produces a matrix payload, a ROW-scoped overlay
// typically produces a series, but the choice is per-overlay.
type OverlayScope string

const (
	// OverlayScopeCell decorates every cell of the base result. For a
	// crosstab base this is one value per (row_key, column_key) pair.
	OverlayScopeCell OverlayScope = "cell"

	// OverlayScopeRow decorates every row tuple of the base result —
	// one value per row key, independent of columns.
	OverlayScopeRow OverlayScope = "row"

	// OverlayScopeColumn decorates every column tuple of the base
	// result — one value per column key, independent of rows.
	OverlayScopeColumn OverlayScope = "column"

	// OverlayScopeMatrix decorates the matrix as a whole; the payload
	// typically carries a derived matrix that mirrors the base shape
	// (e.g. a column-normalized re-projection of the cell values).
	OverlayScopeMatrix OverlayScope = "matrix"

	// OverlayScopeGroup decorates one grouper level. Reserved for future
	// nested-axis families; v1 emits OVERLAY_NOT_IMPLEMENTED if used
	// against OVERLAY_INDEX_VS_MARGIN.
	OverlayScopeGroup OverlayScope = "group"

	// OverlayScopeTotal decorates the grand-total margin slot — a single
	// scalar covering the whole result.
	OverlayScopeTotal OverlayScope = "total"
)

// MarginAxis names which margin family an OverlayMarginRef targets.
// Mirrors the AxisKey conventions used by CrosstabSpec.
type MarginAxis string

const (
	// MarginAxisRow targets the row-margin vector (Σ over columns per
	// row key).
	MarginAxisRow MarginAxis = "row"

	// MarginAxisColumn targets the column-margin vector (Σ over rows
	// per column key).
	MarginAxisColumn MarginAxis = "column"

	// MarginAxisGrand targets the grand-total margin (Σ over every
	// filter-passing row).
	MarginAxisGrand MarginAxis = "grand"
)

// OverlayMarginRef references one of the base result's margin slots.
// E1 ships only this family; later epics drop additional pointer fields
// into OverlayRef for sibling cells, baseline indices, population
// comparisons, multi-stage chain references, and slot lookups.
type OverlayMarginRef struct {
	// Axis selects which margin slot is the denominator. Required.
	Axis MarginAxis `json:"axis"`
}

// OverlaySiblingRef is reserved for sibling-cell comparison overlays
// (e.g. compare each cell against the cell at the same row but a
// different column key). Not populated in E1; included so later
// stories drop in without re-opening this file.
type OverlaySiblingRef struct {
	// Field names the axis dimension whose sibling is referenced
	// (typically a grouper Field name on the row or column axis).
	Field string `json:"field,omitempty"`

	// Value names the specific axis-key value to compare against.
	Value string `json:"value,omitempty"`
}

// OverlayBaselineIndexRef is reserved for "vs baseline" comparison
// overlays — every cell is divided by the cell at a designated
// baseline coordinate. Not populated in E1.
type OverlayBaselineIndexRef struct {
	// Row names the baseline row-axis tuple as a sorted, axis-ordered
	// list of dictionary keys. Empty list means "use the grand total".
	Row []string `json:"row,omitempty"`

	// Column names the baseline column-axis tuple. Empty list means
	// "use the grand total".
	Column []string `json:"column,omitempty"`
}

// OverlayPopulationRef is reserved for "vs population" comparisons that
// compare a filtered cohort against an unfiltered (or differently-
// filtered) population. Not populated in E1.
type OverlayPopulationRef struct {
	// Cohort names the .pulse cohort whose unfiltered (or alternately
	// filtered) statistics constitute the comparison population.
	Cohort string `json:"cohort,omitempty"`
}

// OverlayStageRef is reserved for ProcessChain-aware overlays that
// reference an earlier stage's result. Not populated in E1.
type OverlayStageRef struct {
	// Stage indexes the earlier ChainRequest stage whose output is the
	// comparison surface. Zero-based.
	Stage int `json:"stage,omitempty"`
}

// OverlaySlotRef is reserved for slot-aware overlays that reference a
// named slot of the base result (e.g. a labelled regression
// coefficient, a named percentile bucket). Not populated in E1.
type OverlaySlotRef struct {
	// Name identifies the slot to reference.
	Name string `json:"name,omitempty"`
}

// OverlayRef is the discriminated union identifying what an overlay
// compares against. Each pointer field corresponds to one comparison
// family; exactly one is meaningfully populated per OverlaySpec. The
// validator (E1-S2) rejects an OverlaySpec that populates the wrong
// pointer for its Kind.
//
// E1 only consumes Margin. The other pointers are placeholder slots so
// later stories drop in without re-opening this file (no migration of
// embedder-side JSON when subsequent overlay families land).
type OverlayRef struct {
	// Margin selects an axis-margin slot of the base result.
	Margin *OverlayMarginRef `json:"margin,omitempty"`

	// Sibling selects another cell on the same axis. Reserved.
	Sibling *OverlaySiblingRef `json:"sibling,omitempty"`

	// BaselineIndex selects a fixed baseline coordinate. Reserved.
	BaselineIndex *OverlayBaselineIndexRef `json:"baseline_index,omitempty"`

	// Population selects an alternate cohort / population. Reserved.
	Population *OverlayPopulationRef `json:"population,omitempty"`

	// Stage selects an earlier ProcessChain stage. Reserved.
	Stage *OverlayStageRef `json:"stage,omitempty"`

	// Slot selects a named slot on the base result. Reserved.
	Slot *OverlaySlotRef `json:"slot,omitempty"`
}

// OverlaySpec is the request-side definition of one overlay layer.
// Multiple specs may ride the same Request.Overlays slice; each
// produces one OverlayLayer in Response.Overlays in matching order.
//
// Validation rules (enforced in descriptor + processing layers, not in
// this file — see E1-S2 / E1-S3):
//   - Kind is required and must be a known OverlayKind.
//   - Scope is required and must be a known OverlayScope.
//   - Ref must populate exactly one family pointer matching Kind's
//     contract (OVERLAY_INDEX_VS_MARGIN ⇒ Ref.Margin must be set).
//   - Name, when set, becomes the renderer-facing label; when empty the
//     processing layer synthesises a deterministic default keyed by
//     Kind + Scope + Ref.
//   - Params carries operator-specific configuration; the per-kind
//     schema lives alongside the kind's processor.
type OverlaySpec struct {
	// Name is the renderer-facing label for this overlay. When empty,
	// the processing layer synthesises a deterministic default.
	Name string `json:"name,omitempty"`

	// Kind selects the overlay catalog entry to execute.
	Kind OverlayKind `json:"kind"`

	// Scope declares where the overlay lands relative to the base
	// result.
	Scope OverlayScope `json:"scope"`

	// Ref names what the overlay compares against. Family pointer
	// selection depends on Kind.
	Ref OverlayRef `json:"ref"`

	// Params holds operator-specific configuration as raw JSON. Per-
	// kind schema documented alongside the kind's processor.
	Params json.RawMessage `json:"params,omitempty"`
}

// SeriesPayload is the keys-and-values strip used by series-shaped
// overlay payloads. Keys and Values share an index; len(Keys) ==
// len(Values). Keys are the rendered axis-key strings (matching the
// AxisKey display used by the matching base layer); Values are the
// derived float64 entries.
//
// Minimal placeholder: future series-bearing families (time-series
// overlays, sparkline projections) may extend SeriesPayload with
// additional metadata (axis name, value label). The shape is additive
// so additional optional fields land without breaking the existing
// JSON contract.
type SeriesPayload struct {
	// Keys is the ordered list of axis-key labels indexing this series.
	Keys []string `json:"keys"`

	// Values is the per-key float64 strip. len(Values) == len(Keys).
	Values []float64 `json:"values"`
}

// OverlayPayload is the discriminated union carrying the actual
// derived numbers an overlay produced. Exactly one of Scalar / Series
// / Matrix is meaningfully populated; Shape echoes which one.
//
// Renderers branch on Shape and read the matching field. Matrix
// reuses crosstab.MatrixPayload so a CELL-scoped overlay layered on
// top of a matrix base shares the same row/column header conventions
// as the base.
type OverlayPayload struct {
	// Shape declares which of Scalar / Series / Matrix is populated.
	Shape OverlayShape `json:"shape"`

	// Scalar is the single-value payload. Populated when Shape =
	// OverlayShapeScalar.
	Scalar *float64 `json:"scalar,omitempty"`

	// Series is the keys-and-values strip payload. Populated when
	// Shape = OverlayShapeSeries.
	Series *SeriesPayload `json:"series,omitempty"`

	// Matrix is the dense row × column payload. Populated when Shape =
	// OverlayShapeMatrix. Reuses crosstab.MatrixPayload so renderers
	// handle the overlay grid with the same header machinery as the
	// base layer.
	Matrix *MatrixPayload `json:"matrix,omitempty"`
}

// OverlaySummary carries optional renderer-friendly metadata for one
// overlay layer — min/max for colour-ramp scaling, count of present
// cells for sparsity hints, an optional baseline reference value.
// Every field is omitempty so a producer can populate just the
// summary slots that make sense for the kind in question (e.g.
// INDEX_VS_MARGIN reports min/max but not baseline; a future Z-score
// overlay reports baseline=0 and the populated standard deviation).
type OverlaySummary struct {
	// Min is the minimum derived value across the layer's payload.
	Min *float64 `json:"min,omitempty"`

	// Max is the maximum derived value across the layer's payload.
	Max *float64 `json:"max,omitempty"`

	// Count is the number of present (non-null, non-missing) entries
	// the layer produced. Zero is a valid value (e.g. an empty
	// matrix); the pointer distinguishes "0 known" from "not
	// reported".
	Count *int `json:"count,omitempty"`

	// Baseline is the comparison anchor — 100 for index-vs-margin
	// (anything < 100 underperforms, > 100 overperforms), 0 for delta
	// overlays, 1 for ratio overlays. Renderers use it to centre
	// diverging colour ramps.
	Baseline *float64 `json:"baseline,omitempty"`
}

// OverlayLayer is the response-side wrapper for one executed overlay
// spec. Response.Overlays carries one OverlayLayer per
// Request.Overlays entry in matching order.
type OverlayLayer struct {
	// Name echoes the renderer-facing label — either the request
	// Name or the synthesised default.
	Name string `json:"name"`

	// Kind echoes the overlay catalog entry that produced this layer.
	Kind OverlayKind `json:"kind"`

	// Scope echoes the spec's scope.
	Scope OverlayScope `json:"scope"`

	// Ref echoes the spec's discriminated reference.
	Ref OverlayRef `json:"ref"`

	// Payload carries the derived numbers.
	Payload OverlayPayload `json:"payload"`

	// Summary carries optional renderer-friendly metadata. Omitted
	// when the layer reported nothing useful.
	Summary *OverlaySummary `json:"summary,omitempty"`
}
