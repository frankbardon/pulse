package groupedtests

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestSpecIndex(t *testing.T) {
	t.Parallel()

	cases := []struct{ k, specs, results, want int }{
		{0, 2, 2, 0}, {1, 2, 2, 1}, // ungrouped: one per slot
		{0, 2, 6, 0}, {2, 2, 6, 0}, {3, 2, 6, 1}, {5, 2, 6, 1}, // 3 groups, test-major
		{6, 2, 6, -1}, {-1, 2, 6, -1}, // out of range
		{0, 2, 5, -1}, // not a multiple
		{0, 0, 0, -1},
	}
	for _, c := range cases {
		if got := SpecIndex(c.k, c.specs, c.results); got != c.want {
			t.Errorf("SpecIndex(%d, %d, %d) = %d, want %d", c.k, c.specs, c.results, got, c.want)
		}
	}
}

func TestSplitRefusal(t *testing.T) {
	t.Parallel()

	g := func(typ types.GroupType, field string) *types.Group { return &types.Group{Type: typ, Field: field} }
	cases := []struct {
		name   string
		tests  []*types.Test
		groups []*types.Group
		code   errors.Code
		slot   string
	}{
		{"no groups", []*types.Test{{Type: types.TEST_T, SplitBy: "sex"}}, nil, "", ""},
		{"other field", []*types.Test{{Type: types.TEST_T, SplitBy: "sex"}}, []*types.Group{g(types.GROUP_CATEGORY, "region")}, "", ""},
		{"split_by", []*types.Test{nil, {Type: types.TEST_T, SplitBy: "sex"}}, []*types.Group{g(types.GROUP_CATEGORY, "region"), g(types.GROUP_CATEGORY, "sex")}, errors.PULSE_TEST_SPLIT_GROUPS_LT_2, "split_by"},
		{"rows", []*types.Test{{Type: types.TEST_CHISQ, Rows: "sex", Cols: "tier"}}, []*types.Group{g(types.GROUP_CATEGORY, "sex")}, errors.PULSE_TEST_CONTINGENCY_DEGENERATE, "rows"},
		{"cols", []*types.Test{{Type: types.TEST_CHISQ, Rows: "sex", Cols: "tier"}}, []*types.Group{g(types.GROUP_CATEGORY, "tier")}, errors.PULSE_TEST_CONTINGENCY_DEGENERATE, "cols"},
		{"vector grouper never matches", []*types.Test{{Type: types.TEST_T, SplitBy: "y"}}, []*types.Group{{Type: types.GROUP_KMEANS, Field: "y", Fields: []string{"y"}}}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := SplitRefusal(c.tests, c.groups)
			if c.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Code != c.code || err.Details["slot"] != c.slot || err.Details["reason"] != ReasonSplitIsGrouper {
				t.Fatalf("got %v (%v), want %s on %s", err, err, c.code, c.slot)
			}
		})
	}
}

// PostTestDepth: the prefix rule per post-test, one depth per request,
// and a split on a partition key column refused.
func TestPostTestDepth(t *testing.T) {
	t.Parallel()

	g := func(field string) *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: field} }
	two := []*types.Group{g("warehouse"), g("week")}
	pt := func(params, split string) *types.Test {
		return &types.Test{Type: types.TEST_TREND, Field: "n", SplitBy: split, Params: json.RawMessage(params)}
	}
	cases := []struct {
		name   string
		tests  []*types.Test
		groups []*types.Group
		depth  int
		code   errors.Code
		reason string
	}{
		{"ungrouped", []*types.Test{pt("", "")}, nil, 0, "", ""},
		{"one grouper", []*types.Test{pt("", "")}, two[:1], 0, "", ""},
		{"implicit", []*types.Test{pt("", ""), pt(`{"variant":"mann_kendall"}`, "")}, two, 1, "", ""},
		{"explicit none", []*types.Test{pt(`{"partition_by":[]}`, "")}, two, 0, "", ""},
		{"nil entry skipped", []*types.Test{nil, pt("", "")}, two, 1, "", ""},
		{"not prefix", []*types.Test{pt(`{"partition_by":["week"]}`, "")}, two, 0, errors.PULSE_TEST_PARTITION_INVALID, "not_prefix"},
		{"ungrouped names a column", []*types.Test{pt(`{"partition_by":["week"]}`, "")}, nil, 0, errors.PULSE_TEST_PARTITION_INVALID, "unknown_field"},
		{"mixed", []*types.Test{pt("", ""), pt(`{"partition_by":[]}`, "")}, two, 0, errors.PULSE_TEST_PARTITION_INVALID, ReasonMixedDepth},
		{"split on partition key", []*types.Test{pt("", "warehouse")}, two, 0, errors.PULSE_TEST_SPLIT_GROUPS_LT_2, ReasonSplitIsPartitionKey},
		{"split on series key", []*types.Test{pt("", "week")}, two, 1, "", ""},
		{"split with no partition", []*types.Test{pt(`{"partition_by":[]}`, "warehouse")}, two, 0, "", ""},
	}
	for _, c := range cases {
		depth, err := PostTestDepth(c.tests, c.groups)
		switch {
		case c.code == "" && (err != nil || depth != c.depth):
			t.Errorf("%s: (%d, %v), want depth %d", c.name, depth, err, c.depth)
		case c.code != "" && (err == nil || err.Code != c.code || err.Details["reason"] != c.reason):
			t.Errorf("%s: %v, want %s %s", c.name, err, c.code, c.reason)
		}
	}
}

// WithoutPartition strips only partition_by, never mutating the spec.
func TestWithoutPartition(t *testing.T) {
	t.Parallel()

	in := &types.Test{Type: types.TEST_TREND, Params: json.RawMessage(`{"partition_by":[],"variant":"mann_kendall"}`)}
	out := WithoutPartition(in)
	if string(out.Params) != `{"variant":"mann_kendall"}` || string(in.Params) != `{"partition_by":[],"variant":"mann_kendall"}` {
		t.Fatalf("stripped %s, original %s", out.Params, in.Params)
	}
	if got := WithoutPartition(&types.Test{Params: json.RawMessage(`{"partition_by":["a"]}`)}); got.Params != nil {
		t.Fatalf("lone partition_by left %s", got.Params)
	}
	keep := &types.Test{Params: json.RawMessage(`{"variant":"x"}`)}
	if WithoutPartition(keep) != keep {
		t.Fatal("a spec without partition_by was copied")
	}
}
