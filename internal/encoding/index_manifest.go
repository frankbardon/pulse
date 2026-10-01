package encoding

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// IndexManifestSuffix is appended to a cohort's filename to derive its
// sidecar index MANIFEST — "cohort.pulse.indexes.json". It follows the
// same "suffix appended to the cohort filename" convention
// imports.SidecarSuffix (".meta.json") and spss.SidecarSuffix
// (".spss.json") already use, and is deliberately distinct from both.
//
// It exists because SidecarIndexPath derives an index's filename from a
// HASH OF ITS KEY TUPLE, which makes a sidecar impossible to open
// without already knowing the thing a caller is trying to learn. The
// key tuple is recorded INSIDE the sidecar (IndexMeta.Keys), so on a
// local disk the escape is to glob "<cohort>.*.idx" and read each
// match's meta — but OBJECT STORAGE CANNOT LIST A DIRECTORY, so a
// caller reading cohorts out of a bucket has neither the hash nor a
// listing and cannot discover an index at all. Guessing is a
// permutation search, because key ORDER is significant end to end.
//
// This manifest is the keyless, deterministically-named answer: one
// predictable path, reachable with a single GET, carrying every
// sidecar's ordered key tuple. Service.ListIndexes reads it first and
// falls back to the directory listing only when it is absent, so a
// corpus written by a Pulse that predates it keeps working unchanged.
const IndexManifestSuffix = ".indexes.json"

// IndexManifestKind is the manifest document's self-identifying kind.
// A document carrying any other kind is REFUSED rather than read as an
// empty manifest — silently reporting "this cohort has no indexes"
// over a file that is simply something else is exactly the failure the
// manifest exists to remove.
const IndexManifestKind = "pulse.index-manifest"

// IndexManifestFormatVersion is the manifest document's own version,
// independent of both the .pulse FormatVersion and the sidecar
// IndexFormatVersion: the manifest is a JSON catalog, not a binary
// layout, and versioning it separately means adding a field to it can
// never force a cohort or index rebuild. An unrecognised value is
// REFUSED, for the same reason a foreign kind is.
const IndexManifestFormatVersion = "1"

// IndexManifestKey is one key column in a manifest entry's ordered key
// tuple. Type carries FieldType.String() — the manifest reports the
// type as well as the name because a caller composing a lookup key has
// to render its value as a literal, and the per-type literal rules
// (a categorical's dictionary entry, a datetime's parse form) differ.
type IndexManifestKey struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// IndexManifestEntry describes one sidecar index. IndexPath is the
// sidecar's BASENAME, not a full path, so the document survives the
// cohort tree being moved, copied or served under a different prefix —
// a manifest holding absolute paths would be wrong the first time a
// corpus was synced anywhere.
//
// DistinctKeys / IndexedRecords are recorded at build time so a
// listing served from this manifest needs no read of any sidecar. They
// are facts about a deterministic rebuild artifact (BuildIndex over
// unchanged content produces a byte-identical sidecar), not a freshness
// claim: freshness lives in each sidecar's own Fingerprint and
// source-stat snapshot, and nothing here is consulted for it.
type IndexManifestEntry struct {
	IndexPath      string             `json:"index_path"`
	KeyHash        string             `json:"key_hash"`
	Keys           []IndexManifestKey `json:"keys"`
	DistinctKeys   int                `json:"distinct_keys"`
	IndexedRecords int                `json:"indexed_records"`
}

// KeyNames returns the entry's ordered key column names — the tuple a
// caller passes to Lookup / BuildIndex / VerifyIndex, in the order
// those calls require.
func (e IndexManifestEntry) KeyNames() []string {
	names := make([]string, len(e.Keys))
	for i, k := range e.Keys {
		names[i] = k.Name
	}
	return names
}

// IndexManifest is the catalog document itself. Cohort carries the
// cohort's BASENAME for the same location-independence reason
// IndexManifestEntry.IndexPath does.
type IndexManifest struct {
	FormatVersion string               `json:"format_version"`
	Kind          string               `json:"kind"`
	Cohort        string               `json:"cohort"`
	Indexes       []IndexManifestEntry `json:"indexes"`
}

// IndexManifestPath derives the deterministic, KEYLESS on-disk path of
// the sidecar index manifest for cohortPath. Pure — no filesystem
// access. Unlike SidecarIndexPath there is nothing to know in advance:
// that is the whole point.
func IndexManifestPath(cohortPath string) string {
	return cohortPath + IndexManifestSuffix
}

// NewIndexManifest returns an empty, well-formed manifest for
// cohortPath.
func NewIndexManifest(cohortPath string) *IndexManifest {
	return &IndexManifest{
		FormatVersion: IndexManifestFormatVersion,
		Kind:          IndexManifestKind,
		Cohort:        filepath.Base(cohortPath),
		Indexes:       []IndexManifestEntry{},
	}
}

// IndexManifestEntryFor builds the manifest entry describing idx, the
// index just written to indexPath. Counts come from the index's own
// bucket table rather than from a re-scan of the cohort.
func IndexManifestEntryFor(indexPath string, idx *Index) IndexManifestEntry {
	keys := make([]IndexManifestKey, len(idx.Keys))
	for i, k := range idx.Keys {
		keys[i] = IndexManifestKey{Name: k.Name, Type: k.Type.String()}
	}
	distinct, records := 0, 0
	for _, b := range idx.Buckets {
		distinct += len(b.Entries)
		for _, e := range b.Entries {
			records += len(e.RowIDs)
		}
	}
	base := filepath.Base(indexPath)
	return IndexManifestEntry{
		IndexPath:      base,
		KeyHash:        indexKeyHashFromName(base),
		Keys:           keys,
		DistinctKeys:   distinct,
		IndexedRecords: records,
	}
}

