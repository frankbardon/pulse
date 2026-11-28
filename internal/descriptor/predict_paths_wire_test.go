package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_DefaultsAppliedPathsAreWireKeys pins a sample of
// defaults_applied[].path: wire keys and indices, matching every other
// details.path (#795) — top-level aggregation and group, a crosstab's
// multi-measure cell and its axes.
func TestPredict_DefaultsAppliedPathsAreWireKeys(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "revenue", Type: encoding.FieldTypeF64, Description: "Revenue for the row in USD."},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "east", "west"), Description: "Sales region of the row."},
	}}
	data := buildTestPulseFile(t, schema)
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Field: "revenue"}},
		Groups:       []*types.Group{{Field: "region"}},
	}
	ct := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Field: "region"}},
		Columns: []*types.Group{{Field: "region"}},
		Cells: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "revenue", Label: "n"},
			{Field: "revenue", Label: "total"},
		},
	}}
	cases := []struct {
		name string
		req  *types.Request
		want [][]string
	}{
		{"top_level", req, [][]string{{"aggregations", "0", "type"}, {"groups", "0", "type"}}},
		{"crosstab", ct, [][]string{{"crosstab", "cells", "1", "type"}, {"crosstab", "rows", "0", "type"}, {"crosstab", "columns", "0", "type"}}},
	}
	for _, c := range cases {
		res, ok := predictFromBytes(data, c.req, nil).Data.(*descriptor.PredictResult)
		if !ok {
			t.Fatalf("%s: Data is not *PredictResult", c.name)
		}
		if len(res.DefaultsApplied) != len(c.want) {
			t.Fatalf("%s: defaults_applied = %+v, want %d entries", c.name, res.DefaultsApplied, len(c.want))
		}
		for i, w := range c.want {
			if got := res.DefaultsApplied[i].Path; !slices.Equal(got, w) {
				t.Errorf("%s: defaults_applied[%d].path = %q, want %q", c.name, i, got, w)
			}
		}
	}
}
