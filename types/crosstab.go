package types

import (
	"strings"
)

// CrosstabNormalize selects how cell values are normalized prior to emission.
type CrosstabNormalize string

const (
	// CrosstabNormalizeNone leaves cell values as the raw cell aggregation.
	CrosstabNormalizeNone CrosstabNormalize = "none"
	// CrosstabNormalizeRow divides each cell by its row margin (cells in a row sum to 1).
	CrosstabNormalizeRow CrosstabNormalize = "row"
	// CrosstabNormalizeColumn divides each cell by its column margin (cells in a column sum to 1).
	CrosstabNormalizeColumn CrosstabNormalize = "column"
	// CrosstabNormalizeTotal divides each cell by the grand-total margin (whole table sums to 1).
	CrosstabNormalizeTotal CrosstabNormalize = "total"
)

// CrosstabShape selects the response payload layout.
type CrosstabShape string

const (
	// CrosstabShapeMatrix returns Response.Crosstab populated with a
	// MatrixPayload (row/col axis tuples + dense cell matrix + margins).
	// Inherently buffered.
	CrosstabShapeMatrix CrosstabShape = "matrix"
	// CrosstabShapeLong returns Response.Data with one tuple per
	// (row-key, column-key) cell — the existing grouped-tuple shape any
	// consumer of grouped output already handles. Margin rows are
	// flagged via the `_margin` field when emitted.
	CrosstabShapeLong CrosstabShape = "long"
)

// CrosstabMargins selects which margins are emitted on the response.
// Display flags are independent; a normalize direction may still trigger
// the internal computation of a margin whose display flag is false.
type CrosstabMargins struct {
	Rows    bool `json:"rows,omitempty"`
	Columns bool `json:"columns,omitempty"`
	Grand   bool `json:"grand,omitempty"`
}

// CrosstabSpec is the request-side definition of a cross-tabulation.
// It composes the existing grouper + aggregator machinery for cell
// computation and adds a reshape + margins + normalization layer.
//
// Validation rules (enforced in both predict and execution):
//   - Rows must contain at least one Group.
//   - Columns must contain at least one Group.
//   - Cell is required.
//   - A crosstab section cannot coexist with top-level Aggregations / Groups
//     on the same Request (PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS).
//   - Normalize=row requires row margins internally; normalize=column
//     requires column margins; normalize=total requires the grand total.
//     The margins are computed even when the corresponding display flag is
//     false; only emission depends on the display flag.
//   - Every MarginAggregations entry must be non-nil and carry an
//     aggregation Type (PULSE_CROSSTAB_MARGIN_AGG_INVALID), and their
//     effective labels must be unique across the slot and distinct from
//     CellLabel (PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL). Declaring
//     them on a section that emits no margin warns
//     PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED — an advisory, not a refusal.
type CrosstabSpec struct {
	Rows    []*Group     `json:"rows"`
	Columns []*Group     `json:"columns"`
	Cell    *Aggregation `json:"cell"`

	// MarginAggregations declares zero or more AUXILIARY aggregations
	// that are evaluated into the row / column / grand MARGIN
	// accumulators only and never into a cell. It exists because Cell
	// is a single *Aggregation: a second figure over the same axes —
	// the canonical case is an unweighted respondent base beside a
	// weighted metric — would otherwise cost a whole second scan of
	// the cohort.
	//
	// Additive and `omitempty`: a request that does not declare the
	// slot marshals byte-identically to one written before the slot
	// existed (TestCrosstabSpec_MarginAggregationsAbsentByteIdentical).
	//
	// Each entry carries the same shape as Cell (Type / Field / Label
	// / Params). Effective labels — Label when set, otherwise
	// TYPE_field, see MarginAggregationLabels — must be unique across
	// the auxiliary set AND distinct from CellLabel, because the
	// margin-components payload is keyed by label and the cell
	// aggregator's own margin already occupies its own.
	//
	// ADMISSION CONTRACT (normative for the accumulation paths): an
	// auxiliary margin aggregation observes the SAME record admission
	// as the cell aggregator — a record contributes to these
	// accumulators only if it contributed to a cell. That is
	// deliberately NOT how the cell aggregator's own margins behave
	// (those see every filter-passing record with a non-null axis
	// key), and it is what makes an auxiliary base reconcilable
	// against the cells it sits beside. The slot carries no knob for
	// it: one sanctioned behaviour, stated here rather than made
	// configurable.
	//
	// Auxiliary figures are only observable where a margin is emitted;
	// see MarginAggregationsObserved.
	MarginAggregations []*Aggregation `json:"margin_aggregations,omitempty"`

	Margins   CrosstabMargins   `json:"margins,omitzero"`
	Normalize CrosstabNormalize `json:"normalize,omitempty"`
	Shape     CrosstabShape     `json:"shape,omitempty"`

	// NormalizeLevel selects the depth in the nested axis whose value
	// constitutes the 100% denominator for normalization. Zero-indexed
	// from the top of the axis (0 = first grouper). Applies only when
	// Normalize=row or Normalize=column. Absent (nil) defaults to the
	// leaf — len(axis)-1 — which is the original per-leaf-tuple
	// normalization behavior. Rejected when set with normalize=total
	// (no axis to descend) or normalize=none.
	NormalizeLevel *int `json:"normalize_level,omitempty"`

	// NormalizeWithin selects a prefix depth on the OTHER axis whose
	// value, combined with the full normalize-axis key (or its
	// NormalizeLevel-truncated form), constitutes the 100% denominator.
	// Zero-indexed from the top of the other axis (0 = first grouper).
	// Applies only when Normalize=row or Normalize=column. Absent (nil)
	// leaves the denominator scope unchanged — the existing row /
	// column marginal. Rejected when set with normalize=none or
	// normalize=total (no other axis to partition).
	//
	// Example: Rows=[brand], Columns=[wavedate, xxx], Normalize=row,
	// NormalizeWithin=0 ⇒ each cell is divided by Σ over xxx within
	// (brand, wavedate); the (brand, wavedate) slab of xxx cells sums
	// to 1. Combines independently with NormalizeLevel — that field
	// truncates the normalize axis; this one fixes a prefix of the
	// OTHER axis.
	NormalizeWithin *int `json:"normalize_within,omitempty"`
}

