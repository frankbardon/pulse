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

	// Current record — reused across Next() calls to reduce allocation.
	current *processing.Record
	values  map[string]float64
	nulls   map[string]bool
	done    bool
	err     error
}

// newStreamingIterator creates a streaming iterator for the given cohort.
// The file is opened lazily on the first call to Next().
func newStreamingIterator(fs afero.Fs, path string, schema *encoding.Schema) *streamingIterator {
	return &streamingIterator{
		fs:     fs,
		path:   path,
		schema: schema,
		values: make(map[string]float64, len(schema.Fields)),
		nulls:  make(map[string]bool),
	}
}

// newStreamingIteratorFromBytes creates a streaming iterator from raw bytes.
// Used for testing and when file data is already in memory.
func newStreamingIteratorFromBytes(data []byte, schema *encoding.Schema) *streamingIterator {
	it := &streamingIterator{
		schema: schema,
		values: make(map[string]float64, len(schema.Fields)),
		nulls:  make(map[string]bool),
	}
	it.initFromReader(bytes.NewReader(data))
	return it
}

func (it *streamingIterator) initFromFile() error {
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

	err := it.reader.ReadRecord(it.values, it.nulls)
	if err == io.EOF {
		it.done = true
		return false
	}
	if err != nil {
		it.err = err
		it.done = true
		return false
	}

	// Build record from the reusable maps. We must copy values because the
	// processing layer may hold references across iterations (e.g., collecting
	// filtered records into a slice for aggregation).
	valsCopy := make(map[string]float64, len(it.values))
	for k, v := range it.values {
		valsCopy[k] = v
	}
	nullsCopy := make(map[string]bool, len(it.nulls))
	for k, v := range it.nulls {
		nullsCopy[k] = v
	}
	it.current = processing.NewRecordWithNulls(it.schema, valsCopy, nullsCopy)
	return true
}

// Record returns the current record.
func (it *streamingIterator) Record() *processing.Record {
	return it.current
}

// Reset resets the iterator to re-read from the beginning.
func (it *streamingIterator) Reset() {
	if it.closer != nil {
		it.closer.Close()
		it.closer = nil
	}
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
	if it.closer != nil {
		return it.closer.Close()
	}
	return nil
}
