package pulse

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

const benchComponentsRowCount = 100_000

// writeBenchComponentsCohort materialises a 100K-row, single-f64-field
// cohort into memFs at "bench.pulse". Returns the synthesised path. The
// payload is small (~800KB) so multiple b.Run sub-cases re-use the same
// in-memory file cheaply.
func writeBenchComponentsCohort(b *testing.B, memFs afero.Fs) string {
	b.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	for i := 0; i < benchComponentsRowCount; i++ {
		// Deterministic pseudo-random float to avoid trivial constant
		// folding in the aggregators' running state.
		v := float64((i*37)%1009) + 0.5
		bits := math.Float64bits(v)
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, bits); err != nil {
			b.Fatalf("WriteFieldValue: %v", err)
		}
	}
	const path = "bench.pulse"
	if err := afero.WriteFile(memFs, path, buf.Bytes(), 0o644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	return path
}

// buildBenchMixedAggRequest returns a Request carrying five aggregators
// over the single f64 field "score". Four are mergeable (SUM, COUNT,
// AVERAGE, VARIANCE — Welford-family); one is non-mergeable (MEDIAN).
// The mix exercises both the per-chunk Components-projection path and
// the terminal-only buffered-flush path in one bench.
func buildBenchMixedAggRequest(path string) *Request {
	return &Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n_score"},
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
			{Type: types.AGG_VARIANCE, Field: "score", Label: "var_score"},
			{Type: types.AGG_MEDIAN, Field: "score", Label: "median_score"},
		},
	}
}

func BenchmarkProcessStream_WithComponents(b *testing.B) {
	memFs := afero.NewMemMapFs()
	path := writeBenchComponentsCohort(b, memFs)
	p, err := New(Options{FS: memFs})
	if err != nil {
		b.Fatalf("pulse.New: %v", err)
	}
	req := buildBenchMixedAggRequest(path)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		res, err := p.ProcessStreamResult(ctx, req)
		if err != nil {
			b.Fatalf("ProcessStreamResult: %v", err)
		}
		for range res.Chunks {
			// Drain — backpressure-bound; iterator runs on the producer
			// goroutine and stops when consumer falls behind.
		}
		term := <-res.Done
		if term.Status != StreamCompleted {
			b.Fatalf("terminal status = %v (err=%v)", term.Status, term.Error)
		}
	}
}

func BenchmarkProcess_BufferedComponents(b *testing.B) {
	memFs := afero.NewMemMapFs()
	path := writeBenchComponentsCohort(b, memFs)
	p, err := New(Options{FS: memFs})
	if err != nil {
		b.Fatalf("pulse.New: %v", err)
	}
	req := buildBenchMixedAggRequest(path)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		resp, err := p.Process(ctx, req)
		if err != nil {
			b.Fatalf("Process: %v", err)
		}
		if resp.Components == nil || len(resp.Components.Aggregations) != 5 {
			b.Fatalf("Components.Aggregations missing or wrong cardinality; got %+v",
				resp.Components)
		}
	}
}
