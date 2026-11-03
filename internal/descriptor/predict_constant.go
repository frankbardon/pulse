package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/aggconst"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// predict_constant.go is predict's half of the AGG_CONSTANT reading
// (internal/aggconst): the slot shape (no field, a label), the params
// (type, exactly one of value / expr, the literal against the type) and
// the static judgement of params.expr over the request's params and the
// catalog its references resolve against — the twin of the runtime's
// pre-resolution pass (processing.ResolveExtensionScalars) and the
// aggregator factory's reading.

// constantSlots are req's AGG_CONSTANT aggregation slots, walk order
// (aggregations, the crosstab cell or cells, its margin aggregations).
// A hidden AGG_CONSTANT is an unknown aggregation, judged elsewhere.
func constantSlots(req *types.Request, inst *InstanceSnapshot) []vectors.AggregationSlot {
	if req == nil || !hasConstantSlot(req) || opRoute(inst, types.AGG_CONSTANT) == "" {
		return nil
	}
	var out []vectors.AggregationSlot
	for _, s := range vectors.AggregationSlots(req) {
		if s.Agg.Type == types.AGG_CONSTANT {
			out = append(out, s)
		}
	}
	return out
}

// validateConstantSlots reports every AGG_CONSTANT refusal of req, in
// slot walk order, each located at its slot.
func validateConstantSlots(env *descriptor.Envelope, req *types.Request, opts *PredictOptions) {
	slots := constantSlots(req, opts.instance())
	if len(slots) == 0 {
		return
	}
	senv := windowScalarEnv(req, opts)
	for _, s := range slots {
		if r := aggconst.CheckSlot(s.Agg); r != nil {
			addCodedError(env, r.At(s.Path))
			continue
		}
		if _, _, _, r := aggconst.Eval(s.Agg.Params, senv, senv.Declared(), false); r != nil {
			addCodedError(env, r.At(s.Path))
		}
	}
}

// constantUnresolved lists req's AGG_CONSTANT expressions the static
// judgement left to the runtime (they call a request-bound function),
// each path prefix + "<slot>.params.expr". Nil when none.
func constantUnresolved(req *types.Request, opts *PredictOptions, prefix string) []descriptor.UnresolvedScalar {
	slots := constantSlots(req, opts.instance())
	if len(slots) == 0 {
		return nil
	}
	var senv *scalarslot.Env
	var out []descriptor.UnresolvedScalar
	for _, s := range slots {
		if !aggconst.HasExpr(s.Agg) {
			continue
		}
		if senv == nil {
			e := windowScalarEnv(req, opts)
			senv = &e
		}
		_, un, _, _ := aggconst.Eval(s.Agg.Params, *senv, senv.Declared(), false)
		if un != nil {
			out = append(out, descriptor.UnresolvedScalar{Path: prefix + s.Path + ".params." + aggconst.ParamExpr, Expression: un.Expression, Reads: un.Reads})
		}
	}
	return out
}

// hasConstantSlot reports whether req names AGG_CONSTANT in any
// aggregation slot, without building the slot list: predict's
// allocation baseline (TestObservabilityOffNoAllocs) holds for a
// request with no constant.
func hasConstantSlot(req *types.Request) bool {
	is := func(as []*types.Aggregation) bool {
		for _, a := range as {
			if a != nil && a.Type == types.AGG_CONSTANT {
				return true
			}
		}
		return false
	}
	if is(req.Aggregations) {
		return true
	}
	if ct := req.Crosstab; ct != nil {
		if ct.Cell != nil && ct.Cell.Type == types.AGG_CONSTANT {
			return true
		}
		return is(ct.Cells) || is(ct.MarginAggregations)
	}
	return false
}
