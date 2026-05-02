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

	// Allocate fresh maps directly into the next Record. Downstream consumers
	// (processing.Processor.Process) retain Records past the next ReadRecord
	// call, so each Record needs its own backing maps. Allocating once and
	// having ReadRecord populate them in-place is cheaper than allocating
	// reusable buffers and then range-copying out.
	values := make(map[string]float64, len(it.schema.Fields))
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	err := it.reader.ReadRecordWithWide(values, nulls, wide)
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
