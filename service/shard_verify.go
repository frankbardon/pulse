package service

import (
	"bytes"
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// VerifyResult carries the structured outcome of VerifyShardArchive.
// Errors aggregates every fatal cohesion failure discovered while
// walking the archive's shards; Warnings carry non-fatal divergences
// (per-field description drift, aggregate-record-count drift). An
// empty Errors slice means the archive is structurally sound.
//
// The shape parallels descriptor.Envelope's {errors, warnings} so
// callers can lift entries straight into the --json envelope without
// reshaping. encoding.CohesionWarning is the canonical warning carrier
// (shared with insert-time cohesion validators).
type VerifyResult struct {
	Errors   []*errors.CodedError       `json:"errors"`
	Warnings []encoding.CohesionWarning `json:"warnings"`
}

// VerifyShardArchive walks the archive at archivePath and re-validates
// every shard against the canonical schema. For every real shard
// (excluding `_schema.pulse`) it:
//
//  1. Calls encoding.Archive.PeekShardHeader to check magic +
//     format_version, raising PULSE_SHARD_HEADER_INVALID on mismatch.
//  2. Parses the shard's full header + schema block.
//  3. Calls encoding.ValidateStructuralCohesion against the canonical
//     schema. Structural mismatches surface as
//     PULSE_SHARD_SCHEMA_MISMATCH errors; per-field description
//     divergence emits PULSE_SHARD_DESCRIPTION_DIVERGENCE warnings.
//  4. Calls encoding.ValidateDictPrefixRule. Any prefix-rule violation
//     surfaces as PULSE_SHARD_DICT_DIVERGENCE.
//  5. Re-peeks the shard's record count and sums across the archive.
//     A drift against the canonical AggregateRecordCount emits a
//     warning (the live per-shard sum is authoritative per the design
//     contract).
//
// Returns a non-nil error only when the archive itself cannot be opened
// (PULSE_ARCHIVE_MAGIC_INVALID, PULSE_ARCHIVE_CORRUPT, SERVICE_RESOURCE).
// Per-shard cohesion failures are accumulated into VerifyResult.Errors
// so the caller renders the full diagnosis — partial failures should
// not mask later shards' issues.
func (s *Service) VerifyShardArchive(ctx context.Context, archivePath string) (*VerifyResult, error) {
	fsys := s.fs.Fs()

	archiveBytes, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("VerifyShardArchive: reading archive %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, err
	}

	canonicalDoc, err := readSchemaDocEntry(arch)
	if err != nil {
		return nil, err
	}

	result := &VerifyResult{}
	var liveAggregate uint64

	for _, e := range arch.Entries() {
		if e.Name == encoding.ReservedSchemaName {
			continue
		}

		// (1) Magic + format_version. PeekShardHeader returns a CodedError
		// already wrapped with PULSE_SHARD_HEADER_INVALID; capture it
		// and continue so later shards still get verified.
		if perr := arch.PeekShardHeader(e.Name); perr != nil {
			result.Errors = append(result.Errors, coerceCodedError(perr))
			continue
		}

		// (2) Full header + schema parse via the entry payload. Reuse the
		// existing helper that reports PULSE_SHARD_HEADER_INVALID on
		// parse failure.
		payload, perr := readEntryBytes(arch, e.Name)
		if perr != nil {
			result.Errors = append(result.Errors, coerceCodedError(perr))
			continue
		}
		shardSchema, perr := readSinglePulseSchema(payload)
		if perr != nil {
			result.Errors = append(result.Errors, coerceCodedError(perr))
			continue
		}

		// (3) Structural cohesion. Per-field description divergence
		// surfaces as warnings; everything else is fatal for this shard.
		warnings, cerr := encoding.ValidateStructuralCohesion(canonicalDoc.Schema, shardSchema)
		for _, w := range warnings {
			result.Warnings = append(result.Warnings, decorateWarning(w, e.Name))
		}
		if cerr != nil {
			result.Errors = append(result.Errors, attachShardName(coerceCodedError(cerr), e.Name))
			continue
		}

		// (4) Dictionary prefix rule. Reverse-direction prefix
		// (canonical extends incoming) is fine at verify time — the
		// archive's canonical schema is authoritative — but neither-is-
		// prefix raises PULSE_SHARD_DICT_DIVERGENCE.
		if _, derr := encoding.ValidateDictPrefixRule(canonicalDoc.Schema, shardSchema); derr != nil {
			result.Errors = append(result.Errors, attachShardName(coerceCodedError(derr), e.Name))
			continue
		}

		// (5) Record-count cross-check. Use the archive's PeekShardRecordCount
		// directly (drains the entry stream) rather than recomputing
		// from the cached payload, mirroring the source-of-truth
		// behavior used elsewhere in the codebase.
		n, perr := arch.PeekShardRecordCount(e.Name)
		if perr != nil {
			result.Errors = append(result.Errors, attachShardName(coerceCodedError(perr), e.Name))
			continue
		}
		liveAggregate += uint64(n)
	}

	// Aggregate drift is a warning, never an error: the per-shard sum
	// is authoritative per the design contract; the cached aggregate is
	// a sanity check exposed for cheap Inspect calls. A drift means the
	// archive was edited outside Pulse (or interrupted between schema
	// rewrite and shard placement); Compact will refresh the cache.
	if len(result.Errors) == 0 && liveAggregate != canonicalDoc.AggregateRecordCount {
		result.Warnings = append(result.Warnings, encoding.CohesionWarning{
			Code: "PULSE_SHARD_AGGREGATE_DRIFT",
			Message: fmt.Sprintf(
				"canonical aggregate_record_count (%d) does not match the live per-shard sum (%d); run `pulse shard compact` to refresh the cached value",
				canonicalDoc.AggregateRecordCount, liveAggregate),
			Details: map[string]any{
				"canonical_aggregate": canonicalDoc.AggregateRecordCount,
				"live_aggregate":      liveAggregate,
			},
		})
	}

	return result, nil
}

// coerceCodedError unwraps err to a *errors.CodedError if possible, or
// wraps it as a generic SERVICE_RESOURCE-coded error otherwise. The
// verify path collects errors into a result struct rather than
// returning them directly, so a stable typed shape simplifies callers
// (CLI JSON envelope, MCP forwarders, programmatic checks).
func coerceCodedError(err error) *errors.CodedError {
	var ce *errors.CodedError
	if asCodedError(err, &ce) {
		return ce
	}
	return errors.NewCodedError(errors.SERVICE_RESOURCE, err.Error())
}

// asCodedError is a tiny errors.As-equivalent that avoids dragging the
// stdlib errors package into the import set just for the cast. The
// errors helpers above already depend on the typed CodedError so the
// pointer-pointer cast is sufficient for the unwrap chain.
func asCodedError(err error, target **errors.CodedError) bool {
	for cur := err; cur != nil; {
		if ce, ok := cur.(*errors.CodedError); ok {
			*target = ce
			return true
		}
		// Try Unwrap.
		type unwrapper interface{ Unwrap() error }
		u, ok := cur.(unwrapper)
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}

// attachShardName decorates ce with the offending shard's basename so
// downstream consumers (CLI rendering, MCP forwarders) can point at
// the offending entry without parsing the message.
func attachShardName(ce *errors.CodedError, name string) *errors.CodedError {
	if ce == nil {
		return ce
	}
	if ce.Details == nil {
		ce.Details = map[string]any{}
	}
	if _, present := ce.Details["entry"]; !present {
		ce.Details["entry"] = name
	}
	return ce
}

// decorateWarning attaches the shard's basename to a cohesion warning
// so multi-shard verify output points at the offender. The base
// warning is yielded unchanged when its Details already carries an
// entry field (the structural validator never sets one today, but the
// guard keeps the decorator idempotent for future validators).
func decorateWarning(w encoding.CohesionWarning, name string) encoding.CohesionWarning {
	if w.Details == nil {
		w.Details = map[string]any{}
	}
	if _, present := w.Details["entry"]; !present {
		w.Details["entry"] = name
	}
	return w
}
