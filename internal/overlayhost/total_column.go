package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The implicit Total column (U47 E2-S1): a crosstab with no column
// grouper materialises ONE column keyed types.CrosstabTotalColumnKey
// under the synthetic header {fields: ["_total"], types: ["TOTAL"]}.
// Predict reads the spec (no column grouper) and runtime the host (one
// header position), so every axis-shape rule an overlay applies must
// read the column the same way on both sides. The two helpers below
// are that one reading.

// HeaderGrouperCount is the number of groupers behind a materialised
// axis header: its field count, except the implicit Total column, whose
// synthetic position stands for no grouper. The square-host arity rule
// (MarkovArityRefusal) counts groupers, so predict's
// len(CrosstabSpec.Columns) and runtime's header agree on zero.
func HeaderGrouperCount(h types.AxisHeader) int {
	if len(h.Types) == 1 && h.Types[0] == types.CrosstabTotalColumnType {
		return 0
	}
	return len(h.Fields)
}

// TotalColumnRefusal refuses, on a crosstab with no column grouper, a
// kind whose handler needs a contingency of at least two columns: the
// χ² matrix / per-row tests, the adjusted residuals and correspondence
// analysis. Their runtime handlers refuse a one-column host from the
// materialised counts (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); the
// implicit Total column is one column by construction, so predict
// refuses it up front with the same code and both sides raise this
// refusal ahead of the handler. route is the kind the rule keys on
// (an extension routed onto one of them counts); messages name the
// authored kind. nil for every other kind.
func TotalColumnRefusal(kind, route types.OverlayKind, index int) *errors.CodedError {
	switch route {
	case types.OverlayKindChiSqMatrix, types.OverlayKindChiSqRow,
		types.OverlayKindStdResidual, types.OverlayKindCorrespondence:
	default:
		return nil
	}
	details := map[string]any{"kind": string(kind), "cols": 1, "reason": "implicit_total_column"}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" requires a host contingency of at least 2 columns; a crosstab with no column grouper has the one implicit \"Total\" column",
		details)
}
