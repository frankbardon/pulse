package service

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// archive_index_upkeep_test.go pins shard add / remove index upkeep
// (U51 #359, FR-34): an ordinary add builds the new shard's sidecar for
// every archive index and appends its manifest record, a remove deletes
// the removed shard's sidecar and record and touches nothing else —
// both leaving exactly what a fresh `index build` would write — under
// BUILD BEFORE PUBLISH: a failed upkeep build aborts the add with the
// archive, every sidecar and the manifest byte-identical.

// removeShardErr drops RemoveShard's result for error-only call sites.
func removeShardErr(_ *RemoveShardResult, err error) error { return err }

var upkeepKeySets = [][]string{{"id"}, {"region", "id"}}

// snapshotFiles reads every file under the fs root.
func snapshotFiles(t *testing.T, fsys afero.Fs) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := afero.Walk(fsys, ".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := afero.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		out[filepath.Clean(path)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// diffSnapshots names every path added, removed or changed.
func diffSnapshots(before, after map[string]string) []string {
	var out []string
	for p, b := range before {
		if a, ok := after[p]; !ok {
			out = append(out, "-"+p)
		} else if a != b {
			out = append(out, "~"+p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			out = append(out, "+"+p)
		}
	}
	sort.Strings(out)
	return out
}

// archiveIndexFiles is the archive's manifest plus every per-shard
// sidecar of the upkeep key sets, read back.
func archiveIndexFiles(t *testing.T, fsys afero.Fs, shards []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	paths := []string{encx.IndexManifestPath("arch.pulse")}
	for _, keys := range upkeepKeySets {
		for _, sh := range shards {
			paths = append(paths, encx.ArchiveShardIndexPath("arch.pulse", sh, keys))
		}
	}
	for _, p := range paths {
		raw, err := afero.ReadFile(fsys, p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		out[p] = string(raw)
	}
	return out
}

// rebuildMatches rebuilds every upkeep index from scratch and fails if
// the manifest or any per-shard sidecar the upkeep left differs from
// the fresh build's bytes.
func rebuildMatches(t *testing.T, svc *Service, fsys afero.Fs, shards []string) {
	t.Helper()
	kept := archiveIndexFiles(t, fsys, shards)
	for _, keys := range upkeepKeySets {
		if _, err := svc.BuildIndex(context.Background(), "arch.pulse", keys); err != nil {
			t.Fatalf("BuildIndex(%v): %v", keys, err)
		}
	}
	fresh := archiveIndexFiles(t, fsys, shards)
	for p, want := range fresh {
		if kept[p] != want {
			t.Errorf("%s: upkeep left\n%s\nfresh build writes\n%s", p, kept[p], want)
		}
	}
}

// writeUpkeepShard writes e.pulse: id 5 a third time, a fresh id 9 and
// a label ("west") no stored shard holds, so the canonical dictionary
// grows and only the incoming shard is remapped.
func writeUpkeepShard(t *testing.T, fsys afero.Fs, path string, score float64) {
	t.Helper()
	rows := []archiveLookupRow{{9, "west", score}, {5, "north", 81}}
	writeArchiveLookupCohort(t, fsys, path, archiveLookupSchema(shardLabels(rows)...), rows)
}

func cohesionCodes(ws []encx.CohesionWarning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Code)
	}
	return out
}

// TestArchiveIndexUpkeep_AddExtendsEveryIndex: an add builds the new
// shard's sidecar for each archive index and appends its record, so
// lookups reach the new shard at once — and what it left is byte for
// byte what a fresh build writes. The mandatory warning names each
// index kept.
func TestArchiveIndexUpkeep_AddExtendsEveryIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, upkeepKeySets...)
	writeUpkeepShard(t, fsys, "e.pulse", 80)

	res, err := svc.AddShard(ctx, "arch.pulse", "e.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if len(res.InvalidatedSidecars) != 0 {
		t.Errorf("ordinary add InvalidatedSidecars = %+v, want none", res.InvalidatedSidecars)
	}
	var upd *encx.CohesionWarning
	for i := range res.Warnings {
		if res.Warnings[i].Code == string(errors.PULSE_SHARD_INDEXES_UPDATED) {
			upd = &res.Warnings[i]
		}
	}
	if upd == nil {
		t.Fatalf("warnings = %v, want PULSE_SHARD_INDEXES_UPDATED", cohesionCodes(res.Warnings))
	}
	if upd.Details["action"] != "added" || upd.Details["shard"] != "e.pulse" {
		t.Errorf("warning details = %v", upd.Details)
	}
	idx, _ := upd.Details["indexes"].([]map[string]any)
	if len(idx) != len(upkeepKeySets) {
		t.Fatalf("warning indexes = %v, want one per archive index", upd.Details["indexes"])
	}
	for _, e := range idx {
		keys, _ := e["keys"].([]string)
		if e["shard_index_path"] != encx.ArchiveShardIndexPath("arch.pulse", "e.pulse", keys) {
			t.Errorf("warning entry %v names the wrong shard sidecar", e)
		}
	}

	for _, c := range []struct {
		keys []types.LookupKey
		mode types.LookupMultiplicity
		want string
	}{
		{lk("id", "9"), "", `[{"region":"west","score":80}]`},
		{lk("id", "5"), types.LookupMultiplicityAll, `[{"region":"north","score":30},{"region":"south","score":50},{"region":"north","score":81}]`},
		{lk("region", "west", "id", "9"), "", `[{"region":"west","score":80}]`},
		{lk("id", "7"), "", `[{"region":"east","score":60}]`},
	} {
		if got := lookupOutcome(t, svc, "arch.pulse", c.keys, c.mode); got != c.want {
			t.Errorf("lookup %v = %s, want %s", c.keys, got, c.want)
		}
	}
	if v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"}); err != nil || !v.Fresh {
		t.Errorf("verify after add = %+v, %v; want fresh", v, err)
	}
	rebuildMatches(t, svc, fsys, []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse", "e.pulse"})
}

