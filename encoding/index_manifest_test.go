package encoding

import (
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func manifestTestIndex(keys ...IndexKeySpec) *Index {
	return &Index{
		Keys: keys,
		Buckets: []IndexBucket{
			{Entries: []IndexEntry{{Key: []byte{1}, RowIDs: []uint64{0, 3}}}},
			{},
			{Entries: []IndexEntry{{Key: []byte{2}, RowIDs: []uint64{1}}}},
		},
	}
}

// TestIndexManifestPath_IsDerivableWithoutTheKeyTuple is the whole
// point of the manifest stated as an assertion: its path is a function
// of the COHORT alone, while a sidecar's path is a function of the key
// tuple a caller is trying to discover. A caller holding only the
// cohort path can open one and not the other.
func TestIndexManifestPath_IsDerivableWithoutTheKeyTuple(t *testing.T) {
	const cohort = "brandscape.pulse"

	got := IndexManifestPath(cohort)
	if want := cohort + IndexManifestSuffix; got != want {
		t.Fatalf("IndexManifestPath(%q) = %q, want %q", cohort, got, want)
	}

	// The sidecar's own name cannot be derived from the cohort alone —
	// two different key tuples produce two different, unguessable names,
	// and key ORDER is part of what is unguessable.
	a := SidecarIndexPath(cohort, []string{"category_id", "brand_id", "temporal_code"})
	b := SidecarIndexPath(cohort, []string{"temporal_code", "brand_id", "category_id"})
	if a == b {
		t.Fatal("key order must change the sidecar path, or the manifest is solving a problem that does not exist")
	}
	if strings.Contains(a, "category_id") {
		t.Fatalf("sidecar path %q leaks a key name — this test's premise (the name is a hash) is wrong", a)
	}
}

// TestReadIndexManifest_AbsentIsNotAnError locks the fallback
// contract: a corpus whose indexes were built before the manifest
// existed has none, and that is an ordinary state Service.ListIndexes
// answers from the directory listing — not a fault.
func TestReadIndexManifest_AbsentIsNotAnError(t *testing.T) {
	fsys := afero.NewMemMapFs()
	m, present, err := ReadIndexManifest(fsys, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadIndexManifest over a manifest-free cohort: %v", err)
	}
	if present {
		t.Error("present = true with no manifest on disk")
	}
	if m != nil {
		t.Errorf("manifest = %+v, want nil when absent", m)
	}
}

// TestReadIndexManifest_UntrustworthyDocumentIsRefused covers every
// way a manifest can exist and not be usable. Each is refused rather
// than read as an empty manifest, because "this cohort has no indexes"
// is a plausible-looking answer that sends a caller to rebuild data
// that is already there.
func TestReadIndexManifest_UntrustworthyDocumentIsRefused(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"kind": `},
		{name: "foreign kind", body: `{"format_version":"1","kind":"orbit.folder-index","indexes":[]}`},
		{name: "unknown format version", body: `{"format_version":"99","kind":"pulse.index-manifest","indexes":[]}`},
		{name: "entry names no index file", body: `{"format_version":"1","kind":"pulse.index-manifest","indexes":[{"keys":[{"name":"id","type":"u32"}]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := afero.NewMemMapFs()
			if err := afero.WriteFile(fsys, IndexManifestPath("cohort.pulse"), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			_, present, err := ReadIndexManifest(fsys, "cohort.pulse")
			if !present {
				t.Error("present = false for a manifest that exists on disk")
			}
			if !errors.HasCode(err, errors.PULSE_INDEX_MANIFEST_INVALID) {
				t.Fatalf("want PULSE_INDEX_MANIFEST_INVALID, got: %v", err)
			}
			var coded *errors.CodedError
			if !stderrors.As(err, &coded) {
				t.Fatalf("not a CodedError: %v", err)
			}
			if coded.Details["manifest_path"] != IndexManifestPath("cohort.pulse") {
				t.Errorf("details must name the offending path, got %v", coded.Details)
			}
		})
	}
}

// TestIndexManifestEntryFor_CarriesTheOrderedTupleAndItsOwnHash is the
// recovery direction that the naming scheme makes impossible on its
// own: given the entry, both the ordered key tuple AND the sidecar
// filename that tuple derives are in hand, so a reader can confirm the
// file it is about to open is the one the tuple describes.
func TestIndexManifestEntryFor_CarriesTheOrderedTupleAndItsOwnHash(t *testing.T) {
	keys := []IndexKeySpec{
		{Name: "category_id", Type: FieldTypeU16},
		{Name: "brand_id", Type: FieldTypeU32},
		{Name: "temporal_code", Type: FieldTypeU32},
	}
	indexPath := SidecarIndexPath("brandscape.pulse", []string{"category_id", "brand_id", "temporal_code"})
	entry := IndexManifestEntryFor(indexPath, manifestTestIndex(keys...))

	want := []string{"category_id", "brand_id", "temporal_code"}
	got := entry.KeyNames()
	if len(got) != len(want) {
		t.Fatalf("KeyNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("KeyNames() = %v, want %v — order is significant end to end", got, want)
		}
	}
	if entry.Keys[1].Type != FieldTypeU32.String() {
		t.Errorf("key type = %q, want %q", entry.Keys[1].Type, FieldTypeU32.String())
	}
	if entry.DistinctKeys != 2 || entry.IndexedRecords != 3 {
		t.Errorf("distinct=%d records=%d, want 2 and 3", entry.DistinctKeys, entry.IndexedRecords)
	}

	// The recorded hash must be the one the recovered tuple derives —
	// that round trip is what makes the entry checkable.
	rebuilt := SidecarIndexPath("brandscape.pulse", got)
	if !strings.HasSuffix(rebuilt, "."+entry.KeyHash+".idx") {
		t.Errorf("key_hash %q does not match the path the recovered tuple derives (%q)", entry.KeyHash, rebuilt)
	}
}

// TestIndexManifest_IsByteStableRegardlessOfBuildOrder — the document
// is a rebuild artifact that lands in diffs and syncs, so two corpora
// with the same indexes built in different orders must produce the same
// bytes.
func TestIndexManifest_IsByteStableRegardlessOfBuildOrder(t *testing.T) {
	entryA := IndexManifestEntryFor(SidecarIndexPath("c.pulse", []string{"a"}), manifestTestIndex(IndexKeySpec{Name: "a", Type: FieldTypeU32}))
	entryB := IndexManifestEntryFor(SidecarIndexPath("c.pulse", []string{"b"}), manifestTestIndex(IndexKeySpec{Name: "b", Type: FieldTypeU32}))

	write := func(order ...IndexManifestEntry) []byte {
		fsys := afero.NewMemMapFs()
		m := NewIndexManifest("c.pulse")
		for _, e := range order {
			m.Upsert(e)
		}
		if err := WriteIndexManifest(fsys, "c.pulse", m); err != nil {
			t.Fatalf("WriteIndexManifest: %v", err)
		}
		raw, err := afero.ReadFile(fsys, IndexManifestPath("c.pulse"))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if string(write(entryA, entryB)) != string(write(entryB, entryA)) {
		t.Error("manifest bytes depend on the order indexes were built in")
	}

	// Upsert replaces rather than duplicating: rebuilding the same key
	// tuple must not grow the document.
	m := NewIndexManifest("c.pulse")
	m.Upsert(entryA)
	m.Upsert(entryA)
	if len(m.Indexes) != 1 {
		t.Errorf("Indexes = %d entries after two upserts of one tuple, want 1", len(m.Indexes))
	}
	if !m.Remove(entryA.IndexPath) {
		t.Error("Remove reported no entry for one that was upserted")
	}
	if m.Remove(entryA.IndexPath) {
		t.Error("Remove reported an entry the second time")
	}
}

// TestWriteIndexManifest_EmptyCatalogIsAnEmptyArray — a cohort whose
// last index was dropped keeps a readable manifest saying so, rather
// than a null that a consumer has to special-case.
func TestWriteIndexManifest_EmptyCatalogIsAnEmptyArray(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := WriteIndexManifest(fsys, "c.pulse", &IndexManifest{}); err != nil {
		t.Fatalf("WriteIndexManifest: %v", err)
	}
	raw, err := afero.ReadFile(fsys, IndexManifestPath("c.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		FormatVersion string             `json:"format_version"`
		Kind          string             `json:"kind"`
		Cohort        string             `json:"cohort"`
		Indexes       *[]json.RawMessage `json:"indexes"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("written manifest does not parse: %v", err)
	}
	if probe.Indexes == nil {
		t.Error("indexes key is absent or null, want an empty array")
	}
	if probe.FormatVersion != IndexManifestFormatVersion || probe.Kind != IndexManifestKind {
		t.Errorf("write must stamp kind + format_version, got %+v", probe)
	}
	if probe.Cohort != "c.pulse" {
		t.Errorf("cohort = %q, want the basename %q", probe.Cohort, "c.pulse")
	}
}
