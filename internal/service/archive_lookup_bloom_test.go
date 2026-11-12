package service

import (
	"context"
	"encoding/binary"
	"reflect"
	"strconv"
	"strings"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// archive_lookup_bloom_test.go pins the Bloom prune (archive storage
// #493 part 2, #721): BuildIndex writes every shard's filter into the
// index's archive Bloom set, a lookup skips (unopened) every shard whose
// fresh filter says "not present" after the key-range check, never
// loses a match by it, probes on a missing / damaged / stale filter,
// VerifyIndex flags a stale one and DropIndex removes the set. An index
// built before the set (per-shard ".bloom" sidecars only) still prunes.

// bloomProbed: with every Bloom filter in place, a lookup opens only the
// sidecars of shards that hold the key (the fixture's keys hit no
// false positive; the hashing is deterministic, so this is stable).
var bloomProbed = map[string][]string{
	"id 1":                              {"a.pulse"},
	"id 2^53":                           {"a.pulse"},
	"id 2^53+1 is above a's exact max":  {"b.pulse"},
	"id 5 in two shards":                {"a.pulse", "b.pulse"},
	"id 8":                              {"d.pulse"},
	"id 0 below every range":            {},
	"region east":                       {"b.pulse"},
	"composite region excludes a and d": {},
	"composite id excludes d, region excludes b": {"a.pulse"},
}

// TestArchiveLookupBloom_SkipsShardsWithoutTheKey: the Bloom skips the
// in-range shards that lack the key — including the empty shard, whose
// all-zero filter rejects every key — and every answer equals the
// single-file twin's: no false negative for any id the fixture holds.
func TestArchiveLookupBloom_SkipsShardsWithoutTheKey(t *testing.T) {
	t.Parallel()

	svc, _, counter := setupBloomArchive(t)
	for _, c := range pruneCases {
		twin := lookupOutcome(t, svc, "twin.pulse", c.keys, types.LookupMultiplicityAll)
		counter.take(nil)
		if got := lookupOutcome(t, svc, "arch.pulse", c.keys, types.LookupMultiplicityAll); got != twin {
			t.Errorf("%s: archive = %s, twin = %s", c.name, got, twin)
		}
		want, ok := bloomProbed[c.name]
		if !ok {
			t.Fatalf("no Bloom expectation for %q", c.name)
		}
		if probed := counter.take(pruneKeyNames(c.keys)); !reflect.DeepEqual(probed, want) {
			t.Errorf("%s: opened sidecars of %v, want %v", c.name, probed, want)
		}
	}
	for _, sh := range archiveLookupShards {
		for _, r := range sh.rows {
			v := strconv.FormatUint(r.id, 10)
			twin := lookupOutcome(t, svc, "twin.pulse", lk("id", v), types.LookupMultiplicityAll)
			if got := lookupOutcome(t, svc, "arch.pulse", lk("id", v), types.LookupMultiplicityAll); got != twin {
				t.Errorf("id %s: archive = %s, twin = %s", v, got, twin)
			}
			composite := lk("region", r.region, "id", v)
			twin = lookupOutcome(t, svc, "twin.pulse", composite, types.LookupMultiplicityAll)
			if got := lookupOutcome(t, svc, "arch.pulse", composite, types.LookupMultiplicityAll); got != twin {
				t.Errorf("region %s id %s: archive = %s, twin = %s", r.region, v, got, twin)
			}
		}
	}
}

// readBloomSet reads the archive Bloom set of the index over keys.
func readBloomSet(t *testing.T, fsys afero.Fs, keys []string) *encx.ArchiveBloomSet {
	t.Helper()
	set, present, err := encx.ReadArchiveBloomSetFile(fsys, encx.ArchiveBloomSetPath("arch.pulse", keys))
	if err != nil || !present {
		t.Fatalf("reading Bloom set %v: present %v, %v", keys, present, err)
	}
	return set
}

// editBloomSet rewrites the set of the index over keys with edit
// applied to its filters, in archive order; edit returning nil drops
// the shard's record.
func editBloomSet(t *testing.T, fsys afero.Fs, keys []string, edit func(f *encx.ShardBloom) *encx.ShardBloom) {
	t.Helper()
	set := readBloomSet(t, fsys, keys)
	var out []*encx.ShardBloom
	for _, sh := range archiveLookupShards {
		if f := set.Filters[sh.name]; f != nil {
			if f = edit(f); f != nil {
				out = append(out, f)
			}
		}
	}
	if err := encx.WriteArchiveBloomSetFile(fsys, encx.ArchiveBloomSetPath("arch.pulse", keys), out); err != nil {
		t.Fatal(err)
	}
}

// toLegacyBlooms turns every index's Bloom set into the pre-set layout:
// one per-shard ".bloom" sidecar per filter, no set file.
func toLegacyBlooms(t *testing.T, fsys afero.Fs, keySets ...[]string) {
	t.Helper()
	for _, keys := range keySets {
		set := readBloomSet(t, fsys, keys)
		for name, f := range set.Filters {
			if err := encx.WriteShardBloomFile(fsys, encx.ArchiveShardBloomPath("arch.pulse", name, keys), f); err != nil {
				t.Fatal(err)
			}
		}
		if err := fsys.Remove(encx.ArchiveBloomSetPath("arch.pulse", keys)); err != nil {
			t.Fatal(err)
		}
	}
}

var bloomKeySets = [][]string{{"id"}, {"region"}, {"region", "id"}}

// TestArchiveLookupBloom_MissingDamagedOrStaleProbes: id 2 lies inside
// a's key range but not in a, so a fresh filter skips a; with the set
// missing, its header damaged, a's record damaged, a's record dropped
// or stamped with another shard identity, a is probed and the lookup
// still misses correctly — and id 1 is still found. Losing the whole
// set probes the empty shard c too (it records no key range).
func TestArchiveLookupBloom_MissingDamagedOrStaleProbes(t *testing.T) {
	t.Parallel()

	setPath := encx.ArchiveBloomSetPath("arch.pulse", []string{"id"})
	flip := func(at func(raw []byte) int) func(t *testing.T, fsys afero.Fs) {
		return func(t *testing.T, fsys afero.Fs) {
			raw, err := afero.ReadFile(fsys, setPath)
			if err != nil {
				t.Fatal(err)
			}
			raw[at(raw)] ^= 0x10
			if err := afero.WriteFile(fsys, setPath, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range []struct {
		name  string
		probe []string
		edit  func(t *testing.T, fsys afero.Fs)
	}{
		{"set missing", []string{"a.pulse", "c.pulse"}, func(t *testing.T, fsys afero.Fs) {
			if err := fsys.Remove(setPath); err != nil {
				t.Fatal(err)
			}
		}},
		{"set header damaged", []string{"a.pulse", "c.pulse"}, flip(func([]byte) int { return 0 })},
		// a's record is the first: 13-byte set header, 4-byte length,
		// then the record — its last word sits just before its CRC32.
		{"a's record damaged", []string{"a.pulse"}, flip(func(raw []byte) int {
			n := int(binary.LittleEndian.Uint32(raw[13:]))
			return 17 + n - 6
		})},
		{"a's record dropped", []string{"a.pulse"}, func(t *testing.T, fsys afero.Fs) {
			editBloomSet(t, fsys, []string{"id"}, func(f *encx.ShardBloom) *encx.ShardBloom {
				if f.Shard == "a.pulse" {
					return nil
				}
				return f
			})
		}},
		{"a's record other shard identity", []string{"a.pulse"}, func(t *testing.T, fsys afero.Fs) {
			editBloomSet(t, fsys, []string{"id"}, func(f *encx.ShardBloom) *encx.ShardBloom {
				if f.Shard == "a.pulse" {
					f.CRC32++
				}
				return f
			})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fsys, counter := setupBloomArchive(t)
			if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "2"), ""); !strings.HasPrefix(got, "PULSE_LOOKUP_NOT_FOUND") {
				t.Fatalf("lookup id 2 = %s, want a miss", got)
			}
			if opened := counter.take([]string{"id"}); len(opened) != 0 {
				t.Fatalf("fixture: a fresh Bloom should skip every shard for id 2, opened %v", opened)
			}
			c.edit(t, fsys)
			if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "2"), ""); !strings.HasPrefix(got, "PULSE_LOOKUP_NOT_FOUND") {
				t.Errorf("lookup id 2 = %s, want a miss", got)
			}
			if opened := counter.take([]string{"id"}); !reflect.DeepEqual(opened, c.probe) {
				t.Errorf("opened sidecars of %v, want %v probed", opened, c.probe)
			}
			if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "1"), ""); got != `[{"region":"north","score":10}]` {
				t.Errorf("lookup id 1 = %s", got)
			}
		})
	}
}

