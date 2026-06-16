//go:build perf

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

func TestFusedCrosstab_PerfGate(t *testing.T) {
	const (
		fieldCount = 200
		rowCount   = 100_000
	)

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/perf_wide.pulse"

	schema, payload := buildWideCohort(t, fieldCount, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _ := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))

	buildReq := func() *types.Request {
		fiscalOffset := -3
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "brand"},
				},
				Columns: []*types.Group{
					{
						Type:   types.GROUP_DATE,
						Field:  "waveDate",
						Params: dateParams("quarter", &fiscalOffset),
					},
					{Type: types.GROUP_CATEGORY, Field: "cardFeeling"},
				},
				Cell: &types.Aggregation{
					Type:  types.AGG_SUM,
					Field: "weight",
					Label: "sum_weight",
				},
				Margins:         types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:           types.CrosstabShapeMatrix,
				Normalize:       types.CrosstabNormalizeRow,
				NormalizeWithin: intPtr(0),
			},
		}
	}
	ctx := context.Background()

	// Warmup once outside the timed loop so the first iteration's
	// page-fault / cache-warm cost doesn't skew the smaller of the
	// two measurements.
	warmup := New(cfg)
	if _, err := warmup.Process(ctx, buildReq()); err != nil {
		t.Fatalf("warmup Process: %v", err)
	}

	measure := func(disableFusion bool) testing.BenchmarkResult {
		return testing.Benchmark(func(b *testing.B) {
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(disableFusion)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := svc.Process(ctx, buildReq()); err != nil {
					b.Fatalf("Process: %v", err)
				}
			}
		})
	}

	bufferedResult := measure(true)
	fusedResult := measure(false)

	bufferedNs := float64(bufferedResult.NsPerOp())
	fusedNs := float64(fusedResult.NsPerOp())
	if bufferedNs <= 0 || fusedNs <= 0 {
		t.Fatalf("invalid bench timings: buffered=%v fused=%v", bufferedResult, fusedResult)
	}

	ratio := fusedNs / bufferedNs
	t.Logf("buffered: %d ns/op  %d B/op  %d allocs/op",
		bufferedResult.NsPerOp(), bufferedResult.AllocedBytesPerOp(), bufferedResult.AllocsPerOp())
	t.Logf("fused:    %d ns/op  %d B/op  %d allocs/op",
		fusedResult.NsPerOp(), fusedResult.AllocedBytesPerOp(), fusedResult.AllocsPerOp())
	t.Logf("ratio (fused / buffered): %.3f (target ≤ 0.80)", ratio)

	const gate = 0.80
	if ratio > gate {
		t.Errorf("fused ns/op (%d) is not ≤ %.2f × buffered ns/op (%d); ratio=%.3f",
			fusedResult.NsPerOp(), gate, bufferedResult.NsPerOp(), ratio)
	}
}
