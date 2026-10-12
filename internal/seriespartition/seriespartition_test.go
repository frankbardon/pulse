package seriespartition

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func groups(n int) []*types.Group {
	all := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "region"},
		{Type: types.GROUP_CATEGORY, Field: "store"},
		{Type: types.GROUP_DATE, Field: "day"},
	}
	return all[:n]
}

func spec(params string) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: types.OverlayKindZScoreVsRolling}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// Absent / null / unparseable partition_by partitions by every key but
// the last; an explicit leading prefix gives its length; [] gives 0.
func TestDepth(t *testing.T) {
	for _, c := range []struct {
		groups int
		params string
		want   int
	}{
		{0, "", 0},
		{1, "", 0},
		{2, "", 1},
		{3, "", 2},
		{3, `{"window": 2}`, 2},
		{3, `{"partition_by": null}`, 2},
		{3, `[1]`, 2},
		{3, `{"partition_by": []}`, 0},
		{3, `{"partition_by": ["region"]}`, 1},
		{3, `{"partition_by": ["region", "store"]}`, 2},
		{3, `{"partition_by": ["region", "store", "day"]}`, 3},
		{1, `{"partition_by": []}`, 0},
		{2, `{"partition_by": ["region"], "window": 3}`, 1},
	} {
		got, err := Depth(groups(c.groups), spec(c.params), 0)
		if err != nil || got != c.want {
			t.Errorf("%d groups %s: Depth = (%d, %v), want %d", c.groups, c.params, got, err, c.want)
		}
	}
}

// A vector-input grouper's key column is <TYPE>_<first field>, and
// partition_by names it by that column.
func TestDepth_VectorGroupKeyColumn(t *testing.T) {
	gs := []*types.Group{
		{Type: types.GROUP_KMEANS, Fields: []string{"x", "y"}},
		{Type: types.GROUP_CATEGORY, Field: "store"},
	}
	if got := KeyColumns(gs); !reflect.DeepEqual(got, []string{"GROUP_KMEANS_x", "store"}) {
		t.Fatalf("KeyColumns = %v", got)
	}
	if got, err := Depth(gs, spec(`{"partition_by": ["GROUP_KMEANS_x"]}`), 0); err != nil || got != 1 {
		t.Errorf("Depth = (%d, %v), want 1", got, err)
	}
}

// Anything but a leading prefix is refused PULSE_OVERLAY_PARTITION_INVALID
// with {index, kind, partition_by, groups, reason[, field]}.
func TestDepth_Refused(t *testing.T) {
	cols := []string{"region", "store", "day"}
	for _, c := range []struct {
		params string
		given  any
		reason string
		field  string
	}{
		{`{"partition_by": ["store"]}`, []string{"store"}, "not_prefix", "store"},
		{`{"partition_by": ["region", "day"]}`, []string{"region", "day"}, "not_prefix", "day"},
		{`{"partition_by": ["region", "region"]}`, []string{"region", "region"}, "not_prefix", "region"},
		{`{"partition_by": ["warehouse"]}`, []string{"warehouse"}, "unknown_field", "warehouse"},
		{`{"partition_by": ["AGG_SUM_spend"]}`, []string{"AGG_SUM_spend"}, "unknown_field", "AGG_SUM_spend"},
		{`{"partition_by": "region"}`, "region", "not_string_list", ""},
		{`{"partition_by": [1]}`, []any{1.0}, "not_string_list", ""},
	} {
		_, err := Depth(groups(3), spec(c.params), 5)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_PARTITION_INVALID {
			t.Fatalf("%s: err = %v, want PULSE_OVERLAY_PARTITION_INVALID", c.params, err)
		}
		want := map[string]any{
			"index":        5,
			"kind":         string(types.OverlayKindZScoreVsRolling),
			"partition_by": c.given,
			"groups":       cols,
			"reason":       c.reason,
		}
		if c.field != "" {
			want["field"] = c.field
		}
		if !reflect.DeepEqual(ce.Details, want) {
			t.Errorf("%s: details %#v, want %#v", c.params, ce.Details, want)
		}
	}
}

