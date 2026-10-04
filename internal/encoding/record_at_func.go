package encoding

import (
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// ReadRecordAt decodes the record at index i directly: it seeks r to
// loc.Offset(i) and decodes from there, without iterating any
// preceding record. r must be an io.ReadSeeker positioned over the same
// underlying bytes the locator was built from (the same *bytes.Reader
// passed to NewRecordLocator, or a fresh reader over the same byte
// slice) — the seek is absolute, so r's current cursor position on
// entry is irrelevant.
//
// plan is optional: pass a *DecodePlan (from BuildDecodePlan(schema, retained)) to
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
func ReadRecordAt(loc *encoding.RecordLocator,
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

// ReadRecordAtRaw is ReadRecordAt's full-record exact variant: it
// decodes record i into values / nulls / wide exactly as ReadRecordAt
// with a nil plan and a nil keep does, and ALSO writes the exact
// on-wire word of every field read through ReadFieldValue into raw
// (integers u8..u64, f32/f64 bit patterns, date, datetime, categorical
// dictionary IDs and the narrow set masks). Bit-packed fields (u4,
// packed_bool) are exact in values; decimal128 and the wide set rungs
// are exact in wide. A null field is absent from raw and wide.
//
// It is the decode path for an index-addressed reader that must hand
// back the stored value rather than the float64 echo (u64 above 2^53,
// datetime beyond 2^53 seconds). A grouped (0x02) locator decodes the
// logical record, identical to the ungrouped twin.
func ReadRecordAtRaw(loc *encoding.RecordLocator,
	r io.ReadSeeker,
	i uint64,
	values map[string]float64,
	nulls map[string]bool,
	wide map[string]any,
	raw map[string]uint64,
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
	rr.raw = raw
	return rr.ReadRecordWithWide(values, nulls, wide)
}
