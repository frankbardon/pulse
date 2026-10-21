package service

import (
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// shardIter walks every shard inside a Pulse shard archive in
// central-directory (insertion) order, feeding each shard's records
// through the same processing pipeline as if they came from a single
// concatenated cohort. Online aggregators accumulate across shards;
// two-pass attributes use union mean / variance because the iterator
// surfaces "all rows" as a single stream.
//
// It implements processing.RecordIterator and ReusableIterator so the
// streaming and two-pass orchestrators can drive it interchangeably
// with the single-file streamingIterator.
//
// Each shard inside the archive is itself a complete single-file
// `.pulse` (header + schema + records). The canonical schema is the
// source of truth (carried on Cohort.Schema); per-shard schemas are
// skipped past on each shard's prelude so byte cursors stay aligned.
type shardIter struct {
	fs     afero.Fs
	path   string
	schema *encoding.Schema
	shards []ShardEntry

	// archive is the archive opened for random access (one handle, the
	// central directory parsed once per scan). Each shard reader is a
	// buffered SectionReader at the shard's data offset; rewinding
	// (Reset) re-carves sections rather than re-opening the archive.
	archive *archiveFile

	// per-shard reader state.
	shardIdx int
	reader   *encx.RecordReader
	// exact carries the exact u64 words of the projection-without-plan
	// map decode (nil when the schema has no u64 field); shared by every
	// shard's reader, all built over it.schema.
	exact *encx.ExactU64Words
	// shardRec is the in-shard record index of the next row Next hands
	// out; with shardIdx and starts[shardIdx] it is the row's
	// merge-block position (the key a processing.BlockMerger folds the
	// row under). openShard zeroes it.
	shardRec int
	// starts is each shard's first global record ordinal
	// (shardStarts), so a row keys by the ordinal its single-file twin
	// gives it.
	starts []int

	current *processing.Record
	done    bool
	err     error

	// reuse=true returns the SAME *processing.Record across iterations
	// (Reused fast path on the encoding reader). Single-file
	// streamingIterator honors the same toggle.
	reuse     bool
	reusedRec *processing.Record

	// binding mints the buffered path's fresh per-row Records (see
	// streamingIterator.binding).
	binding *processing.RecordBinding
	// extras are the attribute labels the binding reserves slots for
	// (SetExtras).
	extras []string

	// project / projectSize: optional field-keep filter. Bytes for
	// excluded fields are still consumed from the reader so byte
	// offsets stay aligned; only map writes are skipped. nil means
	// full decode.
	project     encx.FieldFilter
	projectSize int

	// plan / planKey: precomputed DecodePlan over (it.schema, retained
	// set derived from it.project). Built once at SetProjection time
	// so per-record Next() walks the plan instead of every schema
	// field. Mirrors streamingIterator semantics. Stays nil when
	// project is nil.
	plan    *encx.DecodePlan
	planKey string
}

// newShardIter constructs a multi-shard iterator over the cohort's
// archive. The archive is opened once for random access (its central
// directory parsed, no payload read) and held for the iterator's
// lifetime — subsequent shard transitions and Reset() rewinds reuse the
// same handle; only one shard's read-ahead buffer is ever resident.
// Returns an iterator in the same "lazy init"
// state as newStreamingIterator: the archive is parsed on the first
// Next() call.
//
// The caller is expected to have validated that c.Shards() is
// non-empty; for single-file cohorts use newStreamingIterator instead.
func newShardIter(fs afero.Fs, path string, schema *encoding.Schema, shards []ShardEntry) *shardIter {
	out := make([]ShardEntry, len(shards))
	copy(out, shards)
	return &shardIter{
		fs:     fs,
		path:   path,
		schema: schema,
		shards: out,
		starts: shardStarts(out),
	}
}

// shardStarts returns each shard's first GLOBAL record ordinal: the
// record counts of the shards before it, in archive order — the
// ordinal its rows carry in the single file holding the same rows in
// the same order. The serial iterator and the per-shard reducer both
// stamp merge positions from it, so a processing.BlockMerger keys a
// row identically on either path and on the single-file twin.
func shardStarts(shards []ShardEntry) []int {
	out := make([]int, len(shards))
	next := 0
	for i, s := range shards {
		out[i] = next
		next += int(s.RecordCount)
	}
	return out
}

// initArchive opens the archive and parses the central directory
// once. Idempotent — Reset uses it to rewind without re-opening.
func (it *shardIter) initArchive() error {
	if it.archive != nil {
		return nil
	}
	a, err := openArchiveFile(it.fs, it.path)
	if err != nil {
		return err
	}
	it.archive = a
	return nil
}

// openShard positions a fresh RecordReader at the start of the
// shard at shards[idx]. Reads + skips that shard's own header and
// schema block. Returns io.EOF when idx is past the end so Next()
// can stop cleanly.
func (it *shardIter) openShard(idx int) error {
	if idx >= len(it.shards) {
		return io.EOF
	}
	name := it.shards[idx].Filename
	// A deflated entry is refused (storedShard), never decoded as raw
	// compressed bytes.
	r, err := it.archive.shardReader(name)
	if err != nil {
		return err
	}
	// Each shard is a complete single-file Pulse cohort. Skip its
	// header + schema so the RecordReader lands on the first record.
	// The schema is read layout-only: the canonical schema decodes the
	// rows, so the shard's own dictionaries and group entry tables are
	// read past, never allocated (TestShardScan_SkipsShardDictionaries).
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q header", name))
	}
	if _, err := encx.ReadSchemaLayout(r, pulseVersion); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q schema", name))
	}
	// Schema cohesion is validated at insert time; on the read
	// path the canonical schema is authoritative.
	it.reader = encx.NewRecordReader(r, it.schema)
	if it.exact == nil {
		it.exact = encx.NewExactU64Words(it.schema)
	}
	it.reader.CaptureExactU64(it.exact)
	it.shardRec = 0
	return nil
}

