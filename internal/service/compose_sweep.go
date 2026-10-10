package service

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// composeRank is a sweep's rank, carried from the as-written request
// (the effective request has no sweep) to the finished batch: the spec
// and the index of the first sweep slot.
type composeRank struct {
	spec     *types.SweepRank
	explicit int
}

// expandCompose returns the effective request Compose and
// ComposeParallel run: the ComposedRequest with its sweep expanded into
// slots appended after the explicit ones (sweep.Expand, the expansion
// predict shares). With EchoRequest on, it is what
// ComposedResponse.NormalizedRequest carries. A sweep-free request
// comes back as the same pointer, after the MaxComposeSlots preflight
// it always ran. The rank is non-nil only when the sweep sets one.
//
// A set sweep on an instance that hides it is refused as an unknown
// field BEFORE its contents are judged, exactly as predict refuses it;
// then the limit is checked on explicit + expanded slots before a single
// sweep slot is rendered, so no product of axes bypasses it.
func (s *Service) expandCompose(composed *types.ComposedRequest) (*types.ComposedRequest, *composeRank, error) {
	if composed == nil || (len(composed.Requests) == 0 && composed.Sweep == nil) {
		return nil, nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "composed request must contain at least one request")
	}
	if composed.Sweep != nil {
		if err := s.slotRefusal(composed); err != nil {
			return nil, nil, err
		}
	}
	exp, err := sweep.Expand(composed, s.composeSlotsPreflight)
	if err != nil {
		return nil, nil, err
	}
	var rank *composeRank
	if composed.Sweep != nil && composed.Sweep.Rank != nil {
		rank = &composeRank{spec: composed.Sweep.Rank, explicit: exp.Explicit}
	}
	return exp.Composed, rank, nil
}

// rankVetoKey marks a ranked sweep slot's ctx: the slot computes every
// part its own `return` would skip, so the rank reads the unshaped
// response (shaping still trims the wire afterwards).
type rankVetoKey struct{}

// rankSlotContext is ctx for slot i, marked with the rank veto when i is
// a sweep slot of a ranked sweep.
func rankSlotContext(ctx context.Context, rank *composeRank, i int) context.Context {
	if rank != nil && i >= rank.explicit {
		ctx = context.WithValue(ctx, rankVetoKey{}, true)
	}
	return ctx
}

// rankVetoed reports whether ctx carries the rank veto.
func rankVetoed(ctx context.Context) bool {
	v, _ := ctx.Value(rankVetoKey{}).(bool)
	return v
}

// applyComposeRank fills out.Ranking from the sweep slots (the slots
// from rank.explicit on, labelled by the resolved requests). It runs
// after every fold, before the facade shapes anything, so each slot is
// read unshaped. No rank leaves out untouched.
func applyComposeRank(rank *composeRank, requests []*types.Request, out *types.ComposedResponse) error {
	if rank == nil {
		return nil
	}
	n := len(out.Responses) - rank.explicit
	if n < 0 {
		n = 0
	}
	labels := make([]string, n)
	for i := range labels {
		if r := requests[rank.explicit+i]; r != nil {
			labels[i] = r.Label
		}
	}
	ranking, err := sweep.Rank(rank.spec, labels, out.Responses[rank.explicit:])
	if err != nil {
		return err
	}
	out.Ranking = ranking
	return nil
}
