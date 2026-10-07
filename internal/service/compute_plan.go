package service

import (
	"context"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/internal/weighting"
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
// overlays a `compose` multiplicity family claims stay computed through
// the multiplicity veto (composeSlotContext). nil when there are no
// Compose overlays.
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
// when a Compose overlay names it (vetoes from composeSlotVetoes), and
// carrying the slot's share of the batch's multiplicity plan (mult;
// the batch resolved it, so the slot's Process does not) for the
// multiplicity veto.
func composeSlotContext(ctx context.Context, vetoes []bool, mult *descx.ComposeMultiplicityPlan, i int) context.Context {
	if i < len(vetoes) && vetoes[i] {
		ctx = withComponentsVeto(ctx)
	}
	if mult != nil && i < len(mult.Requests) && mult.Requests[i] != nil {
		ctx = context.WithValue(ctx, slotMultiplicityKey{}, mult.Requests[i])
	}
	return ctx
}

// slotMultiplicityKey carries a Compose slot's resolved multiplicity
// plan (its entry of the batch's ComposeMultiplicityPlan) to the slot's
// compute plan.
type slotMultiplicityKey struct{}

func slotMultiplicity(ctx context.Context) *descx.MultiplicityPlan {
	m, _ := ctx.Value(slotMultiplicityKey{}).(*descx.MultiplicityPlan)
	return m
}

// multiplicityVetoes is the multiplicity veto over one whole slot list
// (request overlays here; tests and post-tests reuse it): per entry of
// an n-long list, whether a resolved multiplicity family claims it
// (descx.ResolvedMultiplicity.Member, index-aligned). A member keeps
// computing whatever the selection excludes, so the family's size m
// and every kept p_adjusted are exactly the full run's. nil when no
// entry is a member.
func multiplicityVetoes(slots []descx.ResolvedMultiplicity, n int) []bool {
	var out []bool
	for i := 0; i < n && i < len(slots); i++ {
		if !slots[i].Member {
			continue
		}
		if out == nil {
			out = make([]bool, n)
		}
		out[i] = true
	}
	return out
}

// anyMember reports whether a resolved multiplicity family claims any
// entry of slots.
func anyMember(slots []descx.ResolvedMultiplicity) bool {
	for _, r := range slots {
		if r.Member {
			return true
		}
	}
	return false
}

// composeOverlaysComputed reports whether a Compose batch folds its
// Compose-host overlays: always when its Compose-level `return` (ret,
// nil when absent) keeps `overlays`; otherwise only when a resolved
// multiplicity family (mult.Overlays) claims one of them — then all of
// them run, since a Compose-host spec may emit several layers and the
// fold aligns its families by position. Skipped, they run no handler
// and raise no refusal, and they read (so veto) no slot's Components.
func composeOverlaysComputed(composed *types.ComposedRequest, ret *returnplan.Plan, mult *descx.ComposeMultiplicityPlan) bool {
	if composed == nil || len(composed.Overlays) == 0 {
		return false
	}
	if processing.ComputePlanFor(ret).Overlays {
		return true
	}
	return mult != nil && anyMember(mult.Overlays)
}

// requestOverlaysReadComponents reports whether any of req's own
// overlays that plan computes reads its crosstab host's
// Components.Crosstab — the rule predict shares
// (descx.OverlayReadsHostComponents), over the crosstab cell's weight
// basis resolved exactly as the runtime stamps it (request, slot and
// the instance default weight; descx.CrosstabCellWeightBasis). A
// skipped layer reads nothing; the basis is resolved only when a
// computed layer's kind depends on it.
func (s *Service) requestOverlaysReadComponents(req *types.Request, plan processing.ComputePlan) bool {
	if req == nil {
		return false
	}
	basis, resolved := weighting.Unweighted, false
	for i := range req.Overlays {
		if !plan.ComputesOverlay(i) {
			continue
		}
		kind := req.Overlays[i].Kind
		if !resolved && !types.IsPairwiseOverlayKind(kind) {
			basis, resolved = descx.CrosstabCellWeightBasis(req, s.defaultWeight, s.instance), true
		}
		if descx.OverlayReadsHostComponents(kind, basis) {
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
//     sub-parts, and the regressions slot, follow the selection with
//     no veto: nothing downstream reads them (matrices are refused
//     under joins and crosstab; neither sits in the multiplicity pool;
//     no Compose / chain overlay reads them). A skipped regression
//     fits nothing and raises no fit refusal or low-n_eff warning;
//  3. the overlay, tests and post-tests slots follow the selection,
//     per entry: an excluded slot still computes every entry a
//     resolved multiplicity family claims (multiplicityVetoes over
//     mult.Overlays / .Tests / .PostTests — mult is the request's
//     plan, or the Compose slot's share of its batch's when nil), so
//     the family's m and every kept p_adjusted are the full run's.
//     Nothing else reads them: Compose and chain overlays read slots'
//     data, crosstab payload and Components only. A skipped entry runs
//     no handler or fold and raises no refusal or warning (incl.
//     PULSE_WEIGHT_LOW_NEFF);
//  4. veto: a COMPUTED request overlay layer that reads its host's
//     components (requestOverlaysReadComponents: a pairwise kind, or
//     a χ² / Fisher kind over a weighted crosstab cell) keeps
//     components.crosstab — the only sub-part an overlay reads; its
//     *_margin_aggregations figures follow the selection — and a
//     Compose overlay naming this slot (componentsVetoed) keeps every
//     Components sub-part. A chain overlay reads its stages' data and
//     crosstab payload only, so it vetoes nothing;
//  5. the closed DisableComponents gate drops every Components sub-part,
//     veto or not (an overlay that needs them refuses, as before).
//
// No `return` and an open gate is FullComputePlan: byte-identical.
func (s *Service) resolveComputePlan(ctx context.Context, req *types.Request, ret *returnplan.Plan, mult *descx.MultiplicityPlan) processing.ComputePlan {
	full := processing.FullComputePlan()
	plan := processing.ComputePlanFor(ret)
	if mult == nil {
		mult = slotMultiplicity(ctx)
	}
	var claimed descx.MultiplicityPlan
	if mult != nil {
		claimed = *mult
	}
	if req != nil {
		if !plan.Overlays && len(req.Overlays) > 0 {
			plan = plan.WithOverlayLayers(multiplicityVetoes(claimed.Overlays, len(req.Overlays)))
		}
		if !plan.Tests && len(req.Tests) > 0 {
			plan = plan.WithTestEntries(multiplicityVetoes(claimed.Tests, len(req.Tests)))
		}
		if !plan.PostTests && len(req.PostTests) > 0 {
			plan = plan.WithPostTestEntries(multiplicityVetoes(claimed.PostTests, len(req.PostTests)))
		}
	}
	if s.requestOverlaysReadComponents(req, plan) {
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
	return s.resolveComputePlan(ctx, req, nil, nil)
}
