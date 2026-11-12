package service

import (
	"context"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// archive_lookup_prune_test.go pins the per-shard key-range prune
// (archive storage #493): the archive index manifest records each
// shard's indexed-key range, a lookup never opens the sidecar of a
// shard whose range excludes the key and never loses a match by it, a
// manifest without the ranges probes every shard, and VerifyIndex
// checks each recorded range against its sidecar. These tests isolate
// the RANGE prune: setupPruneArchive deletes every Bloom file — the
// archive Bloom sets and any per-shard sidecar (no Bloom means probe);
// archive_lookup_bloom_test.go pins the Bloom prune on top of it.

// sidecarOpenFs counts Opens of archive shard sidecars.
type sidecarOpenFs struct {
	afero.Fs
	mu     sync.Mutex
	opened []string
}

func (c *sidecarOpenFs) note(name string) {
	if strings.HasPrefix(name, "arch.pulse#") && strings.HasSuffix(name, ".idx") {
		c.mu.Lock()
		c.opened = append(c.opened, name)
		c.mu.Unlock()
	}
}

func (c *sidecarOpenFs) Open(name string) (afero.File, error) {
	c.note(name)
	return c.Fs.Open(name)
}

func (c *sidecarOpenFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	c.note(name)
	return c.Fs.OpenFile(name, flag, perm)
}

// take returns the shards whose sidecar (for keys) was opened since the
// last take, sorted and deduplicated, and resets the count.
func (c *sidecarOpenFs) take(keys []string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	for _, p := range c.opened {
		for _, name := range []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse", "e.pulse"} {
			if p == encx.ArchiveShardIndexPath("arch.pulse", name, keys) {
				seen[name] = true
			}
		}
	}
	c.opened = nil
	out := []string{}
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func setupPruneArchive(t *testing.T) (*Service, afero.Fs, *sidecarOpenFs) {
	t.Helper()
	svc, fsys, counter := setupBloomArchive(t)
	removeShardBlooms(t, fsys)
	return svc, fsys, counter
}

// removeShardBlooms deletes every Bloom file under the root: archive
// Bloom sets and legacy per-shard Bloom sidecars.
func removeShardBlooms(t *testing.T, fsys afero.Fs) {
	t.Helper()
	n := 0
	for p := range snapshotFiles(t, fsys) {
		if strings.HasSuffix(p, encx.ShardBloomSuffix) || strings.HasSuffix(p, encx.ArchiveBloomSetSuffix) {
			if err := fsys.Remove(p); err != nil {
				t.Fatal(err)
			}
			n++
		}
	}
	if n == 0 {
		t.Fatal("fixture: no Bloom sidecar to remove")
	}
}

// setupBloomArchive is the prune fixture with every Bloom sidecar the
// build wrote left in place.
func setupBloomArchive(t *testing.T) (*Service, afero.Fs, *sidecarOpenFs) {
	t.Helper()
	counter := &sidecarOpenFs{Fs: afero.NewMemMapFs()}
	cfg, err := fs.New(fs.WithFs(counter))
	if err != nil {
		t.Fatal(err)
	}
	svc, fsys := setupArchiveLookupOn(t, cfg, []string{"id"}, []string{"region"}, []string{"region", "id"})
	counter.take(nil)
	return svc, fsys, counter
}

// rewriteArchiveManifest applies edit to every shard record of the
// archive's index over keys and writes the manifest back.
func rewriteArchiveManifest(t *testing.T, fsys afero.Fs, keys []string, edit func(sh *encx.IndexManifestShard)) {
	t.Helper()
	m, _, err := encx.ReadIndexManifest(fsys, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	base := encx.SidecarIndexPath("arch.pulse", keys)
	for i := range m.Indexes {
		if m.Indexes[i].IndexPath != base {
			continue
		}
		for j := range m.Indexes[i].Shards {
			edit(&m.Indexes[i].Shards[j])
		}
	}
	if err := encx.WriteIndexManifest(fsys, "arch.pulse", m); err != nil {
		t.Fatal(err)
	}
}

func archiveShardRecords(t *testing.T, fsys afero.Fs, keys []string) map[string]encx.IndexManifestShard {
	t.Helper()
	m, _, err := encx.ReadIndexManifest(fsys, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]encx.IndexManifestShard{}
	base := encx.SidecarIndexPath("arch.pulse", keys)
	for _, e := range m.Indexes {
		if e.IndexPath == base {
			for _, sh := range e.Shards {
				out[sh.Name] = sh
			}
		}
	}
	return out
}

// pruneCases: id ranges are a [1, 2^53], b [5, 2^53+1], c none (empty),
// d [8, 8]; region ids (canonical north=0, south=1, east=2) are a
// [0, 1], b [1, 2], d [0, 0].
var pruneCases = []struct {
	name   string
	keys   []types.LookupKey
	probed []string
}{
	{"id 1", lk("id", "1"), []string{"a.pulse", "c.pulse"}},
	{"id 2^53", lk("id", strconv.FormatUint(alU64Lo, 10)), []string{"a.pulse", "b.pulse", "c.pulse"}},
	{"id 2^53+1 is above a's exact max", lk("id", strconv.FormatUint(alU64LoNext, 10)), []string{"b.pulse", "c.pulse"}},
	{"id 5 in two shards", lk("id", "5"), []string{"a.pulse", "b.pulse", "c.pulse"}},
	{"id 8", lk("id", "8"), []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse"}},
	{"id 0 below every range", lk("id", "0"), []string{"c.pulse"}},
	{"region east", lk("region", "east"), []string{"b.pulse", "c.pulse"}},
	{"composite region excludes a and d", lk("region", "east", "id", "5"), []string{"b.pulse", "c.pulse"}},
	{"composite id excludes d, region excludes b", lk("region", "north", "id", "5"), []string{"a.pulse", "c.pulse"}},
}

func pruneKeyNames(keys []types.LookupKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.Field
	}
	return out
}

// TestArchiveLookupPrune_RecordsExactKeyRanges: the build records each
// shard's per-component range in order-preserving hex — u64 bounds
// above 2^53 exact — and none for the empty shard.
func TestArchiveLookupPrune_RecordsExactKeyRanges(t *testing.T) {
	t.Parallel()

	_, fsys, _ := setupPruneArchive(t)
	got := archiveShardRecords(t, fsys, []string{"region", "id"})
	want := map[string][2][]string{
		"a.pulse": {{"00", "0000000000000001"}, {"01", "0020000000000000"}},
		"b.pulse": {{"01", "0000000000000005"}, {"02", "0020000000000001"}},
		"c.pulse": {nil, nil},
		"d.pulse": {{"00", "0000000000000008"}, {"00", "0000000000000008"}},
	}
	for name, w := range want {
		sh, ok := got[name]
		if !ok {
			t.Fatalf("no manifest record for %s", name)
		}
		if !reflect.DeepEqual(sh.KeyMin, w[0]) || !reflect.DeepEqual(sh.KeyMax, w[1]) {
			t.Errorf("%s range = %v..%v, want %v..%v", name, sh.KeyMin, sh.KeyMax, w[0], w[1])
		}
	}
}

// TestArchiveLookupPrune_SkipsOutOfRangeShards: each lookup opens only
// the sidecars of shards whose recorded range admits the key (the empty
// shard, with no range, is always probed) and answers exactly as the
// single-file twin — pruning never loses a match.
func TestArchiveLookupPrune_SkipsOutOfRangeShards(t *testing.T) {
	t.Parallel()

	svc, _, counter := setupPruneArchive(t)
	for _, c := range pruneCases {
		twin := lookupOutcome(t, svc, "twin.pulse", c.keys, types.LookupMultiplicityAll)
		counter.take(nil)
		got := lookupOutcome(t, svc, "arch.pulse", c.keys, types.LookupMultiplicityAll)
		if got != twin {
			t.Errorf("%s: archive = %s, twin = %s", c.name, got, twin)
		}
		if probed := counter.take(pruneKeyNames(c.keys)); !reflect.DeepEqual(probed, c.probed) {
			t.Errorf("%s: opened sidecars of %v, want %v", c.name, probed, c.probed)
		}
	}
	// No false negative: every id the fixture holds is found.
	for _, sh := range archiveLookupShards {
		for _, r := range sh.rows {
			v := strconv.FormatUint(r.id, 10)
			twin := lookupOutcome(t, svc, "twin.pulse", lk("id", v), types.LookupMultiplicityAll)
			if got := lookupOutcome(t, svc, "arch.pulse", lk("id", v), types.LookupMultiplicityAll); got != twin {
				t.Errorf("id %s: archive = %s, twin = %s", v, got, twin)
			}
		}
	}
}

// TestArchiveLookupPrune_AbsentRangesProbeEveryShard: a manifest whose
// shard records carry no range — what an older binary's rewrite leaves
// — probes every shard and still answers as the twin.
func TestArchiveLookupPrune_AbsentRangesProbeEveryShard(t *testing.T) {
	t.Parallel()

	svc, fsys, counter := setupPruneArchive(t)
	for _, keys := range [][]string{{"id"}, {"region"}, {"region", "id"}} {
		rewriteArchiveManifest(t, fsys, keys, func(sh *encx.IndexManifestShard) { sh.KeyMin, sh.KeyMax = nil, nil })
	}
	raw, err := afero.ReadFile(fsys, encx.IndexManifestPath("arch.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "key_min") {
		t.Fatal("fixture: ranges not stripped")
	}
	all := []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse"}
	for _, c := range pruneCases {
		twin := lookupOutcome(t, svc, "twin.pulse", c.keys, types.LookupMultiplicityAll)
		counter.take(nil)
		if got := lookupOutcome(t, svc, "arch.pulse", c.keys, types.LookupMultiplicityAll); got != twin {
			t.Errorf("%s: archive = %s, twin = %s", c.name, got, twin)
		}
		if probed := counter.take(pruneKeyNames(c.keys)); !reflect.DeepEqual(probed, all) {
			t.Errorf("%s: opened sidecars of %v, want every shard", c.name, probed)
		}
	}
	if v, err := svc.VerifyIndex(context.Background(), "arch.pulse", []string{"id"}); err != nil || !v.Fresh {
		t.Errorf("verify without ranges = %+v, %v; want fresh", v, err)
	}
}

// TestArchiveLookupPrune_VerifyChecksRecordedRange: a fresh build
// verifies clean; a recorded range that disagrees with the shard's
// sidecar — here one that would hide id 8 — is stale with
// range_mismatch naming that shard.
func TestArchiveLookupPrune_VerifyChecksRecordedRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, _ := setupPruneArchive(t)
	v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil || !v.Fresh || v.ShardsRangeMismatched != nil {
		t.Fatalf("verify after build = %+v, %v; want fresh", v, err)
	}

	rewriteArchiveManifest(t, fsys, []string{"id"}, func(sh *encx.IndexManifestShard) {
		if sh.Name == "d.pulse" {
			sh.KeyMax = []string{"0000000000000007"}
		}
	})
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "8"), ""); !strings.HasPrefix(got, "PULSE_LOOKUP_NOT_FOUND") {
		t.Fatalf("fixture: a wrong range should hide id 8, lookup = %s", got)
	}
	v, err = svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Fresh || v.Reason != IndexFreshnessReasonRangeMismatch || !reflect.DeepEqual(v.ShardsRangeMismatched, []string{"d.pulse"}) {
		t.Errorf("verify with a wrong range = %+v, want stale range_mismatch naming d.pulse", v)
	}
}

