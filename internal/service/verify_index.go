package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// IndexFreshnessReason names why Service.VerifyIndex reached its Fresh
// verdict. All three values are reachable from VerifyIndex's size
// fast-path / full-hash decision tree — see VerifyIndex's doc comment
// for the full contract.
type IndexFreshnessReason string

const (
	// IndexFreshnessReasonStatMismatch indicates the current cohort
	// file's SIZE no longer matches the sidecar's recorded
	// Index.SourceSize. The cohort has definitely changed since the
	// index was built — the fingerprint is taken over the whole file, so
	// a file of a different length cannot hash to the recorded digest —
	// and this verdict is therefore reached WITHOUT computing the
	// content hash (the size fast-path).
	//
	// A modification time that moved while the size held no longer
	// reaches this reason: mtime resolution is not preserved across
	// filesystems, so that case escalates to the fingerprint and lands
	// on IndexFreshnessReasonFingerprintMatch / _Mismatch with
	// ModTimeDrift set. See VerifyIndex's doc comment.
	IndexFreshnessReasonStatMismatch IndexFreshnessReason = "stat_mismatch"

	// IndexFreshnessReasonFingerprintMatch indicates the size matched
	// the sidecar's recorded snapshot and the full content hash
	// (recomputed as confirmation, never skipped) is byte-equal to the
	// sidecar's embedded Fingerprint. Reached whether or not the
	// modification time also matched — VerifyIndexResult.ModTimeDrift
	// reports which.
	IndexFreshnessReasonFingerprintMatch IndexFreshnessReason = "fingerprint_match"

	// IndexFreshnessReasonFingerprintMismatch indicates the size matched
	// the sidecar's recorded snapshot, but the full content hash
	// recomputed for confirmation differs from the sidecar's embedded
	// Fingerprint — either a same-size rewrite the size check cannot
	// rule out, or a same-size rewrite that also moved the mtime
	// (ModTimeDrift true).
	IndexFreshnessReasonFingerprintMismatch IndexFreshnessReason = "fingerprint_mismatch"
)

// VerifyIndexResult is the outcome of Service.VerifyIndex: whether the
// sidecar index at IndexPath is still fresh against its source cohort,
// why, and whether that verdict was reached via the size fast-path
// alone (FastPath=true) or required a full content-hash recompute
// (FastPath=false).
type VerifyIndexResult struct {
	IndexPath string
	Fresh     bool
	Reason    IndexFreshnessReason

	// FastPath is true exactly when Reason ==
	// IndexFreshnessReasonStatMismatch — the size comparison alone was
	// conclusive and no content hash was computed. A fresh=true verdict
	// is NEVER reached via the fast path alone: a matching size is
	// treated as inconclusive on its own and always falls through to a
	// full Fingerprint recompute before VerifyIndex will report
	// Fresh=true.
	FastPath bool

	// ModTimeDrift reports that the cohort file's modification time no
	// longer matches the snapshot taken at index build while its size
	// does. On its own that is NOT staleness — mtime resolution is not
	// preserved across filesystems (an S3 LastModified is whole
	// seconds against the index's recorded nanoseconds), so the verdict
	// comes from the fingerprint and this flag is how a caller sees
	// that the cheap stat pair disagreed and the hash overruled it.
	// Always false alongside IndexFreshnessReasonStatMismatch, which is
	// a size verdict.
	ModTimeDrift bool

	// ShardsUncovered names, for a shard archive, every shard the
	// index does not cover — absent from the manifest entry, a
	// different size or central-directory CRC32 (a replaced shard), or a
	// sidecar recording another source size — in archive order. Any
	// entry makes the verdict stale via the size fast-path
	// (IndexFreshnessReasonStatMismatch, FastPath true, no hash). Nil for
	// a single-file cohort.
	ShardsUncovered []string

	// ShardsMismatched names, for a shard archive whose shards are all
	// covered, every shard whose stored payload no longer hashes to its
	// sidecar's fingerprint (IndexFreshnessReasonFingerprintMismatch).
	// Nil for a single-file cohort.
	ShardsMismatched []string
}

