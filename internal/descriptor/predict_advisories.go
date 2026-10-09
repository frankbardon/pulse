package descriptor

import (
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// predict_advisories.go answers PredictResult.Advisories: coded,
// non-blocking notes that the chosen analysis may not fit the data.
// No-execute like the rest of predict — every rule reads only the
// request, the schema and its dictionaries, and figures predict has
// already computed (PValues). The trigger principle is authoritative
// metadata only: no field-name or value-shape heuristics.
//
// An advisory is never a warning (strict mode never sees one) and
// never changes execution. Any operator an advisory names — in its
// message or details.suggested — is one the instance offers.

// advisoryCodes is every registered advisory code, in rule order. It is
// the set Options.SuppressAdvisories is validated against.
var advisoryCodes = []errors.Code{
	errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS,
	errors.PULSE_ADVISORY_MANY_TESTS,
}

// AdvisoryCodes returns every registered PULSE_ADVISORY_* code, in rule
// order, as a fresh slice.
func AdvisoryCodes() []string {
	out := make([]string, len(advisoryCodes))
	for i, c := range advisoryCodes {
		out[i] = string(c)
	}
	return out
}

// ValidateSuppressAdvisories refuses an Options.SuppressAdvisories entry
// that is not a registered advisory code with
// PULSE_SUPPRESS_ADVISORY_UNKNOWN (details: code, valid). Validated
// against the whole registry, not the instance view: suppressing a code
// a feature profile can never fire is harmless.
func ValidateSuppressAdvisories(codes []string) error {
	valid := AdvisoryCodes()
	for _, c := range codes {
		if !slices.Contains(valid, c) {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SUPPRESS_ADVISORY_UNKNOWN,
				"Options.SuppressAdvisories: "+strconv.Quote(c)+" is not an advisory code",
				map[string]any{"code": c, "valid": valid})
		}
	}
	return nil
}

// manyGroupAlternatives is the replacement the two-group advisory
// suggests, preferred first: Welch's ANOVA (the many-group form of the
// Welch two-sample test both TEST_T and TEST_WELCH run, and the NotFor
// "three or more groups" alternative of both), then the classic F.
var manyGroupAlternatives = []types.TestType{types.TEST_ANOVA_WELCH, types.TEST_ANOVA_F}

// computeAdvisories runs every advisory rule over req (defaults
// resolved) and schema (the one it executes over). plan is
// ResolveMultiplicity's (nil when nothing names a block) and pv the
// result's PValues. Suppressed codes are dropped. Nil when none fires.
func computeAdvisories(req *types.Request, schema *encoding.Schema, plan *MultiplicityPlan, pv *descriptor.PValueCount, inst *InstanceSnapshot) []descriptor.Advisory {
	if req == nil {
		return nil
	}
	var out []descriptor.Advisory
	add := func(a descriptor.Advisory) {
		if !inst.AdvisorySuppressed(a.Code) {
			out = append(out, a)
		}
	}
	for _, a := range twoGroupAdvisories(req, schema, inst) {
		add(a)
	}
	if a, ok := manyTestsAdvisory(plan, pv); ok {
		add(a)
	}
	return out
}

// twoGroupAdvisories: one PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS per
// TEST_T / TEST_WELCH slot (tests, then post-tests) whose split_by is a
// categorical field with more than two dictionary entries. A packed_bool
// split has two groups and never fires; a field split_by may not name
// (numeric) is predict's error, not an advisory.
func twoGroupAdvisories(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) []descriptor.Advisory {
	var out []descriptor.Advisory
	for _, tier := range []struct {
		key  string
		list []*types.Test
	}{{"tests", req.Tests}, {"post_tests", req.PostTests}} {
		for i, t := range tier.list {
			if t == nil || t.SplitBy == "" {
				continue
			}
			routed := opRoute(inst, t.Type)
			if routed != types.TEST_T && routed != types.TEST_WELCH {
				continue
			}
			groups := splitGroupCount(schema, t.SplitBy)
			if groups <= 2 {
				continue
			}
			slot := tier.key + "[" + strconv.Itoa(i) + "]"
			details := map[string]any{
				"slot":     slot,
				"operator": string(routed),
				"field":    t.Field,
				"split_by": t.SplitBy,
				"groups":   groups,
			}
			next := "filter split_by down to the two groups you mean to compare"
			for _, alt := range manyGroupAlternatives {
				if inst.Enabled(string(alt)) {
					details["suggested"] = string(alt)
					next = "consider " + string(alt) + ", which compares every group at once, or " + next
					break
				}
			}
			out = append(out, descriptor.Advisory{
				Code: string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS),
				Message: string(routed) + " at " + slot + " splits " + t.Field + " by " + t.SplitBy +
					", whose dictionary holds " + strconv.Itoa(groups) +
					" groups, but a two-sample test compares exactly two; " + next + ".",
				Details: details,
			})
		}
	}
	return out
}

// splitGroupCount is the group count a split_by field resolves to from
// the schema alone: a categorical field's dictionary entry count, 2 for
// packed_bool, 0 (unknown) otherwise.
func splitGroupCount(schema *encoding.Schema, field string) int {
	if schema == nil {
		return 0
	}
	for _, f := range schema.Fields {
		if f.Name != field {
			continue
		}
		switch {
		case f.Type.IsCategorical() && f.Dictionary != nil:
			return f.Dictionary.Count()
		case f.Type == encoding.FieldTypePackedBool:
			return 2
		}
		return 0
	}
	return 0
}

// manyTestsAdvisory: PULSE_ADVISORY_MANY_TESTS when the request emits at
// least MultiplicityTriggerThreshold uncorrected p-values and nothing —
// the request, a slot or Options.DefaultMultiplicity — names a
// multiplicity block (plan nil). pv is nil when the instance hides the
// multiple-comparison capability, so the advisory never proposes it.
func manyTestsAdvisory(plan *MultiplicityPlan, pv *descriptor.PValueCount) (descriptor.Advisory, bool) {
	if plan != nil || pv == nil || pv.Uncorrected < descriptor.MultiplicityTriggerThreshold {
		return descriptor.Advisory{}, false
	}
	count := strconv.Itoa(pv.Uncorrected)
	switch pv.Basis {
	case descriptor.PValueBasisLowerBound:
		count = "at least " + count
	case descriptor.PValueBasisDictionary:
		count = "an estimated " + count
	}
	return descriptor.Advisory{
		Code: string(errors.PULSE_ADVISORY_MANY_TESTS),
		Message: "The request emits " + count + " uncorrected p-values (threshold " +
			strconv.Itoa(descriptor.MultiplicityTriggerThreshold) +
			") and names no multiplicity block; with this many tests some may fall below alpha by chance alone, so consider adding one such as holm.",
		Details: map[string]any{
			"total":       pv.Total,
			"uncorrected": pv.Uncorrected,
			"basis":       pv.Basis,
			"threshold":   descriptor.MultiplicityTriggerThreshold,
			"suggested":   map[string]any{"multiplicity": map[string]any{"method": "holm"}},
		},
	}, true
}
