package io

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// mockReader implements Reader and ResetReader for testing.
type mockReader struct {
	columns []string
	rows    [][]string
	pos     int
	started bool
}

func newMockReader(columns []string, rows [][]string) *mockReader {
	return &mockReader{columns: columns, rows: rows}
}

func (m *mockReader) ReadHeader() ([]string, error) {
	m.started = true
	return m.columns, nil
}

func (m *mockReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for m.pos < len(m.rows) {
		if err := fn(m.rows[m.pos]); err != nil {
			if err == errStopIteration {
				return nil
			}
			return err
		}
		m.pos++
	}
	return nil
}

func (m *mockReader) Close() error { return nil }

func (m *mockReader) Reset() error {
	m.pos = 0
	m.started = false
	return nil
}

func TestInferSchema_AllNumericTypes(t *testing.T) {
	// u8 range: 0-255
	// u16 range: 256-65535
	// u32 range: 65536-4294967295
	// u64 range: > u32
	// f32: small floats
	// f64: large floats
	columns := []string{"tiny", "medium", "large", "huge", "smallf", "bigf"}
	rows := make([][]string, 100)
	for i := 0; i < 100; i++ {
		rows[i] = []string{
			fmt.Sprintf("%d", i),                            // u8: 0-99
			fmt.Sprintf("%d", 300+i),                        // u16: 300-399
			fmt.Sprintf("%d", 70000+i),                      // u32: 70000+
			fmt.Sprintf("%d", uint64(5000000000)+uint64(i)), // u64
			fmt.Sprintf("%.2f", float64(i)*0.5),             // f32
			fmt.Sprintf("%.10f", float64(i)*1e200),          // f64 (too big for f32)
		}
	}

	reader := newMockReader(columns, rows)
	schema, _, err := InferSchema(reader, 500)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}

	tests := []struct {
		name string
		want encoding.FieldType
	}{
		{"tiny", encoding.FieldTypeU8},
		{"medium", encoding.FieldTypeU16},
		{"large", encoding.FieldTypeU32},
		{"huge", encoding.FieldTypeU64},
		{"smallf", encoding.FieldTypeF32},
		{"bigf", encoding.FieldTypeF64},
	}

	for _, tc := range tests {
		f := schema.Field(tc.name)
		if f == nil {
			t.Errorf("field %q not found", tc.name)
			continue
		}
		if f.Type != tc.want {
			t.Errorf("field %q: got %s, want %s", tc.name, f.Type, tc.want)
		}
	}
}

func TestInferSchema_DateColumn(t *testing.T) {
	reader := newMockReader(
		[]string{"date"},
		[][]string{
			{"2024-01-15"},
			{"2024-02-20"},
			{"2024-03-25"},
			{"2024-04-30"},
			{"2024-05-10"},
		},
	)
	schema, _, err := InferSchema(reader, 500)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	f := schema.Field("date")
	if f == nil {
		t.Fatal("field 'date' not found")
	}
	if f.Type != encoding.FieldTypeDate {
		t.Errorf("got %s, want date", f.Type)
	}
}

func TestInferSchema_BoolColumn(t *testing.T) {
	reader := newMockReader(
		[]string{"flag"},
		[][]string{
			{"true"}, {"false"}, {"yes"}, {"no"}, {"1"}, {"0"},
			{"true"}, {"false"}, {"true"}, {"false"},
		},
	)
	schema, _, err := InferSchema(reader, 500)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	f := schema.Field("flag")
	if f == nil {
		t.Fatal("field 'flag' not found")
	}
	if f.Type != encoding.FieldTypePackedBool {
		t.Errorf("got %s, want packed_bool", f.Type)
	}
}

func TestInferSchema_CategoricalColumn(t *testing.T) {
	rows := make([][]string, 100)
	colors := []string{"red", "green", "blue", "yellow", "orange"}
	for i := range rows {
		rows[i] = []string{colors[i%len(colors)]}
	}

	reader := newMockReader([]string{"color"}, rows)
	schema, _, err := InferSchema(reader, 500)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	f := schema.Field("color")
	if f == nil {
		t.Fatal("field 'color' not found")
	}
	if !f.Type.IsCategorical() {
		t.Errorf("got %s, want categorical", f.Type)
	}
}

