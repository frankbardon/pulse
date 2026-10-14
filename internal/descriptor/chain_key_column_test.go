package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// TestChainPredict_KeyColumnFollowsGroupKeyField pins predict's chain
// stage typing (chainPredictedOutputFields + synthChainSchema) to the
// key column the runtime writes (vectors.GroupKeyField), matching
// processing.ChainOutputSchema's names
// (TestChainOutputSchema_KeyColumnFollowsGroupKeyField) for a
// key-renaming grouper shape.
func TestChainPredict_KeyColumnFollowsGroupKeyField(t *testing.T) {
	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
			{Type: types.GROUP_KMEANS, Vector: "profile"},
			{Type: types.GROUP_KMEANS, Fields: []string{"a", "b"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "region", Label: "n"},
		},
	}
	want := []string{"region", "GROUP_KMEANS_profile", "GROUP_KMEANS_a", "n"}

	if got := chainPredictedOutputFields(req, nil, vectors.AggregationKinds{}); !slices.Equal(got, want) {
		t.Fatalf("chainPredictedOutputFields = %v, want %v", got, want)
	}
	schema := synthChainSchema(req, nil, vectors.AggregationKinds{})
	got := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		got = append(got, f.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("synthChainSchema fields = %v, want %v", got, want)
	}
}
