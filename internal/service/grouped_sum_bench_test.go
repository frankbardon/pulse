package service

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// BenchmarkGroupedSumScale (U51 E2-S3, E3-S3): the real grouped engine
// path for AGG_SUM + AGG_AVERAGE at ~100k and ~800k groups —
// GROUP_ROUNDED with interval 1 over a u32 key, so every distinct key is
// a group. E2-S3 priced the binned reproducible accumulator against the
// naive += it replaced on the serial arm (DecodeWorkers 1); since E3-S3
// GROUP_ROUNDED merges, so the same request also runs fanned out —
// DecodeWorkers 2 / 8 on the single file, ShardWorkers 1 / 8 on an
// 8-shard archive of the same rows — and the bench prices the binned
// per-group merge end to end. Each sub-bench first asserts the arm it
// claims (a request that silently stayed serial would time the wrong
// thing). Figures in docs/internals/BENCHMARKS_BASELINE.md. Never run in CI.
func BenchmarkGroupedSumScale(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	const rows = 4_000_000
	for _, groups := range []int{100_000, 800_000} {
		path := groupedSumCohort(b, dir, rows, groups)
		archive := groupedSumArchive(b, dir, rows, groups, 8)
		mk := func(p string) func() *types.Request {
			return func() *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: p},
					Groups: []*types.Group{{Type: types.GROUP_ROUNDED, Field: "g", Interval: 1}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "x", Label: "s"},
						{Type: types.AGG_AVERAGE, Field: "y", Label: "a"},
					}}
			}
		}
		for _, workers := range []int{1, 2, 8} {
			b.Run(fmt.Sprintf("groups=%dk/decode=%d", groups/1000, workers), func(b *testing.B) {
				svc := New(cfg)
				svc.SetProjectBufferedFields(true)
				svc.SetDecodeWorkers(workers)
				groupedSumAssertDecodeArm(b, svc, path, mk(path)(), workers)
				runMatrixScale(b, svc, true, mk(path))
			})
		}
		for _, workers := range []int{1, 8} {
			b.Run(fmt.Sprintf("groups=%dk/shards=%d", groups/1000, workers), func(b *testing.B) {
				svc := New(cfg)
				svc.SetProjectBufferedFields(true)
				svc.SetShardWorkers(workers)
				svc.SetDecodeWorkers(1)
				cohort, err := svc.Open(context.Background(), archive)
				if err != nil {
					b.Fatal(err)
				}
				if _, fans := svc.shouldFanOut(mk(archive)(), cohort); fans != (workers > 1) {
					b.Fatalf("ShardWorkers %d: fan-out %v", workers, fans)
				}
				runMatrixScale(b, svc, false, mk(archive))
			})
		}
	}
}

// groupedSumAssertDecodeArm fails the bench unless the single-file run
// takes the parallel-decode arm exactly when workers > 1.
func groupedSumAssertDecodeArm(b *testing.B, svc *Service, path string, req *types.Request, workers int) {
	b.Helper()
	ctx := context.Background()
	cohort, err := svc.Open(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	count, err := svc.CountRecords(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	eligible, _ := svc.canParallelDecode(req, cohort.Schema(), cohort, workers, int(count))
	_, fans := shouldFanOutDecode(workers, int(count))
	if eligible && fans != (workers > 1) {
		b.Fatalf("DecodeWorkers %d: parallel decode %v", workers, eligible && fans)
	}
	if !eligible && workers > 1 {
		b.Fatalf("DecodeWorkers %d: request not eligible for parallel decode", workers)
	}
}

// groupedSumSchema: a u32 group key g, then two f64 measures x and y.
func groupedSumSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 12, CsvColumnIdx: 2},
	}}
}

// groupedSumCohort returns the cached single-file cohort of rows rows
// whose key spreads over groups values; measures are non-dyadic and of
// mixed magnitude (a hash-scaled unit times 2^k, k in [0, 20)).
func groupedSumCohort(tb testing.TB, dir string, rows, groups int) string {
	tb.Helper()
	path := filepath.Join(dir, fmt.Sprintf("grouped_sum_v1_r%d_g%d.pulse", rows, groups))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer os.Remove(tmp.Name())
	bw := bufio.NewWriterSize(tmp, 4<<20)
	schema := groupedSumSchema()
	if err = encoding.WriteHeader(bw); err == nil {
		err = encoding.WriteSchema(bw, schema)
	}
	if err == nil {
		err = writeGroupedSumRows(bw, 0, uint64(rows), groups)
	}
	if err == nil {
		err = bw.Flush()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err != nil {
		tb.Fatalf("generate %s: %v", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		tb.Fatalf("rename %s: %v", path, err)
	}
	return path
}

// writeGroupedSumRows writes rows [from, from+n) of the grouped-sum
// generator (a function of the row index alone, so a shard split
// reproduces the single file's rows exactly).
func writeGroupedSumRows(w io.Writer, from, n uint64, groups int) error {
	row := make([]byte, 20)
	for r := from; r < from+n; r++ {
		h := matrixScaleHash(r, 0)
		binary.LittleEndian.PutUint32(row, uint32(h%uint64(groups)))
		x := math.Ldexp(matrixScaleUnit(matrixScaleHash(r, 1)), int(h>>59)%20)
		y := math.Ldexp(matrixScaleUnit(matrixScaleHash(r, 2)), int(h>>54)%20)
		binary.LittleEndian.PutUint64(row[4:], math.Float64bits(x))
		binary.LittleEndian.PutUint64(row[12:], math.Float64bits(y))
		if _, err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// groupedSumArchive returns the cached store-only shard archive of the
// same rows as groupedSumCohort, split into shards contiguous runs.
func groupedSumArchive(tb testing.TB, dir string, rows, groups, shards int) string {
	tb.Helper()
	path := filepath.Join(dir, fmt.Sprintf("grouped_sum_v1_r%d_g%d_s%d.pulse", rows, groups, shards))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer os.Remove(tmp.Name())
	bw := bufio.NewWriterSize(tmp, 4<<20)
	schema := groupedSumSchema()
	zw := zip.NewWriter(bw)
	entry := func(name string) (*bufio.Writer, error) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		return bufio.NewWriterSize(w, 4<<20), nil
	}
	var w *bufio.Writer
	if w, err = entry(encx.ReservedSchemaName); err == nil {
		if err = encx.WriteSchemaDoc(w, schema, uint64(rows), uint16(shards)); err == nil {
			err = w.Flush()
		}
	}
	from := uint64(0)
	for i := 0; err == nil && i < shards; i++ {
		n := uint64(rows / shards)
		if i < rows%shards {
			n++
		}
		if w, err = entry(fmt.Sprintf("s%02d.pulse", i)); err != nil {
			break
		}
		if err = encoding.WriteHeader(w); err == nil {
			err = encoding.WriteSchema(w, schema)
		}
		if err == nil {
			err = writeGroupedSumRows(w, from, n, groups)
		}
		if err == nil {
			err = w.Flush()
		}
		from += n
	}
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = bw.Flush()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err != nil {
		tb.Fatalf("generate %s: %v", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		tb.Fatalf("rename %s: %v", path, err)
	}
	return path
}
