package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// predict_advisories_roots.go carries the advisory rule set past the
// Request root: Facet predict (ValidateFacetWithOptions) and Compose
// predict (ValidateComposeWithOptions). Chain predict is deliberately
// untouched — a v1 chain stage reaches no p-site. No-execute: every
// figure comes from the request, a schema read through
// PredictOptions.SchemaLoader and the sidecar facts read through
// PredictOptions.SidecarLoader, never a record.

// countFacetPValues is FacetValidationResult.PValues: one count per
// inferential FACET-host overlay (OVERLAY_CHISQ_VS_POP /
// OVERLAY_KS_VS_POP each carry one scalar p-value for the facet field
// they decorate; a kind whose extent predict cannot derive counts as
// one, a lower bound). plan is ResolveFacetMultiplicity's, index-aligned
// with req.Overlays (nil when nothing names a block). Nil when the facet
// request emits no p-value.
func countFacetPValues(req *types.FacetRequest, plan []ResolvedMultiplicity, opts *PredictOptions) *descriptor.PValueCount {
	if req == nil {
		return nil
	}
	out := &descriptor.PValueCount{Basis: descriptor.PValueBasisExact, Threshold: descriptor.MultiplicityTriggerThreshold}
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		routed := opts.overlayRoute(spec.Kind)
		if routed == "" || !overlayCapabilityFor(routed).Inferential {
			continue
		}
		n, basis := overlayPValueCount(routed, spec.Scope, 0, 0)
		addPValues(out, n, basis, i < len(plan) && plan[i].Member)
	}
	if out.Total == 0 {
		return nil
	}
	return out
}

// countComposeHostPValues is ComposeValidationResult.PValues: the
// p-values the Compose-host overlays carry. A Compose-host overlay's
// extent depends on slot results predict never sees, so every
// inferential spec counts as one and the basis is always lower_bound.
// plan is ResolveComposeMultiplicity's (nil when nothing names a block).
func countComposeHostPValues(req *types.ComposedRequest, plan *ComposeMultiplicityPlan, opts *PredictOptions) *descriptor.PValueCount {
	if req == nil {
		return nil
	}
	var members []ResolvedMultiplicity
	if plan != nil {
		members = plan.Overlays
	}
	out := &descriptor.PValueCount{Basis: descriptor.PValueBasisExact, Threshold: descriptor.MultiplicityTriggerThreshold}
	for i := range req.Overlays {
		routed := opts.overlayRoute(req.Overlays[i].Kind)
		if routed == "" || !overlayCapabilityFor(routed).Inferential {
			continue
		}
		addPValues(out, 1, descriptor.PValueBasisLowerBound, i < len(members) && members[i].Member)
	}
	if out.Total == 0 {
		return nil
	}
	return out
}

// addPValues folds n p-values on basis into out; the weakest basis wins.
func addPValues(out *descriptor.PValueCount, n int, basis string, member bool) {
	out.Total += n
	if !member {
		out.Uncorrected += n
	}
	if pvalueBasisRank[basis] > pvalueBasisRank[out.Basis] {
		out.Basis = basis
	}
}

// facetAdvisories is FacetValidationResult.Advisories: the full rule set
// over the facet request. A FacetRequest carries no test, aggregator or
// weight slot, so only PULSE_ADVISORY_MANY_TESTS — over the facet
// overlays' p-values — can fire; the slot rules run and find nothing.
func facetAdvisories(plan []ResolvedMultiplicity, pv *descriptor.PValueCount, opts *PredictOptions) []descriptor.Advisory {
	var mp *MultiplicityPlan
	if plan != nil {
		mp = &MultiplicityPlan{Overlays: plan}
	}
	return computeAdvisories(advisoryInput{
		req:     &types.Request{},
		plan:    mp,
		pv:      pv,
		inst:    opts.instance(),
		subject: "The facet request emits",
	})
}

// composeAdvisories is ComposeValidationResult.Advisories: every slot's
// Request advisories, in slot order, attributed to their slot (each
// slot path prefixed "requests[i]." and details.request = i), then
// PULSE_ADVISORY_MANY_TESTS over the Compose-host overlays' p-values
// (hostPV, always a lower_bound). plan is ResolveComposeMultiplicity's;
// planOK false (a refused batch) withholds every p-value count.
func composeAdvisories(req *types.ComposedRequest, plan *ComposeMultiplicityPlan, planOK bool, hostPV *descriptor.PValueCount, opts *PredictOptions) []descriptor.Advisory {
	if req == nil {
		return nil
	}
	if opts == nil {
		opts = &PredictOptions{}
	}
	inst := opts.instance()
	var out []descriptor.Advisory
	for i, slot := range req.Requests {
		if slot == nil {
			continue
		}
		cohort := cohortSchemaFor(slot.Cohort, opts)
		schema, refusals := validatorRequestSchema(slot, cohort, opts)
		if schema == nil || len(refusals) > 0 {
			schema = cohort
		}
		defaulted := defaultedForValidation(slot, schema, opts)
		var slotPlan *MultiplicityPlan
		if plan != nil && i < len(plan.Requests) {
			slotPlan = plan.Requests[i]
		}
		var pv *descriptor.PValueCount
		if planOK && inst.Enabled(featMultiplicity) {
			pv = countPValues(defaulted, schema, slotPlan, opts)
		}
		var weightVar string
		var measures map[string]string
		if opts.SidecarLoader != nil && slot.Cohort != nil {
			weightVar, measures = opts.SidecarLoader(cohortPathFor(slot.Cohort))
		}
		var suggested *descriptor.SuggestedWeight
		if weightVar != "" && inst.Enabled(featWeighting) {
			if weights, werr := ResolveWeights(defaulted, schema, opts.DefaultWeight, inst); werr == nil && !anyWeightResolves(weights) {
				suggested = SuggestWeight(cohort, weightVar, inst)
			}
		}
		at := "requests[" + strconv.Itoa(i) + "]"
		out = append(out, computeAdvisories(advisoryInput{
			req:             defaulted,
			schema:          schema,
			cohortSchema:    cohort,
			plan:            slotPlan,
			pv:              pv,
			suggestedWeight: suggested,
			measures:        measures,
			inst:            inst,
			prefix:          at + ".",
			extra:           map[string]any{"request": i},
			subject:         "The request at " + at + " emits",
		})...)
	}
	if a, ok := manyTestsAdvisory(plan != nil, hostPV, "The compose request's overlays emit"); ok && !inst.AdvisorySuppressed(a.Code) {
		out = append(out, a)
	}
	return out
}
