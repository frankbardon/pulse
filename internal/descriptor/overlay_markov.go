package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// markovParts declares the fixed-name parts OVERLAY_MARKOV writes beside
// its primary transition matrix, each with its payload shape.
// overlayPartShapes reads it. The per-step parts step_<n> (MATRIX) and
// distribution_<n> (SERIES) are named by params.steps, so they are not
// listed: an Interpretation cannot address them by a fixed path.
var markovParts = map[string]types.OverlayShape{
	"steady_state":    types.OverlayShapeSeries,
	"absorption_time": types.OverlayShapeSeries,
}

// validateOverlayMarkov enforces the predict contract of OVERLAY_MARKOV
// (route = the instance's routing of the kind; any other route
// returns): Ref must be EMPTY (implicit-margin), the host must be
// MATRIX-shaped (Request.Crosstab), Scope must be MATRIX, params must
// parse (overlayhost.ParseMarkovParams), the cell aggregation must be
// AGG_COUNT or AGG_SUM (overlayhost.NonnegativeCellRefusal) and the row
// and column axes must carry the same number of groupers
// (overlayhost.MarkovArityRefusal — the one square-host rule the request
// shape alone decides; the key-set equality and every named state are
// the runtime's call, predict reads no record). Every refusal but the
// shape / scope ones is the shared overlayhost refusal the runtime
// handler raises — same code and message, plus index.
//
// Errors (first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: a Ref family, or no crosstab.
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED: Scope other than matrix.
//   - PULSE_OVERLAY_PARAM_MISSING: a malformed / unknown param.
//   - PULSE_OVERLAY_NONNEGATIVE_REQUIRED: a cell aggregation other
//     than AGG_COUNT / AGG_SUM.
//   - PULSE_OVERLAY_MATRIX_NOT_SQUARE: different grouper counts on the
//     two axes.
func validateOverlayMarkov(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) {
	if route != types.OverlayKindMarkov || req == nil || spec == nil {
		return
	}
	if spec.Ref != (types.OverlayRef{}) {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the chain reads the whole table)",
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
	if _, cerr := overlayhost.ParseMarkovParams(spec, index); cerr != nil {
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
