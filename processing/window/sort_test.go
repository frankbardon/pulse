package window

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestSortIndices_AscendingNumeric(t *testing.T) {
	rows := []map[string]any{
		{"x": 3.0},
		{"x": 1.0},
		{"x": 2.0},
	}
	idx := []int{0, 1, 2}
	sortIndices(rows, idx, nil, []types.OrderKey{{Field: "x"}})
	want := []int{1, 2, 0}
	if !reflect.DeepEqual(idx, want) {
		t.Errorf("idx = %v, want %v", idx, want)
	}
}

func TestSortIndices_DescendingNumeric(t *testing.T) {
	rows := []map[string]any{
		{"x": 1.0},
		{"x": 3.0},
		{"x": 2.0},
	}
	idx := []int{0, 1, 2}
	sortIndices(rows, idx, nil, []types.OrderKey{{Field: "x", Desc: true}})
	want := []int{1, 2, 0}
	if !reflect.DeepEqual(idx, want) {
		t.Errorf("idx = %v, want %v", idx, want)
	}
}

func TestSortIndices_NullsLast(t *testing.T) {
	rows := []map[string]any{
		{"x": nil},
		{"x": 2.0},
		{"x": 1.0},
		{"x": nil},
	}
	idx := []int{0, 1, 2, 3}
	sortIndices(rows, idx, nil, []types.OrderKey{{Field: "x"}})
	// Non-null values first (1.0 then 2.0), nulls trailing.
	if rows[idx[0]]["x"] != 1.0 || rows[idx[1]]["x"] != 2.0 {
		t.Errorf("non-null prefix wrong: %+v", []any{rows[idx[0]]["x"], rows[idx[1]]["x"]})
	}
	if rows[idx[2]]["x"] != nil || rows[idx[3]]["x"] != nil {
		t.Errorf("nulls not at end: %+v", []any{rows[idx[2]]["x"], rows[idx[3]]["x"]})
	}
}

func TestSortIndices_PartitionThenOrder(t *testing.T) {
	rows := []map[string]any{
		{"region": "us", "ts": 2.0},
		{"region": "eu", "ts": 1.0},
		{"region": "us", "ts": 1.0},
		{"region": "eu", "ts": 2.0},
	}
	idx := []int{0, 1, 2, 3}
	sortIndices(rows, idx, []string{"region"}, []types.OrderKey{{Field: "ts"}})
	// All eu come first (ts 1, ts 2), then us (ts 1, ts 2).
	got := []string{}
	for _, i := range idx {
		got = append(got, rows[i]["region"].(string))
	}
	want := []string{"eu", "eu", "us", "us"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("region order = %v, want %v", got, want)
	}
}

func TestSortIndices_StableForEqualKeys(t *testing.T) {
	rows := []map[string]any{
		{"x": 1.0, "id": "a"},
		{"x": 1.0, "id": "b"},
		{"x": 1.0, "id": "c"},
	}
	idx := []int{0, 1, 2}
	sortIndices(rows, idx, nil, []types.OrderKey{{Field: "x"}})
	// All equal keys: stable sort preserves input order.
	if !reflect.DeepEqual(idx, []int{0, 1, 2}) {
		t.Errorf("stable sort failed: idx = %v", idx)
	}
}

func TestPartition_NoPartitionBy(t *testing.T) {
	rows := []map[string]any{{"x": 1.0}, {"x": 2.0}, {"x": 3.0}}
	parts := partition(rows, []int{0, 1, 2}, nil)
	if len(parts) != 1 {
		t.Fatalf("partitions = %d, want 1", len(parts))
	}
	if !reflect.DeepEqual(parts[0], []int{0, 1, 2}) {
		t.Errorf("single partition = %v, want [0 1 2]", parts[0])
	}
}

func TestPartition_TwoGroups(t *testing.T) {
	rows := []map[string]any{
		{"region": "eu", "ts": 1.0},
		{"region": "eu", "ts": 2.0},
		{"region": "us", "ts": 1.0},
		{"region": "us", "ts": 2.0},
	}
	parts := partition(rows, []int{0, 1, 2, 3}, []string{"region"})
	if len(parts) != 2 {
		t.Fatalf("partitions = %d, want 2", len(parts))
	}
	if !reflect.DeepEqual(parts[0], []int{0, 1}) {
		t.Errorf("partition[0] = %v, want [0 1]", parts[0])
	}
	if !reflect.DeepEqual(parts[1], []int{2, 3}) {
		t.Errorf("partition[1] = %v, want [2 3]", parts[1])
	}
}

func TestPartition_SingletonGroups(t *testing.T) {
	rows := []map[string]any{
		{"region": "a"},
		{"region": "b"},
		{"region": "c"},
	}
	parts := partition(rows, []int{0, 1, 2}, []string{"region"})
	if len(parts) != 3 {
		t.Fatalf("partitions = %d, want 3", len(parts))
	}
}

func TestSortCache_SharesSortAcrossEqualTuples(t *testing.T) {
	rows := []map[string]any{{"x": 2.0}, {"x": 1.0}}
	cache := newSortCache(rows)
	a, _ := cache.get(nil, []types.OrderKey{{Field: "x"}})
	b, _ := cache.get(nil, []types.OrderKey{{Field: "x"}})
	// Same underlying slice.
	if &a[0] != &b[0] {
		t.Error("expected cache hit to share slice")
	}
}

func TestSortCache_DistinctTuplesGetDistinctSorts(t *testing.T) {
	rows := []map[string]any{{"x": 2.0}, {"x": 1.0}}
	cache := newSortCache(rows)
	a, _ := cache.get(nil, []types.OrderKey{{Field: "x"}})
	b, _ := cache.get(nil, []types.OrderKey{{Field: "x", Desc: true}})
	if &a[0] == &b[0] {
		t.Error("expected distinct sorts for ASC vs DESC")
	}
}