// TestArchiveIndexUpkeep_RemovePrunesOnlyTheOrphan: a remove deletes the
// removed shard's sidecars and manifest records, and every other file —
// the kept shards' sidecars, the twin's index, the sources — is
// byte-identical.
func TestArchiveIndexUpkeep_RemovePrunesOnlyTheOrphan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, upkeepKeySets...)
	before := snapshotFiles(t, fsys)

	res, err := svc.RemoveShard(ctx, "arch.pulse", "b.pulse")
	if err != nil {
		t.Fatalf("RemoveShard: %v", err)
	}
	if codes := cohesionCodes(res.Warnings); len(codes) != 1 || codes[0] != string(errors.PULSE_SHARD_INDEXES_UPDATED) {
		t.Fatalf("warnings = %v, want PULSE_SHARD_INDEXES_UPDATED", codes)
	}
	if res.Warnings[0].Details["action"] != "removed" || res.ShardCount != 3 {
		t.Errorf("result = %+v", res)
	}

	var want []string
	for _, keys := range upkeepKeySets {
		want = append(want, "-"+encx.ArchiveShardIndexPath("arch.pulse", "b.pulse", keys))
	}
	want = append(want, "~arch.pulse", "~"+encx.IndexManifestPath("arch.pulse"))
	sort.Strings(want)
	if got := diffSnapshots(before, snapshotFiles(t, fsys)); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("remove changed %v\nwant           %v", got, want)
	}

	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "5"), ""); got != `[{"region":"north","score":30}]` {
		t.Errorf("id 5 after removing b = %s, want the one row left", got)
	}
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "7"), ""); got != "PULSE_LOOKUP_NOT_FOUND match_count=<nil>" {
		t.Errorf("id 7 after removing b = %s, want a miss", got)
	}
	rebuildMatches(t, svc, fsys, []string{"a.pulse", "c.pulse", "d.pulse"})
}

// TestArchiveIndexUpkeep_RemoveOfUnindexedShard: a shard no index covers
// leaves the manifest alone and warns nothing; an archive without
// indexes warns nothing either.
func TestArchiveIndexUpkeep_RemoveOfUnindexedShard(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t)
	res, err := svc.RemoveShard(ctx, "arch.pulse", "a.pulse")
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("unindexed remove = %+v, %v; want no warning", res, err)
	}
	if ok, _ := afero.Exists(fsys, encx.IndexManifestPath("arch.pulse")); ok {
		t.Error("an unindexed remove wrote a manifest")
	}
}

// failingWriteFs fails every file creation whose name fail matches.
type failingWriteFs struct {
	afero.Fs
	fail func(name string) bool
}

func (f *failingWriteFs) Create(name string) (afero.File, error) {
	if f.fail(name) {
		return nil, &os.PathError{Op: "create", Path: name, Err: os.ErrPermission}
	}
	return f.Fs.Create(name)
}

func (f *failingWriteFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 && f.fail(name) {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
	}
	return f.Fs.OpenFile(name, flag, perm)
}

// failingUpkeepRenameFs fails the rename of any upkeep stage whose
// final name fail matches; the archive's own rename goes through.
type failingUpkeepRenameFs struct {
	afero.Fs
	fail func(newname string) bool
}

func (f *failingUpkeepRenameFs) Rename(oldname, newname string) error {
	if f.fail(newname) {
		return &os.PathError{Op: "rename", Path: newname, Err: os.ErrPermission}
	}
	return f.Fs.Rename(oldname, newname)
}

