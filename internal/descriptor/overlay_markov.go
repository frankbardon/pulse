package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// markovParts declares the parts OVERLAY_MARKOV writes beside its
// primary transition matrix, each with its payload shape and condition.
// overlayPartShapes reads it. The per-step parts step_<n> (MATRIX) and
// distribution_<n> (SERIES) are templates named by params.steps: the
// manifest lists them, but an Interpretation cannot address them by a
// fixed path (overlayPartShape).
var markovParts = map[string]overlayPart{
	"step_<n>":         {Shape: types.OverlayShapeMatrix, When: "one per params.steps entry (default 1, 3, 6, 12)"},
	"distribution_<n>": {Shape: types.OverlayShapeSeries, When: "one per params.steps entry, only with params.initial"},
	"steady_state":     {Shape: types.OverlayShapeSeries, When: "only for an irreducible aperiodic chain"},
	"absorption_time":  {Shape: types.OverlayShapeSeries, When: "only with params.absorbing"},
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
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
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
