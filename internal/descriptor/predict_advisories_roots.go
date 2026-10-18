package descriptor

import (
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
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
//
// It also returns ComposeValidationResult.Slots: per slot, the
// streamability verdict Process predict gives it (computeStreamable over
// the same defaulted request and schema), its p-value count and the
// correction families its corrected p-values join (countPValuesVisit, so
// the families never drift from the count). PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT
// reads them, after the slot advisories and before the host many-tests
// note.
func composeAdvisories(req *types.ComposedRequest, plan *ComposeMultiplicityPlan, planOK bool, hostPV *descriptor.PValueCount, opts *PredictOptions) ([]descriptor.Advisory, []descriptor.ComposeSlotPredict) {
	if req == nil {
		return nil, nil
	}
	if opts == nil {
		opts = &PredictOptions{}
	}
	inst := opts.instance()
	var out []descriptor.Advisory
	var slots []descriptor.ComposeSlotPredict
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
		at := "requests[" + strconv.Itoa(i) + "]"
		entry := descriptor.ComposeSlotPredict{Request: i, Label: slot.Label}
		if entry.Label == "" {
			entry.Label = composeDescriptorDefaultLabel(i)
		}
		entry.Streamable, entry.StreamableReasons = computeStreamable(defaulted, schema, opts)
		var pv *descriptor.PValueCount
		if planOK && inst.Enabled(featMultiplicity) {
			pv = countPValuesVisit(defaulted, schema, slotPlan, opts, func(n int, basis string, rm ResolvedMultiplicity) {
				entry.Families = addFamily(entry.Families, pvalueFamilyID(at, rm), rm, n, basis)
			})
		}
		entry.PValues = pv
		slots = append(slots, entry)
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
		// The slot cohort's header record count (opts.CohortCounter, the
		// runtime pre-flight's header-only reader; floors a truncated
		// tail silently) — the figure Process predict reads from the file
		// length, so the record-count rules fire identically.
		counts := opts.cohortCounts(slot.Cohort)
		out = append(out, computeAdvisories(advisoryInput{
			req:             defaulted,
			schema:          schema,
			cohortSchema:    cohort,
			plan:            slotPlan,
			pv:              pv,
			suggestedWeight: suggested,
			measures:        measures,
			inst:            inst,
			records:         counts.Records,
			recordsKnown:    counts.Records >= 0,
			prefix:          at + ".",
			extra:           map[string]any{"request": i},
			subject:         "The request at " + at + " emits",
		})...)
	}
	out = append(out, fitJackknifeCostAdvisories(req, opts, inst)...)
	if a, ok := familiesSplitAdvisory(slots); ok && !inst.AdvisorySuppressed(a.Code) {
		out = append(out, a)
	}
	if a, ok := manyTestsAdvisory(plan != nil, hostPV, "The compose request's overlays emit"); ok && !inst.AdvisorySuppressed(a.Code) {
		out = append(out, a)
	}
	return out, slots
}

// fitJackknifeCostAdvisories is PULSE_ADVISORY_JACKKNIFE_COST over the
// batch's regression fits: a fit's request is a Request like a slot's,
// so its leave-one-out jackknife over a large cohort draws the advisory
// Process predict gives the same request, attributed to the fit
// (details.fit = i, the slot path prefixed "fits[i].request."). A fit
// reaches no p-site and no other advisory rule reads it.
func fitJackknifeCostAdvisories(req *types.ComposedRequest, opts *PredictOptions, inst *InstanceSnapshot) []descriptor.Advisory {
	var out []descriptor.Advisory
	for i, f := range req.Fits {
		if f.Request == nil || len(f.Request.Regressions) == 0 {
			continue
		}
		counts := opts.cohortCounts(f.Request.Cohort)
		if counts.Records < 0 {
			continue
		}
		for _, a := range jackknifeCostAdvisories(f.Request, counts.Records, "fits["+strconv.Itoa(i)+"].request.", inst) {
			if inst.AdvisorySuppressed(a.Code) {
				continue
			}
			a.Details["fit"] = i
			out = append(out, a)
		}
	}
	return out
}

