package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
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
// own Request.Overlays carries a MATRIX_RESULT kind (as the instance
// routes it): that host reaches a process request and a Compose slot
// only (overlayhost.StageRefusal), located by details.stage. Predict
// (ValidateChain) and the chain runtime (service.processChain) both run
// it after the stages' models, before the cohort opens.
func ChainStageOverlayRefusal(stages []*types.ChainStage, inst *InstanceSnapshot) error {
	for s, st := range stages {
		if st == nil || st.Request == nil {
			continue
		}
		for i := range st.Request.Overlays {
			kind := st.Request.Overlays[i].Kind
			route := kind
			if inst.Hidden(string(kind)) {
				route = ""
			}
			if cerr := overlayhost.StageRefusal(kind, route, s, i); cerr != nil {
				return cerr
			}
		}
	}
	return nil
}
