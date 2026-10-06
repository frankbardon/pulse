// Package returnshape applies a resolved `return` plan to a finished
// Response. It is the runtime half of response shaping: the facade
// resolves the plan (internal/descriptor.ResolveReturn — the pass
// predict runs) and calls Apply on the response it is about to hand
// back. Apply works on ONE Response, so every surface that returns
// responses (Process today; Compose slots, chain stages and streamed
// rows next) shapes through it.
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