// pvalueFamilyID names the family rm resolved for a slot of the Compose
// slot at (`requests[i]`) — the runtime fold's family key
// (multFamilyKey / overlayFamilyKey) spelled for the wire: the one
// batch-wide `compose` family; the slot's own `request` family; every
// other family (an overlay layer's layer / row / column, a matrix
// spec's matrix) local to the slot path that owns it.
func pvalueFamilyID(at string, rm ResolvedMultiplicity) string {
	switch rm.Family {
	case types.MultiplicityFamilyCompose:
		return string(types.MultiplicityFamilyCompose)
	case types.MultiplicityFamilyRequest:
		return at + "#" + string(rm.Family)
	}
	return at + "." + rm.Slot + "#" + string(rm.Family)
}

// addFamily folds n p-values on basis into the family id of fams (first
// member first); the weakest basis wins.
func addFamily(fams []descriptor.PValueFamily, id string, rm ResolvedMultiplicity, n int, basis string) []descriptor.PValueFamily {
	for k := range fams {
		if fams[k].ID != id {
			continue
		}
		fams[k].Count += n
		if pvalueBasisRank[basis] > pvalueBasisRank[fams[k].Basis] {
			fams[k].Basis = basis
		}
		return fams
	}
	return append(fams, descriptor.PValueFamily{ID: id, Family: rm.Family, Method: rm.Method, Count: n, Basis: basis})
}

// familiesSplitAdvisory is PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT over
// ComposeValidationResult.Slots: it fires when two or more slots emit
// p-values, their corrected p-values join two or more distinct families
// other than `compose`, and the slots emit at least
// MultiplicityTriggerThreshold p-values in total (corrected or not).
// Each such family is corrected on its own, so no correction spans the
// batch. details: slots (the slot indices whose p-values join a split
// family), families (their IDs, first-seen order), total, uncorrected,
// basis (weakest), threshold, and the patch suggested =
// {"multiplicity": {"method": <the first family's method>, "family":
// "compose"}}. A batch advisory: no details.request.
func familiesSplitAdvisory(slots []descriptor.ComposeSlotPredict) (descriptor.Advisory, bool) {
	emitting, total, uncorrected := 0, 0, 0
	basis := descriptor.PValueBasisExact
	var families []string
	var inFamily []int
	var method types.MultiplicityMethod
	for _, s := range slots {
		if s.PValues == nil || s.PValues.Total == 0 {
			continue
		}
		emitting++
		total += s.PValues.Total
		uncorrected += s.PValues.Uncorrected
		if pvalueBasisRank[s.PValues.Basis] > pvalueBasisRank[basis] {
			basis = s.PValues.Basis
		}
		joined := false
		for _, f := range s.Families {
			if f.Family == types.MultiplicityFamilyCompose {
				continue
			}
			joined = true
			if !slices.Contains(families, f.ID) {
				if len(families) == 0 {
					method = f.Method
				}
				families = append(families, f.ID)
			}
		}
		if joined {
			inFamily = append(inFamily, s.Request)
		}
	}
	if emitting < 2 || len(families) < 2 || total < descriptor.MultiplicityTriggerThreshold {
		return descriptor.Advisory{}, false
	}
	count := strconv.Itoa(total)
	switch basis {
	case descriptor.PValueBasisLowerBound:
		count = "at least " + count
	case descriptor.PValueBasisDictionary:
		count = "an estimated " + count
	}
	return descriptor.Advisory{
		Code: string(errors.PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT),
		Message: "The compose request's slots emit " + count + " p-values (threshold " +
			strconv.Itoa(descriptor.MultiplicityTriggerThreshold) + ") into " + strconv.Itoa(len(families)) +
			" separate correction families (" + strings.Join(families, ", ") +
			"); each family is corrected on its own, so no correction spans the batch. To correct them as one, set family compose on the multiplicity block of each slot in details.slots, or keep the split when each slot answers a separate question.",
		Details: map[string]any{
			"slots":       inFamily,
			"families":    families,
			"total":       total,
			"uncorrected": uncorrected,
			"basis":       basis,
			"threshold":   descriptor.MultiplicityTriggerThreshold,
			"suggested":   map[string]any{"multiplicity": map[string]any{"method": string(method), "family": string(types.MultiplicityFamilyCompose)}},
		},
	}, true
}
