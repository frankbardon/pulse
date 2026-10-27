//go:build heavy

package service

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// TestWideSet_DecodeWorkersParity pins Options.DecodeWorkers on a
// single-file wide-set cohort. The fixture is deliberately larger than
// parallelDecodeRecordThreshold and lives on the real OS filesystem,
// because the eligibility predicate needs BOTH (record count >=
// threshold, and mmap via RealPather) and bails silently on either.
func TestWideSet_DecodeWorkersParity(t *testing.T) {
	t.Parallel()

	const rowCount = parallelDecodeRecordThreshold + 4096

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/wide_set_parity.pulse"

	schema, payload := wideSetParityCohort(t, rowCount)
	writeWideSetCohort(t, osFs, path, schema, payload)

	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	if !processing.CanMergeRequest(wideSetParityRequest(path), schema) {
		t.Fatal("wide-set aggregation request is not mergeable; the parallel decode path would never engage")
	}

	// Gate check: the parallel decode path must actually be available
	// for this cohort. A silent bail here is exactly the failure mode
	// this test exists to rule out.
	svc := New(cfg)
	svc.SetDecodeWorkers(4)
	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pctx, cleanup, available, err := buildParallelDecodeContext(
		svc, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields),
	)
	if err != nil {
		t.Fatalf("buildParallelDecodeContext: %v", err)
	}
	t.Cleanup(func() {
		if cleanup != nil {
			_ = cleanup()
		}
	})
	if !available {
		t.Fatal("parallel decode unavailable for an OsFs cohort above threshold — the parity check below would compare serial against serial")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}
	_ = pctx

	// Serial baseline: DecodeWorkers == 1 forces the serial path.
	serialSvc := New(cfg)
	serialSvc.SetDecodeWorkers(1)
	serialResp, err := serialSvc.Process(context.Background(), wideSetParityRequest(path))
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if len(serialResp.Data) == 0 {
		t.Fatal("serial: expected an aggregation row")
	}
	wantRow := serialResp.Data[0]
	assertWideSetRowNonDegenerate(t, wantRow)

	for _, workers := range []int{2, 4, runtime.NumCPU()} {
		workers := workers
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			parSvc := New(cfg)
			parSvc.SetDecodeWorkers(workers)
			resp, err := parSvc.Process(context.Background(), wideSetParityRequest(path))
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			if len(resp.Data) == 0 {
				t.Fatal("parallel: expected an aggregation row")
			}
			gotRow := resp.Data[0]
			for _, key := range []string{"n", "union", "freq", "card_sum", "card_avg", "distinct"} {
				if !reflect.DeepEqual(gotRow[key], wantRow[key]) {
					t.Errorf("workers=%d: %q differs\n  parallel: %v\n  serial:   %v",
						workers, key, gotRow[key], wantRow[key])
				}
			}
			// Run components must agree. PER-SLOT
			// Components.Aggregations is deliberately NOT asserted
			// here: neither parallel reducer emits it on any
			// aggregator, set or numeric — shardPartial carries a
			// single primary-field nullRecords counter and no per-slot
			// {n, n_null} floor, so finalizeMergedPartial can only
			// populate Components.Run. That gap predates wide sets and
			// closing it is a per-slot floor-tracking change across
			// both arms and their mergers, not a set-type change.
			if resp.Components == nil || serialResp.Components == nil {
				t.Fatal("expected Components on both arms")
			}
			if !reflect.DeepEqual(resp.Components.Run, serialResp.Components.Run) {
				t.Errorf("workers=%d: Components.Run differs\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Run, serialResp.Components.Run)
			}
		})
	}
}
