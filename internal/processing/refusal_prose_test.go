package processing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestRefusalRemedies_DefaultsAreHistorical pins every remedy's nil
// rendering to the literal its site carried before the clause became
// instance-aware: an instance without a feature profile is
// byte-identical.
func TestRefusalRemedies_DefaultsAreHistorical(t *testing.T) {
	want := []string{
		" Use AGG_SET_FREQUENCY for per-member counts, AGG_SET_DISTINCT_VALUES for distinct combinations, or AGG_COUNT for answered rows.",
		" Use ATTR_SET_POPCOUNT for set size or ATTR_SET_HAS for membership.",
		" Use FILTER_SET_CONTAINS_ANY / _ALL / _NONE / _EQUALS, or ATTR_SET_POPCOUNT for set size.",
		"; use GROUP_SET_VALUE or GROUP_SET_PER_ELEMENT",
		"; specify two groups via a FILTER_INCLUDE/FILTER_EXCLUDE upstream",
		"; filter to two via FILTER_INCLUDE/FILTER_EXCLUDE",
		"; specify a two-group SplitBy or use TEST_CHISQ",
		"; specify a two-group SplitBy or use TEST_ANOVA_F",
		"; use TEST_ANOVA_F for k>2",
		" (typically lifted from a preceding TEST_ANOVA_F)",
		", or use OVERLAY_PAIRWISE_PROP_Z / OVERLAY_PAIRWISE_PROBIT_T, which read a separate n leg",
		", admitted: AGG_DISTINCT_SUM, AGG_DISTINCT_COUNT",
		" The count of the field's most common value is AGG_MODE_COUNT.",
		"; use REG_OLS for regularized fits or wait for the GLM regularization phase",
		"; use REG_OLS if you want resample-based uncertainty",
		". Use REG_OLS for greedy AIC/BIC selection",
		" or switch to REG_OLS",
		"; Family is a REG_GLM knob",
		"; Link is a REG_GLM knob",
	}
	rs := refusalRemedies()
	if len(rs) != len(want) {
		t.Fatalf("%d remedies, want %d", len(rs), len(want))
	}
	for i, r := range rs {
		if got := r(nil); got != want[i] {
			t.Errorf("remedy %d default = %q, want %q", i, got, want[i])
		}
		if got := r(func(string) bool { return true }); got != want[i] {
			t.Errorf("remedy %d all-offered = %q, want the default %q", i, got, want[i])
		}
	}
}

// TestRefusalRemedies_Distinct: no default rendering contains another,
// so ScopeRefusal rewrites each clause exactly once.
func TestRefusalRemedies_Distinct(t *testing.T) {
	rs := refusalRemedies()
	for i := range rs {
		for j := range rs {
			if i != j && strings.Contains(rs[i](nil), rs[j](nil)) {
				t.Errorf("remedy %d default %q contains remedy %d default %q", i, rs[i](nil), j, rs[j](nil))
			}
		}
	}
}

// TestRefusalRemedies_NameNoHiddenOperator: with every name a remedy
// can recommend hidden, no rendering names any of them.
func TestRefusalRemedies_NameNoHiddenOperator(t *testing.T) {
	none := func(string) bool { return false }
	for i, r := range refusalRemedies() {
		got := r(none)
		for _, tok := range strings.FieldsFunc(r(nil), func(c rune) bool {
			return !(c == '_' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9')
		}) {
			if strings.Count(tok, "_") > 0 && len(tok) > 4 && strings.Contains(got, tok) {
				t.Errorf("remedy %d with everything hidden still names %s: %q", i, tok, got)
			}
		}
	}
}

func scopedRegistry(hidden ...string) *ExtensionRegistry {
	set := map[string]bool{}
	for _, h := range hidden {
		set[h] = true
	}
	return (*ExtensionRegistry)(nil).WithHidden(func(n string) bool { return set[n] })
}

// TestScopeRefusal_RewritesForTheInstance: the default clause becomes
// the instance's, alternates drop hidden names, a wrapped coded error is
// reached, and an unscoped registry leaves the error untouched.
func TestScopeRefusal_RewritesForTheInstance(t *testing.T) {
	build := func() *errors.CodedError {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"GROUP_RANGE: field tags is a set column (set_u8)"+remedyGrouperSetField(nil),
			map[string]any{"alternts": []string{"GROUP_SET_VALUE", "GROUP_SET_PER_ELEMENT"}})
	}

	if got := (*ExtensionRegistry)(nil).ScopeRefusal(build()); got.(*errors.CodedError).Message != build().Message {
		t.Errorf("nil registry rewrote the message: %q", got.(*errors.CodedError).Message)
	}
	unscoped := &ExtensionRegistry{}
	if got := unscoped.ScopeRefusal(build()).(*errors.CodedError); !reflect.DeepEqual(got, build()) {
		t.Errorf("unscoped registry rewrote the error: %+v", got)
	}

	r := scopedRegistry("GROUP_SET_PER_ELEMENT")
	ce := r.ScopeRefusal(build()).(*errors.CodedError)
	if want := "GROUP_RANGE: field tags is a set column (set_u8); use GROUP_SET_VALUE"; ce.Message != want {
		t.Errorf("message = %q, want %q", ce.Message, want)
	}
	if got := ce.Details["alternts"]; !reflect.DeepEqual(got, []string{"GROUP_SET_VALUE"}) {
		t.Errorf("alternts = %v", got)
	}
	// Idempotent.
	again := r.ScopeRefusal(ce).(*errors.CodedError)
	if again.Message != "GROUP_RANGE: field tags is a set column (set_u8); use GROUP_SET_VALUE" {
		t.Errorf("second pass changed the message: %q", again.Message)
	}

	// Wrapped: a coded error further down the chain is rewritten too.
	inner := build()
	wrapped := fmt.Errorf("outer: %w", errors.WrapCodedError(inner, errors.PROCESSING_CONFIG, "stage failed"))
	r2 := scopedRegistry("GROUP_SET_VALUE", "GROUP_SET_PER_ELEMENT")
	_ = r2.ScopeRefusal(wrapped)
	if want := "GROUP_RANGE: field tags is a set column (set_u8)"; inner.Message != want {
		t.Errorf("wrapped refusal = %q, want %q", inner.Message, want)
	}
	if got := inner.Details["alternts"]; !reflect.DeepEqual(got, []string{}) {
		t.Errorf("wrapped alternts = %#v, want empty", got)
	}
	if got := r2.ScopeRefusal(nil); got != nil {
		t.Errorf("nil error became %v", got)
	}
}

