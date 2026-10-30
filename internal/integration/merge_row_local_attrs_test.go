package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestChain_RowLocalReadAttributesAdmitted (#391): ATTR_CODE_IN,
// ATTR_SET_HAS and ATTR_SET_POPCOUNT are row-local with no per-run
// state, so the shared merge gate admits them on a ProcessChain stage —
// predict-chain and the runtime agree (both used to refuse the stage
// PULSE_CHAIN_NOT_MERGEABLE) — and stage 0 answers exactly what the
// same request answers through Process.
func TestChain_RowLocalReadAttributesAdmitted(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	ctx := context.Background()
	stage0 := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: f.cohort},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_CODE_IN, Field: "region", Label: "in", Params: []byte(`{"codes":["north","west"]}`)},
				{Type: types.ATTR_SET_HAS, Field: "tags", Label: "has_a", Params: []byte(`{"label":"a"}`)},
				{Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "pc"},
			},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "in", Label: "in_n"},
				{Type: types.AGG_SUM, Field: "has_a", Label: "a_n"},
				{Type: types.AGG_SUM, Field: "pc", Label: "pc_sum"},
			},
		}
	}
	chain := &types.ChainRequest{Cohort: &types.Cohort{Filename: f.cohort}, Stages: []*types.ChainStage{
		{Name: "s0", Request: stage0()},
		{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "in_n", Label: "in_total"}}}},
	}}
	env, err := f.p.PredictChain(ctx, chain)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict-chain: %v %+v", err, env)
	}
	got, err := f.p.ProcessChain(ctx, chain)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	want, err := f.p.Process(ctx, stage0())
	if err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got.Stages[0].Data)
	w, _ := json.Marshal(want.Data)
	if string(g) != string(w) || len(want.Data) != 2 {
		t.Fatalf("stage 0 %s, Process %s", g, w)
	}
	var total float64
	for _, row := range want.Data {
		v, _ := row["in_n"].(float64)
		total += v
	}
	if total == 0 || got.Final.Data[0]["in_total"] != total {
		t.Fatalf("final %v, want in_total %v", got.Final.Data, total)
	}
}
