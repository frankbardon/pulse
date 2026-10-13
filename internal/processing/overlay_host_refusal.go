package processing

import (
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// refuseWrongHostOverlays is the runtime twin of the predict pre-pass in
// descriptor.ValidateOverlays: the first Request.Overlays spec whose
// kind does not run on a Process host (a Compose, whole-chain or FACET
// kind) is refused with the shared overlayhost slot refusal, and the
// first whose kind does not run on the request's AUTHORED host shape
// (AuthoredHostShape: a SERIES kind over a crosstab, a MATRIX kind over
// a grouped request) with the shared shape refusal — both
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details
// {index, kind, host, hosts} and {index, kind, host, shape, shapes}.
// Both Process hooks (the crosstab applyOverlaysToResponse and the
// grouped applyOverlaysSeriesToResponse) call it before any payload
// guard, so the refusal is a property of the request: an empty result,
// a long-shape crosstab or an ungrouped run refuses it the way predict
// does. keep is the ComputePlan's overlay mask (nil: every spec); a
// skipped spec raises nothing.
func refuseWrongHostOverlays(req *types.Request, exts *ExtensionRegistry, keep func(int) bool) error {
	if req == nil {
		return nil
	}
	shape := AuthoredHostShape(req)
	matrices := len(req.Matrices) > 0
	for i := range req.Overlays {
		if keep != nil && !keep(i) {
			continue
		}
		route := exts.overlayRoute(req.Overlays[i].Kind)
		if cerr := overlayhost.Refusal(overlayhost.Request, req.Overlays[i].Kind, route, i); cerr != nil {
			return cerr
		}
		// A request carrying matrices offers the MATRIX_RESULT host too
		// (overlayhost.JudgedShape).
		if cerr := overlayhost.ShapeRefusal(overlayhost.JudgedShape(shape, matrices, route), req.Overlays[i].Kind, route, i); cerr != nil {
			return cerr
		}
	}
	return nil
}

// WrongHostRefusal is the shared wrong-slot refusal (overlayhost.Refusal)
// for the spec of kind at index on host, judged on the route exts gives
// the kind: a kind the instance hides is never refused here (its slot's
// unknown-kind branch owns it). nil when the slot runs the kind. For a
// caller outside this package that dispatches one spec at a time — the
// FACET overlay loop in internal/service — and so must raise it with the
// spec's own index before its per-spec work.
func WrongHostRefusal(host overlayhost.Host, kind types.OverlayKind, exts *ExtensionRegistry, index int) error {
	if cerr := overlayhost.Refusal(host, kind, exts.overlayRoute(kind), index); cerr != nil {
		return cerr
	}
	return nil
}