// TestRefusalRemedies_PartialRenderings pins the instance wording for
// a partly hidden alternative set.
func TestRefusalRemedies_PartialRenderings(t *testing.T) {
	hide := func(names ...string) func(string) bool {
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		return func(n string) bool { return !set[n] }
	}
	cases := []struct {
		name string
		r    remedy
		o    func(string) bool
		want string
	}{
		{"agg one hidden", remedyAggregatorSetField, hide("AGG_SET_DISTINCT_VALUES"), " Use AGG_SET_FREQUENCY for per-member counts or AGG_COUNT for answered rows."},
		{"agg all hidden", remedyAggregatorSetField, hide("AGG_SET_FREQUENCY", "AGG_SET_DISTINCT_VALUES", "AGG_COUNT"), ""},
		{"attr one hidden", remedyAttributeSetField, hide("ATTR_SET_HAS"), " Use ATTR_SET_POPCOUNT for set size."},
		{"attr all hidden", remedyAttributeSetField, hide("ATTR_SET_HAS", "ATTR_SET_POPCOUNT"), ""},
		{"filter partial", remedyFiltererSetField, hide("FILTER_SET_EQUALS", "ATTR_SET_POPCOUNT"), " Use FILTER_SET_CONTAINS_ANY / FILTER_SET_CONTAINS_ALL / FILTER_SET_CONTAINS_NONE."},
		{"filter only popcount", remedyFiltererSetField, hide(setFilterers...), " Use ATTR_SET_POPCOUNT for set size."},
		{"filter none", remedyFiltererSetField, hide(append([]string{"ATTR_SET_POPCOUNT"}, setFilterers...)...), ""},
		{"grouper none", remedyGrouperSetField, hide("GROUP_SET_VALUE", "GROUP_SET_PER_ELEMENT"), ""},
		{"ks one", remedyTwoGroupsFilterUpstream, hide("FILTER_EXCLUDE"), "; specify two groups via a FILTER_INCLUDE upstream"},
		{"ks none", remedyTwoGroupsFilterUpstream, hide("FILTER_EXCLUDE", "FILTER_INCLUDE"), "; restrict to exactly two"},
		{"mwu none", remedyTwoGroupsFilterTo, hide("FILTER_EXCLUDE", "FILTER_INCLUDE"), "; restrict to exactly two"},
		{"mwu one", remedyTwoGroupsFilterTo, hide("FILTER_INCLUDE"), "; filter to two via FILTER_EXCLUDE"},
		{"propz", remedyTwoGroupsOrChiSq, hide("TEST_CHISQ"), "; specify a two-group SplitBy"},
		{"z", remedyTwoGroupsOrAnova, hide("TEST_ANOVA_F"), "; specify a two-group SplitBy"},
		{"t", remedyAnovaForK, hide("TEST_ANOVA_F"), "; specify a two-group SplitBy"},
		{"tukey", remedyTukeyFromAnova, hide("TEST_ANOVA_F"), ""},
		{"n leg one", remedyPairwiseNLeg, hide("OVERLAY_PAIRWISE_PROBIT_T"), ", or use OVERLAY_PAIRWISE_PROP_Z, which reads a separate n leg"},
		{"n leg none", remedyPairwiseNLeg, hide("OVERLAY_PAIRWISE_PROBIT_T", "OVERLAY_PAIRWISE_PROP_Z"), ""},
		{"admitted one", remedyDistinctNAdmitted, hide("AGG_DISTINCT_SUM"), ", admitted: AGG_DISTINCT_COUNT"},
		{"admitted none", remedyDistinctNAdmitted, hide("AGG_DISTINCT_SUM", "AGG_DISTINCT_COUNT"), ""},
	}
	for _, tc := range cases {
		if got := tc.r(tc.o); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
