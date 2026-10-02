package pulse_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// nullOrderCohort imports a cohort with one nullable column per
// orderable type family. Rows (id: value per column), null at id 3 and
// id 5 in every column but id:
//
//	id  n    d           ts                    flag   cat
//	1   2.5  2024-01-02  2024-01-02T00:00:00Z  true   b
//	2   -1   1950-06-01  1969-12-31T23:59:59Z  false  a
//	3   ""   ""          ""                    ""     ""
//	4   7    1970-01-01  1970-01-01T00:00:00Z  true   c
//	5   ""   ""          ""                    ""     ""
//
// decimal128 rides the hand-built "dec.pulse" (orderabilityCohort):
// id 1..5, amt 10.50, -3.25, 2.50, 100.00, null.
func nullOrderCohort(t *testing.T) (*pulse.Pulse, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	csv := "id,n,d,ts,flag,cat\n" +
		"1,2.5,2024-01-02,2024-01-02T00:00:00Z,true,b\n" +
		"2,-1,1950-06-01,1969-12-31T23:59:59Z,false,a\n" +
		"3,,,,,\n" +
		"4,7,1970-01-01,1970-01-01T00:00:00Z,true,c\n" +
		"5,,,,,\n"
	orderabilityCohort(t, fs, "dec.pulse")
	if err := afero.WriteFile(fs, "nulls.csv", []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "nulls.csv",
		ColumnTypeOverrides: map[string]string{"n": "f64",
			"d": "date", "ts": "datetime", "flag": "packed_bool", "cat": "categorical_u8"}})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := p.Inspect(ctx, res.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "id:u4 n:f64 d:date ts:datetime flag:packed_bool cat:categorical_u8"
	got := ""
	for i, f := range ins.Fields {
		if i > 0 {
			got += " "
		}
		got += f.Name + ":" + f.Type
	}
	if got != want {
		t.Fatalf("imported schema = %s, want %s — the test would prove nothing", got, want)
	}
	return p, res.Path
}

// nullOrderAscIDs / nullOrderDescIDs are the id order every arm must
// produce for each column: non-null values by value (signed epochs,
// false before true, categorical by label), then the two nulls in input
// order — LAST in BOTH directions.
var (
	nullOrderAscIDs = map[string]string{
		"n": "[2 1 4 3 5]", "amt": "[2 3 1 4 5]", "d": "[2 4 1 3 5]",
		"ts": "[2 4 1 3 5]", "flag": "[2 1 4 3 5]", "cat": "[2 1 4 3 5]",
	}
	nullOrderDescIDs = map[string]string{
		"n": "[4 1 2 3 5]", "amt": "[4 1 3 2 5]", "d": "[1 4 2 3 5]",
		"ts": "[1 4 2 3 5]", "flag": "[1 4 2 3 5]", "cat": "[4 1 2 3 5]",
	}
)

// TestNullOrdering_LastInBothDirections: a null sorts LAST under Desc
// as well as Asc, on every ordering arm — window order_by (the row
// numbers a WIN_ROW_NUMBER assigns) and Request.Sort over record rows —
// for every orderable type family. Before, Desc reversed the whole
// comparison, null term included, so every Desc key put its nulls FIRST
// while the contract says nulls last regardless of direction.
func TestNullOrdering_LastInBothDirections(t *testing.T) {
	p, cohort := nullOrderCohort(t)
	ctx := context.Background()
	for _, field := range []string{"n", "amt", "d", "ts", "flag", "cat"} {
		for _, desc := range []bool{false, true} {
			want := nullOrderAscIDs[field]
			if desc {
				want = nullOrderDescIDs[field]
			}
			key := []types.OrderKey{{Field: field, Desc: desc}}
			cohort := cohort
			if field == "amt" {
				cohort = "dec.pulse"
			}
			t.Run(fmt.Sprintf("%s/desc=%v", field, desc), func(t *testing.T) {
				// Arm 1: window order_by. Output sorted by rn so the
				// row-number assignment reads as an id order.
				resp, err := p.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: cohort},
					Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: key}},
					Sort:    []types.OrderKey{{Field: "rn"}, {Field: "id"}}})
				if err != nil {
					t.Fatalf("window arm: %v", err)
				}
				if got := idOrder(resp.Data); got != want {
					t.Errorf("window order_by ids = %s, want %s", got, want)
				}
				// Arm 2: Request.Sort over the record rows.
				resp, err = p.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: cohort},
					Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "id"}}}},
					Sort:    key})
				if err != nil {
					t.Fatalf("sort arm: %v", err)
				}
				if got := idOrder(resp.Data); got != want {
					t.Errorf("Request.Sort ids = %s, want %s", got, want)
				}
			})
		}
	}
}

// TestNullOrdering_GroupedSortByDecimalAggregate: Request.Sort over a
// grouped response orders a decimal aggregate column by VALUE in both
// directions. Before, the comparator saw the decimal aggregate result
// as an opaque struct, every row compared equal, and the sort silently
// kept the grouper's key order.
func TestNullOrdering_GroupedSortByDecimalAggregate(t *testing.T) {
	p, _ := nullOrderCohort(t)
	ctx := context.Background()
	// SUM(amt) per id bucket: 1→10.50 2→-3.25 3→2.50 4→100.00; id 5's
	// only amt is null.
	for desc, want := range map[bool]string{false: "[2 3 1 4]", true: "[4 1 3 2]"} {
		resp, err := p.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: "dec.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "id"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amt", Label: "s"}},
			Sort:         []types.OrderKey{{Field: "s", Desc: desc}}})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range resp.Data {
			if id := fmt.Sprint(r["id"]); id != "5" {
				ids = append(ids, id)
			}
		}
		if got := fmt.Sprint(ids); got != want {
			t.Errorf("desc=%v: id order by SUM(amt) = %v (rows %v), want %s", desc, got, resp.Data, want)
		}
	}
}

func idOrder(rows []map[string]any) string {
	ids := make([]float64, 0, len(rows))
	for _, r := range rows {
		id, _ := toF64(r["id"])
		ids = append(ids, id)
	}
	return fmt.Sprint(ids)
}