// TestArchiveLookupBloom_StaleFilterNeverHidesAMatch is the zero-miss
// property against a stale filter: a's record is replaced by an EMPTY
// filter — which would hide every key of a if trusted — stamped with
// a's name and size but an old CRC32 (a's bytes moved since). The
// lookup must not trust it: every key a holds is still found, equal to
// the single-file twin.
func TestArchiveLookupBloom_StaleFilterNeverHidesAMatch(t *testing.T) {
	t.Parallel()

	svc, fsys, _ := setupBloomArchive(t)
	editBloomSet(t, fsys, []string{"id"}, func(f *encx.ShardBloom) *encx.ShardBloom {
		if f.Shard != "a.pulse" {
			return f
		}
		return encx.NewShardBloom(f.Shard, f.Size, f.CRC32^1, int(f.KeyWidth), 3)
	})
	for _, r := range archiveLookupShards[0].rows {
		v := strconv.FormatUint(r.id, 10)
		twin := lookupOutcome(t, svc, "twin.pulse", lk("id", v), types.LookupMultiplicityAll)
		if got := lookupOutcome(t, svc, "arch.pulse", lk("id", v), types.LookupMultiplicityAll); got != twin {
			t.Errorf("id %s with a stale filter for a.pulse: archive = %s, twin = %s", v, got, twin)
		}
	}
}

