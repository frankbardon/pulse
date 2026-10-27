//go:build heavy

package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestCrosstab_WeightedTwoMeansZ_DecodeWorkersParity covers the one
// crosstab arm DecodeWorkers really changes: buffered record
// materialisation above parallelDecodeRecordThreshold on a mmap-able
// file (MemMapFs bails to serial, so this needs a temp-dir OsFs).
// Parallel materialisation must reproduce the serial record stream, so
// overlay, Components and matrix match serial buffered and fused.
func TestCrosstab_WeightedTwoMeansZ_DecodeWorkersParity(t *testing.T) {
	t.Parallel()

	const rowCount = parallelDecodeRecordThreshold + 4096
	schema := wzsSchema()
	recs, nullAt := wzsRows(rowCount, 0)

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/wz_decode.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	ctx := context.Background()

	run := func(t *testing.T, fused bool, workers int) *types.Response {
		t.Helper()
		svc := New(cfg)
		svc.SetDecodeWorkers(workers)
		svc.SetDisableCrosstabFusion(!fused)
		resp, err := svc.Process(ctx, wzsCrosstabRequest(path))
		if err != nil {
			t.Fatalf("Process(fused=%t, workers=%d): %v", fused, workers, err)
		}
		return resp
	}

	ref := run(t, false, 1)
	wzsAssertNonVacuous(t, ref, len(wzsCrosstabRequest("").Overlays))

	// Gate check: a silent bail would compare serial to serial.
	probe := New(cfg)
	cohort, err := probe.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, cleanup, available, err := buildParallelDecodeContext(probe, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields))
	if err != nil {
		t.Fatalf("buildParallelDecodeContext: %v", err)
	}
	if cleanup != nil {
		defer func() { _ = cleanup() }()
	}
	if !available {
		t.Fatal("parallel decode unavailable for an OsFs cohort above threshold")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}

	for _, tc := range []struct {
		name    string
		fused   bool
		workers int
	}{
		{"buffered_workers_4", false, 4},
		{"fused_workers_4", true, 4},
		{"fused_workers_1", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, tc.fused, tc.workers)
			wzsAssertJSONNear(t, "Crosstab", ref.Crosstab, got.Crosstab)
			wzsAssertJSONNear(t, "Components", ref.Components.Crosstab, got.Components.Crosstab)
			wzsAssertJSONNear(t, "Overlays", ref.Overlays, got.Overlays)
			wzsAssertJSONNear(t, "Warnings", ref.Warnings, got.Warnings)
		})
	}
}
