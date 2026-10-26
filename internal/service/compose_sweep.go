package service

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/banner"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
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
	// axes are the sweep slots' axis values (banner.Expansion.SweepAxes),
	// carried onto every RankEntry.
	axes []map[string]any
	// ranked is the ranking a sweep-bound batch computed in phase 0
	// (done set): the one the response reports, never recomputed.
	ranked []types.RankEntry
	done   bool
}

// composeExpansion is what the as-written request hands the finished
// batch: the sweep rank and the banner layout (each nil when unset).
type composeExpansion struct {
	rank   *composeRank
	banner *banner.Layout
	// seed is a sampling sweep's effective seed (nil without one),
	// echoed as ComposedResponse.SweepSeed.
	seed *int64
	// sweepFits is the scope a from_sweep fit is judged against and
	// resolved over (descx.SweepFitScope — predict builds the same).
	sweepFits model.SweepScope
}

// expandCompose returns the effective request Compose and
// ComposeParallel run: the ComposedRequest with its sweep, then its
// banner, expanded into slots appended after the explicit ones
// (banner.ExpandCompose, the expansion predict shares). With
// EchoRequest on, it is what ComposedResponse.NormalizedRequest
// carries. Then its top-level params merge into every slot and fit
// request (descx.InheritComposeParams). A sweep-, banner- and
// params-free request comes back as the same pointer, after the
// MaxComposeSlots preflight it always ran.
//
// A set sweep, banner or params on an instance that hides it is refused as an
// unknown field BEFORE its contents are judged, exactly as predict
// refuses it; then the limit is checked on explicit + expanded slots
// before a single expanded slot is rendered, so no product of axes and
// no banner bypasses it.
func (s *Service) expandCompose(composed *types.ComposedRequest) (*types.ComposedRequest, *composeExpansion, error) {
	if composed == nil || (len(composed.Requests) == 0 && composed.Sweep == nil && composed.Banner == nil) {
		return nil, nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "composed request must contain at least one request")
	}
	if composed.Sweep != nil || composed.Banner != nil || len(composed.Params) > 0 {
		if err := s.slotRefusal(composed); err != nil {
			return nil, nil, err
		}
	}
	exp, err := banner.ExpandCompose(composed, s.composeSlotsPreflight)
	if err != nil {
		return nil, nil, err
	}
	ext := &composeExpansion{banner: exp.Banner, seed: exp.SweepSeed}
	if composed.Sweep != nil && composed.Sweep.Rank != nil {
		ext.rank = &composeRank{spec: composed.Sweep.Rank, explicit: exp.Explicit, count: len(exp.SweepLabels), axes: exp.SweepAxes}
	}
	// The top-level params merge into every slot (sweep and banner
	// slots included) and every fit request, each sub-request's own key
	// winning — predict's InheritComposeParams call at the same point.
	effective, cerr := descx.InheritComposeParams(exp.Composed)
	if cerr != nil {
		return nil, nil, cerr
	}
	ext.sweepFits = descx.SweepFitScope(composed, effective, exp.Explicit, len(exp.SweepLabels))
	return effective, ext, nil
}

// checkComposeFits runs the fits' structural rules over the batch's
// sweep (model.CheckFitsIn — predict runs the same at the same point)
// and returns the request the batch runs: composed with every
// from_sweep fit's request resolved off the first sweep slot
// (model.ResolveSweepFits), composed itself when it has none. The
// caller's request — what NormalizedRequest echoes — is never mutated.
func checkComposeFits(composed *types.ComposedRequest, ext *composeExpansion) (*types.ComposedRequest, error) {
	var scope model.SweepScope
	if ext != nil {
		scope = ext.sweepFits
	}
	if err := model.CheckFitsIn(composed.Fits, scope); err != nil {
		return nil, err
	}
	resolved := model.ResolveSweepFits(composed.Fits, scope)
	if len(resolved) == 0 || &resolved[0] == &composed.Fits[0] {
		return composed, nil
	}
	out := *composed
	out.Fits = resolved
	return &out, nil
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

// applyComposeSweep is the finished batch's sweep echo: a sampling
// sweep's effective seed (ComposedResponse.SweepSeed), then the rank
// (applyComposeRank). Both orchestrators call it at the same point, so
// serial and parallel answer identically.
func applyComposeSweep(ext *composeExpansion, requests []*types.Request, out *types.ComposedResponse) error {
	if ext.seed != nil {
		seed := *ext.seed
		out.SweepSeed = &seed
	}
	return applyComposeRank(ext.rank, requests, out)
}

// applyComposeRank fills out.Ranking from the sweep slots (the slots
// from rank.explicit on, labelled by the resolved requests). It runs
// after every fold, before the facade shapes anything, so each slot is
// read unshaped. No rank leaves out untouched.
func applyComposeRank(rank *composeRank, requests []*types.Request, out *types.ComposedResponse) error {
	if rank == nil {
		return nil
	}
	if rank.done {
		// A sweep-bound batch ranked in phase 0, and bound its fits
		// from that ranking: report it as computed.
		out.Ranking = rank.ranked
		return nil
	}
	ranking, err := rankComposeSweep(rank, requests, out.Responses)
	if err != nil {
		return err
	}
	out.Ranking = ranking
	return nil
}
