package service

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/spf13/afero"
)

// BuildIndexResult is the outcome of a successful point-lookup sidecar
// index build: the derived sidecar path it was written to (via
// encoding.SidecarIndexPath) plus the in-memory encoding.Index that
// was serialized there, in case a caller wants to inspect it (or run
// assertions against it in a test) without a round-trip read.
type BuildIndexResult struct {
	IndexPath string
	Index     *encx.Index

	// ManifestPath is the keyless sidecar index manifest this build
	// upserted its entry into (encoding.IndexManifestPath). Additive —
	// it names the ONE path a caller can read to discover this index's
	// key tuple without knowing the tuple already, which the sidecar's
	// own hash-derived filename makes impossible.
	ManifestPath string

	// DistinctKeys / IndexedRecords summarise the built index: distinct
	// key values and rows indexed (null-key rows are skipped). For a
	// shard archive they are the per-shard sums — a key present in k
	// shards counts k times.
	DistinctKeys   int
	IndexedRecords int

	// ShardIndexPaths lists, for a shard archive, every per-shard
	// sidecar the build wrote, in archive order. Nil for a single-file
	// cohort. An archive index has no single file: IndexPath is then its
	// logical name (the path SidecarIndexPath derives for the archive,
	// carrying the key hash) and Index is nil.
	ShardIndexPaths []string
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
// A shard archive cohort (detected via the cheap leading-magic-bytes
// dispatch Service.Open already performs) is indexed ONE SIDECAR PER
// SHARD plus the archive's manifest entry listing the covered shards
// (buildArchiveIndex): row ids are in-shard ordinals, so each shard is
// its own contiguous record region, and the index format is unchanged.
//
// Key field types are gated by processing.IsIndexKeyableFieldType — see
// that predicate's doc comment for the full, settled keyable-type
// policy (E2-S3). Rejected types fail fast here, before any record is
// scanned: set_* with PROCESSING_CONFIG and
// processing.IndexKeyRejectionMessage's type-specific explanation, a
// vec_f32 / vec_f64 field with PULSE_VECTOR_FIELD_NOT_SCALAR
// (vectors.FieldNotScalar, slot "index key", that explanation under
// details.reason).
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

	keys := make([]encx.IndexKeySpec, 0, len(keyFields))
	fields := make([]*encoding.Field, 0, len(keyFields))
	for _, name := range keyFields {
		field := schema.Field(name)
		if field == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"index key field not found in schema",
				map[string]any{"field": name})
		}
		// A vector field: the vector-in-a-scalar-slot refusal, whose
		// fixups point at the vector operators.
		if ce := vectors.ScalarRefusal(schema, name, "index key", "", map[string]any{"reason": processing.IndexKeyRejectionMessage(field.Type)}); ce != nil {
			return nil, ce
		}
		if !processing.IsIndexKeyableFieldType(field.Type) {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				processing.IndexKeyRejectionMessage(field.Type),
				map[string]any{"field": name, "type": field.Type.String()})
		}
		keys = append(keys, encx.IndexKeySpec{Name: field.Name, Type: field.Type})
		fields = append(fields, field)
	}

	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}

	if len(cohort.Shards()) > 0 {
		return s.buildArchiveIndex(fsys, cohort, path, keyFields, keys, fields)
	}

	fp, err := computeCohortFingerprint(fsys, path)
	if err != nil {
		return nil, err
	}

	sourceSize, sourceModTime, err := statCohortFile(fsys, path)
	if err != nil {
		return nil, err
	}

	buckets, err := scanBuildBuckets(fsys, path, schema, fields)
	if err != nil {
		return nil, err
	}

	idx := &encx.Index{
		Fingerprint:   fp,
		Keys:          keys,
		Buckets:       buckets,
		SourceSize:    sourceSize,
		SourceModTime: sourceModTime,
	}

	indexPath := encx.SidecarIndexPath(path, keyFields)
	if err := encx.WriteIndexFile(fsys, indexPath, idx); err != nil {
		return nil, err
	}

	// Publish the key tuple where it can be FOUND. The sidecar records
	// its own Keys, but its filename is a hash OF those keys, so the
	// file cannot be opened without already knowing them — and object
	// storage cannot list a directory, so there is no glob-and-read
	// escape where cohorts are actually served from. The manifest is
	// the keyless, one-GET answer. See encoding.IndexManifestSuffix.
	manifestPath, err := s.recordIndexInManifest(fsys, path, indexPath, idx)
	if err != nil {
		return nil, err
	}

	distinct, records := indexBucketCounts(buckets)
	return &BuildIndexResult{IndexPath: indexPath, Index: idx, ManifestPath: manifestPath,
		DistinctKeys: distinct, IndexedRecords: records}, nil
}