func TestInferSchema_CategoricalWidthSelection(t *testing.T) {
	// <=200 unique -> U8
	// <=60000 unique -> U16
	// >60000 unique -> U32
	tests := []struct {
		name   string
		unique int
		want   encoding.FieldType
	}{
		{"small", 5, encoding.FieldTypeCategoricalU8},
		{"medium_boundary", 200, encoding.FieldTypeCategoricalU8},
		{"medium", 201, encoding.FieldTypeCategoricalU16},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build rows where values repeat, giving `unique` distinct values
			// but enough total rows to exceed minSampleRows.
			rows := make([][]string, max(tc.unique*2, 100))
			for i := range rows {
				rows[i] = []string{fmt.Sprintf("val_%d", i%tc.unique)}
			}
			reader := newMockReader([]string{"cat"}, rows)
			schema, _, err := InferSchema(reader, len(rows))
			if err != nil {
				t.Fatalf("InferSchema: %v", err)
			}
			f := schema.Field("cat")
			if f == nil {
				t.Fatal("field 'cat' not found")
			}
			if f.Type != tc.want {
				t.Errorf("got %s, want %s", f.Type, tc.want)
			}
		})
	}
}

func TestInferSchema_Ambiguous_FailsClosed(t *testing.T) {
	// No columns should fail.
	reader := newMockReader([]string{}, [][]string{})
	_, _, err := InferSchema(reader, 500)
	if err == nil {
		t.Fatal("expected error for empty columns")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS) {
		t.Errorf("expected PULSE_IMPORT_SCHEMA_AMBIGUOUS, got: %v", err)
	}

	// No data rows should also fail.
	reader2 := newMockReader([]string{"col"}, [][]string{})
	_, _, err = InferSchema(reader2, 500)
	if err == nil {
		t.Fatal("expected error for no data rows")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS) {
		t.Errorf("expected PULSE_IMPORT_SCHEMA_AMBIGUOUS, got: %v", err)
	}
}

func TestInferSchema_UnboundedCategorical_FailsClosed(t *testing.T) {
	// All unique string values with enough rows.
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("unique_value_%d", i)}
	}

	reader := newMockReader([]string{"id"}, rows)
	_, _, err := InferSchema(reader, 500)
	if err == nil {
		t.Fatal("expected error for unbounded categorical")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_CATEGORICAL_UNBOUNDED) {
		t.Errorf("expected PULSE_IMPORT_CATEGORICAL_UNBOUNDED, got: %v", err)
	}
}

func TestInferSchema_MinSampleRows(t *testing.T) {
	// Even if we pass sampleRows=10, it should use minSampleRows (50).
	rows := make([][]string, 60)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("%d", i%100)}
	}

	reader := newMockReader([]string{"val"}, rows)
	schema, _, err := InferSchema(reader, 10) // should be clamped to 50
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	if schema == nil {
		t.Fatal("schema is nil")
	}
}

func TestInferSchema_CustomSampleSize(t *testing.T) {
	// Create 200 rows, sample only 100.
	rows := make([][]string, 200)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("%d", i%50)}
	}

	reader := newMockReader([]string{"val"}, rows)
	schema, _, err := InferSchema(reader, 100)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	if schema == nil {
		t.Fatal("schema is nil")
	}
	// Should still infer u8 since all values 0-49.
	f := schema.Field("val")
	if f.Type != encoding.FieldTypeU8 {
		t.Errorf("got %s, want u8", f.Type)
	}
}

func TestInferSchema_NullableDetection(t *testing.T) {
	// Mix of integers with some null/empty values.
	rows := [][]string{
		{"10"}, {"20"}, {""}, {"30"}, {"null"}, {"40"}, {"50"},
		{"10"}, {"20"}, {""}, {"30"}, {"null"}, {"40"}, {"50"},
	}

	reader := newMockReader([]string{"val"}, rows)
	schema, _, err := InferSchema(reader, 500)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	f := schema.Field("val")
	if f == nil {
		t.Fatal("field 'val' not found")
	}
	// Should detect nullable integer type (NullableU8 since values fit in u8).
	switch f.Type {
	case encoding.FieldTypeNullableU4, encoding.FieldTypeNullableU8:
		// acceptable
	default:
		t.Errorf("got %s, want nullable integer type", f.Type)
	}
}

func TestInferSchema_NullableBool(t *testing.T) {
	rows := [][]string{
		{"true"}, {"false"}, {""}, {"true"}, {"null"}, {"false"},
		{"true"}, {"false"}, {""}, {"true"}, {"null"}, {"false"},
	}

	reader := newMockReader([]string{"flag"}, rows)
	schema, _, err := InferSchema(reader, 500)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}
	f := schema.Field("flag")
	if f == nil {
		t.Fatal("field 'flag' not found")
	}
	if f.Type != encoding.FieldTypeNullableBool {
		t.Errorf("got %s, want nullable_bool", f.Type)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
