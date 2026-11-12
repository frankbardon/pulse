package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// U73 #720: a carried distinct count is keyed on the shard's identity
// (size + CRC-32 from the central directory), never its entry name
// alone. A shard entry replaced OUTSIDE Pulse under the same name — what
// a zip tool does — keeps its name and its old trailer entry; every
// writer must recount it (its new keys, not the old figures) while still
// carrying every unchanged shard without a read. The "carried, not
// recounted" proof is a DOCTORED count: an unchanged shard's entry is
// rewritten to a figure no recount could produce (identity intact), and
// must survive the write.

// rebuildArchive rewrites the zip at path entry by entry, as an external
// zip tool would: `_schema.pulse` becomes doc when non-nil, a shard named
// in replace gets those bytes under the same name, every other entry
// keeps its bytes.
func rebuildArchive(t *testing.T, fsys afero.Fs, path string, doc []byte, replace map[string][]byte) {
	t.Helper()
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		payload, ok := replace[f.Name]
		if f.Name == encx.ReservedSchemaName && doc != nil {
			payload, ok = doc, true
		}
		if !ok {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			if payload, err = io.ReadAll(rc); err != nil {
				t.Fatal(err)
			}
			_ = rc.Close()
		}
		if err := writeArchiveEntry(zw, f.Name, payload); err != nil {
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

// doctorCounts rewrites the archive's DIST trailer so shard's entry
// reads counts — its recorded identity untouched, so a carry keeps them.
func doctorCounts(t *testing.T, fsys afero.Fs, path, shard string, counts ...encx.DistinctCount) {
	t.Helper()
	doc := schemaDocFromArchive(t, fsys, path)
	d := readDistinct(t, fsys, path)
	found := false
	for i := range d.Shards {
		if d.Shards[i].Name == shard {
			copy(d.Shards[i].Counts, counts)
			found = true
		}
	}
	if !found {
		t.Fatalf("no trailer entry for %s", shard)
	}
	var b bytes.Buffer
	if err := encx.WriteSchemaDocDistinct(&b, doc.Schema, doc.AggregateRecordCount, doc.ShardCount, d); err != nil {
		t.Fatal(err)
	}
	rebuildArchive(t, fsys, path, b.Bytes(), nil)
}

// wantIdentitiesMatchDirectory asserts every trailer entry records its
// shard's central-directory identity.
func wantIdentitiesMatchDirectory(t *testing.T, what string, fsys afero.Fs, path string) {
	t.Helper()
	d := readDistinct(t, fsys, path)
	a, err := openArchiveFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ident := archiveShardIdentities(a)
	for _, sh := range d.Shards {
		if want := encx.EntryIdentity(ident[sh.Name]); sh.Identity != want {
			t.Errorf("%s: %s records identity %+v, directory says %+v", what, sh.Name, sh.Identity, want)
		}
	}
}

// predictBound is the id-field bound predict reads (descx.ArchiveDistinctOf
// — the reader the run's pre-flight shares).
func predictBound(t *testing.T, fsys afero.Fs, path string) (uint64, bool) {
	t.Helper()
	a, err := openArchiveFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	return descx.ArchiveDistinctOf(a.arch).FieldBound("id")
}

// The replaced b.pulse: 20 rows, 11 ids (400..410), 3 scores — every
// figure differs from the original b's 5 / 7.
var distReplacedB = [2]encx.DistinctCount{exact(11), exact(3)}

// doctoredA is a figure no recount of a.pulse (3 / 4) produces.
var doctoredA = [2]encx.DistinctCount{exact(42), exact(43)}

func replaceShardB(t *testing.T, fsys afero.Fs, path string) {
	t.Helper()
	repl := writeSinglePulse(t, adminScoreSchema(), distinctRecords(400, 20, 11, 3))
	rebuildArchive(t, fsys, path, nil, map[string][]byte{"b.pulse": repl})
}

func TestShardDistinct_ReplacedShardIsRecountedNotCarried(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name    string
		created []string
		op      func(*Service) error
		want    map[string][2]encx.DistinctCount
		order   []string
	}{
		{"add (append)", []string{"a.pulse", "b.pulse"},
			func(s *Service) error { _, err := s.AddShard(ctx, "arch.pulse", "c.pulse"); return err },
			map[string][2]encx.DistinctCount{"a.pulse": doctoredA, "b.pulse": distReplacedB, "c.pulse": distC},
			[]string{"a.pulse", "b.pulse", "c.pulse"}},
		{"add (rewrite)", []string{"a.pulse", "b.pulse"},
			func(s *Service) error {
				_, err := s.AddShardWithOptions(ctx, "arch.pulse", "c.pulse", AddShardOptions{Rewrite: true})
				return err
			},
			map[string][2]encx.DistinctCount{"a.pulse": doctoredA, "b.pulse": distReplacedB, "c.pulse": distC},
			[]string{"a.pulse", "b.pulse", "c.pulse"}},
		{"remove", []string{"a.pulse", "b.pulse", "c.pulse"},
			func(s *Service) error { _, err := s.RemoveShard(ctx, "arch.pulse", "c.pulse"); return err },
			map[string][2]encx.DistinctCount{"a.pulse": doctoredA, "b.pulse": distReplacedB},
			[]string{"a.pulse", "b.pulse"}},
		{"compact", []string{"a.pulse", "b.pulse"},
			func(s *Service) error { return s.CompactShardArchive(ctx, "arch.pulse") },
			map[string][2]encx.DistinctCount{"a.pulse": doctoredA, "b.pulse": distReplacedB},
			[]string{"a.pulse", "b.pulse"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			svc, fsys := distinctEnv(t)
			if _, err := svc.CreateShardArchive(ctx, "arch.pulse", c.created); err != nil {
				t.Fatal(err)
			}
			doctorCounts(t, fsys, "arch.pulse", "a.pulse", doctoredA[:]...)
			if got, ok := predictBound(t, fsys, "arch.pulse"); !ok || got != 42+5+uint64(len(c.created)-2)*6 {
				t.Fatalf("doctored archive bound = %d, %v", got, ok)
			}
			replaceShardB(t, fsys, "arch.pulse")
			// Before any Pulse write, predict already refuses the stale
			// entry: no bound rather than b's old 5 ids.
			if got, ok := predictBound(t, fsys, "arch.pulse"); ok {
				t.Fatalf("predict bounds a replaced shard by its old counts: %d", got)
			}

			if err := c.op(svc); err != nil {
				t.Fatal(err)
			}
			wantDistinct(t, c.name, readDistinct(t, fsys, "arch.pulse"), c.want, c.order...)
			wantIdentitiesMatchDirectory(t, c.name, fsys, "arch.pulse")
			var sum uint64
			for _, n := range c.order {
				sum += c.want[n][0].Count
			}
			if got, ok := predictBound(t, fsys, "arch.pulse"); !ok || got != sum {
				t.Errorf("predict bound after %s = %d, %v; want %d (b's NEW 11 ids)", c.name, got, ok, sum)
			}
		})
	}
}

// The re-encoding add (an archive-level set widen re-lays-out every
// stored shard): `_schema.pulse` is written before any shard, so the
// identity of each re-encoded shard comes from a pre-pass. It must be
// the identity the published archive's directory holds — a later
// compact then carries every entry (the doctored seed count survives),
// where a wrong stamp would make every shard stale and recounted.
func TestShardDistinct_WideningAddStampsReencodedIdentities(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, fsys := setWidenFixture(t)
	dict := []string{"tv", "radio", "print", "web", "mail"}
	other := writeSetShard(t, encoding.FieldTypeSetU8, dict, [][2]uint64{{10, 1}, {11, 2}})
	if err := afero.WriteFile(fsys, "other.pulse", other, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddShardWithOptions(ctx, "arch.pulse", "other.pulse", AddShardOptions{Rewrite: true}); err != nil {
		t.Fatal(err)
	}
	doctorCounts(t, fsys, "arch.pulse", "seed.pulse", exact(42))
	// other.pulse replaced under its name: 4 ids instead of 2.
	repl := writeSetShard(t, encoding.FieldTypeSetU8, dict, [][2]uint64{{20, 1}, {21, 2}, {22, 4}, {23, 8}})
	rebuildArchive(t, fsys, "arch.pulse", nil, map[string][]byte{"other.pulse": repl})

	res, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if res.Widened == nil {
		t.Fatal("the add did not widen the archive: the re-encoding arm was not exercised")
	}
	check := func(what string) {
		t.Helper()
		d := readDistinct(t, fsys, "arch.pulse")
		for shard, want := range map[string]uint64{"seed.pulse": 42, "other.pulse": 4, "add.pulse": 2} {
			if got := d.Lookup(shard, "id"); got != exact(want) {
				t.Errorf("%s: %s id = %+v, want exact %d", what, shard, got, want)
			}
		}
		wantIdentitiesMatchDirectory(t, what, fsys, "arch.pulse")
	}
	check("widening add")
	if err := svc.CompactShardArchive(ctx, "arch.pulse"); err != nil {
		t.Fatal(err)
	}
	check("compact after the widening add")
}

// v1DistinctTrailer hand-encodes d in the version-1 layout (no
// identities) — every build of the trailer before U73 #720 wrote it.
func v1DistinctTrailer(d *encx.ArchiveDistinct) []byte {
	le := binary.LittleEndian
	var body bytes.Buffer
	name := func(s string) {
		_ = binary.Write(&body, le, uint16(len(s)))
		body.WriteString(s)
	}
	_ = binary.Write(&body, le, uint32(len(d.Fields)))
	for _, f := range d.Fields {
		name(f.Name)
		body.WriteByte(byte(f.Type))
	}
	_ = binary.Write(&body, le, uint32(len(d.Shards)))
	for _, sh := range d.Shards {
		name(sh.Name)
		for _, c := range sh.Counts {
			body.WriteByte(byte(c.Status))
			_ = binary.Write(&body, le, c.Count)
		}
	}
	out := append([]byte("DIST"), 1)
	out = le.AppendUint64(out, uint64(body.Len()))
	return append(out, body.Bytes()...)
}

// OLD-TRAILER compatibility: a version-1 trailer still reads, but its
// counts carry no identity — never trusted. Predict refuses them, and
// an add (which reads no stored shard it can carry) recounts them, so
// the archive ends with true counts and identities.
func TestShardDistinct_Version1TrailerIsRecounted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, fsys := distinctEnv(t)
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	doc := schemaDocFromArchive(t, fsys, "arch.pulse")
	d := readDistinct(t, fsys, "arch.pulse")
	d.Shards[0].Counts[0] = exact(42) // a stale figure a v1 trailer could hold
	var b bytes.Buffer
	if err := encx.WriteSchemaDoc(&b, doc.Schema, doc.AggregateRecordCount, doc.ShardCount); err != nil {
		t.Fatal(err)
	}
	b.Write(v1DistinctTrailer(d))
	rebuildArchive(t, fsys, "arch.pulse", b.Bytes(), nil)
	if v1 := readDistinct(t, fsys, "arch.pulse"); v1 == nil || v1.Shards[0].Identity.Known {
		t.Fatalf("v1 trailer read as %+v", v1)
	}
	if got, ok := predictBound(t, fsys, "arch.pulse"); ok {
		t.Fatalf("predict bounds by identity-less v1 counts: %d", got)
	}
	if _, err := svc.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatal(err)
	}
	wantDistinct(t, "add over a v1 trailer", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": distA, "b.pulse": distB, "c.pulse": distC},
		"a.pulse", "b.pulse", "c.pulse")
	wantIdentitiesMatchDirectory(t, "add over a v1 trailer", fsys, "arch.pulse")
	if got, ok := predictBound(t, fsys, "arch.pulse"); !ok || got != 14 {
		t.Errorf("bound after the add = %d, %v; want 14", got, ok)
	}
}
