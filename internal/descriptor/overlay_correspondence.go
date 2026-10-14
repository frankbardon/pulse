package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// correspondenceParts declares the named parts OVERLAY_CORRESPONDENCE
// writes beside its primary inertia series, each with its payload
// shape. overlayPartShapes reads it.
var correspondenceParts = map[string]overlayPart{
	"row_coordinates":     {Shape: types.OverlayShapeMatrix},
	"column_coordinates":  {Shape: types.OverlayShapeMatrix},
	"row_quality":         {Shape: types.OverlayShapeSeries},
	"column_quality":      {Shape: types.OverlayShapeSeries},
	"row_contribution":    {Shape: types.OverlayShapeMatrix},
	"column_contribution": {Shape: types.OverlayShapeMatrix},
	"row_distance":        {Shape: types.OverlayShapeMatrix},
	"column_distance":     {Shape: types.OverlayShapeMatrix},
}

// validateOverlayCorrespondence enforces the predict contract of
// OVERLAY_CORRESPONDENCE (route = the instance's routing of the kind;
// any other route returns): Ref must be EMPTY (implicit-margin), the
// host must be MATRIX-shaped (Request.Crosstab), Scope must be MATRIX,
// params must parse (overlayhost.ParseCorrespondenceParams), the cell
// aggregation must be AGG_COUNT or AGG_SUM
// (overlayhost.NonnegativeCellRefusal) and an explicit dimensions must
// not exceed min(r, c) − 1 when the axes' bucket counts are known from
// the dictionaries (axisBuckets; the runtime judges the kept table,
// which zero-margin drops can shrink further). Every refusal but the
// shape / scope ones is the shared overlayhost refusal the runtime
// handler raises — same code and message, plus index.
//
// Errors (first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: a Ref family, or no crosstab.
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED: Scope other than matrix.
//   - PULSE_OVERLAY_PARAM_MISSING: a malformed / unknown param, or
//     dimensions above the known cap.
//   - PULSE_OVERLAY_NONNEGATIVE_REQUIRED: a cell aggregation other
//     than AGG_COUNT / AGG_SUM.
func validateOverlayCorrespondence(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, schema *encoding.Schema, inst *InstanceSnapshot, route types.OverlayKind, index int) {
	if route != types.OverlayKindCorrespondence || req == nil || spec == nil {
		return
	}
	if spec.Ref != (types.OverlayRef{}) {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the analysis reads the whole table)",
			map[string]any{"index": index, "kind": string(spec.Kind)})
		return
	}
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{"index": index, "kind": string(spec.Kind)})
		return
	}
	if spec.Scope != types.OverlayScopeMatrix {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: matrix)",
			map[string]any{"index": index, "kind": string(spec.Kind), "scope": string(spec.Scope)})
		return
	}
	params, cerr := overlayhost.ParseCorrespondenceParams(spec, index)
	if cerr != nil {
		addCodedError(env, cerr)
		return
	}
	if cell := req.Crosstab.Cell; cell != nil {
		if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, cell.Type, index); cerr != nil {
			addCodedError(env, cerr)
			return
		}
	}
	rows := axisBuckets(req.Crosstab.Rows, schema, inst)
	cols := axisBuckets(req.Crosstab.Columns, schema, inst)
	if rows > 0 && cols > 0 {
		if cerr := overlayhost.CorrespondenceDimensionsRefusal(spec.Kind, params, rows, cols, index); cerr != nil {
			addCodedError(env, cerr)
		}
	}
}
