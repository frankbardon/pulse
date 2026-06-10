package types

import "testing"

// TestStreamability_AggregationsKnown asserts every aggregation type returns
// the documented streamability value. Adding a new aggregation requires
// extending the switch in streamability.go AND this table.
func TestStreamability_AggregationsKnown(t *testing.T) {
	expected := map[AggregationType]bool{
		AGG_COUNT:          true,
		AGG_SUM:            true,
		AGG_AVERAGE:        true,
		AGG_MIN:            true,
		AGG_MAX:            true,
		AGG_STDDEV:         true,
		AGG_VARIANCE:       true,
		AGG_RANGE:          true,
		AGG_FREQUENCY:      true,
		AGG_MODE:           true,
		AGG_SKEWNESS:       true,
		AGG_KURTOSIS:       true,
		AGG_DISTINCT_COUNT: true,
		AGG_MEDIAN:         false,
		AGG_PERCENTILE:     false,
		AGG_ZSCORE:         false,
		AGG_NULL_COUNT:     true,
		AGG_WEIGHTED_MEAN:  true,
		AGG_RATIO:          true,
		AGG_CI_LOWER:       true,
		AGG_CI_UPPER:       true,

		AGG_SET_UNION:           true,
		AGG_SET_INTERSECTION:    true,
		AGG_SET_FREQUENCY:       true,
		AGG_SET_CARDINALITY_SUM: true,
		AGG_SET_CARDINALITY_AVG: true,
		AGG_SET_DISTINCT_VALUES: true,
	}
	for _, agg := range AllAggregationTypes() {
		want, ok := expected[agg]
		if !ok {
			t.Fatalf("aggregation %s missing from streamability table — declare it in types/streamability.go and add an entry here", agg)
		}
		if got := agg.Streamable(); got != want {
			t.Errorf("%s.Streamable() = %v, want %v", agg, got, want)
		}
	}
	if len(expected) != len(AllAggregationTypes()) {
		t.Fatalf("streamability table size mismatch: %d entries, %d types", len(expected), len(AllAggregationTypes()))
	}
}

func TestStreamability_AttributesKnown(t *testing.T) {
	expected := map[AttributeType]bool{
		ATTR_ZSCORE:       true,
		ATTR_TSCORE:       true,
		ATTR_NORMALIZED:   true,
		ATTR_FORMULA:      true,
		ATTR_PERCENTILE:   false,
		ATTR_DATE_PART:    true,
		ATTR_REG_FITTED:   true,
		ATTR_REG_RESIDUAL: true,
		ATTR_REG_LEVERAGE: true,

		ATTR_SET_POPCOUNT: true,
		ATTR_SET_HAS:      true,
	}
	for _, a := range AllAttributeTypes() {
		want, ok := expected[a]
		if !ok {
			t.Fatalf("attribute %s missing from streamability table", a)
		}
		if got := a.Streamable(); got != want {
			t.Errorf("%s.Streamable() = %v, want %v", a, got, want)
		}
	}
	if len(expected) != len(AllAttributeTypes()) {
		t.Fatalf("attribute streamability table size mismatch: %d entries, %d types", len(expected), len(AllAttributeTypes()))
	}
}

func TestStreamability_FilterersKnown(t *testing.T) {
	expected := map[FiltererType]bool{
		FILTER_INCLUDE:    true,
		FILTER_EXCLUDE:    true,
		FILTER_RANGE:      true,
		FILTER_EXPRESSION: true,
		FILTER_NULL:       true,
		FILTER_TRUE:       true,
		FILTER_FALSE:      true,

		FILTER_SET_CONTAINS_ANY:  true,
		FILTER_SET_CONTAINS_ALL:  true,
		FILTER_SET_CONTAINS_NONE: true,
		FILTER_SET_EQUALS:        true,
	}
	for _, f := range AllFiltererTypes() {
		want, ok := expected[f]
		if !ok {
			t.Fatalf("filterer %s missing from streamability table", f)
		}
		if got := f.Streamable(); got != want {
			t.Errorf("%s.Streamable() = %v, want %v", f, got, want)
		}
	}
	if len(expected) != len(AllFiltererTypes()) {
		t.Fatalf("filterer streamability table size mismatch: %d entries, %d types", len(expected), len(AllFiltererTypes()))
	}
}

