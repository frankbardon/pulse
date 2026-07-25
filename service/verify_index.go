package service

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// IndexFreshnessReason names why Service.VerifyIndex reached its Fresh
// verdict. All three values are reachable from VerifyIndex's fast-path
// / full-hash decision tree — see VerifyIndex's doc comment for the
// full contract.
type IndexFreshnessReason string

const (
	// IndexFreshnessReasonStatMismatch indicates the current cohort
	// file's size and/or modification time no longer matches the
	// sidecar's recorded Index.SourceSize / Index.SourceModTime. The
	// cohort has definitely changed since the index was built — this
	// verdict is reached WITHOUT computing the full content hash (the
	// size+mtime fast-path).
	IndexFreshnessReasonStatMismatch IndexFreshnessReason = "stat_mismatch"

	// IndexFreshnessReasonFingerprintMatch indicates size+mtime matched
	// the sidecar's recorded snapshot, and the full content hash
	// (recomputed as confirmation, never skipped on a match) is
	// byte-equal to the sidecar's embedded Fingerprint.
	IndexFreshnessReasonFingerprintMatch IndexFreshnessReason = "fingerprint_match"

	// IndexFreshnessReasonFingerprintMismatch indicates size+mtime
	// matched the sidecar's recorded snapshot, but the full content
	// hash recomputed for confirmation differs from the sidecar's
	// embedded Fingerprint — a same-size, same-mtime rewrite (a rare
	// but real edge case the fast-path alone cannot rule out).
	IndexFreshnessReasonFingerprintMismatch IndexFreshnessReason = "fingerprint_mismatch"
)

// VerifyIndexResult is the outcome of Service.VerifyIndex: whether the
// sidecar index at IndexPath is still fresh against its source cohort,
// why, and whether that verdict was reached via the size+mtime
// fast-path alone (FastPath=true) or required a full content-hash
// recompute (FastPath=false).
type VerifyIndexResult struct {
	IndexPath string
	Fresh     bool
	Reason    IndexFreshnessReason

	// FastPath is true exactly when Reason ==
	// IndexFreshnessReasonStatMismatch — the size+mtime comparison
	// alone was conclusive and no content hash was computed. A
	// fresh=true verdict is NEVER reached via the fast path alone: a
	// matching size+mtime pair is treated as inconclusive on its own
	// and always falls through to a full Fingerprint recompute before
	// VerifyIndex will report Fresh=true.
	FastPath bool
}

// VerifyIndex reports whether the sidecar point-lookup index built for
// keyFields against the cohort at path is still fresh, using a
// size+mtime fast-path before paying for a full content-hash recompute:
//
//  1. Load the sidecar's metadata only (encoding.ReadIndexMetaFile at
//     the path encoding.SidecarIndexPath derives — never the bucket
//     data, which freshness never needs). PULSE_INDEX_MISSING if absent.
//  2. Stat the CURRENT cohort file and compare its size + modification
//     time against the sidecar's recorded Index.SourceSize /
//     Index.SourceModTime (captured by Service.BuildIndex at build
//     time). If either differs, the cohort has definitely changed —
//     report Fresh=false / Reason=IndexFreshnessReasonStatMismatch /
//     FastPath=true WITHOUT computing the full content hash.
//  3. If size+mtime both match, that alone is not sufficient proof of
//     freshness (mtime resolution and pathological same-size rewrites
//     both admit false negatives), so fall through to a full
//     encoding.ComputeFingerprint recompute and compare against the
//     sidecar's embedded Fingerprint for a conclusive verdict
//     (IndexFreshnessReasonFingerprintMatch /
//     IndexFreshnessReasonFingerprintMismatch, both FastPath=false).
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

	currentSize, currentModTime, err := statCohortFile(fsys, path)
	if err != nil {
		return nil, err
	}

	if currentSize != meta.SourceSize || currentModTime != meta.SourceModTime {
		return &VerifyIndexResult{
			IndexPath: indexPath,
			Fresh:     false,
			Reason:    IndexFreshnessReasonStatMismatch,
			FastPath:  true,
		}, nil
	}

	fp, err := computeCohortFingerprint(fsys, path)
	if err != nil {
		return nil, err
	}

	if fp != meta.Fingerprint {
		return &VerifyIndexResult{
			IndexPath: indexPath,
			Fresh:     false,
			Reason:    IndexFreshnessReasonFingerprintMismatch,
			FastPath:  false,
		}, nil
	}

	return &VerifyIndexResult{
		IndexPath: indexPath,
		Fresh:     true,
		Reason:    IndexFreshnessReasonFingerprintMatch,
		FastPath:  false,
	}, nil
}
