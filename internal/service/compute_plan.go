package service

import (
	"context"
	"github.com/frankbardon/pulse/internal/overlayhost"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/paramread"
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

// componentsVetoKey carries the Components veto a Compose overlay puts
// on a slot it reads after the run (composeSlotVetoes) — set per slot,
// never for a whole Compose.
type componentsVetoKey struct{}

// componentsVeto is how much of a Compose slot's Components no `return`
// selection may skip.
type componentsVeto uint8

const (
	// vetoNone: the slot's Components follow its own selection.
	vetoNone componentsVeto = iota
	// vetoCrosstab keeps components.crosstab — the only sub-part the
	// floor-reading Compose kinds read; the rest follow the selection.
	vetoCrosstab
	// vetoAll keeps every Components sub-part.
	vetoAll
)

// withComponentsVeto returns ctx marked so the request run under it
// keeps every Components sub-part whatever its `return` excludes.
func withComponentsVeto(ctx context.Context) context.Context {
	return withComponentsVetoLevel(ctx, vetoAll)
}

// withComponentsVetoLevel returns ctx carrying veto (ctx itself for
// vetoNone).
func withComponentsVetoLevel(ctx context.Context, veto componentsVeto) context.Context {
	if veto == vetoNone {
		return ctx
	}
	return context.WithValue(ctx, componentsVetoKey{}, veto)
}

func componentsVetoOf(ctx context.Context) componentsVeto {
	v, _ := ctx.Value(componentsVetoKey{}).(componentsVeto)
	return v
}

// composeSlotVetoes reports, per slot of requests (labels already
// defaulted, applyComposeLabelDefaults), the Components veto the
// Compose overlays that name it — as their reference or one of their
// targets (an overlay with no targets names every slot, the reading the
// validators share) — put on it: the strongest of composeOverlaySlotVeto
// over those overlays. Nothing else a Compose overlay reads is
// skippable: a slot's data and crosstab payload are never skipped, and
// the slot's tests / post-tests / overlays a `compose` multiplicity
// family claims stay computed through the multiplicity veto
// (composeSlotContext). nil when there are no Compose overlays.
//
// A slot's crosstab cell weight basis is resolved exactly as the
// runtime stamps it (descx.CrosstabCellWeightBasis: the cell's own
// weight, the request's, the instance default) — the basis the
// hidden-floor refusal and its PROCESSING_INTERNAL backstop
// (composeOverlayFloorsPresent) read — once per slot, and only when a
// weight-aware kind names it.
func (s *Service) composeSlotVetoes(overlays []types.ComposeOverlaySpec, requests []*types.Request) []componentsVeto {
	if len(overlays) == 0 {
		return nil
	}
	out := make([]componentsVeto, len(requests))
	bases := make([]*weighting.Basis, len(requests))
	for i := range overlays {
		spec := &overlays[i]
		for j, r := range requests {
			if r == nil || out[j] == vetoAll || !composeOverlayNames(spec, r.Label) {
				continue
			}
			basis := func() weighting.Basis {
				if bases[j] == nil {
					b := descx.CrosstabCellWeightBasis(r, s.defaultWeight, s.instance)
					bases[j] = &b
				}
				return *bases[j]
			}
			if v := composeOverlaySlotVetoRule(spec, r.Label, basis); v > out[j] {
				out[j] = v
			}
		}
	}
	return out
}

// composeOverlayNames reports whether spec names the slot labelled
// label: its reference, one of its targets, or any slot when it has no
// targets.
func composeOverlayNames(spec *types.ComposeOverlaySpec, label string) bool {
	if spec.Reference == label || len(spec.Targets) == 0 {
		return true
	}
	for _, t := range spec.Targets {
		if t == label {
			return true
		}
	}
	return false
}

// composeOverlaySlotVetoRule is the rule composeSlotVetoes applies —
// composeOverlaySlotVeto; a test swaps it for a wrong rule to prove the
// fold-time backstop (composeOverlayFloorsPresent) catches it.
var composeOverlaySlotVetoRule = composeOverlaySlotVeto

// composeOverlaySlotVeto is the Components veto one Compose overlay
// puts on one slot it names (label; basis resolves the slot's crosstab
// cell weight basis on demand). The per-kind audit of the Compose
// handlers (internal/processing/overlay_compose_*.go,
// overlay_prop_z_panel.go, overlay_panel_index_vs_ref.go) it rests on:
//
//	kind                 reads host floors?            Welford triples?        rule
//	CHISQ_VS_REF         target cells, weighted only   no                      target: crosstab iff weighted; reference: none
//	PROP_Z_CELL          row bases, weighted only      no                      crosstab iff weighted
//	PROP_Z_PANEL         row bases, weighted only;     no                      crosstab iff weighted, or always under
//	                     cell n / distinct margins                             cell_n_unweighted / row_margin_distinct_within
//	                     under two n_source modes                              (every state gate reads Components.Crosstab)
//	T_CELL / Z_CELL      -                             yes, crosstab cells     all
//	T_VS_REF / Z_VS_REF  -                             yes, aggregation groups all
//	FORMULA              -                             cell/margin SDs         all
//	INDEX_VS_REF,        no (payload only on the       series arm: an          all
//	DELTA_VS_REF         crosstab arm)                 AGG_WELFORD row's leg
//	                                                   (narrowSeriesHost)
//	RANK,                no (payload only)             no                      all (unaudited for a release)
//	PANEL_INDEX_VS_REF
//	any other / alias    -                             -                       all
//
// The floor kinds read Components.Crosstab ONLY on a weighted slot
// (floorLeg answers nothing under weighting.Unweighted, and the host
// basis is read off the floor keys, so a skipped block reads as
// unweighted): an unweighted slot's skip changes no number, and a
// weighted one keeps components.crosstab — never the other sub-parts,
// which no Compose kind reads. CHISQ_VS_REF reads its reference as a
// distribution only (descx.ComposeOverlayHiddenFloorRefusal's reads).
// Every other kind keeps the full veto.
func composeOverlaySlotVeto(spec *types.ComposeOverlaySpec, label string, basis func() weighting.Basis) componentsVeto {
	weighted := func() componentsVeto {
		if basis().Weighted() {
			return vetoCrosstab
		}
		return vetoNone
	}
	switch spec.Kind {
	case types.OverlayKindChiSqVsRef:
		if label == spec.Reference {
			return vetoNone
		}
		return weighted()
	case types.OverlayKindPropZCell:
		return weighted()
	case types.OverlayKindPropZPanel:
		paramread.Declare(paramread.Overlay, string(spec.Kind), (*types.PanelOverlayParams)(nil))
		params, err := types.DecodePanelParamsMap(spec.Params)
		if err != nil {
			return vetoAll
		}
		if types.PanelNSourceReadsComponents(params.NSource) {
			return vetoCrosstab
		}
		return weighted()
	}
	return vetoAll
}

// composeSlotContext is ctx for slot i: marked with the Components veto
// a Compose overlay puts on it (vetoes from composeSlotVetoes), and
// carrying the slot's share of the batch's multiplicity plan (mult;
// the batch resolved it, so the slot's Process does not) for the
// multiplicity veto.
func composeSlotContext(ctx context.Context, vetoes []componentsVeto, mult *descx.ComposeMultiplicityPlan, i int) context.Context {
	if i < len(vetoes) {
		ctx = withComponentsVetoLevel(ctx, vetoes[i])
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
	if req == nil || req.Crosstab == nil {
		// No crosstab host: no Components.Crosstab to read (a series
		// host's readers are requestOverlaysReadGroupComponents').
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

// requestOverlaysReadGroupComponents reports whether any of req's own
// overlays that plan computes is an inferential prior-period kind over a
// grouped Process (SERIES) host: each period's leg is read off the
// scored aggregation's per-group components (Components.Aggregations[]
// .Groups), so a `return` that excludes them still computes them.
func requestOverlaysReadGroupComponents(req *types.Request, plan processing.ComputePlan) bool {
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		return false
	}
	for i := range req.Overlays {
		if plan.ComputesOverlay(i) && overlayhost.PriorTestKind(req.Overlays[i].Kind) {
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
//     sub-parts, and the regressions slot, follow the selection except
//     for the multiplicity veto: a member matrix spec keeps its
//     auxiliaries and a member regression (or one that saves a model)
//     keeps the whole regressions slot computed, so the family's m and
//     every kept p_adjusted are the full run's. A skipped regression
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
//     Compose overlay naming this slot keeps what composeSlotVetoes
//     resolved for it (componentsVetoOf): every Components sub-part, or
//     components.crosstab alone for a floor kind over a weighted slot,
//     or nothing for a floor kind over an unweighted one. A chain
//     overlay reads its stages' data and crosstab payload only, so it
//     vetoes nothing;
//  5. veto: a sweep slot of a ranked sweep (rankVetoed) computes the
//     full plan — the rank reads the unshaped response, so a slot
//     `return` that excludes the ranked part never skips it;
//  6. the closed DisableComponents gate drops every Components sub-part,
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
		if !plan.ComputesAllMatrixAuxiliary() && anyMember(claimed.Matrices) {
			// A member matrix spec's p-values sit in auxiliary.p: a
			// selection that drops the matrices or their auxiliaries
			// still computes them, so the family's m and every kept
			// p_adjusted (a pooled `request` family's tests included)
			// match the full run.
			plan = plan.WithMatrixAuxiliary()
		}
	}
	if !plan.MatricesSlot && processing.AppliesMatrixResultOverlays(req, s.extensions, plan) {
		// A computed MATRIX_RESULT overlay reads every result's Primary:
		// a selection that drops the matrices still computes them.
		plan.MatricesSlot = true
	}
	if !plan.Regressions && anyMember(claimed.Regressions) {
		// A member regression's coefficient p-values join a family: a
		// selection that drops the regressions still fits them, so m and
		// every kept p_adjusted (a pooled `request` family's tests
		// included) match the full run.
		plan.Regressions = true
	}
	if !plan.Regressions && savesModel(req) {
		// A regression that saves a model file is fitted whatever the
		// `return` keeps: the save reads its result.
		plan.Regressions = true
	}
	if s.requestOverlaysReadComponents(req, plan) {
		plan.Crosstab = true
	}
	if requestOverlaysReadGroupComponents(req, plan) {
		plan.Aggs, plan.Groups = true, true
	}
	switch componentsVetoOf(ctx) {
	case vetoAll:
		plan = plan.WithComponentsOf(full)
	case vetoCrosstab:
		plan.Crosstab = true
	}
	if rankVetoed(ctx) {
		// A ranked sweep slot: the rank reads the unshaped response, so
		// nothing its selection excludes is skipped.
		plan = full
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

// savesModel reports whether any regression of req sets Save.
func savesModel(req *types.Request) bool {
	if req == nil {
		return false
	}
	for _, r := range req.Regressions {
		if r != nil && r.Save != "" {
			return true
		}
	}
	return false
}
