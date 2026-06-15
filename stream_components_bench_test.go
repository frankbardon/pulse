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

// E4-S9 — perf bench locks for the Components emission machinery.
//
// These establish the baseline numbers that E5 (final perf budget gates)
// will lock against. The benches are hermetic: in-memory afero fs, no
// disk I/O, no external fixture files. Each bench drives a 100K-record
// f64 cohort through a five-aggregator mix that covers both mergeable
// (AGG_SUM / AGG_COUNT / AGG_AVERAGE / AGG_VARIANCE — Welford-family)
// and non-mergeable (AGG_MEDIAN) paths so the buffered terminal flush
// and per-chunk redaction code paths are both exercised.

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

// BenchmarkProcessStream_WithComponents establishes the baseline
// throughput for the streaming Process pipeline with Response.Components
// emission enabled across a five-aggregator mergeable/non-mergeable mix.
// Components emission is always-on in the current build (no
// pulse.Options.DisableComponents knob) so this single sub-case captures
// the budget E5 locks against; if a disable knob lands, add a paired
// "off" sub-case for the +5% delta gate.
//
// Drives 100K records through ProcessStreamResult, drains every chunk to
// avoid backpressure, and waits on the terminator. Reports ns/op +
// allocs/op for the full pipeline; per-record cost is total/100_000.
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

// BenchmarkProcess_BufferedComponents establishes the baseline
// throughput for the buffered Process pipeline with Response.Components
// emission enabled. Same 100K cohort + five-aggregator mix as the
// streaming bench, but driven through pulse.Process so the buffered
// terminal-flush path (which is the only path that can carry the
// AGG_MEDIAN Operator map) is measured end-to-end.
//
// This is the apples-to-apples comparison point for the streaming bench:
// the streaming variant pays per-chunk projection cost; the buffered
// variant pays one terminal-flush cost. E5 locks both to +5% of the
// per-aggregator baseline captured here.
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
