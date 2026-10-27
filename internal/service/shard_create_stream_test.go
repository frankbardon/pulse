package service

import (
	"archive/zip"
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// U51 E1-S4: `shard create` streams — a header-only plan, then each
// source once into the stage — and legacy-offset cohorts (#373) join,
// add to and pass the precheck against canonical-offset ones.

// legacyShape is one legacy stored-offset layout and its canonical twin.
type legacyShape struct {
	name    string
	fields  []encoding.Field // stored offsets as the legacy writer stored them
	country int
	rows    [][]uint64
}

func legacyShapes() []legacyShape {
	return []legacyShape{
		{
			// An embedder ImportJob.Schema that never set ByteOffset.
			name: "zero_offset_explicit_schema",
			fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeU32},
				{Name: "country", Type: encoding.FieldTypeCategoricalU8},
				{Name: "score", Type: encoding.FieldTypeU16},
			},
			country: 1,
			rows:    [][]uint64{{0x0A0B0C01, 1, 0xBEEF}, {0x0A0B0C0D, 0, 0x1234}},
		},
		{
			// A schema-file cohort laid out before the canonical routine:
			// packed_bool summed as 0 wide, every later offset a byte short.
			name: "bit_packed_first_schema_file",
			fields: []encoding.Field{
				{Name: "flag", Type: encoding.FieldTypePackedBool, ByteOffset: 0},
				{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0},
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 1},
			},
			country: 1,
			rows:    [][]uint64{{1, 1, 0x01020304}, {0, 0, 0x0A0B0C0D}},
		},
	}
}

// canonicalFields is fields laid out by the canonical routine.
func canonicalFields(fields []encoding.Field) []encoding.Field {
	s := &encoding.Schema{Fields: append([]encoding.Field(nil), fields...)}
	encx.LayoutFields(s)
	return s.Fields
}

// decodedRow is one record decoded under the archive's CANONICAL schema
// (as every engine path decodes a shard), its categorical resolved to
// the label.
type decodedRow map[string]any

// decodeArchiveRows decodes every stored shard of archive under the
// canonical `_schema.pulse` schema, in archive order.
func decodeArchiveRows(t *testing.T, fsys afero.Fs, archive string, country string) []decodedRow {
	t.Helper()
	a, err := openArchiveFile(fsys, archive)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer a.Close()
	doc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		t.Fatalf("schema doc: %v", err)
	}
	canonical := doc.Schema
	var out []decodedRow
	for _, e := range a.arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		payload, err := readEntryBytes(a.arch, e.Name)
		if err != nil {
			t.Fatal(err)
		}
		r := bytes.NewReader(payload)
		v, err := encoding.ReadHeader(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := encoding.ReadSchema(r, v); err != nil {
			t.Fatal(err)
		}
		rr := encx.NewRecordReader(r, canonical)
		for {
			vals := map[string]float64{}
			nulls := map[string]bool{}
			if err := rr.ReadRecord(vals, nulls); err != nil {
				if !stderrors.Is(err, io.EOF) {
					t.Fatalf("shard %s: %v", e.Name, err)
				}
				break
			}
			row := decodedRow{}
			for k, v := range vals {
				row[k] = v
			}
			ci := -1
			for i, f := range canonical.Fields {
				if f.Name == country {
					ci = i
				}
			}
			row[country] = canonical.Fields[ci].Dictionary.Resolve(uint32(vals[country]))
			out = append(out, row)
		}
	}
	return out
}

// expectedRows is what the shard's rows mean: the categorical resolved
// through the shard's OWN dictionary.
func expectedRows(fields []encoding.Field, country int, dict []string, rows [][]uint64) []decodedRow {
	out := make([]decodedRow, len(rows))
	for i, row := range rows {
		r := decodedRow{}
		for fi, f := range fields {
			if fi == country {
				r[f.Name] = dict[row[fi]]
				continue
			}
			r[f.Name] = float64(row[fi])
		}
		out[i] = r
	}
	return out
}

