package service

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// crosstabJoinFixture writes a left cohort (id, region, segment, value)
// and a right cohort (id, tier) to a MemMap fs.
//
// Left rows (id region segment value):
//
//	1  north retail      10
//	2  north wholesale   20
//	3  south retail       5
//	4  south retail      15
//	5  east  wholesale    1
//	98 south wholesale  500   (no right match)
//	99 north retail    1000   (no right match)
//
// Right rows (id tier): 1 gold, 2 silver, 3 gold, 4 gold, 5 silver,
// 5 gold — id 5 matches twice, so the joined stream has 6 rows and the
// two unmatched left rows are absent. A crosstab that silently ignored
// the join would see 7 rows, the 1000 / 500 values, and no r_tier.
func crosstabJoinFixture(t *testing.T) *fs.Config {
	t.Helper()
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	regionDict.Add("east")
	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")
	left := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: regionDict},
		{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 5, CsvColumnIdx: 2, Dictionary: segmentDict},
		{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 6, CsvColumnIdx: 3},
	}}
	tierDict := encoding.NewDictionary()
	tierDict.Add("gold")
	tierDict.Add("silver")
	right := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "tier", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: tierDict},
	}}
	f := math.Float64bits
	leftRecs := [][]uint64{
		{1, 0, 0, f(10)},
		{2, 0, 1, f(20)},
		{3, 1, 0, f(5)},
		{4, 1, 0, f(15)},
		{5, 2, 1, f(1)},
		{98, 1, 1, f(500)},
		{99, 0, 0, f(1000)},
	}
	rightRecs := [][]uint64{
		{1, 0}, {2, 1}, {3, 0}, {4, 0}, {5, 1}, {5, 0},
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "left.pulse", writePulseFile(t, left, leftRecs), 0o644); err != nil {
		t.Fatalf("WriteFile left: %v", err)
	}
	if err := afero.WriteFile(cfg.Fs(), "right.pulse", writePulseFile(t, right, rightRecs), 0o644); err != nil {
		t.Fatalf("WriteFile right: %v", err)
	}
	return cfg
}

func crosstabJoinSpec() []*types.JoinSpec {
	return []*types.JoinSpec{{
		Right: "right.pulse",
		Kind:  "inner",
		As:    "r_",
		On:    []types.OnPair{{LeftField: "id", RightField: "id"}},
	}}
}

// joinedGroupedExpectation runs the equivalent NON-crosstab join
// requests (the long-established processWithJoin path) — one
// FILTER_INCLUDE on each row value, grouped by colField — and returns
// sum / count per (row, col) bucket: the independent expectation every
// crosstab arm must equal.
func joinedGroupedExpectation(t *testing.T, svc *Service, rowField, colField string) (sums map[[2]string]float64, counts map[[2]string]int) {
	t.Helper()
	sums = map[[2]string]float64{}
	counts = map[[2]string]int{}
	for _, rv := range []string{"north", "south", "east"} {
		req := &types.Request{
			Cohort:    &types.Cohort{Filename: "left.pulse"},
			Joins:     crosstabJoinSpec(),
			Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: rowField, Values: []string{rv}}},
			Groups:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: colField}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "value", Label: "s"},
				{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			},
		}
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("grouped join Process: %v", err)
		}
		for _, row := range resp.Data {
			k := [2]string{rv, fmt.Sprint(row[colField])}
			sums[k] = row["s"].(float64)
			counts[k] = int(row["n"].(float64))
		}
	}
	if len(sums) == 0 {
		t.Fatalf("grouped join expectation is empty")
	}
	return sums, counts
}

