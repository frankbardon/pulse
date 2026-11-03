package service

import (
	stderrors "errors"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/resultcall"
	"github.com/frankbardon/pulse/types"
)

// resultPhaseSlots are the slots the result pre-phase runs: every slot
// an optimize block's result(slot, aggregation) call reads
// (optblock.ResultSlots — the static judgement descx.OptimizeRefusal
// already passed, so each names a known, ungrouped slot) that an
// earlier phase has not run (ran: runComposeFitPhases' sweep slots).
// Index order; nil when no block reads one.
func resultPhaseSlots(composed *types.ComposedRequest, ran []bool) []int {
	if len(composed.Optimize) == 0 {
		return nil
	}
	var out []int
	for _, i := range optblock.ResultSlots(composed.Optimize, resultcall.CatalogOf(composed.Requests)) {
		if i >= len(ran) || !ran[i] {
			out = append(out, i)
		}
	}
	return out
}

// runComposeResultPhase is the result pre-phase, between the fit phases
// and the optimize blocks: it runs resultPhaseSlots through the
// orchestrator's own slot runner (the serial loop or the parallel pool)
// over the bound set, so each slot runs exactly as it would later —
// same ctx, models and position in responses — and returns ran with
// those slots marked, so the final slot phase (unranSlots) never runs
// one twice (a failed one included), plus the slots it ran. ran comes
// back unchanged when no block reads result().
//
// A failure is the runner's own (the serial loop's located slot error,
// the pool's slotPoolFailure) named by resultPhaseFailure. The caller
// then skips every optimize block — the slots' dependents — and, except
// on a non-fail-fast pool, returns it at once.
func runComposeResultPhase(composed *types.ComposedRequest, n int, ran []bool, models *model.Set, runSlots composeSlotRunner) ([]bool, []int, error) {
	idx := resultPhaseSlots(composed, ran)
	if len(idx) == 0 {
		return ran, nil, nil
	}
	if ran == nil {
		ran = make([]bool, n)
	}
	for _, i := range idx {
		ran[i] = true
	}
	if err := runSlots(idx, models); err != nil {
		return ran, idx, resultPhaseFailure(err, composed, idx)
	}
	return ran, idx, nil
}

// resultPhaseFailure names the pre-phase slots in a slot-run failure
// err, so the error that stops the optimize blocks says which read slot
// failed. A located slot error (serial Compose, a fail-fast pool:
// details.request = i) whose slot is in idx keeps its own code and
// message and gains details.result_slot (the slot's label) and
// details.optimize (the first block, in spec order, whose result()
// reads it). A non-fail-fast pool's SERVICE_INTERNAL aggregate gains
// details.result_slots: the labels of the failed slots in idx, in index
// order. Any other error — uncoded (a ctx error), or a slot outside idx
// — is returned unchanged.
func resultPhaseFailure(err error, composed *types.ComposedRequest, idx []int) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return err
	}
	details := make(map[string]any, len(ce.Details)+2)
	for k, v := range ce.Details {
		details[k] = v
	}
	if failed, ok := ce.Details["failed_indices"].([]int); ok && ce.Code == errors.SERVICE_INTERNAL {
		var labels []string
		for _, i := range failed {
			if slices.Contains(idx, i) {
				labels = append(labels, resultcall.EffectiveLabel(composed.Requests[i], i))
			}
		}
		if labels == nil {
			return err
		}
		details["result_slots"] = labels
	} else {
		i, ok := ce.Details["request"].(int)
		if !ok || !slices.Contains(idx, i) {
			return err
		}
		if _, set := details["result_slot"]; !set {
			details["result_slot"] = resultcall.EffectiveLabel(composed.Requests[i], i)
		}
		if _, set := details["optimize"]; !set {
			details["optimize"] = firstResultReader(composed, i)
		}
	}
	return &locatedChain{ce: &errors.CodedError{Code: ce.Code, Message: ce.Message, Details: details, Cause: err}}
}

// firstResultReader is the index of the first optimize block whose
// result() calls read slot i (one exists: slot i is in the pre-phase).
func firstResultReader(composed *types.ComposedRequest, i int) int {
	cat := resultcall.CatalogOf(composed.Requests)
	for b := range composed.Optimize {
		if slices.Contains(optblock.ResultSlots(composed.Optimize[b:b+1], cat), i) {
			return b
		}
	}
	return 0
}
