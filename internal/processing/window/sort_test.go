package window

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
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

// TestCompareCell_Decimal128ByValue: decimal128 cells (the Record's
// unboxed encoding.Decimal128) order by value, negatives first, nulls
// last — before, every decimal compared equal and kept input order.
func TestCompareCell_Decimal128ByValue(t *testing.T) {
	d := encoding.NewDecimal128FromInt
	rows := []map[string]any{{"x": d(1050)}, {"x": d(-325)}, {"x": nil}, {"x": d(250)}, {"x": d(10000)}}
	idx := []int{0, 1, 2, 3, 4}
	sortIndices(rows, idx, nil, []types.OrderKey{{Field: "x"}})
	if want := []int{1, 3, 0, 4, 2}; !reflect.DeepEqual(idx, want) {
		t.Errorf("idx = %v, want %v", idx, want)
	}
}

// nilSortValue is a SortValuer whose SortValue is null.
type nilSortValue struct{}

func (nilSortValue) SortValue() any { return nil }

// TestCompareKey_NullsLastBothDirections: a null (nil, missing, or a
// SortValuer whose SortValue is nil) trails every non-null cell under
// Desc as well as Asc, for every cell family the comparator orders.
// Before, Desc negated compareCell wholesale — null term included — so
// nulls led every descending order.
func TestCompareKey_NullsLastBothDirections(t *testing.T) {
	d := encoding.NewDecimal128FromInt
	families := map[string]any{
		"float":          2.5,
		"negative epoch": float64(-1),
		"bool":           true,
		"decimal":        d(1050),
		"scaled decimal": ScaledDecimal{Value: d(1050), Scale: 2},
		"label":          "b",
	}
	for name, v := range families {
		for _, desc := range []bool{false, true} {
			for _, null := range []any{nil, nilSortValue{}} {
				if got := CompareKey(v, null, desc); got >= 0 {
					t.Errorf("%s desc=%v: CompareKey(value, %T) = %d, want < 0", name, desc, null, got)
				}
				if got := CompareKey(null, v, desc); got <= 0 {
					t.Errorf("%s desc=%v: CompareKey(%T, value) = %d, want > 0", name, desc, null, got)
				}
			}
		}
	}
	if got := CompareKey(nil, nilSortValue{}, true); got != 0 {
		t.Errorf("two nulls = %d, want 0", got)
	}
}

// TestSortAndSortIndices_DescNullsLast: both public ordering entry
// points (Request.Sort's Sort, window order_by's sortIndices) keep nulls
// and missing keys last under Desc.
func TestSortAndSortIndices_DescNullsLast(t *testing.T) {
	mk := func() []map[string]any {
		return []map[string]any{{"id": 1, "x": 1.0}, {"id": 2, "x": nil}, {"id": 3, "x": 3.0}, {"id": 4}, {"id": 5, "x": 2.0}}
	}
	keys := []types.OrderKey{{Field: "x", Desc: true}}
	rows := mk()
	Sort(rows, keys)
	var got []any
	for _, r := range rows {
		got = append(got, r["id"])
	}
	if want := []any{3, 5, 1, 2, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("Sort desc ids = %v, want %v", got, want)
	}
	rows = mk()
	idx := []int{0, 1, 2, 3, 4}
	sortIndices(rows, idx, nil, keys)
	if want := []int{2, 4, 0, 1, 3}; !reflect.DeepEqual(idx, want) {
		t.Errorf("sortIndices desc idx = %v, want %v", idx, want)
	}
}

// TestCompareCell_ScaledDecimal: ScaledDecimal cells of one scale order
// exactly; across scales and against a plain number (an aggregate that
// fell back to f64) by value.
func TestCompareCell_ScaledDecimal(t *testing.T) {
	d := encoding.NewDecimal128FromInt
	cases := []struct {
		a, b any
		want int
	}{
		{ScaledDecimal{d(-325), 2}, ScaledDecimal{d(250), 2}, -1},
		{ScaledDecimal{d(10000), 2}, ScaledDecimal{d(1050), 2}, 1},
		{ScaledDecimal{d(105), 1}, ScaledDecimal{d(1049), 2}, 1}, // 10.5 > 10.49
		{ScaledDecimal{d(1050), 2}, 11.0, -1},
	}
	for _, c := range cases {
		if got := compareCell(c.a, c.b); got != c.want {
			t.Errorf("compareCell(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