// TestArchiveIndexUpkeep_BuildFailureAbortsTheAdd is the owner's BUILD
// BEFORE PUBLISH rule: a failure building any new sidecar or the
// manifest aborts the whole add — the archive, every sidecar and the
// manifest stay byte-identical and no stage is left behind.
func TestArchiveIndexUpkeep_BuildFailureAbortsTheAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	second := encx.ArchiveShardIndexPath("arch.pulse", "e.pulse", upkeepKeySets[1])
	for _, c := range []struct {
		name string
		fail func(string) bool
	}{
		{"second index's sidecar", func(n string) bool { return n == upkeepStagePath(second) }},
		{"manifest", func(n string) bool { return n == upkeepStagePath(encx.IndexManifestPath("arch.pulse")) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, fsys := setupArchiveLookup(t, upkeepKeySets...)
			writeUpkeepShard(t, fsys, "e.pulse", 80)
			before := snapshotFiles(t, fsys)

			cfg, err := fs.New(fs.WithFs(&failingWriteFs{Fs: fsys, fail: c.fail}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := New(cfg).AddShard(ctx, "arch.pulse", "e.pulse"); err == nil {
				t.Fatal("AddShard with a failing upkeep build: want an error")
			}
			if d := diffSnapshots(before, snapshotFiles(t, fsys)); len(d) != 0 {
				t.Errorf("a failed upkeep build changed %v; the add must leave everything untouched", d)
			}
		})
	}
}

// TestArchiveIndexUpkeep_UnreadableManifestAbortsTheMutation: an add or
// remove cannot keep an index it cannot read, so an invalid manifest
// refuses both before anything is written.
func TestArchiveIndexUpkeep_UnreadableManifestAbortsTheMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, op := range []string{"add", "remove"} {
		t.Run(op, func(t *testing.T) {
			svc, fsys := setupArchiveLookup(t, upkeepKeySets...)
			writeUpkeepShard(t, fsys, "e.pulse", 80)
			if err := afero.WriteFile(fsys, encx.IndexManifestPath("arch.pulse"), []byte("{"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshotFiles(t, fsys)
			var err error
			if op == "add" {
				_, err = svc.AddShard(ctx, "arch.pulse", "e.pulse")
			} else {
				_, err = svc.RemoveShard(ctx, "arch.pulse", "a.pulse")
			}
			if !errors.HasCode(err, errors.PULSE_INDEX_MANIFEST_INVALID) {
				t.Fatalf("%s with an invalid manifest = %v, want PULSE_INDEX_MANIFEST_INVALID", op, err)
			}
			if d := diffSnapshots(before, snapshotFiles(t, fsys)); len(d) != 0 {
				t.Errorf("refused %s changed %v", op, d)
			}
		})
	}
}

// TestArchiveIndexUpkeep_PublishFailureAfterTheArchiveWarns: a sidecar
// rename that fails AFTER the archive rename cannot be rolled back; the
// add stands, warns PULSE_INDEX_STALE naming the uncovered shard, leaves
// no stage, and lookups refuse loudly rather than miss the shard.
func TestArchiveIndexUpkeep_PublishFailureAfterTheArchiveWarns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, upkeepKeySets...)
	writeUpkeepShard(t, fsys, "e.pulse", 80)
	manifestBefore, err := afero.ReadFile(fsys, encx.IndexManifestPath("arch.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(&failingUpkeepRenameFs{Fs: fsys, fail: func(n string) bool { return strings.HasSuffix(n, ".idx") }}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := New(cfg).AddShard(ctx, "arch.pulse", "e.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v (the archive was published; upkeep failure is a warning)", err)
	}
	last := res.Warnings[len(res.Warnings)-1]
	if last.Code != string(errors.PULSE_INDEX_STALE) || alJSON(t, last.Details["shards_uncovered"]) != `["e.pulse"]` {
		t.Fatalf("warning = %+v, want PULSE_INDEX_STALE shards_uncovered [e.pulse]", last)
	}
	if after, _ := afero.ReadFile(fsys, encx.IndexManifestPath("arch.pulse")); string(after) != string(manifestBefore) {
		t.Error("the manifest was published although a sidecar rename failed")
	}
	for p := range snapshotFiles(t, fsys) {
		if strings.Contains(p, shardAdminTempPattern) {
			t.Errorf("stage left behind: %s", p)
		}
	}
	_, err = svc.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "arch.pulse"}, Field: "id", Value: "9"})
	if !errors.HasCode(err, errors.PULSE_INDEX_STALE) {
		t.Errorf("lookup after a failed upkeep publish = %v, want PULSE_INDEX_STALE", err)
	}
}

