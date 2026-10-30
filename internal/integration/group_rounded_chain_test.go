package integration_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestGroupRounded_ChainStageAdmitted (U51 E3-S3): GROUP_ROUNDED is
// mergeable, so the shared chain gate admits it on every ProcessChain
// stage — the runtime runs it (stage 1 rounds stage 0's f64 output) and
// PredictChain validates the same chain with no error, where both used
// to answer PULSE_CHAIN_NOT_MERGEABLE.
func TestGroupRounded_ChainStageAdmitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeBitExactFile(t, filepath.Join(dir, "small.pulse"), bitExactSchema(), func(r uint64) bool { return r < 600 })
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "small.pulse"},
		Stages: []*types.ChainStage{
			{Name: "s0", Request: &types.Request{
				Groups: []*types.Group{{Type: types.GROUP_ROUNDED, Field: "y", Interval: 10}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "y", Label: "n"},
					{Type: types.AGG_SUM, Field: "y", Label: "s"},
				},
			}},
			{Name: "s1", Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_ROUNDED, Field: "n", Interval: 20}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "s", Label: "buckets"}},
			}},
		},
	}
	ctx := context.Background()
	resp, err := p.ProcessChain(ctx, chain)
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if len(resp.Stages) != 2 || len(resp.Stages[0].Data) < 5 || resp.Final == nil || len(resp.Final.Data) == 0 {
		t.Fatalf("degenerate chain answer: %d stages", len(resp.Stages))
	}
	total := 0.0
	for _, row := range resp.Final.Data {
		v, _ := row["buckets"].(float64)
		total += v
	}
	if int(total) != len(resp.Stages[0].Data) {
		t.Errorf("stage 1 counted %v stage-0 rows, stage 0 emitted %d", total, len(resp.Stages[0].Data))
	}
	env, err := p.PredictChain(ctx, chain)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Errorf("PredictChain refuses what the runtime ran: %+v", env.Errors)
	}
}
