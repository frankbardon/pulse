package service

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// compose_adaptive.go is the run-time half of an adaptive sweep (a
// registered adaptive mode — sweep.Adaptive): its rounds run inside
// phase 0, in both orchestrators, before anything reads a sweep slot.
// The batch's slot list was sized to the budget at expansion (round 0
// rendered, every later slot a placeholder); each round installs its
// rendered slots at their fixed indices, runs them through the
// orchestrator's own round runner — the serial loop or the parallel
// pool — and hands their ranked values to the strategy, so the next
// round reads the finished one only, ordered by slot index.
//
// A slot that fails is penalised (its Observation carries the worst
// value seen) and the search goes on: its response is a stub carrying
// only its label, it is left out of the ranking, and the batch warns
// PULSE_SLOT_FAILED once with details failed_indices (batch indices)
// and first_error. Under Strict the first failed round's lowest-index
// failure fails the batch instead; a fail-fast pool aborts at the
// failing round with its located error.

// composeRoundRunner runs one adaptive round's slots at idx over the
// bound set models and reports each slot's error, index-aligned with
// idx (nil for a slot that finished; its response is in place). err is
// a fatal stop — a fail-fast pool's located failure, a bound-set error
// or the caller's ctx — and is returned unchanged.
type composeRoundRunner func(idx []int, models *model.Set) (slotErrs []error, err error)

// adaptiveRun is what an orchestrator hands phase 0 for an adaptive
// sweep: the slices the rounds write into and its round runner. mult
// points at the orchestrator's own plan variable, so a re-resolved plan
// reaches every later slot context and the barrier fold.
type adaptiveRun struct {
	requests  []*types.Request
	responses []*types.Response
	pre       *composeModels
	mult      **descx.ComposeMultiplicityPlan
	round     composeRoundRunner
}

// adaptiveFailures are an adaptive sweep's penalised failed slots:
// their batch indices, ascending (nil when none failed), and the first
// one's error.
type adaptiveFailures struct {
	indices []int
	first   error
}

// runAdaptiveSweep runs an adaptive sweep's rounds (sweep.Adaptive.Run)
// over composed — the effective request, whose sweep slots each round
// rewrites in place — and returns the slots it penalised for failing.
func (s *Service) runAdaptiveSweep(composed *types.ComposedRequest, ext *composeExpansion, early *model.Set, run *adaptiveRun) (adaptiveFailures, error) {
	explicit := ext.rank.explicit
	labels := make(map[string]int, len(run.requests))
	for i, r := range run.requests {
		if r != nil {
			labels[r.Label] = i
		}
	}
	var fails adaptiveFailures
	err := ext.adaptive.Run(func(_ int, slots []sweep.AdaptiveSlot) ([]sweep.Outcome, error) {
		idx := make([]int, len(slots))
		for k, sl := range slots {
			i := explicit + sl.Slot
			idx[k] = i
			if err := s.installAdaptiveSlot(composed, ext, run, labels, i, sl); err != nil {
				return nil, err
			}
		}
		// The rendered bodies may name multiplicity blocks the
		// placeholders did not: the plan is re-resolved over the
		// batch as it now stands, before the round's slots read it.
		plan, err := s.resolveComposeMultiplicity(composed)
		if err != nil {
			return nil, err
		}
		*run.mult = plan
		slotErrs, err := run.round(idx, early)
		if err != nil {
			return nil, err
		}
		if err := foldSweepSlotMultiplicity(plan, run.responses, idx[0], idx[len(idx)-1]+1); err != nil {
			return nil, err
		}
		outs := make([]sweep.Outcome, len(slots))
		for k, i := range idx {
			if serr := slotErrs[k]; serr != nil {
				if s.strict {
					return nil, serr
				}
				fails.indices = append(fails.indices, i)
				if fails.first == nil {
					fails.first = serr
				}
				run.responses[i] = &types.Response{Label: run.requests[i].Label}
				outs[k] = sweep.Outcome{Penalised: true}
				continue
			}
			v, ok, oerr := sweep.Objective(ext.rank.spec, run.requests[i].Label, run.responses[i])
			if oerr != nil {
				return nil, oerr
			}
			outs[k] = sweep.Outcome{Value: v, Penalised: !ok}
		}
		// The round finished: its span joins the trace the rank reads
		// (ComposedResponse.SweepTrace, RankEntry.Round).
		ext.rank.rounds = append(ext.rank.rounds, sweep.RoundSpan{slots[0].Slot, slots[len(slots)-1].Slot})
		return outs, nil
	})
	return fails, err
}

// installAdaptiveSlot places a round's rendered slot at batch index i:
// the effective request (its top-level params inherited, as
// InheritComposeParams gives every slot), the slot's own loaded model
// files, the label-resolved run clone — its label checked against every
// other slot's — and the rank's axis values.
func (s *Service) installAdaptiveSlot(composed *types.ComposedRequest, ext *composeExpansion, run *adaptiveRun, labels map[string]int, i int, sl sweep.AdaptiveSlot) error {
	req := descx.InheritRequestParams(sl.Request, composed.Params)
	composed.Requests[i] = req
	if run.pre != nil {
		files, err := s.loadModelFiles(model.RequestRefs(req))
		if err != nil {
			return descx.RefusalAt(err, "request", i)
		}
		run.pre.slots[i] = files
	}
	clone := *req
	if prev := run.requests[i]; prev != nil && labels[prev.Label] == i {
		delete(labels, prev.Label)
	}
	if j, taken := labels[clone.Label]; taken && j != i {
		return errors.NewCodedErrorWithDetails(errors.PULSE_COMPOSE_LABEL_COLLISION,
			fmt.Sprintf("compose: label %q is used by 2 slots", clone.Label),
			map[string]any{"label": clone.Label, "indices": []int{min(i, j), max(i, j)}})
	}
	labels[clone.Label] = i
	run.requests[i] = &clone
	ext.rank.axes[sl.Slot] = sl.Axes
	return nil
}

// adaptiveFailureWarning is the batch warning a penalised slot raises:
// PULSE_SLOT_FAILED, details slot_kind "sweep", failed_indices (batch
// indices, ascending) and first_error.
func adaptiveFailureWarning(f adaptiveFailures, budget int) *types.ResponseWarning {
	return &types.ResponseWarning{
		Code: string(errors.PULSE_SLOT_FAILED),
		Message: fmt.Sprintf("%d of %d adaptive sweep slots failed; each was penalised as the worst value observed, the search went on "+
			"and none is ranked", len(f.indices), budget),
		Details: map[string]any{"slot_kind": "sweep", "failed_indices": f.indices, "first_error": f.first.Error()},
	}
}

// withoutFailed is responses with the failed slots nil, so the rank
// skips their stubs rather than warning on them.
func withoutFailed(responses []*types.Response, failed []int) []*types.Response {
	if len(failed) == 0 {
		return responses
	}
	out := make([]*types.Response, len(responses))
	copy(out, responses)
	for _, i := range failed {
		out[i] = nil
	}
	return out
}
