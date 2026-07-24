package processing

import (
	"encoding/binary"
	"math"

	"github.com/frankbardon/pulse/encoding"
)

// IsIndexKeyableFieldType reports whether ft may be used as a
// point-lookup index key column at the current exactness tier: the
// plain fixed-width scalar types (u8/u16/u32/u64, f32/f64) plus
// categorical_u8/u16/u32 (keyed on the on-wire dictionary ID — exactly
// what Record.NumericValue already stores for categorical fields, so
// no extra Dictionary.IDFor resolution is needed on the build path,
// which reads already-decoded records rather than string literals).
//
// FieldTypeDate and FieldTypeDecimal128 are deliberately excluded even
// though re-encoding Date from an already-decoded Record would in fact
// be exact today (its on-wire raw is a plain uint32, and
// Record.NumericValue already carries it losslessly) — the full
// keyable-type policy (whether Date should key today, exact
// decimal128 byte resolution instead of the lossy Float64(scale)
// echo, and set_* rejection) is explicitly E2-S3's scope; see that
// story before widening this predicate. The bit-packed types (u4,
// packed_bool — ByteSize()==0, shared bytes with neighbours) and the
// multi-select set_* family (bitmask-over-dictionary, ambiguous
// equality) are excluded because they don't fit this story's
// fixed-width byte-concatenation resolver at all, keyable or not.
func IsIndexKeyableFieldType(ft encoding.FieldType) bool {
	if ft.IsCategorical() {
		return true
	}
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64:
		return true
	}
	return false
}

// KeyFieldOnWireBytes resolves the exact on-wire byte representation
// of record's value for field, for use as a point-lookup index key —
// the raw bytes a byte-equal reader would see at that field's offset
// in the source .pulse file, not the lossy float64/string echo
// Record.AllValues() exposes. Mirrors the categorical-vs-numeric
// branch includeFilterer uses when resolving a filter literal
// (processing/filterer.go), but reads the value straight off an
// already-decoded Record instead of parsing a request-supplied string
// — the index build path walks decoded records, not filter literals.
//
// Returns (nil, false) when the record's value for field is null on
// this record — null key values are unindexable (skipped by the
// build path), not a resolution error — or when field.Type is not
// IsIndexKeyableFieldType.
//
// Callers MUST verify IsIndexKeyableFieldType(field.Type) themselves
// before scanning records (service.BuildIndex does this once per key
// column, up front, so a disallowed key type fails fast with a
// PROCESSING_CONFIG error instead of silently under-indexing after
// walking part of the cohort). This function stays a pure per-record
// resolver with no error return so it composes cleanly into a hot
// per-row loop.
func KeyFieldOnWireBytes(record *Record, field *encoding.Field) ([]byte, bool) {
	if field == nil || !IsIndexKeyableFieldType(field.Type) {
		return nil, false
	}
	if record.IsNull(field.Name) {
		return nil, false
	}
	v, ok := record.NumericValue(field.Name)
	if !ok {
		return nil, false
	}

	switch field.Type {
	case encoding.FieldTypeF32:
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, math.Float32bits(float32(v)))
		return buf, true
	case encoding.FieldTypeF64:
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
		return buf, true
	default:
		// Plain unsigned-integer on-wire encodings: u8/u16/u32/u64 and
		// categorical_u8/u16/u32 (the dictionary ID IS the on-wire
		// value already carried by NumericValue).
		return encodeUintOnWire(uint64(v), field.Type.ByteSize()), true
	}
}

// encodeUintOnWire re-encodes v as a little-endian byte slice of the
// given width, matching the on-wire integer encoding ReadFieldValue /
// WriteFieldValue use for the fixed-width unsigned integer field
// types (encoding/reader.go, encoding/writer.go).
func encodeUintOnWire(v uint64, width int) []byte {
	buf := make([]byte, width)
	switch width {
	case 1:
		buf[0] = byte(v)
	case 2:
		binary.LittleEndian.PutUint16(buf, uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(buf, uint32(v))
	case 8:
		binary.LittleEndian.PutUint64(buf, v)
	}
	return buf
}
