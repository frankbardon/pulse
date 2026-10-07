package service

import (
	"context"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// compute_plan.go resolves the processing.ComputePlan a request runs
// under — the one value every execution arm consults at its build
// points, superseding the single Components opt-out bool. It is built
// ONCE per request in Service.process, before dispatch, and rides the
// dispatch context to every seam: newProcessor (serial scan, join,
// crosstab buffered / fused, chain stages), the parallel-decode reducer
// and the shard reducer (finalizeMergedPartial, the per-bucket floor
// allocation). Dispatch itself — the merge gate, canStream, crosstab
// fusion — reads the ORIGINAL request, so a `return` selection never
// moves a request onto a different arm.

// computePlanKey carries the request's resolved ComputePlan on the
// dispatch context.
type computePlanKey struct{}

// withComputePlan returns ctx carrying plan for the seams below it.
func withComputePlan(ctx context.Context, plan processing.ComputePlan) context.Context {
	return context.WithValue(ctx, computePlanKey{}, plan)
}

// componentsVetoKey marks a context whose request feeds a consumer
// that reads its Components after the run — a Compose slot a Compose
// overlay names as its reference or a target — so no `return` selection
// may skip them. Set per slot (composeSlotVetoes), never for a whole
// Compose.
type componentsVetoKey struct{}

// withComponentsVeto returns ctx marked so the request run under it
// keeps its Components whatever its `return` excludes.
func withComponentsVeto(ctx context.Context) context.Context {
	return context.WithValue(ctx, componentsVetoKey{}, true)
}

func componentsVetoed(ctx context.Context) bool {
	v, _ := ctx.Value(componentsVetoKey{}).(bool)
	return v
}

// composeSlotVetoes reports, per slot of requests (labels already
// defaulted, applyComposeLabelDefaults), whether a Compose overlay
// names it — as its reference or one of its targets (an overlay with no
// targets names every slot, the reading the validators share). A named
// slot keeps every Components sub-part: the Compose host classifies a
// slot by its Components block (processing.newComposeSlotView), so even
// a non-crosstab slot's must stay as the unshaped run builds it. Nothing
// else a Compose overlay reads is skippable: a slot's data and crosstab
// payload are never skipped, and the slot's tests / post-tests /
// overlays (the `compose` multiplicity family) stay computed until
// their skip lands. nil when there are no Compose overlays.
func composeSlotVetoes(overlays []types.ComposeOverlaySpec, requests []*types.Request) []bool {
	if len(overlays) == 0 {
		return nil
	}
	named := map[string]bool{}
	all := false
	for i := range overlays {
		named[overlays[i].Reference] = true
		if len(overlays[i].Targets) == 0 {
			all = true
		}
		for _, t := range overlays[i].Targets {
			named[t] = true
		}
	}
	out := make([]bool, len(requests))
	for i, r := range requests {
		out[i] = r != nil && (all || named[r.Label])
	}
	return out
}

// composeSlotContext is ctx for slot i: marked with the Components veto
// when a Compose overlay names it (vetoes from composeSlotVetoes).
func composeSlotContext(ctx context.Context, vetoes []bool, i int) context.Context {
	if i < len(vetoes) && vetoes[i] {
		return withComponentsVeto(ctx)
	}
	return ctx
}

// requestOverlaysReadComponents reports whether any of req's own
// overlays reads its crosstab host's Components.Crosstab — the rule
// predict shares (descx.OverlayReadsHostComponents).
func requestOverlaysReadComponents(req *types.Request) bool {
	if req == nil {
		return false
	}
	for i := range req.Overlays {
		if descx.OverlayReadsHostComponents(req.Overlays[i].Kind) {
			return true
		}
	}
	return false
}

// componentsGateClosed is the DisableComponents gate for req — the rule
// predict shares (descx.EffectiveDisableComponents): a request's explicit
// disable_components wins (an explicit false re-opens an engine-off
// gate); else the engine Options.DisableComponents. A request `return`
// never re-opens it. It is one INPUT of the ComputePlan, and on its own
// decides only the Compose-overlay hidden-floor refusal.
func (s *Service) componentsGateClosed(req *types.Request) bool {
	return descx.EffectiveDisableComponents(req, s.disableComponents)
}

// resolveComputePlan folds the request's resolved `return` plan (nil
// when no layer supplies one), the veto set and the DisableComponents
// gate into the ComputePlan the run executes:
//
//  1. processing.ComputePlanFor(ret): a part is computed iff the
//     selection keeps it or something below it;
//  2. the matrices slot and its auxiliary / scalars / vectors
//     sub-parts follow the selection with no veto: nothing downstream
//     reads them (matrices are refused under joins and crosstab, sit
//     outside the multiplicity pool, and no Compose / chain overlay
//     reads them); the other whole-slot parts (overlays, tests,
//     post-tests, regressions) stay computed — their skip rules and
//     vetoes land with the stories that wire them;
//  3. veto: a KEPT request overlay that reads its host's components
//     (requestOverlaysReadComponents) keeps components.crosstab — the
//     only sub-part an overlay reads; its *_margin_aggregations figures
//     follow the selection — and a Compose overlay naming this slot
//     (componentsVetoed) keeps every Components sub-part. A chain
//     overlay reads its stages' data and crosstab payload only, so it
//     vetoes nothing;
//  4. the closed DisableComponents gate drops every Components sub-part,
//     veto or not (an overlay that needs them refuses, as before).
//
// No `return` and an open gate is FullComputePlan: byte-identical.
func (s *Service) resolveComputePlan(ctx context.Context, req *types.Request, ret *returnplan.Plan) processing.ComputePlan {
	full := processing.FullComputePlan()
	plan := processing.ComputePlanFor(ret)
	plan.Overlays, plan.Tests, plan.PostTests, plan.Regressions =
		full.Overlays, full.Tests, full.PostTests, full.Regressions
	if plan.Overlays && requestOverlaysReadComponents(req) {
		plan.Crosstab = true
	}
	if componentsVetoed(ctx) {
		plan = plan.WithComponentsOf(full)
	}
	if s.componentsGateClosed(req) {
		plan = plan.WithoutComponents()
	}
	return plan
}

// computePlanFor is the plan a seam runs req under: the one
// Service.process resolved onto ctx, else (a caller that bypassed
// process: a later chain stage carries its own, see processChain) the
// request's vetoes and the DisableComponents gate over a full selection.
func (s *Service) computePlanFor(ctx context.Context, req *types.Request) processing.ComputePlan {
	if plan, ok := ctx.Value(computePlanKey{}).(processing.ComputePlan); ok {
		return plan
	}
	return s.resolveComputePlan(ctx, req, nil)
}
