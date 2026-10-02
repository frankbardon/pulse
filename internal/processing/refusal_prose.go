package processing

import (
	stderrors "errors"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// Refusal prose that names OTHER built-ins.
//
// A few runtime refusals recommend operators the caller did not write
// as the fix ("use GROUP_SET_VALUE or GROUP_SET_PER_ELEMENT"). On a
// profiled instance such a clause must name only what the instance
// offers: a refusal never advertises a hidden feature. Each clause is a
// remedy — a pure function of the instance's offered predicate (nil =
// everything offered) whose nil rendering is the historical literal.
// The site builds its message from remedy(nil), so an instance without
// a feature profile is byte-identical; ExtensionRegistry.ScopeRefusal
// then swaps each default rendering for the instance's on the way out
// of the service, which reaches every site (factories, tests, overlay
// handlers, the regression engine) without threading the instance
// through their signatures.
//
// A clause naming a name every profile must enable alongside the
// refusing operator (a hard dependency edge in
// internal/descriptor/features.go, e.g. AGG_WELFORD for the Welford
// pairwise kinds, GROUP_DATE for OVERLAY_YOY, REG_OLS for ATTR_REG_*)
// stays literal: it can never be hidden while the refusal is reachable.

type remedy func(offered func(string) bool) string

func isOffered(offered func(string) bool, name string) bool {
	return offered == nil || offered(name)
}

func offeredOf(offered func(string) bool, names ...string) []string {
	var out []string
	for _, n := range names {
		if isOffered(offered, n) {
			out = append(out, n)
		}
	}
	return out
}

// orList joins items as English alternatives: "a", "a or b",
// "a, b, or c".
func orList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
}

// remedyAggregatorSetField completes the numeric-aggregator-on-a-set
// refusal (rejectSetFieldForNumericAggregator).
func remedyAggregatorSetField(offered func(string) bool) string {
	var items []string
	for _, it := range []struct{ name, what string }{
		{string(types.AGG_SET_FREQUENCY), "per-member counts"},
		{string(types.AGG_SET_DISTINCT_VALUES), "distinct combinations"},
		{string(types.AGG_COUNT), "answered rows"},
	} {
		if isOffered(offered, it.name) {
			items = append(items, it.name+" for "+it.what)
		}
	}
	if len(items) == 0 {
		return ""
	}
	return " Use " + orList(items) + "."
}

// remedyAttributeSetField completes the numeric-attribute-on-a-set
// refusal (rejectSetFieldForNumericAttribute).
func remedyAttributeSetField(offered func(string) bool) string {
	var items []string
	for _, it := range []struct{ name, what string }{
		{string(types.ATTR_SET_POPCOUNT), "set size"},
		{string(types.ATTR_SET_HAS), "membership"},
	} {
		if isOffered(offered, it.name) {
			items = append(items, it.name+" for "+it.what)
		}
	}
	if len(items) == 0 {
		return ""
	}
	return " Use " + orList(items) + "."
}

// setFilterers are the set-membership filterers, in refusal order.
var setFilterers = []string{
	string(types.FILTER_SET_CONTAINS_ANY),
	string(types.FILTER_SET_CONTAINS_ALL),
	string(types.FILTER_SET_CONTAINS_NONE),
	string(types.FILTER_SET_EQUALS),
}

// remedyFiltererSetField completes the numeric-filterer-on-a-set
// refusal (rejectSetFieldForNumericFilter). The full family keeps its
// historical shorthand; a partial one is spelled out.
func remedyFiltererSetField(offered func(string) bool) string {
	var items []string
	switch fs := offeredOf(offered, setFilterers...); {
	case len(fs) == len(setFilterers):
		items = append(items, "FILTER_SET_CONTAINS_ANY / _ALL / _NONE / _EQUALS")
	case len(fs) > 0:
		items = append(items, strings.Join(fs, " / "))
	}
	if isOffered(offered, string(types.ATTR_SET_POPCOUNT)) {
		items = append(items, string(types.ATTR_SET_POPCOUNT)+" for set size")
	}
	if len(items) == 0 {
		return ""
	}
	return " Use " + strings.Join(items, ", or ") + "."
}

// remedyGrouperSetField completes the value-grouper-on-a-set refusal
// (rejectSetFieldForNumericGrouper).
func remedyGrouperSetField(offered func(string) bool) string {
	gs := offeredOf(offered, string(types.GROUP_SET_VALUE), string(types.GROUP_SET_PER_ELEMENT))
	if len(gs) == 0 {
		return ""
	}
	return "; use " + orList(gs)
}

// remedyTwoGroupsFilterUpstream completes TEST_KS's too-many-groups
// refusal.
func remedyTwoGroupsFilterUpstream(offered func(string) bool) string {
	fs := offeredOf(offered, string(types.FILTER_INCLUDE), string(types.FILTER_EXCLUDE))
	if len(fs) == 0 {
		return "; restrict to exactly two"
	}
	return "; specify two groups via a " + strings.Join(fs, "/") + " upstream"
}

