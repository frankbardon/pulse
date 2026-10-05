package weighting

import (
	"math"
	"reflect"
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
	if !IsAware("AGG_SUM") || !IsAware("AGG_MEDIAN") || IsAware("AGG_MIN") || !IsAware("AGG_CI_LOWER") {
		t.Fatal("IsAware mismatch")
	}
}

// TestClassify_NonAggregatorFamilies pins the non-aggregator classes
// (.claude/reference/weighting.md, Refusal rules): every built-in
// TEST_* and REG_*, ATTR_ZSCORE / TSCORE / PERCENTILE and
// GROUP_QUANTILE refuse under any weight; ATTR_NORMALIZED and every
// WIN_* are not weightable (skipped under a default, refused under an
// explicit weight); every other attribute, grouper, filterer and
// feature is untouched (ClassNone). The tests whose weighted computation
// exists (testClasses: the seven moment tests) are ClassAware.
func TestClassify_NonAggregatorFamilies(t *testing.T) {
	want := map[string]Class{}
	for _, tt := range types.AllTestTypes() {
		want[string(tt)] = ClassRefuse
	}
	for tt, c := range testClasses {
		want[string(tt)] = c
	}
	for _, rt := range types.AllRegressionTypes() {
		want[string(rt)] = ClassRefuse
	}
	// U12 E4-S1: weighted OLS (plain + penalised) under both kinds;
	// the conjugate Bayes posterior under frequency only.
	want[string(types.REG_OLS)] = ClassAware
	want[string(types.REG_BAYES_LINEAR)] = ClassFrequencyOnly
	// U12 E4-S2: weighted GLM IRLS under both kinds.
	want[string(types.REG_GLM)] = ClassAware
	for _, at := range types.AllAttributeTypes() {
		want[string(at)] = ClassNone
	}
	for _, a := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_PERCENTILE} {
		want[string(a)] = ClassRefuse
	}
	want[string(types.ATTR_NORMALIZED)] = ClassNotWeightable
	// U12 E4-S2: the regression attributes refit REG_OLS weighted.
	for _, a := range []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE} {
		want[string(a)] = ClassAware
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
		aware := c == ClassAware || c == ClassFrequencyOnly
		if aware != IsAware(op) || aware != (KindsOf(op) != nil) {
			t.Errorf("%s: aware %v (kinds %v) disagrees with class %v", op, IsAware(op), KindsOf(op), c)
		}
	}
}

// TestKindsOf: the kinds accessor is table-driven off the class —
// both kinds (frequency first) for ClassAware, frequency alone for
// ClassFrequencyOnly, none otherwise — and IsAware agrees with it.
func TestKindsOf(t *testing.T) {
	both := []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
	freq := []types.WeightKind{types.WeightKindFrequency}
	for _, at := range types.AllAggregationTypes() {
		op := string(at)
		var want []types.WeightKind
		if ClassOf(op) == ClassAware {
			want = both
		}
		if !reflect.DeepEqual(KindsOf(op), want) {
			t.Errorf("KindsOf(%s) = %v, want %v", op, KindsOf(op), want)
		}
		if IsAware(op) != (want != nil) {
			t.Errorf("IsAware(%s) disagrees with KindsOf", op)
		}
	}
	for _, tt := range []types.TestType{types.TEST_MANN_WHITNEY_U, types.TEST_WILCOXON_SR, types.TEST_KRUSKAL_WALLIS, types.TEST_SPEARMAN_R, types.TEST_KENDALL_TAU,
		types.TEST_FISHER_EXACT, types.TEST_KS, types.TEST_BROWN_FORSYTHE} {
		if !reflect.DeepEqual(KindsOf(string(tt)), freq) {
			t.Errorf("KindsOf(%s) = %v, want frequency only", tt, KindsOf(string(tt)))
		}
	}
	restore := OverrideClassForTest("TEST_STUB_FREQ_ONLY", ClassFrequencyOnly)
	if !reflect.DeepEqual(KindsOf("TEST_STUB_FREQ_ONLY"), freq) || !IsAware("TEST_STUB_FREQ_ONLY") {
		t.Fatalf("frequency-only stub: kinds %v aware %v", KindsOf("TEST_STUB_FREQ_ONLY"), IsAware("TEST_STUB_FREQ_ONLY"))
	}
	restore()
	if ClassOf("TEST_STUB_FREQ_ONLY") != ClassNone {
		t.Fatal("restore left the stub classed")
	}
	for _, c := range []Class{ClassNone, ClassNotWeightable, ClassRefuse} {
		if KindsOfClass(c) != nil {
			t.Errorf("class %v carries kinds %v", c, KindsOfClass(c))
		}
	}
}

// TestRefusalReasons_AreRefused: every permanent refusal is a refused
// operator, the named ones carry a reason, and a lifted operator
// (TEST_T) does not.
func TestRefusalReasons_AreRefused(t *testing.T) {
	for op := range refusalReasons {
		if ClassOf(op) != ClassRefuse {
			t.Errorf("%s has a refusal reason but class %v", op, ClassOf(op))
		}
	}
	for _, op := range []string{"TEST_SHAPIRO_WILK", "TEST_TUKEY_HSD", "TEST_ANOVA_RM", "TEST_TREND", "ATTR_PERCENTILE"} {
		if RefusalReason(op) == "" {
			t.Errorf("%s: no permanent refusal reason", op)
		}
	}
	if RefusalReason("TEST_T") != "" || RefusalReason("AGG_SUM") != "" {
		t.Fatal("a liftable operator carries a permanent reason")
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

// TestClassify_MomentTestsAware pins the E1-S2 / E1-S3 flips: the seven
// moment tests, prop-z and χ² compute weighted under both kinds.
func TestClassify_MomentTestsAware(t *testing.T) {
	both := []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
	for _, op := range []types.TestType{types.TEST_T, types.TEST_WELCH, types.TEST_PAIRED_T,
		types.TEST_Z_TWO_SAMPLE, types.TEST_ANOVA_F, types.TEST_ANOVA_WELCH, types.TEST_PEARSON_R,
		types.TEST_PROP_Z, types.TEST_CHISQ} {
		if !reflect.DeepEqual(KindsOf(string(op)), both) {
			t.Errorf("%s: kinds %v, want both", op, KindsOf(string(op)))
		}
	}
	if IsAware(string(types.TEST_SHAPIRO_WILK)) {
		t.Fatal("TEST_SHAPIRO_WILK has no weighted computation yet")
	}
}
