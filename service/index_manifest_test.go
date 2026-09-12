package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// bucketFs models the filesystem this whole mechanism exists for: an
// object store. Its defining property is that IT CANNOT LIST A
// DIRECTORY — there are no directory objects, only keys — so every
// glob-and-read escape from the hash-named-sidecar problem is
// unavailable. It also records which paths were opened, which is how a
// test asserts that a listing served from the manifest read no sidecar
// at all rather than merely reading them faster.
type bucketFs struct {
	afero.Fs
	noList bool
	opened []string
}

func (b *bucketFs) note(name string) {
	b.opened = append(b.opened, name)
}

func (b *bucketFs) openedAny(suffix string) string {
	for _, name := range b.opened {
		if strings.HasSuffix(name, suffix) {
			return name
		}
	}
	return ""
}

func (b *bucketFs) rejectListing(name string) error {
	if !b.noList {
		return nil
	}
	info, err := b.Fs.Stat(name)
	if err == nil && info.IsDir() {
		return fmt.Errorf("object storage has no directory to list: %s", name)
	}
	return nil
}

func (b *bucketFs) Open(name string) (afero.File, error) {
	if err := b.rejectListing(name); err != nil {
		return nil, err
	}
	b.note(name)
	return b.Fs.Open(name)
}

func (b *bucketFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if err := b.rejectListing(name); err != nil {
		return nil, err
	}
	b.note(name)
	return b.Fs.OpenFile(name, flag, perm)
}

// manifestService builds a two-index cohort on a bucketFs and returns
// the service, the filesystem and the two key tuples that were built.
func manifestService(t *testing.T, noList bool) (*Service, *bucketFs, [][]string) {
	t.Helper()

	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "cohort.pulse", writePulseFile(t, indexTestSchema(), indexTestRecords()), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	bucket := &bucketFs{Fs: mem}
	cfg, err := fs.New(fs.WithFs(bucket))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	svc := New(cfg)

	tuples := [][]string{{"id"}, {"region", "id"}}
	for _, keys := range tuples {
		if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", keys); err != nil {
			t.Fatalf("BuildIndex(%v): %v", keys, err)
		}
	}
	// Listing is disabled only AFTER the build, so the fixture's own
	// setup is not what is being tested.
	bucket.noList = noList
	bucket.opened = nil
	return svc, bucket, tuples
}

// TestBuildIndex_PublishesTheKeyTupleAtAKeylessPath is the ask stated
// as a test: holding ONLY the cohort path — no key tuple, no hash, no
// directory listing — recover every index's ordered key tuple, and
// confirm each recovered tuple derives the sidecar it describes.
//
// Recovering the tuple by brute-forcing the filename's hash against
// candidate orderings is what a caller had to do before this, and it is
// a diagnostic technique rather than an interface.
func TestBuildIndex_PublishesTheKeyTupleAtAKeylessPath(t *testing.T) {
	_, bucket, tuples := manifestService(t, true)

	manifest, present, err := encoding.ReadIndexManifest(bucket, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadIndexManifest: %v", err)
	}
	if !present {
		t.Fatal("BuildIndex published no manifest — the key tuple is undiscoverable without the hash")
	}
	if len(manifest.Indexes) != len(tuples) {
		t.Fatalf("manifest names %d indexes, want %d", len(manifest.Indexes), len(tuples))
	}

	recovered := map[string][]string{}
	for _, entry := range manifest.Indexes {
		recovered[entry.IndexPath] = entry.KeyNames()
	}
	for _, keys := range tuples {
		base := encoding.SidecarIndexPath("cohort.pulse", keys)
		got, ok := recovered[base]
		if !ok {
			t.Fatalf("manifest does not name the sidecar for %v (has %v)", keys, recovered)
		}
		if strings.Join(got, ",") != strings.Join(keys, ",") {
			t.Fatalf("recovered tuple %v, want %v — order is significant, so a reordered "+
				"recovery composes a key the index cannot answer", got, keys)
		}
	}
}

