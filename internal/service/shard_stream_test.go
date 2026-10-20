package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// U51 E1-S3: shard add / remove / compact stream the rewritten archive
// into a staged file — stored shards copied raw, the aggregate summed
// header-only — so memory is bounded by the arriving shard, never the
// archive, and the original stays byte-identical until one rename.

// streamShardRecords builds an adminScoreSchema shard of n records whose
// ids start at base.
func streamShardRecords(base, n int) [][]uint64 {
	recs := make([][]uint64, n)
	for i := range recs {
		recs[i] = []uint64{uint64(base + i), math.Float64bits(float64(base+i) / 4)}
	}
	return recs
}

// streamOsEnv is a service over the real OS filesystem rooted at a
// fresh temp dir: an in-memory fs grows its files by append, which would
// charge the WRITE of the new archive to the heap and mask exactly what
// the bounded-memory gate measures.
func streamOsEnv(t *testing.T) (*Service, afero.Fs, string) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := fs.New(fs.WithFs(afero.NewOsFs()))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), cfg.Fs(), dir
}

// streamBuildArchive writes shards s00..s<n-1> of recs records each
// beside dir/arch.pulse and creates the archive from them.
func streamBuildArchive(t *testing.T, svc *Service, fsys afero.Fs, dir string, shards, recs int) string {
	t.Helper()
	schema := adminScoreSchema()
	paths := make([]string, shards)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("s%02d.pulse", i))
		adminWriteShardFile(t, fsys, paths[i], schema, streamShardRecords(i*recs, recs))
	}
	archive := filepath.Join(dir, "arch.pulse")
	if _, err := svc.CreateShardArchive(context.Background(), archive, paths); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return archive
}

// allocDuring is the heap bytes allocated while fn runs.
func allocDuring(t *testing.T, fn func() error) uint64 {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestShardStream_MutationAllocationBoundedByShard is the FR-15 gate:
// the bytes `shard add` allocates are bounded by the ARRIVING shard, not
// the archive. The same add is measured against an archive of 4 shards
// and one of 32 (8x the bytes): the slurping rewrite allocated about 3x
// the archive (tens of MiB on the larger one); the streaming one
// allocates the arriving shard plus fixed buffers at either size.
// remove and compact are held to the same bound with no arriving shard.
func TestShardStream_MutationAllocationBoundedByShard(t *testing.T) {
	const recs = 40_000 // 12-byte records: ~480 KB per shard
	shardBytes := uint64(recs * 12)
	fixed := uint64(2 << 20) // copy buffers, zip + bufio writers, schema decode

	type verb struct {
		name  string
		run   func(svc *Service, archive, incoming string) error
		bound uint64
	}
	ctx := context.Background()
	verbs := []verb{
		{"add", func(svc *Service, archive, incoming string) error {
			_, err := svc.AddShard(ctx, archive, incoming)
			return err
		}, 3*shardBytes + fixed},
		{"remove", func(svc *Service, archive, _ string) error {
			return svc.RemoveShard(ctx, archive, "s00.pulse")
		}, fixed},
		{"compact", func(svc *Service, archive, _ string) error {
			return svc.CompactShardArchive(ctx, archive)
		}, fixed},
	}
	for _, v := range verbs {
		t.Run(v.name, func(t *testing.T) {
			var got [2]uint64
			var sizes [2]int64
			for i, shards := range []int{4, 32} {
				svc, fsys, dir := streamOsEnv(t)
				archive := streamBuildArchive(t, svc, fsys, dir, shards, recs)
				incoming := filepath.Join(dir, "incoming.pulse")
				adminWriteShardFile(t, fsys, incoming, adminScoreSchema(), streamShardRecords(1_000_000, recs))
				st, err := fsys.Stat(archive)
				if err != nil {
					t.Fatal(err)
				}
				sizes[i] = st.Size()
				got[i] = allocDuring(t, func() error { return v.run(svc, archive, incoming) })
			}
			t.Logf("%s: archive %d B -> alloc %d B; archive %d B -> alloc %d B (bound %d)",
				v.name, sizes[0], got[0], sizes[1], got[1], v.bound)
			for i := range got {
				if got[i] > v.bound {
					t.Errorf("%s over a %d-byte archive allocated %d bytes, want <= %d (the arriving shard + fixed buffers, not the archive)",
						v.name, sizes[i], got[i], v.bound)
				}
			}
			// 8x the archive must not cost meaningfully more: a slurp
			// would add ~3 x the extra 28 shards here.
			if got[1] > got[0]+shardBytes/2 {
				t.Errorf("%s allocation grows with the archive: %d B at %d B, %d B at %d B",
					v.name, got[0], sizes[0], got[1], sizes[1])
			}
		})
	}
}

// TestShardStream_StoredShardsCopiedRaw: add, remove and compact copy a
// stored shard's bytes RAW (zip CreateRaw from a SectionReader) — no
// decode, so no CRC re-check. A record byte flipped inside a stored
// entry (its CRC now stale) is carried through verbatim; the decoding
// rewrite this replaced failed the whole mutation on the checksum.
func TestShardStream_StoredShardsCopiedRaw(t *testing.T) {
	ctx := context.Background()
	verbs := map[string]func(svc *Service) error{
		"add": func(svc *Service) error {
			_, err := svc.AddShard(ctx, "arch.pulse", "c.pulse")
			return err
		},
		"remove":  func(svc *Service) error { return svc.RemoveShard(ctx, "arch.pulse", "a.pulse") },
		"compact": func(svc *Service) error { return svc.CompactShardArchive(ctx, "arch.pulse") },
	}
	for name, run := range verbs {
		t.Run(name, func(t *testing.T) {
			cfg := fs.NewMemMap()
			fsys := cfg.Fs()
			svc := New(cfg)
			schema := adminScoreSchema()
			for i, n := range []string{"a.pulse", "b.pulse", "c.pulse"} {
				adminWriteShardFile(t, fsys, n, schema, streamShardRecords(i*10, 10))
			}
			if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
				t.Fatal(err)
			}
			flipped := streamFlipLastByte(t, fsys, "arch.pulse", "b.pulse")
			if err := run(svc); err != nil {
				t.Fatalf("%s over a stale-CRC stored shard: %v (a raw copy never re-hashes)", name, err)
			}
			if got := streamEntryBytes(t, fsys, "arch.pulse", "b.pulse"); !bytes.Equal(got, flipped) {
				t.Fatalf("%s re-encoded the stored shard instead of copying it raw", name)
			}
		})
	}
}

