package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Benchmark suite for evaluating mmap/arena perf improvements (Improvement 12).
//
// Captures three orthogonal axes:
//   - Raw scan throughput (iter.Next loop, no aggregation work)
//   - Streaming aggregation (Process with online aggs)
//   - File shape: {1 numeric field} vs {5 numeric + 2 categorical}
//   - Row count: 10K / 100K / 1M
//
// All metrics: ns/op, B/op, allocs/op. Compare deltas vs BENCHMARKS_BASELINE.md
// after each layer (arena, mmap, zero-copy cat) lands.

// scanShape selects which schema/data layout the benchmark uses.
type scanShape int

const (
	shape1Num scanShape = iota
	shape5Num2Cat
)

func (s scanShape) String() string {
	switch s {
	case shape1Num:
		return "1num"
	case shape5Num2Cat:
		return "5num_2cat"
	default:
		return "unknown"
	}
}

// buildBenchSchema returns a schema + writer that produces nRows records of
// the chosen shape. Categorical fields use a 16-entry dictionary so categorical_u8
// is exercised end-to-end (dict resolve on read).
func buildBenchSchema(shape scanShape) (*encoding.Schema, func(*bytes.Buffer, int)) {
	switch shape {
	case shape1Num:
		schema := &encoding.Schema{
			Fields: []encoding.Field{
				{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
			},
		}
		writeRow := func(buf *bytes.Buffer, i int) {
			bits := float64BitsFor(i)
			encoding.WriteFieldValue(buf, encoding.FieldTypeF64, bits)
		}
		return schema, func(buf *bytes.Buffer, n int) {
			for i := range n {
				writeRow(buf, i)
			}
		}
	case shape5Num2Cat:
		// Build categorical dictionaries up front so they are written into the schema.
		dict0 := encoding.NewDictionary()
		dict1 := encoding.NewDictionary()
		for i := range 16 {
			_, _ = dict0.Add(fmt.Sprintf("cat0_%d", i))
			_, _ = dict1.Add(fmt.Sprintf("cat1_%d", i))
		}
		schema := &encoding.Schema{
			Fields: []encoding.Field{
				{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
				{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
				{Name: "c", Type: encoding.FieldTypeU32, ByteOffset: 16, CsvColumnIdx: 2},
				{Name: "d", Type: encoding.FieldTypeU16, ByteOffset: 20, CsvColumnIdx: 3},
				{Name: "e", Type: encoding.FieldTypeF32, ByteOffset: 22, CsvColumnIdx: 4},
				{Name: "cat0", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 26, CsvColumnIdx: 5, Dictionary: dict0},
				{Name: "cat1", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 27, CsvColumnIdx: 6, Dictionary: dict1},
			},
		}
		return schema, func(buf *bytes.Buffer, n int) {
			for i := range n {
				encoding.WriteFieldValue(buf, encoding.FieldTypeF64, float64BitsFor(i))
				encoding.WriteFieldValue(buf, encoding.FieldTypeF64, float64BitsFor(i*3))
				encoding.WriteFieldValue(buf, encoding.FieldTypeU32, uint64(i*7))
				encoding.WriteFieldValue(buf, encoding.FieldTypeU16, uint64(i%4096))
				encoding.WriteFieldValue(buf, encoding.FieldTypeF32, float32BitsFor(i))
				encoding.WriteFieldValue(buf, encoding.FieldTypeCategoricalU8, uint64(i%16))
				encoding.WriteFieldValue(buf, encoding.FieldTypeCategoricalU8, uint64((i*5)%16))
			}
		}
	}
	panic("unknown shape")
}

func float64BitsFor(i int) uint64 {
	// Deterministic pseudo-random float across i to avoid trivial constant folding.
	v := float64((i*37)%1009) + 0.5
	return math.Float64bits(v)
}

func float32BitsFor(i int) uint64 {
	v := float32((i*13)%509) + 0.25
	return uint64(math.Float32bits(v))
}

// writeBenchPulseFile materializes nRows records of the given shape into memFs at path.
func writeBenchPulseFile(memFs afero.Fs, path string, shape scanShape, nRows int) (*encoding.Schema, error) {
	schema, writeAll := buildBenchSchema(shape)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		return nil, err
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		return nil, err
	}
	writeAll(&buf, nRows)
	if err := afero.WriteFile(memFs, path, buf.Bytes(), 0644); err != nil {
		return nil, err
	}
	return schema, nil
}

// BenchmarkScan_Iterator measures pure iteration cost: open + read every
// record, no aggregator state. Isolates encoding/decoding + per-record map
// allocation overhead from any processing work.
func BenchmarkScan_Iterator(b *testing.B) {
	for _, shape := range []scanShape{shape1Num, shape5Num2Cat} {
		for _, n := range []int{10_000, 100_000, 1_000_000} {
			b.Run(fmt.Sprintf("shape=%s/rows=%d", shape, n), func(b *testing.B) {
				memFs := afero.NewMemMapFs()
				schema, err := writeBenchPulseFile(memFs, "bench.pulse", shape, n)
				if err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					iter := newStreamingIterator(memFs, "bench.pulse", schema)
					count := 0
					for iter.Next() {
						count++
					}
					iter.Close()
					if count != n {
						b.Fatalf("read %d rows, want %d", count, n)
					}
				}
			})
		}
	}
}

// BenchmarkScan_IteratorReuse measures the same iterator drain as
// BenchmarkScan_Iterator but with SetReuse(true) enabled. Isolates the
// raw decode + record-reuse win from the Process orchestration cost.
func BenchmarkScan_IteratorReuse(b *testing.B) {
	for _, shape := range []scanShape{shape1Num, shape5Num2Cat} {
		for _, n := range []int{10_000, 100_000, 1_000_000} {
			b.Run(fmt.Sprintf("shape=%s/rows=%d", shape, n), func(b *testing.B) {
				memFs := afero.NewMemMapFs()
				schema, err := writeBenchPulseFile(memFs, "bench.pulse", shape, n)
				if err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					iter := newStreamingIterator(memFs, "bench.pulse", schema)
					iter.SetReuse(true)
					count := 0
					for iter.Next() {
						count++
					}
					iter.Close()
					if count != n {
						b.Fatalf("read %d rows, want %d", count, n)
					}
				}
			})
		}
	}
}