// TestArchiveLookupPrune_AddRecordsRangeAndPrunes: shard-add upkeep
// records the arriving shard's range, and lookups prune by it.
func TestArchiveLookupPrune_AddRecordsRangeAndPrunes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, counter := setupPruneArchive(t)
	writeUpkeepShard(t, fsys, "e.pulse", 80) // ids 9 and 5
	if _, err := svc.AddShard(ctx, "arch.pulse", "e.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	e := archiveShardRecords(t, fsys, []string{"id"})["e.pulse"]
	if !reflect.DeepEqual(e.KeyMin, []string{"0000000000000005"}) || !reflect.DeepEqual(e.KeyMax, []string{"0000000000000009"}) {
		t.Errorf("added shard range = %v..%v, want 5..9", e.KeyMin, e.KeyMax)
	}
	counter.take(nil)
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "1"), ""); got != `[{"region":"north","score":10}]` {
		t.Errorf("lookup id 1 = %s", got)
	}
	if opened := counter.take([]string{"id"}); !reflect.DeepEqual(opened, []string{"a.pulse", "c.pulse"}) {
		t.Errorf("lookup id 1 opened sidecars of %v, want [a.pulse c.pulse]", opened)
	}
	if v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"}); err != nil || !v.Fresh {
		t.Errorf("verify after add = %+v, %v; want fresh", v, err)
	}
}