// TestArchiveLookupBloom_VerifyFlagsStaleBloom: a fresh build verifies
// clean; a record that is not the filter its sidecar's keys build for
// the shard — here an empty one stamped with the right identity, which
// would hide id 1 — is stale (bloom_stale) naming that shard; a set
// whose header is damaged names every shard; a damaged record names
// the shard it held; an intact set without a's record, or no set at
// all, is fresh.
func TestArchiveLookupBloom_VerifyFlagsStaleBloom(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	verify := func(t *testing.T, svc *Service) *VerifyIndexResult {
		t.Helper()
		v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	stale := func(t *testing.T, v *VerifyIndexResult, want []string) {
		t.Helper()
		if v.Fresh || v.Reason != IndexFreshnessReasonBloomStale || !reflect.DeepEqual(v.ShardsBloomStale, want) {
			t.Errorf("verify = %+v, want stale bloom_stale naming %v", v, want)
		}
	}
	fresh := func(t *testing.T, v *VerifyIndexResult) {
		t.Helper()
		if !v.Fresh || v.ShardsBloomStale != nil {
			t.Errorf("verify = %+v, want fresh", v)
		}
	}
	setPath := encx.ArchiveBloomSetPath("arch.pulse", []string{"id"})
	all := []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse"}

	t.Run("wrong filter, right identity", func(t *testing.T) {
		svc, fsys, _ := setupBloomArchive(t)
		fresh(t, verify(t, svc))
		editBloomSet(t, fsys, []string{"id"}, func(f *encx.ShardBloom) *encx.ShardBloom {
			if f.Shard != "a.pulse" {
				return f
			}
			return encx.NewShardBloom(f.Shard, f.Size, f.CRC32, int(f.KeyWidth), 3)
		})
		if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "1"), ""); !strings.HasPrefix(got, "PULSE_LOOKUP_NOT_FOUND") {
			t.Fatalf("fixture: a wrong Bloom should hide id 1, lookup = %s", got)
		}
		stale(t, verify(t, svc), []string{"a.pulse"})
	})
	t.Run("set header damaged", func(t *testing.T) {
		svc, fsys, _ := setupBloomArchive(t)
		raw, _ := afero.ReadFile(fsys, setPath)
		raw[0] ^= 1
		if err := afero.WriteFile(fsys, setPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		stale(t, verify(t, svc), all)
	})
	t.Run("record damaged", func(t *testing.T) {
		svc, fsys, _ := setupBloomArchive(t)
		raw, _ := afero.ReadFile(fsys, setPath)
		raw[17+int(binary.LittleEndian.Uint32(raw[13:]))-6] ^= 1
		if err := afero.WriteFile(fsys, setPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		stale(t, verify(t, svc), []string{"a.pulse"})
	})
	t.Run("record absent from an intact set", func(t *testing.T) {
		svc, fsys, _ := setupBloomArchive(t)
		editBloomSet(t, fsys, []string{"id"}, func(f *encx.ShardBloom) *encx.ShardBloom {
			if f.Shard == "a.pulse" {
				return nil
			}
			return f
		})
		fresh(t, verify(t, svc))
	})
	t.Run("no set", func(t *testing.T) {
		svc, fsys, _ := setupBloomArchive(t)
		if err := fsys.Remove(setPath); err != nil {
			t.Fatal(err)
		}
		fresh(t, verify(t, svc))
	})
	t.Run("legacy sidecar wrong", func(t *testing.T) {
		svc, fsys, _ := setupBloomArchive(t)
		toLegacyBlooms(t, fsys, []string{"id"})
		fresh(t, verify(t, svc))
		bloom := encx.ArchiveShardBloomPath("arch.pulse", "a.pulse", []string{"id"})
		f, _, err := encx.ReadShardBloomFile(fsys, bloom)
		if err != nil {
			t.Fatal(err)
		}
		if err := encx.WriteShardBloomFile(fsys, bloom, encx.NewShardBloom(f.Shard, f.Size, f.CRC32, int(f.KeyWidth), 3)); err != nil {
			t.Fatal(err)
		}
		stale(t, verify(t, svc), []string{"a.pulse"})
	})
}

// TestArchiveLookupBloom_DropRemovesBlooms: DropIndex removes the index's
// Bloom set (and any legacy per-shard Bloom sidecar) and leaves every
// other index's; the listing never surfaces a Bloom.
func TestArchiveLookupBloom_DropRemovesBlooms(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, _ := setupBloomArchive(t)
	legacy := encx.ArchiveShardBloomPath("arch.pulse", "b.pulse", []string{"id"})
	if err := afero.WriteFile(fsys, legacy, []byte("left by an older build"), 0o644); err != nil {
		t.Fatal(err)
	}
	infos, err := svc.ListIndexes(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if !strings.HasSuffix(info.IndexPath, ".idx") {
			t.Errorf("listing surfaced %s", info.IndexPath)
		}
		for _, p := range info.ShardIndexPaths {
			if !strings.HasSuffix(p, ".idx") {
				t.Errorf("listing surfaced %s", p)
			}
		}
	}
	if err := svc.DropIndex(ctx, "arch.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{encx.ArchiveBloomSetPath("arch.pulse", []string{"id"}), legacy} {
		if ok, _ := afero.Exists(fsys, p); ok {
			t.Errorf("drop left %s", p)
		}
	}
	if ok, _ := afero.Exists(fsys, encx.ArchiveBloomSetPath("arch.pulse", []string{"region"})); !ok {
		t.Error("drop of the id index removed the region index's Bloom set")
	}
}

// TestArchiveLookupBloom_AddWritesBloomAndPrunes: shard-add upkeep
// adds the arriving shard's filter to the set, so an in-range key the
// new shard lacks skips it.
func TestArchiveLookupBloom_AddWritesBloomAndPrunes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, counter := setupBloomArchive(t)
	writeUpkeepShard(t, fsys, "e.pulse", 80) // ids 9 and 5
	if _, err := svc.AddShard(ctx, "arch.pulse", "e.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if set := readBloomSet(t, fsys, []string{"id"}); set.Filters["e.pulse"] == nil || len(set.Filters) != 5 {
		t.Fatalf("set after add holds %d filters, e.pulse %v; want all five", len(set.Filters), set.Filters["e.pulse"] != nil)
	}
	counter.take(nil)
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "7"), ""); got != `[{"region":"east","score":60}]` {
		t.Errorf("lookup id 7 = %s", got)
	}
	if opened := counter.take([]string{"id"}); !reflect.DeepEqual(opened, []string{"b.pulse"}) {
		t.Errorf("lookup id 7 opened sidecars of %v, want [b.pulse] (e in range, Bloom says no)", opened)
	}
	if v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"}); err != nil || !v.Fresh {
		t.Errorf("verify after add = %+v, %v; want fresh", v, err)
	}
}

