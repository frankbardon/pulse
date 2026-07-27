package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func fiscalStrptr(s string) *string { return &s }

// fiscalCohort seeds a hermetic cohort with an inferred `txn_date` date
// column spanning the fiscal launch/growth/steady ranges plus one
// out-of-range row, and returns a Pulse whose Extensions register the
// matching "fiscal" range table.
func fiscalCohort(t *testing.T) *Pulse {
	t.Helper()
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "txns.pulse",
		[]string{"id", "txn_date"},
		[][]string{
			{"1", "2024-02-10"}, // launch
			{"2", "2024-02-20"}, // launch
			{"3", "2024-05-05"}, // growth
			{"4", "2024-11-01"}, // steady
			{"5", "2023-06-01"}, // unmatched / out of range
		},
	)
	p, err := New(Options{
		FS: memFs,
		Extensions: Extensions{
			RangeTables: map[string]RangeTable{
				"fiscal": {Ranges: []DateRangeSpec{
					{Label: "launch", Start: fiscalStrptr("2024-01-01"), End: fiscalStrptr("2024-03-31")},
					{Label: "growth", Start: fiscalStrptr("2024-04-01"), End: fiscalStrptr("2024-09-30")},
					{Label: "steady", Start: fiscalStrptr("2024-10-01")},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// TestFacade_GroupDateRanges_TableSource is the E2E acceptance for the
// grouper: process with {grouper: GROUP_DATE_RANGES, table:"fiscal"} yields
// the customer's range labels as bucket keys.
func TestFacade_GroupDateRanges_TableSource(t *testing.T) {
	p := fiscalCohort(t)
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "txns.pulse"},
		Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "txn_date", Params: json.RawMessage(`{"table":"fiscal"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := map[string]float64{}
	for _, row := range resp.Data {
		got[row["txn_date"].(string)] = row["n"].(float64)
	}
	want := map[string]float64{"launch": 2, "growth": 1, "steady": 1, "unmatched": 1}
	if len(got) != len(want) {
		t.Fatalf("buckets = %v, want %v", got, want)
	}
	for label, n := range want {
		if got[label] != n {
			t.Errorf("bucket %q = %v, want %v", label, got[label], n)
		}
	}
}

// TestFacade_GroupDateRanges_InlineVsTableEquivalence proves a named table
// and an equivalent inline set produce identical process results.
func TestFacade_GroupDateRanges_InlineVsTableEquivalence(t *testing.T) {
	p := fiscalCohort(t)
	inline := json.RawMessage(`{"ranges":[` +
		`{"label":"launch","start":"2024-01-01","end":"2024-03-31"},` +
		`{"label":"growth","start":"2024-04-01","end":"2024-09-30"},` +
		`{"label":"steady","start":"2024-10-01"}]}`)

	run := func(params json.RawMessage) map[string]float64 {
		resp, err := p.Process(context.Background(), &types.Request{
			Cohort:       &types.Cohort{Filename: "txns.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "txn_date", Params: params}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
		})
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		out := map[string]float64{}
		for _, row := range resp.Data {
			out[row["txn_date"].(string)] = row["n"].(float64)
		}
		return out
	}

	inlineRes := run(inline)
	tableRes := run(json.RawMessage(`{"table":"fiscal"}`))
	if len(inlineRes) != len(tableRes) {
		t.Fatalf("bucket sets differ: inline=%v table=%v", inlineRes, tableRes)
	}
	for label, n := range inlineRes {
		if tableRes[label] != n {
			t.Errorf("bucket %q: inline=%v table=%v", label, n, tableRes[label])
		}
	}
}

// TestFacade_FilterDateRanges_TableSource is the E2E acceptance for the
// filter: the named table narrows the cohort to in-range rows.
func TestFacade_FilterDateRanges_TableSource(t *testing.T) {
	p := fiscalCohort(t)
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "txns.pulse"},
		Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "txn_date", Params: json.RawMessage(`{"table":"fiscal"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["n"].(float64); got != 4 {
		t.Errorf("kept records = %v, want 4 (launch2+growth1+steady1; 2023 dropped)", got)
	}
}

// TestFacade_DateRanges_TableErrors covers ambiguous-source and
// unknown-table surfacing through the facade for both operators.
func TestFacade_DateRanges_TableErrors(t *testing.T) {
	p := fiscalCohort(t)
	ctx := context.Background()

	grpReq := func(params json.RawMessage) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "txns.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "txn_date", Params: params}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
		}
	}
	filtReq := func(params json.RawMessage) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "txns.pulse"},
			Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "txn_date", Params: params}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
		}
	}

	cases := []struct {
		name string
		req  *types.Request
	}{
		{"grouper both", grpReq(json.RawMessage(`{"ranges":[{"label":"a","start":"2024-01-01"}],"table":"fiscal"}`))},
		{"grouper unknown", grpReq(json.RawMessage(`{"table":"nope"}`))},
		{"filter both", filtReq(json.RawMessage(`{"ranges":[{"label":"a","start":"2024-01-01"}],"table":"fiscal"}`))},
		{"filter unknown", filtReq(json.RawMessage(`{"table":"nope"}`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.Process(ctx, tc.req); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