// Next advances to the next record. When a shard is exhausted it
// transparently rolls to the next shard. Returns false on global
// exhaustion or on the first error encountered. Every record carries
// its merge-block position: the shard's archive index, its first
// global ordinal and the in-shard record index — the same keys the
// per-shard parallel reducer stamps.
func (it *shardIter) Next() bool {
	if !it.next() {
		return false
	}
	it.current.SetMergePosition(it.shardIdx, it.starts[it.shardIdx], it.shardRec)
	it.shardRec++
	return true
}

func (it *shardIter) next() bool {
	if it.done {
		return false
	}
	if it.archive == nil {
		if err := it.initArchive(); err != nil {
			it.err = err
			it.done = true
			return false
		}
	}
	for {
		if it.reader == nil {
			err := it.openShard(it.shardIdx)
			if err == io.EOF {
				it.done = true
				return false
			}
			if err != nil {
				it.err = err
				it.done = true
				return false
			}
		}

		if it.reuse {
			if it.reusedRec == nil {
				it.reusedRec = processing.NewReusableRecord(it.schema)
			}
			// Plan-aware reused decode honors projection under reuse, the
			// same as streamingIterator: a nil plan is byte-identical to
			// ReadRecordReused, a non-nil plan seeks past unprojected
			// on-wire ranges instead of decoding every field.
			var err error
			if it.plan != nil {
				err = it.reader.ReadRecordReusedWithPlan(it.reusedRec, it.project, it.plan)
			} else {
				err = it.reader.ReadRecordReused(it.reusedRec)
			}
			if err == io.EOF {
				// Advance to the next shard and keep going.
				it.reader = nil
				it.shardIdx++
				continue
			}
			if err != nil {
				it.err = err
				it.done = true
				return false
			}
			it.current = it.reusedRec
			return true
		}

		// Buffered path: a FRESH positional Record per row, decoded by
		// schema position (see streamingIterator.Next). The shard's
		// reader is built over it.schema, the same schema as the record.
		var rec *processing.Record
		var err error
		switch {
		case it.plan != nil:
			if it.binding == nil {
				it.binding = recordBindingFor(it.schema, it.plan, it.project, it.extras...)
			}
			rec = it.binding.NewRecord()
			err = it.reader.ReadRecordReusedWithPlan(rec, it.project, it.plan)
		case it.project != nil:
			// Defensive: projection without a plan. Map decode,
			// converted on construction.
			values := make(map[string]float64, it.projectSize)
			nulls := make(map[string]bool)
			wide := make(map[string]any)
			err = it.reader.ReadRecordWithWideProjected(values, nulls, wide, it.project)
			if err == nil {
				rec = processing.NewRecordWithWide(it.schema, values, nulls, wide)
				it.exact.ApplyTo(rec)
			}
		default:
			if it.binding == nil {
				it.binding = recordBindingFor(it.schema, it.plan, it.project, it.extras...)
			}
			rec = it.binding.NewRecord()
			err = it.reader.ReadRecordReused(rec)
		}
		if err == io.EOF {
			it.reader = nil
			it.shardIdx++
			continue
		}
		if err != nil {
			it.err = err
			it.done = true
			return false
		}
		it.current = rec
		return true
	}
}

// Record returns the current record. Only valid after Next() returns true.
func (it *shardIter) Record() *processing.Record {
	return it.current
}

// Reset rewinds the iterator to the first record of the first shard.
// The archive handle + central directory cache survive Reset; only the
// per-shard reader state is discarded. Preserves the reuse flag and
// the reusable Record so two-pass orchestrators benefit from the same
// allocation savings on pass 2.
func (it *shardIter) Reset() {
	it.shardIdx = 0
	it.reader = nil
	it.current = nil
	it.done = false
	it.err = nil
}

// SetReuse toggles per-row Record reuse. MUST be called before the
// first Next(). Mirrors streamingIterator.SetReuse.
func (it *shardIter) SetReuse(reuse bool) {
	it.reuse = reuse
}

// SetProjection installs a field-keep filter. MUST be called before
// the first Next(). Mirrors streamingIterator.SetProjection — builds
// a DecodePlan once at install time and caches it for the per-record
// Next() path.
func (it *shardIter) SetProjection(keep encx.FieldFilter, size int) {
	it.project = keep
	it.projectSize = size
	it.binding = nil
	if keep == nil {
		it.plan = nil
		it.planKey = ""
		return
	}
	if it.schema == nil {
		return
	}
	retained := retainedFromFilter(it.schema, keep)
	key := joinRetainedKey(retained)
	if it.plan != nil && key == it.planKey {
		return
	}
	plan, err := encx.BuildDecodePlan(it.schema, retained)
	if err != nil {
		it.plan = nil
		it.planKey = ""
		return
	}
	it.plan = plan
	it.planKey = key
}

// SetExtras implements scanIterator (see streamingIterator.SetExtras).
func (it *shardIter) SetExtras(labels []string) {
	it.extras = labels
	it.binding = nil
}

// Err returns any error encountered during iteration.
func (it *shardIter) Err() error {
	return it.err
}

// Close releases the archive handle and the per-shard reader state.
func (it *shardIter) Close() error {
	err := it.archive.Close()
	it.archive = nil
	it.reader = nil
	it.current = nil
	it.reusedRec = nil
	return err
}
