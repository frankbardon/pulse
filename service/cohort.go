package service

import (
	"bytes"
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// Cohort represents an opened .pulse file with its parsed schema.
type Cohort struct {
	path   string
	schema *encoding.Schema
	fs     afero.Fs
}

// Schema returns the cohort's schema.
func (c *Cohort) Schema() *encoding.Schema {
	return c.schema
}

// Records returns a streaming iterator over records in the cohort.
// Records are decoded lazily from disk — the full file is not materialized.
func (c *Cohort) Records(_ context.Context) (processing.RecordIterator, error) {
	return newStreamingIterator(c.fs, c.path, c.schema), nil
}

// RecordCount returns the number of records in the cohort file.
// It reads the file and counts records based on the schema's per-record byte size.
func (c *Cohort) RecordCount() (int64, error) {
	data, err := afero.ReadFile(c.fs, c.path)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "reading cohort file")
	}

	r := bytes.NewReader(data)

	// Skip header
	if err := encoding.ReadHeader(r); err != nil {
		return 0, err
	}

	// Skip schema
	if _, err := encoding.ReadSchema(r); err != nil {
		return 0, err
	}

	// Calculate record size from schema
	recordSize := recordByteSize(c.schema)
	if recordSize == 0 {
		return 0, nil
	}

	remaining := int64(r.Len())
	return remaining / int64(recordSize), nil
}

// recordByteSize computes the byte size of a single record for the schema.
func recordByteSize(schema *encoding.Schema) int {
	size := 0
	for _, f := range schema.Fields {
		size += f.Type.ByteSize()
	}
	return size
}
