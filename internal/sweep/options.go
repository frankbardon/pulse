package sweep

import "github.com/frankbardon/pulse/internal/template"

// Options carries the instance facts an expansion's checks read beyond
// the request itself — the one value Expand (the compose sweep) and
// ExpandItems (item for_each) take, so a later fact reaches both. The
// zero value judges against the built-in slots only. Plain data: the
// runtime (internal/service) and predict (internal/descriptor) fill it
// from the instance, so this package never learns about the instance
// it serves and its import ceiling (TestSweep_ImportBoundary) holds.
type Options struct {
	// ExprParams maps an operator type name to the params keys that
	// carry a scalar expression on this instance — the VISIBLE embedder
	// extensions' Expression-flagged params
	// (processing.ExtensionRegistry.ScalarParamsByOperator at runtime,
	// descriptor InstanceSnapshot.ExprParamsByOperator at predict; a
	// hidden extension flags nothing). An axis `{{token}}` in the string
	// at params.<key> of an object whose literal `type` flags <key> is
	// PULSE_SWEEP_EXPR_SPLICE / PULSE_FOR_EACH_EXPR_SPLICE, slot
	// exprslots.SlotExtensionParam, exactly as in a built-in expression
	// slot. It is the template validator's
	// template.ValidateOptions.ExprParams, matched by the same walk.
	ExprParams map[string][]string
}

// validate is the template validator's view of o.
func (o Options) validate() template.ValidateOptions {
	return template.ValidateOptions{ExprParams: o.ExprParams}
}
