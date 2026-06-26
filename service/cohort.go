package service

import (
	"bytes"
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// ShardEntry is one shard inside a Pulse shard archive. Populated on a
// Cohort opened against a `.pulse` artifact whose first four bytes
// match the zip magic; empty for single-file cohorts.
type ShardEntry struct {
	// Filename is the basename of the shard inside the archive
	// (e.g. "20190101.pulse"). Equals the zip entry name.
	Filename string

	// RecordCount is the number of records carried by the shard.
	// Populated either from the `_schema.pulse` aggregate metadata or
	// from a first-read peek of the shard's own header.
	RecordCount int64
}

// Cohort represents an opened .pulse file with its parsed schema. For
// shard archives Shards is populated in central-directory order (which
// equals shard insertion order). For single-file cohorts Shards is
// empty.
type Cohort struct {
	path   string
	schema *encoding.Schema
	fs     afero.Fs

	// shards is non-nil and non-empty when the cohort was opened
	// against a Pulse shard archive (first four bytes = PK\x03\x04).
	// Sorted by central-directory order.
	shards []ShardEntry
}

// Shards returns the shard manifest for an archive-backed cohort. Returns
// an empty slice for single-file cohorts. The returned slice is a
// defensive copy; callers may mutate freely.
func (c *Cohort) Shards() []ShardEntry {
	out := make([]ShardEntry, len(c.shards))
	copy(out, c.shards)
	return out
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

func recordByteSize(schema *encoding.Schema) int {
	size := 0
	for _, f := range schema.Fields {
		size += f.Type.ByteSize()
	}
	return size
}
