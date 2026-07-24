package service

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// BuildIndexResult is the outcome of a successful point-lookup sidecar
// index build: the derived sidecar path it was written to (via
// encoding.SidecarIndexPath) plus the in-memory encoding.Index that
// was serialized there, in case a caller wants to inspect it (or run
// assertions against it in a test) without a round-trip read.
type BuildIndexResult struct {
	IndexPath string
	Index     *encoding.Index
}

// BuildIndex scans the single-file cohort at path exactly once,
// resolves each record's ordered-tuple composite key across every
// column named in keyFields to its exact on-wire bytes
// (processing.CompositeKeyFieldOnWireBytes — the ordered concatenation
// of each key column's on-wire bytes; a single-element keyFields is the
// degenerate 1-tuple case and byte-identical to the E1 single-key
// path), groups row-ids by key into hash buckets (encoding.BucketIndex
// / encoding.HashKey — the same hash the lookup probe uses), computes
// the source .pulse file's content-hash fingerprint
// (encoding.ComputeFingerprint), and writes the sidecar via
// encoding.WriteIndexFile at the path encoding.SidecarIndexPath
// derives. keyFields may name any number of columns >= 1; key column
// ORDER is significant — building on ["a","b"] and ["b","a"] produces
// two distinct sidecar files at two distinct derived paths, with
// distinct composite key byte layouts.
//
// Row-ids are the record's 0-based sequential position in the cohort
// (row 0 is the first record after the header + schema), matching
// encoding.RecordLocator's Offset(i) addressing — lookup feeds a
// bucket's row-id straight into RecordLocator without any translation.
//
// Rows where ANY key column's value is null are skipped entirely — not
// indexed, not a build error (processing.CompositeKeyFieldOnWireBytes
// returns ok=false when any component is null; a composite key with a
// null component is unindexable, so the whole row is skipped rather
// than partially indexed).
//
// Scope gaps intentionally left for later stories (see story notes):
//   - Shard archive cohorts are rejected with SERVICE_VALIDATION (an
//     existing, already-registered code) rather than a dedicated
//     shard-rejection code — that dedicated code is a later story's
//     job. Row-id addressing (RecordLocator.Offset) only has meaning
//     for a single contiguous record region, which a multi-shard
//     archive does not present.
//   - Key field types beyond processing.IsIndexKeyableFieldType (most
//     notably date and decimal128 — see that predicate's doc comment
//     for why they're excluded even though Date could key exactly
//     today) are rejected with PROCESSING_CONFIG; the full
//     keyable-type policy is E2-S3's job.
func (s *Service) BuildIndex(ctx context.Context, path string, keyFields []string) (*BuildIndexResult, error) {
	if len(keyFields) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"index build requires at least one key column")
	}

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	schema := cohort.Schema()

	if len(cohort.Shards()) > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"point-lookup index build does not support shard archive cohorts yet",
			map[string]any{"cohort": path})
	}

	keys := make([]encoding.IndexKeySpec, 0, len(keyFields))
	fields := make([]*encoding.Field, 0, len(keyFields))
	for _, name := range keyFields {
		field := schema.Field(name)
		if field == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"index key field not found in schema",
				map[string]any{"field": name})
		}
		if !processing.IsIndexKeyableFieldType(field.Type) {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"field type is not supported as a point-lookup index key",
				map[string]any{"field": name, "type": field.Type.String()})
		}
		keys = append(keys, encoding.IndexKeySpec{Name: field.Name, Type: field.Type})
		fields = append(fields, field)
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}

	fp, err := computeCohortFingerprint(fsys, path)
	if err != nil {
		return nil, err
	}

	buckets, err := scanBuildBuckets(fsys, path, schema, fields)
	if err != nil {
		return nil, err
	}

	idx := &encoding.Index{
		Fingerprint: fp,
		Keys:        keys,
		Buckets:     buckets,
	}

	indexPath := encoding.SidecarIndexPath(path, keyFields)
	if err := encoding.WriteIndexFile(fsys, indexPath, idx); err != nil {
		return nil, err
	}

	return &BuildIndexResult{IndexPath: indexPath, Index: idx}, nil
}

// computeCohortFingerprint reads the whole cohort file at path back
// off fsys and hashes it via encoding.ComputeFingerprint. A second
// full read of a cohort we just opened (Open only reads header +
// schema) — acceptable because a build already pays O(record count)
// to scan every row; hashing the raw bytes is a comparable single
// linear pass, not an added order-of-magnitude cost.
func computeCohortFingerprint(fsys afero.Fs, path string) (encoding.Fingerprint, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return encoding.Fingerprint{}, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file for fingerprint: %s", path))
	}
	defer f.Close()

	fp, err := encoding.ComputeFingerprint(f)
	if err != nil {
		return encoding.Fingerprint{}, err
	}
	return fp, nil
}

// scanBuildBuckets performs the single full sequential scan the build
// contract requires: every record is visited exactly once (no early
// stop, unlike Sample's offset+limit loop), in row-id order, and the
// on-wire composite key bytes for keyFields are resolved once per row
// via processing.CompositeKeyFieldOnWireBytes — the ordered-tuple
// concatenation of every key column's on-wire bytes. keyFields with a
// single element degenerates to the E1 single-key path exactly.
//
// Entries are accumulated in an ordered map keyed by the string form
// of the raw key bytes (fast O(1) "have we seen this key already"
// lookup) alongside an insertion-order slice — the insertion-order
// slice is what makes bucket placement below deterministic and
// therefore rebuild-idempotent: Go map iteration order is NOT
// deterministic, so the final bucket layout is built by walking the
// ordered slice, never by ranging the map.
//
// Bucket count is sized to the number of DISTINCT keys observed
// (not the row count) — a multimap build where most rows share a
// handful of keys would otherwise massively over-allocate empty
// buckets. Load factor ~1 keeps the average bucket at 0-1 entries,
// matching IndexBucket's doc comment ("a well-distributed build
// populates each bucket with zero or one entries on average").
func scanBuildBuckets(fsys afero.Fs, path string, schema *encoding.Schema, keyFields []*encoding.Field) ([]encoding.IndexBucket, error) {
	iter := newStreamingIterator(fsys, path, schema)
	defer iter.Close()

	type accumEntry struct {
		key    []byte
		rowIDs []uint64
	}
	byKey := make(map[string]*accumEntry)
	order := make([]string, 0)

	var rowID uint64
	for iter.Next() {
		rec := iter.Record()
		keyBytes, ok := processing.CompositeKeyFieldOnWireBytes(rec, keyFields)
		if ok {
			k := string(keyBytes)
			e, seen := byKey[k]
			if !seen {
				e = &accumEntry{key: keyBytes}
				byKey[k] = e
				order = append(order, k)
			}
			e.rowIDs = append(e.rowIDs, rowID)
		}
		rowID++
	}
	if err := iter.Err(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("scanning cohort for index build: %s", path))
	}

	bucketCount := uint32(len(order))
	if bucketCount == 0 {
		return nil, nil
	}

	buckets := make([]encoding.IndexBucket, bucketCount)
	for _, k := range order {
		e := byKey[k]
		bi := encoding.BucketIndex(e.key, bucketCount)
		buckets[bi].Entries = append(buckets[bi].Entries, encoding.IndexEntry{
			Key:    e.key,
			RowIDs: e.rowIDs,
		})
	}
	return buckets, nil
}
