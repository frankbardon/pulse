package service

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// Record-footprint benchmarks for positional Record storage.
//
// The buffered arm holds every decoded *processing.Record live until the
// pass ends, so the per-record RESIDENT size — not B/op, which is
// cumulative and counts transient decode garbage too — is what decides
// whether a cohort fits in memory. Two memory figures are reported per
// buffered record:
//
//   - retained-B/record: live heap after a forced GC with the []*Record
//     slice still reachable, divided by the record count. Deterministic;
//     this is the steady-state cost of holding the cohort.
//   - peak-B/record: the sampled peak HeapAlloc during materialisation
//     (reportPeakHeap's idiom), which adds whatever transient decode
//     garbage was live at the high-water mark.
//
// Plus decode throughput for both the buffered (fresh record per row)
// and the streaming reuse arm. Fixture is fully synthetic
// (buildWideCohort): 95 mixed-type fields including decimal128, a set,
// packed bools and a nullable categorical.

const (
	footprintFields = 95
	footprintRows   = 200_000
)

func writeFootprintCohort(b *testing.B) (afero.Fs, string, *encoding.Schema) {
	b.Helper()
	schema, payload := buildWideCohort(b, footprintFields, footprintRows)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "/footprint.pulse", buf.Bytes(), 0o644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	return mem, "/footprint.pulse", schema
}

func materializeFootprint(fs afero.Fs, path string, schema *encoding.Schema) ([]*processing.Record, error) {
	return materializeFootprintKeep(fs, path, schema, nil)
}

// footprintKeep4 retains four fields of the fixture — the shape of a
// typical projected buffered request (two group keys, a filter, a cell).
func footprintKeep4(name string) bool {
	switch name {
	case "brand", "waveDate", "cardFeeling", "weight":
		return true
	}
	return false
}

func materializeFootprintKeep(fs afero.Fs, path string, schema *encoding.Schema, keep encoding.FieldFilter) ([]*processing.Record, error) {
	it := newStreamingIterator(fs, path, schema)
	defer it.Close()
	if keep != nil {
		it.SetProjection(keep, 4)
	}
	out := make([]*processing.Record, 0, footprintRows)
	for it.Next() {
		out = append(out, it.Record())
	}
	return out, it.Err()
}

// BenchmarkBufferedRecordFootprint measures the resident size of one
// buffered record and the buffered decode rate.
func BenchmarkBufferedRecordFootprint(b *testing.B) {
	fs, path, schema := writeFootprintCohort(b)
	b.Logf("stride=%d bytes, fields=%d, rows=%d", schema.RecordByteSize(), len(schema.Fields), footprintRows)

	b.Run("retained", func(b *testing.B) {
		var perRec float64
		for b.Loop() {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			recs, err := materializeFootprint(fs, path, schema)
			if err != nil {
				b.Fatalf("materialize: %v", err)
			}
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			perRec = float64(after.HeapAlloc-before.HeapAlloc) / float64(len(recs))
			runtime.KeepAlive(recs)
		}
		b.ReportMetric(perRec, "retained-B/record")
	})

	b.Run("retained-projected4", func(b *testing.B) {
		var perRec float64
		for b.Loop() {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			recs, err := materializeFootprintKeep(fs, path, schema, footprintKeep4)
			if err != nil {
				b.Fatalf("materialize: %v", err)
			}
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			perRec = float64(after.HeapAlloc-before.HeapAlloc) / float64(len(recs))
			runtime.KeepAlive(recs)
		}
		b.ReportMetric(perRec, "retained-B/record")
	})

	b.Run("peak", func(b *testing.B) {
		var recs []*processing.Record
		start := time.Now()
		reportPeakHeap(b, func() error {
			var err error
			recs, err = materializeFootprint(fs, path, schema)
			return err
		})
		elapsed := time.Since(start)
		// reportPeakHeap reports MB; restate per record for the story.
		b.ReportMetric(float64(elapsed.Nanoseconds())/float64(len(recs)), "ns/record")
		runtime.KeepAlive(recs)
	})

	b.Run("decode-buffered", func(b *testing.B) {
		b.ReportAllocs()
		var n int
		for b.Loop() {
			recs, err := materializeFootprint(fs, path, schema)
			if err != nil {
				b.Fatalf("materialize: %v", err)
			}
			n = len(recs)
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/record")
	})

	b.Run("decode-buffered-projected4", func(b *testing.B) {
		b.ReportAllocs()
		var n int
		for b.Loop() {
			recs, err := materializeFootprintKeep(fs, path, schema, footprintKeep4)
			if err != nil {
				b.Fatalf("materialize: %v", err)
			}
			n = len(recs)
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/record")
	})

	b.Run("decode-reuse", func(b *testing.B) {
		b.ReportAllocs()
		var n int
		for b.Loop() {
			it := newStreamingIterator(fs, path, schema)
			it.SetReuse(true)
			n = 0
			for it.Next() {
				n++
			}
			if err := it.Err(); err != nil {
				b.Fatalf("scan: %v", err)
			}
			it.Close()
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/record")
	})
}
