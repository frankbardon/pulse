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
// point-lookup index key column. This is the settled E2-S3 keyable-type
// policy — every field type resolves to a byte-concatenation key, so
// the predicate below is the full and final matrix (see the
// per-category rationale):
//
//   - ALLOW — fixed-width scalars: u4, u8, u16, u32, u64, f32, f64.
//     u4 is bit-packed on the wire (ByteSize()==0, shares a byte with a
//     neighbouring field) but its decoded range (0-15) still has a
//     well-defined, injective 1-byte key encoding — see
//     numericOnWireBytes. Floats key on their raw IEEE-754 bit pattern:
//     this is EXACT bit-pattern equality, which is byte-index-correct,
//     but callers should know -0.0 != 0.0's bits differ and NaN payloads
//     compare unequal to each other under this scheme — Pulse does not
//     normalize float keys (documented here and in the numeric key
//     encoding paths; normalization would make the build-time key and a
//     lookup-time key derived from a differently-bit-patterned-but-
//     numerically-equal literal diverge in the OTHER direction).
//   - ALLOW — date: on-wire epoch-days (uint32), exact — no precision
//     loss versus the persisted representation.
//   - ALLOW — datetime: on-wire epoch-seconds (uint64), the same
//     fixed-width unsigned-integer shape as u64 and admitted on the
//     same rationale. A lookup literal resolves through
//     encoding.ParseDateTime (never a ParseFloat round trip), so a
//     literal probed here and the same literal imported as a cell land
//     on identical key bytes. It inherits u64's one caveat verbatim:
//     Record.NumericValue carries the value as float64, so a magnitude
//     above 2^53 is not exactly representable. For datetime that range
//     is reached only by a PRE-1970 instant (stored as a negative
//     second count reinterpreted as uint64 two's-complement); the
//     rounding is applied identically on the build and the probe path
//     so equality lookups still resolve, but two pre-1970 instants
//     inside one rounding step share a key. Post-epoch instants —
//     every realistic cohort — are exact.
//   - ALLOW — decimal128: exact 128-bit mantissa bytes via
//     encoding.EncodeDecimal128, never the lossy Record.NumericValue
//     Float64(scale) echo. See KeyFieldOnWireBytes and
//     ResolveLookupKeyBytes for where the two directions branch to the
//     wide (Decimal128) path instead of the float64 numeric path.
//   - ALLOW — categorical_u8/u16/u32: keyed on the on-wire dictionary ID
//     (exactly what Record.NumericValue already stores for categorical
//     fields, so no extra Dictionary.IDFor resolution is needed on the
//     build path, which reads already-decoded records rather than
//     string literals).
//   - ALLOW — packed_bool: a 1-bit bool, decoded range {0,1}. Exact
//     under the same "well-defined injective 1-byte encoding" rationale
//     as u4.
//   - REJECT — set_u8/u16/u32/u64: a multi-select bitmask has no single
//     unambiguous equality value for a point key — the empty selection,
//     a single member, and every multi-member combination are all
//     legal, distinct bitmask states, and "does this row's set contain
//     X" is a membership predicate (FILTER_SET), not a point-lookup
//     equality key. Rejected with a set_*-specific message — see
//     IndexKeyRejectionMessage.
func IsIndexKeyableFieldType(ft encoding.FieldType) bool {
	if ft.IsCategorical() {
		return true
	}
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate, encoding.FieldTypeDateTime,
		encoding.FieldTypeDecimal128, encoding.FieldTypePackedBool:
		return true
	}
	return false
}

