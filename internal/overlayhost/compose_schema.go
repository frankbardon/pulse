package overlayhost

import (
	"slices"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_schema.go is the one structural schema-match rule a
// ComposedRequest.Overlays spec's reference and target slots meet — the
// PULSE_OVERLAY_SCHEMA_DIVERGENT gate predict-compose
// (descriptor.validateComposeOverlaySpec, Gate 4) and the runtime
// Compose dispatcher (processing.checkSlotShapeAndSchemaShaped) both
// raise. Both sides read each slot's AUTHORED request, so they judge
// the same tuples, render the same canonical strings and raise the
// same message and details.

// AxisSchema is one Compose slot's structural axis schema: the grouper
// kinds of its row and column axes, in axis order (field names are
// free to differ across slots; kinds and nesting depth are not).
type AxisSchema struct {
	Rows    []string
	Columns []string
}

// RequestAxisSchema is the axis schema a slot's request declares —
// exactly the header types the runtime stamps on its result:
//
//   - a crosstab: its row grouper kinds (a multi-measure crosstab's
//     MeasureRowHeader, which appends the measure axis) and its
//     ColumnHeader kinds (the implicit Total column's sentinel kind
//     when it has no column axis);
//   - a grouped request with aggregations (SERIES): its grouper kinds;
//   - anything else (SCALAR): no axes.
func RequestAxisSchema(req *types.Request) AxisSchema {
	if req == nil {
		return AxisSchema{}
	}
	if ct := req.Crosstab; ct != nil {
		rows := types.AxisTypes(ct.Rows)
		if ct.HasMeasures() {
			rows = ct.MeasureRowHeader().Types
		}
		return AxisSchema{Rows: rows, Columns: ct.ColumnHeader().Types}
	}
	if len(req.Groups) > 0 && len(req.Aggregations) > 0 {
		return AxisSchema{Rows: types.AxisTypes(req.Groups)}
	}
	return AxisSchema{}
}

// Equal reports whether a and b are structurally identical: each axis
// the same kinds in the same order (nil and empty agree).
func (a AxisSchema) Equal(b AxisSchema) bool {
	return slices.Equal(a.Rows, b.Rows) && slices.Equal(a.Columns, b.Columns)
}

// Canonical renders a as "<rowKinds>/<colKinds>", each half joined by
// "|"; an empty half renders "" so the slash is always present
// ("GROUP_CATEGORY|GROUP_DATE/GROUP_RANGE", "/" for a SCALAR slot).
func (a AxisSchema) Canonical() string {
	return strings.Join(a.Rows, "|") + "/" + strings.Join(a.Columns, "|")
}

// SchemaDivergentRefusal is the axis-schema arm of
// PULSE_OVERLAY_SCHEMA_DIVERGENT for the spec at index: the reference
// and target slots' axis schemas differ (nil when they agree). Details
// {index, kind, reference, target_label, reference_schema,
// target_schema}.
func SchemaDivergentRefusal(index int, kind types.OverlayKind, reference, target string, ref, tgt AxisSchema) *errors.CodedError {
	if ref.Equal(tgt) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT,
		"compose overlay reference and target slots produce structurally different axis schemas",
		map[string]any{
			"index":            index,
			"kind":             string(kind),
			"reference":        reference,
			"target_label":     target,
			"reference_schema": ref.Canonical(),
			"target_schema":    tgt.Canonical(),
		})
}

// CellShapeDivergentRefusal is the cell-shape arm of
// PULSE_OVERLAY_SCHEMA_DIVERGENT: the slots' axis schemas agree but
// their cells carry structurally different payloads (a scalar cell vs
// a Welford triple). Only the runtime reads cell values, so only it
// raises this arm; its wording and details extend the axis arm's
// ({…, reference_cell_shape, target_cell_shape}).
func CellShapeDivergentRefusal(index int, kind types.OverlayKind, reference, target string, schema AxisSchema, refCell, tgtCell string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT,
		"compose overlay reference and target slots produce structurally different cell shapes",
		map[string]any{
			"index":                index,
			"kind":                 string(kind),
			"reference":            reference,
			"target_label":         target,
			"reference_schema":     schema.Canonical(),
			"target_schema":        schema.Canonical(),
			"reference_cell_shape": refCell,
			"target_cell_shape":    tgtCell,
		})
}

// Cell-shape tokens: the canonical spelling of a crosstab cell's payload
// structure the Compose cell-shape arm compares (processing's
// canonicalCellShape renders a cell VALUE into one of them).
const (
	CellShapeScalar       = "scalar"
	CellShapeWelford      = "welford_triple"
	CellShapeLabels       = "[]string"
	CellShapeCounts       = "map[string]int"
	CellShapeWeightCounts = "map[string]float64"
)

// CellShapeOf is the cell-shape token a crosstab whose cell aggregation
// has (routed) type t writes into every present cell, from the request
// alone — predict-compose's twin of the runtime's per-cell probe:
// AGG_SET_UNION / AGG_SET_INTERSECTION a label list, AGG_SET_FREQUENCY
// a per-label count map (Σw under a weight: weighted), every other
// built-in one number (AGG_WELFORD's cell is its mean; the moment
// triple rides the cell components). "" when the type
// alone does not decide it — AGG_CONSTANT (the constant's own type
// does), an array-valued AGG_VEC_* (params.element does), an extension
// or unknown type (builtin false) — and the arm then never refuses.
// processing's TestCellShapeOf_MatchesRuntime holds the table to what
// each built-in writes.
func CellShapeOf(t types.AggregationType, builtin, weighted bool) string {
	if !builtin {
		return ""
	}
	switch {
	case t == types.AGG_SET_UNION || t == types.AGG_SET_INTERSECTION:
		return CellShapeLabels
	case t == types.AGG_SET_FREQUENCY && weighted:
		return CellShapeWeightCounts
	case t == types.AGG_SET_FREQUENCY:
		return CellShapeCounts
	case t == types.AGG_CONSTANT || t.ArrayValued():
		return ""
	}
	return CellShapeScalar
}

// RequestCellShape is CellShapeOf for a Compose slot's request: its
// crosstab's single cell aggregation (route resolves the authored type
// — a hidden one is unknown; weighted reports whether the cell resolves
// an applied weight). "" for a slot with no crosstab, a multi-measure
// crosstab, or an untyped cell.
func RequestCellShape(req *types.Request, route func(types.AggregationType) (types.AggregationType, bool), weighted bool) string {
	if req == nil || req.Crosstab == nil || req.Crosstab.HasMeasures() || req.Crosstab.Cell == nil || req.Crosstab.Cell.Type == "" {
		return ""
	}
	t, builtin := route(req.Crosstab.Cell.Type)
	return CellShapeOf(t, builtin, weighted)
}
