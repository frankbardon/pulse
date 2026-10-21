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

// BenchmarkGroupedSumScale (U51 E2-S3, E3-S3, E3-S4): the real grouped
// engine path for AGG_SUM + AGG_AVERAGE at 100k–800k groups over 4M
// rows — GROUP_ROUNDED with interval 1 over a u32 key, so every distinct
// key is a group. E2-S3 priced the binned reproducible accumulator
// against the naive += it replaced on the serial arm (DecodeWorkers 1);
// since E3-S3 GROUP_ROUNDED merges, so the same request also runs fanned
// out — DecodeWorkers 2 / 4 / 8 on the single file, ShardWorkers 1 / 8 on
// an 8-shard archive of the same rows — and the bench prices the binned
// per-group merge end to end.
//
// E3-S4 sweeps the cardinality to find where the fan-out stops paying,
// as a function of the share of each partition's rows that mint a key.
// The variants check the aggregator mix — "count" (AGG_COUNT alone) and
// "wide" (SUM, AVERAGE, MIN, MAX, VARIANCE: five states a key, one
// Welford) — and the key path: "cat", "catcount" and "catwide" group the
// same keys through GROUP_CATEGORY over a categorical_u32 whose
// dictionary holds every key (out to 3.2M keys, share ~0.92). Every
// grouped request fans out whatever its cardinality; each sub-bench
// first asserts the arm it claims (a request that silently stayed serial
// would time the wrong thing). Figures in
// docs/internals/BENCHMARKS_BASELINE.md. Never run in CI.
func BenchmarkGroupedSumScale(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	const rows = 4_000_000
	sumAvg := []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "x", Label: "s"},
		{Type: types.AGG_AVERAGE, Field: "y", Label: "a"},
	}
	variants := []struct {
		name   string
		cat    bool
		aggs   []*types.Aggregation
		decode []int
		shards []int
	}{
		{name: "", aggs: sumAvg, decode: []int{1, 2, 4, 8}, shards: []int{1, 8}},
		{name: "cat/", cat: true, aggs: sumAvg, decode: []int{1, 8}},
		{name: "count/", aggs: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}, decode: []int{1, 8}},
		{name: "wide/", aggs: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x", Label: "s"},
			{Type: types.AGG_AVERAGE, Field: "y", Label: "a"},
			{Type: types.AGG_MIN, Field: "x", Label: "lo"},
			{Type: types.AGG_MAX, Field: "x", Label: "hi"},
			{Type: types.AGG_VARIANCE, Field: "y", Label: "v"},
		}, decode: []int{1, 8}},
	}
	// The categorical twins of the three mixes; only they run past 800k
	// keys (where the value-keyed fan-out has already stopped paying).
	for _, v := range variants[1:] {
		if !v.cat {
			v.name, v.cat = "cat"+v.name, true
			variants = append(variants, v)
		}
	}
	for _, groups := range []int{100_000, 200_000, 300_000, 400_000, 600_000, 800_000, 1_600_000, 3_200_000} {
		for _, v := range variants {
			if groups > 800_000 && !v.cat {
				continue
			}
			path := groupedSumCohort(b, dir, rows, groups, v.cat)
			mk := func(p string) func() *types.Request {
				return func() *types.Request {
					g := &types.Group{Type: types.GROUP_ROUNDED, Field: "g", Interval: 1}
					if v.cat {
						g = &types.Group{Type: types.GROUP_CATEGORY, Field: "g"}
					}
					aggs := make([]*types.Aggregation, len(v.aggs))
					for i, a := range v.aggs {
						c := *a
						aggs[i] = &c
					}
					return &types.Request{Cohort: &types.Cohort{Filename: p},
						Groups: []*types.Group{g}, Aggregations: aggs}
				}
			}
			for _, workers := range v.decode {
				fan := workers > 1
				b.Run(fmt.Sprintf("groups=%dk/%sdecode=%d", groups/1000, v.name, workers), func(b *testing.B) {
					svc := New(cfg)
					svc.SetProjectBufferedFields(true)
					svc.SetDecodeWorkers(workers)
					groupedSumAssertDecodeArm(b, svc, path, mk(path)(), workers, fan)
					runMatrixScale(b, svc, true, mk(path))
				})
			}
			if len(v.shards) == 0 {
				continue
			}
			archive := groupedSumArchive(b, dir, rows, groups, 8)
			for _, workers := range v.shards {
				b.Run(fmt.Sprintf("groups=%dk/%sshards=%d", groups/1000, v.name, workers), func(b *testing.B) {
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
}

// groupedSumAssertDecodeArm fails the bench unless the single-file run
// takes the parallel-decode arm exactly when fan says so.
func groupedSumAssertDecodeArm(b *testing.B, svc *Service, path string, req *types.Request, workers int, fan bool) {
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
	eligible, reason := svc.canParallelDecode(req, cohort.Schema(), cohort, workers, int(count))
	_, fans := shouldFanOutDecode(workers, int(count))
	if got := eligible && fans; got != fan {
		b.Fatalf("DecodeWorkers %d: parallel decode %v (%s), want %v", workers, got, reason, fan)
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

// groupedSumCatSchema is groupedSumSchema with g a categorical_u32
// whose dictionary holds the groups keys k0000000 … in id order, so the
// row bytes (a u32 dictionary id) are the u32 cohort's.
func groupedSumCatSchema(groups int) *encoding.Schema {
	s := groupedSumSchema()
	dict := encoding.NewDictionary()
	for i := range groups {
		if _, err := dict.Add(fmt.Sprintf("k%07d", i)); err != nil {
			panic(err)
		}
	}
	s.Fields[0].Type = encoding.FieldTypeCategoricalU32
	s.Fields[0].Dictionary = dict
	return s
}

// groupedSumCohort returns the cached single-file cohort of rows rows
// whose key spreads over groups values (a categorical_u32 key when cat);
// measures are non-dyadic and of mixed magnitude (a hash-scaled unit
// times 2^k, k in [0, 20)).
func groupedSumCohort(tb testing.TB, dir string, rows, groups int, cat bool) string {
	tb.Helper()
	name := fmt.Sprintf("grouped_sum_v1_r%d_g%d.pulse", rows, groups)
	schema := groupedSumSchema()
	if cat {
		name = fmt.Sprintf("grouped_sum_cat_v1_r%d_g%d.pulse", rows, groups)
		schema = groupedSumCatSchema(groups)
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer os.Remove(tmp.Name())
	bw := bufio.NewWriterSize(tmp, 4<<20)
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