func TestStreamability_GroupsKnown(t *testing.T) {
	expected := map[GroupType]bool{
		GROUP_CATEGORY: true,
		GROUP_ROUNDED:  true,
		GROUP_RANGE:    true,
		GROUP_QUANTILE: false,
		GROUP_DATE:     false,

		GROUP_SET_VALUE:       true,
		GROUP_SET_PER_ELEMENT: true,
	}
	for _, g := range AllGroupTypes() {
		want, ok := expected[g]
		if !ok {
			t.Fatalf("grouper %s missing from streamability table", g)
		}
		if got := g.Streamable(); got != want {
			t.Errorf("%s.Streamable() = %v, want %v", g, got, want)
		}
	}
	if len(expected) != len(AllGroupTypes()) {
		t.Fatalf("grouper streamability table size mismatch: %d entries, %d types", len(expected), len(AllGroupTypes()))
	}
}

func TestStreamability_WindowsKnown(t *testing.T) {
	for _, w := range AllWindowTypes() {
		if w.Streamable() {
			t.Errorf("%s.Streamable() = true, want false (no window operator streams today)", w)
		}
	}
}

// TestStreamability_TestsKnown asserts every statistical test type returns
// the documented tier-1 streamability value. Adding a new test type
// requires extending the switch in streamability.go AND this table.
func TestStreamability_TestsKnown(t *testing.T) {
	expected := map[TestType]bool{
		TEST_T:              true,
		TEST_WELCH:          true,
		TEST_CHISQ:          true,
		TEST_ANOVA_F:        true,
		TEST_KS:             false,
		TEST_TUKEY_HSD:      false,
		TEST_TREND:          false,
		TEST_PEARSON_R:      true,
		TEST_PAIRED_T:       true,
		TEST_PROP_Z:         true,
		TEST_MANN_WHITNEY_U: false,
		TEST_WILCOXON_SR:    false,
		TEST_KRUSKAL_WALLIS: false,
		TEST_SPEARMAN_R:     false,
		TEST_KENDALL_TAU:    false,
		TEST_ANOVA_WELCH:    true,
		TEST_ANOVA_RM:       false,
		TEST_BROWN_FORSYTHE: false,
		TEST_FISHER_EXACT:   false,
		TEST_SHAPIRO_WILK:   false,
		TEST_Z_TWO_SAMPLE:   true,
	}
	for _, tt := range AllTestTypes() {
		want, ok := expected[tt]
		if !ok {
			t.Fatalf("test type %s missing from streamability table — declare it in types/streamability.go and add an entry here", tt)
		}
		if got := tt.Streamable(); got != want {
			t.Errorf("%s.Streamable() = %v, want %v", tt, got, want)
		}
	}
	if len(expected) != len(AllTestTypes()) {
		t.Fatalf("test streamability table size mismatch: %d entries, %d types", len(expected), len(AllTestTypes()))
	}
}

// TestRegressionTypesKnown asserts every regression type returns the
// documented streamability value and is listed in AllRegressionTypes().
// Adding a new RegressionType requires extending the switch in
// regression.go AND this table.
func TestRegressionTypesKnown(t *testing.T) {
	expected := map[RegressionType]bool{
		REG_OLS:          true,
		REG_GLM:          false,
		REG_BAYES_LINEAR: true,
	}
	for _, rt := range AllRegressionTypes() {
		want, ok := expected[rt]
		if !ok {
			t.Fatalf("regression type %s missing from streamability table — declare it in types/regression.go and add an entry here", rt)
		}
		if got := rt.Streamable(); got != want {
			t.Errorf("%s.Streamable() = %v, want %v", rt, got, want)
		}
	}
	if len(expected) != len(AllRegressionTypes()) {
		t.Fatalf("regression streamability table size mismatch: %d entries, %d types", len(expected), len(AllRegressionTypes()))
	}
}