// Partition groups by key equality on the prefix — not contiguity — in
// first-appearance order with ordinals ascending; string "1" and number
// 1, and nil and "", are distinct keys.
func TestPartition(t *testing.T) {
	keys := []types.AxisKey{
		{"east", "a", 1}, {"west", "a", 1}, {"east", "b", 2}, {"west", "a", 2},
		{nil, "a", 1}, {"", "a", 1}, {"1", "a", 1}, {1, "a", 1}, {"east", "a", 3},
	}
	for depth, want := range map[int][][]int{
		0: {{0, 1, 2, 3, 4, 5, 6, 7, 8}},
		1: {{0, 2, 8}, {1, 3}, {4}, {5}, {6}, {7}},
		2: {{0, 8}, {1, 3}, {2}, {4}, {5}, {6}, {7}},
		3: {{0}, {1}, {2}, {3}, {4}, {5}, {6}, {7}, {8}},
	} {
		if got := Partition(keys, depth); !reflect.DeepEqual(got, want) {
			t.Errorf("depth %d: %v, want %v", depth, got, want)
		}
	}
	if got := Partition(nil, 1); got != nil {
		t.Errorf("empty host: %v", got)
	}
	// A key shorter than depth compares its missing tail as nil.
	if got := Partition([]types.AxisKey{{"a"}, {"a", nil}, {"a", "x"}}, 2); !reflect.DeepEqual(got, [][]int{{0, 1}, {2}}) {
		t.Errorf("short keys: %v", got)
	}
}

func TestRowKeys(t *testing.T) {
	rows := []map[string]any{{"region": "east", "day": "d1", "n": 3}, {"day": "d2"}}
	want := []types.AxisKey{{"east", "d1"}, {nil, "d2"}}
	if got := RowKeys(rows, []string{"region", "day"}); !reflect.DeepEqual(got, want) {
		t.Errorf("RowKeys = %v, want %v", got, want)
	}
}

func TestKinds(t *testing.T) {
	ks := Kinds()
	if len(ks) != 11 {
		t.Fatalf("Kinds() = %v, want the 8 ordered kinds and the 3 _TOTAL kinds", ks)
	}
	ordered := 0
	for _, k := range ks {
		if !Applies(k) {
			t.Errorf("%s listed but not applied", k)
		}
		if Ordered(k) {
			ordered++
		}
	}
	if ordered != 8 {
		t.Errorf("%d ordered kinds, want 8", ordered)
	}
	for _, k := range []types.OverlayKind{types.OverlayKindIndexVsTotal, types.OverlayKindShareOfTotal, types.OverlayKindZScoreVsTotal} {
		if !Applies(k) || Ordered(k) {
			t.Errorf("%s: Applies=%v Ordered=%v, want a partitioned, order-free kind", k, Applies(k), Ordered(k))
		}
	}
	if Applies(types.OverlayKindYoY) || Ordered(types.OverlayKindYoY) {
		t.Error("YoY reads its own rule (YoY), not the prefix rule")
	}
}

func TestExplicit(t *testing.T) {
	for params, want := range map[string]bool{
		"":                             false,
		`{"window": 2}`:                false,
		`{"partition_by": null}`:       false,
		`[1]`:                          false,
		`{"partition_by": []}`:         true,
		`{"partition_by": ["region"]}`: true,
		`{"partition_by": "region"}`:   true,
	} {
		if got := Explicit(spec(params)); got != want {
			t.Errorf("Explicit(%s) = %v, want %v", params, got, want)
		}
	}
	if Explicit(nil) {
		t.Error("Explicit(nil)")
	}
}

func isDate(g *types.Group) bool { return g.Type == types.GROUP_DATE }

func yoySpec(params string) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: types.OverlayKindYoY}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// PartitionOn splits by key equality on the named elements wherever they
// sit; Partition(depth) is PartitionOn over the leading depth indices.
func TestPartitionOn(t *testing.T) {
	keys := []types.AxisKey{{"m1", "east"}, {"m1", "west"}, {"m2", "east"}, {"m2", "west"}, {"m3", "east"}}
	for _, c := range []struct {
		on   []int
		want [][]int
	}{
		{nil, [][]int{{0, 1, 2, 3, 4}}},
		{[]int{1}, [][]int{{0, 2, 4}, {1, 3}}},
		{[]int{0}, [][]int{{0, 1}, {2, 3}, {4}}},
		{[]int{1, 0}, [][]int{{0}, {1}, {2}, {3}, {4}}},
	} {
		if got := PartitionOn(keys, c.on); !reflect.DeepEqual(got, c.want) {
			t.Errorf("on %v: %v, want %v", c.on, got, c.want)
		}
	}
	if got, want := PartitionOn(keys, []int{0}), Partition(keys, 1); !reflect.DeepEqual(got, want) {
		t.Errorf("PartitionOn([0]) %v != Partition(1) %v", got, want)
	}
}

