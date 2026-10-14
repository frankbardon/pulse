package processing

import (
	"testing"

	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// TestChainOutputSchema_KeyColumnFollowsGroupKeyField pins the chain
// stage schema's grouper column to the column the runtime writes the
// bucket key under (vectors.GroupKeyField) — not the slot's Field, which
// a vector-input grouper leaves empty. The descriptor twin
// (TestChainPredict_KeyColumnFollowsGroupKeyField) pins the same names,
// so predict and runtime chain typing agree.
func TestChainOutputSchema_KeyColumnFollowsGroupKeyField(t *testing.T) {
	req := chainKeyColumnRequest()
	schema, err := ChainOutputSchema(req, nil, vectors.AggregationKinds{})
	if err != nil {
		t.Fatalf("ChainOutputSchema: %v", err)
	}
	got := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		got = append(got, f.Name)
	}
	want := []string{"region", "GROUP_KMEANS_profile", "GROUP_KMEANS_a", "n"}
	if len(got) != len(want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fields = %v, want %v", got, want)
		}
	}
}

func chainKeyColumnRequest() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
			{Type: types.GROUP_KMEANS, Vector: "profile"},
			{Type: types.GROUP_KMEANS, Fields: []string{"a", "b"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "region", Label: "n"},
		},
	}
}
