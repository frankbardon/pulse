package io

import (
	"bufio"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// CohortRecords is a cohort opened for one sequential pass over its
// records: the read every whole-cohort export makes (ExportJob.Run and
// Predict, the `.sav` writer's cohort path). It dispatches on the
// file's first four bytes exactly as pulse.Open does, so a shard
// archive exports like the single-file cohort it is equivalent to.
//
//   - A single-file cohort: Schema is the file's own; Reader is the file
//     past its preamble, read to EOF (a truncated tail stays the caller's
//     to judge, as it always was).
//   - A shard archive: Schema is the archive's CANONICAL schema (widened
//     set rungs, the dictionaries every shard's own are a prefix of) and
//     Reader yields every shard's whole records back to back in archive
//     order (encx.OpenArchiveRecords) — byte-for-byte the record region of
//     the single file a flatten would write.
//
// Either way Reader holds PHYSICAL rows: a grouped schema is read
// through encx.NewLogicalStream by the caller, as before.
type CohortRecords struct {
	Schema *encoding.Schema
	Reader io.Reader
	// Records is the number of whole records Reader holds — the
	// payload-length estimate ExportJob.Predict has always reported.
	Records int64
	// Archive reports a shard-archive source.
	Archive bool
	file    afero.File
}

// Close releases the cohort's file handle. Safe on nil.
func (c *CohortRecords) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	err := c.file.Close()
	c.file = nil
	return err
}

// OpenCohortRecords opens path on fsys for a sequential record pass.
// An unopenable path is the plain "reading pulse file" error export has
// always returned; header, schema and archive faults keep their own
// codes (ENCODING_INVALID, PULSE_COHORT_COMPRESSED, the PULSE_ARCHIVE_*
// / PULSE_SHARD_* codes). The caller owns Close.
func OpenCohortRecords(fsys afero.Fs, path string) (*CohortRecords, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading pulse file: %w", err)
	}
	c, err := cohortRecordsFromFile(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return c, nil
}

func cohortRecordsFromFile(f afero.File) (*CohortRecords, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading pulse file: %w", err)
	}
	size := st.Size()
	isArch, err := encx.IsArchive(f, size)
	if err != nil {
		return nil, err
	}
	if isArch {
		ar, err := encx.OpenArchiveRecords(f, size)
		if err != nil {
			return nil, err
		}
		return &CohortRecords{
			Schema:  ar.Schema,
			Reader:  bufio.NewReaderSize(ar.Reader, exportReadBufferSize),
			Records: ar.Records,
			Archive: true,
			file:    f,
		}, nil
	}

	// IsArchive read through ReadAt, so the handle still sits at 0.
	cr := &recordsCounter{r: f}
	r := bufio.NewReaderSize(cr, exportReadBufferSize)
	version, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	schema, err := encoding.ReadSchema(r, version)
	if err != nil {
		return nil, err
	}
	var records int64
	if stride := int64(schema.RecordByteSize()); stride > 0 {
		records = (size - (cr.n - int64(r.Buffered()))) / stride
	}
	return &CohortRecords{Schema: schema, Reader: r, Records: records, file: f}, nil
}

// recordsCounter counts the bytes the preamble read pulled off the file,
// so the record region's offset survives the bufio read-ahead.
type recordsCounter struct {
	r io.Reader
	n int64
}

func (c *recordsCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
