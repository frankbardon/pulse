package cli

import "testing"

func TestFormatFromExt(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"data.csv", "csv"},
		{"data.CSV", "csv"},
		{"data.tsv", "tsv"},
		{"data.ndjson", "ndjson"},
		{"data.jsonl", "ndjson"},
		{"data.parquet", "parquet"},
		{"data.pq", "parquet"},
		{"data.xlsx", "excel"},
		{"data.xls", "excel"},
		{"data.pulse", "pulse"},
		{"data.unknown", ""},
		{"noext", ""},
	}
	for _, tt := range tests {
		got := formatFromExt(tt.path)
		if got != tt.want {
			t.Errorf("formatFromExt(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

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
		{"date", "date"},
		{"packed_bool", "packed_bool"},
		{"nullable_bool", "nullable_bool"},
		{"nullable_u4", "nullable_u4"},
		{"nullable_u8", "nullable_u8"},
		{"nullable_u16", "nullable_u16"},
		{"categorical_u8", "categorical_u8"},
		{"categorical_u16", "categorical_u16"},
		{"categorical_u32", "categorical_u32"},
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
