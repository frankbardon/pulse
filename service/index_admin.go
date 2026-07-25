package service

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// IndexInfo describes one sidecar point-lookup index discovered
// alongside a cohort by Service.ListIndexes: the sidecar's on-disk
// path (encoding.SidecarIndexPath's derived shape), its ordered key
// column names, and the same distinct-key / indexed-record summary
// Service.BuildIndex's result carries — computed here from the
// sidecar's own bucket table (encoding.ReadIndexFile), not re-derived
// from re-scanning the cohort.
type IndexInfo struct {
	IndexPath      string
	Keys           []string
	DistinctKeys   int
	IndexedRecords int
}

// sidecarIndexNameRegexp matches the basename shape
// encoding.SidecarIndexPath derives for cohortBase:
// "<cohortBase>.<16-hex-digit-keyhash>.idx". Anchored on both ends so a
// coincidentally-named unrelated file (e.g. a hand-placed
// "cohort.pulse.deadbeefdeadbeef.idx.bak") never matches.
func sidecarIndexNameRegexp(cohortBase string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(cohortBase) + `\.[0-9a-f]{16}\.idx$`)
}

// ListIndexes enumerates every sidecar point-lookup index built
// against the cohort at path: it lists path's containing directory
// (afero.ReadDir, never raw os), keeps only entries whose basename
// matches the encoding.SidecarIndexPath shape for this cohort's
// basename, and reads each match's key spec + bucket table
// (encoding.ReadIndexFile) to populate IndexInfo. Results are sorted
// by IndexPath for a deterministic, rebuild-stable listing order (a
// directory listing's raw order is not guaranteed across afero
// backends).
//
// No sidecar indexes is not an error — ListIndexes returns an empty
// (non-nil) slice. Shard archive cohorts (same leading-magic-bytes
// dispatch Service.Open already performs) are rejected with
// PULSE_INDEX_UNSUPPORTED_SHARDED, matching Service.BuildIndex /
// Service.VerifyIndex's v1 single-file-only contract.
func (s *Service) ListIndexes(ctx context.Context, path string) ([]IndexInfo, error) {
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(cohort.Shards()) > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_UNSUPPORTED_SHARDED,
			"point-lookup index list does not support shard archive cohorts",
			map[string]any{"cohort": path})
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	re := sidecarIndexNameRegexp(base)

	entries, err := afero.ReadDir(fsys, dir)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("listing sidecar indexes for cohort: %s", path))
	}

	out := make([]IndexInfo, 0)
	for _, ent := range entries {
		if ent.IsDir() || !re.MatchString(ent.Name()) {
			continue
		}
		full := filepath.Join(dir, ent.Name())
		idx, err := encoding.ReadIndexFile(fsys, full)
		if err != nil {
			return nil, err
		}
		out = append(out, IndexInfo{
			IndexPath:      full,
			Keys:           indexKeyNames(idx),
			DistinctKeys:   countIndexDistinctKeys(idx),
			IndexedRecords: countIndexIndexedRecords(idx),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].IndexPath < out[j].IndexPath })
	return out, nil
}

// DropIndex removes the sidecar point-lookup index built for keyFields
// against the cohort at path (encoding.SidecarIndexPath derives the
// exact path). Non-interactive by design — there is no confirmation
// prompt (a prompt would block a scripted/automated caller); callers
// that want a confirm-before-delete UX build it on top of ListIndexes +
// DropIndex themselves. Sidecars are cheap, deterministic rebuild
// artifacts (see encoding.IndexFormatVersion's doc comment), so
// dropping one is never destructive of anything Service.BuildIndex
// cannot regenerate.
//
// Returns PULSE_INDEX_MISSING (not a silent no-op) when no sidecar
// exists at the derived path for keyFields — a caller who asked to
// drop a specific key-set's index gets a clear, coded signal rather
// than an ambiguous success, matching Service.VerifyIndex's contract
// for the same "no sidecar found" case. Shard archive cohorts are
// rejected with PULSE_INDEX_UNSUPPORTED_SHARDED, matching
// Service.BuildIndex / Service.VerifyIndex / Service.ListIndexes's v1
// single-file-only contract.
func (s *Service) DropIndex(ctx context.Context, path string, keyFields []string) error {
	if len(keyFields) == 0 {
		return errors.NewCodedError(errors.SERVICE_VALIDATION,
			"index drop requires at least one key column")
	}

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return err
	}
	if len(cohort.Shards()) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_UNSUPPORTED_SHARDED,
			"point-lookup index drop does not support shard archive cohorts",
			map[string]any{"cohort": path})
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}

	indexPath := encoding.SidecarIndexPath(path, keyFields)
	exists, err := afero.Exists(fsys, indexPath)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("checking sidecar index existence: %s", indexPath))
	}
	if !exists {
		return errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MISSING,
			"no sidecar point-lookup index found for these key fields",
			map[string]any{"cohort": path, "fields": keyFields, "index_path": indexPath})
	}

	if err := fsys.Remove(indexPath); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("removing sidecar index: %s", indexPath))
	}
	return nil
}

// indexKeyNames extracts the ordered key column names from idx's key
// spec, discarding the on-wire FieldType (ListIndexes' contract is
// "each one's key columns" — the type is available to a caller that
// re-reads the sidecar directly via encoding.ReadIndexFile).
func indexKeyNames(idx *encoding.Index) []string {
	names := make([]string, len(idx.Keys))
	for i, k := range idx.Keys {
		names[i] = k.Name
	}
	return names
}

// countIndexDistinctKeys sums the number of key entries across every
// hash bucket — mirrors internal/cli's countDistinctIndexKeys but
// operates directly on an *encoding.Index (ListIndexes reads the
// sidecar directly rather than going through a fresh BuildIndex scan).
func countIndexDistinctKeys(idx *encoding.Index) int {
	n := 0
	for _, b := range idx.Buckets {
		n += len(b.Entries)
	}
	return n
}

// countIndexIndexedRecords sums every entry's RowIDs across every
// bucket — mirrors internal/cli's countIndexedRecords but operates
// directly on an *encoding.Index.
func countIndexIndexedRecords(idx *encoding.Index) int {
	n := 0
	for _, b := range idx.Buckets {
		for _, e := range b.Entries {
			n += len(e.RowIDs)
		}
	}
	return n
}