// TestListIndexes_ServedFromTheManifestWithoutADirectoryListing is the
// load-bearing one: the same enumeration, on a filesystem where
// afero.ReadDir cannot succeed at all.
func TestListIndexes_ServedFromTheManifestWithoutADirectoryListing(t *testing.T) {
	svc, bucket, tuples := manifestService(t, true)

	// Guard the fixture: the old directory-listing path must genuinely
	// be unavailable here, or this test proves nothing.
	if _, err := afero.ReadDir(bucket, "."); err == nil {
		t.Fatal("fixture is not modelling object storage — the directory listed successfully")
	}

	got, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ListIndexes over a no-listing backend: %v", err)
	}
	if len(got) != len(tuples) {
		t.Fatalf("ListIndexes returned %d indexes, want %d: %+v", len(got), len(tuples), got)
	}
	for _, info := range got {
		if len(info.Keys) == 0 {
			t.Errorf("entry %q carries no key tuple", info.IndexPath)
		}
		if info.DistinctKeys == 0 || info.IndexedRecords == 0 {
			t.Errorf("entry %q carries no counts (%d/%d) — the manifest records them precisely so "+
				"this path need not read the sidecar", info.IndexPath, info.DistinctKeys, info.IndexedRecords)
		}
	}

	// And it read no sidecar to do it: on a bucket, downloading every
	// index to answer "which indexes exist" is the cost the recorded
	// counts exist to avoid.
	if opened := bucket.openedAny(".idx"); opened != "" {
		t.Errorf("ListIndexes opened sidecar %q; a manifest-served listing must read none", opened)
	}
}

// TestListIndexes_UnionsASidecarTheManifestDoesNotName covers the
// migration case from the read side: a corpus indexed before the
// manifest existed has sidecars nobody recorded, and the day a manifest
// appears beside them they must not vanish from the listing.
func TestListIndexes_UnionsASidecarTheManifestDoesNotName(t *testing.T) {
	svc, bucket, tuples := manifestService(t, false)

	// Forget one index in the manifest while leaving its sidecar on
	// disk — exactly the shape a pre-manifest build leaves behind.
	manifest, _, err := encoding.ReadIndexManifest(bucket, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadIndexManifest: %v", err)
	}
	orphan := encoding.SidecarIndexPath("cohort.pulse", tuples[0])
	if !manifest.Remove(orphan) {
		t.Fatalf("fixture: manifest did not name %q", orphan)
	}
	if err := encoding.WriteIndexManifest(bucket, "cohort.pulse", manifest); err != nil {
		t.Fatalf("WriteIndexManifest: %v", err)
	}

	got, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(got) != len(tuples) {
		t.Fatalf("ListIndexes returned %d indexes, want %d — a sidecar the manifest omits must "+
			"still be listed wherever the directory can be read: %+v", len(got), len(tuples), got)
	}
	// No duplicates: the union must not list a manifest-named sidecar twice.
	seen := map[string]int{}
	for _, info := range got {
		seen[info.IndexPath]++
	}
	for path, n := range seen {
		if n != 1 {
			t.Errorf("%q listed %d times", path, n)
		}
	}
}

// TestBuildIndex_SeedsTheManifestFromExistingSidecars covers the same
// migration from the write side: the first build after an upgrade must
// not publish a manifest naming only itself, because a manifest-first
// listing then reports that as the whole truth.
func TestBuildIndex_SeedsTheManifestFromExistingSidecars(t *testing.T) {
	svc, bucket, tuples := manifestService(t, false)

	if err := bucket.Remove(encoding.IndexManifestPath("cohort.pulse")); err != nil {
		t.Fatalf("Remove manifest: %v", err)
	}
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"score"}); err != nil {
		t.Fatalf("BuildIndex(score): %v", err)
	}

	manifest, present, err := encoding.ReadIndexManifest(bucket, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadIndexManifest: %v", err)
	}
	if !present {
		t.Fatal("no manifest after a build")
	}
	if len(manifest.Indexes) != len(tuples)+1 {
		names := []string{}
		for _, e := range manifest.Indexes {
			names = append(names, strings.Join(e.KeyNames(), "+"))
		}
		t.Fatalf("manifest names %v, want the new tuple AND the %d sidecars already on disk",
			names, len(tuples))
	}
}

// TestListIndexes_ManifestNamingAMissingSidecarIsStale — skipping the
// entry would report two indexes for a manifest claiming three, which
// is indistinguishable from a correct answer.
func TestListIndexes_ManifestNamingAMissingSidecarIsStale(t *testing.T) {
	svc, bucket, tuples := manifestService(t, false)

	gone := encoding.SidecarIndexPath("cohort.pulse", tuples[0])
	if err := bucket.Remove(gone); err != nil {
		t.Fatalf("Remove sidecar: %v", err)
	}

	_, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if !errors.HasCode(err, errors.PULSE_INDEX_MANIFEST_STALE) {
		t.Fatalf("want PULSE_INDEX_MANIFEST_STALE, got: %v", err)
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("not a CodedError: %v", err)
	}
	if coded.Details["index_path"] != gone {
		t.Errorf("details must name the missing sidecar, got %v", coded.Details)
	}
	if fields, ok := coded.Details["fields"].([]string); !ok || strings.Join(fields, ",") != strings.Join(tuples[0], ",") {
		t.Errorf("details must name the entry's key fields so the repair call can be composed, got %v", coded.Details)
	}
}

