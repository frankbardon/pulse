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
// fit in float64 (decimal128, point_f64, h3_cell), call ReadRecordWithWide
// instead and pass a third map.
func (rr *RecordReader) ReadRecord(values map[string]float64, nulls map[string]bool) error {
	return rr.ReadRecordWithWide(values, nulls, nil)
}

// ReadRecordWithWide reads a record and populates a wide map with typed
// values for decimal128, point_f64, and h3_cell fields. The wide map may
// be nil to skip wide population.
func (rr *RecordReader) ReadRecordWithWide(values map[string]float64, nulls map[string]bool, wide map[string]any) error {
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
		switch field.Type {
		case FieldTypePackedBool:
			v, err := ReadBit(rr.r, uint(field.BitPosition))
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if v {
				values[field.Name] = 1
			} else {
				values[field.Name] = 0
			}

		case FieldTypeNullableBool:
			v, err := ReadBit(rr.r, uint(field.BitPosition))
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			// For nullable bool, bit=0 can mean null or false depending on convention.
			// Treat as: 1=true, 0=false. Null tracking requires a separate null bitmap
			// which is not yet implemented; treat all as non-null for now.
			if v {
				values[field.Name] = 1
			} else {
				values[field.Name] = 0
			}

		case FieldTypeNullableU4:
			v, err := ReadNibble(rr.r, field.BitPosition > 0)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			values[field.Name] = float64(v)

		case FieldTypeDecimal128, FieldTypeNullableDecimal128:
			d, isNull, err := ReadDecimal128(rr.r)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if field.Type == FieldTypeNullableDecimal128 && isNull {
				nulls[field.Name] = true
				values[field.Name] = 0
				continue
			}
			values[field.Name] = d.Float64(field.Scale)
			if wide != nil {
				wide[field.Name] = d
			}

		case FieldTypePointF64:
			p, err := ReadPointF64(rr.r)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			// No useful float64 representation; record stores 0 to keep
			// the values map populated for callers that index by name.
			values[field.Name] = 0
			if wide != nil {
				wide[field.Name] = p
			}

		case FieldTypeH3Cell:
			c, err := ReadH3Cell(rr.r)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			values[field.Name] = float64(c)
			if wide != nil {
				wide[field.Name] = c
			}

		default:
			raw, err := ReadFieldValue(rr.r, field.Type)
			if err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					return io.EOF
				}
				return err
			}
			values[field.Name] = rawToFloat64(field.Type, raw)
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
