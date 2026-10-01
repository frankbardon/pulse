package service

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
// Shard archive cohorts (detected via the cheap leading-magic-bytes
// dispatch Service.Open already performs) are rejected with
// PULSE_INDEX_UNSUPPORTED_SHARDED — sharded point-lookup is out of
// scope for v1. See Service.BuildIndex's doc comment for why.
func (s *Service) VerifyIndex(ctx context.Context, path string, keyFields []string) (*VerifyIndexResult, error) {
	if len(keyFields) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"index verify requires at least one key column")
	}

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(cohort.Shards()) > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_UNSUPPORTED_SHARDED,
			"point-lookup index verify does not support shard archive cohorts",
			map[string]any{"cohort": path})
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}

	indexPath := encoding.SidecarIndexPath(path, keyFields)
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

	meta, err := encoding.ReadIndexMetaFile(fsys, indexPath)
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