// NormalizeOrDefault returns the configured normalization mode, defaulting
// to "none" on the zero value.
func (s *CrosstabSpec) NormalizeOrDefault() CrosstabNormalize {
	if s == nil || s.Normalize == "" {
		return CrosstabNormalizeNone
	}
	return s.Normalize
}

// ShapeOrDefault returns the configured shape, defaulting to "matrix" on
// the zero value (matches the canonical handoff §2 default).
func (s *CrosstabSpec) ShapeOrDefault() CrosstabShape {
	if s == nil || s.Shape == "" {
		return CrosstabShapeMatrix
	}
	return s.Shape
}

// NormalizeLevelOrLeaf returns the configured normalize depth clamped
// to a valid axis position. axisLen is the count of groupers on the
// relevant axis (rows when normalize=row, columns when
// normalize=column). When NormalizeLevel is nil, negative, or
// >= axisLen, the leaf depth (axisLen-1) is returned. An axisLen of 0
// yields 0; callers must guard against the empty-axis case
// independently (the validator does).
func (s *CrosstabSpec) NormalizeLevelOrLeaf(axisLen int) int {
	if axisLen <= 0 {
		return 0
	}
	leaf := axisLen - 1
	if s == nil || s.NormalizeLevel == nil {
		return leaf
	}
	lvl := *s.NormalizeLevel
	if lvl < 0 || lvl > leaf {
		return leaf
	}
	return lvl
}

// NeedsRowMargin reports whether the row-margin vector is required
// either for display or to support the configured normalization mode.
func (s *CrosstabSpec) NeedsRowMargin() bool {
	if s == nil {
		return false
	}
	return s.Margins.Rows || s.NormalizeOrDefault() == CrosstabNormalizeRow
}

// NeedsColumnMargin reports whether the column-margin vector is required.
func (s *CrosstabSpec) NeedsColumnMargin() bool {
	if s == nil {
		return false
	}
	return s.Margins.Columns || s.NormalizeOrDefault() == CrosstabNormalizeColumn
}

