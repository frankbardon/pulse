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
		AGG_GEO_CENTROID:   false,
		AGG_GEO_BBOX:       false,
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
		ATTR_ZSCORE:     true,
		ATTR_TSCORE:     true,
		ATTR_NORMALIZED: true,
		ATTR_FORMULA:    true,
		ATTR_PERCENTILE: false,
		ATTR_DATE_PART:  true,
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
		FILTER_INCLUDE:             true,
		FILTER_EXCLUDE:             true,
		FILTER_RANGE:               true,
		FILTER_EXPRESSION:          true,
		FILTER_GEO_WITHIN:          true,
		FILTER_GEO_WITHIN_RADIUS_M: true,
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
		GROUP_H3_CELL:  true,
		GROUP_QUANTILE: false,
		GROUP_DATE:     false,
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