// streamFlipLastByte flips the last stored byte of the named entry in
// place (a record byte; the CRC is left stale) and returns the entry's
// new bytes.
func streamFlipLastByte(t *testing.T, fsys afero.Fs, archive, entry string) []byte {
	t.Helper()
	data := gReadFile(t, fsys, archive)
	arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range arch.Entries() {
		if e.Name == entry {
			data[e.Offset+e.Size-1] ^= 0xFF
			if err := afero.WriteFile(fsys, archive, data, 0o644); err != nil {
				t.Fatal(err)
			}
			return slices.Clone(data[e.Offset : e.Offset+e.Size])
		}
	}
	t.Fatalf("no entry %q", entry)
	return nil
}

// streamEntryBytes is the named entry's stored bytes, read by offset.
func streamEntryBytes(t *testing.T, fsys afero.Fs, archive, entry string) []byte {
	t.Helper()
	data := gReadFile(t, fsys, archive)
	arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	sect, err := arch.OpenAt(entry)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(&sect)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestShardStream_CompactIsByteIdentity: a raw copy re-emits the
// source's own headers, so compacting a Pulse-written archive whose
// metadata is already current reproduces it byte for byte, and a
// remove-then-add round trip equals creating the same shards directly.
func TestShardStream_CompactIsByteIdentity(t *testing.T) {
	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	names := []string{"a.pulse", "b.pulse", "c.pulse"}
	for i, n := range names {
		adminWriteShardFile(t, fsys, n, schema, streamShardRecords(i*100, 50+i))
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", names); err != nil {
		t.Fatal(err)
	}
	before := gReadFile(t, fsys, "arch.pulse")
	if err := svc.CompactShardArchive(ctx, "arch.pulse"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, gReadFile(t, fsys, "arch.pulse")) {
		t.Fatal("compacting a current archive changed its bytes")
	}
	if err := svc.RemoveShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, gReadFile(t, fsys, "arch.pulse")) {
		t.Fatal("remove + add of the last shard did not reproduce the created archive")
	}
	gVerifyClean(t, svc, "arch.pulse")
}

