package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// BenchmarkCrosstabWideCohort_Buffered runs the same crosstab workload
// as BenchmarkCrosstabWideCohort but with fusion forced off so the
// buffered RunCrosstab path takes over. Provides the baseline against
// which the fused path's speedup / allocation reduction is measured.
//
// Run with:
//
//	go test ./service/ -bench BenchmarkCrosstabWideCohort_Buffered -benchmem -run=^$
func BenchmarkCrosstabWideCohort_Buffered(b *testing.B) {
	svc, req, ctx := setupCrosstabWideBench(b)
	svc.SetDisableCrosstabFusion(true)

	b.ReportAllocs()
	for b.Loop() {
		_, err := svc.Process(ctx, req)
		if err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}

// BenchmarkCrosstabWideCohort_Fused runs the wide-cohort crosstab with
// the fused streaming path engaged. Compare to
// BenchmarkCrosstabWideCohort_Buffered for the speedup / allocation
// reduction the fused path delivers on a 200-field × 10K-row cohort
// with three referenced fields.
//
// Run with:
//
//	go test ./service/ -bench BenchmarkCrosstabWideCohort_Fused -benchmem -run=^$
func BenchmarkCrosstabWideCohort_Fused(b *testing.B) {
	svc, req, ctx := setupCrosstabWideBench(b)
	// Fusion enabled by default; assert here for clarity.
	svc.SetDisableCrosstabFusion(false)

	b.ReportAllocs()
	for b.Loop() {
		_, err := svc.Process(ctx, req)
		if err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}

// setupCrosstabWideBench builds the same wide-cohort fixture as
// BenchmarkCrosstabWideCohort. Extracted so the fused / buffered
// benchmarks share the cohort + request without duplicating the
// boilerplate.
func setupCrosstabWideBench(b *testing.B) (*Service, *types.Request, context.Context) {
	b.Helper()
	const pad = 197 // 200 total fields including region, segment, value
	const rows = 10000

	schema := buildBenchWideSchema(pad)
	recs := buildBenchWideRecords(rows, pad)

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	for ri, rec := range recs {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				b.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "ct.pulse", buf.Bytes(), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	return svc, req, context.Background()
}
