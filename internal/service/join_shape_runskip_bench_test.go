package service

import (
	"bytes"
	"io"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// BenchmarkJoinShape_RunSkip measures run-skip on the bench-scale
// join-shape cohort (100K rows) in its generated SORTED order and in a
// SCATTERED permutation of the same rows, on the full-stride and the
// projected (plan) reuse paths, and gates the sorted win (E2-S3).
//
// Both arms decode the same IN-MEMORY payload (a bytes.Reader over the
// record region), so the figures are decode CPU only: a per-scan fresh
// mmap page-faults the whole file in and that cost, identical for both
// arms, would otherwise swamp the difference (the reason
// BenchmarkJoinShape_DecodeThroughput is not the harness for this).
//
// Two arms decode the same bytes into the same production record
// (processing.Record) through the same reader:
//
//   - run-skip: the production reuse loop;
//   - no-compare: the same loop with rec.ClearForRow() before every
//     read. That withdraws the record from run-skip, so BeginRunRow
//     refuses the partial rewrite and the reader does the full
//     clear-and-repopulate the pre-run-skip decoder did — with no
//     wrapper between decoder and record, so neither arm pays a
//     forwarding call the other does not. Its only extra work is the
//     redundant clear (a few words per row).
//
// Gated (b.Fatalf past threshold): the sorted speedup, no-compare /
// run-skip, on both shapes. MEASURED and REPORTED but deliberately NOT
// gated: the scattered ratio. That is the net-loss bound — the cost of
// comparing on data that does not repeat, capped by the adaptive
// backoff. It is accepted and quantified, not prevented; the
// deterministic structural bound (writes never exceed a full decode,
// compared rows stay under 5%) is TestJoinShapeRunSkip_WorkCounts.
//
// Continuation (the byte-level hit rate) of each ordering is asserted
// first, so a fixture-generation change that destroys the sort fails
// loudly instead of silently weakening the benchmark.
func BenchmarkJoinShape_RunSkip(b *testing.B) {
	fsys, path, schema, rows := joinShapeBenchCohort(b)
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		b.Fatal(err)
	}
	scattered := scatterJoinShapeCohort(data, schema, rows)
	// MEASURED continuation (100K rows): sorted 0.7086, scattered 0.1924.
	if c := joinShapeContinuation(data, schema, rows); c < 0.65 {
		b.Fatalf("sorted bench cohort continuation %.4f below 0.65: the parent block no longer repeats", c)
	}
	if c := joinShapeContinuation(scattered, schema, rows); c > 0.30 {
		b.Fatalf("scattered bench cohort continuation %.4f above 0.30: the scatter no longer breaks the runs", c)
	}
	payloads := map[string][]byte{
		"sorted":    data[len(data)-rows*schema.RecordByteSize():],
		"scattered": scattered[len(scattered)-rows*schema.RecordByteSize():],
	}
	scan := func(order string, keep encx.FieldFilter, plan *encx.DecodePlan, skip bool) func() (int, error) {
		return func() (int, error) {
			rr := encx.NewRecordReader(bytes.NewReader(payloads[order]), schema)
			rec := processing.NewReusableRecord(schema)
			n := 0
			for {
				if !skip {
					rec.ClearForRow()
				}
				err := rr.ReadRecordReusedWithPlan(rec, keep, plan)
				if err == io.EOF {
					return n, nil
				}
				if err != nil {
					return n, err
				}
				n++
			}
		}
	}
	projPlan, err := encx.BuildDecodePlan(schema, retainedFromFilter(schema, joinShapeKeep4))
	if err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		order string
		shape string
		keep  encx.FieldFilter
		plan  *encx.DecodePlan
		// minSpeedup gates no-compare / run-skip; 0 = report only.
		minSpeedup float64
	}{
		// MEASURED (100K rows, min of 5 reps, 6 runs), no-compare ->
		// run-skip ns/row and speedup:
		//   arm64 (M1 Max):        670-677 -> 504-514, 1.31-1.34x
		//   amd64 (Rosetta 2):     1177-1192 -> 836-847, 1.40-1.42x
		// Gate ~87% of the arm64 figure.
		{order: "sorted", shape: "full", minSpeedup: 1.15},
		// MEASURED: arm64 104.5-105.8 -> 99.7-102.2, 1.03-1.06x;
		// amd64 147.8-149.2 -> 135.6-136.5, 1.09x. Two of the four
		// retained fields change nearly every row (write fraction 0.45,
		// TestJoinShapeRunSkip_WorkCounts), so the projected win is
		// small; gated as no-regression, not as a speedup.
		{order: "sorted", shape: "projected4", keep: joinShapeKeep4, plan: projPlan, minSpeedup: 1.00},
		// NET-LOSS BOUND, reported only. MEASURED: arm64 743.6-753.7 ->
		// 757.7-765.0, 0.974-0.994x (a 0.6-2.6% loss); amd64 1231-1242
		// -> 1252-1262, 0.976-0.988x (1.2-2.4%).
		{order: "scattered", shape: "full"},
		// MEASURED: arm64 103.8-106.4 -> 102.8-103.8, 1.00-1.03x; amd64
		// 145.2-147.2 -> 141.9-144.9, 1.00-1.03x — break-even (the
		// no-compare arm's redundant clear is of the same order).
		{order: "scattered", shape: "projected4", keep: joinShapeKeep4, plan: projPlan},
	} {
		b.Run(tc.order+"/"+tc.shape, func(b *testing.B) {
			const reps = 5
			skip, full := -1.0, -1.0
			for b.Loop() {
				for range reps {
					if d := timedRun(b, scan(tc.order, tc.keep, tc.plan, true), rows); skip < 0 || d < skip {
						skip = d
					}
					if d := timedRun(b, scan(tc.order, tc.keep, tc.plan, false), rows); full < 0 || d < full {
						full = d
					}
				}
			}
			speedup := full / skip
			b.ReportMetric(skip/float64(rows), "runskip-ns/row")
			b.ReportMetric(full/float64(rows), "nocompare-ns/row")
			b.ReportMetric(speedup, "speedup")
			if tc.minSpeedup > 0 && !(speedup >= tc.minSpeedup) {
				b.Fatalf("run-skip %s/%s: %.1f ns/row vs no-compare %.1f: speedup %.3fx below %.2fx",
					tc.order, tc.shape, skip/float64(rows), full/float64(rows), speedup, tc.minSpeedup)
			}
		})
	}
}
