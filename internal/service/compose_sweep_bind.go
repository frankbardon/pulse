package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// compose_sweep_bind.go is the run-time half of a sweep-bound fit — a
// `fits` entry carrying `from_sweep` (types.FitFromSweep; the static
// half is internal/model/sweep_fits.go). A batch holding one runs
// sweep-first, in both orchestrators:
//
//  1. the authored fits (every entry without from_sweep), bound into a
//     set the sweep slots read (a sweep slot never reads a bound fit —
//     model.CheckFitsIn refused that cycle);
//  2. phase 0: the sweep slots, then their slot-local multiplicity
//     families (foldSweepSlotMultiplicity), then the rank — computed
//     ONCE here and reported as ComposedResponse.Ranking;
//  3. each bound fit, refitted as an ordinary regression fit over its
//     winning slot (model.SweepFitRequest of ranking[0]) — or, with an
//     empty ranking, bound FAILED;
//  4. then today's order: optimize, and every slot phase 0 did not run.
//
// A batch with no from_sweep entry takes none of this: its order and
// bytes are exactly the sweep-free schedule's.

// sweepBinding is a batch's sweep-first schedule: the sweep slots
// [lo, hi) of the effective request and the masks of its authored and
// sweep-bound fits. Nil when no fit binds from the sweep.
type sweepBinding struct {
	lo, hi   int
	authored []bool
	bound    []bool
}

// newSweepBinding is fits' schedule over the expansion, nil when no
// entry carries from_sweep. model.CheckFitsIn already proved a binding
// has a ranked sweep.
func newSweepBinding(fits []types.FitSpec, ext *composeExpansion) *sweepBinding {
	if ext == nil || ext.rank == nil || !slices.ContainsFunc(fits, func(f types.FitSpec) bool { return f.FromSweep != nil }) {
		return nil
	}
	b := &sweepBinding{lo: ext.rank.explicit, hi: ext.rank.explicit + ext.rank.count,
		authored: make([]bool, len(fits)), bound: make([]bool, len(fits))}
	for i, f := range fits {
		b.bound[i] = f.FromSweep != nil
		b.authored[i] = f.FromSweep == nil
	}
	return b
}

// slots are the sweep slots' indices, in order.
func (b *sweepBinding) slots() []int {
	out := make([]int, 0, b.hi-b.lo)
	for i := b.lo; i < b.hi; i++ {
		out = append(out, i)
	}
	return out
}

// composeSlotRunner runs the slots at idx (in index order for the
// serial orchestrator, on its pool for the parallel one), each over its
// bound set — models plus the slot's own loaded models — writing
// responses[i].
type composeSlotRunner func(idx []int, models *model.Set) error

// runComposeFitPhases runs the batch's fits — and, when a fit binds
// from the sweep, phase 0 (the sweep slots and their rank) before the
// bound fits — and returns the bound set, the ComposedResponse.Fits
// entries, the lifted fit warnings and the slots it already ran (nil
// when it ran none). Without a binding it is runComposeFits.
func (s *Service) runComposeFitPhases(ctx context.Context, composed *types.ComposedRequest, pre *composeModels, workers int,
	ext *composeExpansion, requests []*types.Request, responses []*types.Response,
	mult *descx.ComposeMultiplicityPlan, runSlots composeSlotRunner,
) (*model.Set, map[string]*types.FitResult, []*types.ResponseWarning, []bool, error) {
	bind := newSweepBinding(composed.Fits, ext)
	if bind == nil {
		models, results, warnings, err := s.runComposeFits(ctx, composed.Fits, pre, workers)
		return models, results, warnings, nil, err
	}
	pass := s.newComposeFitPass(composed.Fits, pre)
	if err := pass.execute(ctx, workers, bind.authored); err != nil {
		return nil, nil, nil, nil, err
	}
	early, _, _, err := pass.bind(bind.authored)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := runSlots(bind.slots(), early); err != nil {
		return nil, nil, nil, nil, err
	}
	ran := make([]bool, len(requests))
	for i := bind.lo; i < bind.hi && i < len(ran); i++ {
		ran[i] = true
	}
	if err := foldSweepSlotMultiplicity(mult, responses, bind.lo, bind.hi); err != nil {
		return nil, nil, nil, nil, err
	}
	ranking, err := rankComposeSweep(ext.rank, requests, responses)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ext.rank.ranked, ext.rank.done = ranking, true
	if err := pass.bindWinner(bind, ranking, composed.Requests, ext.rank, s.strict); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := pass.execute(ctx, workers, pass.resolved(bind)); err != nil {
		return nil, nil, nil, nil, err
	}
	models, results, warnings, err := pass.bind(pass.every())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return models, results, warnings, ran, nil
}

