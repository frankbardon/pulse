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
	if len(ks) != 8 {
		t.Fatalf("Kinds() = %v, want the 8 prior / rolling / baseline / sibling kinds", ks)
	}
	for _, k := range ks {
		if !Applies(k) {
			t.Errorf("%s listed but not applied", k)
		}
	}
	if Applies(types.OverlayKindYoY) || Applies(types.OverlayKindShareOfTotal) {
		t.Error("YoY or a _TOTAL kind partitions before its story lands")
	}
}
