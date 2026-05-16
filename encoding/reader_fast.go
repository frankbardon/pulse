package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/frankbardon/pulse/errors"
)

// ReusableRecord is the subset of *processing.Record needed by the reuse
// fast path. Declared here as an interface so encoding/ does not depend
// on processing/. Implementations (processing.Record) MUST clear their
// own null/wide maps before this call returns successfully; the reader
// populates them in place but only on fields where the value applies.
type ReusableRecord interface {
	SetNumeric(name string, value float64)
	SetNullField(name string)
	SetWideField(name string, value any)
	ClearForRow()
}

// ReadRecordReused reads one record into an existing ReusableRecord,
// reusing the record's internal maps. Returns io.EOF when the underlying
// reader is exhausted.
//
// Hot path semantics:
//   - Caller MUST consume the populated rec before the next call.
//   - Fixed-size numeric fields are decoded via a single stack-resident
//     [16]byte scratch + binary.LittleEndian, avoiding the per-field
//     allocation of binary.Read's internal buffer.
//   - Bit-packed and 16-byte fields fall back to the existing typed
//     readers.
func (rr *RecordReader) ReadRecordReused(rec ReusableRecord) error {
	rec.ClearForRow()
	var scratch [16]byte
	for _, field := range rr.schema.Fields {
		switch field.Type {
		case FieldTypePackedBool:
			v, err := ReadBit(rr.r, uint(field.BitPosition))
			if err != nil {
				return mapEOF(err)
			}
			if v {
				rec.SetNumeric(field.Name, 1)
			} else {
				rec.SetNumeric(field.Name, 0)
			}

		case FieldTypeNullableBool:
			v, err := ReadBit(rr.r, uint(field.BitPosition))
			if err != nil {
				return mapEOF(err)
			}
			if v {
				rec.SetNumeric(field.Name, 1)
			} else {
				rec.SetNumeric(field.Name, 0)
			}

		case FieldTypeNullableU4:
			v, err := ReadNibble(rr.r, field.BitPosition > 0)
			if err != nil {
				return mapEOF(err)
			}
			if v == nullableU4NullSentinel {
				rec.SetNullField(field.Name)
				rec.SetNumeric(field.Name, 0)
				continue
			}
			rec.SetNumeric(field.Name, float64(v))

		case FieldTypeDecimal128, FieldTypeNullableDecimal128:
			d, isNull, err := ReadDecimal128(rr.r)
			if err != nil {
				return mapEOF(err)
			}
			if field.Type == FieldTypeNullableDecimal128 && isNull {
				rec.SetNullField(field.Name)
				rec.SetNumeric(field.Name, 0)
				continue
			}
			rec.SetNumeric(field.Name, d.Float64(field.Scale))
			rec.SetWideField(field.Name, d)

		default:
			n := fixedWidthBytes(field.Type)
			if n == 0 {
				return errors.NewCodedError(errors.ENCODING_INVALID,
					fmt.Sprintf("unknown field type %d", field.Type))
			}
			if _, err := io.ReadFull(rr.r, scratch[:n]); err != nil {
				return mapEOF(err)
			}
			rec.SetNumeric(field.Name, decodeFixed(field.Type, scratch[:n]))
		}
	}
	return nil
}

// fixedWidthBytes returns the on-wire width of a fixed-size scalar
// field type. Returns 0 for variable / bit-packed / 16-byte / unknown
// types so callers can fall through to typed readers.
func fixedWidthBytes(ft FieldType) int {
	switch ft {
	case FieldTypeU8, FieldTypeNullableU8, FieldTypeCategoricalU8:
		return 1
	case FieldTypeU16, FieldTypeNullableU16, FieldTypeCategoricalU16:
		return 2
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32, FieldTypeF32:
		return 4
	case FieldTypeU64, FieldTypeF64:
		return 8
	default:
		return 0
	}
}

// decodeFixed turns a scratch slice of the right width into the float64
// representation used by Record.values. Mirrors rawToFloat64 in reader.go
// but operates on bytes directly so no intermediate uint64 allocation
// is required (the uint64 is stack-resident).
func decodeFixed(ft FieldType, buf []byte) float64 {
	switch ft {
	case FieldTypeU8, FieldTypeNullableU8, FieldTypeCategoricalU8:
		return float64(buf[0])
	case FieldTypeU16, FieldTypeNullableU16, FieldTypeCategoricalU16:
		return float64(binary.LittleEndian.Uint16(buf))
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32:
		return float64(binary.LittleEndian.Uint32(buf))
	case FieldTypeU64:
		return float64(binary.LittleEndian.Uint64(buf))
	case FieldTypeF32:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(buf)))
	case FieldTypeF64:
		return math.Float64frombits(binary.LittleEndian.Uint64(buf))
	}
	return 0
}

func mapEOF(err error) error {
	if err == nil {
		return nil
	}
	if err == io.EOF || isEOF(err) {
		return io.EOF
	}
	return err
}