// TestShardStream_LegacyOffsetCohortsInteroperate (#373): a legacy
// stored-offset cohort and its canonical-offset twin join in either
// order — by create, by add, and through the cohesion precheck — and the
// archive decodes every record to what it meant in its source. The
// arriving dictionary diverges, so the dictionary rewrite (which used
// to refuse on the stored offsets) runs on the legacy side too.
func TestShardStream_LegacyOffsetCohortsInteroperate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, shape := range legacyShapes() {
		layouts := map[string][]encoding.Field{
			"legacy":    shape.fields,
			"canonical": canonicalFields(shape.fields),
		}
		dicts := map[string][]string{"first": {"US", "CA"}, "second": {"US", "MX"}}
		for _, order := range [][2]string{{"legacy", "canonical"}, {"canonical", "legacy"}} {
			first, second := order[0], order[1]
			t.Run(shape.name+"/"+first+"_then_"+second, func(t *testing.T) {
				want := append(
					expectedRows(shape.fields, shape.country, dicts["first"], shape.rows),
					expectedRows(shape.fields, shape.country, dicts["second"], shape.rows)...)
				country := shape.fields[shape.country].Name

				setup := func() (*Service, afero.Fs) {
					cfg := fs.NewMemMap()
					fsys := cfg.Fs()
					offsetTrustShard(t, fsys, "a.pulse", layouts[first], dicts["first"], shape.rows)
					offsetTrustShard(t, fsys, "b.pulse", layouts[second], dicts["second"], shape.rows)
					return New(cfg), fsys
				}

				t.Run("create", func(t *testing.T) {
					svc, fsys := setup()
					if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
						t.Fatalf("CreateShardArchive: %v", err)
					}
					if got := decodeArchiveRows(t, fsys, "arch.pulse", country); !reflect.DeepEqual(got, want) {
						t.Fatalf("decoded rows\n got %v\nwant %v", got, want)
					}
					assertVerifiesClean(t, svc, "arch.pulse")
				})
				t.Run("add", func(t *testing.T) {
					svc, fsys := setup()
					if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
						t.Fatalf("CreateShardArchive: %v", err)
					}
					if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); err != nil {
						t.Fatalf("AddShard: %v", err)
					}
					if got := decodeArchiveRows(t, fsys, "arch.pulse", country); !reflect.DeepEqual(got, want) {
						t.Fatalf("decoded rows\n got %v\nwant %v", got, want)
					}
					assertVerifiesClean(t, svc, "arch.pulse")
				})
				t.Run("precheck", func(t *testing.T) {
					svc, _ := setup()
					if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
						t.Fatalf("CreateShardArchive: %v", err)
					}
					schema := &encoding.Schema{Fields: append([]encoding.Field(nil), layouts[second]...)}
					d := encoding.NewDictionary()
					for _, v := range dicts["second"] {
						_, _ = d.Add(v)
					}
					schema.Fields[shape.country].Dictionary = d
					if err := svc.PrecheckShardCohesion(ctx, "arch.pulse", schema); err != nil {
						t.Fatalf("PrecheckShardCohesion: %v", err)
					}
				})
			})
		}
	}
}

func assertVerifiesClean(t *testing.T, svc *Service, archive string) {
	t.Helper()
	vr, err := svc.VerifyShardArchive(context.Background(), archive)
	if err != nil {
		t.Fatalf("VerifyShardArchive: %v", err)
	}
	if len(vr.Errors) != 0 {
		t.Fatalf("archive does not verify: %v", vr.Errors)
	}
}

// TestShardStream_CreateMatchesSequentialAdd: the streaming create —
// a header-only fold, each source then replayed once — writes the same
// bytes, and reports the same widenings, as creating from the first
// source and adding the rest one by one. The sequence exercises every
// replayed transform: a dictionary remap (b), an archive-level rung widen
// that re-lays-out the earlier sources (c), and a narrower arrival
// promoted to the archive's rung (d), whose re-laid-out record count
// must be the source's even though the fold only saw its header.
func TestShardStream_CreateMatchesSequentialAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	write := func(name string, ft encoding.FieldType, dict []string, recs [][2]uint64) string {
		if err := afero.WriteFile(fsys, name, writeSetShard(t, ft, dict, recs), 0o644); err != nil {
			t.Fatal(err)
		}
		return name
	}
	paths := []string{
		write("a.pulse", encoding.FieldTypeSetU8, []string{"tv"}, [][2]uint64{{1, 1}, {2, 0}}),
		write("b.pulse", encoding.FieldTypeSetU8, []string{"radio", "tv"}, [][2]uint64{{3, 3}, {4, 1}, {5, 2}}),
		write("c.pulse", encoding.FieldTypeSetU16, []string{"tv", "web"}, [][2]uint64{{6, 2}}),
		write("d.pulse", encoding.FieldTypeSetU8, []string{"tv", "print"}, [][2]uint64{{7, 3}, {8, 2}, {9, 1}, {10, 0}}),
	}

	created, err := svc.CreateShardArchive(ctx, "created.pulse", paths)
	if err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "added.pulse", paths[:1]); err != nil {
		t.Fatalf("CreateShardArchive seed: %v", err)
	}
	var addWidened []SetWidening
	for _, p := range paths[1:] {
		res, err := svc.AddShard(ctx, "added.pulse", p)
		if err != nil {
			t.Fatalf("AddShard %s: %v", p, err)
		}
		addWidened = append(addWidened, res.Widened...)
	}

	got, err := afero.ReadFile(fsys, "created.pulse")
	if err != nil {
		t.Fatal(err)
	}
	want, err := afero.ReadFile(fsys, "added.pulse")
	if err != nil {
		t.Fatal(err)
	}
	gotDoc, wantDoc := archiveEntriesByName(t, got), archiveEntriesByName(t, want)
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		for name := range wantDoc {
			if !bytes.Equal(gotDoc[name], wantDoc[name]) {
				t.Errorf("entry %s differs between create and sequential add", name)
			}
		}
		t.Fatalf("create and sequential add disagree")
	}
	if !reflect.DeepEqual(created.Widened, addWidened) {
		t.Fatalf("widenings differ\ncreate %+v\n   add %+v", created.Widened, addWidened)
	}
	if len(created.Widened) != 2 || created.Widened[1].RecordsRewritten != 4 {
		t.Fatalf("widenings = %+v, want the archive widen and d's 4-record promotion", created.Widened)
	}
}

