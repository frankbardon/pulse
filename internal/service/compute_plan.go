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

// componentsVetoKey marks a context whose requests feed a consumer that
// reads their Components after the run (a Compose-level or chain-level
// overlay), so no `return` selection may skip them.
type componentsVetoKey struct{}

// withComponentsVeto returns ctx marked so every request run under it
// keeps its Components whatever its `return` excludes.
func withComponentsVeto(ctx context.Context) context.Context {
	return context.WithValue(ctx, componentsVetoKey{}, true)
}

func componentsVetoed(ctx context.Context) bool {
	v, _ := ctx.Value(componentsVetoKey{}).(bool)
	return v
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
//  2. the whole-slot parts (matrices, overlays, tests, post-tests,
//     regressions) stay computed — their skip rules and vetoes land
//     with the stories that wire them;
//  3. veto (conservative until the precise overlay-reads-components
//     rule lands): any overlay on the request, or a Compose / chain
//     overlay downstream (componentsVetoed), keeps every Components
//     sub-part — an overlay may read them;
//  4. the closed DisableComponents gate drops every Components sub-part,
//     veto or not (an overlay that needs them refuses, as before).
//
// No `return` and an open gate is FullComputePlan: byte-identical.
func (s *Service) resolveComputePlan(ctx context.Context, req *types.Request, ret *returnplan.Plan) processing.ComputePlan {
	full := processing.FullComputePlan()
	plan := processing.ComputePlanFor(ret)
	plan.MatricesSlot, plan.Overlays, plan.Tests, plan.PostTests, plan.Regressions =
		full.MatricesSlot, full.Overlays, full.Tests, full.PostTests, full.Regressions
	if (req != nil && len(req.Overlays) > 0) || componentsVetoed(ctx) {
		plan = plan.WithComponentsOf(full)
	}
	if s.componentsGateClosed(req) {
		plan = plan.WithoutComponents()
	}
	return plan
}

// computePlanFor is the plan a seam runs req under: the one
// Service.process resolved onto ctx, else (a caller that bypassed
// process, such as a later chain stage) the DisableComponents gate
// alone — the pre-plan behaviour.
func (s *Service) computePlanFor(ctx context.Context, req *types.Request) processing.ComputePlan {
	if plan, ok := ctx.Value(computePlanKey{}).(processing.ComputePlan); ok {
		return plan
	}
	return s.resolveComputePlan(ctx, req, nil)
}