// computeCohortFingerprint reads the whole cohort file at path back
// off fsys and hashes it via encoding.ComputeFingerprint. A second
// full read of a cohort we just opened (Open only reads header +
// schema) — acceptable because a build already pays O(record count)
// to scan every row; hashing the raw bytes is a comparable single
// linear pass, not an added order-of-magnitude cost.
func computeCohortFingerprint(fsys afero.Fs, path string) (encx.Fingerprint, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return encx.Fingerprint{}, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file for fingerprint: %s", path))
	}
	defer f.Close()

	fp, err := encx.ComputeFingerprint(f)
	if err != nil {
		return encx.Fingerprint{}, err
	}
	return fp, nil
}

// statCohortFile stats path on fsys via afero (never raw os) and
// returns the size + modification time (Unix nanoseconds) snapshot
// embedded in the sidecar index — the source-stat pair
// Service.VerifyIndex compares against on a later freshness check
// before paying for a full content hash.
func statCohortFile(fsys afero.Fs, path string) (size uint64, modTimeUnixNano int64, err error) {
	info, err := fsys.Stat(path)
	if err != nil {
		return 0, 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("stat cohort file for index build: %s", path))
	}
	return uint64(info.Size()), info.ModTime().UnixNano(), nil
}

// scanBuildBuckets performs the single full sequential scan the build
// contract requires: every record is visited exactly once (no early
// stop, unlike Sample's offset+limit loop), in row-id order, and the
// on-wire composite key bytes for keyFields are resolved once per row
// via processing.CompositeKeyFieldOnWireBytes — the ordered-tuple
// concatenation of every key column's on-wire bytes. keyFields with a
// single element degenerates to the E1 single-key path exactly.
//
// Layout (first-seen key order, distinct-key bucket count) is
// indexBucketBuilder's.
func scanBuildBuckets(fsys afero.Fs, path string, schema *encoding.Schema, keyFields []*encoding.Field) ([]encx.IndexBucket, error) {
	iter := newStreamingIterator(fsys, path, schema)
	defer iter.Close()

	var b indexBucketBuilder
	var rowID uint64
	for iter.Next() {
		if keyBytes, ok := processing.CompositeKeyFieldOnWireBytes(iter.Record(), keyFields); ok {
			b.add(keyBytes, rowID)
		}
		rowID++
	}
	if err := iter.Err(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("scanning cohort for index build: %s", path))
	}
	return b.buckets(), nil
}

// indexBucketBuilder accumulates one index's key -> row-id multimap in
// first-seen key order and lays it out into hash buckets. It is the one
// layout rule for a single-file build and every shard of an archive
// build, which is what makes an archive's per-shard sidecar
// byte-identical to the one an anchored build of that shard writes.
//
// Entries are held in a map keyed by the string form of the raw key
// bytes (fast "have we seen this key" lookup) alongside an
// insertion-order slice — the slice is what makes bucket placement
// deterministic and therefore rebuild-idempotent: Go map iteration
// order is NOT deterministic, so the bucket layout is built by walking
// the slice, never by ranging the map.
//
// Bucket count is sized to the number of DISTINCT keys observed (not
// the row count) — a multimap build where most rows share a handful of
// keys would otherwise massively over-allocate empty buckets. Load
// factor ~1 keeps the average bucket at 0-1 entries, matching
// IndexBucket's doc comment.
type indexBucketBuilder struct {
	byKey map[string]*indexAccumEntry
	order []string
}

type indexAccumEntry struct {
	key    []byte
	rowIDs []uint64
}

// add records rowID under key.
func (b *indexBucketBuilder) add(key []byte, rowID uint64) {
	if b.byKey == nil {
		b.byKey = make(map[string]*indexAccumEntry)
	}
	k := string(key)
	e, seen := b.byKey[k]
	if !seen {
		e = &indexAccumEntry{key: key}
		b.byKey[k] = e
		b.order = append(b.order, k)
	}
	e.rowIDs = append(e.rowIDs, rowID)
}

// buckets lays the accumulated entries out into hash buckets (nil for
// no keys) and empties the builder for reuse.
func (b *indexBucketBuilder) buckets() []encx.IndexBucket {
	bucketCount := uint32(len(b.order))
	if bucketCount == 0 {
		return nil
	}
	out := make([]encx.IndexBucket, bucketCount)
	for _, k := range b.order {
		e := b.byKey[k]
		bi := encx.BucketIndex(e.key, bucketCount)
		out[bi].Entries = append(out[bi].Entries, encx.IndexEntry{
			Key:    e.key,
			RowIDs: e.rowIDs,
		})
	}
	b.byKey, b.order = nil, nil
	return out
}

// indexBucketCounts returns the distinct-key and indexed-record counts
// of a bucket table.
func indexBucketCounts(buckets []encx.IndexBucket) (distinct, records int) {
	for _, bk := range buckets {
		distinct += len(bk.Entries)
		for _, e := range bk.Entries {
			records += len(e.RowIDs)
		}
	}
	return distinct, records
}
