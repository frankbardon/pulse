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
// {index, kind, host, hosts} and {index, kind, host, shape, shapes} —
// and, on a SERIES host, the first whose Ref or Scope breaks the shared
// SERIES rule (overlayhost.SeriesSpecRefusal), and on a SCALAR host the
// first spec at all (overlayhost.ScalarSpecRefusal: nothing runs there).
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
		judged := overlayhost.JudgedShape(shape, matrices, route)
		if cerr := overlayhost.ShapeRefusal(judged, req.Overlays[i].Kind, route, i); cerr != nil {
			return cerr
		}
		// On a SERIES host, the shared SERIES Ref / Scope rule predict's
		// per-kind validators raise first. Here, before every payload
		// guard, it holds on the buffered, streaming and parallel tails
		// alike and on an empty result. (The crosstab rule runs in the
		// MATRIX dispatcher, applyOverlaysPlanned.)
		if judged == types.OverlayShapeSeries {
			if cerr := overlayhost.SeriesSpecRefusal(&req.Overlays[i], route, i); cerr != nil {
				return cerr
			}
		}
		// On a SCALAR host (no crosstab, no matrices, no grouped
		// aggregations) nothing runs: every Process kind is refused with
		// predict's host refusal (overlayhost.ScalarSpecRefusal — the
		// kind's Ref / Scope table first, then the host it needs).
		if judged == types.OverlayShapeScalar {
			if cerr := overlayhost.ScalarSpecRefusal(&req.Overlays[i], route, len(req.Groups) > 0, i); cerr != nil {
				return cerr
			}
		}
		// A crosstab with no column grouper has the one implicit Total
		// column: a kind needing two columns is refused up front, the
		// refusal predict raises (overlayhost.TotalColumnRefusal).
		if req.Crosstab.HasImplicitTotalColumn() {
			if cerr := overlayhost.TotalColumnRefusal(req.Overlays[i].Kind, route, i); cerr != nil {
				return cerr
			}
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

// FacetSpecRefusal is the shared FACET-host Ref / Scope / Level refusal
// (overlayhost.FacetSpecRefusal) for the FacetRequest.Overlays spec at
// index, judged on the route exts gives its kind, for the runtime FACET
// loop in internal/service, which raises it right after
// WrongHostRefusal — the point predict (validateFacetOverlaySpec)
// raises it. nil when the spec passes.
func FacetSpecRefusal(spec *types.OverlaySpec, exts *ExtensionRegistry, index int) error {
	if spec == nil {
		return nil
	}
	if cerr := overlayhost.FacetSpecRefusal(spec, exts.overlayRoute(spec.Kind), index); cerr != nil {
		return cerr
	}
	return nil
}

// RefuseWrongHostOverlays is refuseWrongHostOverlays for a runtime arm
// outside this package that finalizes a Process result without either
// Process hook — the shard-parallel ungrouped tail
// (service.finalizeMergedPartial) — so an ungrouped request's overlays
// meet the SCALAR host refusal there too. No-op when the plan computes
// no overlay; compute's overlay mask applies.
func RefuseWrongHostOverlays(req *types.Request, exts *ExtensionRegistry, compute ComputePlan) error {
	if req == nil || len(req.Overlays) == 0 || !compute.Overlays {
		return nil
	}
	return refuseWrongHostOverlays(req, exts, compute.overlayKeep())
}
