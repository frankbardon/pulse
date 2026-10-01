package io

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// convertValue is now a pure value converter: nulls are handled by the
// caller via the per-record bitmap, so this function is never invoked on
// a null cell. Tests cover only the value-bearing branches.
func TestConvertValue_AllTypes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		ft      encoding.FieldType
		wantErr bool
	}{
		{"u4", "5", encoding.FieldTypeU4, false},
		{"u4_overflow", "16", encoding.FieldTypeU4, true},
		{"u8", "42", encoding.FieldTypeU8, false},
		{"u16", "1000", encoding.FieldTypeU16, false},
		{"u32", "100000", encoding.FieldTypeU32, false},
		{"u64", "999999999999", encoding.FieldTypeU64, false},
		{"f32", "3.14", encoding.FieldTypeF32, false},
		{"f64", "3.14159265358979", encoding.FieldTypeF64, false},
		{"date", "2024-01-15", encoding.FieldTypeDate, false},
		{"date_bad", "not-a-date", encoding.FieldTypeDate, true},
		{"bool_true", "true", encoding.FieldTypePackedBool, false},
		{"bool_false", "false", encoding.FieldTypePackedBool, false},
		{"bool_yes", "yes", encoding.FieldTypePackedBool, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := convertValue(tc.raw, tc.ft, nil, "")
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

	val, err := convertValue("apple", encoding.FieldTypeCategoricalU8, dict, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val != 0 {
		t.Errorf("got %d, want 0", val)
	}

	val2, err := convertValue("banana", encoding.FieldTypeCategoricalU8, dict, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val2 != 1 {
		t.Errorf("got %d, want 1", val2)
	}

	// Duplicate returns same ID.
	val3, err := convertValue("apple", encoding.FieldTypeCategoricalU8, dict, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val3 != 0 {
		t.Errorf("got %d, want 0", val3)
	}
}

func TestConvertValue_CategoricalNilDict(t *testing.T) {
	_, err := convertValue("val", encoding.FieldTypeCategoricalU8, nil, "")
	if err == nil {
		t.Error("expected error for nil dictionary")
	}
}

func TestConvertValue_F32_Bits(t *testing.T) {
	val, err := convertValue("3.14", encoding.FieldTypeF32, nil, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	got := math.Float32frombits(uint32(val))
	if got < 3.13 || got > 3.15 {
		t.Errorf("got %f, want ~3.14", got)
	}
}

func TestConvertValue_F64_Bits(t *testing.T) {
	val, err := convertValue("3.14159265358979", encoding.FieldTypeF64, nil, "")
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
		val, err := convertValue(v, encoding.FieldTypePackedBool, nil, "")
		if err != nil {
			t.Errorf("parseBool(%q): %v", v, err)
		}
		if val != 1 {
			t.Errorf("parseBool(%q) = %d, want 1", v, val)
		}
	}

	for _, v := range boolFalse {
		val, err := convertValue(v, encoding.FieldTypePackedBool, nil, "")
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
		_, err := convertValue(d, encoding.FieldTypeDate, nil, "")
		if err != nil {
			t.Errorf("date %q: %v", d, err)
		}
	}
}

func TestConvertValue_UnsupportedType(t *testing.T) {
	_, err := convertValue("x", encoding.FieldType(200), nil, "")
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

// formatFieldValue is also pure — nulls are applied separately by the
// export loop via the per-record bitmap and never reach this function.
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
	raw, err := convertValue("2024-01-15", encoding.FieldTypeDate, nil, "")
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
		{"u4_low", encoding.FieldTypeU4, 5, "5"},
		{"u4_high_masked", encoding.FieldTypeU4, 0xF5, "5"}, // upper nibble masked off
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
