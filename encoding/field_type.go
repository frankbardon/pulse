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
	FieldTypeSetU8                           // 13 (bitmask over shared dict, ≤8 members)
	FieldTypeSetU16                          // 14 (≤16 members)
	FieldTypeSetU32                          // 15 (≤32 members)
	FieldTypeSetU64                          // 16 (≤64 members)

	fieldTypeCount // sentinel
)

// ByteSize returns the number of bytes this field type occupies in a record.
// Bit-packed types (U4, PackedBool) share bytes with adjacent fields and
// return 0 here; their on-wire stride is handled by Schema.RecordByteSize.
func (ft FieldType) ByteSize() int {
	switch ft {
	case FieldTypeU8, FieldTypeCategoricalU8, FieldTypeSetU8:
		return 1
	case FieldTypeU16, FieldTypeCategoricalU16, FieldTypeSetU16:
		return 2
	case FieldTypeU32, FieldTypeF32, FieldTypeDate, FieldTypeCategoricalU32, FieldTypeSetU32:
		return 4
	case FieldTypeU64, FieldTypeF64, FieldTypeSetU64:
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
	case FieldTypeSetU8:
		return "set_u8"
	case FieldTypeSetU16:
		return "set_u16"
	case FieldTypeSetU32:
		return "set_u32"
	case FieldTypeSetU64:
		return "set_u64"
	default:
		return fmt.Sprintf("unknown(%d)", ft)
	}
}

// ParseFieldType inverts FieldType.String, returning the typed value
// and ok=true when name matches a registered FieldType. Caller paths
// (managed-imports sidecar parsing, MCP schema validation, ad-hoc
// admin tooling) use this to round-trip an externally supplied type
// name through the codec layer without relying on stringly typed
// switches in every place.
func ParseFieldType(name string) (FieldType, bool) {
	switch name {
	case "u4":
		return FieldTypeU4, true
	case "u8":
		return FieldTypeU8, true
	case "u16":
		return FieldTypeU16, true
	case "u32":
		return FieldTypeU32, true
	case "u64":
		return FieldTypeU64, true
	case "f32":
		return FieldTypeF32, true
	case "f64":
		return FieldTypeF64, true
	case "date":
		return FieldTypeDate, true
	case "packed_bool":
		return FieldTypePackedBool, true
	case "categorical_u8":
		return FieldTypeCategoricalU8, true
	case "categorical_u16":
		return FieldTypeCategoricalU16, true
	case "categorical_u32":
		return FieldTypeCategoricalU32, true
	case "decimal128":
		return FieldTypeDecimal128, true
	case "set_u8":
		return FieldTypeSetU8, true
	case "set_u16":
		return FieldTypeSetU16, true
	case "set_u32":
		return FieldTypeSetU32, true
	case "set_u64":
		return FieldTypeSetU64, true
	}
	return 0, false
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

// IsSet reports whether the field type is a bitmask-over-dictionary set
// (multi-select) type. Set fields share the categorical dictionary block
// shape but the on-wire payload is a fixed-width unsigned integer whose
// bit i corresponds to dictionary entry i.
func (ft FieldType) IsSet() bool {
	switch ft {
	case FieldTypeSetU8, FieldTypeSetU16, FieldTypeSetU32, FieldTypeSetU64:
		return true
	}
	return false
}

// MaxSetEntries returns the maximum dictionary size (= bitmask capacity)
// for a set type. Returns 0 for non-set types.
func (ft FieldType) MaxSetEntries() uint32 {
	switch ft {
	case FieldTypeSetU8:
		return 8
	case FieldTypeSetU16:
		return 16
	case FieldTypeSetU32:
		return 32
	case FieldTypeSetU64:
		return 64
	default:
		return 0
	}
}

// HasDictionary reports whether the field type carries an inline
// dictionary block in the schema (categorical_* or set_*).
func (ft FieldType) HasDictionary() bool {
	return ft.IsCategorical() || ft.IsSet()
}

// MaxDictEntries returns the capacity of the inline dictionary block
// for dictionary-bearing types. Categoricals return MaxCategoricalEntries;
// sets return MaxSetEntries (= bitmask width). Non-dictionary types
// return 0.
func (ft FieldType) MaxDictEntries() uint32 {
	switch {
	case ft.IsSet():
		return ft.MaxSetEntries()
	case ft.IsCategorical():
		return ft.MaxCategoricalEntries()
	}
	return 0
}
