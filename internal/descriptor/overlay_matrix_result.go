package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// matrixGroupSummaryParts declares the named parts
// OVERLAY_MATRIX_GROUP_SUMMARY writes beside its primary per-cell mean,
// with their payload shapes. overlayPartShapes reads it.
var matrixGroupSummaryParts = func() map[string]overlayPart {
	out := map[string]overlayPart{}
	for _, p := range overlayhost.GroupSummaryParts() {
		out[p] = overlayPart{Shape: types.OverlayShapeMatrix}
	}
	return out
}()

// validateOverlayMatrixResult is the predict twin of the MATRIX_RESULT
// runtime dispatcher (processing.ApplyMatrixResultOverlays): for a spec
// whose route runs on the MATRIX_RESULT host it raises the shared
// overlayhost.MatrixResultRefusal — Ref.Matrix arm, scope, Level /
// Within, params, the named spec's existence and Request.Groups —
// identically to the runtime (code, message and details).
func validateOverlayMatrixResult(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) {
	if cerr := overlayhost.MatrixResultRefusal(req, spec, route, index); cerr != nil {
		addCodedError(env, cerr)
	}
}

// ChainStageOverlayRefusal refuses the first ProcessChain stage whose
// own Request.Overlays carries a spec its stage cannot run, located by
// details.stage. Predict (ValidateChain) and the chain runtime
// (service.processChain) both run it after the stages' models, before
// the cohort opens, so both refuse with one code, message and details
// (predict-chain used to judge none of a stage's own overlays). Per
// spec, in order, keyed on the kind as the instance routes it:
//
//   - a MATRIX_RESULT kind: that host reaches a process request and a
//     Compose slot only (overlayhost.StageRefusal);
//   - a kind that does not run on Request.Overlays (overlayhost.Refusal);
//   - a kind over the wrong Process shape of the stage's authored
//     request (overlayhost.ShapeRefusal);
//   - a Ref or Scope its host's shared rule refuses
//     (overlayhost.ProcessSpecRefusal: the SERIES table on a grouped
//     stage). The runtime stage hooks raise the same three, unlocated.
func ChainStageOverlayRefusal(stages []*types.ChainStage, inst *InstanceSnapshot) error {
	for s, st := range stages {
		if st == nil || st.Request == nil {
			continue
		}
		shape := inferComposeSlotShape(st.Request)
		matrices := len(st.Request.Matrices) > 0
		for i := range st.Request.Overlays {
			spec := &st.Request.Overlays[i]
			kind := spec.Kind
			route := kind
			if inst.Hidden(string(kind)) {
				route = ""
			}
			if cerr := overlayhost.StageRefusal(kind, route, s, i); cerr != nil {
				return cerr
			}
			judged := overlayhost.JudgedShape(shape, matrices, route)
			for _, cerr := range []*errors.CodedError{
				overlayhost.Refusal(overlayhost.Request, kind, route, i),
				overlayhost.ShapeRefusal(judged, kind, route, i),
				overlayhost.ProcessSpecRefusal(judged, spec, route, i),
			} {
				if cerr != nil {
					return RefusalAt(cerr, "stage", s)
				}
			}
		}
	}
	return nil
}
