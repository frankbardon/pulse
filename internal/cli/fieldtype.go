package cli

import "github.com/frankbardon/pulse/encoding"

// parseFieldType converts a string type name to an encoding.FieldType.
func parseFieldType(name string) encoding.FieldType {
	m := map[string]encoding.FieldType{
		"u8":              encoding.FieldTypeU8,
		"u16":             encoding.FieldTypeU16,
		"u32":             encoding.FieldTypeU32,
		"u64":             encoding.FieldTypeU64,
		"f32":             encoding.FieldTypeF32,
		"f64":             encoding.FieldTypeF64,
		"nullable_bool":   encoding.FieldTypeNullableBool,
		"nullable_u4":     encoding.FieldTypeNullableU4,
		"nullable_u8":     encoding.FieldTypeNullableU8,
		"nullable_u16":    encoding.FieldTypeNullableU16,
		"date":            encoding.FieldTypeDate,
		"packed_bool":     encoding.FieldTypePackedBool,
		"categorical_u8":  encoding.FieldTypeCategoricalU8,
		"categorical_u16": encoding.FieldTypeCategoricalU16,
		"categorical_u32": encoding.FieldTypeCategoricalU32,
	}
	if ft, ok := m[name]; ok {
		return ft
	}
	return encoding.FieldTypeF64 // fallback
}
