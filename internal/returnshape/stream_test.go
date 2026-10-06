package returnshape_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/types"
)

// fakeSource is a RowIter that hands out the SAME row maps and the same
// Components pointer on every call — the reuse a true streaming
// iterator is allowed.
type fakeSource struct {
	rows []map[string]any
	i    int
	comp *types.ResponseComponents
	meta *types.ResponseMetadata
}

func (f *fakeSource) Next(context.Context) (map[string]any, bool, error) {
	if f.i >= len(f.rows) {
		return nil, false, nil
	}
	f.i++
	return f.rows[f.i-1], true, nil
}
func (f *fakeSource) Close() error                          { return nil }
func (f *fakeSource) Metadata() *types.ResponseMetadata     { return f.meta }
func (f *fakeSource) Components() *types.ResponseComponents { return f.comp }

// TestRowIter_NeverMutatesInner: shaping clones — the inner iterator's
// rows (nested values included), Components (whose excluded slots keep
// accumulating) and Metadata are untouched by the prune, while the
// shaped views drop what the plan excludes.
func TestRowIter_NeverMutatesInner(t *testing.T) {
	src := &fakeSource{
		rows: []map[string]any{
			{"g": "a", "m": 1.25, "nested": map[string]any{"keep": 1.0, "drop": 2.0}},
			{"g": "b", "m": 2.5, "nested": map[string]any{"keep": 3.0, "drop": 4.0}},
		},
		comp: &types.ResponseComponents{
			Aggregations: []types.AggregationComponents{{Label: "m", N: 2, Operator: map[string]any{"sum": 3.75}}},
			Run:          &types.RunComponents{TotalRecords: 2},
		},
		meta: &types.ResponseMetadata{TotalRows: 2, FilteredRows: 2, CohortFile: "c.pulse"},
	}
	before := struct {
		rows []map[string]any
		comp types.ResponseComponents
		meta types.ResponseMetadata
	}{
		rows: []map[string]any{
			{"g": "a", "m": 1.25, "nested": map[string]any{"keep": 1.0, "drop": 2.0}},
			{"g": "b", "m": 2.5, "nested": map[string]any{"keep": 3.0, "drop": 4.0}},
		},
		comp: types.ResponseComponents{
			Aggregations: []types.AggregationComponents{{Label: "m", N: 2, Operator: map[string]any{"sum": 3.75}}},
			Run:          &types.RunComponents{TotalRecords: 2},
		},
		meta: *src.meta,
	}
	plan := planFor(t, &types.Return{Exclude: []string{
		"data[*].g", "data[*].nested.drop", "components.run", "components.aggregations[*].operator", "metadata.cohort_file",
	}})
	it := returnshape.NewRowIter(src, plan)
	ctx := context.Background()
	for i := 0; ; i++ {
		row, ok, err := it.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if _, has := row["g"]; has {
			t.Errorf("row %d kept excluded column g: %v", i, row)
		}
		if _, has := row["nested"].(map[string]any)["drop"]; has {
			t.Errorf("row %d kept excluded nested.drop: %v", i, row)
		}
	}
	comp := it.Components()
	if comp == nil || comp.Run != nil || comp.Aggregations[0].Operator != nil || comp.Aggregations[0].N != 2 {
		t.Errorf("shaped components = %+v", comp)
	}
	if md := it.Metadata(); md == nil || md.CohortFile != "" || md.TotalRows != 2 {
		t.Errorf("shaped metadata = %+v", md)
	}
	if !reflect.DeepEqual(src.rows, before.rows) {
		t.Errorf("inner rows mutated: %v", src.rows)
	}
	if !reflect.DeepEqual(*src.comp, before.comp) {
		t.Errorf("inner components mutated: %+v", src.comp)
	}
	if !reflect.DeepEqual(*src.meta, before.meta) {
		t.Errorf("inner metadata mutated: %+v", src.meta)
	}
}
