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
type CrosstabSpec struct {
	Rows    []*Group     `json:"rows"`
	Columns []*Group     `json:"columns"`
	Cell    *Aggregation `json:"cell"`

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
type MatrixCell struct {
	Value   float64 `json:"value,omitempty"`
	Present bool    `json:"present"`
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
	if s == nil || s.Cell == nil {
		return ""
	}
	if s.Cell.Label != "" {
		return s.Cell.Label
	}
	return string(s.Cell.Type) + "_" + s.Cell.Field
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
