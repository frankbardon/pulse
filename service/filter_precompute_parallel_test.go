package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestFilterPrecompute_ParallelDecode (E5-S1): the parallel buffered
// Process arm builds one FilterFunc set per worker, so each worker keeps
// its own verdict table over the shared (read-only) group dictionary.
// Over the >100K-record grouped fixture the result — filterer
// components included — equals the 0x01 cohort's and the per-row run's
// at every worker count, and the entry evaluations are bounded by
// workers × entries, never by rows.
func TestFilterPrecompute_ParallelDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping >100K-record parallel decode fixture in -short mode")
	}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	cohorts := strideParallelCohorts(t)
	for _, name := range []string{"flat", "grouped"} {
		if err := afero.WriteFile(osFs, dir+"/"+name+".pulse", cohorts[name], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	req := func(path string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "west"}},
				{Type: types.FILTER_RANGE, Field: "weight", Values: []string{"12", "100"}},
				{Type: types.FILTER_INCLUDE, Field: "src", Values: []string{"batch-a"}},
			},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}, {Type: types.AGG_SUM, Field: "amount", Label: "amount"}},
		}
	}
	svc := New(cfg)
	gc, err := svc.Open(context.Background(), dir+"/grouped.pulse")
	if err != nil {
		t.Fatal(err)
	}
	gs := gc.Schema()
	if ok, why := svc.canParallelDecode(req(dir+"/grouped.pulse"), gs, gc, 4, strideParallelRows); !ok {
		t.Fatalf("parallel decode gate closed: %s", why)
	}
	// Per worker, each filter slot evaluates at most its group's entry
	// count: region and weight share the parent group, src is constant.
	perWorker := int64(2*gs.GroupEntryCount(0) + gs.GroupEntryCount(1))

	run := func(path string, workers int, precompute bool) (string, int64) {
		t.Helper()
		prev := processing.SetFilterPrecompute(precompute)
		defer processing.SetFilterPrecompute(prev)
		s := New(cfg)
		s.SetDecodeWorkers(workers)
		before := processing.FilterPrecomputeStats().EntryEvaluations
		resp, err := s.Process(context.Background(), req(path))
		if err != nil {
			t.Fatalf("%s workers=%d: %v", path, workers, err)
		}
		if resp.Components == nil || len(resp.Components.Filterers) != 3 {
			t.Fatalf("%s workers=%d: filterer components missing", path, workers)
		}
		return fmt.Sprintf("%v|%+v", resp.Data, resp.Components.Filterers), processing.FilterPrecomputeStats().EntryEvaluations - before
	}
	want, _ := run(dir+"/flat.pulse", 1, true)
	for _, workers := range []int{1, 2, 4, 7} {
		got, evals := run(dir+"/grouped.pulse", workers, true)
		if got != want {
			t.Fatalf("workers=%d precomputed:\n got  %s\n want %s", workers, got, want)
		}
		if evals == 0 || evals > int64(workers)*perWorker {
			t.Fatalf("workers=%d: %d entry evaluations, want 1..%d (workers × entries per filter) over %d rows", workers, evals, int64(workers)*perWorker, strideParallelRows)
		}
		t.Logf("workers=%d: %d entry evaluations over %d rows", workers, evals, strideParallelRows)
		perRow, evals := run(dir+"/grouped.pulse", workers, false)
		if perRow != want || evals != 0 {
			t.Fatalf("workers=%d per-row: %d entry evaluations, result %s", workers, evals, perRow)
		}
	}
}