// BenchmarkProcess_StreamingSum measures the full streaming Process pipeline
// with a single AGG_SUM over the first numeric field. Exercises the
// streamingIterator + processStreaming hot path end-to-end.
func BenchmarkProcess_StreamingSum(b *testing.B) {
	for _, shape := range []scanShape{shape1Num, shape5Num2Cat} {
		for _, n := range []int{10_000, 100_000, 1_000_000} {
			b.Run(fmt.Sprintf("shape=%s/rows=%d", shape, n), func(b *testing.B) {
				memFs := afero.NewMemMapFs()
				if _, err := writeBenchPulseFile(memFs, "bench.pulse", shape, n); err != nil {
					b.Fatal(err)
				}
				cfg, _ := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
				svc := New(cfg)
				field := "v"
				if shape == shape5Num2Cat {
					field = "a"
				}
				req := &types.Request{
					Cohort: &types.Cohort{Filename: "bench.pulse"},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: field, Label: "s"},
					},
				}
				ctx := context.Background()
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					if _, err := svc.Process(ctx, req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkProcess_GroupedByCategorical exercises the only hot path in
// Pulse where the categorical dictionary is touched on every row: the
// GROUP_CATEGORY grouper resolves the field's float64 ID into a Go
// string via Dictionary.Resolve to form the bucket key. Used to
// evaluate whether Layer 3 (zero-copy categorical) is worth doing on
// top of Layers 1 + 2.
func BenchmarkProcess_GroupedByCategorical(b *testing.B) {
	for _, n := range []int{100_000, 1_000_000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			memFs := afero.NewMemMapFs()
			if _, err := writeBenchPulseFile(memFs, "bench.pulse", shape5Num2Cat, n); err != nil {
				b.Fatal(err)
			}
			cfg, _ := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
			svc := New(cfg)
			req := &types.Request{
				Cohort: &types.Cohort{Filename: "bench.pulse"},
				Groups: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "cat0"},
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "a", Label: "s"},
				},
			}
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := svc.Process(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// osFsBenchPath sets up a real OS-backed afero.OsFs in a tempdir, writes
// the bench file, and returns the fs config + the full filesystem path
// to the file. Used by the mmap-capable benchmarks below so the mmap
// path (which requires a real OS file descriptor) can fire.
func osFsBenchPath(b *testing.B, shape scanShape, nRows int) (*fs.Config, *encoding.Schema, string) {
	b.Helper()
	dir := b.TempDir()
	osFs := afero.NewOsFs()
	full := dir + "/bench.pulse"
	schema, err := writeBenchPulseFile(osFs, full, shape, nRows)
	if err != nil {
		b.Fatal(err)
	}
	cfg, _ := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	return cfg, schema, full
}

// BenchmarkScan_OsFsReuse drives the streaming iterator over a real
// on-disk file with reuse enabled. The mmap-capable path (Layer 2)
// activates only on OsFs; this bench is the apples-to-apples comparison
// point for measuring mmap deltas.
func BenchmarkScan_OsFsReuse(b *testing.B) {
	for _, shape := range []scanShape{shape1Num, shape5Num2Cat} {
		for _, n := range []int{10_000, 100_000, 1_000_000} {
			b.Run(fmt.Sprintf("shape=%s/rows=%d", shape, n), func(b *testing.B) {
				cfg, schema, name := osFsBenchPath(b, shape, n)
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					iter := newStreamingIterator(cfg.Fs(), name, schema)
					iter.SetReuse(true)
					count := 0
					for iter.Next() {
						count++
					}
					iter.Close()
					if count != n {
						b.Fatalf("read %d rows, want %d", count, n)
					}
				}
			})
		}
	}
}

// BenchmarkProcess_StreamingSum_OsFs is the OsFs variant of
// BenchmarkProcess_StreamingSum. Same Process call, but with the cohort
// file backed by a real filesystem so the mmap path can fire.
func BenchmarkProcess_StreamingSum_OsFs(b *testing.B) {
	for _, shape := range []scanShape{shape1Num, shape5Num2Cat} {
		for _, n := range []int{10_000, 100_000, 1_000_000} {
			b.Run(fmt.Sprintf("shape=%s/rows=%d", shape, n), func(b *testing.B) {
				cfg, _, name := osFsBenchPath(b, shape, n)
				svc := New(cfg)
				field := "v"
				if shape == shape5Num2Cat {
					field = "a"
				}
				req := &types.Request{
					Cohort: &types.Cohort{Filename: name},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: field, Label: "s"},
					},
				}
				ctx := context.Background()
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					if _, err := svc.Process(ctx, req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkProcess_StreamingMulti measures Process with five online
// aggregators over a single numeric field. Reveals whether per-record
// overhead dominates over aggregator work as agg count grows.
func BenchmarkProcess_StreamingMulti(b *testing.B) {
	for _, shape := range []scanShape{shape1Num, shape5Num2Cat} {
		for _, n := range []int{100_000, 1_000_000} {
			b.Run(fmt.Sprintf("shape=%s/rows=%d", shape, n), func(b *testing.B) {
				memFs := afero.NewMemMapFs()
				if _, err := writeBenchPulseFile(memFs, "bench.pulse", shape, n); err != nil {
					b.Fatal(err)
				}
				cfg, _ := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
				svc := New(cfg)
				field := "v"
				if shape == shape5Num2Cat {
					field = "a"
				}
				req := &types.Request{
					Cohort: &types.Cohort{Filename: "bench.pulse"},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: field, Label: "n"},
						{Type: types.AGG_SUM, Field: field, Label: "s"},
						{Type: types.AGG_AVERAGE, Field: field, Label: "avg"},
						{Type: types.AGG_MIN, Field: field, Label: "min"},
						{Type: types.AGG_MAX, Field: field, Label: "max"},
					},
				}
				ctx := context.Background()
				b.ResetTimer()
				b.ReportAllocs()
				for b.Loop() {
					if _, err := svc.Process(ctx, req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
