package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// measureAggregations are the built-in aggregators whose value is a
// measure (rounded under Return.precision). Together with
// countSemanticAggregations it must classify every registered type, so a
// new aggregator forces a count-or-measure decision.
var measureAggregations = []types.AggregationType{
	types.AGG_AVERAGE, types.AGG_CI_LOWER, types.AGG_CI_UPPER, types.AGG_DISTINCT_SUM,
	types.AGG_KURTOSIS, types.AGG_MAX, types.AGG_MEDIAN, types.AGG_MIN, types.AGG_MODE,
	types.AGG_PERCENTILE, types.AGG_RANGE, types.AGG_RATIO, types.AGG_SET_CARDINALITY_AVG,
	types.AGG_SET_INTERSECTION, types.AGG_SET_UNION, types.AGG_SKEWNESS, types.AGG_STDDEV,
	types.AGG_SUM, types.AGG_VARIANCE, types.AGG_WEIGHTED_MEAN, types.AGG_WELFORD, types.AGG_ZSCORE,
}

// wantCountSemantic is the exact count registry: dropping an entry (so
// a count column starts rounding) fails here.
var wantCountSemantic = []types.AggregationType{
	types.AGG_COUNT, types.AGG_DISTINCT_COUNT, types.AGG_NULL_COUNT, types.AGG_MODE_COUNT,
	types.AGG_FREQUENCY, types.AGG_SET_FREQUENCY, types.AGG_SET_CARDINALITY_SUM, types.AGG_SET_DISTINCT_VALUES,
}

func TestCountSemanticAggregations_Classified(t *testing.T) {
	measure := map[types.AggregationType]bool{}
	for _, a := range measureAggregations {
		measure[a] = true
	}
	for _, a := range types.AllAggregationTypes() {
		if countSemanticAggregations[a] == measure[a] {
			t.Errorf("%s must be classified exactly once (count %v, measure %v)", a, countSemanticAggregations[a], measure[a])
		}
	}
	if len(countSemanticAggregations) != len(wantCountSemantic) {
		t.Errorf("countSemanticAggregations has %d entries, want %d", len(countSemanticAggregations), len(wantCountSemantic))
	}
	for _, a := range wantCountSemantic {
		if !countSemanticAggregations[a] {
			t.Errorf("%s is not count-semantic", a)
		}
	}
}

// TestResolveReturn_ExactPaths: with precision set the plan carries the
// request's count columns / count crosstab figures as Exact paths; a
// measure, a normalized crosstab and a precision-free block carry none.
func TestResolveReturn_ExactPaths(t *testing.T) {
	g := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	cases := []struct {
		name string
		req  *types.Request
		want []string
	}{
		{"aggregations", &types.Request{
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_AVERAGE, Field: "x", Label: "m"},
				{Type: types.AGG_DISTINCT_COUNT, Field: "x"},
			},
			Return: &types.Return{Precision: 3},
		}, []string{"data[*].n", "data[*].AGG_DISTINCT_COUNT_x"}},
		{"crosstab count", &types.Request{
			Crosstab: &types.CrosstabSpec{Rows: g, Columns: g,
				Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "c"},
				MarginAggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "base"}, {Type: types.AGG_SUM, Field: "x", Label: "s"}}},
			Return: &types.Return{Precision: 3},
		}, []string{
			"data[*].c", "crosstab.matrix.cells[*][*].value", "crosstab.matrix.row_margins[*].value",
			"crosstab.matrix.column_margins[*].value", "crosstab.matrix.grand_total.value",
			"components.crosstab.row_margin_aggregations[*].base.value",
			"components.crosstab.column_margin_aggregations[*].base.value",
			"components.crosstab.grand_total_aggregations.base.value",
		}},
		{"crosstab normalized", &types.Request{
			Crosstab: &types.CrosstabSpec{Rows: g, Columns: g, Normalize: types.CrosstabNormalizeRow,
				Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "c"}},
			Return: &types.Return{Precision: 3},
		}, nil},
		{"no precision", &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			Return:       &types.Return{Exclude: []string{"metadata"}},
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ResolveReturn(tc.req, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := returnplan.Strings(p.Exact)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Exact = %v, want %v", got, tc.want)
			}
		})
	}
}
