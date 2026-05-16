package encoding

import "fmt"

// FieldType identifies the data type stored in a schema field.
type FieldType byte

// nullableU4NullSentinel is the 4-bit value (0x0F = 15) reserved to mark
// a nullable_u4 cell as null. The reader maps this raw value to the
// Record's null map so downstream Record.NumericValue returns ok=false,
// keeping aggregation denominators and regression observation counts
// correct without any orchestrator-level branching.
const nullableU4NullSentinel uint8 = 0x0F

// All 17 field types supported by the .pulse format.
const (
	FieldTypeU8                 FieldType = iota // 0
	FieldTypeU16                                 // 1
	FieldTypeU32                                 // 2
	FieldTypeU64                                 // 3
	FieldTypeF32                                 // 4
	FieldTypeF64                                 // 5
	FieldTypeNullableBool                        // 6
	FieldTypeNullableU4                          // 7
	FieldTypeNullableU8                          // 8
	FieldTypeNullableU16                         // 9
	FieldTypeDate                                // 10
	FieldTypePackedBool                          // 11
	FieldTypeCategoricalU8                       // 12
	FieldTypeCategoricalU16                      // 13
	FieldTypeCategoricalU32                      // 14
	FieldTypeDecimal128                          // 15
	FieldTypeNullableDecimal128                  // 16

	fieldTypeCount // sentinel
)

// ByteSize returns the number of bytes this field type occupies in a record.
// Packed types (PackedBool, NullableBool, NullableU4) share bytes with
// adjacent fields via bit packing and return 0 here.
func (ft FieldType) ByteSize() int {
	switch ft {
	case FieldTypeU8, FieldTypeNullableU8, FieldTypeCategoricalU8:
		return 1
	case FieldTypeU16, FieldTypeNullableU16, FieldTypeCategoricalU16:
		return 2
	case FieldTypeU32, FieldTypeF32, FieldTypeDate, FieldTypeCategoricalU32:
		return 4
	case FieldTypeU64, FieldTypeF64:
		return 8
	case FieldTypeDecimal128, FieldTypeNullableDecimal128:
		return 16
	case FieldTypeNullableBool, FieldTypeNullableU4, FieldTypePackedBool:
		return 0 // bit-packed, no whole-byte allocation
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
	case FieldTypeNullableBool:
		return "nullable_bool"
	case FieldTypeNullableU4:
		return "nullable_u4"
	case FieldTypeNullableU8:
		return "nullable_u8"
	case FieldTypeNullableU16:
		return "nullable_u16"
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
	case FieldTypeNullableDecimal128:
		return "nullable_decimal128"
	default:
		return fmt.Sprintf("unknown(%d)", ft)
	}
}

// IsCategorical reports whether the field type is one of the categorical types.
func (ft FieldType) IsCategorical() bool {
	return ft == FieldTypeCategoricalU8 || ft == FieldTypeCategoricalU16 || ft == FieldTypeCategoricalU32
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
// (f32/f64), and the decimal family (decimal128/nullable_decimal128).
// Date and bit-packed integer encodings are excluded — see
// IsNumericForAnalytics for the analytics-layer predicate.
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
// integer encodings (nullable_u4, nullable_bool, packed_bool) and date
// are included because their stored representation is an ordinal /
// cardinal number the analytics layer can average, sum, or regress
// without an explicit ATTR_FORMULA cast.
//
// Null exclusion is the reader's responsibility: nullable_u4 marks 0x0F
// as null at decode time so the downstream Record.NumericValue contract
// (returns ok=false on null) keeps the aggregation denominator honest.
func (ft FieldType) IsNumericForAnalytics() bool {
	if ft.IsNumeric() {
		return true
	}
	switch ft {
	case FieldTypeDate,
		FieldTypeNullableU4, FieldTypeNullableU8, FieldTypeNullableU16,
		FieldTypeNullableBool, FieldTypePackedBool:
		return true
	}
	return false
}

// IsDecimal reports whether the field type is a decimal128 variant.
func (ft FieldType) IsDecimal() bool {
	return ft == FieldTypeDecimal128 || ft == FieldTypeNullableDecimal128
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
