package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// shard_append_test.go pins the in-place tail append of an ordinary
// `shard add` (U73 #492): the archive's old bytes are a prefix of the
// new archive, what is written is the new shard + `_schema.pulse` + the
// directory whatever the archive's size, the result reads exactly as a
// rewrite (or a create) of the same shards does, a reader opened before
// the append keeps reading every pre-append shard, and a refused
// write-open is PULSE_SHARD_APPEND_REFUSED with nothing written.

// appendEntries is the archive's directory as OpenArchive reads it.
func appendEntries(t *testing.T, data []byte) []encx.ArchiveEntry {
	t.Helper()
	a, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	return a.Entries()
}

// sameArchiveContent fails unless got and want list the same entry
// names in the same directory order with the same sizes, CRC32s and
// payloads.
func sameArchiveContent(t *testing.T, got, want []byte) {
	t.Helper()
	ge, we := appendEntries(t, got), appendEntries(t, want)
	render := func(es []encx.ArchiveEntry) string {
		var parts []string
		for _, e := range es {
			parts = append(parts, fmt.Sprintf("%s:%d:%08x", e.Name, e.Size, e.CRC32))
		}
		return strings.Join(parts, " ")
	}
	if render(ge) != render(we) {
		t.Fatalf("directory\n got %s\nwant %s", render(ge), render(we))
	}
	gp, wp := archiveEntriesByName(t, got), archiveEntriesByName(t, want)
	for name, w := range wp {
		if !bytes.Equal(gp[name], w) {
			t.Errorf("entry %s payload differs", name)
		}
	}
}

// TestShardAppend_AddAppendsInPlace: an ordinary add leaves the
// archive's old bytes as a prefix, lists ONE `_schema.pulse` (the new
// one, first) and the shards in archive order, reads through
// archive/zip and the Pulse reader, and counts every record.
func TestShardAppend_AddAppendsInPlace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	for i, n := range []string{"a.pulse", "b.pulse", "c.pulse"} {
		adminWriteShardFile(t, fsys, n, schema, streamShardRecords(i*100, 10+i))
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	before := gReadFile(t, fsys, "arch.pulse")

	res, err := svc.AddShard(ctx, "arch.pulse", "c.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if res.ShardCount != 3 || len(res.Warnings) != 0 {
		t.Errorf("result = %+v, want 3 shards and no warning (no index, no widen)", res)
	}
	after := gReadFile(t, fsys, "arch.pulse")
	if !bytes.HasPrefix(after, before) {
		t.Fatal("an ordinary add rewrote the archive's existing bytes")
	}
	var names []string
	for _, e := range appendEntries(t, after) {
		names = append(names, e.Name)
	}
	if got := strings.Join(names, ","); got != "_schema.pulse,a.pulse,b.pulse,c.pulse" {
		t.Errorf("directory = %s", got)
	}
	payloads := archiveEntriesByName(t, after)
	if !bytes.Equal(payloads["c.pulse"], gReadFile(t, fsys, "c.pulse")) {
		t.Error("appended shard differs from its source")
	}
	if n, err := svc.CountRecords(ctx, "arch.pulse"); err != nil || n != 10+11+12 {
		t.Errorf("CountRecords = %d, %v; want 33 (the refreshed aggregate)", n, err)
	}
	gVerifyClean(t, svc, "arch.pulse")

	// Compacting reclaims the dead bytes and lands on create's bytes.
	if _, err := svc.CreateShardArchive(ctx, "created.pulse", []string{"a.pulse", "b.pulse", "c.pulse"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.CompactShardArchive(ctx, "arch.pulse"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gReadFile(t, fsys, "arch.pulse"), gReadFile(t, fsys, "created.pulse")) {
		t.Error("compacting the appended archive did not reproduce create's bytes")
	}
}

// TestShardAppend_WriteVolumeIsIndependentOfArchiveSize: an append
// writes the new shard, the new `_schema.pulse` and the directory — the
// same number of bytes over an archive of tiny shards and one a hundred
// times larger, to the byte the zip records account for.
func TestShardAppend_WriteVolumeIsIndependentOfArchiveSize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := adminScoreSchema()
	growth := func(storedRecords int) (int64, int64) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)
		adminWriteShardFile(t, fsys, "a.pulse", schema, streamShardRecords(0, storedRecords))
		adminWriteShardFile(t, fsys, "b.pulse", schema, streamShardRecords(storedRecords, storedRecords))
		adminWriteShardFile(t, fsys, "new.pulse", schema, streamShardRecords(1<<20, 25))
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
			t.Fatal(err)
		}
		before := gReadFile(t, fsys, "arch.pulse")
		if _, err := svc.AddShard(ctx, "arch.pulse", "new.pulse"); err != nil {
			t.Fatal(err)
		}
		after := gReadFile(t, fsys, "arch.pulse")
		if !bytes.HasPrefix(after, before) {
			t.Fatal("add rewrote the archive")
		}
		// Exactly: two entries (local header + name + payload + data
		// descriptor), then one directory record per live entry, then
		// the EOCD.
		var want int64
		for _, e := range appendEntries(t, after) {
			if e.Name == "new.pulse" || e.Name == encx.ReservedSchemaName {
				want += 30 + int64(len(e.Name)) + e.Size + 16
			}
			want += 46 + int64(len(e.Name))
		}
		want += 22
		return int64(len(after) - len(before)), want
	}

	small, wantSmall := growth(4)
	large, wantLarge := growth(4000)
	if small != wantSmall || large != wantLarge {
		t.Errorf("bytes appended = %d / %d, the records account for %d / %d", small, large, wantSmall, wantLarge)
	}
	if small != large {
		t.Errorf("bytes appended grew with the archive: %d over tiny shards, %d over large ones", small, large)
	}
}

