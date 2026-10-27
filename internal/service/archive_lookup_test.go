package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// archive_lookup_test.go pins archive point lookup (U51 #359, FR-31..36)
// and the exact u64 index key (FR-45): an archive indexed one sidecar
// per shard answers every lookup exactly as its single-file twin
// (archive rows in archive order, canonical dictionary) does —
// multiplicity across the union, the miss path, a categorical literal
// one shard's dictionary lacks, u64 ids one float apart — and a shard
// the index does not cover is refused, never silently missed.

const (
	alU64Lo     = uint64(1) << 53
	alU64LoNext = alU64Lo + 1
)

// archiveLookupRow is one fixture row, spelled with its region LABEL so
// each cohort encodes it against its own dictionary.
type archiveLookupRow struct {
	id     uint64
	region string
	score  float64
}

// archiveLookupShards: a's dictionary lacks "east", b's lacks "north"
// (the archive remaps b onto the canonical prefix), c is empty, id 5
// sits in two shards and the two u64 ids above 2^53 share one float64.
var archiveLookupShards = []struct {
	name string
	rows []archiveLookupRow
}{
	{"a.pulse", []archiveLookupRow{{1, "north", 10}, {alU64Lo, "south", 20}, {5, "north", 30}}},
	{"b.pulse", []archiveLookupRow{{alU64LoNext, "east", 40}, {5, "south", 50}, {7, "east", 60}}},
	{"c.pulse", nil},
	{"d.pulse", []archiveLookupRow{{8, "north", 70}}},
}

func archiveLookupSchema(labels ...string) *encoding.Schema {
	d := encoding.NewDictionary()
	for _, l := range labels {
		_, _ = d.Add(l)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, CsvColumnIdx: 1, Dictionary: d},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 9, CsvColumnIdx: 2},
	}}
}

// writeArchiveLookupCohort writes rows as a single-file cohort whose
// region dictionary is schema's.
func writeArchiveLookupCohort(t *testing.T, fsys afero.Fs, path string, schema *encoding.Schema, rows []archiveLookupRow) {
	t.Helper()
	dict := schema.Field("region").Dictionary
	recs := make([][]uint64, 0, len(rows))
	for _, r := range rows {
		id, ok := dict.IDFor(r.region)
		if !ok {
			t.Fatalf("fixture: %q not in %s's dictionary", r.region, path)
		}
		recs = append(recs, []uint64{r.id, uint64(id), math.Float64bits(r.score)})
	}
	if err := afero.WriteFile(fsys, path, writeSinglePulse(t, schema, recs), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shardLabels is the dictionary a shard's own rows need, first-seen.
func shardLabels(rows []archiveLookupRow) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		if !seen[r.region] {
			seen[r.region] = true
			out = append(out, r.region)
		}
	}
	if len(out) == 0 {
		out = []string{"north"}
	}
	return out
}

// setupArchiveLookup writes the shards, builds "arch.pulse" from them
// and the single-file twin "twin.pulse" (every row in archive order,
// encoded against the archive's canonical dictionary), and indexes both
// on each key tuple.
func setupArchiveLookup(t *testing.T, keySets ...[]string) (*Service, afero.Fs) {
	t.Helper()
	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	var names []string
	var all []archiveLookupRow
	for _, sh := range archiveLookupShards {
		writeArchiveLookupCohort(t, fsys, sh.name, archiveLookupSchema(shardLabels(sh.rows)...), sh.rows)
		names = append(names, sh.name)
		all = append(all, sh.rows...)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", names); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	arch, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	writeArchiveLookupCohort(t, fsys, "twin.pulse", arch.Schema(), all)
	for _, keys := range keySets {
		for _, path := range []string{"arch.pulse", "twin.pulse"} {
			if _, err := svc.BuildIndex(ctx, path, keys); err != nil {
				t.Fatalf("BuildIndex(%s, %v): %v", path, keys, err)
			}
		}
	}
	return svc, fsys
}

// lookupOutcome renders a lookup's rows (region + score: the u64 id
// column renders through its float64 echo on LookupResult.Rows, so the
// score is what tells 2^53 and 2^53+1 apart), or its error code and
// match_count, for comparison across the archive and its twin.
func lookupOutcome(t *testing.T, svc *Service, path string, keys []types.LookupKey, mode types.LookupMultiplicity) string {
	t.Helper()
	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: path}, Keys: keys, Multiplicity: mode,
		ReturnColumns: []string{"region", "score"},
	})
	if err != nil {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			t.Fatalf("uncoded lookup error: %v", err)
		}
		return fmt.Sprintf("%s match_count=%v", ce.Code, ce.Details["match_count"])
	}
	b, err := json.Marshal(res.Rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lk(pairs ...string) []types.LookupKey {
	var out []types.LookupKey
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, types.LookupKey{Field: pairs[i], Value: pairs[i+1]})
	}
	return out
}