// indexKeyHashFromName recovers the 16-hex-digit key hash
// SidecarIndexPath embedded in a sidecar's basename. Recorded on the
// entry so a reader can confirm the name it is about to open is the one
// the key tuple beside it derives — the check the filename-as-hash
// scheme otherwise makes impossible to perform in the useful
// direction. Returns "" for a name that does not carry one.
func indexKeyHashFromName(base string) string {
	if !strings.HasSuffix(base, ".idx") {
		return ""
	}
	trimmed := strings.TrimSuffix(base, ".idx")
	dot := strings.LastIndex(trimmed, ".")
	if dot < 0 {
		return ""
	}
	return trimmed[dot+1:]
}

// Upsert records entry, replacing any earlier entry for the same
// sidecar basename, and keeps Indexes sorted by basename so the
// document is byte-stable across rebuilds regardless of the order
// indexes were built in.
func (m *IndexManifest) Upsert(entry IndexManifestEntry) {
	for i := range m.Indexes {
		if m.Indexes[i].IndexPath == entry.IndexPath {
			m.Indexes[i] = entry
			m.sort()
			return
		}
	}
	m.Indexes = append(m.Indexes, entry)
	m.sort()
}

// Remove drops the entry for the given sidecar basename, reporting
// whether one was present.
func (m *IndexManifest) Remove(indexBase string) bool {
	for i := range m.Indexes {
		if m.Indexes[i].IndexPath == indexBase {
			m.Indexes = append(m.Indexes[:i], m.Indexes[i+1:]...)
			return true
		}
	}
	return false
}

func (m *IndexManifest) sort() {
	sort.Slice(m.Indexes, func(i, j int) bool { return m.Indexes[i].IndexPath < m.Indexes[j].IndexPath })
}

// ReadIndexManifest loads the sidecar index manifest for cohortPath.
// The boolean reports PRESENCE: false with a nil error means no
// manifest exists, which is not an error — a cohort whose indexes were
// built by a Pulse that predates the manifest legitimately has none,
// and Service.ListIndexes falls back to the directory listing there.
//
// A manifest that EXISTS but cannot be trusted is refused with
// PULSE_INDEX_MANIFEST_INVALID naming the path: malformed JSON, a
// foreign Kind, an unrecognised FormatVersion, or an entry with no
// index_path. The alternative — degrading to the directory listing —
// would succeed on a local disk and answer "no indexes" on a bucket,
// which is the same silent, backend-dependent divergence this whole
// mechanism exists to remove.
func ReadIndexManifest(fsys afero.Fs, cohortPath string) (*IndexManifest, bool, error) {
	path := IndexManifestPath(cohortPath)
	exists, err := afero.Exists(fsys, path)
	if err != nil {
		return nil, false, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("checking sidecar index manifest existence: %s", path))
	}
	if !exists {
		return nil, false, nil
	}

	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, false, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading sidecar index manifest: %s", path))
	}

	var m IndexManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, true, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MANIFEST_INVALID,
			"sidecar index manifest is not a readable manifest document",
			map[string]any{"manifest_path": path, "cohort": cohortPath, "reason": "malformed JSON"})
	}
	if m.Kind != IndexManifestKind {
		return nil, true, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MANIFEST_INVALID,
			"sidecar index manifest declares a foreign kind",
			map[string]any{"manifest_path": path, "cohort": cohortPath, "kind": m.Kind, "expected_kind": IndexManifestKind})
	}
	if m.FormatVersion != IndexManifestFormatVersion {
		return nil, true, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MANIFEST_INVALID,
			"sidecar index manifest declares an unrecognised format version",
			map[string]any{"manifest_path": path, "cohort": cohortPath,
				"format_version": m.FormatVersion, "expected_format_version": IndexManifestFormatVersion})
	}
	for i, e := range m.Indexes {
		if e.IndexPath == "" {
			return nil, true, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MANIFEST_INVALID,
				"sidecar index manifest entry names no index file",
				map[string]any{"manifest_path": path, "cohort": cohortPath, "entry": i})
		}
	}
	if m.Indexes == nil {
		m.Indexes = []IndexManifestEntry{}
	}
	m.sort()
	return &m, true, nil
}

// WriteIndexManifest serialises m to cohortPath's derived manifest
// path through fsys (never raw os), indented because the document's
// second job is to be read by a human debugging a corpus. Entries are
// sorted first, so the same set of indexes always produces the same
// bytes.
func WriteIndexManifest(fsys afero.Fs, cohortPath string, m *IndexManifest) error {
	path := IndexManifestPath(cohortPath)
	m.FormatVersion = IndexManifestFormatVersion
	m.Kind = IndexManifestKind
	if m.Cohort == "" {
		m.Cohort = filepath.Base(cohortPath)
	}
	if m.Indexes == nil {
		m.Indexes = []IndexManifestEntry{}
	}
	m.sort()

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("encoding sidecar index manifest: %s", path))
	}
	raw = append(raw, '\n')
	if err := afero.WriteFile(fsys, path, raw, 0644); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing sidecar index manifest: %s", path))
	}
	return nil
}
