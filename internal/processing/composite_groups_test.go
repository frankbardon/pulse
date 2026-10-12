package processing

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// TestCompositeKeySet_Expand pins the per-record composite expansion:
// the cartesian product of each position's key set, outer → inner, and
// nothing at all when any position is empty (a null key at any
// position).
func TestCompositeKeySet_Expand(t *testing.T) {
	cases := []struct {
		name   string
		sets   [][]string
		want   []string
		tuples []types.AxisKey
	}{
		{"Single", [][]string{{"north"}, {"F"}}, []string{"north\x00F"}, []types.AxisKey{{"north", "F"}}},
		{"FanOutInner", [][]string{{"north"}, {"a", "b"}},
			[]string{"north\x00a", "north\x00b"}, []types.AxisKey{{"north", "a"}, {"north", "b"}}},
		{"FanOutBoth", [][]string{{"x", "y"}, {"a", "b"}, {"F"}},
			[]string{"x\x00a\x00F", "x\x00b\x00F", "y\x00a\x00F", "y\x00b\x00F"},
			[]types.AxisKey{{"x", "a", "F"}, {"x", "b", "F"}, {"y", "a", "F"}, {"y", "b", "F"}}},
		{"OuterNull", [][]string{nil, {"F"}}, nil, nil},
		{"InnerNull", [][]string{{"north"}, {}}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := NewCompositeKeySet(len(c.sets))
			var got []string
			if err := set.Expand(c.sets, func(k string) error { got = append(got, k); return nil }); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("keys %q, want %q", got, c.want)
			}
			if set.Len() != len(c.want) {
				t.Fatalf("Len %d, want %d", set.Len(), len(c.want))
			}
			for i, k := range c.want {
				if tup := set.Tuple(k); !reflect.DeepEqual(tup, c.tuples[i]) {
					t.Errorf("Tuple(%q) = %v, want %v", k, tup, c.tuples[i])
				}
			}
		})
	}
}

// TestCompositeKeySet_ExpandWidthMismatch: a key-set count that is not
// the grouper count is an internal error, never a silent truncation.
func TestCompositeKeySet_ExpandWidthMismatch(t *testing.T) {
	set := NewCompositeKeySet(2)
	if err := set.Expand([][]string{{"a"}}, func(string) error { return nil }); err == nil {
		t.Fatal("width mismatch accepted")
	}
}

// TestCompositeKeySet_RepeatAllocatesNothing: a combination already
// observed is looked up, not rebuilt.
func TestCompositeKeySet_RepeatAllocatesNothing(t *testing.T) {
	set := NewCompositeKeySet(2)
	sets := [][]string{{"north"}, {"F"}}
	emit := func(string) error { return nil }
	if err := set.Expand(sets, emit); err != nil {
		t.Fatal(err)
	}
	if a := testing.AllocsPerRun(100, func() { _ = set.Expand(sets, emit) }); a != 0 {
		t.Fatalf("repeat Expand allocated %v times", a)
	}
}

// TestCompositeKeySet_Merge: a partial's keys join with their tuples.
func TestCompositeKeySet_Merge(t *testing.T) {
	a, b := NewCompositeKeySet(2), NewCompositeKeySet(2)
	emit := func(string) error { return nil }
	_ = a.Expand([][]string{{"n"}, {"F"}}, emit)
	_ = b.Expand([][]string{{"s"}, {"M"}}, emit)
	a.Merge(b)
	if a.Len() != 2 || !reflect.DeepEqual(a.Tuple("s\x00M"), types.AxisKey{"s", "M"}) {
		t.Fatalf("merge: len %d tuple %v", a.Len(), a.Tuple("s\x00M"))
	}
}

// TestOrderCompositeGroupKeys: each position orders by its own grouper's
// native order (include list first, else alphabetical), outer → inner.
func TestOrderCompositeGroupKeys(t *testing.T) {
	outer := &categoryGrouper{include: buildIncludeFilter([]string{"west", "north"})}
	inner := &categoryGrouper{}
	set := NewCompositeKeySet(2)
	for _, s := range [][]string{{"north", "M"}, {"west", "M"}, {"north", "F"}, {"west", "F"}} {
		if err := set.Expand([][]string{{s[0]}, {s[1]}}, func(string) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	got := OrderCompositeGroupKeys(set.Keys(), set, []Grouper{outer, inner})
	want := []string{"west\x00F", "west\x00M", "north\x00F", "north\x00M"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order %q, want %q", got, want)
	}
}

// TestWriteGroupKeyColumns: each column comes from its tuple element,
// under the grouper's key column (GROUP_KMEANS → GROUP_KMEANS_<first>).
func TestWriteGroupKeyColumns(t *testing.T) {
	row := map[string]any{}
	WriteGroupKeyColumns(row, []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "region"},
		{Type: types.GROUP_KMEANS, Fields: []string{"y"}},
	}, types.AxisKey{"north", "cluster_1"})
	want := map[string]any{"region": "north", "GROUP_KMEANS_y": "cluster_1"}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("row %v, want %v", row, want)
	}
}

// TestPartitionComposite_MaxGroupsObserved: the limit trips on the
// observed composite count, not on either grouper's count.
func TestPartitionComposite_MaxGroupsObserved(t *testing.T) {
	recs := []*Record{
		NewRecord(nil, map[string]float64{"a": 1, "b": 1}),
		NewRecord(nil, map[string]float64{"a": 1, "b": 2}),
		NewRecord(nil, map[string]float64{"a": 2, "b": 1}),
	}
	groups := []*types.Group{{Type: types.GROUP_RANGE, Field: "a", Interval: 1}, {Type: types.GROUP_RANGE, Field: "b", Interval: 1}}
	p := NewProcessor(nil)
	gs, err := p.newRequestGroupers(groups)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := partitionComposite(groups, gs, recs, limits.Limits{MaxGroups: 2}, nil); err == nil {
		t.Fatal("3 observed composite keys passed MaxGroups=2")
	}
	gs, _ = p.newRequestGroupers(groups)
	part, err := partitionComposite(groups, gs, recs, limits.Limits{MaxGroups: 3}, nil)
	if err != nil || part.Set.Len() != 3 {
		t.Fatalf("at the limit: %v, %v", part, err)
	}
}
