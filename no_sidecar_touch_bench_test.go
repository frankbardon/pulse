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

// buildSidecarBenchCohort materializes a small, realistic (id u32,
// region categorical_u8, amount f64) cohort as raw .pulse bytes —
// header + schema + fixed-width records, no null bitmap — mirroring
// the raw-byte-construction convention service's buffered-decode
// benchmarks use (service/buffered_process_bench_test.go's
// buildWideCohort) so fixture-build cost stays out of the timed
// warmup and the benchmark exercises a realistic buffered-decode +
// aggregation workload rather than the (slower, allocation-heavy)
// CSV-import fixture path.
func buildSidecarBenchCohort(b testing.TB, rowCount int) []byte {
	b.Helper()

	regionDict := encoding.NewDictionary()
	regions := []string{"north", "south", "east", "west"}
	for _, r := range regions {
		if _, err := regionDict.Add(r); err != nil {
			b.Fatalf("region dict: %v", err)
		}
	}

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: regionDict, ByteOffset: 4, CsvColumnIdx: 1},
		{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 5, CsvColumnIdx: 2},
	}}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}

	for r := range rowCount {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, uint64(r)); err != nil {
			b.Fatalf("write id: %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, uint64(r%len(regions))); err != nil {
			b.Fatalf("write region: %v", err)
		}
		amount := float64((r*37)%10007) + 0.5
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(amount)); err != nil {
			b.Fatalf("write amount: %v", err)
		}
	}

	return buf.Bytes()
}

// BenchmarkProcess_SidecarIndexPresence is the runnable measurement
// half of E6-S1's "zero-regression" requirement: buffered decode +
// a representative aggregation (AGG_SUM + AGG_COUNT over the same
// field, matching the aggregation shape service/
// buffered_process_bench_test.go's BenchmarkBufferedProcessWideCohortMergeable
// already benches for the wide-schema-decode-perf effort) run twice
// against the SAME cohort — once before any sidecar index exists,
// once after Pulse.BuildIndex has written a sidecar next to it. The
// correctness half of the invariant (Process never opens/stats the
// sidecar) is TestNoSidecarTouch_ProcessSampleFacetNeverAccessTheIndex
// in no_sidecar_touch_test.go; this benchmark is the human-in-the-loop
// wall-clock measurement `make bench` / `benchstat` compares.
//
// Wall-clock ns/op is NOT gated in CI here — a strict wall-clock
// threshold in a shared CI runner is flaky (see the existing
// service/parallel_decode_perf_test.go's own rationale for gating its
// ratio check behind `-tags=perf`, opt-in only, never wired into any
// CI workflow). The enforceable, deterministic form of "did the
// sidecar's presence add cost" lives in
// TestNoRegression_ProcessAccessCountUnaffectedByIndexPresence below,
// which asserts the exact filesystem access COUNT (not timing) is
// identical with and without the sidecar present — a non-flaky proxy
// that is nonetheless load-bearing, since I/O call count is what
// would change if a future edit accidentally made Process probe for
// or read the sidecar.
func BenchmarkProcess_SidecarIndexPresence(b *testing.B) {
	b.Run("no_sidecar_index", func(b *testing.B) {
		runSidecarPresenceBench(b, false)
	})
	b.Run("with_sidecar_index", func(b *testing.B) {
		runSidecarPresenceBench(b, true)
	})
}

// runSidecarPresenceBench runs the inner Process loop for one
// with/without-sidecar variant. Extracted from the b.Run closures
// (mirroring service/buffered_process_bench_test.go's
// runMergeableBenchInner / runMergeableBench split) so the opt-in
// wall-clock perf gate (no_sidecar_touch_perf_test.go, //go:build
// perf) can drive each variant independently via testing.Benchmark
// without re-running the whole parent benchmark to isolate one
// sub-case.
func runSidecarPresenceBench(b *testing.B, withIndex bool) {
	b.Helper()

	const rowCount = 20_000

	memFs := afero.NewMemMapFs()
	const path = "bench_sidecar.pulse"
	if err := afero.WriteFile(memFs, path, buildSidecarBenchCohort(b, rowCount), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}

	p, err := New(Options{FS: memFs})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if withIndex {
		if _, err := p.BuildIndex(ctx, path, []string{"id"}); err != nil {
			b.Fatalf("BuildIndex: %v", err)
		}
	}

	mkReq := func() *Request {
		return &Request{
			Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "amount", Label: "sum_amount"},
				{Type: types.AGG_COUNT, Field: "amount", Label: "n"},
			},
		}
	}

	if _, err := p.Process(ctx, mkReq()); err != nil {
		b.Fatalf("warmup Process (withIndex=%v): %v", withIndex, err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.Process(ctx, mkReq()); err != nil {
			b.Fatalf("Process (withIndex=%v): %v", withIndex, err)
		}
	}
}

// TestNoRegression_ProcessAccessCountUnaffectedByIndexPresence is the
// enforceable, non-flaky companion to the wall-clock benchmark above.
// Wall-clock is not a safe CI gate (shared-runner noise), but the
// NUMBER of filesystem calls (Open/OpenFile/Stat) Process makes to
// satisfy one request is deterministic — it does not depend on host
// load. This test builds the exact same request against the exact
// same cohort twice: once before a sidecar index exists, once after,
// and asserts the recorded access COUNT and access PATH LIST are
// byte-for-byte identical. Any future change that made Process probe
// for, stat, or conditionally branch on the sidecar's presence would
// change this count and fail the assertion — a real regression gate,
// just measured in syscalls rather than nanoseconds.
func TestNoRegression_ProcessAccessCountUnaffectedByIndexPresence(t *testing.T) {
	ctx := context.Background()
	baseFs := afero.NewMemMapFs()
	const path = "regression_sidecar.pulse"
	if err := afero.WriteFile(baseFs, path, buildSidecarBenchCohort(t, 500), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mkReq := func() *Request {
		return &Request{
			Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "amount", Label: "sum_amount"},
				{Type: types.AGG_COUNT, Field: "amount", Label: "n"},
			},
		}
	}

	recFs := newAccessRecordingFs(baseFs)
	p, err := New(Options{FS: recFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recFs.Reset()
	if _, err := p.Process(ctx, mkReq()); err != nil {
		t.Fatalf("Process (before index): %v", err)
	}
	before := recFs.Accessed()
	if len(before) == 0 {
		t.Fatalf("positive control failed: no accesses recorded before index build")
	}

	if _, err := p.BuildIndex(ctx, path, []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	recFs.Reset()
	if _, err := p.Process(ctx, mkReq()); err != nil {
		t.Fatalf("Process (after index): %v", err)
	}
	after := recFs.Accessed()

	if len(before) != len(after) {
		t.Fatalf("Process filesystem access count changed after sidecar index build: before=%d after=%d\nbefore=%v\nafter=%v",
			len(before), len(after), before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("Process access[%d] changed after sidecar index build: before=%q after=%q", i, before[i], after[i])
		}
	}
}
