package cli

import "testing"

func TestParseFieldType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"u8", "u8"},
		{"u16", "u16"},
		{"u32", "u32"},
		{"u64", "u64"},
		{"f32", "f32"},
		{"f64", "f64"},
		{"u4", "u4"},
		{"date", "date"},
		{"packed_bool", "packed_bool"},
		{"categorical_u8", "categorical_u8"},
		{"categorical_u16", "categorical_u16"},
		{"categorical_u32", "categorical_u32"},
		{"decimal128", "decimal128"},
		{"unknown_type", "f64"}, // fallback
	}
	for _, tt := range tests {
		ft := parseFieldType(tt.name)
		got := ft.String()
		if got != tt.want {
			t.Errorf("parseFieldType(%q).String() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
