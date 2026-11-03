package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestMmapCapability_PredictMatchesRuntime (U77 E2-S1): on a build that
// cannot map (mmap_other.go), predict's DecodeMappable, the
// canParallelDecode gate, the run's dispatch pre-gate and the parallel
// context build all refuse together, so predict charges the serial arm
// the run takes. With the capability on, every one of them accepts the
// same real-path single file. The capability is flipped through
// mmapAvailable, so this runs on every OS.
//
// Falsified by reverting decodeMappable or canParallelDecode to the bare
// resolveRealPath probe (the "off" half then sees a mappable answer).
//
// serial: flips the package-level mmapAvailable capability.
func TestMmapCapability_PredictMatchesRuntime(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := filepath.Join(dir, "big.pulse")
	if err := afero.WriteFile(osFs, path, decodeFanOutFile(t, parallelDecodeRecordThreshold+4096), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	mkReq := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}}
	}

	saved := mmapCapable()
	t.Cleanup(func() { mmapAvailable.Store(saved) })

	for _, capable := range []bool{true, false} {
		mmapAvailable.Store(capable)
		svc := New(cfg)
		svc.SetDecodeWorkers(4)
		l := limits.Defaults()
		l.MaxEstimatedMemory = 1 << 50
		svc.SetLimits(l)
		cohort, err := svc.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		count, err := svc.CountRecords(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		req := mkReq()

		if got := decodeMappable(cohort); got != capable {
			t.Errorf("capable=%v: decodeMappable = %v", capable, got)
		}
		if got := svc.DecodeMappable(ctx, path); got != capable {
			t.Errorf("capable=%v: DecodeMappable = %v", capable, got)
		}
		ok, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, 4, int(count))
		if ok != capable {
			t.Errorf("capable=%v: canParallelDecode = %v (%q)", capable, ok, reason)
		}
		if !capable && reason != reasonMmapUnsupported {
			t.Errorf("canParallelDecode reason = %q, want %q", reason, reasonMmapUnsupported)
		}

		// Predict's arm: the decode fan-out over the run's limit inputs.
		in, err := svc.limitInputs(ctx, cohort, path)
		if err != nil {
			t.Fatal(err)
		}
		predicted := descx.DecodeFanOut(req, cohort.Schema(), svc.instance, in)
		// The run's arm: the parallel dispatch either takes the request
		// or hands it back to the serial path.
		resp, ran, err := svc.processSingleFileParallelMaybe(ctx, processing.StampWeights(mkReq(), nil), cohort, path)
		if err != nil {
			t.Fatal(err)
		}
		if predicted != ran || ran != capable {
			t.Errorf("capable=%v: predicted parallel arm %v, run took it %v", capable, predicted, ran)
		}
		if ran && resp == nil {
			t.Error("parallel arm ran but returned no response")
		}
		// Release the mapping: on Windows an open view blocks the
		// TempDir cleanup's delete of big.pulse.
		_, cleanup, available, err := buildParallelDecodeContext(svc, path, cohort.Schema(), nil, nil, 0)
		if cleanup != nil {
			if cErr := cleanup(); cErr != nil {
				t.Errorf("capable=%v: buildParallelDecodeContext cleanup: %v", capable, cErr)
			}
		}
		if err != nil || available != capable {
			t.Errorf("capable=%v: buildParallelDecodeContext available=%v err=%v", capable, available, err)
		}
	}
}