// TestDropIndex_PrunesTheOrphanedManifestEntry is the repair path
// PULSE_INDEX_MANIFEST_STALE's fixup points at, and the reason drop
// treats an orphaned entry as a successful drop: if it returned
// PULSE_INDEX_MISSING there, the stale manifest could not be repaired
// by any command at all.
func TestDropIndex_PrunesTheOrphanedManifestEntry(t *testing.T) {
	svc, bucket, tuples := manifestService(t, false)

	if err := bucket.Remove(encoding.SidecarIndexPath("cohort.pulse", tuples[0])); err != nil {
		t.Fatalf("Remove sidecar: %v", err)
	}
	if err := svc.DropIndex(context.Background(), "cohort.pulse", tuples[0]); err != nil {
		t.Fatalf("DropIndex over an orphaned entry must succeed: %v", err)
	}

	got, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ListIndexes after the repair: %v", err)
	}
	if len(got) != len(tuples)-1 {
		t.Fatalf("ListIndexes returned %d indexes, want %d after the drop", len(got), len(tuples)-1)
	}

	// A key tuple that was never built is still PULSE_INDEX_MISSING —
	// the orphan allowance must not turn drop into a silent no-op.
	err = svc.DropIndex(context.Background(), "cohort.pulse", []string{"score"})
	if !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Fatalf("want PULSE_INDEX_MISSING for a tuple with neither sidecar nor entry, got: %v", err)
	}
}

// TestDropIndex_PrunesTheManifestEntryOfALiveIndex — a dropped index
// must stop being DISCOVERABLE as well as stop being readable, or the
// next listing reports it and the lookup that follows fails.
func TestDropIndex_PrunesTheManifestEntryOfALiveIndex(t *testing.T) {
	svc, bucket, tuples := manifestService(t, true)

	if err := svc.DropIndex(context.Background(), "cohort.pulse", tuples[0]); err != nil {
		t.Fatalf("DropIndex: %v", err)
	}

	manifest, _, err := encoding.ReadIndexManifest(bucket, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadIndexManifest: %v", err)
	}
	dropped := encoding.SidecarIndexPath("cohort.pulse", tuples[0])
	for _, e := range manifest.Indexes {
		if e.IndexPath == dropped {
			t.Fatalf("manifest still names the dropped index %q", dropped)
		}
	}
	if len(manifest.Indexes) != len(tuples)-1 {
		t.Errorf("manifest names %d indexes, want %d", len(manifest.Indexes), len(tuples)-1)
	}
}

// TestListIndexes_MalformedManifestIsRefusedNotDegraded locks the
// refusal that looks like over-strictness and is not: the directory
// listing here WOULD have produced the right answer, and taking it
// would make the behaviour depend on the backend — succeeding locally
// and answering "no indexes" on the bucket where the manifest is the
// only source. A caller cannot act on a difference it cannot see.
func TestListIndexes_MalformedManifestIsRefusedNotDegraded(t *testing.T) {
	svc, bucket, _ := manifestService(t, false)

	if err := afero.WriteFile(bucket, encoding.IndexManifestPath("cohort.pulse"), []byte("{oops"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := afero.ReadDir(bucket, "."); err != nil {
		t.Fatalf("fixture: the directory listing must be available here, or the point is lost: %v", err)
	}

	_, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if !errors.HasCode(err, errors.PULSE_INDEX_MANIFEST_INVALID) {
		t.Fatalf("want PULSE_INDEX_MANIFEST_INVALID, got: %v", err)
	}
}

// TestListIndexes_NoManifestFallsBackToTheDirectoryListing keeps the
// pre-manifest path alive: a corpus Pulse has not rebuilt since the
// upgrade must enumerate exactly as it did before.
func TestListIndexes_NoManifestFallsBackToTheDirectoryListing(t *testing.T) {
	svc, bucket, tuples := manifestService(t, false)

	if err := bucket.Remove(encoding.IndexManifestPath("cohort.pulse")); err != nil {
		t.Fatalf("Remove manifest: %v", err)
	}
	got, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ListIndexes with no manifest: %v", err)
	}
	if len(got) != len(tuples) {
		t.Fatalf("ListIndexes returned %d indexes, want %d", len(got), len(tuples))
	}
	for _, info := range got {
		if len(info.Keys) == 0 || info.IndexedRecords == 0 {
			t.Errorf("fallback entry %+v is incomplete — it must read the sidecar's own key spec "+
				"and bucket table", info)
		}
	}
}
