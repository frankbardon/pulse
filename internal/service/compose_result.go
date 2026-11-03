package service

import (
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
// one twice. ran comes back unchanged when no block reads result().
func runComposeResultPhase(composed *types.ComposedRequest, n int, ran []bool, models *model.Set, runSlots composeSlotRunner) ([]bool, error) {
	idx := resultPhaseSlots(composed, ran)
	if len(idx) == 0 {
		return ran, nil
	}
	if err := runSlots(idx, models); err != nil {
		return nil, err
	}
	if ran == nil {
		ran = make([]bool, n)
	}
	for _, i := range idx {
		ran[i] = true
	}
	return ran, nil
}
