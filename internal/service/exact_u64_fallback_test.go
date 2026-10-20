package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// exact_u64_fallback_test.go pins #459 on the MAP-decode fallback paths:
// the record each one builds from a values map must still carry a u64
// field's exact on-wire word, so processing.Record.Uint64Value — what
// extend.Record.Uint64Value reads for every extension operator — returns
// the stored value above 2^53, never the float64 echo (2^53 and 2^53+1
// share one echo; so do MaxUint64 and MaxUint64-1). A null u64 reads
// (0, false). One test per path: the streaming and shard iterators'
// projection-without-plan decode, the parallel decoder's, the point
// lookup's record read and filter-to-file's per-row decode.

// exactU64Want is the u64 column of the fixture, row by row; nil = null.
var exactU64Want = []*uint64{
	u64p(1 << 53), u64p(1<<53 + 1), u64p(math.MaxUint64), u64p(math.MaxUint64 - 1), nil, u64p(7),
}

func u64p(v uint64) *uint64 { return &v }

// exactU64Schema: id (u32, the lookup key), k (nullable u64), a (f64).
func exactU64Schema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "k", Type: encoding.FieldTypeU64, ByteOffset: 4, Nullable: true},
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 12},
	}}
}

// exactU64Rows returns rows [from, to) of the fixture and their nulls.
func exactU64Rows(from, to int) ([][]uint64, [][]bool) {
	var recs [][]uint64
	var nulls [][]bool
	for r := from; r < to; r++ {
		var k uint64
		if w := exactU64Want[r]; w != nil {
			k = *w
		}
		recs = append(recs, []uint64{uint64(r + 1), k, math.Float64bits(float64(r) + 0.5)})
		nulls = append(nulls, []bool{false, exactU64Want[r] == nil, false})
	}
	return recs, nulls
}

func exactU64File(t *testing.T) []byte {
	t.Helper()
	recs, nulls := exactU64Rows(0, len(exactU64Want))
	return writeSinglePulseNulls(t, exactU64Schema(), recs, nulls)
}

// keepK projects to the u64 column alone.
func keepK(name string) bool { return name == "k" }

// assertExactU64 checks rec (row r of the fixture) reads k exactly.
func assertExactU64(t *testing.T, path string, r int, rec *processing.Record) {
	t.Helper()
	got, ok := rec.Uint64Value("k")
	want := exactU64Want[r]
	switch {
	case want == nil:
		if ok || got != 0 {
			t.Errorf("%s row %d: Uint64Value(k) = (%d, %v), want (0, false) for a null", path, r, got, ok)
		}
	case !ok || got != *want:
		t.Errorf("%s row %d: Uint64Value(k) = (%d, %v), want (%d, true)", path, r, got, ok, *want)
	}
}

// drainExact reads every record of iter and checks each one.
func drainExact(t *testing.T, path string, iter processing.RecordIterator) {
	t.Helper()
	r := 0
	for iter.Next() {
		if r >= len(exactU64Want) {
			t.Fatalf("%s: more than %d records", path, len(exactU64Want))
		}
		assertExactU64(t, path, r, iter.Record())
		r++
	}
	if r != len(exactU64Want) {
		t.Fatalf("%s: %d records, want %d", path, r, len(exactU64Want))
	}
}

func TestExactU64_StreamProjectionWithoutPlan(t *testing.T) {
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "c.pulse", exactU64File(t), 0o644); err != nil {
		t.Fatal(err)
	}
	it := newStreamingIterator(cfg.Fs(), "c.pulse", exactU64Schema())
	defer it.Close()
	it.SetProjection(keepK, 1)
	it.plan, it.planKey = nil, "" // force the map-decode fallback
	drainExact(t, "stream", it)
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
}

// exactU64Archive opens a two-shard archive of the fixture (rows split
// in half) on a fresh service.
func exactU64Archive(t *testing.T) (*Service, *fs.Config, *Cohort) {
	t.Helper()
	schema := exactU64Schema()
	var doc bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, schema, uint64(len(exactU64Want)), 2); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	half := len(exactU64Want) / 2
	for i, span := range [][2]int{{0, half}, {half, len(exactU64Want)}} {
		recs, nulls := exactU64Rows(span[0], span[1])
		write([]string{"s0.pulse", "s1.pulse"}[i], writeSinglePulseNulls(t, schema, recs, nulls))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(cohort.Shards()) != 2 {
		t.Fatalf("archive shards = %d, want 2", len(cohort.Shards()))
	}
	return svc, cfg, cohort
}

