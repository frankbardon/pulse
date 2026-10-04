package weighting

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestClassify_Complete: every built-in aggregator carries a weight
// class, so a new aggregator cannot ship without deciding one.
func TestClassify_Complete(t *testing.T) {
	for _, at := range types.AllAggregationTypes() {
		if ClassOf(string(at)) == ClassNone {
			t.Errorf("%s has no weight class", at)
		}
	}
	if ClassOf("AGG_EXT_FOO_BAR") != ClassNone || ClassOf("TEST_EXT_FOO_BAR") != ClassNone {
		t.Fatal("extension names must be ClassNone (their class is the registration's)")
	}
	if !IsAware("AGG_SUM") || !IsAware("AGG_MEDIAN") || IsAware("AGG_MIN") || IsAware("AGG_CI_LOWER") {
		t.Fatal("IsAware mismatch")
	}
}

// TestClassify_NonAggregatorFamilies pins the non-aggregator classes
// (.claude/reference/weighting.md, Refusal rules): every built-in
// TEST_* and REG_*, the reference-distribution attributes and
// GROUP_QUANTILE refuse under any weight; every WIN_* is not weightable
// (skipped under a default, refused under an explicit weight); every
// other attribute, grouper, filterer and feature is untouched
// (ClassNone).
func TestClassify_NonAggregatorFamilies(t *testing.T) {
	want := map[string]Class{}
	for _, tt := range types.AllTestTypes() {
		want[string(tt)] = ClassRefuse
	}
	for _, rt := range types.AllRegressionTypes() {
		want[string(rt)] = ClassRefuse
	}
	for _, at := range types.AllAttributeTypes() {
		want[string(at)] = ClassNone
	}
	for _, a := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_PERCENTILE, types.ATTR_NORMALIZED} {
		want[string(a)] = ClassRefuse
	}
	for _, gt := range types.AllGroupTypes() {
		want[string(gt)] = ClassNone
	}
	want[string(types.GROUP_QUANTILE)] = ClassRefuse
	for _, wt := range types.AllWindowTypes() {
		want[string(wt)] = ClassNotWeightable
	}
	for _, ft := range types.AllFiltererTypes() {
		want[string(ft)] = ClassNone
	}
	for _, ft := range types.AllFeatureTypes() {
		want[string(ft)] = ClassNone
	}
	for op, c := range want {
		if got := ClassOf(op); got != c {
			t.Errorf("ClassOf(%s) = %v, want %v", op, got, c)
		}
		if IsAware(op) {
			t.Errorf("%s must not be weight-aware", op)
		}
	}
}

func TestClassifyWeight(t *testing.T) {
	cases := []struct {
		w       float64
		present bool
		kind    types.WeightKind
		want    Reason
	}{
		{1.5, true, types.WeightKindProbability, Valid},
		{0, true, types.WeightKindProbability, Valid},
		{3, true, types.WeightKindFrequency, Valid},
		{0, false, types.WeightKindProbability, Null},
		{-1, true, types.WeightKindProbability, Negative},
		{math.NaN(), true, "", NaNInf},
		{math.Inf(1), true, "", NaNInf},
		{math.Inf(-1), true, "", NaNInf},
		{2.5, true, types.WeightKindFrequency, NonIntegerFrequency},
	}
	for _, tc := range cases {
		if got := Classify(tc.w, tc.present, tc.kind); got != tc.want {
			t.Errorf("Classify(%v, %v, %q) = %v, want %v", tc.w, tc.present, tc.kind, got, tc.want)
		}
	}
	keys := map[string]bool{}
	for _, r := range InvalidReasons {
		keys[r.Key()] = true
	}
	for _, k := range []string{"null", "negative", "nan_inf", "non_integer_frequency"} {
		if !keys[k] {
			t.Errorf("missing reason key %s", k)
		}
	}
	if Valid.Key() != "valid" {
		t.Fatal("valid key")
	}
}
