package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// TestGroupedMatrices_TouchedBlocksOnly: a bucket's slots hold a
// co-moment block only for the merge blocks its own rows reached — a
// bucket fed rows in blocks 0 and 3 holds two blocks, one fed a single
// row holds one — and Merge moves a key one partition alone saw over
// whole while merging a shared key block-wise.
func TestGroupedMatrices_TouchedBlocksOnly(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}
	req := &types.Request{
		Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"x", "y"}}},
	}
	build := func() *GroupedMatrices {
		g, err := BuildGroupedMatrices(req, schema, nil, FullComputePlan())
		if err != nil || g == nil {
			t.Fatalf("BuildGroupedMatrices = %v, %v", g, err)
		}
		return g
	}
	feed := func(g *GroupedMatrices, key string, pos int) {
		r := NewRecordWithWide(schema, map[string]float64{"x": float64(pos), "y": float64(pos * pos)}, nil, nil)
		r.SetMergePosition(0, pos)
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

	res, comps, err := a.finalize([]string{"wide", "thin"})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(res) != 2 || len(comps) != 2 || comps[0].N != 3 || comps[1].N != 1 {
		t.Fatalf("finalize: %d results, components %+v; want 2 with n 3 and 1", len(res), comps)
	}

	if g, err := BuildGroupedMatrices(&types.Request{Matrices: req.Matrices}, schema, nil, FullComputePlan()); g != nil || err != nil {
		t.Errorf("ungrouped request built grouped matrix state %v, %v", g, err)
	}
}