// remedyTwoGroupsFilterTo completes TEST_MANN_WHITNEY_U's
// too-many-groups refusal.
func remedyTwoGroupsFilterTo(offered func(string) bool) string {
	fs := offeredOf(offered, string(types.FILTER_INCLUDE), string(types.FILTER_EXCLUDE))
	if len(fs) == 0 {
		return "; restrict to exactly two"
	}
	return "; filter to two via " + strings.Join(fs, "/")
}

// remedyTwoGroupsOrChiSq completes TEST_PROP_Z's too-many-groups
// refusal.
func remedyTwoGroupsOrChiSq(offered func(string) bool) string {
	if isOffered(offered, string(types.TEST_CHISQ)) {
		return "; specify a two-group SplitBy or use TEST_CHISQ"
	}
	return "; specify a two-group SplitBy"
}

// remedyTwoGroupsOrAnova completes TEST_Z_TWO_SAMPLE's too-many-groups
// refusal.
func remedyTwoGroupsOrAnova(offered func(string) bool) string {
	if isOffered(offered, string(types.TEST_ANOVA_F)) {
		return "; specify a two-group SplitBy or use TEST_ANOVA_F"
	}
	return "; specify a two-group SplitBy"
}

// remedyAnovaForK completes TEST_T's two-sample too-many-groups
// refusal.
func remedyAnovaForK(offered func(string) bool) string {
	if isOffered(offered, string(types.TEST_ANOVA_F)) {
		return "; use TEST_ANOVA_F for k>2"
	}
	return "; specify a two-group SplitBy"
}

// remedyTukeyFromAnova completes TEST_TUKEY_HSD's missing-params
// refusal. TEST_TUKEY_HSD does not depend on TEST_ANOVA_F (its inputs
// are plain numbers), so the pairing is advice and follows the profile.
func remedyTukeyFromAnova(offered func(string) bool) string {
	if isOffered(offered, string(types.TEST_ANOVA_F)) {
		return " (typically lifted from a preceding TEST_ANOVA_F)"
	}
	return ""
}

// remedyPairwiseNLeg completes the Welford pairwise kinds' n_source
// refusal: the proportion kinds that read a separate n leg.
func remedyPairwiseNLeg(offered func(string) bool) string {
	ks := offeredOf(offered, string(types.OverlayKindPairwisePropZ), string(types.OverlayKindPairwiseProbitT))
	switch len(ks) {
	case 0:
		return ""
	case 1:
		return ", or use " + ks[0] + ", which reads a separate n leg"
	}
	return ", or use " + strings.Join(ks, " / ") + ", which read a separate n leg"
}

// remedyDistinctNAdmitted completes the distinct-key n_source admission
// refusal (MATRIX pairwise and the compose panel): the admitted cell
// aggregators.
func remedyDistinctNAdmitted(offered func(string) bool) string {
	names := make([]string, 0, 2)
	for _, a := range PairwiseDistinctNAdmitted() {
		names = append(names, string(a))
	}
	as := offeredOf(offered, names...)
	if len(as) == 0 {
		return ""
	}
	return ", admitted: " + strings.Join(as, ", ")
}

// refusalRemedies is every remedy ScopeRefusal rewrites. No default
// rendering may contain another's (TestRefusalRemedies_Distinct).
func refusalRemedies() []remedy {
	out := []remedy{
		remedyAggregatorSetField,
		remedyAttributeSetField,
		remedyFiltererSetField,
		remedyGrouperSetField,
		remedyTwoGroupsFilterUpstream,
		remedyTwoGroupsFilterTo,
		remedyTwoGroupsOrChiSq,
		remedyTwoGroupsOrAnova,
		remedyAnovaForK,
		remedyTukeyFromAnova,
		remedyPairwiseNLeg,
		remedyDistinctNAdmitted,
	}
	for _, r := range regression.Remedies() {
		out = append(out, r)
	}
	return out
}

// remedyDetailLists are the details keys whose []string value lists
// alternatives a refusal recommends (not names the caller wrote).
var remedyDetailLists = []string{"alternates", "alternts", "admitted_cell_aggregators"}

// ScopeRefusal rewrites err for the instance r serves: in every coded
// error on the chain, each remedy clause's default rendering becomes
// the instance's, and the alternatives listed under remedyDetailLists
// drop hidden names. A registry without a feature-set predicate (every
// instance without a feature profile) returns err untouched, so its
// refusals stay byte-identical. The rewrite is idempotent.
func (r *ExtensionRegistry) ScopeRefusal(err error) error {
	if err == nil || r == nil || r.hidden == nil {
		return err
	}
	offered := func(name string) bool { return !r.hidden(name) }
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		ce, ok := cur.(*errors.CodedError)
		if !ok {
			continue
		}
		for _, rm := range refusalRemedies() {
			if def := rm(nil); def != "" && strings.Contains(ce.Message, def) {
				ce.Message = strings.Replace(ce.Message, def, rm(offered), 1)
			}
		}
		for _, k := range remedyDetailLists {
			names, ok := ce.Details[k].([]string)
			if !ok {
				continue
			}
			kept := make([]string, 0, len(names))
			for _, n := range names {
				if offered(n) {
					kept = append(kept, n)
				}
			}
			ce.Details[k] = kept
		}
	}
	return err
}
