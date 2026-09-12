package service

import (
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// cohortFingerprintCacheCap bounds the per-Service fingerprint memo at
// a number of distinct cohort paths, not a byte budget: one entry is a
// path string plus 48 bytes of stat + digest, so 1024 of them is well
// under a megabyte while covering every corpus a single process is
// plausibly serving lookups against. Overflow CLEARS the map rather
// than evicting a victim — the memo is a pure optimisation whose miss
// path is always correct, so a generational flush costs one extra hash
// per live cohort and needs no LRU bookkeeping on the hot path.
const cohortFingerprintCacheCap = 1024

// cohortFingerprintEntry memoises one cohort file's content digest
// alongside the stat pair it was computed under. The entry is served
// only when the file still reports that exact (size, modTime) pair, so
// the memo inherits precisely the assumption the read-path stat check
// already makes on a match — same size and same mtime means same bytes
// — and introduces no freshness gap that did not already exist. The
// residual case is identical and already documented: an in-place edit
// preserving BOTH size and mtime.
//
// Service.VerifyIndex never READS this memo — its whole job is the
// conclusive answer, and serving it from a memo keyed on the very stat
// pair it exists not to trust would hollow that out. It does WRITE to
// it: a digest verify just computed at a known stat pair is as true as
// one Lookup computed, and warming the memo makes the following lookup
// cheap.
type cohortFingerprintEntry struct {
	size        uint64
	modTime     int64
	fingerprint encoding.Fingerprint
}

// cohortFingerprintCache is the memo itself. Lazily initialised (a
// Service built as &Service{fs: cfg} has no constructor hook to seed
// it) and mutex-guarded, because Lookup is safe for concurrent use.
type cohortFingerprintCache struct {
	mu      sync.Mutex
	entries map[string]cohortFingerprintEntry
}

// lookup returns the memoised fingerprint for path iff it was computed
// under the same (size, modTime) pair the caller just stat'd.
func (c *cohortFingerprintCache) load(path string, size uint64, modTime int64) (encoding.Fingerprint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ent, ok := c.entries[path]
	if !ok || ent.size != size || ent.modTime != modTime {
		return encoding.Fingerprint{}, false
	}
	return ent.fingerprint, true
}

// store records fp for path under the stat pair it was computed from,
// replacing any earlier entry for that path (a cohort whose stat moved
// has a new digest, and keeping the old one would only waste a slot).
func (c *cohortFingerprintCache) store(path string, size uint64, modTime int64, fp encoding.Fingerprint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]cohortFingerprintEntry, 8)
	}
	if len(c.entries) >= cohortFingerprintCacheCap {
		if _, replacing := c.entries[path]; !replacing {
			c.entries = make(map[string]cohortFingerprintEntry, 8)
		}
	}
	c.entries[path] = cohortFingerprintEntry{size: size, modTime: modTime, fingerprint: fp}
}

// cohortFingerprint returns the content fingerprint of the cohort at
// path, served from the memo when the supplied stat pair matches the
// one an earlier call computed under, and computed (then memoised)
// otherwise. size/modTime come from the caller's own statCohortFile so
// the file is stat'd once per decision, not twice.
func (s *Service) cohortFingerprint(fsys afero.Fs, path string, size uint64, modTime int64, useMemo bool) (encoding.Fingerprint, error) {
	if useMemo {
		if fp, ok := s.fingerprints.load(path, size, modTime); ok {
			return fp, nil
		}
	}
	fp, err := computeCohortFingerprint(fsys, path)
	if err != nil {
		return encoding.Fingerprint{}, err
	}
	s.fingerprints.store(path, size, modTime, fp)
	return fp, nil
}

