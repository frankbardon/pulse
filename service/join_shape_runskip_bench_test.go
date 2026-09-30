package service

import (
	"bytes"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// BenchmarkJoinShape_RunSkip measures run-skip on the bench-scale
// join-shape cohort (100K rows) in its generated SORTED order and in a
// SCATTERED permutation of the same rows, on the full-stride and the
// projected (plan) reuse paths.
//
// Both arms decode the same IN-MEMORY payload (a bytes.Reader over the
// record region), so the figures are decode CPU only: a per-scan fresh
// mmap page-faults the whole file in and that cost, identical for both
// arms, would otherwise swamp the difference.
//
// Two arms decode the same bytes through the same reader harness:
//
//   - run-skip: the production reuse record (processing.Record, which
//     implements encoding.RunSkipRecord);
//   - full: the same record behind noSkipRecord, which hides
//     RunSkipRecord so every row is cleared and fully repopulated.
//
// noSkipRecord adds one forwarding call per write, so "full" is a
// slightly pessimistic stand-in for the pre-run-skip decoder; the
// absolute run-skip ns/row is the figure to compare across commits.
// Reported, not gated: the formal ratio gates are E2-S3's.
func BenchmarkJoinShape_RunSkip(b *testing.B) {
	fsys, path, schema, rows := joinShapeBenchCohort(b)
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		b.Fatal(err)
	}
	scattered := scatterJoinShapeCohort(data, schema, rows)
	payloads := map[string][]byte{
		"sorted":    data[len(data)-rows*schema.RecordByteSize():],
		"scattered": scattered[len(scattered)-rows*schema.RecordByteSize():],
	}
	scan := func(order string, keep encoding.FieldFilter, plan *encoding.DecodePlan, skip bool) func() (int, error) {
		return func() (int, error) {
			rr := encoding.NewRecordReader(bytes.NewReader(payloads[order]), schema)
			var rec encoding.ReusableRecord = processing.NewReusableRecord(schema)
			if !skip {
				rec = newNoSkipRecord(schema)
			}
			n := 0
			for {
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
	projPlan, err := schema.BuildDecodePlan(retainedFromFilter(schema, joinShapeKeep4))
	if err != nil {
		b.Fatal(err)
	}
	for _, order := range []struct{ name, path string }{{"sorted", "sorted"}, {"scattered", "scattered"}} {
		for _, shape := range []struct {
			name string
			keep encoding.FieldFilter
			plan *encoding.DecodePlan
		}{{"full", nil, nil}, {"projected4", joinShapeKeep4, projPlan}} {
			b.Run(order.name+"/"+shape.name, func(b *testing.B) {
				const reps = 5
				skip, full := -1.0, -1.0
				for b.Loop() {
					for range reps {
						if d := timedRun(b, scan(order.path, shape.keep, shape.plan, true), rows); skip < 0 || d < skip {
							skip = d
						}
						if d := timedRun(b, scan(order.path, shape.keep, shape.plan, false), rows); full < 0 || d < full {
							full = d
						}
					}
				}
				b.ReportMetric(skip/float64(rows), "runskip-ns/row")
				b.ReportMetric(full/float64(rows), "full-ns/row")
				b.ReportMetric(full/skip, "speedup")
			})
		}
	}
}