// TestShardAppend_ParityWithRewrite: every add the append takes
// produces the archive a create of the same shards does — entry order,
// sizes, CRC32s, the refreshed `_schema.pulse` (dictionary union,
// aggregate, shard count) — and an add that moves the archive (a set
// rung widen) still rewrites it, with its mandatory warning.
func TestShardAppend_ParityWithRewrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		seed    []byte
		arrival []byte
		append  bool
	}{
		{"same_dictionary",
			writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "radio"}, [][2]uint64{{1, 0b01}, {2, 0b11}}),
			writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "radio"}, [][2]uint64{{3, 0b10}}),
			true},
		{"set_dictionary_extends_within_rung",
			writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "radio"}, [][2]uint64{{1, 0b01}, {2, 0b11}}),
			writeSetShard(t, encoding.FieldTypeSetU8, []string{"radio", "web"}, [][2]uint64{{3, 0b11}}),
			true},
		{"set_rung_widens",
			writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "radio"}, [][2]uint64{{1, 0b01}}),
			writeSetShard(t, encoding.FieldTypeSetU16, []string{"tv", "podcast"}, [][2]uint64{{5, 0b10}}),
			false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := fs.NewMemMap()
			fsys := cfg.Fs()
			svc := New(cfg)
			for n, d := range map[string][]byte{"a.pulse": tc.seed, "b.pulse": tc.seed, "c.pulse": tc.arrival} {
				if err := afero.WriteFile(fsys, n, d, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			created, err := svc.CreateShardArchive(ctx, "created.pulse", []string{"a.pulse", "b.pulse", "c.pulse"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
				t.Fatal(err)
			}
			before := gReadFile(t, fsys, "arch.pulse")
			added, err := svc.AddShard(ctx, "arch.pulse", "c.pulse")
			if err != nil {
				t.Fatalf("AddShard: %v", err)
			}
			after := gReadFile(t, fsys, "arch.pulse")
			if got := bytes.HasPrefix(after, before); got != tc.append {
				t.Fatalf("appended in place = %v, want %v", got, tc.append)
			}
			sameArchiveContent(t, after, gReadFile(t, fsys, "created.pulse"))
			if fmt.Sprint(cohesionCodes(added.Warnings)) != fmt.Sprint(cohesionCodes(created.Warnings)) {
				t.Errorf("add warned %v, create %v", cohesionCodes(added.Warnings), cohesionCodes(created.Warnings))
			}
			if !tc.append && (len(added.Widened) == 0 || !added.Widened[0].ArchiveWidened()) {
				t.Errorf("rung widen not reported: %+v", added.Widened)
			}
			gVerifyClean(t, svc, "arch.pulse")
		})
	}
}

// TestShardAppend_ReaderOpenedBeforeKeepsItsView: a reader that parsed
// the archive before the append still reads every pre-append shard and
// the old `_schema.pulse` — its directory points only at bytes the
// append never touches — while a reader opened after sees the new
// shard. Real files (t.TempDir), so the append extends a file another
// handle holds open.
func TestShardAppend_ReaderOpenedBeforeKeepsItsView(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := fs.New(fs.WithFs(afero.NewBasePathFs(afero.NewOsFs(), dir)))
	if err != nil {
		t.Fatal(err)
	}
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	for i, n := range []string{"a.pulse", "b.pulse", "c.pulse"} {
		adminWriteShardFile(t, fsys, n, schema, streamShardRecords(i*100, 20))
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	want := archiveEntriesByName(t, gReadFile(t, fsys, "arch.pulse"))

	old, err := openArchiveFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := svc.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatalf("AddShard under an open reader: %v", err)
	}
	if st, _ := fsys.Stat(filepath.Clean("arch.pulse")); st.Size() <= old.size {
		t.Fatal("fixture: the archive did not grow")
	}
	entries := old.arch.Entries()
	if len(entries) != len(want) {
		t.Fatalf("old view lists %d entries, want %d", len(entries), len(want))
	}
	for _, e := range entries {
		got, err := readEntryBytes(old.arch, e.Name)
		if err != nil {
			t.Fatalf("old view reading %s: %v", e.Name, err)
		}
		if !bytes.Equal(got, want[e.Name]) {
			t.Errorf("old view of %s changed under the append", e.Name)
		}
	}
	shards, err := svc.ListShards(ctx, "arch.pulse")
	if err != nil || len(shards) != 3 {
		t.Errorf("a reader opened after sees %d shards, %v; want 3", len(shards), err)
	}
}

