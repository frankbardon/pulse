package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// ExpandRequestItems is the item for_each expansion every surface runs
// before validating a Request — Service.Process (and through it every
// Compose slot), Service.ProcessChain per stage, Service.Materialize,
// Predict, the Compose and chain validators — so the item lists they
// judge and run cannot drift: sweep.ExpandItems with the
// MaxDerivedColumns preflight over l (the instance's effective limits),
// checked BEFORE any item renders. A request without a for_each comes
// back as req itself, after the same limit check over its plain items.
// The caller runs the slot gate (SlotRefusal) first, so a hidden
// `for_each` is an unknown field before its contents are judged.
//
// The returned finding is the Certain MaxDerivedColumns breach when the
// limit refused the request (nil otherwise), for predict's
// LimitFindings.
func ExpandRequestItems(req *types.Request, l limits.Limits) (*types.Request, []sweep.FanOut, *limits.Finding, error) {
	// The for_each-free fast path (every request without a fan-out):
	// the same limit over the plain items, no closure, no clone.
	// Read straight off the struct: limits.Evaluate's by-name lookup
	// allocates, and this path runs on every request.
	if !sweep.HasForEach(req) {
		if c := l.MaxDerivedColumns; !limits.IsUnlimited(c) {
			if n := int64(sweep.DerivedCount(req)); n > c {
				f := limits.Finding{Limit: limits.MaxDerivedColumns, Configured: c, Estimated: n, Grade: limits.Certain}
				return nil, nil, &f, f.Err()
			}
		}
		return req, nil, nil, nil
	}
	var finding *limits.Finding
	out, fan, err := sweep.ExpandItems(req, func(derived int) error {
		if f, ok := limits.Evaluate(l, limits.MaxDerivedColumns, int64(derived), limits.Certain); ok {
			finding = &f
			return f.Err()
		}
		return nil
	})
	if err != nil {
		return nil, nil, finding, err
	}
	return out, fan, nil, nil
}

// forEachSummaries is PredictResult.ForEach for the fan-outs one
// expansion reported.
func forEachSummaries(fan []sweep.FanOut) []descriptor.ForEachSummary {
	if len(fan) == 0 {
		return nil
	}
	out := make([]descriptor.ForEachSummary, len(fan))
	for i, f := range fan {
		axes := make([]descriptor.SweepAxisSummary, len(f.Axes))
		for j, ax := range f.Axes {
			axes[j] = descriptor.SweepAxisSummary{Name: ax.Name, Count: len(ax.Values)}
		}
		out[i] = descriptor.ForEachSummary{Path: f.Path, Axes: axes, Mode: f.Mode, ExpandedCount: f.Count}
	}
	return out
}

// predictForEachRefusal is the envelope Predict returns when the
// expansion refuses req: Valid false, the refusal coded, and the
// MaxDerivedColumns finding when the limit was the refusal. Nothing
// else is judged — the runtime never gets further either.
func predictForEachRefusal(req *types.Request, err error, finding *limits.Finding) *descriptor.Envelope {
	result := newPredictResult(req)
	result.Valid = false
	env := descriptor.NewEnvelope(result)
	if finding != nil {
		result.LimitFindings = limitFindingsDescriptor([]limits.Finding{*finding})
	}
	addCodedError(env, err)
	return env
}

// ExpandChainItems is ExpandRequestItems over every stage of a chain:
// the chain itself when no stage changes, else a shallow clone whose
// changed stages carry their expanded Request. A refusal is located by
// details.stage (RefusalAt).
func ExpandChainItems(req *types.ChainRequest, l limits.Limits) (*types.ChainRequest, error) {
	if req == nil {
		return nil, nil
	}
	var stages []*types.ChainStage
	for i, st := range req.Stages {
		if st == nil || st.Request == nil {
			continue
		}
		out, _, _, err := ExpandRequestItems(st.Request, l)
		if err != nil {
			return nil, RefusalAt(err, "stage", i)
		}
		if out == st.Request {
			continue
		}
		if stages == nil {
			stages = append([]*types.ChainStage(nil), req.Stages...)
		}
		c := *st
		c.Request = out
		stages[i] = &c
	}
	if stages == nil {
		return req, nil
	}
	clone := *req
	clone.Stages = stages
	return &clone, nil
}
