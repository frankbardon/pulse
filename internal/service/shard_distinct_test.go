package service

import (
	"archive/zip"
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// Per-shard distinct counts (U73 #500): every archive writer records,
// per shard and per eligible field, the exact distinct stored words in
// the `_schema.pulse` DIST trailer. Each writer path gets a test that a
// STALE or COPIED stat fails: the fixtures give every shard counts that
// differ from its record count and from every other shard's.

// distinctRecords returns n records whose id cycles through k values
// (offset by base) and whose score cycles through k2.
func distinctRecords(base, n, k, k2 int) [][]uint64 {
	recs := make([][]uint64, n)
	for i := range recs {
		recs[i] = []uint64{uint64(base + i%k), math.Float64bits(float64(i%k2) + 0.5)}
	}
	return recs
}

// distinctEnv writes shards a (12 rows: 3 ids, 4 scores), b (20 rows: 5
// ids, 7 scores) and c (30 rows: 6 ids, 2 scores) to a fresh memmap fs.
func distinctEnv(t *testing.T) (*Service, afero.Fs) {
	t.Helper()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	schema := adminScoreSchema()
	adminWriteShardFile(t, fsys, "a.pulse", schema, distinctRecords(0, 12, 3, 4))
	adminWriteShardFile(t, fsys, "b.pulse", schema, distinctRecords(100, 20, 5, 7))
	adminWriteShardFile(t, fsys, "c.pulse", schema, distinctRecords(200, 30, 6, 2))
	return New(cfg), fsys
}

// readDistinct decodes an archive's DIST trailer.
func readDistinct(t *testing.T, fsys afero.Fs, path string) *encx.ArchiveDistinct {
	t.Helper()
	a, err := openArchiveFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rc, err := a.arch.Open(encx.ReservedSchemaName)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	_, dist, err := encx.ReadSchemaDocDistinct(rc)
	if err != nil {
		t.Fatalf("ReadSchemaDocDistinct: %v", err)
	}
	return dist
}

func exact(n uint64) encx.DistinctCount {
	return encx.DistinctCount{Status: encx.DistinctExact, Count: n}
}

// wantDistinct asserts the trailer lists exactly shards (in order) with
// the given (id, score) entries.
func wantDistinct(t *testing.T, what string, d *encx.ArchiveDistinct, want map[string][2]encx.DistinctCount, order ...string) {
	t.Helper()
	if d == nil {
		t.Fatalf("%s: no DIST trailer", what)
	}
	if len(d.Fields) != 2 || d.Fields[0].Name != "id" || d.Fields[1].Name != "score" {
		t.Fatalf("%s: fields = %+v, want id, score", what, d.Fields)
	}
	if len(d.Shards) != len(order) {
		t.Fatalf("%s: %d shard entries, want %d (%v)", what, len(d.Shards), len(order), order)
	}
	for i, name := range order {
		sh := d.Shards[i]
		if sh.Name != name {
			t.Fatalf("%s: shard %d = %q, want %q", what, i, sh.Name, name)
		}
		w := want[name]
		if sh.Counts[0] != w[0] || sh.Counts[1] != w[1] {
			t.Errorf("%s: shard %s counts = %+v, want %+v", what, name, sh.Counts, w)
		}
	}
}

var (
	distA = [2]encx.DistinctCount{exact(3), exact(4)}
	distB = [2]encx.DistinctCount{exact(5), exact(7)}
	distC = [2]encx.DistinctCount{exact(6), exact(2)}
)

func TestShardDistinct_CreateCountsEveryShard(t *testing.T) {
	t.Parallel()
	svc, fsys := distinctEnv(t)
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{"a.pulse", "b.pulse", "c.pulse"}); err != nil {
		t.Fatal(err)
	}
	d := readDistinct(t, fsys, "arch.pulse")
	wantDistinct(t, "create", d, map[string][2]encx.DistinctCount{"a.pulse": distA, "b.pulse": distB, "c.pulse": distC},
		"a.pulse", "b.pulse", "c.pulse")
	// The sum over shards is the archive bound (ids are disjoint here,
	// so it is also the union).
	if got, ok := d.FieldBound("id"); !ok || got != 14 {
		t.Errorf("FieldBound(id) = %d, %v; want 14, true", got, ok)
	}
	// Each entry records the identity of the bytes stored (U73 #720),
	// learnt in the counting read.
	wantIdentitiesMatchDirectory(t, "create", fsys, "arch.pulse")
}

func TestShardDistinct_AddCountsTheArrivalAndCarriesTheRest(t *testing.T) {
	t.Parallel()
	for _, rewrite := range []bool{false, true} {
		svc, fsys := distinctEnv(t)
		ctx := context.Background()
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AddShardWithOptions(ctx, "arch.pulse", "c.pulse", AddShardOptions{Rewrite: rewrite}); err != nil {
			t.Fatal(err)
		}
		what := map[bool]string{false: "add (append)", true: "add (rewrite)"}[rewrite]
		wantDistinct(t, what, readDistinct(t, fsys, "arch.pulse"),
			map[string][2]encx.DistinctCount{"a.pulse": distA, "b.pulse": distB, "c.pulse": distC},
			"a.pulse", "b.pulse", "c.pulse")
		wantIdentitiesMatchDirectory(t, what, fsys, "arch.pulse")
	}
}

