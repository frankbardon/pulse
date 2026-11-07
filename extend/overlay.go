package extend

import "github.com/frankbardon/pulse/types"

// OverlayHandler is an extension OVERLAY_* kind's execution contract:
// one handler per host the kind runs on. A nil handler declares that the
// kind does not run on that host — a spec of the kind authored over it
// is refused at predict and at runtime alike, with the shared
// wrong-shape (or wrong-slot) refusal. At least one handler is
// required.
//
// Three hosts: Matrix and Series are the two Process hosts of
// Request.Overlays (and a Compose slot's own Request.Overlays); Compose
// is ComposedRequest.Overlays. No extension kind runs on
// ChainRequest.Overlays or FacetRequest.Overlays. A registration that
// does not set a handler is unaffected by the hosts it leaves out.
type OverlayHandler struct {
	// Matrix runs the kind over a crosstab host: the engine has already
	// materialised the crosstab, and the handler folds a derived layer
	// over it without re-reading a record.
	Matrix MatrixOverlayFunc
	// Series runs the kind over a SERIES host: a grouped request
	// (Request.Groups + Request.Aggregations, no Request.Crosstab),
	// scored on its first aggregation.
	Series SeriesOverlayFunc
	// Compose runs the kind over ComposedRequest.Overlays: the
	// already-finalised slot responses its Reference and Targets name.
	Compose ComposeOverlayFunc
}

// MatrixOverlayFunc folds one overlay layer over a crosstab host. spec
// is a copy of the request's spec (its Params bytes included): the
// handler may read it but must not keep it. host is read-only and valid
// only for the duration of the call.
//
// It must be deterministic and pure: the engine owns the layer's Name,
// Kind, Scope and Ref (echoed from spec), and never re-runs a handler.
// A returned error refuses the request: a *errors.CodedError keeps its
// code, any other error is returned as is. A panic is recovered by the
// engine's operation guard (PROCESSING_PANIC), like any other extension
// panic at run time.
type MatrixOverlayFunc func(spec *types.OverlaySpec, host MatrixOverlayHost) (OverlayOutput, error)

// SeriesOverlayFunc folds one overlay layer over a SERIES host, under
// MatrixOverlayFunc's contract (a spec copy, a read-only host valid for
// the call, deterministic and pure, errors and panics alike).
type SeriesOverlayFunc func(spec *types.OverlaySpec, host SeriesOverlayHost) (OverlayOutput, error)

// ComposeOverlayFunc folds one overlay layer over a Compose host —
// the slot responses spec.Reference and spec.Targets name — under
// MatrixOverlayFunc's contract. spec is a copy (its Params map and
// Targets included).
type ComposeOverlayFunc func(spec *types.ComposeOverlaySpec, host ComposeOverlayHost) (OverlayOutput, error)

// OverlayOutput is what an overlay handler returns: the layer's payload
// and its optional summary and warnings. The engine stamps the layer's
// Name (spec.Name, else the lower-cased kind without its OVERLAY_
// prefix), Kind, Scope and Ref.
//
// The payload is held to the registration on every run and at
// pulse.New's probe (PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH): its Shape
// must be one the registration declares in Shapes, exactly the matching
// arm set (Scalar for scalar, Series for series, Matrix for matrix) and
// the other two nil. The multiple-comparison figures (PAdjusted,
// SignificantAdjusted, on the payload and on the summary) are the
// engine's and must stay nil, and so must Parts.
type OverlayOutput struct {
	// Payload carries the layer's derived numbers.
	Payload types.OverlayPayload
	// Summary is the optional renderer metadata (min / max / count /
	// baseline; statistic / p-value for an inferential kind).
	Summary *types.OverlaySummary
	// Warnings are per-cell diagnostics (an undefined denominator, say);
	// the engine lifts each onto the response's warnings. Each needs a
	// non-empty Code and Message.
	Warnings []types.OverlayWarning
}

// OverlayRefKind names one OverlayRef arm an extension kind reads, as
// the registration's Refs declares it.
type OverlayRefKind string

const (
	// OverlayRefNone accepts a spec with every Ref arm empty.
	OverlayRefNone OverlayRefKind = "none"
	// OverlayRefMargin accepts a spec setting Ref.Margin (a known
	// MarginAxis: row, column or grand).
	OverlayRefMargin OverlayRefKind = "margin"
)