// TestArchiveIndexUpkeep_CompactKeepsSidecarsValid: compact copies every
// stored shard raw (same bytes, size, CRC32), so every sidecar and the
// manifest stay valid and untouched.
func TestArchiveIndexUpkeep_CompactKeepsSidecarsValid(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, upkeepKeySets...)
	before := snapshotFiles(t, fsys)
	if err := svc.CompactShardArchive(ctx, "arch.pulse"); err != nil {
		t.Fatal(err)
	}
	for _, p := range diffSnapshots(before, snapshotFiles(t, fsys)) {
		if p != "~arch.pulse" {
			t.Errorf("compact changed %s", p)
		}
	}
	if v, err := svc.VerifyIndex(ctx, "arch.pulse", []string{"id"}); err != nil || !v.Fresh {
		t.Errorf("verify after compact = %+v, %v; want fresh", v, err)
	}
	if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "7"), ""); got != `[{"region":"east","score":60}]` {
		t.Errorf("lookup after compact = %s", got)
	}
}

// TestArchiveIndexUpkeep_ReplacementAfterUpkeepDetectedByCRC: the record
// an add writes carries the shard's CRC32, so a later same-name,
// same-size replacement by a path that does not keep the index is
// refused, never served.
func TestArchiveIndexUpkeep_ReplacementAfterUpkeepDetectedByCRC(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setupArchiveLookup(t, upkeepKeySets...)
	writeUpkeepShard(t, fsys, "e.pulse", 80)
	if _, err := svc.AddShard(ctx, "arch.pulse", "e.pulse"); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadFile(fsys, "arch.pulse")
	writeUpkeepShard(t, fsys, "x/e.pulse", 99)
	replaceArchive(t, svc, fsys, []string{"a.pulse", "b.pulse", "c.pulse", "d.pulse", "x/e.pulse"})
	after, _ := afero.ReadFile(fsys, "arch.pulse")
	if len(after) != len(before) {
		t.Fatalf("fixture: the replacement must keep the archive size (%d vs %d)", len(after), len(before))
	}
	_, err := svc.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "arch.pulse"}, Field: "id", Value: "1"})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_INDEX_STALE || alJSON(t, ce.Details["shards_uncovered"]) != `["e.pulse"]` {
		t.Fatalf("lookup = %v, want PULSE_INDEX_STALE shards_uncovered [e.pulse]", err)
	}
}

// TestArchiveIndexUpkeep_ReencodingAddReportsInvalidatedSidecars: an add
// that widens the archive re-encodes every stored shard, so it keeps
// nothing — the manifest and sidecars are left as they were, no upkeep
// warning — and reports every per-shard sidecar as invalidated, the
// list Pulse.InvalidatedSidecars derives; lookups refuse until rebuilt.
func TestArchiveIndexUpkeep_ReencodingAddReportsInvalidatedSidecars(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys := setWidenFixture(t)
	if _, err := svc.BuildIndex(ctx, "arch.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	want, err := svc.InvalidatedIndexSidecars(ctx, "arch.pulse")
	if err != nil || len(want) != 1 {
		t.Fatalf("InvalidatedIndexSidecars before = %+v, %v", want, err)
	}
	manifest := encx.IndexManifestPath("arch.pulse")
	sidecar := encx.ArchiveShardIndexPath("arch.pulse", "seed.pulse", []string{"id"})
	mBefore, _ := afero.ReadFile(fsys, manifest)
	sBefore, _ := afero.ReadFile(fsys, sidecar)

	res, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Widened) == 0 || !res.Widened[0].ArchiveWidened() {
		t.Fatalf("fixture: want an archive widen, got %+v", res.Widened)
	}
	for _, c := range cohesionCodes(res.Warnings) {
		if c == string(errors.PULSE_SHARD_INDEXES_UPDATED) {
			t.Error("a re-encoding add claimed to keep the index")
		}
	}
	after, err := svc.InvalidatedIndexSidecars(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if alJSON(t, res.InvalidatedSidecars) != alJSON(t, want) || alJSON(t, after) != alJSON(t, want) {
		t.Errorf("InvalidatedSidecars = %s\nInvalidatedIndexSidecars before = %s after = %s", alJSON(t, res.InvalidatedSidecars), alJSON(t, want), alJSON(t, after))
	}
	if res.InvalidatedSidecars[0].Path != sidecar || res.InvalidatedSidecars[0].Rebuild != "pulse index build arch.pulse --key id" {
		t.Errorf("stale sidecar = %+v", res.InvalidatedSidecars[0])
	}
	mAfter, _ := afero.ReadFile(fsys, manifest)
	sAfter, _ := afero.ReadFile(fsys, sidecar)
	if string(mAfter) != string(mBefore) || string(sAfter) != string(sBefore) {
		t.Error("a re-encoding add rewrote the index it does not keep")
	}
	_, err = svc.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "arch.pulse"}, Field: "id", Value: "1"})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_INDEX_STALE || alJSON(t, ce.Details["shards_uncovered"]) != `["seed.pulse","add.pulse"]` {
		t.Fatalf("lookup after a re-encoding add = %v, want PULSE_INDEX_STALE over both shards", err)
	}
}
