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
	if ClassOf("TEST_T") != ClassNone || ClassOf("AGG_EXT_FOO_BAR") != ClassNone {
		t.Fatal("non-aggregator names must be ClassNone")
	}
	if !IsAware("AGG_SUM") || !IsAware("AGG_MEDIAN") || IsAware("AGG_MIN") || IsAware("AGG_CI_LOWER") {
		t.Fatal("IsAware mismatch")
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