// TestShardStream_DeflatedEntryReStored: a deflated stored shard (a
// third-party zip tool's) cannot be copied raw; a mutation inflates it
// through the zip reader and re-stores it, so the rewritten archive is
// store-only and readable on every record path again.
func TestShardStream_DeflatedEntryReStored(t *testing.T) {
	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	for i, n := range []string{"a.pulse", "b.pulse", "c.pulse"} {
		adminWriteShardFile(t, fsys, n, schema, streamShardRecords(i*10, 10))
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	deflateEntry(t, fsys, "arch.pulse", "a.pulse")
	if _, err := svc.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatalf("AddShard over a deflated stored shard: %v", err)
	}
	data := gReadFile(t, fsys, "arch.pulse")
	arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range arch.Entries() {
		if !arch.IsStored(e.Name) {
			t.Errorf("entry %q still deflated after the rewrite", e.Name)
		}
	}
	if got, want := streamEntryBytes(t, fsys, "arch.pulse", "a.pulse"), gReadFile(t, fsys, "a.pulse"); !bytes.Equal(got, want) {
		t.Fatal("re-stored shard differs from its source")
	}
	gVerifyClean(t, svc, "arch.pulse")
}

// crashFs forwards to Fs, but its Rename runs onRename and then fails,
// and it cannot Remove a stage — the state a process killed just
// before publishing leaves behind.
type crashFs struct {
	afero.Fs
	onRename func(oldname, newname string)
}

func (c *crashFs) Rename(oldname, newname string) error {
	if c.onRename != nil {
		c.onRename(oldname, newname)
	}
	return &os.PathError{Op: "rename", Path: newname, Err: io.ErrUnexpectedEOF}
}

func (c *crashFs) Remove(name string) error {
	if strings.Contains(name, shardAdminTempPattern) {
		return &os.PathError{Op: "remove", Path: name, Err: os.ErrPermission}
	}
	return c.Fs.Remove(name)
}

