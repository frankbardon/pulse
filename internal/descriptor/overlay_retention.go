package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// validateOverlayRetention enforces the predict contract of
// OVERLAY_RETENTION (route = the instance's routing of the kind; any
// other route returns): Ref must be EMPTY and Scope ROW
// (overlayhost.CrosstabSpecRefusal, the runtime dispatcher's twin), the host must be MATRIX-shaped
// (Request.Crosstab), params must parse (overlayhost.ParseRetentionParams),
// the cell aggregation must be AGG_COUNT or AGG_SUM
// (overlayhost.NonnegativeCellRefusal) and the row and column axes must
// carry the same number of groupers (overlayhost.MarkovArityRefusal —
// otherwise no row key can name a column, so there is no diagonal).
// Whether an absorbing name is a host state is the runtime's call
// (overlayhost.MarkovStateRefusal): predict reads no record. Every
// refusal but the no-crosstab one is the shared overlayhost refusal the
// runtime handler raises — same code and message, plus index.
//
// Errors (first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: a Ref family, or no crosstab.
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED: Scope other than row (a set Ref
//     is judged first).
//   - PULSE_OVERLAY_PARAM_MISSING: a malformed / unknown param.
//   - PULSE_OVERLAY_NONNEGATIVE_REQUIRED: a cell aggregation other
//     than AGG_COUNT / AGG_SUM.
//   - PULSE_OVERLAY_MATRIX_NOT_SQUARE: different grouper counts on the
//     two axes.
func validateOverlayRetention(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) {
	if route != types.OverlayKindRetention || req == nil || spec == nil {
		return
	}
	if cerr := overlayhost.CrosstabSpecRefusal(spec, route, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{"index": index, "kind": string(spec.Kind)})
		return
	}
	if _, cerr := overlayhost.ParseRetentionParams(spec, index); cerr != nil {
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
