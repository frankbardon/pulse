package service

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// streamingIterator reads records lazily from a .pulse file without
// loading the entire file into memory. It holds only the current record
// at any given time.
//
// Memory usage is O(schema_size) per record, not O(file_size).
type streamingIterator struct {
	fs     afero.Fs
	path   string
	schema *encoding.Schema

	// Underlying reader state.
	reader    *encoding.RecordReader
	closer    io.Closer // non-nil when reading from afero.File
	rawReader io.Reader // may be *bytes.Reader for Reset support

	// Current record. A fresh Record (with its own values/nulls maps) is
	// produced per Next() call because downstream consumers (notably
	// processing.Processor.Process) collect Records into a slice and require
	// each Record's maps to remain valid past the next iteration. See the
	// reuse contract on encoding.RecordReader.ReadRecord.
	current *processing.Record
	done    bool
	err     error

	// reuse=true makes Next return the SAME *processing.Record pointer
	// across iterations, refreshing its values/nulls/wide maps in place.
	// Callers MUST consume the record inline before the next Next() call.
	// Set by SetReuse before iteration begins. Default false preserves
	// the slice-collection contract used by the buffered Process path.
	reuse     bool
	reusedRec *processing.Record

	// mmapBytes is the read-only mmap'd region when the iterator
	// opened the file via memory-mapping. It is passed to
	// bytes.NewReader; the surrounding Read loop sees it as plain
	// memory (no extra indirection over a SectionReader).
	//
	// mmapCleanup is the unmap+close pair returned by mmapFileBytes.
	// Both nil when the iterator fell back to afero.ReadFile (MemMapFs,
	// custom fs without RealPather, or platforms where mmap is
	// unavailable).
	mmapBytes   []byte
	mmapCleanup func() error

	// project, when non-nil, scopes the per-record value/null/wide
	// maps to the names accepted by the filter. Bytes for excluded
	// fields are still consumed from the reader so the cursor stays
	// aligned; only the map writes are skipped. nil means full
	// decode (the default). projectSize is a hint for the per-record
	// map allocation when projection is active.
	project     encoding.FieldFilter
	projectSize int

	// plan is the precomputed DecodePlan for (it.schema, retained set
	// derived from it.project). Built once at SetProjection time and
	// reused on every Next() — the per-record hot path walks the plan
	// instead of every schema field. Stays nil when project is nil
	// (full-decode path) so SetProjection(nil, 0) is identical to
	// today's behaviour. planKey memoises the sorted retained set so
	// re-calling SetProjection with the same retained set reuses the
	// existing plan rather than rebuilding.
	plan    *encoding.DecodePlan
	planKey string
}

// newStreamingIterator creates a streaming iterator for the given cohort.
// The file is opened lazily on the first call to Next().
func newStreamingIterator(fs afero.Fs, path string, schema *encoding.Schema) *streamingIterator {
	return &streamingIterator{
		fs:     fs,
		path:   path,
		schema: schema,
	}
}

func (it *streamingIterator) initFromFile() error {
	// If a mmap region is already live (e.g., we're rewinding after a
	// Reset between two streaming passes), wrap it in a fresh
	// bytes.Reader rather than re-mapping the file.
	if it.mmapBytes != nil {
		it.initFromReader(bytes.NewReader(it.mmapBytes))
		return nil
	}
	// Fast path: when the underlying filesystem ultimately maps to a
	// regular on-disk file, mmap it read-only and serve reads directly
	// from the mapped region. Avoids the whole-file allocation + memcpy
	// that afero.ReadFile performs.
	//
	// resolveRealPath probes for the RealPather capability interface
	// (satisfied by *afero.BasePathFs out of the box and by any external
	// fs that opts in — e.g. a CoR overlay caching GCS objects to a
	// tempdir) and falls back to a direct *afero.OsFs check. The default
	// Pulse fs is BasePathFs(OsFs, PULSE_DATA_DIR), so the default
	// configuration takes this path.
	//
	// Capability-detected (not flagged) — failures fall back to
	// afero.ReadFile so callers using MemMapFs, custom Fs
	// implementations without RealPath, or platforms where the kernel
	// rejects mmap continue to work unchanged. SIGBUS on truncation is
	// a documented mmap hazard; Pulse's write-then-read pattern makes
	// it improbable in practice, but a misbehaving external writer
	// could still tear a file beneath us.
	if realPath, ok := resolveRealPath(it.fs, it.path); ok {
		if data, cleanup, err := mmapFileBytes(realPath); err == nil {
			it.mmapBytes = data
			it.mmapCleanup = cleanup
			it.initFromReader(bytes.NewReader(data))
			return nil
		}
		// Fall through to ReadFile on mmap failure (e.g. empty file,
		// EACCES on a special file, or errMmapUnsupported on non-unix
		// builds).
	}
	data, err := afero.ReadFile(it.fs, it.path)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "opening cohort file")
	}
	it.initFromReader(bytes.NewReader(data))
	return nil
}