// TestArchiveLookup_ParityWithSingleFileTwin is FR-36: every lookup —
// hit, u64 ids one float apart, multiplicity across shards, a miss, a
// literal one shard's dictionary lacks, a literal no dictionary holds —
// answers exactly as on the single-file twin, and the archive build
// wrote one sidecar per shard (the empty one included).
func TestArchiveLookup_ParityWithSingleFileTwin(t *testing.T) {
	t.Parallel()

	svc, fsys := setupArchiveLookup(t, []string{"id"}, []string{"region"}, []string{"region", "id"})
	lo, loNext := strconv.FormatUint(alU64Lo, 10), strconv.FormatUint(alU64LoNext, 10)
	cases := []struct {
		name string
		keys []types.LookupKey
		mode types.LookupMultiplicity
		want string
	}{
		{"unique hit", lk("id", "1"), "", `[{"region":"north","score":10}]`},
		{"u64 2^53", lk("id", lo), "", `[{"region":"south","score":20}]`},
		{"u64 2^53+1", lk("id", loNext), "", `[{"region":"east","score":40}]`},
		{"ambiguous across shards", lk("id", "5"), "", "PULSE_LOOKUP_AMBIGUOUS match_count=2"},
		{"first is the lowest global row", lk("id", "5"), types.LookupMultiplicityFirst, `[{"region":"north","score":30}]`},
		{"all in archive order", lk("id", "5"), types.LookupMultiplicityAll, `[{"region":"north","score":30},{"region":"south","score":50}]`},
		{"miss", lk("id", "999"), "", "PULSE_LOOKUP_NOT_FOUND match_count=<nil>"},
		{"literal absent from shard a's dictionary", lk("region", "east"), types.LookupMultiplicityAll, `[{"region":"east","score":40},{"region":"east","score":60}]`},
		{"categorical across three shards", lk("region", "north"), types.LookupMultiplicityAll, `[{"region":"north","score":10},{"region":"north","score":30},{"region":"north","score":70}]`},
		{"literal in no dictionary", lk("region", "west"), "", "PROCESSING_CONFIG match_count=<nil>"},
		{"composite hit", lk("region", "north", "id", "5"), "", `[{"region":"north","score":30}]`},
		{"composite miss", lk("region", "east", "id", "5"), "", "PULSE_LOOKUP_NOT_FOUND match_count=<nil>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			twin := lookupOutcome(t, svc, "twin.pulse", c.keys, c.mode)
			got := lookupOutcome(t, svc, "arch.pulse", c.keys, c.mode)
			if twin != c.want {
				t.Fatalf("fixture drift: twin = %s, want %s", twin, c.want)
			}
			if got != twin {
				t.Errorf("archive = %s\ntwin    = %s", got, twin)
			}
		})
	}
	for _, sh := range archiveLookupShards {
		p := encx.ArchiveShardIndexPath("arch.pulse", sh.name, []string{"id"})
		if ok, _ := afero.Exists(fsys, p); !ok {
			t.Errorf("no per-shard sidecar for %s at %s", sh.name, p)
		}
	}
	if ok, _ := afero.Exists(fsys, encx.SidecarIndexPath("arch.pulse", []string{"id"})); ok {
		t.Error("an archive index must not write a file at its logical path")
	}
}