// IndexKeyRejectionMessage returns a field-type-specific explanation for
// why ft cannot be used as a point-lookup index key, for callers
// building a PROCESSING_CONFIG coded error around a
// !IsIndexKeyableFieldType(ft) check (processing.ResolveLookupKeyBytes
// and service.BuildIndex both surface this text). set_* gets a
// dedicated explanation of the ambiguous-equality rationale; every
// other (hypothetical future) rejected type falls back to a generic
// message.
func IndexKeyRejectionMessage(ft encoding.FieldType) string {
	if ft.IsSet() {
		return "set_* fields cannot be a point-lookup index key: a multi-select bitmask has no single unambiguous equality value (empty selection, single-member, and multi-member masks are all distinct legal states) — use a FILTER_SET membership predicate instead"
	}
	return "field type is not supported as a point-lookup index key"
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
// decimal128 is the one field type whose exact value does NOT live in
// Record.NumericValue — that accessor only carries the lossy
// Float64(scale) echo populated for AllValues()/analytics consumers.
// The exact 128-bit mantissa lives in Record.WideValue, populated by
// the reader alongside the lossy float echo (encoding/reader.go,
// encoding/reader_plan.go). KeyFieldOnWireBytes reads WideValue for
// decimal128 fields and re-encodes via encoding.EncodeDecimal128 — the
// same function the .pulse writer uses — so the resolved key bytes are
// never round-tripped through float64.
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

	if field.Type == encoding.FieldTypeDecimal128 {
		wv, ok := record.WideValue(field.Name)
		if !ok {
			return nil, false
		}
		d, ok := wv.(encoding.Decimal128)
		if !ok {
			return nil, false
		}
		enc := encoding.EncodeDecimal128(d)
		return append([]byte(nil), enc[:]...), true
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
// logical value are always byte-equal. decimal128 does NOT go through
// this function — see KeyFieldOnWireBytes and ResolveLookupKeyBytes for
// its dedicated exact-bytes (Decimal128 mantissa) path.
//
// Float key note: f32/f64 key on the raw IEEE-754 bit pattern
// (math.Float32bits / math.Float64bits). This is EXACT bit-pattern
// equality — correct for a byte-index — but it means -0.0 and 0.0 key
// differently (distinct bit patterns) and NaN payloads never key-match
// even against themselves under normal comparison semantics elsewhere
// in the engine. Pulse does not normalize float keys: normalizing would
// make two literals that ARE bit-identical on the wire but were parsed
// from different-looking input strings (or vice versa) diverge instead.
// Callers who need float lookup semantics tolerant of -0.0 or NaN
// should not key on a raw float column.
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
	case encoding.FieldTypeU4, encoding.FieldTypePackedBool:
		// Bit-packed types share their on-wire byte with a neighbouring
		// field in the .pulse record stream (ByteSize()==0 — there is no
		// literal on-wire byte range to copy verbatim). The index key
		// format is a synthetic per-field byte concatenation, not a
		// literal file-offset copy, so both types get a dedicated 1-byte
		// slot here instead: u4's decoded range is 0-15 and
		// packed_bool's is 0/1, both fit in a single byte, and the
		// encoding is injective (distinct decoded values -> distinct
		// bytes) — all exact-bytes point-lookup equality requires.
		return encodeUintOnWire(uint64(v), 1)
	default:
		// Plain unsigned-integer on-wire encodings: u8/u16/u32/u64, date
		// (uint32 epoch-days — same 4-byte on-wire shape as u32, and
		// already carried losslessly by NumericValue), datetime (uint64
		// epoch-seconds — same 8-byte on-wire shape as u64), and
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
// field's on-wire width; date and decimal128 literals resolve through
// their own dedicated exact-bytes parse (encoding.ParseDate /
// encoding.ParseDecimal128 + encoding.EncodeDecimal128) — no
// ParseFloat round-trip for either; every other keyable field type
// parses the literal as a float64 and re-encodes via numericOnWireBytes
// — the same helper KeyFieldOnWireBytes uses — so a build-time on-wire
// key and a lookup-time on-wire key derived from the same logical value
// are always byte-equal.
//
// Returns a PROCESSING_CONFIG coded error when field is nil, when
// field.Type fails IsIndexKeyableFieldType (see that predicate's doc
// comment for the full keyable-type policy — set_* is the only
// currently-registered field type this rejects, via
// IndexKeyRejectionMessage's dedicated explanation), when a categorical
// literal has no matching dictionary entry, when a date/decimal128
// literal fails to parse or a decimal literal overflows the field's
// declared precision, when a datetime literal fails to parse against
// encoding.DateTimeFormats (same no-float-round-trip footing as date),
// or when a numeric literal fails to parse as a float.
func ResolveLookupKeyBytes(field *encoding.Field, literal string) ([]byte, error) {
	if field == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"lookup key field not found in schema")
	}
	if !IsIndexKeyableFieldType(field.Type) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			IndexKeyRejectionMessage(field.Type),
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

	if field.Type == encoding.FieldTypeDate {
		days, err := encoding.ParseDate(literal)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
				fmt.Sprintf("parsing lookup date value %q for field %q", literal, field.Name))
		}
		return numericOnWireBytes(float64(days), field.Type), nil
	}

	if field.Type == encoding.FieldTypeDateTime {
		// Delegates to encoding.ParseDateTime — the same authority
		// io/import.go's convertValue uses to persist a datetime cell —
		// so a literal probed here and the same literal imported land
		// on identical on-wire bytes. A bare integer second count is
		// NOT accepted: the datetime literal grammar is the canonical
		// one, and an ambiguous slash-date form fails loudly there
		// rather than silently swapping day and month.
		sec, err := encoding.ParseDateTime(literal)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
				fmt.Sprintf("parsing lookup datetime value %q for field %q", literal, field.Name))
		}
		return numericOnWireBytes(float64(sec), field.Type), nil
	}

	if field.Type == encoding.FieldTypeDecimal128 {
		return resolveDecimalLookupKeyBytes(field, literal)
	}

	v, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			fmt.Sprintf("parsing lookup value %q for field %q", literal, field.Name))
	}
	return numericOnWireBytes(v, field.Type), nil
}

// resolveDecimalLookupKeyBytes parses literal as a decimal128 value and
// re-encodes it to the exact 16-byte on-wire mantissa representation —
// the same encoding.ParseDecimal128 / Decimal128.Rescale /
// encoding.EncodeDecimal128 sequence io/import.go's convertValueWide
// uses to persist a decimal128 cell, so a literal resolved here and the
// same literal imported as a cell value always land on the identical
// on-wire bytes. No float64 round-trip anywhere in this path — the
// mantissa is arbitrary-precision (big.Int-backed) end to end.
func resolveDecimalLookupKeyBytes(field *encoding.Field, literal string) ([]byte, error) {
	d, parsedScale, err := encoding.ParseDecimal128(literal)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			fmt.Sprintf("parsing lookup decimal value %q for field %q", literal, field.Name))
	}
	if parsedScale != field.Scale {
		d, err = d.Rescale(parsedScale, field.Scale)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
				fmt.Sprintf("rescaling lookup decimal value %q for field %q", literal, field.Name))
		}
	}
	if !d.FitsPrecision(field.Precision) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"lookup decimal value exceeds field precision",
			map[string]any{"field": field.Name, "value": literal, "precision": field.Precision, "scale": field.Scale})
	}
	enc := encoding.EncodeDecimal128(d)
	return append([]byte(nil), enc[:]...), nil
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
