package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// TestGroupedMatrices_TouchedBlocksOnly: a bucket's slots hold a
// co-moment block only for the merge blocks its own rows reached — a
// bucket fed rows in blocks 0 and 3 holds two blocks, one fed a single
// row holds one — and Merge moves a key one partition alone saw over
// whole while merging a shared key block-wise.
func TestGroupedMatrices_TouchedBlocksOnly(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}
	req := &types.Request{
		Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"x", "y"}}},
	}
	build := func() *GroupedMatrices {
		g, err := BuildGroupedMatrices(req, schema, nil, FullComputePlan(), limits.Limits{}, nil)
		if err != nil || g == nil {
			t.Fatalf("BuildGroupedMatrices = %v, %v", g, err)
		}
		return g
	}
	feed := func(g *GroupedMatrices, key string, pos int) {
		r := NewRecordWithWide(schema, map[string]float64{"x": float64(pos), "y": float64(pos * pos)}, nil, nil)
		r.SetMergePosition(0, 0, pos)
		if err := g.UpdateRow(key, r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	B := linalg.MergeBlockSize
	a, b := build(), build()
	feed(a, "wide", 1)
	feed(a, "wide", 2)
	feed(b, "wide", 3*B+5)
	feed(b, "thin", 3*B+6)
	if n := a.buckets["wide"][0].state.Len(); n != 1 {
		t.Fatalf("partition a bucket wide holds %d blocks, want 1", n)
	}
	if err := a.Merge(b); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(a.buckets["thin"]) != 1 {
		t.Fatalf("merged state lost bucket thin (only partition b saw it)")
	}
	if n := a.buckets["wide"][0].state.Len(); n != 2 {
		t.Errorf("merged bucket wide holds %d blocks, want 2 (blocks 0 and 3)", n)
	}
	if n := a.buckets["thin"][0].state.Len(); n != 1 {
		t.Errorf("merged bucket thin holds %d blocks, want 1", n)
	}

	res, comps, err := a.finalize([]string{"wide", "thin"}, req.Groups, nil)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(res) != 2 || len(comps) != 2 || comps[0].N != 3 || comps[1].N != 1 {
		t.Fatalf("finalize: %d results, components %+v; want 2 with n 3 and 1", len(res), comps)
	}

	if g, err := BuildGroupedMatrices(&types.Request{Matrices: req.Matrices}, schema, nil, FullComputePlan(), limits.Limits{}, nil); g != nil || err != nil {
		t.Errorf("ungrouped request built grouped matrix state %v, %v", g, err)
	}
}

// TestGroupedMatrices_FanOutRowStorePerBucket (U35 #267): a buffered
// (rank) matrix grouped by GROUP_SET_PER_ELEMENT reaches the buffered
// row store once per selected member — every row with all four members
// selected is admitted (and kept) by all four buckets, so the row
// stores hold 4 x records rows: the multiplicity the memory estimate's
// row_buffer_bytes charges (descriptor.FanOutMultiplicity).
func TestGroupedMatrices_FanOutRowStorePerBucket(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
	}}
	const n = 12
	recs := make([]*Record, n)
	for i := range recs {
		recs[i] = NewRecordWithWide(schema, map[string]float64{"x": float64(i), "y": float64((i * 7) % n)}, nil, map[string]any{"tags": uint64(0b1111)})
		recs[i].SetMergePosition(0, 0, i)
	}
	req := &types.Request{
		Groups:   []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
		Matrices: []types.MatrixSpec{{Name: "r", Type: types.MAT_CORRELATION, Fields: []string{"x", "y"}, Params: []byte(`{"method": "spearman"}`)}},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || len(resp.Components.Matrices) != 4 {
		t.Fatalf("matrix components = %+v, want one per member bucket", resp.Components)
	}
	var kept int
	for _, c := range resp.Components.Matrices {
		kept += c.N
	}
	if kept != 4*n {
		t.Fatalf("the four buckets admitted %d rows, want %d (every row once per selected member)", kept, 4*n)
	}
}