// A shard removed, then a DIFFERENT shard added under its name: the
// removed shard's entry must go with it, and the newcomer must be counted
// from its own data, never inherit the old name's figures.
func TestShardDistinct_RemoveDropsTheEntryAndNamesNeverCarry(t *testing.T) {
	t.Parallel()
	svc, fsys := distinctEnv(t)
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse", "c.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveShard(ctx, "arch.pulse", "b.pulse"); err != nil {
		t.Fatal(err)
	}
	wantDistinct(t, "remove", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": distA, "c.pulse": distC}, "a.pulse", "c.pulse")

	adminWriteShardFile(t, fsys, "b.pulse", adminScoreSchema(), distinctRecords(300, 9, 9, 1))
	if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); err != nil {
		t.Fatal(err)
	}
	wantDistinct(t, "re-add under a removed name", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": distA, "c.pulse": distC, "b.pulse": {exact(9), exact(1)}},
		"a.pulse", "c.pulse", "b.pulse")
}

// writeLegacyArchive writes an archive the way a build without the DIST
// trailer did: `_schema.pulse` ending at SHRD, then the shards.
func writeLegacyArchive(t *testing.T, fsys afero.Fs, path string, names ...string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var agg uint64
	payloads := make([][]byte, len(names))
	for i, n := range names {
		b, err := afero.ReadFile(fsys, n)
		if err != nil {
			t.Fatal(err)
		}
		payloads[i] = b
		c, err := recordCountFromBytes(b, adminScoreSchema())
		if err != nil {
			t.Fatal(err)
		}
		agg += uint64(c)
	}
	var doc bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, adminScoreSchema(), agg, uint16(len(names))); err != nil {
		t.Fatal(err)
	}
	if err := writeArchiveEntry(zw, encx.ReservedSchemaName, doc.Bytes()); err != nil {
		t.Fatal(err)
	}
	for i, n := range names {
		if err := writeArchiveEntry(zw, n, payloads[i]); err != nil {
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

var absent = [2]encx.DistinctCount{}

// An archive written before the trailer: add (append) and remove never
// fabricate counts for shards they do not read — those stay absent, and
// the archive has no bound — while the arrival is counted. Compact is the
// backfill: it counts every uncovered shard.
func TestShardDistinct_LegacyArchiveNeverFabricatesAndCompactBackfills(t *testing.T) {
	t.Parallel()
	svc, fsys := distinctEnv(t)
	ctx := context.Background()
	writeLegacyArchive(t, fsys, "arch.pulse", "a.pulse", "b.pulse")
	if d := readDistinct(t, fsys, "arch.pulse"); d != nil {
		t.Fatalf("legacy archive reads a trailer: %+v", d)
	}
	if _, err := svc.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatal(err)
	}
	d := readDistinct(t, fsys, "arch.pulse")
	wantDistinct(t, "add over legacy", d,
		map[string][2]encx.DistinctCount{"a.pulse": absent, "b.pulse": absent, "c.pulse": distC},
		"a.pulse", "b.pulse", "c.pulse")
	if _, ok := d.FieldBound("id"); ok {
		t.Error("FieldBound over absent shards reported a bound")
	}
	if _, err := svc.RemoveShard(ctx, "arch.pulse", "b.pulse"); err != nil {
		t.Fatal(err)
	}
	wantDistinct(t, "remove over legacy", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": absent, "c.pulse": distC}, "a.pulse", "c.pulse")
	if err := svc.CompactShardArchive(ctx, "arch.pulse"); err != nil {
		t.Fatal(err)
	}
	wantDistinct(t, "compact backfill", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": distA, "c.pulse": distC}, "a.pulse", "c.pulse")
}

// Compact over a counted archive carries every entry (the bytes are
// copied raw) and re-emits the archive byte-identically.
func TestShardDistinct_CompactPreserves(t *testing.T) {
	t.Parallel()
	svc, fsys := distinctEnv(t)
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadFile(fsys, "arch.pulse")
	if err := svc.CompactShardArchive(ctx, "arch.pulse"); err != nil {
		t.Fatal(err)
	}
	after, _ := afero.ReadFile(fsys, "arch.pulse")
	if !bytes.Equal(before, after) {
		t.Error("compact of a current archive is not byte-identical")
	}
	wantDistinct(t, "compact", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": distA, "b.pulse": distB}, "a.pulse", "b.pulse")
}

// Filter-to-file writes new shard bytes: each is counted fresh, so a
// filter that drops values shrinks the counts.
func TestShardDistinct_FilterToFileCountsTheFilteredShards(t *testing.T) {
	t.Parallel()
	svc, fsys := distinctEnv(t)
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "c.pulse"}); err != nil {
		t.Fatal(err)
	}
	// a: ids 0..2 → keeps 0 (scores of rows 0,3,6,9: 0.5,3.5,2.5,1.5);
	// c: ids 200..205 → keeps 200 (rows 0,6,..,24: score 0.5 only).
	if _, err := svc.FilterToFile(ctx, "arch.pulse", "out.pulse", "id == 0 || id == 200"); err != nil {
		t.Fatal(err)
	}
	wantDistinct(t, "filter-to-file", readDistinct(t, fsys, "out.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": {exact(1), exact(4)}, "c.pulse": {exact(1), exact(1)}},
		"a.pulse", "c.pulse")
	wantIdentitiesMatchDirectory(t, "filter-to-file", fsys, "out.pulse")
}

func TestShardDistinct_OptOutAndLimitAreDistinguishable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	svc, fsys := distinctEnv(t)
	svc.SetShardDistinct(0, true)
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	opted := encx.DistinctCount{Status: encx.DistinctOptedOut}
	wantDistinct(t, "opted out", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": {opted, opted}, "b.pulse": {opted, opted}}, "a.pulse", "b.pulse")

	svc, fsys = distinctEnv(t)
	svc.SetShardDistinct(4, false)
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	over := encx.DistinctCount{Status: encx.DistinctOverLimit, Count: 4}
	wantDistinct(t, "limit 4", readDistinct(t, fsys, "arch.pulse"),
		map[string][2]encx.DistinctCount{"a.pulse": {exact(3), exact(4)}, "b.pulse": {over, over}}, "a.pulse", "b.pulse")
}

// Single files get nothing and the stored shards are the sources'
// bytes: the trailer is appended after SHRD in `_schema.pulse` alone,
// whose `.pulse` version byte stays 0x01 for an ungrouped schema and
// whose bytes up to SHRD are exactly the pre-trailer WriteSchemaDoc's.
func TestShardDistinct_VersionByteAndShardBytesUntouched(t *testing.T) {
	t.Parallel()
	svc, fsys := distinctEnv(t)
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.pulse", "b.pulse"} {
		src, _ := afero.ReadFile(fsys, n)
		if got := streamEntryBytes(t, fsys, "arch.pulse", n); !bytes.Equal(got, src) {
			t.Errorf("stored shard %s differs from its source", n)
		}
	}
	doc := streamEntryBytes(t, fsys, "arch.pulse", encx.ReservedSchemaName)
	if doc[encoding.HeaderSize-1] != encoding.FormatVersionV1 {
		t.Errorf("_schema.pulse version byte = %#x, want 0x01", doc[encoding.HeaderSize-1])
	}
	var legacy bytes.Buffer
	if err := encx.WriteSchemaDoc(&legacy, adminScoreSchema(), 32, 2); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(doc, legacy.Bytes()) || len(doc) == legacy.Len() {
		t.Error("_schema.pulse is not the pre-trailer bytes followed by a trailer")
	}
	if !bytes.Equal(doc[legacy.Len():legacy.Len()+4], encx.DistinctTrailerMagic[:]) {
		t.Error("the trailer does not start with DIST right after SHRD")
	}
}

// A grouped (0x02) archive counts the LOGICAL values — group members
// decoded through the canonical group dictionaries — so its counts equal
// the flat twin's.
func TestShardDistinct_GroupedArchiveMatchesFlatTwin(t *testing.T) {
	t.Parallel()
	ga, gb := gShardA(), gShardB()
	flatA, groupedA, _ := ga.build(t, nil)
	flatB, groupedB, _ := gb.build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{
		"fa/a.pulse": flatA, "fb/b.pulse": flatB,
		"ga/a.pulse": groupedA, "gb/b.pulse": groupedB,
	})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "flat.pulse", []string{"fa/a.pulse", "fb/b.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateShardArchive(ctx, "grouped.pulse", []string{"ga/a.pulse", "gb/b.pulse"}); err != nil {
		t.Fatal(err)
	}
	flat, grouped := readDistinct(t, fsys, "flat.pulse"), readDistinct(t, fsys, "grouped.pulse")
	wantIdentitiesMatchDirectory(t, "flat create", fsys, "flat.pulse")
	wantIdentitiesMatchDirectory(t, "grouped create", fsys, "grouped.pulse")
	for _, f := range []string{"id", "parent", "weight", "amount"} {
		for _, sh := range []string{"a.pulse", "b.pulse"} {
			fc, gc := flat.Lookup(sh, f), grouped.Lookup(sh, f)
			if fc.Status != encx.DistinctExact || fc != gc {
				t.Errorf("%s/%s: flat %+v, grouped %+v", sh, f, fc, gc)
			}
		}
	}
	// parent spans 0..29 in a, 20..44 in b.
	if got := grouped.Lookup("a.pulse", "parent"); got != exact(30) {
		t.Errorf("a.pulse parent = %+v, want exact 30", got)
	}
	if got := grouped.Lookup("b.pulse", "parent"); got != exact(25) {
		t.Errorf("b.pulse parent = %+v, want exact 25", got)
	}
}