func (it *streamingIterator) initFromReader(r io.Reader) {
	// Skip header.
	if err := encoding.ReadHeader(r); err != nil {
		it.err = err
		it.done = true
		return
	}
	// Skip schema (we already have it).
	if _, err := encoding.ReadSchema(r); err != nil {
		it.err = err
		it.done = true
		return
	}
	it.rawReader = r
	it.reader = encoding.NewRecordReader(r, it.schema)
}

// Next advances to the next record. Returns false when exhausted or on error.
func (it *streamingIterator) Next() bool {
	if it.done {
		return false
	}

	// Lazy init on first call.
	if it.reader == nil {
		if it.fs != nil {
			if err := it.initFromFile(); err != nil {
				it.err = err
				it.done = true
				return false
			}
		} else {
			it.done = true
			return false
		}
	}

	if it.reuse {
		if it.reusedRec == nil {
			it.reusedRec = processing.NewReusableRecord(it.schema)
		}
		// Plan-aware reused decode honors projection under reuse: when a
		// DecodePlan is installed (it.project + it.plan built at
		// SetProjection time), walk the plan so unprojected on-wire ranges
		// seek past instead of decoding. When no plan is installed
		// (full-decode reuse), ReadRecordReusedWithPlan(nil plan) is
		// byte-identical to ReadRecordReused.
		var err error
		if it.plan != nil {
			err = it.reader.ReadRecordReusedWithPlan(it.reusedRec, it.project, it.plan)
		} else {
			err = it.reader.ReadRecordReused(it.reusedRec)
		}
		if err == io.EOF {
			it.done = true
			return false
		}
		if err != nil {
			it.err = err
			it.done = true
			return false
		}
		it.current = it.reusedRec
		return true
	}

	// Allocate fresh maps directly into the next Record. Downstream consumers
	// (processing.Processor.Process) retain Records past the next ReadRecord
	// call, so each Record needs its own backing maps. Allocating once and
	// having ReadRecord populate them in-place is cheaper than allocating
	// reusable buffers and then range-copying out.
	mapHint := len(it.schema.Fields)
	if it.project != nil && it.projectSize > 0 {
		mapHint = it.projectSize
	}
	values := make(map[string]float64, mapHint)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	var err error
	switch {
	case it.plan != nil:
		// Plan-driven projected decode: SkipBytes segments seek past
		// unprojected on-wire ranges with a single io.Seeker call;
		// DecodeFields segments run the per-field decoder for the
		// retained group only. Plan was built once at SetProjection
		// time and reused across every Next() call.
		err = it.reader.ReadRecordWithWidePlan(values, nulls, wide, it.project, it.plan)
	case it.project != nil:
		// Projection requested but plan construction was skipped
		// (defensive — currently SetProjection always builds when
		// it.project is non-nil). Fall back to the per-field
		// projected decode.
		err = it.reader.ReadRecordWithWideProjected(values, nulls, wide, it.project)
	default:
		err = it.reader.ReadRecordWithWide(values, nulls, wide)
	}
	if err == io.EOF {
		it.done = true
		return false
	}
	if err != nil {
		it.err = err
		it.done = true
		return false
	}

	it.current = processing.NewRecordWithWide(it.schema, values, nulls, wide)
	return true
}