// VerifyIndex reports whether the sidecar point-lookup index built for
// keyFields against the cohort at path is still fresh, using a size
// fast-path before paying for a full content-hash recompute:
//
//  1. Load the sidecar's metadata only (encoding.ReadIndexMetaFile at
//     the path encoding.SidecarIndexPath derives — never the bucket
//     data, which freshness never needs). PULSE_INDEX_MISSING if absent.
//  2. Stat the CURRENT cohort file and compare its SIZE against the
//     sidecar's recorded Index.SourceSize (captured by
//     Service.BuildIndex at build time). If it differs, the cohort has
//     definitely changed — the fingerprint covers the whole file, so a
//     different length cannot produce the recorded digest — so report
//     Fresh=false / Reason=IndexFreshnessReasonStatMismatch /
//     FastPath=true WITHOUT computing the content hash.
//  3. If the size matches, that alone is not sufficient proof of
//     freshness (a pathological same-size rewrite admits a false
//     negative), so fall through to a full encoding.ComputeFingerprint
//     recompute and compare against the sidecar's embedded Fingerprint
//     for a conclusive verdict (IndexFreshnessReasonFingerprintMatch /
//     IndexFreshnessReasonFingerprintMismatch, both FastPath=false).
//     A modification time that moved while the size held takes this
//     same path and sets ModTimeDrift: mtime resolution is NOT
//     preserved across filesystems (the snapshot is Unix nanoseconds,
//     an S3 LastModified is whole seconds, and every copy and cache hop
//     truncates somewhere), so drift alone is not evidence of mutation
//     and the fingerprint is what answers.
//
// VerifyIndex never rebuilds the sidecar and never falls back to a
// full cohort scan — it only reports freshness. Rebuilding stays an
// explicit, caller-initiated Service.BuildIndex call (surfaced as
// `pulse index build`). This method underpins the `pulse index verify`
// CLI leaf (a later story).
//
// A shard archive cohort is verified shard by shard (verifyArchiveIndex):
// the coverage check (name + size + central-directory CRC32 against the
// manifest entry) is its size fast-path, and with every shard covered
// each shard's stored payload is hashed against its sidecar's
// fingerprint. IndexPath is then the index's logical path.
func (s *Service) VerifyIndex(ctx context.Context, path string, keyFields []string) (*VerifyIndexResult, error) {
	if len(keyFields) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"index verify requires at least one key column")
	}

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}
	if len(cohort.Shards()) > 0 {
		return verifyArchiveIndex(fsys, cohort, path, keyFields)
	}

	indexPath := encx.SidecarIndexPath(path, keyFields)
	exists, err := afero.Exists(fsys, indexPath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("checking sidecar index existence: %s", indexPath))
	}
	if !exists {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MISSING,
			"no sidecar point-lookup index found for these key fields",
			map[string]any{"cohort": path, "fields": keyFields, "index_path": indexPath})
	}

	meta, err := encx.ReadIndexMetaFile(fsys, indexPath)
	if err != nil {
		return nil, err
	}

	// authoritative=true: verify's whole job is the conclusive answer,
	// so it neither accepts the stat pair as proof the way the Lookup
	// read path does nor serves its hash from the per-Service memo.
	// Sharing the classifier with Lookup is what keeps the two from
	// drifting on WHICH stat differences are treated as evidence.
	freshness, err := s.classifyIndexFreshness(fsys, path, meta, true)
	if err != nil {
		return nil, err
	}

	if freshness.SizeMismatch {
		return &VerifyIndexResult{
			IndexPath: indexPath,
			Fresh:     false,
			Reason:    IndexFreshnessReasonStatMismatch,
			FastPath:  true,
		}, nil
	}

	reason := IndexFreshnessReasonFingerprintMatch
	if !freshness.Fresh {
		reason = IndexFreshnessReasonFingerprintMismatch
	}
	return &VerifyIndexResult{
		IndexPath:    indexPath,
		Fresh:        freshness.Fresh,
		Reason:       reason,
		FastPath:     false,
		ModTimeDrift: freshness.ModTimeDrift,
	}, nil
}

// verifyArchiveIndex is VerifyIndex for a shard archive. Coverage first
// (no payload read): any uncovered shard is a stale verdict on the
// fast-path. Otherwise every covered shard's stored payload is hashed
// and compared with its sidecar's fingerprint — the authoritative check
// a lookup's identity comparison does not make. A covered shard whose
// sidecar file is gone is PULSE_INDEX_MANIFEST_STALE.
func verifyArchiveIndex(fsys afero.Fs, cohort *Cohort, path string, keyFields []string) (*VerifyIndexResult, error) {
	entry, err := archiveIndexEntry(fsys, path, keyFields)
	if err != nil {
		return nil, err
	}
	a, err := openArchiveFile(fsys, path)
	if err != nil {
		return nil, err
	}
	defer a.Close()

	indexPath := encx.SidecarIndexPath(path, keyFields)
	covered, uncovered := archiveCoverage(cohort.Shards(), archiveShardIdentities(a), entry)
	dir := filepath.Dir(path)
	metas := make([]*encx.IndexMeta, len(covered))
	for k, cov := range covered {
		sidecar := filepath.Join(dir, cov.IndexPath)
		exists, err := afero.Exists(fsys, sidecar)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				fmt.Sprintf("checking shard sidecar index existence: %s", sidecar))
		}
		if !exists {
			return nil, archiveShardSidecarMissing(path, keyFields, sidecar)
		}
		meta, err := encx.ReadIndexMetaFile(fsys, sidecar)
		if err != nil {
			return nil, err
		}
		if meta.SourceSize != uint64(cov.Size) || len(meta.Keys) != len(keyFields) {
			uncovered = append(uncovered, cov.Name)
		}
		metas[k] = meta
	}
	if len(uncovered) > 0 {
		return &VerifyIndexResult{IndexPath: indexPath, Fresh: false,
			Reason: IndexFreshnessReasonStatMismatch, FastPath: true, ShardsUncovered: uncovered}, nil
	}

	var mismatched []string
	for k, cov := range covered {
		sect, err := a.storedShard(cov.Name)
		if err != nil {
			return nil, err
		}
		fp, err := encx.ComputeFingerprint(sect)
		if err != nil {
			return nil, err
		}
		if fp != metas[k].Fingerprint {
			mismatched = append(mismatched, cov.Name)
		}
	}
	reason := IndexFreshnessReasonFingerprintMatch
	if len(mismatched) > 0 {
		reason = IndexFreshnessReasonFingerprintMismatch
	}
	return &VerifyIndexResult{IndexPath: indexPath, Fresh: len(mismatched) == 0,
		Reason: reason, ShardsMismatched: mismatched}, nil
}