// NeedsGrandMargin reports whether the grand-total margin is required.
func (s *CrosstabSpec) NeedsGrandMargin() bool {
	if s == nil {
		return false
	}
	return s.Margins.Grand || s.NormalizeOrDefault() == CrosstabNormalizeTotal
}

// IsBuffered reports whether the section forces the buffered execution
// path. Matrix shape, any margin, or any normalization buffers; only
// shape=long with no margins and normalize=none can stream the cell
// aggregation through the existing grouped streaming path.
func (s *CrosstabSpec) IsBuffered() bool {
	if s == nil {
		return false
	}
	if s.ShapeOrDefault() == CrosstabShapeMatrix {
		return true
	}
	if s.Margins.Rows || s.Margins.Columns || s.Margins.Grand {
		return true
	}
	if s.NormalizeOrDefault() != CrosstabNormalizeNone {
		return true
	}
	return false
}

// IsValidNormalize reports whether mode is one of the known normalize
// strings. Used by validators that accept the zero value as "none".
func IsValidNormalize(mode CrosstabNormalize) bool {
	switch mode {
	case "", CrosstabNormalizeNone,
		CrosstabNormalizeRow, CrosstabNormalizeColumn, CrosstabNormalizeTotal:
		return true
	}
	return false
}

// IsValidShape reports whether s is one of the known shape strings. Used
// by validators that accept the zero value as "matrix".
func IsValidShape(s CrosstabShape) bool {
	switch s {
	case "", CrosstabShapeMatrix, CrosstabShapeLong:
		return true
	}
	return false
}

// AxisKey is the per-axis tuple of dictionary keys identifying one row or
// column of the materialized matrix. Each entry is the value emitted by
// the corresponding grouper in the configured Rows / Columns slice (in
// declaration order). Encoded as []any so callers can compare against the
// long-form result rows directly — numeric bins stay numeric, categorical
// keys stay strings, date keys stay strings (whatever the grouper
// emitted).
type AxisKey []any

// AxisHeader names the fields making up one axis tuple. Field i in the
// header corresponds to position i in every AxisKey on that axis.
type AxisHeader struct {
	// Fields lists the grouper Field names in axis order.
	Fields []string `json:"fields"`
	// Types lists the GroupType strings in axis order (e.g.
	// "GROUP_CATEGORY", "GROUP_RANGE"). Carried so downstream consumers
	// can render bin labels correctly without re-deriving from a
	// schema.
	Types []string `json:"types"`
}

// MatrixCell is one entry in the dense cell matrix. Present=false marks
// a structurally missing cell (no underlying record matched the
// row × column tuple), which downstream consumers must render as null /
// empty rather than zero (the AGG_AVERAGE of an empty set is undefined,
// not zero).
//
// Value is a scalar/rich union. Scalar aggregators populate a float64
// (existing JSON shape preserved byte-for-byte through encoding/json).
// RichAggregator implementations populate the structured payload
// directly: AGG_SET_FREQUENCY emits map[string]int (per-label row
// counts), AGG_SET_UNION / AGG_SET_INTERSECTION emit []string (sorted
// dictionary labels). AGG_WELFORD is the named carve-out — its rich
// WelfordTriple payload does NOT ride MatrixCell.Value (the carrier is
// Response.Components.Crosstab.CellComponents[r][c]'s
// `{mean, variance, n}` triple instead, populated by the orchestrator's
// MetaAggregator pass); the cell value falls back to the scalar mean
// (matching welfordAggregator.Aggregate / Finalize) so downstream
// renderers see a plain float64. Normalize modes (row/column/total)
// are only defined for scalar cells; the Crosstab validator rejects
// them paired with map-valued aggregators (see
// types.AggregationType.MapValued).
type MatrixCell struct {
	Value   any  `json:"value,omitempty"`
	Present bool `json:"present"`
}

// Scalar returns the cell's scalar form for callers that expect a
// numeric value. Returns 0 when the cell is absent OR when Value is
// non-scalar (rich payload). Use Value directly with a type switch to
// distinguish scalar / map / slice payloads.
func (c MatrixCell) Scalar() float64 {
	if !c.Present {
		return 0
	}
	switch v := c.Value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	}
	return 0
}