// SetProjection installs a field-keep filter on the iterator. When
// set, each Record's values/nulls/wide maps are populated only with
// the fields keep accepts; excluded field bytes are still consumed
// from the reader so byte offsets stay aligned. size is the expected
// number of accepted fields, used to size the per-record map
// allocation. Pass nil to clear (full decode).
//
// Builds a DecodePlan once for (it.schema, retained-set derived from
// keep) and caches it on the iterator. The per-record Next() path
// walks the plan instead of every schema field — unprojected byte
// ranges become a single SkipBytes / Seek call. The plan is keyed by
// (schema-pointer-identity, sorted-retained-set-string); same retained
// set returns the same plan reference.
//
// SetProjection(nil, 0) clears the cached plan and reverts to the
// full-decode path used today.
//
// MUST be called before the first Next() call.
func (it *streamingIterator) SetProjection(keep encoding.FieldFilter, size int) {
	it.project = keep
	it.projectSize = size
	if keep == nil {
		// Cleared projection ⇒ full-decode path. Drop any cached plan
		// so Next() takes the existing ReadRecordWithWide branch.
		it.plan = nil
		it.planKey = ""
		return
	}
	it.installPlan(keep)
}

// installPlan derives the sorted retained set from keep against
// it.schema, builds a DecodePlan if the retained set differs from any
// previously cached plan, and stores the result. A nil it.schema is a
// no-op (defensive — the iterator constructor always wires the
// schema), as is a BuildDecodePlan error (no plan today emits one,
// but the contract is reserved).
func (it *streamingIterator) installPlan(keep encoding.FieldFilter) {
	if it.schema == nil {
		return
	}
	retained := retainedFromFilter(it.schema, keep)
	key := joinRetainedKey(retained)
	if it.plan != nil && key == it.planKey {
		// Same retained set ⇒ reuse the existing plan. Cache key is
		// (schema-pointer-identity, sorted-retained-set-string); the
		// iterator only ever sees one schema, so the schema half is
		// implicit.
		return
	}
	plan, err := it.schema.BuildDecodePlan(retained)
	if err != nil {
		// BuildDecodePlan reserves the error slot for future shape
		// validation; today it always returns nil. On error, drop
		// back to the per-field projected path silently — the caller
		// already has it.project installed so projection still works,
		// just without the plan elision.
		it.plan = nil
		it.planKey = ""
		return
	}
	it.plan = plan
	it.planKey = key
}

// SetReuse toggles per-row Record reuse. When true, Next returns the
// SAME *processing.Record pointer across iterations with refreshed
// values/nulls/wide maps; callers MUST consume the record before the
// next Next() call. When false (default), each Next allocates a fresh
// Record, preserving the slice-collection contract used by the buffered
// Process path.
//
// MUST be called before the first Next() call. Calling it after
// iteration has begun has undefined effect on already-issued records.
func (it *streamingIterator) SetReuse(reuse bool) {
	it.reuse = reuse
}

// Record returns the current record.
func (it *streamingIterator) Record() *processing.Record {
	return it.current
}

// Reset resets the iterator to re-read from the beginning. Preserves
// the reuse flag and the reusable Record across passes so two-pass
// streaming paths benefit from the same allocation savings on pass 2.
//
// When the iterator backs onto an mmap'd file, Reset rewinds the
// SectionReader in place rather than re-mapping; the mmap region stays
// alive until Close.
func (it *streamingIterator) Reset() {
	if it.closer != nil {
		it.closer.Close()
		it.closer = nil
	}
	// NOTE: mmapHandle survives Reset; initFromFile reuses it to build
	// a fresh SectionReader without re-mapping the file. Only Close
	// releases the mapping.
	it.reader = nil
	it.rawReader = nil
	it.current = nil
	it.done = false
	it.err = nil
}

// Err returns any error encountered during iteration.
func (it *streamingIterator) Err() error {
	return it.err
}

// Close releases resources.
func (it *streamingIterator) Close() error {
	var firstErr error
	if it.closer != nil {
		firstErr = it.closer.Close()
		it.closer = nil
	}
	if it.mmapCleanup != nil {
		if err := it.mmapCleanup(); err != nil && firstErr == nil {
			firstErr = err
		}
		it.mmapCleanup = nil
		it.mmapBytes = nil
	}
	return firstErr
}