// YoY finds the one date grouper wherever it sits and partitions on
// every other key, in group order; an explicit partition_by naming
// exactly those keys (any order) is the same.
func TestYoY(t *testing.T) {
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	store := &types.Group{Type: types.GROUP_CATEGORY, Field: "store"}
	day := &types.Group{Type: types.GROUP_DATE, Field: "day"}
	for _, c := range []struct {
		groups []*types.Group
		params string
		date   int
		on     []int
	}{
		{[]*types.Group{day}, "", 0, []int{}},
		{[]*types.Group{day}, `{"partition_by": []}`, 0, []int{}},
		{[]*types.Group{region, day}, "", 1, []int{0}},
		{[]*types.Group{day, region}, "", 0, []int{1}},
		{[]*types.Group{region, day, store}, "", 1, []int{0, 2}},
		{[]*types.Group{region, day, store}, `{"partition_by": ["store", "region"]}`, 1, []int{0, 2}},
		{[]*types.Group{region, day, store}, `{"partition_by": null, "frequency": "annual"}`, 1, []int{0, 2}},
	} {
		date, on, err := YoY(c.groups, isDate, yoySpec(c.params), 0)
		if err != nil || date != c.date || !reflect.DeepEqual(on, c.on) {
			t.Errorf("%v %s: YoY = (%d, %v, %v), want (%d, %v)", KeyColumns(c.groups), c.params, date, on, err, c.date, c.on)
		}
	}
	if got := DateGrouper([]*types.Group{region, store, day}, isDate); got != 2 {
		t.Errorf("DateGrouper = %d, want 2", got)
	}
	if got := DateGrouper([]*types.Group{region, nil}, isDate); got != -1 {
		t.Errorf("DateGrouper with no date grouper = %d, want -1", got)
	}
}

// YoY refuses the date-grouper count and a partition_by that is not
// exactly the non-date keys, with the Details predict and the runtime
// both echo.
func TestYoY_Refused(t *testing.T) {
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	store := &types.Group{Type: types.GROUP_CATEGORY, Field: "store"}
	day := &types.Group{Type: types.GROUP_DATE, Field: "day"}
	month := &types.Group{Type: types.GROUP_DATE, Field: "month"}
	shape := func(extra map[string]any) map[string]any {
		out := map[string]any{"index": 3, "kind": "OVERLAY_YOY", "required": "GROUP_DATE"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	part := func(given any, cols []string, reason, field string) map[string]any {
		out := map[string]any{"index": 3, "kind": "OVERLAY_YOY", "partition_by": given, "groups": cols, "reason": reason}
		if field != "" {
			out["field"] = field
		}
		return out
	}
	for _, c := range []struct {
		groups []*types.Group
		params string
		code   errors.Code
		want   map[string]any
	}{
		{[]*types.Group{region}, "", errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, shape(map[string]any{"host_grouper": "GROUP_CATEGORY"})},
		{[]*types.Group{region, store}, "", errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, shape(map[string]any{"groupers": 2, "date_groupers": 0})},
		{[]*types.Group{day, region, month}, "", errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, shape(map[string]any{"groupers": 3, "date_groupers": 2})},
		{[]*types.Group{region, day, store}, `{"partition_by": ["region"]}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			part([]string{"region"}, []string{"region", "day", "store"}, "not_non_date_keys", "store")},
		{[]*types.Group{region, day}, `{"partition_by": ["region", "day"]}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			part([]string{"region", "day"}, []string{"region", "day"}, "not_non_date_keys", "day")},
		{[]*types.Group{region, day}, `{"partition_by": ["region", "region"]}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			part([]string{"region", "region"}, []string{"region", "day"}, "not_non_date_keys", "region")},
		{[]*types.Group{region, day}, `{"partition_by": []}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			part([]string{}, []string{"region", "day"}, "not_non_date_keys", "region")},
		{[]*types.Group{day}, `{"partition_by": ["day"]}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			part([]string{"day"}, []string{"day"}, "not_non_date_keys", "day")},
		{[]*types.Group{region, day}, `{"partition_by": ["warehouse"]}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			part([]string{"warehouse"}, []string{"region", "day"}, "unknown_field", "warehouse")},
		{[]*types.Group{region, day}, `{"partition_by": "region"}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			part("region", []string{"region", "day"}, "not_string_list", "")},
	} {
		_, _, err := YoY(c.groups, isDate, yoySpec(c.params), 3)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != c.code {
			t.Fatalf("%v %s: err = %v, want %s", KeyColumns(c.groups), c.params, err, c.code)
		}
		if !reflect.DeepEqual(ce.Details, c.want) {
			t.Errorf("%v %s: details %#v, want %#v", KeyColumns(c.groups), c.params, ce.Details, c.want)
		}
	}
}