// MatrixPayload is the structured matrix-shape response carried on
// Response.Crosstab. Includes enough information for a downstream renderer
// (Prism heatmap, terminal grid, etc.) to lay out the table without
// re-deriving axis structure from the long-form result.
type MatrixPayload struct {
	// RowHeader names the row-axis grouper fields and types in order.
	RowHeader AxisHeader `json:"row_header"`
	// ColumnHeader names the column-axis grouper fields and types in order.
	ColumnHeader AxisHeader `json:"column_header"`

	// RowKeys is the deterministic, sorted list of row tuples.
	RowKeys []AxisKey `json:"row_keys"`
	// ColumnKeys is the deterministic, sorted list of column tuples.
	ColumnKeys []AxisKey `json:"column_keys"`

	// Cells is a 2-D array indexed [row_index][column_index]. Missing
	// cells set MatrixCell.Present=false; present cells set Value to the
	// (possibly normalized) cell aggregation.
	Cells [][]MatrixCell `json:"cells"`

	// RowMargins carries the per-row margin in RowKeys order. Empty when
	// row margins were not requested and not required by normalization.
	RowMargins []MatrixCell `json:"row_margins,omitempty"`
	// ColumnMargins carries the per-column margin in ColumnKeys order.
	ColumnMargins []MatrixCell `json:"column_margins,omitempty"`
	// GrandTotal is the grand-total margin. Present=false when neither
	// requested nor required by normalization.
	GrandTotal MatrixCell `json:"grand_total"`

	// CellLabel echoes the underlying aggregation's effective label so
	// downstream consumers can colorize/sort by it without inspecting
	// the originating Request.
	CellLabel string `json:"cell_label"`

	// NormalizeApplied echoes the normalization mode that was actually
	// applied. Mirrors CrosstabSpec.Normalize after defaulting.
	NormalizeApplied CrosstabNormalize `json:"normalize_applied"`
}

// MarginAggregationFigure is one auxiliary margin-only aggregation's
// finished figure in one margin slot — the counterpart of a single
// entry in CrosstabComponents.RowMarginComponents, but for an
// aggregation from CrosstabSpec.MarginAggregations rather than for the
// cell aggregator itself.
//
// PRESENT IS LOAD-BEARING AND IS NOT A ZERO VALUE. An auxiliary slot
// that admitted no record at all carries Present false and NO Value:
// an aggregator over an empty set has no defined output, and emitting
// 0 would put a fabricated base beside real cells — indistinguishable,
// on the wire, from a genuine zero. Present therefore carries no
// omitempty, so a false survives the round trip rather than
// disappearing into the same absence a consumer must not read as a
// figure.
//
// Components carries the universal floor {n, n_null} over the ADMITTED
// records merged with this aggregator's own ComponentSchema keys — so
// AGG_DISTINCT_SUM surfaces its distinct_count here alongside the
// scalar sum in Value, which is the entire reason one auxiliary can
// serve two rendered sample-size rows off one scan. It is present even
// when Present is false, carrying the floor alone, because n = 0 is a
// true statement about the slot where a value would be an invented one.
type MarginAggregationFigure struct {
	// Value is the aggregator's own finalised output. Omitted entirely
	// when Present is false.
	Value any `json:"value,omitempty"`

	// Present reports whether any record was admitted to this slot.
	// Deliberately not omitempty — see the type comment.
	Present bool `json:"present"`

	// Components is the universal floor {n, n_null} over the admitted
	// records merged with the aggregator's operator-specific keys.
	Components map[string]any `json:"components,omitempty"`
}