// TestArchiveLookupPrune_PrunedShardSidecarUnread: a pruned shard's
// sidecar is never opened, so a lookup its range excludes answers from
// the other shards even when that sidecar records another source — the
// shard cannot hold the key. VerifyIndex still reports the sidecar.
func TestArchiveLookupPrune_PrunedShardSidecarUnread(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, counter := setupPruneArchive(t)
	p := encx.ArchiveShardIndexPath("arch.pulse", "d.pulse", []string{"id"})
	idx, err := encx.ReadIndexFile(fsys, p)
	if err != nil {
		t.Fatal(err)
	}
	idx.SourceSize++
	if err := encx.WriteIndexFile(fsys, p, idx); err != nil {
		t.Fatal(err)
	}
	counter.take(nil)
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "7"), ""); got != `[{"region":"east","score":60}]` {
		t.Errorf("lookup id 7 = %s, want b's row", got)
	}
	if opened := counter.take([]string{"id"}); !reflect.DeepEqual(opened, []string{"a.pulse", "b.pulse", "c.pulse"}) {
		t.Errorf("lookup id 7 opened sidecars of %v, want d's skipped", opened)
	}
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "8"), ""); !strings.HasPrefix(got, "PULSE_INDEX_STALE") {
		t.Errorf("lookup id 8 = %s, want PULSE_INDEX_STALE (d probed)", got)
	}
	v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil || v.Fresh || !reflect.DeepEqual(v.ShardsUncovered, []string{"d.pulse"}) {
		t.Errorf("verify = %+v, %v; want stale naming d.pulse", v, err)
	}
}
