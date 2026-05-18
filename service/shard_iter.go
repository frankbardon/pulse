package service

import (
	"bytes"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
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

	// archiveData holds the zip bytes for the lifetime of the iterator.
	// Each shard reader is built from a SectionReader carved out of
	// these bytes; rewinding (Reset) re-seeks rather than re-reading
	// the archive from disk.
	archiveData []byte
	archive     *encoding.Archive

	// per-shard reader state.
	shardIdx int
	reader   *encoding.RecordReader

	current *processing.Record
	done    bool
	err     error

	// reuse=true returns the SAME *processing.Record across iterations
	// (Reused fast path on the encoding reader). Single-file
	// streamingIterator honors the same toggle.
	reuse     bool
	reusedRec *processing.Record

	// project / projectSize: optional field-keep filter. Bytes for
	// excluded fields are still consumed from the reader so byte
	// offsets stay aligned; only map writes are skipped. nil means
	// full decode.
	project     encoding.FieldFilter
	projectSize int
}

// newShardIter constructs a multi-shard iterator over the cohort's
// archive. The archive bytes are read once via afero and held for the
// iterator's lifetime — subsequent shard transitions and Reset() rewinds
// reuse the same buffer. Returns an iterator in the same "lazy init"
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
	}
}

// initArchive loads the archive bytes once and parses the central
// directory. Idempotent — Reset uses it to rewind without re-reading.
func (it *shardIter) initArchive() error {
	if it.archive != nil {
		return nil
	}
	data, err := afero.ReadFile(it.fs, it.path)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening shard archive: %s", it.path))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	it.archiveData = data
	it.archive = arch
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
	sect, err := it.archive.OpenAt(name)
	if err != nil {
		return err
	}
	// Each shard is a complete single-file Pulse cohort. Skip its
	// header + schema so the RecordReader lands on the first record.
	r := &sect
	if err := encoding.ReadHeader(r); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q header", name))
	}
	if _, err := encoding.ReadSchema(r); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q schema", name))
	}
	// Schema cohesion is validated at insert time (S2); on the read
	// path the canonical schema is authoritative.
	it.reader = encoding.NewRecordReader(r, it.schema)
	return nil
}

// Next advances to the next record. When a shard is exhausted it
// transparently rolls to the next shard. Returns false on global
// exhaustion or on the first error encountered.
func (it *shardIter) Next() bool {
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
			err := it.reader.ReadRecordReused(it.reusedRec)
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

		mapHint := len(it.schema.Fields)
		if it.project != nil && it.projectSize > 0 {
			mapHint = it.projectSize
		}
		values := make(map[string]float64, mapHint)
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		var err error
		if it.project != nil {
			err = it.reader.ReadRecordWithWideProjected(values, nulls, wide, it.project)
		} else {
			err = it.reader.ReadRecordWithWide(values, nulls, wide)
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
		it.current = processing.NewRecordWithWide(it.schema, values, nulls, wide)
		return true
	}
}

// Record returns the current record. Only valid after Next() returns true.
func (it *shardIter) Record() *processing.Record {
	return it.current
}

// Reset rewinds the iterator to the first record of the first shard.
// The archive bytes + central directory cache survive Reset; only the
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
// the first Next(). Mirrors streamingIterator.SetProjection.
func (it *shardIter) SetProjection(keep encoding.FieldFilter, size int) {
	it.project = keep
	it.projectSize = size
}

// Err returns any error encountered during iteration.
func (it *shardIter) Err() error {
	return it.err
}

// Close releases resources. The archive byte buffer is dropped so the
// GC can reclaim it. Returns nil — the iterator owns no file handles
// directly (the underlying SectionReader is GC-managed).
func (it *shardIter) Close() error {
	it.archive = nil
	it.archiveData = nil
	it.reader = nil
	it.current = nil
	it.reusedRec = nil
	return nil
}