// CrosstabComponents carries the constituent-parts metadata for a
// crosstab response. Mirrors MatrixPayload coordinate-for-coordinate so
// consumers can index components by the same (row, column) tuple they
// already use to read MatrixPayload.Cells / RowMargins / ColumnMargins
// / GrandTotal. Every field is `omitempty` so an unpopulated
// CrosstabComponents marshals to byte-identical wire output against the
// pre-Components baseline; an empty struct produces no JSON keys at
// all.
//
// Layout (each axis indexed in the same order as MatrixPayload.RowKeys
// / MatrixPayload.ColumnKeys):
//
//   - CellCounts[r][c] — per-cell record count (mirrors
//     MatrixPayload.Cells[r][c]).
//   - CellComponents[r][c] — per-cell aggregator components; keys are
//     governed by the cell aggregator's ComponentSchema declaration
//     in descriptor/capabilities_aggregators.go.
//   - RowMarginCounts[r] / RowMarginComponents[r] — row-margin counts
//   - components, indexed by row (mirrors MatrixPayload.RowMargins).
//   - ColumnMarginCounts[c] / ColumnMarginComponents[c] — column-margin
//     counts + components, indexed by column (mirrors
//     MatrixPayload.ColumnMargins).
//   - GrandTotalCount / GrandTotalComponents — grand-total counterparts
//     (mirror MatrixPayload.GrandTotal).
//   - RowMarginAggregations[r] / ColumnMarginAggregations[c] /
//     GrandTotalAggregations — the AUXILIARY margin-only aggregation
//     figures (CrosstabSpec.MarginAggregations), keyed by effective
//     label. Present only when the request declared an auxiliary AND
//     the matching margin is emitted; absent otherwise, so a caller
//     can gate on presence rather than on a partial payload.
//   - RowKeyComponents[r] / ColumnKeyComponents[c] — per-axis grouper
//     components carried alongside each row / column tuple (bucket
//     edges, dict mappings, etc. — same shape as
//     GrouperComponents.Operator). Indexed by row / column position so
//     consumers can join axis-key metadata to the corresponding
//     MatrixPayload.RowKeys[r] / ColumnKeys[c] entry.
//   - IncludedRecords / ExcludedRecords — sanity counters. IncludedRecords
//     is the number of records that contributed to at least one cell;
//     ExcludedRecords is the number that were filtered out at the
//     crosstab stage (null axis key, etc.). Their sum equals the
//     post-filter input record count.
//
// Service-side population lives in service/crosstab.go.
type CrosstabComponents struct {
	// CellCounts is the per-cell record count matrix. CellCounts[r][c]
	// mirrors MatrixPayload.Cells[r][c] coordinate-for-coordinate.
	CellCounts [][]int `json:"cell_counts,omitempty"`

	// CellComponents is the per-cell aggregator components matrix.
	// CellComponents[r][c] carries the keys declared by the cell
	// aggregator's ComponentSchema.
	CellComponents [][]map[string]any `json:"cell_components,omitempty"`

	// RowMarginCounts is the per-row-margin record count vector,
	// indexed in MatrixPayload.RowKeys order.
	RowMarginCounts []int `json:"row_margin_counts,omitempty"`

	// RowMarginComponents is the per-row-margin aggregator components
	// vector, indexed in MatrixPayload.RowKeys order.
	RowMarginComponents []map[string]any `json:"row_margin_components,omitempty"`

	// ColumnMarginCounts is the per-column-margin record count vector,
	// indexed in MatrixPayload.ColumnKeys order.
	ColumnMarginCounts []int `json:"column_margin_counts,omitempty"`

	// ColumnMarginComponents is the per-column-margin aggregator
	// components vector, indexed in MatrixPayload.ColumnKeys order.
	ColumnMarginComponents []map[string]any `json:"column_margin_components,omitempty"`

	// GrandTotalCount is the grand-total record count (mirrors the
	// count behind MatrixPayload.GrandTotal).
	GrandTotalCount int `json:"grand_total_count,omitempty"`

	// GrandTotalComponents is the grand-total aggregator components
	// payload (mirrors the components behind MatrixPayload.GrandTotal).
	GrandTotalComponents map[string]any `json:"grand_total_components,omitempty"`

	// RowMarginAggregations carries the AUXILIARY margin-only
	// aggregation figures for each row, indexed in
	// MatrixPayload.RowKeys order and keyed inside each entry by the
	// auxiliary's effective label (CrosstabSpec.MarginAggregationLabels,
	// which the validators already forced unique across the slot AND
	// distinct from CellLabel — so a label can address exactly one
	// figure). A row that admitted no record at all emits nil in its
	// slot, exactly as RowMarginComponents does.
	//
	// These sit BESIDE RowMarginComponents rather than inside it
	// because they are not the same figure: RowMarginComponents
	// describes the CELL aggregator's own row margin, which counts
	// every filter-passing record routed to that row; an auxiliary
	// observes the cell's ADMISSION instead. Merging the two key sets
	// into one map would put two differently-based figures under one
	// roof with nothing saying so.
	RowMarginAggregations []map[string]MarginAggregationFigure `json:"row_margin_aggregations,omitempty"`

	// ColumnMarginAggregations is the column-axis sibling of
	// RowMarginAggregations, indexed in MatrixPayload.ColumnKeys order.
	ColumnMarginAggregations []map[string]MarginAggregationFigure `json:"column_margin_aggregations,omitempty"`

	// GrandTotalAggregations is the grand-slot sibling of
	// RowMarginAggregations — one entry per declared auxiliary, keyed
	// by effective label.
	GrandTotalAggregations map[string]MarginAggregationFigure `json:"grand_total_aggregations,omitempty"`

	// RowKeyComponents carries one grouper-components payload per row,
	// indexed in MatrixPayload.RowKeys order. The key set on each
	// element matches GrouperComponents.Operator for the row-axis
	// grouper.
	RowKeyComponents []map[string]any `json:"row_key_components,omitempty"`

	// ColumnKeyComponents carries one grouper-components payload per
	// column, indexed in MatrixPayload.ColumnKeys order. The key set on
	// each element matches GrouperComponents.Operator for the
	// column-axis grouper.
	ColumnKeyComponents []map[string]any `json:"column_key_components,omitempty"`

	// IncludedRecords is the number of records that contributed to at
	// least one cell (sanity counter — independent of the cell count
	// matrix sum, which double-counts records under multi-key groupers).
	IncludedRecords int `json:"included_records,omitempty"`

	// ExcludedRecords is the number of records dropped at the crosstab
	// stage (null axis key, missing dict entry, etc.). IncludedRecords
	// + ExcludedRecords equals the post-filter input record count.
	ExcludedRecords int `json:"excluded_records,omitempty"`
}

