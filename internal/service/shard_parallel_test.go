package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// fourShardScoreCohort builds a 16-row score cohort across four
// shards. Used by the parallel-vs-serial parity tests so we exercise
// the multi-worker merge path with non-trivial cardinality.
func fourShardScoreCohort(t *testing.T) (*fs.Config, *encoding.Schema, [][]uint64) {
	t.Helper()
	schema := twoShardScoreSchema()
	concat := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
		{4, math.Float64bits(40.0)},
		{5, math.Float64bits(50.0)},
		{6, math.Float64bits(60.0)},
		{7, math.Float64bits(70.0)},
		{8, math.Float64bits(80.0)},
		{9, math.Float64bits(90.0)},
		{10, math.Float64bits(100.0)},
		{11, math.Float64bits(110.0)},
		{12, math.Float64bits(120.0)},
		{13, math.Float64bits(130.0)},
		{14, math.Float64bits(140.0)},
		{15, math.Float64bits(150.0)},
		{16, math.Float64bits(160.0)},
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: concat[0:4]},
		{Name: "b.pulse", Records: concat[4:8]},
		{Name: "c.pulse", Records: concat[8:12]},
		{Name: "d.pulse", Records: concat[12:16]},
	}
	cfg := fs.NewMemMap()
	archive := buildShardArchive(t, schema, shards)
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", archive, 0644); err != nil {
		t.Fatalf("WriteFile archive: %v", err)
	}
	single := writeSinglePulse(t, schema, concat)
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse.concat", single, 0644); err != nil {
		t.Fatalf("WriteFile concat: %v", err)
	}
	return cfg, schema, concat
}

// TestShardArchiveProcessParallel_NonMergeableFallsThrough confirms
// that requests containing a non-mergeable operator (here AGG_MEDIAN,
// a buffered aggregator) bypass the parallel reducer entirely — the
// serial shardIter path runs, and the result matches the concatenated
// single-file cohort exactly. Regression guard for the
// shouldFanOut() gate.
func TestShardArchiveProcessParallel_NonMergeableFallsThrough(t *testing.T) {
	t.Parallel()

	cfg, _, _ := fourShardScoreCohort(t)
	svc := New(cfg)
	svc.SetShardWorkers(4)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_MEDIAN, Field: "score", Label: "v"},
			},
		}
	}
	respA, err := svc.Process(context.Background(), req("arch.pulse"))
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	respB, err := svc.Process(context.Background(), req("arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process concat: %v", err)
	}
	a, _ := respA.Data[0]["v"].(float64)
	b, _ := respB.Data[0]["v"].(float64)
	if a != b {
		t.Errorf("AGG_MEDIAN parallel-fallthrough: archive=%v concat=%v (must match)", a, b)
	}
	// 16 rows (10..160 step 10): median = (80+90)/2 = 85.
	if a != 85 {
		t.Errorf("AGG_MEDIAN = %v, want 85", a)
	}
}

func TestShardArchiveProcessParallel_SerialEqualsOne(t *testing.T) {
	t.Parallel()

	cfg, _, _ := fourShardScoreCohort(t)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	svc1 := New(cfg)
	svc1.SetShardWorkers(1)
	resp1, err := svc1.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process workers=1: %v", err)
	}
	// Default (0 -> NumCPU) — should match exactly on byte-equal aggs.
	svcDefault := New(cfg)
	respDefault, err := svcDefault.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process default: %v", err)
	}
	if resp1.Data[0]["s"] != respDefault.Data[0]["s"] {
		t.Errorf("SUM serial=%v default=%v", resp1.Data[0]["s"], respDefault.Data[0]["s"])
	}
	if resp1.Data[0]["n"] != respDefault.Data[0]["n"] {
		t.Errorf("COUNT serial=%v default=%v", resp1.Data[0]["n"], respDefault.Data[0]["n"])
	}
}
