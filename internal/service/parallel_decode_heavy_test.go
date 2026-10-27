//go:build heavy

package service

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func TestCrosstab_ParallelDecode_ByteEqualToSerial(t *testing.T) {
	const rowCount = parallelDecodeRecordThreshold + 1024 // just above floor

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/parallel_eq.pulse"

	schema, payload := buildParallelEquivCohort(t, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	mkReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "brand"},
				},
				Columns: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "region"},
				},
				Cell: &types.Aggregation{
					Type:  types.AGG_SUM,
					Field: "weight",
					Label: "sum_weight",
				},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}

	ctx := context.Background()

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}

	svcParallel := New(cfg)
	svcParallel.SetDecodeWorkers(4)
	parallelResp, err := svcParallel.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("parallel Process: %v", err)
	}

	if serialResp.Crosstab == nil || serialResp.Crosstab.Matrix == nil {
		t.Fatal("serial: expected matrix payload")
	}
	if parallelResp.Crosstab == nil || parallelResp.Crosstab.Matrix == nil {
		t.Fatal("parallel: expected matrix payload")
	}

	// Deep-equal compare the matrix payload. Crosstab matrices use
	// stable key ordering (groupers emit keys deterministically), so
	// the per-cell values must match byte-for-byte. Any divergence —
	// even a single ULP on a Welford-mean cell — is a real
	// segment-boundary bug.
	if !reflect.DeepEqual(serialResp.Crosstab.Matrix, parallelResp.Crosstab.Matrix) {
		t.Errorf("Matrix differs between serial and parallel decode")
	}

	if !reflect.DeepEqual(serialResp.Crosstab, parallelResp.Crosstab) {
		t.Errorf("Crosstab top-level differs between serial and parallel decode")
	}

	// Metadata equivalence: TotalRows and FilteredRows must match
	// exactly; CohortFile is the same absolute path on both sides.
	if serialResp.Metadata == nil || parallelResp.Metadata == nil {
		t.Fatalf("expected metadata on both sides: serial=%v parallel=%v",
			serialResp.Metadata, parallelResp.Metadata)
	}
	if serialResp.Metadata.TotalRows != parallelResp.Metadata.TotalRows {
		t.Errorf("TotalRows differs: serial=%d parallel=%d",
			serialResp.Metadata.TotalRows, parallelResp.Metadata.TotalRows)
	}
	if serialResp.Metadata.FilteredRows != parallelResp.Metadata.FilteredRows {
		t.Errorf("FilteredRows differs: serial=%d parallel=%d",
			serialResp.Metadata.FilteredRows, parallelResp.Metadata.FilteredRows)
	}
}