// TestShardStream_CrashBeforeRenameLeavesOriginalAndIsSwept is the
// crash-injection gate (FR-14). Each mutation — ordinary add, an
// archive-level set widen, remove, compact — is killed at the rename:
// at that instant the original is still byte-identical (the stage is
// complete beside it), after it the original is untouched and the stage
// is left on disk, and the NEXT mutation sweeps it together with a
// random-suffix stage an earlier release left, while another archive's
// stage in the same directory survives.
func TestShardStream_CrashBeforeRenameLeavesOriginalAndIsSwept(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(svc *Service) error
	}{
		{"add", func(svc *Service) error { _, err := svc.AddShard(ctx, "arch.pulse", "c.pulse"); return err }},
		{"add_widen", func(svc *Service) error { _, err := svc.AddShard(ctx, "arch.pulse", "wide.pulse"); return err }},
		{"remove", func(svc *Service) error { return svc.RemoveShard(ctx, "arch.pulse", "a.pulse") }},
		{"compact", func(svc *Service) error { return svc.CompactShardArchive(ctx, "arch.pulse") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := afero.NewMemMapFs()
			cfg, err := fs.New(fs.WithFs(base))
			if err != nil {
				t.Fatal(err)
			}
			svc := New(cfg)
			seed := writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "radio"}, [][2]uint64{{1, 0b01}, {2, 0b11}})
			c := writeSetShard(t, encoding.FieldTypeSetU8, []string{"radio", "web"}, [][2]uint64{{3, 0b10}})
			b := writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv"}, [][2]uint64{{4, 0b1}})
			wide := writeSetShard(t, encoding.FieldTypeSetU16, []string{"tv", "podcast"}, [][2]uint64{{5, 0b10}})
			for n, d := range map[string][]byte{"a.pulse": seed, "b.pulse": b, "c.pulse": c, "wide.pulse": wide} {
				if err := afero.WriteFile(base, n, d, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
				t.Fatal(err)
			}
			prior := gReadFile(t, base, "arch.pulse")
			legacy := "arch.pulse" + shardAdminTempPattern + "123456789"
			other := "other.pulse" + shardAdminTempPattern + shardStagedSuffix
			for _, n := range []string{legacy, other} {
				if err := afero.WriteFile(base, n, []byte("orphan"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			renamed := false
			crash := &crashFs{Fs: base, onRename: func(oldname, newname string) {
				renamed = true
				if !bytes.Equal(prior, gReadFile(t, base, newname)) {
					t.Errorf("the original changed before the rename")
				}
				if staged := gReadFile(t, base, oldname); len(staged) == 0 {
					t.Errorf("the stage %s is empty at the rename", oldname)
				}
			}}
			crashCfg, err := fs.New(fs.WithFs(crash))
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.run(New(crashCfg)); err == nil || !errors.HasCode(err, errors.SERVICE_RESOURCE) {
				t.Fatalf("crashed %s: err = %v, want SERVICE_RESOURCE", tc.name, err)
			}
			if !renamed {
				t.Fatal("the mutation never reached its rename")
			}
			if !bytes.Equal(prior, gReadFile(t, base, "arch.pulse")) {
				t.Fatal("a crash before the rename modified the archive")
			}
			stage := stagedArchivePath("arch.pulse")
			if ok, _ := afero.Exists(base, stage); !ok {
				t.Fatalf("the crash left no stage %s to sweep", stage)
			}
			gVerifyClean(t, svc, "arch.pulse")

			// The next mutation sweeps this archive's stages, never another's.
			if err := tc.run(svc); err != nil {
				t.Fatalf("%s after the crash: %v", tc.name, err)
			}
			for _, n := range []string{stage, legacy} {
				if ok, _ := afero.Exists(base, n); ok {
					t.Errorf("stage %s survived the next mutation", n)
				}
			}
			if ok, _ := afero.Exists(base, other); !ok {
				t.Errorf("another archive's stage %s was swept", other)
			}
			gVerifyClean(t, svc, "arch.pulse")
		})
	}
}

// syncSpyFs records renames and directory syncs, in order.
type syncSpyFs struct {
	afero.Fs
	mu     sync.Mutex
	events []string
}

func (s *syncSpyFs) record(e string) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *syncSpyFs) Rename(oldname, newname string) error {
	if err := s.Fs.Rename(oldname, newname); err != nil {
		return err
	}
	s.record("rename " + newname)
	return nil
}

func (s *syncSpyFs) Open(name string) (afero.File, error) {
	f, err := s.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	if st, serr := f.Stat(); serr == nil && st.IsDir() {
		return &syncSpyDir{File: f, fs: s, name: name}, nil
	}
	return f, nil
}

type syncSpyDir struct {
	afero.File
	fs   *syncSpyFs
	name string
}

func (d *syncSpyDir) Sync() error {
	d.fs.record("syncdir " + d.name)
	return d.File.Sync()
}

