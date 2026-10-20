package service

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// BenchmarkGroupedSumScale (U51 E2-S3): the real grouped engine path for
// AGG_SUM + AGG_AVERAGE at ~100k and ~800k groups — GROUP_ROUNDED with
// interval 1 over a u32 key, so every distinct key is a group — serial
// (DecodeWorkers 1) and fanned out (DecodeWorkers 8). It prices the
// binned reproducible accumulator against the naive += it replaced
// where it costs most: one accumulator state per group, the group map
// larger than the cache. Figures in docs/internals/BENCHMARKS_BASELINE.md.
// Never run in CI.
func BenchmarkGroupedSumScale(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	const rows = 4_000_000
	for _, groups := range []int{100_000, 800_000} {
		path := groupedSumCohort(b, dir, rows, groups)
		mk := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path},
				Groups: []*types.Group{{Type: types.GROUP_ROUNDED, Field: "g", Interval: 1}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "x", Label: "s"},
					{Type: types.AGG_AVERAGE, Field: "y", Label: "a"},
				}}
		}
		for _, workers := range []int{1, 8} {
			b.Run(fmt.Sprintf("groups=%dk/decode=%d", groups/1000, workers), func(b *testing.B) {
				svc := New(cfg)
				svc.SetProjectBufferedFields(true)
				svc.SetDecodeWorkers(workers)
				runMatrixScale(b, svc, true, mk)
			})
		}
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
	row := make([]byte, 20)
	for r := uint64(0); err == nil && r < uint64(rows); r++ {
		h := matrixScaleHash(r, 0)
		binary.LittleEndian.PutUint32(row, uint32(h%uint64(groups)))
		x := math.Ldexp(matrixScaleUnit(matrixScaleHash(r, 1)), int(h>>59)%20)
		y := math.Ldexp(matrixScaleUnit(matrixScaleHash(r, 2)), int(h>>54)%20)
		binary.LittleEndian.PutUint64(row[4:], math.Float64bits(x))
		binary.LittleEndian.PutUint64(row[12:], math.Float64bits(y))
		_, err = bw.Write(row)
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
