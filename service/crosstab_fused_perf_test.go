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

// TestFusedCrosstab_PerfGate is the build-tagged perf acceptance for
// E4-S5. Runs the canonical huge-request.json-shaped crosstab (200
// fields × 100K rows synth cohort) through the fused and buffered
// paths via testing.Benchmark and asserts the fused ns/op is at most
// 0.80 × the buffered ns/op — a 20 % wall-clock improvement.
//
// The 0.80 ratio is the spec's "this Mac" reference; the canonical
// 1M-row bench against the real cohort (vc/1.pulse) clears it
// comfortably (~0.67 on the maintainer's M1 Max). Synth-scale parity
// at 10K-100K rows is expected because per-record decode overhead
// dominates at small N; the gate captures the win at the scale that
// matters.
//
// This test mirrors the E3-S5 perf-gate pattern: gated by the `perf`
// build tag so the default `go test ./...` run stays fast, and
// surfaced through `make bench` or `go test -tags=perf
// ./service/... -run TestFusedCrosstab_PerfGate`. Use `-count=3` and
// compare medians when investigating regressions; a single run is
// noisy.
//
// If runtime.NumCPU() is 1 on the test host, the buffered path's
// shard-worker parallelism collapses to serial, eliminating the
// shard-reducer's contribution to the buffered baseline. The gate
// still applies — fusion's win comes from skipping record
// materialisation, not from parallelism — but the absolute ratio may
// tighten.
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