// MatrixOverlayHost is the read-only crosstab an extension overlay
// folds over. Indices are zero-based: rows 0..Rows()-1 in the crosstab's
// RowKeys order, columns 0..Columns()-1 in its ColumnKeys order. Every
// accessor returns a copy or a scalar; nothing it returns aliases the
// response.
//
// Cell and margin values are the crosstab's own figures (the cell
// aggregation, normalised when the crosstab normalises). A value
// accessor reports false for an absent cell, an undefined (NaN) one and
// a non-scalar one; a count accessor reports false when the run
// computes no components (disable_components / Options.DisableComponents).
type MatrixOverlayHost interface {
	// Rows is the number of row tuples.
	Rows() int
	// Columns is the number of column tuples (1 for a crosstab without a
	// column grouper: the implicit Total column).
	Columns() int
	// RowHeader and ColumnHeader name each axis's grouper fields and
	// types.
	RowHeader() types.AxisHeader
	ColumnHeader() types.AxisHeader
	// RowKey and ColumnKey are the axis tuples at index (nil out of
	// range).
	RowKey(r int) types.AxisKey
	ColumnKey(c int) types.AxisKey
	// CellLabel is the crosstab cell aggregation's label.
	CellLabel() string
	// Cell is the scalar cell value at (r, c).
	Cell(r, c int) (float64, bool)
	// RowMargin, ColumnMargin and GrandTotal are the crosstab's margins,
	// false when the crosstab computes none.
	RowMargin(r int) (float64, bool)
	ColumnMargin(c int) (float64, bool)
	GrandTotal() (float64, bool)
	// CellN is the number of records in cell (r, c) — the components
	// floor n.
	CellN(r, c int) (int, bool)
	// RowMarginN and ColumnMarginN are the per-row / per-column record
	// counts.
	RowMarginN(r int) (int, bool)
	ColumnMarginN(c int) (int, bool)
	// WeightKind is the kind of the row weight the crosstab cell ran
	// under, "" when it is unweighted.
	WeightKind() types.WeightKind
	// CellSumWeights is cell (r, c)'s Σw (weighted cells only).
	CellSumWeights(r, c int) (float64, bool)
	// CellNEff is cell (r, c)'s Kish effective n (probability-weighted
	// cells only).
	CellNEff(r, c int) (float64, bool)
}

// SeriesOverlayHost is the read-only SERIES an extension overlay folds
// over: one entry per group row, in the response's row order, scored on
// one aggregation column. Every accessor returns a copy or a scalar;
// nothing it returns aliases the response.
type SeriesOverlayHost interface {
	// Groups is the number of group rows.
	Groups() int
	// GroupFields names the key column of each grouper, in
	// Request.Groups order; GrouperKinds is each grouper's type.
	GroupFields() []string
	GrouperKinds() []types.GroupType
	// GroupKey is the group tuple at index i (nil out of range),
	// element-for-element with GroupFields.
	GroupKey(i int) types.AxisKey
	// ValueLabel is the scored aggregation's output label (the
	// request's first aggregation).
	ValueLabel() string
	// Value is the scored aggregation at group i; false for an absent,
	// undefined (NaN) or non-numeric value.
	Value(i int) (float64, bool)
}

// ComposeOverlayHost is the read-only Compose host of a
// ComposedRequest.Overlays spec: its reference slot and, in
// spec.Targets order, its target slots. The engine has resolved every
// label (an unknown or failed slot is refused before the handler runs)
// and held the slots to the shared Compose gates: one host shape for
// all of them, aligned key sets, matching axis schemas.
type ComposeOverlayHost interface {
	// Reference is the slot spec.Reference names.
	Reference() ComposeOverlaySlot
	// Targets is len(spec.Targets); Target is the slot at position i
	// (nil out of range).
	Targets() int
	Target(i int) ComposeOverlaySlot
}

// ComposeOverlaySlot is one Compose slot's result, read through the
// host view its shape offers.
type ComposeOverlaySlot interface {
	// Label is the slot's Request.Label (request_<n> when it had none);
	// Index its position in ComposedRequest.Requests.
	Label() string
	Index() int
	// Shape is the slot's host shape: types.OverlayShapeMatrix (a
	// crosstab slot), types.OverlayShapeSeries (a grouped slot) or
	// types.OverlayShapeScalar (an ungrouped one).
	Shape() types.OverlayShape
	// Matrix is the slot's crosstab, nil unless Shape is matrix.
	Matrix() MatrixOverlayHost
	// Series is the slot's series, scored on its first aggregation, nil
	// unless Shape is series.
	Series() SeriesOverlayHost
	// Scalar is an ungrouped slot's first aggregation value; false
	// unless Shape is scalar and the value is a finite number.
	Scalar() (float64, bool)
}
