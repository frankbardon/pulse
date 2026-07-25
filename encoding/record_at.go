package encoding

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// RecordLocator captures the fixed geometry a single-file .pulse payload
// needs for O(1) index-addressed record reads: the byte offset where
// the record region begins (immediately after the header + schema
// prefix), the fixed per-record stride, and the total record count for
// bounds checking. Construct one per opened cohort via NewRecordLocator
// and reuse it across many point lookups — the offset math mirrors
// service/parallel_decode.go's per-worker segment slicing
// (recordRegionStart + i*stride), just applied to a single record
// instead of a worker's contiguous range.
type RecordLocator struct {
	// Schema is the parsed schema this locator's geometry is derived
	// from. Callers pass it straight through to ReadRecordWithWidePlan
	// / ReadRecordWithWideProjected via NewRecordReader.
	Schema *Schema

	// RecordRegionStart is the absolute byte offset of the first record,
	// i.e. the number of bytes the header + schema prefix consumed.
	RecordRegionStart int64

	// Stride is Schema.RecordByteSize() — the fixed on-wire byte width
	// of one record (including the trailing null bitmap, if any).
	Stride int64

	// TotalRecords is the number of complete records the payload holds,
	// derived from the payload's total byte length. Zero for an empty
	// cohort or a cohort shorter than one full record stride.
	TotalRecords uint64
}

// NewRecordLocator derives record-region geometry for a single-file
// .pulse payload from r, an io.ReadSeeker over the raw payload bytes —
// most commonly a *bytes.Reader wrapping a mmap'd byte slice, the same
// path the streaming iterator and the parallel decode context use. r's
// cursor must be at absolute position 0 (the very first header byte)
// when this is called; NewRecordLocator reads (and discards, beyond
// measuring their length) the header and schema, leaving r positioned
// at the first record byte as a side effect. Callers that need r
// re-wound for further use should re-seek to loc.RecordRegionStart or
// construct a fresh reader over the same bytes.
//
// schema is the already-parsed schema for this cohort (e.g. from
// pulse.Open) — NewRecordLocator does not re-validate its field
// contents against the freshly-read schema bytes; it reads the schema
// block purely to measure how many bytes the header+schema prefix
// consumes. A mismatched schema silently produces a locator with wrong
// geometry, so callers must pass the schema that was actually parsed
// from this same payload.
//
// A *bytes.Reader is required (rather than a generic io.ReadSeeker) so
// this function can read Size() — the reader's fixed, read-independent
// total length — to derive TotalRecords without a second file stat.
func NewRecordLocator(r *bytes.Reader, schema *Schema) (*RecordLocator, error) {
	if schema == nil {
		return nil, errors.NewCodedError(errors.ENCODING_INVALID,
			"record locator: nil schema")
	}
	if r == nil {
		return nil, errors.NewCodedError(errors.ENCODING_INVALID,
			"record locator: nil reader")
	}

	payloadSize := r.Size()

	if err := ReadHeader(r); err != nil {
		return nil, err
	}
	if _, err := ReadSchema(r); err != nil {
		return nil, err
	}

	recordRegionStart := payloadSize - int64(r.Len())
	stride := int64(schema.RecordByteSize())

	loc := &RecordLocator{
		Schema:            schema,
		RecordRegionStart: recordRegionStart,
		Stride:            stride,
	}
	if stride > 0 {
		available := payloadSize - recordRegionStart
		if available > 0 {
			loc.TotalRecords = uint64(available / stride)
		}
	}
	return loc, nil
}

// Offset returns the absolute byte offset of record i within the
// payload this locator was built from. It does not bounds-check —
// callers that need a guarded lookup should use ReadRecordAt, which
// validates i < TotalRecords before ever seeking.
func (loc *RecordLocator) Offset(i uint64) int64 {
	return loc.RecordRegionStart + int64(i)*loc.Stride
}

// ReadRecordAt decodes the record at index i directly: it seeks r to
// loc.Offset(i) and decodes from there, without iterating any
// preceding record. r must be an io.ReadSeeker positioned over the same
// underlying bytes the locator was built from (the same *bytes.Reader
// passed to NewRecordLocator, or a fresh reader over the same byte
// slice) — the seek is absolute, so r's current cursor position on
// entry is irrelevant.
//
// plan is optional: pass a *DecodePlan (from Schema.BuildDecodePlan) to
// decode only the columns the plan retains — the O(1) projection
// variant required by point-lookup callers that only need a handful of
// return columns out of a wide schema. Pass nil to decode every field
// via the existing full-record path. keep mirrors the FieldFilter
// contract used throughout this package (see ReadRecordWithWidePlan /
// ReadRecordWithWideProjected) — pass the same filter used to build
// plan's retained set so map writes for incidental group members (a
// bit-packed neighbour or a bitmap-adjacent nullable field) stay
// suppressed; pass nil to keep every field the plan/schema visits.
//
// Returns a coded ENCODING_INVALID error — never a panic, never an
// out-of-bounds read — when i >= loc.TotalRecords.
func (loc *RecordLocator) ReadRecordAt(
	r io.ReadSeeker,
	i uint64,
	values map[string]float64,
	nulls map[string]bool,
	wide map[string]any,
	keep FieldFilter,
	plan *DecodePlan,
) error {
	if r == nil {
		return errors.NewCodedError(errors.ENCODING_INVALID,
			"record locator: nil reader")
	}
	if i >= loc.TotalRecords {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"record index out of range",
			map[string]any{"index": i, "total_records": loc.TotalRecords})
	}

	if _, err := r.Seek(loc.Offset(i), io.SeekStart); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			"seeking to record offset")
	}

	rr := NewRecordReader(r, loc.Schema)
	if plan != nil {
		return rr.ReadRecordWithWidePlan(values, nulls, wide, keep, plan)
	}
	return rr.ReadRecordWithWideProjected(values, nulls, wide, keep)
}
