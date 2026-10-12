package groupedtests

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestSpecIndex(t *testing.T) {
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
