package encoding

import (
	"io"
	"math"
)

// RecordReader reads records one at a time from a binary stream.
// It reads directly from the io.Reader without buffering the entire file.
type RecordReader struct {
	r      io.Reader
	schema *Schema

	// recBuf is a reusable per-record scratch buffer owned by the
	// RecordReader. ReadRecordReused reads the whole record stride into
	// this buffer with a single io.ReadFull, then decodes each field from
	// a running cursor subslice — eliminating the per-field io.ReadFull
	// that dominates wide-schema decode. Grown on demand, never shrunk.
	recBuf []byte
}

// NewRecordReader creates a RecordReader. The reader must be positioned
// immediately after the header and schema (i.e., at the first record byte).
func NewRecordReader(r io.Reader, schema *Schema) *RecordReader {
	return &RecordReader{r: r, schema: schema}
}

// ReadRecord reads a single record from the stream, populating the values and
// nulls maps. Returns io.EOF when no more records are available.
//
// The caller provides pre-allocated maps to avoid per-record allocation.
// Maps are cleared at the start of each call.
//
// Reuse contract: the maps are owned by the caller. ReadRecord does not retain
// references to them after returning. If the caller plans to reuse the same
// maps across calls (the typical pattern), they must consume the populated
// values BEFORE invoking ReadRecord again, because the next call clears and
// repopulates the maps in-place. If the caller needs to retain the values
// past the next call (e.g., collecting Records into a slice for later
// aggregation), it must pass distinct map instances per record OR copy the
// contents out before the next ReadRecord call.
//
// To populate typed wide values for fields whose representation does not
// fit in float64 (decimal128), call ReadRecordWithWide instead and pass a
// third map.
func (rr *RecordReader) ReadRecord(values map[string]float64, nulls map[string]bool) error {
	return rr.ReadRecordWithWide(values, nulls, nil)
}

// FieldFilter returns true for field names whose values should be
// written into the caller's maps. Used by ReadRecordWithWideProjected
// to skip map writes for fields the request doesn't read. A nil
// FieldFilter is equivalent to "keep every field."
type FieldFilter func(name string) bool

// ReadRecordWithWide reads a record and populates a wide map with typed
// values for decimal128 fields. The wide map may be nil to skip wide
// population.
func (rr *RecordReader) ReadRecordWithWide(values map[string]float64, nulls map[string]bool, wide map[string]any) error {
	return rr.readRecord(values, nulls, wide, nil)
}

// ReadRecordWithWideProjected reads a record but only writes the
// fields for which keep(name) returns true into the caller's maps.
// Bytes for excluded fields are still consumed from the underlying
// reader so byte offsets stay aligned — projection saves map
// allocations, not decode work.
//
// keep == nil falls back to the full-decode path.
func (rr *RecordReader) ReadRecordWithWideProjected(values map[string]float64, nulls map[string]bool, wide map[string]any, keep FieldFilter) error {
	return rr.readRecord(values, nulls, wide, keep)
}

func (rr *RecordReader) readRecord(values map[string]float64, nulls map[string]bool, wide map[string]any, keep FieldFilter) error {
	// Clear caller-provided maps.
	for k := range values {
		delete(values, k)
	}
	for k := range nulls {
		delete(nulls, k)
	}
	for k := range wide {
		delete(wide, k)
	}

	for _, field := range rr.schema.Fields {
		keepField := keep == nil || keep(field.Name)
		switch field.Type {
		case FieldTypePackedBool:
			v, err := ReadBit(rr.r, uint(field.BitPosition))
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			if v {
				values[field.Name] = 1
			} else {
				values[field.Name] = 0
			}

		case FieldTypeU4:
			v, err := ReadNibble(rr.r, field.BitPosition > 0)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = float64(v)

		case FieldTypeDecimal128:
			d, err := ReadDecimal128(rr.r)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = d.Float64(field.Scale)
			if wide != nil {
				wide[field.Name] = d
			}

		default:
			raw, err := ReadFieldValue(rr.r, field.Type)
			if err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = rawToFloat64(field.Type, raw)
			// Set types: expose the full uint64 mask via the wide map so
			// operators that need bit-level precision (everything beyond
			// the 2^53 float64 limit, plus set_u64 generally) can read
			// without losing high bits to float coercion. The float64
			// echo in values stays for any caller that only consults it.
			if field.Type.IsSet() && wide != nil {
				wide[field.Name] = raw
			}
		}
	}

	// Trailing null bitmap, if the schema declares any nullable field.
	if bmSize := rr.schema.BitmapByteSize(); bmSize > 0 {
		bitmap, err := ReadBitmap(rr.r, bmSize)
		if err != nil {
			if err == io.EOF || isEOF(err) {
				return io.EOF
			}
			return err
		}
		for i, field := range rr.schema.Fields {
			if !field.Nullable {
				continue
			}
			if !BitmapIsNull(bitmap, i) {
				continue
			}
			keepField := keep == nil || keep(field.Name)
			if !keepField {
				continue
			}
			nulls[field.Name] = true
			values[field.Name] = 0
			if wide != nil {
				delete(wide, field.Name)
			}
		}
	}

	return nil
}

// isEOF checks if an error wraps an EOF.
func isEOF(err error) bool {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return true
	}
	// Check wrapped errors.
	unwrapped := err
	for unwrapped != nil {
		if unwrapped == io.EOF || unwrapped == io.ErrUnexpectedEOF {
			return true
		}
		if u, ok := unwrapped.(interface{ Unwrap() error }); ok {
			unwrapped = u.Unwrap()
		} else {
			break
		}
	}
	return false
}

// rawToFloat64 converts raw uint64 bits to float64 based on field type.
func rawToFloat64(ft FieldType, raw uint64) float64 {
	switch ft {
	case FieldTypeF32:
		return float64(math.Float32frombits(uint32(raw)))
	case FieldTypeF64:
		return math.Float64frombits(raw)
	default:
		return float64(raw)
	}
}
