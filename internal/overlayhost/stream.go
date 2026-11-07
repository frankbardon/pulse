package overlayhost

import (
	"strconv"

	"github.com/frankbardon/pulse/types"
)

// stream.go is the one streamability rule a request's Request.Overlays
// meet: the runtime's streaming gate (processing.Processor.canStream,
// via canStreamOverlays) routes the request buffered exactly when
// StreamReasons returns a reason, and predict
// (descriptor.computeStreamable) lists the same reasons in
// PredictResult.StreamableReasons, so PredictResult.Streamable and the
// runtime path agree on every overlay-bearing request.

// AuthoredHostShape is the host shape a request DECLARES: MATRIX for a
// crosstab, SERIES for a grouped request with aggregations, SCALAR
// otherwise (processing.AuthoredHostShape delegates here).
func AuthoredHostShape(req *types.Request) types.OverlayShape {
	if req == nil {
		return types.OverlayShapeScalar
	}
	if req.Crosstab != nil {
		return types.OverlayShapeMatrix
	}
	if len(req.Aggregations) > 0 && len(req.Groups) > 0 {
		return types.OverlayShapeSeries
	}
	return types.OverlayShapeScalar
}

// StreamReasons lists, for every Request.Overlays spec that routes req
// down the buffered path, why — nil when every spec streams (or there
// is none). route maps an authored kind onto the kind every decision
// keys on ("" for a kind the instance hides); extension reports an
// embedder-registered kind (on the routed name). A spec routes buffered
// when its kind is:
//
//   - an extension kind — an embedder overlay folds over the finished
//     host, so it is buffered-only;
//   - unknown (no row in types.OverlayStreamability), or hidden — the
//     buffered hook raises its refusal;
//   - on a slot or host shape it does not run on, or a SCALAR host it
//     is refused on — the buffered hook raises the refusal;
//   - declared not streamable (types.OverlayStreamable).
func StreamReasons(req *types.Request, route func(types.OverlayKind) types.OverlayKind, extension func(types.OverlayKind) bool) []string {
	if req == nil || len(req.Overlays) == 0 {
		return nil
	}
	var out []string
	authored := AuthoredHostShape(req)
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		r := route(spec.Kind)
		at := "overlay " + string(spec.Kind) + " (overlays[" + strconv.Itoa(i) + "])"
		if r != "" && extension(r) {
			out = append(out, at+" is an extension overlay kind, which runs buffered only")
			continue
		}
		streamable, known := types.OverlayStreamable(r)
		if !known {
			out = append(out, at+" is not a registered overlay kind")
			continue
		}
		judged := JudgedShape(authored, len(req.Matrices) > 0, r)
		if !Accepts(Request, r) || !AcceptsShape(judged, r) ||
			(judged == types.OverlayShapeScalar && ScalarSpecRefusal(spec, r, len(req.Groups) > 0, i) != nil) {
			out = append(out, at+" does not run on this host")
			continue
		}
		if !streamable {
			out = append(out, at+" is buffered-only")
		}
	}
	return out
}
