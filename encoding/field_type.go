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

// All 19 field types supported by the .pulse format.
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
	FieldTypePointF64                            // 17
	FieldTypeH3Cell                              // 18

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
	case FieldTypeU64, FieldTypeF64, FieldTypeH3Cell:
		return 8
	case FieldTypeDecimal128, FieldTypeNullableDecimal128, FieldTypePointF64:
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
	case FieldTypePointF64:
		return "point_f64"
	case FieldTypeH3Cell:
		return "h3_cell"
	default:
		return fmt.Sprintf("unknown(%d)", ft)
	}
}

// IsCategorical reports whether the field type is one of the categorical types.
func (ft FieldType) IsCategorical() bool {
	return ft == FieldTypeCategoricalU8 || ft == FieldTypeCategoricalU16 || ft == FieldTypeCategoricalU32
}

// IsNumericForAnalytics reports whether the field type carries a meaningful
// scalar value for numeric analytics (regression, sum/avg/stddev/min/max/
// variance aggregators). The set is broader than the on-wire integer/float
// family: bit-packed integer encodings (nullable_u4, nullable_bool,
// packed_bool) and date are included because their stored representation is
// an ordinal/cardinal number the analytics layer can average, sum, or
// regress without an explicit ATTR_FORMULA cast.
//
// Null exclusion is the reader's responsibility: nullable_u4 marks 0x0F as
// null at decode time so the downstream Record.NumericValue contract
// (returns ok=false on null) keeps the aggregation denominator honest.
func (ft FieldType) IsNumericForAnalytics() bool {
	switch ft {
	case FieldTypeU8, FieldTypeU16, FieldTypeU32, FieldTypeU64,
		FieldTypeF32, FieldTypeF64,
		FieldTypeDate,
		FieldTypeDecimal128, FieldTypeNullableDecimal128,
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

// IsGeo reports whether the field type is a geospatial type.
func (ft FieldType) IsGeo() bool {
	return ft == FieldTypePointF64 || ft == FieldTypeH3Cell
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