// TestArchiveLookupBloom_LegacySidecarsStillPrune: an index built before
// the set (per-shard ".bloom" sidecars only) prunes exactly as the set
// does and answers as the twin; a shard-add migrates it into a set
// carrying every kept shard's filter, deletes the migrated per-shard
// sidecars and still answers as a twin holding the new shard's rows;
// a rebuild supersedes any per-shard sidecar left.
func TestArchiveLookupBloom_LegacySidecarsStillPrune(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, counter := setupBloomArchive(t)
	toLegacyBlooms(t, fsys, bloomKeySets...)
	for _, c := range pruneCases {
		twin := lookupOutcome(t, svc, "twin.pulse", c.keys, types.LookupMultiplicityAll)
		counter.take(nil)
		if got := lookupOutcome(t, svc, "arch.pulse", c.keys, types.LookupMultiplicityAll); got != twin {
			t.Errorf("legacy %s: archive = %s, twin = %s", c.name, got, twin)
		}
		if probed := counter.take(pruneKeyNames(c.keys)); !reflect.DeepEqual(probed, bloomProbed[c.name]) {
			t.Errorf("legacy %s: opened sidecars of %v, want %v", c.name, probed, bloomProbed[c.name])
		}
	}

	writeUpkeepShard(t, fsys, "e.pulse", 80)
	if _, err := svc.AddShard(ctx, "arch.pulse", "e.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if set := readBloomSet(t, fsys, []string{"id"}); len(set.Filters) != 5 || set.Damaged {
		t.Errorf("migrated set holds %d filters (damaged %v), want the four legacy ones plus e.pulse", len(set.Filters), set.Damaged)
	}
	counter.take(nil)
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "2"), ""); !strings.HasPrefix(got, "PULSE_LOOKUP_NOT_FOUND") {
		t.Errorf("lookup id 2 after migration = %s", got)
	}
	if opened := counter.take([]string{"id"}); len(opened) != 0 {
		t.Errorf("after migration id 2 opened sidecars of %v, want none (a's migrated filter says no)", opened)
	}
	for _, keys := range bloomKeySets {
		for _, sh := range []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse", "e.pulse"} {
			if ok, _ := afero.Exists(fsys, encx.ArchiveShardBloomPath("arch.pulse", sh, keys)); ok {
				t.Errorf("migrating add left %s's per-shard Bloom sidecar for %v", sh, keys)
			}
		}
	}
	// The twin of the grown archive: every shard's rows, archive order.
	arch, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	var all []archiveLookupRow
	for _, sh := range archiveLookupShards {
		all = append(all, sh.rows...)
	}
	all = append(all, archiveLookupRow{9, "west", 80}, archiveLookupRow{5, "north", 81})
	writeArchiveLookupCohort(t, fsys, "twin2.pulse", arch.Schema(), all)
	for _, keys := range bloomKeySets {
		if _, err := svc.BuildIndex(ctx, "twin2.pulse", keys); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range all {
		v := strconv.FormatUint(r.id, 10)
		for _, keys := range [][]types.LookupKey{lk("id", v), lk("region", r.region), lk("region", r.region, "id", v)} {
			twin := lookupOutcome(t, svc, "twin2.pulse", keys, types.LookupMultiplicityAll)
			if got := lookupOutcome(t, svc, "arch.pulse", keys, types.LookupMultiplicityAll); got != twin {
				t.Errorf("after migration %v: archive = %s, twin = %s", keys, got, twin)
			}
		}
	}

	if _, err := svc.BuildIndex(ctx, "arch.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	for _, sh := range []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse"} {
		if ok, _ := afero.Exists(fsys, encx.ArchiveShardBloomPath("arch.pulse", sh, []string{"id"})); ok {
			t.Errorf("rebuild left %s's per-shard Bloom sidecar", sh)
		}
	}
}