// TestRegressionSpec_StreamableModifierDowngrade asserts the spec-level
// Streamable helper downgrades to false whenever a Resample or
// Selection modifier is set, regardless of the underlying type's
// Streamable() value. After Phase 2, both unpenalized and penalized
// REG_OLS stream (the regularized finalize solver consumes the
// streaming Gram without re-reading rows); Bayes (Phase 4) remains
// buffered even though its type-level Streamable() returns true.
func TestRegressionSpec_StreamableModifierDowngrade(t *testing.T) {
	cases := []struct {
		name string
		spec RegressionSpec
		want bool
	}{
		{
			name: "unpenalized ols streams",
			spec: RegressionSpec{Type: REG_OLS},
			want: true,
		},
		{
			name: "ols with l1 penalty streams (Phase 2)",
			spec: RegressionSpec{Type: REG_OLS, Penalty: "l1", Alpha: 0.1},
			want: true,
		},
		{
			name: "ols with l2 penalty streams (Phase 2)",
			spec: RegressionSpec{Type: REG_OLS, Penalty: "l2", Alpha: 0.1},
			want: true,
		},
		{
			name: "ols with elasticnet penalty streams (Phase 2)",
			spec: RegressionSpec{Type: REG_OLS, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5},
			want: true,
		},
		{
			name: "ols with bootstrap is buffered",
			spec: RegressionSpec{Type: REG_OLS, Resample: "bootstrap"},
			want: false,
		},
		{
			name: "ols with jackknife is buffered",
			spec: RegressionSpec{Type: REG_OLS, Resample: "jackknife"},
			want: false,
		},
		{
			name: "ols with stepwise selection is buffered",
			spec: RegressionSpec{Type: REG_OLS, Selection: "stepwise", Criterion: "aic"},
			want: false,
		},
		{
			name: "bayes linear streams (Phase 4)",
			spec: RegressionSpec{Type: REG_BAYES_LINEAR},
			want: true,
		},
		{
			name: "bayes with bootstrap is buffered",
			spec: RegressionSpec{Type: REG_BAYES_LINEAR, Resample: "bootstrap"},
			want: false,
		},
		{
			name: "bayes with stepwise selection is buffered",
			spec: RegressionSpec{Type: REG_BAYES_LINEAR, Selection: "stepwise", Criterion: "aic"},
			want: false,
		},
		{
			name: "glm always buffered",
			spec: RegressionSpec{Type: REG_GLM, Family: "binomial"},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.Streamable(); got != c.want {
				t.Errorf("RegressionSpec.Streamable() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestStreamability_FeaturesKnown(t *testing.T) {
	expected := map[FeatureType]bool{
		FEAT_LOG:              true,
		FEAT_SQRT:             true,
		FEAT_BUCKETIZE:        true,
		FEAT_ONE_HOT:          true,
		FEAT_DATE_FEATURES:    true,
		FEAT_FREQUENCY_ENCODE: true,
		FEAT_TARGET_ENCODE:    true,
		FEAT_TRAIN_TEST_SPLIT: true,
		FEAT_POLY:             true,
	}
	for _, f := range AllFeatureTypes() {
		want, ok := expected[f]
		if !ok {
			t.Fatalf("feature %s missing from streamability table", f)
		}
		if got := f.Streamable(); got != want {
			t.Errorf("%s.Streamable() = %v, want %v", f, got, want)
		}
	}
	if len(expected) != len(AllFeatureTypes()) {
		t.Fatalf("feature streamability table size mismatch: %d entries, %d types", len(expected), len(AllFeatureTypes()))
	}
}
