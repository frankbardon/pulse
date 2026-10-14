package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// validateOverlaySymmetry enforces the predict contract of
// OVERLAY_SYMMETRY (route = the instance's routing of the kind; any
// other route returns): Ref must be EMPTY (implicit-margin) and Scope
// MATRIX (overlayhost.CrosstabSpecRefusal, the runtime dispatcher's
// twin), the host must be MATRIX-shaped (Request.Crosstab), params
// must parse (overlayhost.ParseSymmetryParams), the cell aggregation
// must be AGG_COUNT or AGG_SUM (overlayhost.NonnegativeCellRefusal) and
// the row and column axes must carry the same number of groupers
// (overlayhost.MarkovArityRefusal — the square-host rule the request
// shape decides; which keys each axis holds is the runtime's, and a
// one-axis key is a zero row / column there, never a refusal). Every
// refusal but the no-crosstab one is the shared overlayhost refusal
// the runtime handler raises — same code and message, plus index.
//
// Errors (first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: a Ref family, or no crosstab.
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED: Scope other than matrix (a set Ref
//     is judged first).
//   - PULSE_OVERLAY_PARAM_MISSING: a malformed / unknown param.
//   - PULSE_OVERLAY_NONNEGATIVE_REQUIRED: a cell aggregation other
//     than AGG_COUNT / AGG_SUM.
//   - PULSE_OVERLAY_MATRIX_NOT_SQUARE: different grouper counts on the
//     two axes.
func validateOverlaySymmetry(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) {
	if route != types.OverlayKindSymmetry || req == nil || spec == nil {
		return
	}
	if cerr := overlayhost.CrosstabSpecRefusal(spec, route, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}
	if req.Crosstab == nil {
		addHostRequired(env, spec, route, len(req.Groups) > 0, index)
		return
	}
	if _, cerr := overlayhost.ParseSymmetryParams(spec, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}
	if cell := req.Crosstab.Cell; cell != nil {
		if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, cell.Type, index); cerr != nil {
			addCodedError(env, cerr)
			return
		}
	}
	if cerr := overlayhost.MarkovArityRefusal(spec.Kind, len(req.Crosstab.Rows), len(req.Crosstab.Columns), index); cerr != nil {
		addCodedError(env, cerr)
	}
}
