package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/types"
)

// validatePreviousState reports, per ATTR_PREVIOUS_STATE slot, the
// refusal the runtime factory raises — the one shared slot rule
// (prevstate.Check: required slots, foreign slot keys, the field types
// of field / subject_field / order_field), so predict and runtime agree
// on code, message and details by construction. A name the schema does
// not carry is the field-reference walk's refusal. The ambiguous-tie
// warning is data-dependent and never predicted. A slot whose type the
// instance hides routes to "" and is skipped, like a never-registered
// name.
func validatePreviousState(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if req == nil {
		return
	}
	for _, attr := range req.Attributes {
		if attr == nil || opRoute(opts.instance(), attr.Type) != types.ATTR_PREVIOUS_STATE {
			continue
		}
		if cerr := prevstate.Check(attr, schema); cerr != nil {
			addCodedError(env, cerr)
		}
	}
}
