package service

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/banner"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// composeRank is a sweep's rank, carried from the as-written request
// (the effective request has no sweep) to the finished batch: the spec
// the index of the first sweep slot and the number of sweep slots (any
// banner slots follow them).
type composeRank struct {
	spec     *types.SweepRank
	explicit int
	count    int
}

// composeExpansion is what the as-written request hands the finished
// batch: the sweep rank and the banner layout (each nil when unset).
type composeExpansion struct {
	rank   *composeRank
	banner *banner.Layout
}

// expandCompose returns the effective request Compose and
// ComposeParallel run: the ComposedRequest with its sweep, then its
// banner, expanded into slots appended after the explicit ones
// (banner.ExpandCompose, the expansion predict shares). With
// EchoRequest on, it is what ComposedResponse.NormalizedRequest
// carries. A sweep- and banner-free request comes back as the same
// pointer, after the MaxComposeSlots preflight it always ran.
//
// A set sweep or banner on an instance that hides it is refused as an
// unknown field BEFORE its contents are judged, exactly as predict
// refuses it; then the limit is checked on explicit + expanded slots
// before a single expanded slot is rendered, so no product of axes and
// no banner bypasses it.
func (s *Service) expandCompose(composed *types.ComposedRequest) (*types.ComposedRequest, *composeExpansion, error) {
	if composed == nil || (len(composed.Requests) == 0 && composed.Sweep == nil && composed.Banner == nil) {
		return nil, nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "composed request must contain at least one request")
	}
	if composed.Sweep != nil || composed.Banner != nil {
		if err := s.slotRefusal(composed); err != nil {
			return nil, nil, err
		}
	}
	exp, err := banner.ExpandCompose(composed, s.composeSlotsPreflight)
	if err != nil {
		return nil, nil, err
	}
	ext := &composeExpansion{banner: exp.Banner}
	if composed.Sweep != nil && composed.Sweep.Rank != nil {
		ext.rank = &composeRank{spec: composed.Sweep.Rank, explicit: exp.Explicit, count: len(exp.SweepLabels)}
	}
	return exp.Composed, ext, nil
}

// rankVetoKey marks a ranked sweep slot's ctx: the slot computes every
// part its own `return` would skip, so the rank reads the unshaped
// response (shaping still trims the wire afterwards).
type rankVetoKey struct{}

// slotContext is ctx for slot i, marked with the rank veto when i is a
// sweep slot of a ranked sweep or a banner slot: the rank and the
// banner stitch read the unshaped response.
func (e *composeExpansion) slotContext(ctx context.Context, i int) context.Context {
	if e == nil {
		return ctx
	}
	ranked := e.rank != nil && i >= e.rank.explicit && i < e.rank.explicit+e.rank.count
	bannered := e.banner != nil && i >= e.banner.Start
	if ranked || bannered {
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
	n := rank.count
	if rank.explicit+n > len(out.Responses) {
		n = max(len(out.Responses)-rank.explicit, 0)
	}
	labels := make([]string, n)
	for i := range labels {
		if r := requests[rank.explicit+i]; r != nil {
			labels[i] = r.Label
		}
	}
	ranking, err := sweep.Rank(rank.spec, labels, out.Responses[rank.explicit:rank.explicit+n])
	if err != nil {
		return err
	}
	out.Ranking = ranking
	return nil
}
