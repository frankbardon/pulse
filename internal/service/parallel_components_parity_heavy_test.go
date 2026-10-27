//go:build heavy

package service

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// Same contract, other knob. The DecodeWorkers arm shares
// shardPartial / mergeShardPartials / finalizeMergedPartial with the
// shard reducer, so it inherited the same hole — plus one of its own:
// its per-record callback never incremented nullRecords at all, so
// Run.NullRecords came back 0 where serial reported the real count.
func TestDecodeWorkers_ComponentsParity(t *testing.T) {
	t.Parallel()

	const rowCount = parallelDecodeRecordThreshold + 4096

	schema := componentsParitySchema()
	recs, nullAt := componentsParityRows(rowCount, 0)
	payload := writeNullablePulse(t, schema, recs, nullAt)

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/components_parity.pulse"
	if err := afero.WriteFile(osFs, path, payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	req := componentsParityRequest(path)
	if !processing.CanMergeRequest(req, schema) {
		t.Fatal("fixture request is not mergeable; the parallel decode reducer would never engage")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}

	serialSvc := New(cfg)
	serialSvc.SetDecodeWorkers(1)
	serialResp, err := serialSvc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if serialResp.Components == nil {
		t.Fatal("serial run emitted no Components at all")
	}
	want := serialResp.Components.Aggregations
	assertAggregationComponentsUseful(t, want)
	wantRun := serialResp.Components.Run
	if wantRun == nil || wantRun.NullRecords == 0 {
		t.Fatalf("serial Run components carry no null count: %+v", wantRun)
	}

	for _, workers := range []int{2, 4} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			parSvc := New(cfg)
			parSvc.SetDecodeWorkers(workers)

			// Gate check: a silent bail would compare serial to serial.
			cohort, oerr := parSvc.Open(context.Background(), path)
			if oerr != nil {
				t.Fatalf("Open: %v", oerr)
			}
			_, cleanup, available, berr := buildParallelDecodeContext(
				parSvc, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields))
			if berr != nil {
				t.Fatalf("buildParallelDecodeContext: %v", berr)
			}
			if cleanup != nil {
				defer func() { _ = cleanup() }()
			}
			if !available {
				t.Fatal("parallel decode unavailable for an OsFs cohort above threshold")
			}

			resp, err := parSvc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			if resp.Components == nil {
				t.Fatal("parallel run emitted no Components")
			}
			if !reflect.DeepEqual(resp.Components.Aggregations, want) {
				t.Errorf("Components.Aggregations differ under DecodeWorkers=%d\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Aggregations, want)
			}
			if !reflect.DeepEqual(resp.Components.Run, wantRun) {
				t.Errorf("Components.Run differs under DecodeWorkers=%d\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Run, wantRun)
			}
		})
	}
}