// replaceArchive overwrites arch.pulse with an archive built from
// sources (no index upkeep — the mutation an index does not see).
func replaceArchive(t *testing.T, svc *Service, fsys afero.Fs, sources []string) {
	t.Helper()
	if _, err := svc.CreateShardArchive(context.Background(), "next.pulse", sources); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	raw, err := afero.ReadFile(fsys, "next.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "arch.pulse", raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestArchiveLookup_UncoveredShardRefused is FR-33: a shard the
// manifest does not cover — added after the build, or a same-name,
// same-size replacement only the CRC32 tells apart — refuses the lookup
// with PULSE_INDEX_STALE and details.shards_uncovered, and verify
// reports the same shards on its fast-path.
func TestArchiveLookup_UncoveredShardRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse"}
	for _, c := range []struct {
		name    string
		mutate  func(t *testing.T, svc *Service, fsys afero.Fs)
		missing []string
	}{
		{"shard added", func(t *testing.T, svc *Service, fsys afero.Fs) {
			writeArchiveLookupCohort(t, fsys, "e.pulse", archiveLookupSchema("north"), []archiveLookupRow{{1, "north", 99}})
			replaceArchive(t, svc, fsys, append(append([]string(nil), base...), "e.pulse"))
		}, []string{"e.pulse"}},
		{"same name, same size, new content", func(t *testing.T, svc *Service, fsys afero.Fs) {
			rows := append([]archiveLookupRow(nil), archiveLookupShards[1].rows...)
			rows[0].score = 41
			writeArchiveLookupCohort(t, fsys, "x/b.pulse", archiveLookupSchema(shardLabels(rows)...), rows)
			replaceArchive(t, svc, fsys, []string{"a.pulse", "x/b.pulse", "c.pulse", "d.pulse"})
		}, []string{"b.pulse"}},
		{"a sidecar recording another source", func(t *testing.T, svc *Service, fsys afero.Fs) {
			p := encx.ArchiveShardIndexPath("arch.pulse", "d.pulse", []string{"id"})
			idx, err := encx.ReadIndexFile(fsys, p)
			if err != nil {
				t.Fatal(err)
			}
			idx.SourceSize++
			if err := encx.WriteIndexFile(fsys, p, idx); err != nil {
				t.Fatal(err)
			}
		}, []string{"d.pulse"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fsys := setupArchiveLookup(t, []string{"id"})
			before, err := afero.ReadFile(fsys, "arch.pulse")
			if err != nil {
				t.Fatal(err)
			}
			c.mutate(t, svc, fsys)
			after, _ := afero.ReadFile(fsys, "arch.pulse")
			if c.name != "shard added" && len(after) != len(before) {
				t.Fatalf("fixture: the replacement must keep the archive size (%d vs %d)", len(after), len(before))
			}
			_, err = svc.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "arch.pulse"}, Field: "id", Value: "7"})
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_INDEX_STALE {
				t.Fatalf("lookup err = %v, want PULSE_INDEX_STALE", err)
			}
			if got, _ := json.Marshal(ce.Details["shards_uncovered"]); string(got) != alJSON(t, c.missing) {
				t.Errorf("shards_uncovered = %s, want %v", got, c.missing)
			}
			v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
			if err != nil {
				t.Fatal(err)
			}
			if v.Fresh || v.Reason != IndexFreshnessReasonStatMismatch || !v.FastPath || alJSON(t, v.ShardsUncovered) != alJSON(t, c.missing) {
				t.Errorf("verify = %+v, want stale stat_mismatch fast-path naming %v", v, c.missing)
			}
		})
	}
}

func alJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestArchiveLookup_ShardSidecarIsTheAnchorSidecar: a per-shard sidecar
// is byte-identical to the one an anchored build of that shard writes,
// and an anchored lookup reads it with no build of its own.
func TestArchiveLookup_ShardSidecarIsTheAnchorSidecar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, []string{"id"})
	res, err := svc.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "arch.pulse#b.pulse"}, Field: "id", Value: "7", ReturnColumns: []string{"score"}})
	if err != nil {
		t.Fatalf("anchored lookup on the archive-built sidecar: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["score"] != 60.0 {
		t.Errorf("anchored rows = %v, want score 60", res.Rows)
	}
	for _, sh := range archiveLookupShards {
		p := encx.ArchiveShardIndexPath("arch.pulse", sh.name, []string{"id"})
		archiveBuilt, err := afero.ReadFile(fsys, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.BuildIndex(ctx, "arch.pulse#"+sh.name, []string{"id"}); err != nil {
			t.Fatalf("anchored BuildIndex(%s): %v", sh.name, err)
		}
		anchorBuilt, err := afero.ReadFile(fsys, p)
		if err != nil {
			t.Fatal(err)
		}
		if string(archiveBuilt) != string(anchorBuilt) {
			t.Errorf("%s: archive-built sidecar differs from the anchored build's", sh.name)
		}
	}
}

// TestArchiveLookup_VerifyListDrop is FR-35's admin half: verify hashes
// every shard against its sidecar, list serves the manifest with the
// per-shard files, a missing per-shard file is PULSE_INDEX_MANIFEST_STALE,
// and drop removes the entry and every per-shard sidecar.
func TestArchiveLookup_VerifyListDrop(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, []string{"id"}, []string{"region"})

	v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Fresh || v.Reason != IndexFreshnessReasonFingerprintMatch || v.FastPath || v.IndexPath != encx.SidecarIndexPath("arch.pulse", []string{"id"}) {
		t.Fatalf("verify fresh = %+v", v)
	}

	// A sidecar whose fingerprint no longer matches its shard.
	pa := encx.ArchiveShardIndexPath("arch.pulse", "a.pulse", []string{"id"})
	idx, err := encx.ReadIndexFile(fsys, pa)
	if err != nil {
		t.Fatal(err)
	}
	good := idx.Fingerprint
	idx.Fingerprint[0] ^= 0xff
	if err := encx.WriteIndexFile(fsys, pa, idx); err != nil {
		t.Fatal(err)
	}
	v, err = svc.VerifyIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Fresh || v.Reason != IndexFreshnessReasonFingerprintMismatch || alJSON(t, v.ShardsMismatched) != `["a.pulse"]` {
		t.Errorf("verify tampered = %+v, want fingerprint_mismatch on a.pulse", v)
	}
	idx.Fingerprint = good
	if err := encx.WriteIndexFile(fsys, pa, idx); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListIndexes(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListIndexes = %+v, want the id and region indexes", list)
	}
	for _, info := range list {
		if len(info.ShardIndexPaths) != len(archiveLookupShards) {
			t.Errorf("%v: ShardIndexPaths = %v, want one per shard", info.Keys, info.ShardIndexPaths)
		}
		// Per-shard sums: id 5 sits in two shards, so 7 distinct per
		// shard against 6 across the union.
		if info.Keys[0] == "id" && (info.DistinctKeys != 7 || info.IndexedRecords != 7) {
			t.Errorf("id index counts = %d/%d, want per-shard sums 7/7", info.DistinctKeys, info.IndexedRecords)
		}
	}

	pd := encx.ArchiveShardIndexPath("arch.pulse", "d.pulse", []string{"region"})
	raw, _ := afero.ReadFile(fsys, pd)
	if err := fsys.Remove(pd); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListIndexes(ctx, "arch.pulse"); !errors.HasCode(err, errors.PULSE_INDEX_MANIFEST_STALE) {
		t.Errorf("list with a per-shard sidecar gone = %v, want PULSE_INDEX_MANIFEST_STALE", err)
	}
	if _, err := svc.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "arch.pulse"}, Field: "region", Value: "north"}); !errors.HasCode(err, errors.PULSE_INDEX_MANIFEST_STALE) {
		t.Errorf("lookup with a per-shard sidecar gone = %v, want PULSE_INDEX_MANIFEST_STALE", err)
	}
	if err := afero.WriteFile(fsys, pd, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.DropIndex(ctx, "arch.pulse", []string{"id"}); err != nil {
		t.Fatalf("DropIndex: %v", err)
	}
	for _, sh := range archiveLookupShards {
		if ok, _ := afero.Exists(fsys, encx.ArchiveShardIndexPath("arch.pulse", sh.name, []string{"id"})); ok {
			t.Errorf("drop left %s's sidecar", sh.name)
		}
	}
	list, err = svc.ListIndexes(ctx, "arch.pulse")
	if err != nil || len(list) != 1 || list[0].Keys[0] != "region" {
		t.Errorf("after drop ListIndexes = %+v, %v; want the region index alone", list, err)
	}
	if _, err := svc.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "arch.pulse"}, Field: "id", Value: "1"}); !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Errorf("lookup after drop = %v, want PULSE_INDEX_MISSING", err)
	}
	if err := svc.DropIndex(ctx, "arch.pulse", []string{"id"}); !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Errorf("second drop = %v, want PULSE_INDEX_MISSING", err)
	}
	if _, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"}); !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Errorf("verify after drop = %v, want PULSE_INDEX_MISSING", err)
	}
	if ok, _ := afero.Exists(fsys, filepath.Join(".", encx.IndexManifestPath("arch.pulse"))); !ok {
		t.Error("the manifest must survive while another index is listed in it")
	}
}