// archiveEntriesByName is every entry's payload (zip framing aside).
func archiveEntriesByName(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

// peakHeapDuring samples the live heap while fn runs (GC tightened so
// the heap tracks what is actually held) and returns the peak above the
// starting heap.
func peakHeapDuring(t *testing.T, fn func() error) uint64 {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var s runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&s)
			if s.HeapAlloc > base && s.HeapAlloc-base > peak.Load() {
				peak.Store(s.HeapAlloc - base)
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()
	err := fn()
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return peak.Load()
}

// TestShardStream_CreateMemoryBoundedBySource is the FR-12 gate: create
// over N sources holds one source plus the plan, never the sum. Measured
// at N=4 and N=32 (8x the bytes):
//   - verbatim: every source takes no transform, so it is streamed into
//     the stage and the whole create ALLOCATES fixed buffers only;
//   - remapped: every source's dictionary diverges, so each is loaded,
//     rewritten and stored — the live heap peaks at about one source.
//
// The resident create allocated, and held, every source at once.
func TestShardStream_CreateMemoryBoundedBySource(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const recs = 40_000
	ctx := context.Background()

	t.Run("verbatim", func(t *testing.T) {
		shardBytes := uint64(recs * 12)
		fixed := uint64(2 << 20)
		var got [2]uint64
		for i, n := range []int{4, 32} {
			svc, fsys, dir := streamOsEnv(t)
			paths := make([]string, n)
			for j := range paths {
				paths[j] = filepath.Join(dir, fmt.Sprintf("s%02d.pulse", j))
				adminWriteShardFile(t, fsys, paths[j], adminScoreSchema(), streamShardRecords(j*recs, recs))
			}
			got[i] = allocDuring(t, func() error {
				_, err := svc.CreateShardArchive(ctx, filepath.Join(dir, "arch.pulse"), paths)
				return err
			})
		}
		t.Logf("verbatim create: 4 sources -> %d B allocated, 32 sources -> %d B", got[0], got[1])
		for i := range got {
			if got[i] > shardBytes+fixed {
				t.Errorf("create allocated %d B, want <= one source (%d B) + fixed buffers", got[i], shardBytes)
			}
		}
		// 28 more sources cost their plan entries, never their bytes.
		if got[1] > got[0]+shardBytes {
			t.Errorf("create allocation grows with the sources: %d B at 4, %d B at 32", got[0], got[1])
		}
	})

	t.Run("remapped", func(t *testing.T) {
		shardBytes := uint64(recs * 5)
		var got [2]uint64
		for i, n := range []int{4, 32} {
			svc, fsys, dir := streamOsEnv(t)
			paths := make([]string, n)
			for j := range paths {
				paths[j] = filepath.Join(dir, fmt.Sprintf("s%02d.pulse", j))
				// Each source puts its own member FIRST, so the shared
				// "tv" moves index in every one: every source is rewritten.
				rows := make([][2]uint64, recs)
				for r := range rows {
					rows[r] = [2]uint64{uint64(r), 3}
				}
				data := writeSetShard(t, encoding.FieldTypeSetU8, []string{fmt.Sprintf("m%02d", j%7), "tv"}, rows)
				if err := afero.WriteFile(fsys, paths[j], data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got[i] = peakHeapDuring(t, func() error {
				_, err := svc.CreateShardArchive(ctx, filepath.Join(dir, "arch.pulse"), paths)
				return err
			})
		}
		t.Logf("remapped create: 4 sources -> peak %d B, 32 sources -> peak %d B (source %d B)", got[0], got[1], shardBytes)
		// One source loaded + its rewrite + the stored copy's buffers;
		// the resident create held 32 sources (~6.4 MB) at once.
		if bound := 8*shardBytes + uint64(2<<20); got[1] > bound {
			t.Errorf("create over 32 sources peaked at %d B live, want <= %d B (one source + the plan)", got[1], bound)
		}
		if got[1] > got[0]+4*shardBytes {
			t.Errorf("create peak grows with the sources: %d B at 4, %d B at 32", got[0], got[1])
		}
	})
}

// TestShardStream_ShardCountLimitRefused: an archive's SHRD shard_count
// is a u16, so a create, add or filter-to-file that would write more
// than 65535 shards is refused with PULSE_SHARD_COUNT_LIMIT before
// anything is written — never stored truncated.
func TestShardStream_ShardCountLimitRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	assertLimit := func(t *testing.T, err error, want int) {
		t.Helper()
		if !errors.HasCode(err, errors.PULSE_SHARD_COUNT_LIMIT) {
			t.Fatalf("err = %v, want PULSE_SHARD_COUNT_LIMIT", err)
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Details["shard_count"] != want || ce.Details["limit"] != encx.MaxArchiveShards {
			t.Fatalf("details = %v, want shard_count %d limit %d", ce.Details, want, encx.MaxArchiveShards)
		}
	}

	t.Run("create", func(t *testing.T) {
		cfg := fs.NewMemMap()
		svc := New(cfg)
		paths := make([]string, encx.MaxArchiveShards+1)
		for i := range paths {
			paths[i] = fmt.Sprintf("s%05d.pulse", i) // never read
		}
		_, err := svc.CreateShardArchive(ctx, "arch.pulse", paths)
		assertLimit(t, err, encx.MaxArchiveShards+1)
		assertNothingWritten(t, cfg.Fs(), "arch.pulse")
	})

	t.Run("add", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)
		writeWideArchive(t, fsys, "arch.pulse", encx.MaxArchiveShards, nil)
		before, _ := afero.ReadFile(fsys, "arch.pulse")
		// A shard the merge would refuse (renamed field): the limit must
		// answer first, before any merge runs.
		other := adminScoreSchema()
		other.Fields[1].Name = "renamed"
		adminWriteShardFile(t, fsys, "new.pulse", other, [][]uint64{{1, math.Float64bits(1)}})
		_, err := svc.AddShard(ctx, "arch.pulse", "new.pulse")
		assertLimit(t, err, encx.MaxArchiveShards+1)
		after, _ := afero.ReadFile(fsys, "arch.pulse")
		if !bytes.Equal(before, after) {
			t.Fatal("refused add changed the archive")
		}
		assertNothingWritten(t, fsys, "")
	})

	t.Run("filter_to_file", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)
		// An archive an earlier release wrote past the limit (its stored
		// shard_count wrapped); filtering it would write the same count.
		// The last shard is garbage: the limit must answer before any
		// shard is filtered.
		writeWideArchive(t, fsys, "src.pulse", encx.MaxArchiveShards+1, []byte("not a pulse shard"))
		_, err := svc.FilterToFile(ctx, "src.pulse", "dst.pulse", "score > 0")
		assertLimit(t, err, encx.MaxArchiveShards+1)
		assertNothingWritten(t, fsys, "dst.pulse")
	})

	t.Run("at_the_limit_is_admitted", func(t *testing.T) {
		if err := checkArchiveShardCount(encx.MaxArchiveShards, "arch.pulse"); err != nil {
			t.Fatalf("65535 shards refused: %v", err)
		}
	})
}

