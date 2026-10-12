package processing

import "github.com/frankbardon/pulse/types"

// AuthoredHostShape is the host shape a Compose slot or chain stage
// DECLARES: MATRIX for a crosstab, SERIES for a grouped request with
// aggregations, SCALAR otherwise (no aggregations, or no groups). It is
// the runtime twin of the no-execute descriptor.inferComposeSlotShape /
// inferChainStageShape, which predict reads off the same Request
// (TestAuthoredHostShape_MatchesDescriptor pins the three in lockstep).
//
// The materialised Response cannot answer this on its own: an
// UNGROUPED Process result carries its one aggregate row in Data, so
// a Data-based probe calls it SERIES, and a grouped slot paired with
// it then reads as a key-set divergence (Compose) or a SERIES fold
// whose single "" key matches nothing (chain) — where predict, reading
// the request, says the shapes differ. The overlay gates judge shape
// from the authored request whenever one is in reach, so both sides
// refuse a grouped-vs-ungrouped pair under the shape code.
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

// hostShapeAt is the shape the overlay gates judge a slot / stage by:
// the authored request's (AuthoredHostShape) when requests[idx] is in
// reach, else the Response probe (fallback) — the bare
// ApplyComposeOverlays / ApplyChainOverlays entries carry no requests.
func hostShapeAt(requests []*types.Request, idx int, fallback types.OverlayShape) types.OverlayShape {
	if idx < 0 || idx >= len(requests) || requests[idx] == nil {
		return fallback
	}
	return AuthoredHostShape(requests[idx])
}

// composeSlotShapes carries the judged host shape of a Compose spec's
// reference and of each target (targets parallel targetResps).
type composeSlotShapes struct {
	ref     types.OverlayShape
	targets []types.OverlayShape
}

// responseSlotShapes probes every slot's shape off its Response alone —
// the shapes the response-only gate entry points judge by.
func responseSlotShapes(refResp *types.Response, targetResps []*types.Response) composeSlotShapes {
	out := composeSlotShapes{ref: extractSchemaShape(refResp).shape, targets: make([]types.OverlayShape, len(targetResps))}
	for i, t := range targetResps {
		out.targets[i] = extractSchemaShape(t).shape
	}
	return out
}

// authoredSlotShapes is responseSlotShapes with each slot's authored
// request (requests[refIdx], requests[targetIdxs[i]]) deciding its
// shape whenever it is in reach.
func authoredSlotShapes(refResp *types.Response, targetResps []*types.Response, refIdx int, targetIdxs []int, requests []*types.Request) composeSlotShapes {
	out := responseSlotShapes(refResp, targetResps)
	out.ref = hostShapeAt(requests, refIdx, out.ref)
	for i := range out.targets {
		idx := -1
		if i < len(targetIdxs) {
			idx = targetIdxs[i]
		}
		out.targets[i] = hostShapeAt(requests, idx, out.targets[i])
	}
	return out
}
