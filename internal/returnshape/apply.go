// Package returnshape applies a resolved `return` plan to a finished
// Response. It is the runtime half of response shaping: the facade
// resolves the plan (internal/descriptor.ResolveReturn — the pass
// predict runs) and calls Apply on the response it is about to hand
// back. Apply works on ONE Response, so every surface that returns
// responses (Process today; Compose slots, chain stages and streamed
// rows next) shapes through it. ApplyComposed and ApplyChain are the
// multi-response roots built on it.
//
// Shaping runs at the OUTERMOST facade only, never inside
// Service.Process: Compose overlays and chain stages re-enter the
// service and read the finished, unshaped responses.
package returnshape

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

var warningsPath = []returnplan.Segment{returnplan.Key("warnings")}

// Apply shapes resp under plan in place. A nil or identity plan changes
// nothing (the byte-identity fast path). Otherwise every excluded slot
// is pruned from the Go value, the plan is attached so the response's
// MarshalJSON writes only the selected parts, an Open include that
// matched nothing raises PULSE_RETURN_PATH_UNMATCHED on
// resp.Warnings (unless the plan excludes warnings), and
// resp.Returned stamps the selection.
func Apply(resp *types.Response, plan *returnplan.Plan) {
	if resp == nil || plan.Identity() {
		return
	}
	unmatched, _ := returnplan.Apply(resp, plan)
	if len(unmatched) > 0 && plan.Visit(warningsPath).Keep {
		for _, p := range unmatched {
			path := p.String()
			resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
				Code:    string(errors.PULSE_RETURN_PATH_UNMATCHED),
				Message: "return include " + strconv.Quote(path) + " matched nothing in this response",
				Details: map[string]any{"path": path},
			})
		}
	}
	resp.Returned = &types.ReturnedMarker{
		Preset:    plan.Preset,
		Digest:    plan.Digest,
		Precision: plan.Precision,
	}
}

// ApplyComposed shapes a finished Compose result in place: slot i under
// slotPlans[i] (Apply; a missing or nil plan leaves the slot whole) and
// the top-level overlays under top, the Compose-level plan rooted at
// ComposedResponse. Call it only after the overlay and multiplicity
// folds, so every layer was computed from the unshaped slots. A
// non-identity top plan stamps ComposedResponse.Returned; the top
// level has no warnings slot, so an unmatched Open include there is
// not reported.
func ApplyComposed(out *types.ComposedResponse, slotPlans []*returnplan.Plan, top *returnplan.Plan) {
	if out == nil {
		return
	}
	for i, resp := range out.Responses {
		if i < len(slotPlans) {
			Apply(resp, slotPlans[i])
		}
	}
	if top.Identity() {
		return
	}
	_, _ = returnplan.Apply(out, top)
	out.Returned = &types.ReturnedMarker{
		Preset:    top.Preset,
		Digest:    top.Digest,
		Precision: top.Precision,
	}
}

// ApplyChain shapes a finished chain in place: stage i under
// stagePlans[i]. Call it only after the WHOLE chain (and its overlay
// barrier) completed, so no later stage reads pruned rows. Final
// aliases the last stage, so it follows the last stage's plan; it is
// never shaped twice.
func ApplyChain(out *types.ChainResponse, stagePlans []*returnplan.Plan) {
	if out == nil {
		return
	}
	for i, resp := range out.Stages {
		if i < len(stagePlans) {
			Apply(resp, stagePlans[i])
		}
	}
}
