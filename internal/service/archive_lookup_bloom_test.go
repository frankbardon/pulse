package service

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// archive_lookup_bloom_test.go pins the per-shard Bloom sidecar
// (archive storage #493, part 2): BuildIndex writes one beside every
// shard sidecar, a lookup skips (unopened) every shard whose fresh
// Bloom says "not present" after the key-range check, never loses a
// match by it, probes on a missing / damaged / stale Bloom, VerifyIndex
// flags a stale one and DropIndex removes them.

// bloomProbed: with every Bloom in place, a lookup opens only the
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

// TestArchiveLookupBloom_MissingDamagedOrStaleProbes: id 2 lies inside
// a's key range but not in a, so a fresh Bloom skips a; with a's Bloom
// missing, damaged, or stamped with another shard identity, a is
// probed and the lookup still misses correctly.
func TestArchiveLookupBloom_MissingDamagedOrStaleProbes(t *testing.T) {
	t.Parallel()

	bloom := encx.ArchiveShardBloomPath("arch.pulse", "a.pulse", []string{"id"})
	for _, c := range []struct {
		name string
		edit func(t *testing.T, fsys afero.Fs)
	}{
		{"missing", func(t *testing.T, fsys afero.Fs) {
			if err := fsys.Remove(bloom); err != nil {
				t.Fatal(err)
			}
		}},
		{"damaged", func(t *testing.T, fsys afero.Fs) {
			raw, err := afero.ReadFile(fsys, bloom)
			if err != nil {
				t.Fatal(err)
			}
			raw[len(raw)-6] ^= 0x10
			if err := afero.WriteFile(fsys, bloom, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"other shard identity", func(t *testing.T, fsys afero.Fs) {
			f, _, err := encx.ReadShardBloomFile(fsys, bloom)
			if err != nil {
				t.Fatal(err)
			}
			f.CRC32++
			if err := encx.WriteShardBloomFile(fsys, bloom, f); err != nil {
				t.Fatal(err)
			}
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
			if opened := counter.take([]string{"id"}); !reflect.DeepEqual(opened, []string{"a.pulse"}) {
				t.Errorf("opened sidecars of %v, want [a.pulse] probed", opened)
			}
			if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "1"), ""); got != `[{"region":"north","score":10}]` {
				t.Errorf("lookup id 1 = %s", got)
			}
		})
	}
}

// TestArchiveLookupBloom_VerifyFlagsStaleBloom: a fresh build verifies
// clean; a Bloom that is not the filter its sidecar's keys build for
// the shard — here an empty one stamped with the right identity, which
// would hide id 1 — is stale (bloom_stale) naming that shard; a missing
// Bloom is not.
func TestArchiveLookupBloom_VerifyFlagsStaleBloom(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, _ := setupBloomArchive(t)
	v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil || !v.Fresh || v.ShardsBloomStale != nil {
		t.Fatalf("verify after build = %+v, %v; want fresh", v, err)
	}

	bloom := encx.ArchiveShardBloomPath("arch.pulse", "a.pulse", []string{"id"})
	f, _, err := encx.ReadShardBloomFile(fsys, bloom)
	if err != nil {
		t.Fatal(err)
	}
	empty := encx.NewShardBloom(f.Shard, f.Size, f.CRC32, int(f.KeyWidth), 3)
	if err := encx.WriteShardBloomFile(fsys, bloom, empty); err != nil {
		t.Fatal(err)
	}
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "1"), ""); !strings.HasPrefix(got, "PULSE_LOOKUP_NOT_FOUND") {
		t.Fatalf("fixture: a wrong Bloom should hide id 1, lookup = %s", got)
	}
	v, err = svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Fresh || v.Reason != IndexFreshnessReasonBloomStale || !reflect.DeepEqual(v.ShardsBloomStale, []string{"a.pulse"}) {
		t.Errorf("verify with a wrong Bloom = %+v, want stale bloom_stale naming a.pulse", v)
	}

	if err := fsys.Remove(bloom); err != nil {
		t.Fatal(err)
	}
	if v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"}); err != nil || !v.Fresh || v.ShardsBloomStale != nil {
		t.Errorf("verify with a missing Bloom = %+v, %v; want fresh", v, err)
	}
}

// TestArchiveLookupBloom_DropRemovesBlooms: DropIndex removes the index's
// Bloom sidecars with its sidecars and leaves every other index's; the
// listing never surfaces a Bloom.
func TestArchiveLookupBloom_DropRemovesBlooms(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, _ := setupBloomArchive(t)
	infos, err := svc.ListIndexes(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		for _, p := range info.ShardIndexPaths {
			if !strings.HasSuffix(p, ".idx") {
				t.Errorf("listing surfaced %s", p)
			}
		}
	}
	if err := svc.DropIndex(ctx, "arch.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	for _, sh := range []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse"} {
		if ok, _ := afero.Exists(fsys, encx.ArchiveShardBloomPath("arch.pulse", sh, []string{"id"})); ok {
			t.Errorf("drop left %s's Bloom", sh)
		}
		if ok, _ := afero.Exists(fsys, encx.ArchiveShardBloomPath("arch.pulse", sh, []string{"region"})); !ok {
			t.Errorf("drop of the id index removed %s's region Bloom", sh)
		}
	}
}

// TestArchiveLookupBloom_AddWritesBloomAndPrunes: shard-add upkeep
// writes the arriving shard's Bloom, so an in-range key the new shard
// lacks skips it.
func TestArchiveLookupBloom_AddWritesBloomAndPrunes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, counter := setupBloomArchive(t)
	writeUpkeepShard(t, fsys, "e.pulse", 80) // ids 9 and 5
	if _, err := svc.AddShard(ctx, "arch.pulse", "e.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if ok, _ := afero.Exists(fsys, encx.ArchiveShardBloomPath("arch.pulse", "e.pulse", []string{"id"})); !ok {
		t.Fatal("add wrote no Bloom for the new shard")
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