// CrosstabResult is the top-level result of a crosstab request. Carries
// the matrix payload when shape=matrix; long-form rows land on
// Response.Data and Result.Long indicates the shape.
type CrosstabResult struct {
	// Shape echoes the shape that was emitted (after default resolution).
	Shape CrosstabShape `json:"shape"`

	// Matrix is the dense matrix payload. Nil when shape=long.
	Matrix *MatrixPayload `json:"matrix,omitempty"`
}

// LowerToGroupedRequest builds the equivalent grouped Process request
// that produces the long-form cell values for this crosstab. The Cohort,
// Filterers, Labels, and feature slots on src are carried through
// verbatim; the crosstab section itself is cleared on the result so
// downstream processors do not loop. The returned Request shares no
// pointer state with src.Crosstab — modifying the returned slice is
// safe even when src is concurrently in use.
func (s *CrosstabSpec) LowerToGroupedRequest(src *Request) *Request {
	if s == nil || src == nil {
		return nil
	}
	groups := make([]*Group, 0, len(s.Rows)+len(s.Columns))
	for _, g := range s.Rows {
		if g != nil {
			cp := *g
			groups = append(groups, &cp)
		}
	}
	for _, g := range s.Columns {
		if g != nil {
			cp := *g
			groups = append(groups, &cp)
		}
	}
	var aggs []*Aggregation
	if s.Cell != nil {
		cell := *s.Cell
		aggs = []*Aggregation{&cell}
	}
	return &Request{
		Cohort:       src.Cohort,
		Filterers:    src.Filterers,
		Features:     src.Features,
		Labels:       src.Labels,
		Groups:       groups,
		Aggregations: aggs,
	}
}

