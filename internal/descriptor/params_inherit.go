package descriptor

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// Top-level params inheritance (U58 E3-S2).
//
// A ComposedRequest or ChainRequest may declare `params` of its own;
// every sub-request inherits them — a Compose slot (explicit, sweep-
// or banner-expanded), a Compose fit request, a chain stage — and a
// name the sub-request declares itself wins. The merge is part of
// normalization: the runtime (Service.expandCompose, processChain) and
// predict (validateComposeExpanded, validateChainWithOptions) call the
// same function at the same point, so both judge the same merged
// sub-requests and the echoed normalized request carries them.
//
// Neither function mutates its argument: a merged sub-request is a
// shallow copy with a fresh Params map, so a caller re-running the same
// request with other top-level values never meets a stale merge.

// InheritComposeParams returns c with its top-level Params merged into
// every slot and every fit request (each sub-request's own key wins).
// c itself when it declares none. The top-level block is validated
// first (exprenv.NormalizeParams): an invalid one is
// PULSE_PARAMS_INVALID, whatever the sub-requests override. Call it on
// the expanded request (sweep and banner slots already appended).
func InheritComposeParams(c *types.ComposedRequest) (*types.ComposedRequest, *errors.CodedError) {
	if c == nil || len(c.Params) == 0 {
		return c, nil
	}
	if _, cerr := exprenv.NormalizeParams(c.Params); cerr != nil {
		return nil, cerr
	}
	out := *c
	out.Requests = make([]*types.Request, len(c.Requests))
	for i, r := range c.Requests {
		out.Requests[i] = withInheritedParams(r, c.Params)
	}
	if len(c.Fits) > 0 {
		out.Fits = make([]types.FitSpec, len(c.Fits))
		for i, f := range c.Fits {
			f.Request = withInheritedParams(f.Request, c.Params)
			out.Fits[i] = f
		}
	}
	return &out, nil
}

// InheritChainParams returns c with its top-level Params merged into
// every stage's request (each stage's own key wins), c itself when it
// declares none. The top-level block is validated first, as
// InheritComposeParams does.
func InheritChainParams(c *types.ChainRequest) (*types.ChainRequest, *errors.CodedError) {
	if c == nil || len(c.Params) == 0 {
		return c, nil
	}
	if _, cerr := exprenv.NormalizeParams(c.Params); cerr != nil {
		return nil, cerr
	}
	out := *c
	out.Stages = make([]*types.ChainStage, len(c.Stages))
	for i, st := range c.Stages {
		if st == nil {
			continue
		}
		cp := *st
		cp.Request = withInheritedParams(st.Request, c.Params)
		out.Stages[i] = &cp
	}
	return &out, nil
}

// withInheritedParams is a shallow copy of r whose Params are parent
// overlaid by r's own; nil stays nil.
func withInheritedParams(r *types.Request, parent types.Params) *types.Request {
	if r == nil {
		return nil
	}
	merged := make(types.Params, len(parent)+len(r.Params))
	for k, v := range parent {
		merged[k] = v
	}
	for k, v := range r.Params {
		merged[k] = v
	}
	cp := *r
	cp.Params = merged
	return &cp
}
