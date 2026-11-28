package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestWindows_ReadDecimalAggregation (#790): WIN_SHARE, WIN_LIFT and
// WIN_FORMULA over a decimal128 SUM read the sum's value, under Process
// and ProcessStream alike, and predict accepts the request. Before,
// WIN_SHARE and WIN_LIFT read the decimal cell as null on every row and
// returned null, without a warning. Over out.pulse each region holds
// 12 rows, three of each fee 1.25 .. 5.00: fees = 37.50, n = 12.
func TestWindows_ReadDecimalAggregation(t *testing.T) {
	t.Parallel()
	p, fsys := wfOutCohort(t)
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "out.pulse"},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "amt", Label: "fees"},
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			},
			Windows: winFormulaWindows(`[
				{"type": "WIN_SHARE", "field": "fees", "label": "fee_share"},
				{"type": "WIN_SHARE", "field": "fees", "label": "fee_cshare", "order_by": [{"field": "region"}], "params": {"cumulative": true}},
				{"type": "WIN_LIFT", "field": "fees", "label": "fee_lift", "params": {"base": "n"}},
				{"type": "WIN_FORMULA", "label": "fee_per", "params": {"expression": "fees / n"}}
			]`),
		}
	}
	if env := predictEnvelope(t, p, fsys, "out.pulse", req()); len(env.Errors) != 0 {
		t.Fatalf("predict refused: %s %s", env.Errors[0].Code, env.Errors[0].Message)
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	stream := wfStreamRows(ctx, t, p, req())
	if len(resp.Data) != len(wfRegions) || len(stream) != len(resp.Data) {
		t.Fatalf("rows: Process %d, ProcessStream %d, want %d", len(resp.Data), len(stream), len(wfRegions))
	}
	third := 37.5 / (3 * 37.5)
	for i, row := range resp.Data {
		region, _ := row["region"].(string)
		if b, err := json.Marshal(row["fees"]); err != nil || string(b) != `"37.50"` {
			t.Fatalf("region %s: fees = %s (%v), want the decimal string 37.50 — the case proves nothing", region, b, err)
		}
		cshare := 0.0
		for j := 0; j <= i; j++ {
			cshare += 37.5
		}
		cshare /= 3 * 37.5
		for k, want := range map[string]float64{"fee_share": third, "fee_cshare": cshare, "fee_lift": third / (12.0 / 36.0), "fee_per": 37.5 / 12} {
			if got := cellNumber(t, row, k); got != want {
				t.Errorf("region %s: %s = %v, want %v", region, k, row[k], want)
			}
			if stream[i][k] != row[k] {
				t.Errorf("region %s: %s ProcessStream %v, Process %v", region, k, stream[i][k], row[k])
			}
		}
	}
}