// indexFreshness is the verdict classifyIndexFreshness reaches, in the
// shape both read paths need: Service.Lookup only asks Fresh, while
// Service.VerifyIndex additionally reports HOW the verdict was reached.
type indexFreshness struct {
	// Fresh is true when the cohort still matches the sidecar's
	// snapshot — either by a hash-free stat match or by a confirmed
	// fingerprint comparison.
	Fresh bool

	// SizeMismatch is true when the cohort's byte length no longer
	// matches the sidecar's recorded SourceSize. That verdict is
	// reached WITHOUT hashing and is conclusive rather than merely
	// cheap: the fingerprint is taken over the whole file, so a
	// different length cannot produce the recorded digest.
	SizeMismatch bool

	// ModTimeDrift is true when the size matched and the modification
	// time did not. This is NOT by itself a staleness verdict — see
	// classifyIndexFreshness for why it escalates to the fingerprint
	// instead of short-circuiting.
	ModTimeDrift bool

	// HashChecked is true when the verdict rests on a full
	// encoding.ComputeFingerprint comparison (possibly served from the
	// per-Service memo) rather than on the stat pair alone.
	HashChecked bool

	// Size and ModTime are the stat pair the verdict was reached
	// against, returned so a caller that needs the cohort's byte length
	// afterwards (Service.Lookup hands it to openRecordLocator) reuses
	// this stat instead of taking a second one that could disagree with
	// the one the freshness decision was made on.
	Size    uint64
	ModTime int64
}

// classifyIndexFreshness decides whether the cohort at path still
// matches the source snapshot the sidecar recorded in meta.
//
// The decision tree, and why it is shaped this way:
//
//  1. SIZE MISMATCH is a conclusive rejection, taken without hashing.
//     The sidecar's Fingerprint covers the whole file, so a file of a
//     different length cannot hash to it — there is nothing a recompute
//     could add.
//
//  2. MTIME DRIFT WITH A MATCHING SIZE ESCALATES TO THE FINGERPRINT.
//     It is NOT a rejection. A modification time is metadata the
//     filesystem currently reporting the file may simply not carry at
//     the resolution the index recorded: the snapshot is stored as Unix
//     NANOSECONDS, an S3 LastModified is whole SECONDS, and every copy,
//     restore and cache hop in between truncates somewhere of its own
//     choosing. Treating that as proof of mutation refuses an index
//     whose source is byte-identical, and refuses it permanently rather
//     than transiently, so a cohort read through a bucket could not be
//     looked up at all. The fingerprint settles the question the stat
//     pair only raised — which is exactly what the encoding.Index
//     doc comment already promises for a stat MATCH, applied to the one
//     other case the same authority can answer.
//
//  3. A FULL STAT MATCH is served hash-free for a non-authoritative
//     caller (Service.Lookup), because hashing a multi-GB cohort on
//     every point lookup would defeat the O(1) property the sidecar
//     exists to provide, and confirmed with a hash for an authoritative
//     one (Service.VerifyIndex, whose whole job is the conclusive
//     answer).
//
// authoritative=true therefore means both "always hash" and "never
// serve that hash from the memo" — the two are the same statement about
// trusting a stat pair, and splitting them into two parameters would
// let a caller ask for one without the other.
//
// A non-authoritative recompute (case 2) goes through the memo, so a
// backend that never preserves nanoseconds pays the whole-file hash
// ONCE per (path, size, mtime) rather than once per lookup. Without
// that memo this rule would turn a hard failure into an O(cohort) read
// on precisely the deployment that motivated it.
func (s *Service) classifyIndexFreshness(fsys afero.Fs, path string, meta *encoding.IndexMeta, authoritative bool) (indexFreshness, error) {
	size, modTime, err := statCohortFile(fsys, path)
	if err != nil {
		return indexFreshness{}, err
	}

	if size != meta.SourceSize {
		return indexFreshness{Fresh: false, SizeMismatch: true, Size: size, ModTime: modTime}, nil
	}

	drift := modTime != meta.SourceModTime
	if !drift && !authoritative {
		return indexFreshness{Fresh: true, Size: size, ModTime: modTime}, nil
	}

	fp, err := s.cohortFingerprint(fsys, path, size, modTime, !authoritative)
	if err != nil {
		return indexFreshness{}, err
	}

	return indexFreshness{
		Fresh:        fp == meta.Fingerprint,
		ModTimeDrift: drift,
		HashChecked:  true,
		Size:         size,
		ModTime:      modTime,
	}, nil
}