// TestCrosstabJoin_HonoursJoinOnBothArms: a crosstab carrying one
// JoinSpec runs over the JOINED row stream — an axis may name a
// right-side (renamed) field, unmatched left rows are dropped, a 1:N
// right match fans out — on the fused-enabled and fused-disabled
// service alike. Cells, margins and Components equal the independent
// grouped-join expectation and the hand-computed figures.
func TestCrosstabJoin_HonoursJoinOnBothArms(t *testing.T) {
	for _, tc := range []struct {
		name          string
		disableFusion bool
	}{
		{"fusion_enabled", false},
		{"fusion_disabled", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(crosstabJoinFixture(t))
			svc.SetDisableCrosstabFusion(tc.disableFusion)
			wantSum, wantN := joinedGroupedExpectation(t, svc, "region", "r_tier")

			// Hand-computed guard on the expectation itself, so a broken
			// grouped-join path cannot make both sides agree on nonsense.
			hand := map[[2]string]float64{
				{"north", "gold"}: 10, {"north", "silver"}: 20,
				{"south", "gold"}: 20,
				{"east", "gold"}:  1, {"east", "silver"}: 1,
			}
			if len(wantSum) != len(hand) {
				t.Fatalf("grouped expectation buckets = %v, want %v", wantSum, hand)
			}
			for k, v := range hand {
				if wantSum[k] != v {
					t.Fatalf("grouped expectation %v = %v, want %v", k, wantSum[k], v)
				}
			}

			req := &types.Request{
				Cohort: &types.Cohort{Filename: "left.pulse"},
				Joins:  crosstabJoinSpec(),
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r_tier"}},
					Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
					Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					Shape:   types.CrosstabShapeMatrix,
				},
			}
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("crosstab+join Process: %v", err)
			}
			m := resp.Crosstab.Matrix
			ct := resp.Components.Crosstab
			if m == nil || ct == nil {
				t.Fatalf("matrix or components nil")
			}
			seen := 0
			for i, rk := range m.RowKeys {
				for j, ck := range m.ColumnKeys {
					k := [2]string{fmt.Sprint(rk[0]), fmt.Sprint(ck[0])}
					cell := m.Cells[i][j]
					want, ok := wantSum[k]
					if !ok {
						if cell.Present || ct.CellCounts[i][j] != 0 {
							t.Errorf("cell %v present=%v n=%d, want absent", k, cell.Present, ct.CellCounts[i][j])
						}
						continue
					}
					seen++
					if !cell.Present || cell.Scalar() != want {
						t.Errorf("cell %v = %v (present=%v), want %v", k, cell.Value, cell.Present, want)
					}
					if ct.CellCounts[i][j] != wantN[k] {
						t.Errorf("CellCounts %v = %d, want %d", k, ct.CellCounts[i][j], wantN[k])
					}
				}
			}
			if seen != len(wantSum) {
				t.Errorf("matched %d expected cells, want %d", seen, len(wantSum))
			}

			rowSum := map[string]float64{"north": 30, "south": 20, "east": 2}
			rowN := map[string]int{"north": 2, "south": 2, "east": 2}
			for i, rk := range m.RowKeys {
				r := fmt.Sprint(rk[0])
				if got := m.RowMargins[i].Scalar(); got != rowSum[r] {
					t.Errorf("row margin %s = %v, want %v", r, got, rowSum[r])
				}
				if ct.RowMarginCounts[i] != rowN[r] {
					t.Errorf("row margin count %s = %d, want %d", r, ct.RowMarginCounts[i], rowN[r])
				}
			}
			colSum := map[string]float64{"gold": 31, "silver": 21}
			colN := map[string]int{"gold": 4, "silver": 2}
			for j, ck := range m.ColumnKeys {
				c := fmt.Sprint(ck[0])
				if got := m.ColumnMargins[j].Scalar(); got != colSum[c] {
					t.Errorf("column margin %s = %v, want %v", c, got, colSum[c])
				}
				if ct.ColumnMarginCounts[j] != colN[c] {
					t.Errorf("column margin count %s = %d, want %d", c, ct.ColumnMarginCounts[j], colN[c])
				}
			}
			if got := m.GrandTotal.Scalar(); got != 52 {
				t.Errorf("grand total = %v, want 52", got)
			}
			if ct.GrandTotalCount != 6 || ct.IncludedRecords != 6 {
				t.Errorf("grand count = %d included = %d, want 6 / 6 (joined rows)", ct.GrandTotalCount, ct.IncludedRecords)
			}
		})
	}
}

// TestCrosstabJoin_LeftOnlyAxesStillJoin: a crosstab whose axes and
// cell name ONLY left fields must still apply the inner join — the two
// unmatched left rows (values 500 / 1000) are excluded and the 1:N
// match counts twice. This is the silent-drop shape: before the fix
// the fused-disabled arm ran over the bare left cohort.
func TestCrosstabJoin_LeftOnlyAxesStillJoin(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable_fusion_%v", disable), func(t *testing.T) {
			svc := New(crosstabJoinFixture(t))
			svc.SetDisableCrosstabFusion(disable)
			wantSum, wantN := joinedGroupedExpectation(t, svc, "region", "segment")
			req := &types.Request{
				Cohort: &types.Cohort{Filename: "left.pulse"},
				Joins:  crosstabJoinSpec(),
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
					Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
					Margins: types.CrosstabMargins{Grand: true},
					Shape:   types.CrosstabShapeMatrix,
				},
			}
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			m := resp.Crosstab.Matrix
			for i, rk := range m.RowKeys {
				for j, ck := range m.ColumnKeys {
					k := [2]string{fmt.Sprint(rk[0]), fmt.Sprint(ck[0])}
					if want, ok := wantSum[k]; ok {
						if got := m.Cells[i][j].Scalar(); got != want {
							t.Errorf("cell %v = %v, want %v", k, got, want)
						}
						if got := resp.Components.Crosstab.CellCounts[i][j]; got != wantN[k] {
							t.Errorf("CellCounts %v = %d, want %d", k, got, wantN[k])
						}
					} else if m.Cells[i][j].Present {
						t.Errorf("cell %v present, want absent", k)
					}
				}
			}
			// 10+20+5+15+1+1: the unmatched 500 / 1000 never arrive.
			if got := m.GrandTotal.Scalar(); got != 52 {
				t.Errorf("grand total = %v, want 52 (join dropped?)", got)
			}
		})
	}
}