func TestExactU64_ShardIterProjectionWithoutPlan(t *testing.T) {
	_, cfg, cohort := exactU64Archive(t)
	it := newShardIter(cfg.Fs(), "arch.pulse", cohort.Schema(), cohort.Shards())
	defer it.Close()
	it.SetProjection(keepK, 1)
	it.plan, it.planKey = nil, "" // force the map-decode fallback
	drainExact(t, "shard_iter", it)
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestExactU64_ShardParallelProjectionWithoutPlan (U51 E2-S2): the
// per-shard parallel reducer now projects, so it carries the serial
// shard iterator's defensive projection-without-plan map decode — and
// with it the exact-word capture. Falsified by dropping the
// exact.ApplyTo call in shardRows.next.
func TestExactU64_ShardParallelProjectionWithoutPlan(t *testing.T) {
	_, cfg, cohort := exactU64Archive(t)
	arch, err := openArchiveFile(cfg.Fs(), "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	defer arch.Close()
	schema := cohort.Schema()
	// The map-decode fallback: a projection whose plan did not build.
	dec := &shardDecode{keep: keepK, projected: 1, binding: recordBindingFor(schema, nil, keepK)}
	r := 0
	for _, sh := range cohort.Shards() {
		rows, err := dec.openRows(arch, sh.Filename, schema)
		if err != nil {
			t.Fatal(err)
		}
		for {
			rec, err := rows.next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if r >= len(exactU64Want) {
				t.Fatalf("more than %d records", len(exactU64Want))
			}
			assertExactU64(t, "shard_parallel", r, rec)
			r++
		}
	}
	if r != len(exactU64Want) {
		t.Fatalf("%d records, want %d", r, len(exactU64Want))
	}
}

func TestExactU64_ParallelDecodeProjectionWithoutPlan(t *testing.T) {
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := filepath.Join(dir, "c.pulse")
	if err := afero.WriteFile(osFs, path, exactU64File(t), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	pctx, cleanup, available, err := buildParallelDecodeContext(svc, path, exactU64Schema(), nil, keepK, 1)
	if err != nil || !available {
		t.Fatalf("buildParallelDecodeContext: %v (available %v)", err, available)
	}
	defer func() { _ = cleanup() }()
	for _, workers := range []int{1, 2, 3} {
		seen := make([]int, len(exactU64Want))
		err := parallelDecodeMmap(context.Background(), pctx, workers, func(_, start, _ int) DecodeCallback {
			r := start
			return func(rec *processing.Record) error {
				assertExactU64(t, "parallel_decode", r, rec)
				seen[r]++
				r++
				return nil
			}
		})
		if err != nil {
			t.Fatalf("workers=%d: %v", workers, err)
		}
		for r, n := range seen {
			if n != 1 {
				t.Fatalf("workers=%d: row %d decoded %d times, want 1", workers, r, n)
			}
		}
	}
}

func TestExactU64_LookupRecord(t *testing.T) {
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "c.pulse", exactU64File(t), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	if _, err := svc.BuildIndex(context.Background(), "c.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	for r := range exactU64Want {
		matches, _, err := svc.lookupRecords(context.Background(), &types.LookupRequest{
			Cohort:        &types.Cohort{Filename: "c.pulse"},
			Field:         "id",
			Value:         strconv.Itoa(r + 1),
			ReturnColumns: []string{"k"},
		})
		if err != nil {
			t.Fatalf("row %d: %v", r, err)
		}
		if len(matches) != 1 {
			t.Fatalf("row %d: %d matches, want 1", r, len(matches))
		}
		assertExactU64(t, "lookup", r, matches[0].rec)
	}
}

func TestExactU64_FilterToFileRecord(t *testing.T) {
	svc := New(fs.NewMemMap())
	data := exactU64File(t)
	br := bytes.NewReader(data)
	pv, err := encoding.ReadHeader(br)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(br, pv)
	if err != nil {
		t.Fatal(err)
	}
	r := 0
	filter := func(rec *processing.Record) (bool, error) {
		assertExactU64(t, "filter_to_file", r, rec)
		r++
		return true, nil
	}
	for _, keep := range []encx.FieldFilter{nil, keepK} {
		r = 0
		at := bytes.NewReader(data)
		if _, err := at.Seek(int64(len(data)-br.Len()), 0); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		n, err := svc.streamFilterRecords(context.Background(), data, at, schema, filter, keep, &out)
		if err != nil {
			t.Fatal(err)
		}
		if int(n) != len(exactU64Want) || r != len(exactU64Want) {
			t.Fatalf("filter_to_file: wrote %d, filtered %d, want %d", n, r, len(exactU64Want))
		}
	}
}