// bindWinner resolves each sweep-bound fit over ranking: its request
// becomes model.SweepFitRequest of the rank-1 slot (the effective
// request slots[rank.explicit+Slot]) and its models that slot's loaded
// ones. An empty ranking binds the fit FAILED instead — its result a
// regression result carrying a PULSE_SWEEP_RANK_PATH error (reason
// sweep.RankReasonEmpty), its PULSE_SLOT_FAILED warning lifted with
// details.fit — and, under strict, fails the batch with that error.
func (p *composeFitPass) bindWinner(bind *sweepBinding, ranking []types.RankEntry, slots []*types.Request, rank *composeRank, strict bool) error {
	for i, f := range p.fits {
		if !bind.bound[i] {
			continue
		}
		reg := f.FromSweep.Regression
		if len(ranking) > 0 {
			k := rank.explicit + ranking[0].Slot
			p.fits[i].Request = model.SweepFitRequest(slots[k], reg)
			if p.pre != nil && k < len(p.pre.slots) {
				p.winnerFiles[i] = p.pre.slots[k]
			}
			if p.fits[i].Request == nil {
				// CheckFitsIn proved every sweep slot holds it.
				return fitFailure(p.fits, i, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("fits[%d]: the winning sweep slot holds no regression %q", i, reg),
					map[string]any{"fit": i, "name": f.Name, "regression": reg}))
			}
			continue
		}
		se := &types.SlotError{
			Code: string(errors.PULSE_SWEEP_RANK_PATH),
			Message: fmt.Sprintf("no sweep slot was ranked by %q (every slot's value there is null or missing), so no slot won and fit %q binds no model",
				rank.spec.By, f.Name),
			Details: map[string]any{"by": rank.spec.By, "reason": sweep.RankReasonEmpty, "regression": reg},
		}
		fail := slotfail.Failure{Kind: slotfail.KindRegressions, Index: 0, Label: reg, Err: se}
		if strict {
			return fitFailure(p.fits, i, slotfail.StrictError(fail))
		}
		var typ types.RegressionType
		if f.Request != nil && len(f.Request.Regressions) == 1 && f.Request.Regressions[0] != nil {
			typ = f.Request.Regressions[0].Type
		}
		p.results[i] = &types.RegressionResult{Name: reg, Type: typ, Error: se}
		p.failed[i] = se
		p.saveWarnings[i] = append(p.saveWarnings[i], fitWarning(slotfail.Warning(fail), i))
	}
	return nil
}

// resolved is the mask of the sweep-bound fits that have a winner to
// refit (a failed one is already bound failed).
func (p *composeFitPass) resolved(bind *sweepBinding) []bool {
	m := make([]bool, len(p.fits))
	for i := range m {
		m[i] = bind.bound[i] && p.failed[i] == nil
	}
	return m
}

// rankComposeSweep is the rank over the finished sweep slots (rank.explicit
// on, labelled by the resolved requests) — sweep.Rank, which also warns
// on each slot it leaves out.
func rankComposeSweep(rank *composeRank, requests []*types.Request, responses []*types.Response) ([]types.RankEntry, error) {
	n := rank.count
	if rank.explicit+n > len(responses) {
		n = max(len(responses)-rank.explicit, 0)
	}
	labels := make([]string, n)
	for i := range labels {
		if r := requests[rank.explicit+i]; r != nil {
			labels[i] = r.Label
		}
	}
	return sweep.Rank(rank.spec, labels, rank.axes, responses[rank.explicit:rank.explicit+n])
}

// foldSweepSlotMultiplicity corrects the slot-local multiplicity
// families (`request`, `layer`, `row`, `column`, `matrix` — every one
// but `compose`) of slots [lo, hi) in place, so phase 0's rank reads
// the adjusted figures a full batch would. A `compose` family pools
// slots that have not run yet, so it is left to the barrier fold (a
// rank path on its adjusted figures resolves in no slot and is
// refused). The barrier fold later rewrites the same slot-local
// figures from the same raw p-values — identical values.
func foldSweepSlotMultiplicity(plan *descx.ComposeMultiplicityPlan, responses []*types.Response, lo, hi int) error {
	if plan == nil {
		return nil
	}
	active := false
	for i := lo; i < hi && i < len(plan.Requests); i++ {
		active = active || plan.Requests[i].Active()
	}
	if !active {
		return nil
	}
	fams := newMultFamilies()
	for i := lo; i < hi && i < len(plan.Requests) && i < len(responses); i++ {
		collectRequestSites(fams, composeSlotScope(i), plan.Requests[i], responses[i])
	}
	fams.drop(multFamilyKey("", types.MultiplicityFamilyCompose))
	if err := fams.fold(); err != nil {
		return err
	}
	for i := lo; i < hi && i < len(responses); i++ {
		if responses[i] == nil {
			continue
		}
		if err := refreshOverlayLetters(responses[i].Overlays); err != nil {
			return err
		}
	}
	return nil
}

// unranSlots are the indices of n slots that ran does not mark, in
// order (every slot when ran is nil).
func unranSlots(n int, ran []bool) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if i >= len(ran) || !ran[i] {
			out = append(out, i)
		}
	}
	return out
}
