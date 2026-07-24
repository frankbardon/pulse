package processing

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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

	return numericOnWireBytes(v, field.Type), true
}

// CompositeKeyFieldOnWireBytes resolves the composite on-wire key bytes
// for record across every field in fields, in order — the ordered-tuple
// concatenation of each field's exact on-wire byte representation
// (KeyFieldOnWireBytes), one after another. Key column ORDER is
// significant: fields [a, b] and [b, a] produce different byte strings
// (and therefore different hash buckets) even when both name the same
// two columns, because a composite key is a byte-concatenation, not a
// set. The single-key path (E1) is the degenerate 1-tuple case —
// calling this with a 1-element fields slice is byte-identical to
// calling KeyFieldOnWireBytes directly.
//
// Returns (nil, false) when fields is empty or when ANY field's value
// is null on this record — a composite key with a null component is
// unindexable, so the whole row is skipped rather than partially
// indexed (mirrors KeyFieldOnWireBytes's single-field null contract).
func CompositeKeyFieldOnWireBytes(record *Record, fields []*encoding.Field) ([]byte, bool) {
	if len(fields) == 0 {
		return nil, false
	}
	var buf []byte
	for _, f := range fields {
		b, ok := KeyFieldOnWireBytes(record, f)
		if !ok {
			return nil, false
		}
		buf = append(buf, b...)
	}
	return buf, true
}

// numericOnWireBytes re-encodes a float64-carried record value as the
// exact on-wire byte representation for the given field type. Shared by
// KeyFieldOnWireBytes (decoded-record → on-wire, the index build path)
// and ResolveLookupKeyBytes (request literal → on-wire, the lookup
// probe path) so the two directions can never diverge on encoding
// choice — a build-time key and a lookup-time key derived from the same
// logical value are always byte-equal.
func numericOnWireBytes(v float64, ft encoding.FieldType) []byte {
	switch ft {
	case encoding.FieldTypeF32:
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, math.Float32bits(float32(v)))
		return buf
	case encoding.FieldTypeF64:
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
		return buf
	default:
		// Plain unsigned-integer on-wire encodings: u8/u16/u32/u64 and
		// categorical_u8/u16/u32 (the dictionary ID IS the on-wire
		// value already carried by NumericValue).
		return encodeUintOnWire(uint64(v), ft.ByteSize())
	}
}

// ResolveLookupKeyBytes resolves a caller-supplied literal string value
// (LookupRequest.Value) to the exact on-wire byte representation used
// inside a sidecar index's IndexEntry.Key — the inverse of
// KeyFieldOnWireBytes, which resolves from an already-decoded Record
// rather than a request-supplied literal. Mirrors the categorical-vs-
// numeric branch includeFilterer.Build uses when resolving a filter
// literal (processing/filterer.go): categorical field types resolve
// through field.Dictionary.IDFor and re-encode the dictionary ID at the
// field's on-wire width; numeric field types parse the literal as a
// float64 and re-encode via numericOnWireBytes — the same helper
// KeyFieldOnWireBytes uses — so a build-time on-wire key and a
// lookup-time on-wire key derived from the same logical value are
// always byte-equal.
//
// Returns a PROCESSING_CONFIG coded error when field is nil, when
// field.Type fails IsIndexKeyableFieldType (this rejects date and
// decimal128 today — see that predicate's doc comment for why; exact
// date/decimal128 literal resolution is E2-S3's scope), when a
// categorical literal has no matching dictionary entry, or when a
// numeric literal fails to parse as a float.
func ResolveLookupKeyBytes(field *encoding.Field, literal string) ([]byte, error) {
	if field == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"lookup key field not found in schema")
	}
	if !IsIndexKeyableFieldType(field.Type) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"field type is not supported as a point-lookup index key",
			map[string]any{"field": field.Name, "type": field.Type.String()})
	}

	if field.Type.IsCategorical() {
		if field.Dictionary == nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("categorical lookup field %q has no dictionary", field.Name))
		}
		id, ok := field.Dictionary.IDFor(literal)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("lookup value %q not found in dictionary for field %q", literal, field.Name))
		}
		return encodeUintOnWire(uint64(id), field.Type.ByteSize()), nil
	}

	v, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			fmt.Sprintf("parsing lookup value %q for field %q", literal, field.Name))
	}
	return numericOnWireBytes(v, field.Type), nil
}

// ResolveCompositeLookupKeyBytes resolves an ordered set of caller-
// supplied literal values (literals[i] paired with fields[i]) to the
// on-wire composite key bytes — the ordered-tuple concatenation of each
// field's ResolveLookupKeyBytes result, in fields order. The inverse of
// CompositeKeyFieldOnWireBytes: a build-time composite key and a
// lookup-time composite key derived from the same ordered logical
// values are always byte-equal, because both route every scalar
// component through the same numericOnWireBytes / dictionary-ID
// encoding.
//
// Returns a PROCESSING_CONFIG coded error when len(fields) !=
// len(literals) (arity mismatch — callers validate this against the
// loaded sidecar index's key-spec before calling, so this is a
// defense-in-depth guard, not the primary arity check) or when any
// per-field ResolveLookupKeyBytes call fails.
func ResolveCompositeLookupKeyBytes(fields []*encoding.Field, literals []string) ([]byte, error) {
	if len(fields) != len(literals) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"lookup key component count does not match key field count",
			map[string]any{"fields": len(fields), "literals": len(literals)})
	}
	var buf []byte
	for i, f := range fields {
		b, err := ResolveLookupKeyBytes(f, literals[i])
		if err != nil {
			return nil, err
		}
		buf = append(buf, b...)
	}
	return buf, nil
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
