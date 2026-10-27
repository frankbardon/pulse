package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Response.Components.Run.NullRecords is one figure on every arm. The
// serial arms' rule (internal/processing/run_components.go): an
// UNGROUPED run reports the FIRST aggregation slot's floor n_null —
// for a vector aggregator (no Field) the rows whose every member is
// null; a grouped run (or one with no aggregation) scans the primary
// field, the first aggregation that names a Field, else the first
// grouper's. The parallel reducers used to resolve a primary FIELD for
// the ungrouped run too, which a vector slot does not name, so a
// vector-first request reported 0 (or a LATER slot's field's nulls)
// under DecodeWorkers / ShardWorkers while serial reported the slot's
// n_null.

// runNullSchema: a, b (the vector members), c (a later scalar slot) and
// g (the grouper) are all nullable, each on its own cadence, so every
// candidate figure differs.
func runNullSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1, Nullable: true},
		{Name: "c", Type: encoding.FieldTypeF64, ByteOffset: 16, CsvColumnIdx: 2, Nullable: true},
		{Name: "g", Type: encoding.FieldTypeU32, ByteOffset: 24, CsvColumnIdx: 3, Nullable: true},
	}}
}

// runNullNull: a null every 3rd row, b every 5th, c every 2nd, g every
// 7th (global row ordinal).
func runNullNull(r, f int) bool {
	return r%[]int{3, 5, 2, 7}[f] == 0
}

func runNullPayload(t testing.TB, from, to int) []byte {
	t.Helper()
	recs := make([][]uint64, 0, to-from)
	for r := from; r < to; r++ {
		recs = append(recs, []uint64{
			math.Float64bits(float64(r) * 0.5),
			math.Float64bits(float64(r) * 0.25),
			math.Float64bits(float64(r)),
			uint64(r % 4),
		})
	}
	return writeNullablePulse(t, runNullSchema(), recs, func(i, f int) bool { return runNullNull(from+i, f) })
}

// runNullArchive splits rows [0, total) into shards of the given sizes.
func runNullArchive(t testing.TB, sizes []int) []byte {
	t.Helper()
	var doc, buf bytes.Buffer
	total := 0
	for _, n := range sizes {
		total += n
	}
	if err := encx.WriteSchemaDoc(&doc, runNullSchema(), uint64(total), uint16(len(sizes))); err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	from := 0
	for i, n := range sizes {
		write(fmt.Sprintf("s%d.pulse", i), runNullPayload(t, from, from+n))
		from += n
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// runNullCount counts rows of [0, n) for which null(r) holds.
func runNullCount(n int, null func(r int) bool) int64 {
	var k int64
	for r := 0; r < n; r++ {
		if null(r) {
			k++
		}
	}
	return k
}

func TestRunNullRecords_ArmParity(t *testing.T) {
	t.Parallel()

	const rows = 120
	ctx := context.Background()
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	if err := afero.WriteFile(osFs, filepath.Join(dir, "c.pulse"), runNullPayload(t, 0, rows), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(osFs, filepath.Join(dir, "a.pulse"), runNullArchive(t, []int{50, 0, 1, 69}), 0o644); err != nil {
		t.Fatal(err)
	}
	cPath, aPath := filepath.Join(dir, "c.pulse"), filepath.Join(dir, "a.pulse")
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	vec := func() *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"a", "b"}, Label: "m"}
	}
	sum := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_SUM, Field: "c", Label: "s"} }
	vecNull := runNullCount(rows, func(r int) bool { return runNullNull(r, 0) && runNullNull(r, 1) })
	cNull := runNullCount(rows, func(r int) bool { return runNullNull(r, 2) })
	cases := []struct {
		name    string
		aggs    func() []*types.Aggregation
		grouped bool
		want    int64
	}{
		{"vector_only", func() []*types.Aggregation { return []*types.Aggregation{vec()} }, false, vecNull},
		{"vector_first", func() []*types.Aggregation { return []*types.Aggregation{vec(), sum()} }, false, vecNull},
		{"scalar_first", func() []*types.Aggregation { return []*types.Aggregation{sum(), vec()} }, false, cNull},
		// Grouped: the primary field, the first aggregation naming one.
		{"vector_first_grouped", func() []*types.Aggregation { return []*types.Aggregation{vec(), sum()} }, true, cNull},
	}
	if vecNull == 0 || vecNull == cNull {
		t.Fatalf("fixture does not separate the figures: vector %d, c %d", vecNull, cNull)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build := func(cohort string, extra ...*types.Aggregation) *types.Request {
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: append(tc.aggs(), extra...)}
				if tc.grouped {
					req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
				}
				return req
			}
			check := func(arm string, resp *types.Response) {
				t.Helper()
				if resp.Components == nil || resp.Components.Run == nil {
					t.Fatalf("%s: no Components.Run", arm)
				}
				if got := resp.Components.Run.NullRecords; got != tc.want {
					t.Errorf("%s: Run.NullRecords = %d, want %d", arm, got, tc.want)
				}
			}
			process := func(svc *Service, req *types.Request) *types.Response {
				t.Helper()
				resp, err := svc.Process(ctx, req)
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				return resp
			}
			serial := New(cfg)
			serial.SetDecodeWorkers(1)
			serial.SetShardWorkers(1)
			check("serial_streaming", process(serial, build(cPath)))
			check("serial_archive", process(serial, build(aPath)))

			// The buffered arm (a non-streamable slot appended), with the
			// aggregation components computed and skipped.
			median := &types.Aggregation{Type: types.AGG_MEDIAN, Field: "c", Label: "med"}
			check("serial_buffered", process(serial, build(cPath, median)))
			skip := build(cPath, median)
			skip.Return = &types.Return{Exclude: []string{"components.aggregations"}}
			check("serial_buffered_skip_aggs", process(serial, skip))

			// Parallel decode: the segment reducer, driven directly
			// (the cohort is below the decode threshold).
			schema := runNullSchema()
			path := cPath
			pctx, cleanup, ok, err := buildParallelDecodeContext(serial, path, schema, nil, nil, len(schema.Fields))
			if err != nil || !ok {
				t.Fatalf("buildParallelDecodeContext: %v (available %v)", err, ok)
			}
			defer func() { _ = cleanup() }()
			for _, workers := range []int{2, 3} {
				resp, err := New(cfg).reduceParallelBuffered(ctx, build(path), schema, pctx, workers)
				if err != nil {
					t.Fatalf("reduceParallelBuffered(%d): %v", workers, err)
				}
				check(fmt.Sprintf("parallel_decode_%d", workers), resp)
			}

			// Shard reducer.
			for _, workers := range []int{2, 3} {
				svc := New(cfg)
				svc.SetShardWorkers(workers)
				cohort, err := svc.Open(ctx, aPath)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := svc.shouldFanOut(build(aPath), cohort); !ok {
					t.Fatal("shouldFanOut refused: this arm would run serially")
				}
				check(fmt.Sprintf("shard_%d", workers), process(svc, build(aPath)))
			}
		})
	}
}