// TestShardStream_DirectorySyncedAfterRename: every archive mutation
// fsyncs the archive's directory AFTER the rename, so the published name
// itself survives a crash.
func TestShardStream_DirectorySyncedAfterRename(t *testing.T) {
	ctx := context.Background()
	base := afero.NewMemMapFs()
	schema := adminScoreSchema()
	for i, n := range []string{"d/a.pulse", "d/b.pulse"} {
		adminWriteShardFile(t, base, n, schema, streamShardRecords(i*10, 10))
	}
	spy := &syncSpyFs{Fs: base}
	cfg, err := fs.New(fs.WithFs(spy))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	steps := []struct {
		name string
		run  func() error
	}{
		{"create", func() error {
			_, err := svc.CreateShardArchive(ctx, "d/arch.pulse", []string{"d/a.pulse"})
			return err
		}},
		{"add", func() error { _, err := svc.AddShard(ctx, "d/arch.pulse", "d/b.pulse"); return err }},
		{"remove", func() error { return svc.RemoveShard(ctx, "d/arch.pulse", "a.pulse") }},
		{"compact", func() error { return svc.CompactShardArchive(ctx, "d/arch.pulse") }},
	}
	for _, st := range steps {
		spy.events = nil
		if err := st.run(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		want := []string{"rename d/arch.pulse", "syncdir d"}
		if !slices.Equal(spy.events, want) {
			t.Errorf("%s: events %v, want %v", st.name, spy.events, want)
		}
	}
}

// TestShardStream_PromotionAcrossManyStoredShards: a constant-group
// promotion over an archive of SEVERAL stored shards streams each one
// through the two-pass re-encode (discover the final dictionaries, then
// re-encode each shard against them) and lands byte-identical to
// creating the same shards together, with exact counts on the
// MANDATORY warning.
func TestShardStream_PromotionAcrossManyStoredShards(t *testing.T) {
	_, a2, aRows := gShardA().build(t, nil)
	_, b2, bRows := gShardB().build(t, nil)
	c := gShardB()
	c.lo, c.hi, c.idBase, c.src = 50, 70, 30000, "batch-c"
	_, c2, cRows := c.build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"x/a.pulse": a2, "x/b.pulse": b2, "x/c.pulse": c2})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "both.pulse", []string{"x/a.pulse", "x/b.pulse", "x/c.pulse"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "seq.pulse", []string{"x/a.pulse", "x/b.pulse"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.AddShard(ctx, "seq.pulse", "x/c.pulse")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !bytes.Equal(gReadFile(t, fsys, "both.pulse"), gReadFile(t, fsys, "seq.pulse")) {
		t.Fatal("streamed promotion differs from creating the shards together")
	}
	ws := gRegroupWarnings(res.Warnings)
	if len(ws) != 1 || ws[0].Details["reason"] != RegroupConstantPromoted ||
		ws[0].Details["shards_rewritten"] != 3 || ws[0].Details["records_rewritten"] != int64(aRows+bRows+cRows) {
		t.Fatalf("warnings = %+v", res.Warnings)
	}
	doc := schemaDocFromArchive(t, fsys, "seq.pulse")
	if doc.AggregateRecordCount != uint64(aRows+bRows+cRows) {
		t.Fatalf("aggregate %d, want %d", doc.AggregateRecordCount, aRows+bRows+cRows)
	}
	gVerifyClean(t, svc, "seq.pulse")
}

// TestShardStream_ArchiveWidenStreamsEveryStoredShard: an archive-level
// set widen over several stored shards re-strides each one on its way
// into the stage, with the mandatory warning's shards / records exact
// and the result identical to creating the shards together.
func TestShardStream_ArchiveWidenStreamsEveryStoredShard(t *testing.T) {
	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	files := map[string][]byte{
		"a.pulse":    writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "radio"}, [][2]uint64{{1, 0b01}, {2, 0b11}}),
		"b.pulse":    writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "print"}, [][2]uint64{{3, 0b10}, {4, 0b11}, {5, 0b01}}),
		"wide.pulse": writeSetShard(t, encoding.FieldTypeSetU16, []string{"web", "tv"}, [][2]uint64{{6, 0b11}}),
	}
	for n, d := range files {
		if err := afero.WriteFile(fsys, n, d, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CreateShardArchive(ctx, "both.pulse", []string{"a.pulse", "b.pulse", "wide.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateShardArchive(ctx, "seq.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.AddShard(ctx, "seq.pulse", "wide.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Widened) != 1 || !res.Widened[0].ArchiveWidened() ||
		res.Widened[0].ShardsRewritten != 2 || res.Widened[0].RecordsRewritten != 5 {
		t.Fatalf("Widened = %+v, want the archive's 2 shards / 5 records", res.Widened)
	}
	var warned bool
	for _, w := range res.Warnings {
		if w.Code == string(errors.PULSE_SHARD_SET_WIDENED) {
			warned = w.Details["shards_rewritten"] == 2 && w.Details["records_rewritten"] == int64(5)
		}
	}
	if !warned {
		t.Fatalf("no exact PULSE_SHARD_SET_WIDENED warning in %+v", res.Warnings)
	}
	if !bytes.Equal(gReadFile(t, fsys, "both.pulse"), gReadFile(t, fsys, "seq.pulse")) {
		t.Fatal("streamed widen differs from creating the shards together")
	}
	gVerifyClean(t, svc, "seq.pulse")
}
