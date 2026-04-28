package io

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func TestConvertValue_AllTypes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		ft      encoding.FieldType
		wantErr bool
	}{
		{"u8", "42", encoding.FieldTypeU8, false},
		{"u8_null", "", encoding.FieldTypeU8, false},
		{"u16", "1000", encoding.FieldTypeU16, false},
		{"u16_null", "null", encoding.FieldTypeU16, false},
		{"u32", "100000", encoding.FieldTypeU32, false},
		{"u32_null", "NA", encoding.FieldTypeU32, false},
		{"u64", "999999999999", encoding.FieldTypeU64, false},
		{"u64_null", "n/a", encoding.FieldTypeU64, false},
		{"f32", "3.14", encoding.FieldTypeF32, false},
		{"f32_null", "", encoding.FieldTypeF32, false},
		{"f64", "3.14159265358979", encoding.FieldTypeF64, false},
		{"f64_null", "", encoding.FieldTypeF64, false},
		{"date", "2024-01-15", encoding.FieldTypeDate, false},
		{"date_null", "", encoding.FieldTypeDate, false},
		{"date_bad", "not-a-date", encoding.FieldTypeDate, true},
		{"bool_true", "true", encoding.FieldTypePackedBool, false},
		{"bool_false", "false", encoding.FieldTypePackedBool, false},
		{"bool_yes", "yes", encoding.FieldTypePackedBool, false},
		{"bool_null", "", encoding.FieldTypePackedBool, false},
		{"nullable_bool_true", "true", encoding.FieldTypeNullableBool, false},
		{"nullable_bool_false", "false", encoding.FieldTypeNullableBool, false},
		{"nullable_bool_null", "", encoding.FieldTypeNullableBool, false},
		{"nullable_bool_bad", "maybe", encoding.FieldTypeNullableBool, true},
		{"nullable_u4", "5", encoding.FieldTypeNullableU4, false},
		{"nullable_u4_null", "", encoding.FieldTypeNullableU4, false},
		{"nullable_u4_overflow", "15", encoding.FieldTypeNullableU4, true},
		{"nullable_u8", "100", encoding.FieldTypeNullableU8, false},
		{"nullable_u8_null", "", encoding.FieldTypeNullableU8, false},
		{"nullable_u16", "50000", encoding.FieldTypeNullableU16, false},
		{"nullable_u16_null", "null", encoding.FieldTypeNullableU16, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := convertValue(tc.raw, tc.ft, nil)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestConvertValue_Categorical(t *testing.T) {
	dict := encoding.NewDictionary()

	val, err := convertValue("apple", encoding.FieldTypeCategoricalU8, dict)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val != 0 {
		t.Errorf("got %d, want 0", val)
	}

	val2, err := convertValue("banana", encoding.FieldTypeCategoricalU8, dict)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val2 != 1 {
		t.Errorf("got %d, want 1", val2)
	}

	// Duplicate returns same ID.
	val3, err := convertValue("apple", encoding.FieldTypeCategoricalU8, dict)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val3 != 0 {
		t.Errorf("got %d, want 0", val3)
	}

	// Null.
	val4, err := convertValue("", encoding.FieldTypeCategoricalU8, dict)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val4 != 0 {
		t.Errorf("null categorical should be 0, got %d", val4)
	}
}

func TestConvertValue_CategoricalNilDict(t *testing.T) {
	_, err := convertValue("val", encoding.FieldTypeCategoricalU8, nil)
	if err == nil {
		t.Error("expected error for nil dictionary")
	}
}

func TestConvertValue_F32_Bits(t *testing.T) {
	val, err := convertValue("3.14", encoding.FieldTypeF32, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	got := math.Float32frombits(uint32(val))
	if got < 3.13 || got > 3.15 {
		t.Errorf("got %f, want ~3.14", got)
	}
}

func TestConvertValue_F64_Bits(t *testing.T) {
	val, err := convertValue("3.14159265358979", encoding.FieldTypeF64, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	got := math.Float64frombits(val)
	if got < 3.14 || got > 3.15 {
		t.Errorf("got %f, want ~3.14159", got)
	}
}

func TestConvertValue_BoolValues(t *testing.T) {
	boolTrue := []string{"true", "yes", "1", "t", "y", "True", "YES", "T", "Y"}
	boolFalse := []string{"false", "no", "0", "f", "n", "False", "NO", "F", "N"}

	for _, v := range boolTrue {
		val, err := convertValue(v, encoding.FieldTypePackedBool, nil)
		if err != nil {
			t.Errorf("parseBool(%q): %v", v, err)
		}
		if val != 1 {
			t.Errorf("parseBool(%q) = %d, want 1", v, val)
		}
	}

	for _, v := range boolFalse {
		val, err := convertValue(v, encoding.FieldTypePackedBool, nil)
		if err != nil {
			t.Errorf("parseBool(%q): %v", v, err)
		}
		if val != 0 {
			t.Errorf("parseBool(%q) = %d, want 0", v, val)
		}
	}
}

func TestConvertValue_DateFormats(t *testing.T) {
	dates := []string{
		"2024-01-15",
		"01/15/2024",
		"2024-01-15T10:30:00Z",
		"2024-01-15T10:30:00",
		"2024/01/15",
		"15-Jan-2024",
	}

	for _, d := range dates {
		_, err := convertValue(d, encoding.FieldTypeDate, nil)
		if err != nil {
			t.Errorf("date %q: %v", d, err)
		}
	}
}

func TestConvertValue_UnsupportedType(t *testing.T) {
	_, err := convertValue("x", encoding.FieldType(200), nil)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestFormatFieldValue_AllTypes(t *testing.T) {
	tests := []struct {
		name string
		ft   encoding.FieldType
		raw  uint64
		want string
	}{
		{"u8", encoding.FieldTypeU8, 42, "42"},
		{"u16", encoding.FieldTypeU16, 1000, "1000"},
		{"u32", encoding.FieldTypeU32, 100000, "100000"},
		{"u64", encoding.FieldTypeU64, 999999999999, "999999999999"},
		{"f32", encoding.FieldTypeF32, uint64(math.Float32bits(3.14)), "3.14"},
		{"f64", encoding.FieldTypeF64, math.Float64bits(3.14), "3.14"},
		{"nullable_u8", encoding.FieldTypeNullableU8, 42, "42"},
		{"nullable_u8_null", encoding.FieldTypeNullableU8, 0xFF, ""},
		{"nullable_u16", encoding.FieldTypeNullableU16, 1000, "1000"},
		{"nullable_u16_null", encoding.FieldTypeNullableU16, 0xFFFF, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatFieldValue(tc.ft, tc.raw, nil)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatFieldValue_Categorical(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")

	got := formatFieldValue(encoding.FieldTypeCategoricalU8, 0, dict)
	if got != "red" {
		t.Errorf("got %q, want red", got)
	}
	got = formatFieldValue(encoding.FieldTypeCategoricalU16, 1, dict)
	if got != "green" {
		t.Errorf("got %q, want green", got)
	}
	got = formatFieldValue(encoding.FieldTypeCategoricalU32, 2, dict)
	if got != "blue" {
		t.Errorf("got %q, want blue", got)
	}

	// Without dictionary, falls back to numeric.
	got = formatFieldValue(encoding.FieldTypeCategoricalU8, 0, nil)
	if got != "0" {
		t.Errorf("got %q, want 0", got)
	}
}

func TestFormatFieldValue_Date(t *testing.T) {
	// Round-trip: convert a date string to raw, then format it back.
	raw, err := convertValue("2024-01-15", encoding.FieldTypeDate, nil)
	if err != nil {
		t.Fatalf("convertValue: %v", err)
	}
	got := formatFieldValue(encoding.FieldTypeDate, raw, nil)
	if got != "2024-01-15" {
		t.Errorf("got %q, want 2024-01-15", got)
	}
}

func TestFormatPackedValue_AllTypes(t *testing.T) {
	tests := []struct {
		name string
		ft   encoding.FieldType
		b    byte
		want string
	}{
		{"bool_true", encoding.FieldTypePackedBool, 1, "true"},
		{"bool_false", encoding.FieldTypePackedBool, 0, "false"},
		{"nullable_bool_null", encoding.FieldTypeNullableBool, 0, ""},
		{"nullable_bool_false", encoding.FieldTypeNullableBool, 1, "false"},
		{"nullable_bool_true", encoding.FieldTypeNullableBool, 2, "true"},
		{"nullable_bool_unknown", encoding.FieldTypeNullableBool, 3, ""},
		{"nullable_u4", encoding.FieldTypeNullableU4, 5, "5"},
		{"nullable_u4_null", encoding.FieldTypeNullableU4, 0x0F, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatPackedValue(tc.ft, tc.b)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
