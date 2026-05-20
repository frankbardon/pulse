package encoding

import "fmt"

// FieldType identifies the data type stored in a schema field.
type FieldType byte

// All 13 field types supported by the .pulse format. Nullability is
// orthogonal to type — any field can be marked nullable via
// encoding.Field.Nullable and the per-record null bitmap carries the
// actual null state.
const (
	FieldTypeU8             FieldType = iota // 0
	FieldTypeU16                             // 1
	FieldTypeU32                             // 2
	FieldTypeU64                             // 3
	FieldTypeF32                             // 4
	FieldTypeF64                             // 5
	FieldTypeU4                              // 6  (4-bit, bit-packed)
	FieldTypeDate                            // 7
	FieldTypePackedBool                      // 8  (1-bit, bit-packed)
	FieldTypeCategoricalU8                   // 9
	FieldTypeCategoricalU16                  // 10
	FieldTypeCategoricalU32                  // 11
	FieldTypeDecimal128                      // 12

	fieldTypeCount // sentinel
)

// ByteSize returns the number of bytes this field type occupies in a record.
// Bit-packed types (U4, PackedBool) share bytes with adjacent fields and
// return 0 here; their on-wire stride is handled by Schema.RecordByteSize.
func (ft FieldType) ByteSize() int {
	switch ft {
	case FieldTypeU8, FieldTypeCategoricalU8:
		return 1
	case FieldTypeU16, FieldTypeCategoricalU16:
		return 2
	case FieldTypeU32, FieldTypeF32, FieldTypeDate, FieldTypeCategoricalU32:
		return 4
	case FieldTypeU64, FieldTypeF64:
		return 8
	case FieldTypeDecimal128:
		return 16
	case FieldTypeU4, FieldTypePackedBool:
		return 0 // bit-packed
	default:
		return 0
	}
}

// String returns a human-readable name for the field type.
func (ft FieldType) String() string {
	switch ft {
	case FieldTypeU8:
		return "u8"
	case FieldTypeU16:
		return "u16"
	case FieldTypeU32:
		return "u32"
	case FieldTypeU64:
		return "u64"
	case FieldTypeF32:
		return "f32"
	case FieldTypeF64:
		return "f64"
	case FieldTypeU4:
		return "u4"
	case FieldTypeDate:
		return "date"
	case FieldTypePackedBool:
		return "packed_bool"
	case FieldTypeCategoricalU8:
		return "categorical_u8"
	case FieldTypeCategoricalU16:
		return "categorical_u16"
	case FieldTypeCategoricalU32:
		return "categorical_u32"
	case FieldTypeDecimal128:
		return "decimal128"
	default:
		return fmt.Sprintf("unknown(%d)", ft)
	}
}

// IsCategorical reports whether the field type is one of the categorical types.
func (ft FieldType) IsCategorical() bool {
	return ft == FieldTypeCategoricalU8 || ft == FieldTypeCategoricalU16 || ft == FieldTypeCategoricalU32
}

// IsBitPacked reports whether the field type shares its on-wire byte with
// adjacent fields via bit packing (U4, PackedBool). Used by stride math
// and the schema layout pass to advance the bit cursor instead of the
// byte cursor.
func (ft FieldType) IsBitPacked() bool {
	return ft == FieldTypeU4 || ft == FieldTypePackedBool
}

// Numeric predicate hierarchy
//
// The engine has two tiers of "is this a number" depending on whether the
// caller wants on-wire scalar arithmetic semantics or analytics-layer
// semantics. Specialized predicates (`predict_window.isNumericType`,
// `facet.facetIsNumeric`, etc.) intentionally diverge — they encode
// per-operator restrictions (e.g. window operators exclude decimal128
// because the buffered decimal path is unimplemented). Those package-local
// helpers point back at the canonical predicates below in their doc
// comments and should keep their narrower view.
//
// IsNumeric — the strict scalar family. Use when the caller does real-
// number math on a single column and wants to refuse anything bit-packed.
//
// IsNumericForAnalytics — the analytics layer's broader view. Includes
// the bit-packed integer encodings and date. Aggregators, regressions,
// and any other operator that consumes values via Record.NumericValue
// should use this predicate.

// IsNumeric reports whether the field type is a strict scalar number:
// the unsigned-integer family (u8/u16/u32/u64), the float family
// (f32/f64), and decimal128. Date and bit-packed integer encodings are
// excluded — see IsNumericForAnalytics for the analytics-layer predicate.
func (ft FieldType) IsNumeric() bool {
	if ft.IsDecimal() {
		return true
	}
	switch ft {
	case FieldTypeU8, FieldTypeU16, FieldTypeU32, FieldTypeU64,
		FieldTypeF32, FieldTypeF64:
		return true
	}
	return false
}

// IsNumericForAnalytics reports whether the field type carries a meaningful
// scalar value for numeric analytics (regression, sum/avg/stddev/min/max/
// variance aggregators). The set is broader than IsNumeric: bit-packed
// integer encodings (u4, packed_bool) and date are included because their
// stored representation is an ordinal / cardinal number the analytics layer
// can average, sum, or regress without an explicit ATTR_FORMULA cast.
//
// Null exclusion is the reader's responsibility: the per-record null
// bitmap marks any field index as null at decode time so the downstream
// Record.NumericValue contract (returns ok=false on null) keeps the
// aggregation denominator honest.
func (ft FieldType) IsNumericForAnalytics() bool {
	if ft.IsNumeric() {
		return true
	}
	switch ft {
	case FieldTypeDate, FieldTypeU4, FieldTypePackedBool:
		return true
	}
	return false
}

// IsDecimal reports whether the field type is decimal128.
func (ft FieldType) IsDecimal() bool {
	return ft == FieldTypeDecimal128
}

// IsKnown reports whether the byte value corresponds to a registered type.
// Used by the schema reader to reject files written by a future binary
// version that introduces unknown type bytes.
func (ft FieldType) IsKnown() bool {
	return ft < fieldTypeCount
}

// MaxCategoricalEntries returns the maximum dictionary size for a categorical type.
// Returns 0 for non-categorical types.
func (ft FieldType) MaxCategoricalEntries() uint32 {
	switch ft {
	case FieldTypeCategoricalU8:
		return 256
	case FieldTypeCategoricalU16:
		return 65536
	case FieldTypeCategoricalU32:
		return 4294967295
	default:
		return 0
	}
}
