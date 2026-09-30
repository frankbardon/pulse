package service

import (
	"runtime"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// Positional-Record benchmarks on the bench-scale join-shape cohort,
// generated from the committed schema + seed (join_shape_fixture_test.go)
// on first use inside a -bench run — a unit test never builds it.
//
// Both benchmarks run the production positional path AND the in-test
// map-backed baseline over the same bytes in the same process, report
// both, and FAIL (b.Fatalf) when the ratio between them falls past its
// threshold. The ratio, not either absolute figure, is the claim: it is
// reproducible on synthetic data and on any machine, where milliseconds
// and bytes are not.
//
//   - BenchmarkJoinShape_BufferedPeakHeap: peak live heap per buffered
//     record, sampled (measurePeakHeap, the reportPeakHeap idiom) while
//     the cohort materialises. Peak heap, never B/op: B/op is cumulative
//     and cannot tell held records from transient decode garbage.
//   - BenchmarkJoinShape_DecodeThroughput: ns per field per row, buffered
//     and reuse arms. Each arm is timed several times, interleaved with
//     its baseline, and the fastest run of each is compared, so a noisy
//     neighbour inflates both sides or neither.
//
// The unit-test gates on the committed fixture (join_shape_gate_test.go)
// cover the same properties deterministically in `make test`.

// joinShapeBenchParents sizes the bench cohort: 8,000 parents x 12.5 =
// 100,000 rows, ~34 MB on disk. The map-backed baseline holds ~450 MB
// live at full decode, which bounds how large this can go.
const joinShapeBenchParents = 8_000

// joinShapeBenchCohort generates the bench cohort into a per-benchmark
// temp directory on the OS filesystem. A real file, not MemMapFs, on
// purpose: both arms then mmap it off-heap exactly as production does
// for an on-disk cohort, so the peak-heap figures hold records only, not
// a heap copy of the file that would dilute both sides of the ratio.
func joinShapeBenchCohort(b *testing.B) (afero.Fs, string, *encoding.Schema, int) {
	b.Helper()
	schema, data, err := buildJoinShapeCohort(joinShapeBenchParents)
	if err != nil {
		b.Fatalf("generate bench cohort: %v", err)
	}
	dir := b.TempDir()
	cfg, err := fs.New(fs.WithDataDir(dir))
	if err != nil {
		b.Fatalf("fs: %v", err)
	}
	if err := afero.WriteFile(cfg.Fs(), "join_shape_bench.pulse", data, 0o644); err != nil {
		b.Fatalf("write bench cohort: %v", err)
	}
	return cfg.Fs(), "join_shape_bench.pulse", schema, joinShapeRows(joinShapeBenchParents)
}

// BenchmarkJoinShape_BufferedPeakHeap asserts peak heap per buffered
// record, positional / map-backed, at or below maxRatio.
func BenchmarkJoinShape_BufferedPeakHeap(b *testing.B) {
	fsys, path, schema, rows := joinShapeBenchCohort(b)
	for _, tc := range []struct {
		name  string
		keep  encoding.FieldFilter
		keepN int
		// maxRatio: ~40% headroom over the measured ratio (arm64,
		// go1.26, 100K rows).
		maxRatio float64
	}{
		// MEASURED full: positional 1029 B, map 4301 B, ratio 0.239.
		{name: "full", maxRatio: 0.35},
		// MEASURED projected4: positional 142 B, map 412 B, ratio 0.345.
		{name: "projected4", keep: joinShapeKeep4, keepN: 4, maxRatio: 0.50},
	} {
		b.Run(tc.name, func(b *testing.B) {
			posPeak, mapPeak := -1.0, -1.0
			for b.Loop() {
				p, err := measurePeakHeap(func() error {
					recs, err := drainPositionalBuffered(fsys, path, schema, tc.keep, tc.keepN, rows)
					if err == nil && len(recs) != rows {
						b.Fatalf("positional decoded %d rows, want %d", len(recs), rows)
					}
					return err
				})
				if err != nil {
					b.Fatalf("positional: %v", err)
				}
				m, err := measurePeakHeap(func() error {
					recs, err := drainLegacyBuffered(fsys, path, schema, tc.keep, tc.keepN, rows)
					if err == nil && len(recs) != rows {
						b.Fatalf("map decoded %d rows, want %d", len(recs), rows)
					}
					return err
				})
				if err != nil {
					b.Fatalf("map: %v", err)
				}
				if posPeak < 0 || p < posPeak {
					posPeak = p
				}
				if mapPeak < 0 || m < mapPeak {
					mapPeak = m
				}
			}
			ratio := posPeak / mapPeak
			b.ReportMetric(posPeak/float64(rows), "positional-peak-B/record")
			b.ReportMetric(mapPeak/float64(rows), "map-peak-B/record")
			b.ReportMetric(ratio, "peak-ratio")
			if !(ratio <= tc.maxRatio) {
				b.Fatalf("peak heap per buffered record: positional %.0f B vs map %.0f B, ratio %.3f exceeds %.2f",
					posPeak/float64(rows), mapPeak/float64(rows), ratio, tc.maxRatio)
			}
		})
	}
}

// timedRun times one run of fn, in ns, after a
// forced GC before each so the previous run's garbage is not billed to
// this one.
func timedRun(b *testing.B, fn func() (int, error), wantRows int) float64 {
	b.Helper()
	runtime.GC()
	start := time.Now()
	n, err := fn()
	d := time.Since(start)
	if err != nil {
		b.Fatalf("decode: %v", err)
	}
	if n != wantRows {
		b.Fatalf("decoded %d rows, want %d", n, wantRows)
	}
	return float64(d.Nanoseconds())
}

// BenchmarkJoinShape_DecodeThroughput asserts decode speed per field per
// row, map-backed / positional, at or above minSpeedup.
func BenchmarkJoinShape_DecodeThroughput(b *testing.B) {
	fsys, path, schema, rows := joinShapeBenchCohort(b)
	buffered := func(keep encoding.FieldFilter, keepN int, legacy bool) func() (int, error) {
		return func() (int, error) {
			if legacy {
				recs, err := drainLegacyBuffered(fsys, path, schema, keep, keepN, rows)
				return len(recs), err
			}
			recs, err := drainPositionalBuffered(fsys, path, schema, keep, keepN, rows)
			return len(recs), err
		}
	}
	for _, tc := range []struct {
		name       string
		fields     int // fields decoded per row
		positional func() (int, error)
		legacy     func() (int, error)
		// minSpeedup: roughly 60-75% of the measured speedup (arm64,
		// go1.26, 100K rows) — wall clock, so wider headroom than the
		// heap ratios.
		minSpeedup float64
	}{
		// MEASURED buffered-full: positional 10.2, map 43 ns/field/row, 4.2x.
		{name: "buffered-full", fields: len(schema.Fields), positional: buffered(nil, 0, false), legacy: buffered(nil, 0, true), minSpeedup: 2.5},
		// MEASURED buffered-projected4: positional 48.5, map 84 ns/field/row, 1.73x.
		{name: "buffered-projected4", fields: 4, positional: buffered(joinShapeKeep4, 4, false), legacy: buffered(joinShapeKeep4, 4, true), minSpeedup: 1.3},
		// MEASURED reuse-full: positional 8.8, map 19.8 ns/field/row, 2.25x
		// (E1). Since E2-S1 the positional arm includes run-skip on this
		// SORTED cohort: 5.8 vs 18.6, 3.2x (arm64).
		//
		// CAVEAT: both arms open a fresh mmap per scan, as the production
		// streamingIterator does, and first touch page-faults the file in.
		// A CPU profile of this arm puts ~73% of samples in
		// runtime.memmove under bytes.(*Reader).Read — the fault cost of
		// copying each row out of the fresh mapping, identical for both
		// arms — so the ratio here is DILUTED and understates the decode
		// difference. Kept as-is (it is the production open path and the
		// E1 thresholds were set on it); for decode CPU alone, use the
		// in-memory BenchmarkJoinShape_RunSkip.
		{
			name:       "reuse-full",
			fields:     len(schema.Fields),
			positional: func() (int, error) { return scanPositionalReuse(fsys, path, schema, rows) },
			legacy:     func() (int, error) { return scanLegacyReuse(fsys, path, schema, rows) },
			minSpeedup: 1.6,
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			const reps = 3
			pos, legacy := -1.0, -1.0
			for b.Loop() {
				for range reps {
					if d := timedRun(b, tc.positional, rows); pos < 0 || d < pos {
						pos = d
					}
					if d := timedRun(b, tc.legacy, rows); legacy < 0 || d < legacy {
						legacy = d
					}
				}
			}
			cells := float64(rows * tc.fields)
			speedup := legacy / pos
			b.ReportMetric(pos/cells, "positional-ns/field/row")
			b.ReportMetric(legacy/cells, "map-ns/field/row")
			b.ReportMetric(speedup, "speedup")
			if !(speedup >= tc.minSpeedup) {
				b.Fatalf("decode %s: positional %.2f ns/field/row vs map %.2f: speedup %.2fx below %.2fx",
					tc.name, pos/cells, legacy/cells, speedup, tc.minSpeedup)
			}
		})
	}
}