// writeWideArchive writes an archive of n identical one-record shards
// straight through a zip writer (the SHRD count is a placeholder); a
// non-nil last replaces the final shard's payload.
func writeWideArchive(t *testing.T, fsys afero.Fs, path string, n int, last []byte) {
	t.Helper()
	schema := adminScoreSchema()
	shard := writeSinglePulse(t, schema, [][]uint64{{1, math.Float64bits(1)}})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var doc bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, schema, uint64(n), uint16(n)); err != nil {
		t.Fatal(err)
	}
	if err := writeArchiveEntry(zw, encx.ReservedSchemaName, doc.Bytes()); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		payload := shard
		if i == n-1 && last != nil {
			payload = last
		}
		if err := writeArchiveEntry(zw, fmt.Sprintf("s%05d.pulse", i), payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertNothingWritten: no stage beside the root, and (when named) no
// destination.
func assertNothingWritten(t *testing.T, fsys afero.Fs, dst string) {
	t.Helper()
	if dst != "" {
		if ok, _ := afero.Exists(fsys, dst); ok {
			t.Fatalf("%s was written", dst)
		}
	}
	matches, _ := afero.Glob(fsys, "*"+shardAdminTempPattern+"*")
	if len(matches) != 0 {
		t.Fatalf("stage left behind: %v", matches)
	}
}