// LowerRowsOnly builds the rows-only request used to recompute row
// margins. Cohort, Filterers, and Labels carry through; the cell
// aggregation rides verbatim. Cell may be nil only when the caller has
// already validated the spec — production callers always set it.
func (s *CrosstabSpec) LowerRowsOnly(src *Request) *Request {
	if s == nil || src == nil {
		return nil
	}
	groups := make([]*Group, 0, len(s.Rows))
	for _, g := range s.Rows {
		if g != nil {
			cp := *g
			groups = append(groups, &cp)
		}
	}
	var aggs []*Aggregation
	if s.Cell != nil {
		cell := *s.Cell
		aggs = []*Aggregation{&cell}
	}
	return &Request{
		Cohort:       src.Cohort,
		Filterers:    src.Filterers,
		Features:     src.Features,
		Labels:       src.Labels,
		Groups:       groups,
		Aggregations: aggs,
	}
}

// LowerColumnsOnly builds the columns-only request used to recompute
// column margins.
func (s *CrosstabSpec) LowerColumnsOnly(src *Request) *Request {
	if s == nil || src == nil {
		return nil
	}
	groups := make([]*Group, 0, len(s.Columns))
	for _, g := range s.Columns {
		if g != nil {
			cp := *g
			groups = append(groups, &cp)
		}
	}
	var aggs []*Aggregation
	if s.Cell != nil {
		cell := *s.Cell
		aggs = []*Aggregation{&cell}
	}
	return &Request{
		Cohort:       src.Cohort,
		Filterers:    src.Filterers,
		Features:     src.Features,
		Labels:       src.Labels,
		Groups:       groups,
		Aggregations: aggs,
	}
}

// LowerGrandOnly builds the no-grouper request used to recompute the
// grand-total margin. Equivalent to a plain aggregation over the
// filter-passing record set.
func (s *CrosstabSpec) LowerGrandOnly(src *Request) *Request {
	if s == nil || src == nil {
		return nil
	}
	var aggs []*Aggregation
	if s.Cell != nil {
		cell := *s.Cell
		aggs = []*Aggregation{&cell}
	}
	return &Request{
		Cohort:       src.Cohort,
		Filterers:    src.Filterers,
		Features:     src.Features,
		Labels:       src.Labels,
		Aggregations: aggs,
	}
}

// CellLabel returns the output label the cell aggregation would emit in
// a long-form result row. Mirrors processing.AggregationLabel without
// importing processing/.
func (s *CrosstabSpec) CellLabel() string {
	if s == nil {
		return ""
	}
	return AggregationLabelOf(s.Cell)
}

// AggregationLabelOf returns the output label an aggregation emits in a
// long-form result row: the explicit Label when set, otherwise
// TYPE_field. Mirrors processing.AggregationLabel without importing
// processing/ (types must stay dependency-free). A nil aggregation
// yields the empty string.
func AggregationLabelOf(a *Aggregation) string {
	if a == nil {
		return ""
	}
	if a.Label != "" {
		return a.Label
	}
	return string(a.Type) + "_" + a.Field
}

// HasMarginAggregations reports whether the spec declares at least one
// auxiliary margin aggregation.
func (s *CrosstabSpec) HasMarginAggregations() bool {
	return s != nil && len(s.MarginAggregations) > 0
}

// MarginAggregationLabels returns the effective label of every declared
// auxiliary margin aggregation, in declaration order. These are the keys
// the margin-components payload carries the auxiliary figures under. A
// nil entry contributes an empty string so positions stay aligned with
// CrosstabSpec.MarginAggregations; the validators refuse such an entry
// before it reaches an accumulator.
func (s *CrosstabSpec) MarginAggregationLabels() []string {
	if s == nil || len(s.MarginAggregations) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.MarginAggregations))
	for _, a := range s.MarginAggregations {
		out = append(out, AggregationLabelOf(a))
	}
	return out
}

// MarginAggregationsObserved reports whether any margin is emitted or
// computed for this spec, i.e. whether a declared auxiliary margin
// aggregation has somewhere to land. False means the auxiliary
// aggregations would be computed into nowhere — structurally legal but
// almost certainly a mistake, which predict surfaces as a warning.
func (s *CrosstabSpec) MarginAggregationsObserved() bool {
	if s == nil {
		return false
	}
	return s.NeedsRowMargin() || s.NeedsColumnMargin() || s.NeedsGrandMargin()
}

