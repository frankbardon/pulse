package descriptor

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

func catAxis(fields ...string) []*types.Group {
	out := make([]*types.Group, len(fields))
	for i, f := range fields {
		out[i] = &types.Group{Type: types.GROUP_CATEGORY, Field: f}
	}
	return out
}

// TestEstimateCrosstabCells: the MaxCrosstabCells estimator multiplies
// each axis's per-position bounds, clamps each axis by the record count
// (not a fan-out axis), and yields nothing when any position's
// cardinality is unknown.
func TestEstimateCrosstabCells(t *testing.T) {
	schema := groupsSchema(t)
	for _, c := range []struct {
		name    string
		spec    *types.CrosstabSpec
		records int64
		want    int64
		known   bool
	}{
		{"dictionary product", &types.CrosstabSpec{Rows: catAxis("cat"), Columns: catAxis("flag")}, -1, 10, true},
		{"nested axis product", &types.CrosstabSpec{Rows: catAxis("cat", "flag"), Columns: catAxis("cat")}, -1, 50, true},
		{"each axis clamped by records", &types.CrosstabSpec{Rows: catAxis("cat", "flag"), Columns: catAxis("cat")}, 4, 16, true},
		{"fan-out axis not clamped", &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Columns: catAxis("cat"),
		}, 1, 3, true},
		{"unknown row axis", &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "num", Interval: 10}},
			Columns: catAxis("cat"),
		}, -1, 0, false},
		{"unknown column position", &types.CrosstabSpec{
			Rows:    catAxis("cat"),
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "flag"}, {Type: types.GROUP_DATE, Field: "day"}},
		}, -1, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, known := EstimateCrosstabCells(c.spec, schema, nil, c.records)
			if got != c.want || known != c.known {
				t.Fatalf("EstimateCrosstabCells = %d, %v; want %d, %v", got, known, c.want, c.known)
			}
		})
	}
	if _, known := EstimateCrosstabCells(nil, schema, nil, -1); known {
		t.Fatal("a nil crosstab has a cell estimate")
	}
}

func TestMulSaturating(t *testing.T) {
	for _, c := range [][3]int64{
		{0, math.MaxInt64, 0},
		{3, 4, 12},
		{math.MaxInt64 / 2, 3, math.MaxInt64},
		{math.MaxInt64, math.MaxInt64, math.MaxInt64},
	} {
		if got := mulSaturating(c[0], c[1]); got != c[2] {
			t.Errorf("mulSaturating(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

// TestRequestLimitFindings_MaxCrosstabCellsPossible: a dictionary
// product above MaxCrosstabCells is a Possible finding — never a
// pre-flight refusal — and an unknown axis yields none.
func TestRequestLimitFindings_MaxCrosstabCellsPossible(t *testing.T) {
	schema := groupsSchema(t)
	l := limits.Defaults()
	l.MaxCrosstabCells = 9
	req := &types.Request{Crosstab: &types.CrosstabSpec{Rows: catAxis("cat"), Columns: catAxis("flag")}}
	want := limits.Finding{Limit: limits.MaxCrosstabCells, Configured: 9, Estimated: 10, Grade: limits.Possible}
	if fs := RequestLimitFindings(req, schema, nil, l, -1); len(fs) != 1 || fs[0] != want {
		t.Fatalf("findings = %+v, want [%+v]", fs, want)
	}
	if err := LimitRefusal(req, schema, nil, l); err != nil {
		t.Fatalf("a possible finding refused the pre-flight: %v", err)
	}
	l.MaxCrosstabCells = 10
	if fs := RequestLimitFindings(req, schema, nil, l, -1); len(fs) != 0 {
		t.Fatalf("at the limit: findings = %+v", fs)
	}
	l.MaxCrosstabCells = 1
	unknown := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    catAxis("cat"),
		Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "num", Interval: 10}},
	}}
	if fs := RequestLimitFindings(unknown, schema, nil, l, -1); len(fs) != 0 {
		t.Fatalf("unknown axis: findings = %+v", fs)
	}
}