// TestShardAppend_WriteOpenRefusedIsCoded: the append opens the archive
// for writing; a platform that refuses (a read-only file, Windows share
// mode) fails the add with PULSE_SHARD_APPEND_REFUSED — whose fixup
// names the staged rewrite — and leaves the archive, every sidecar and
// the index manifest untouched, no upkeep stage behind.
func TestShardAppend_WriteOpenRefusedIsCoded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, fsys := setupArchiveLookup(t, upkeepKeySets...)
	writeUpkeepShard(t, fsys, "e.pulse", 80)
	before := snapshotFiles(t, fsys)

	cfg, err := fs.New(fs.WithFs(&failingWriteFs{Fs: fsys, fail: func(n string) bool { return n == "arch.pulse" }}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).AddShard(ctx, "arch.pulse", "e.pulse")
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SHARD_APPEND_REFUSED {
		t.Fatalf("err = %v, want PULSE_SHARD_APPEND_REFUSED", err)
	}
	if ce.Details["reason"] != "open_for_write" || ce.Details["archive"] != "arch.pulse" || ce.Details["shard"] != "e.pulse" {
		t.Errorf("details = %v", ce.Details)
	}
	if d := diffSnapshots(before, snapshotFiles(t, fsys)); len(d) != 0 {
		t.Errorf("a refused append changed %v", d)
	}
	lr, ok := errors.Lookup(string(errors.PULSE_SHARD_APPEND_REFUSED))
	if !ok || len(lr.Fixups) == 0 || !strings.Contains(lr.Fixups[0].Hint, "--rewrite") {
		t.Errorf("fixups = %+v, want one naming --rewrite", lr.Fixups)
	}
}

// TestShardAppend_IndexUpkeepAfterAppend: the append keeps every
// archive index in step — the new shard's sidecar and Bloom filter
// (in the index's Bloom set) built from the bytes the append stores, identical to a fresh build —
// and lookups stay fresh after the archive's size and bytes moved.
func TestShardAppend_IndexUpkeepAfterAppend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, upkeepKeySets...)
	writeUpkeepShard(t, fsys, "e.pulse", 80)
	before := gReadFile(t, fsys, "arch.pulse")
	res, err := svc.AddShard(ctx, "arch.pulse", "e.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(gReadFile(t, fsys, "arch.pulse"), before) {
		t.Fatal("fixture: the add did not append")
	}
	if codes := cohesionCodes(res.Warnings); len(codes) != 1 || codes[0] != string(errors.PULSE_SHARD_INDEXES_UPDATED) {
		t.Fatalf("warnings = %v, want PULSE_SHARD_INDEXES_UPDATED", codes)
	}
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "9"), ""); got != `[{"region":"west","score":80}]` {
		t.Errorf("lookup of the appended shard = %s", got)
	}
	if v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"region", "id"}); err != nil || !v.Fresh {
		t.Errorf("verify after append = %+v, %v; want fresh", v, err)
	}
	rebuildMatches(t, svc, fsys, []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse", "e.pulse"})
}

// TestShardAppend_NotAppendableTailRewrites: an archive whose tail the
// appender does not extend (here: bytes after the EOCD) is rewritten,
// not refused — and the rewrite leaves a clean archive.
func TestShardAppend_NotAppendableTailRewrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	adminWriteShardFile(t, fsys, "a.pulse", schema, [][]uint64{{1, math.Float64bits(1)}})
	adminWriteShardFile(t, fsys, "b.pulse", schema, [][]uint64{{2, math.Float64bits(2)}})
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	padded := append(gReadFile(t, fsys, "arch.pulse"), 0, 0, 0, 0)
	if err := afero.WriteFile(fsys, "arch.pulse", padded, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); err != nil {
		t.Fatalf("AddShard over a padded tail: %v", err)
	}
	after := gReadFile(t, fsys, "arch.pulse")
	if bytes.HasPrefix(after, padded) {
		t.Fatal("a tail with trailing bytes was appended to")
	}
	if _, err := svc.CreateShardArchive(ctx, "created.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, gReadFile(t, fsys, "created.pulse")) {
		t.Error("the fallback rewrite differs from a create")
	}
}