// MarginAggregationFaultKind classifies a structural defect in the
// MarginAggregations slot. It deliberately carries no error code: the
// types package stays free of a dependency on errors/, so predict
// (descriptor) and execution (processing) each map a kind onto their own
// coded surface. Sharing the DETECTION is what keeps the two validators
// from drifting on WHICH specs they refuse.
type MarginAggregationFaultKind string

const (
	// MarginAggregationFaultNilEntry marks a nil element in the slot —
	// reachable from JSON as a literal null.
	MarginAggregationFaultNilEntry MarginAggregationFaultKind = "nil_entry"

	// MarginAggregationFaultMissingType marks an entry with no
	// aggregation Type. There is no default auxiliary aggregator.
	MarginAggregationFaultMissingType MarginAggregationFaultKind = "missing_type"

	// MarginAggregationFaultDuplicateLabel marks an effective label
	// claimed twice — by two auxiliary entries, or by an auxiliary
	// entry and the cell aggregation whose own margin already occupies
	// it.
	MarginAggregationFaultDuplicateLabel MarginAggregationFaultKind = "duplicate_label"
)

// MarginAggregationFault is one structural defect found in the
// MarginAggregations slot. Message and Details are rendered verbatim by
// both validators so the predict envelope and the runtime error read
// identically for the same defect.
type MarginAggregationFault struct {
	// Kind classifies the defect; each validator maps it to a code.
	Kind MarginAggregationFaultKind
	// Message is the human-readable sentence, ready to render.
	Message string
	// Details is the structured payload, ready to attach.
	Details map[string]any
}

// MarginAggregationFaults returns every structural defect in the
// MarginAggregations slot, in declaration order. An empty result means
// the slot is well-formed; it says nothing about whether the named
// aggregators exist (extensions register their own) or whether a margin
// is emitted to carry them (see MarginAggregationsObserved).
func (s *CrosstabSpec) MarginAggregationFaults() []MarginAggregationFault {
	if s == nil || len(s.MarginAggregations) == 0 {
		return nil
	}
	var faults []MarginAggregationFault
	// The cell aggregator's own margin already claims CellLabel in the
	// margin-components namespace, so it seeds the seen set.
	seen := map[string]bool{}
	if cl := s.CellLabel(); cl != "" {
		seen[cl] = true
	}
	for i, a := range s.MarginAggregations {
		if a == nil {
			faults = append(faults, MarginAggregationFault{
				Kind:    MarginAggregationFaultNilEntry,
				Message: "crosstab margin_aggregations contains a null entry",
				Details: map[string]any{"index": i},
			})
			continue
		}
		if a.Type == "" {
			faults = append(faults, MarginAggregationFault{
				Kind:    MarginAggregationFaultMissingType,
				Message: "crosstab margin_aggregations entry has no aggregation type",
				Details: map[string]any{"index": i, "field": a.Field},
			})
			continue
		}
		label := AggregationLabelOf(a)
		if seen[label] {
			faults = append(faults, MarginAggregationFault{
				Kind: MarginAggregationFaultDuplicateLabel,
				Message: "crosstab margin_aggregations effective label is already claimed: " + label +
					" — margin components are keyed by label, so the duplicate would overwrite the figure beside it",
				Details: map[string]any{"index": i, "label": label, "aggregation": string(a.Type)},
			})
			continue
		}
		seen[label] = true
	}
	return faults
}

// AxisFieldNames returns the field names making up an axis in axis order.
// Helper for predict warnings and reshape lookups.
func AxisFieldNames(axis []*Group) []string {
	out := make([]string, 0, len(axis))
	for _, g := range axis {
		if g != nil {
			out = append(out, g.Field)
		}
	}
	return out
}

// AxisTypes returns the GroupType strings making up an axis in order.
func AxisTypes(axis []*Group) []string {
	out := make([]string, 0, len(axis))
	for _, g := range axis {
		if g != nil {
			out = append(out, string(g.Type))
		}
	}
	return out
}

// NormalizedString returns the trimmed, lowercased form of a normalize
// mode string so callers can accept user-supplied variants like "Row".
func NormalizedString(mode string) CrosstabNormalize {
	return CrosstabNormalize(strings.ToLower(strings.TrimSpace(mode)))
}
