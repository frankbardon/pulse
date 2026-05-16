package encoding

import (
	"bytes"
	"math"
	"testing"
)

// TestReadRecordProjected_SkipsMapWritesButAdvancesBytes verifies that
// ReadRecordWithWideProjected skips map writes for excluded fields but
// still consumes their on-wire bytes, so the byte cursor stays aligned
// for fields that follow.
func TestReadRecordProjected_SkipsMapWritesButAdvancesBytes(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "v", Type: FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			{Name: "y", Type: FieldTypeU16, ByteOffset: 12, CsvColumnIdx: 2},
		},
	}

	// Write one record manually: id=42, v=3.14, y=7.
	var buf bytes.Buffer
	if err := WriteFieldValue(&buf, FieldTypeU32, 42); err != nil {
		t.Fatalf("write id: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeF64, math.Float64bits(3.14)); err != nil {
		t.Fatalf("write v: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeU16, 7); err != nil {
		t.Fatalf("write y: %v", err)
	}

	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	keep := func(name string) bool { return name == "y" }
	if err := rr.ReadRecordWithWideProjected(values, nulls, wide, keep); err != nil {
		t.Fatalf("ReadRecordWithWideProjected: %v", err)
	}

	if _, ok := values["id"]; ok {
		t.Errorf("id should be excluded from values map")
	}
	if _, ok := values["v"]; ok {
		t.Errorf("v should be excluded from values map")
	}
	got, ok := values["y"]
	if !ok {
		t.Fatalf("y missing from values map")
	}
	if got != 7 {
		t.Errorf("y = %v, want 7", got)
	}
}

// TestReadRecordProjected_PackedBoolNeighborAlignment exercises the
// byte-cursor advance through a packed_bool field that the caller
// projects out. The fixed-width neighbour AFTER the packed field must
// still decode correctly.
func TestReadRecordProjected_PackedBoolNeighborAlignment(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "flag", Type: FieldTypePackedBool, ByteOffset: 1, BitPosition: 0, CsvColumnIdx: 1},
			{Name: "b", Type: FieldTypeU32, ByteOffset: 2, CsvColumnIdx: 2},
		},
	}

	// On-wire: 1 byte for a, 1 byte for the packed flag (single bit), 4
	// bytes for b. Mirrors the import.go writer convention.
	var buf bytes.Buffer
	if err := WriteFieldValue(&buf, FieldTypeU8, 5); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := WriteBit(&buf, 0, true); err != nil {
		t.Fatalf("write flag: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeU32, 99); err != nil {
		t.Fatalf("write b: %v", err)
	}

	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	keep := func(name string) bool { return name == "b" }
	if err := rr.ReadRecordWithWideProjected(values, nulls, wide, keep); err != nil {
		t.Fatalf("ReadRecordWithWideProjected: %v", err)
	}

	if _, ok := values["a"]; ok {
		t.Errorf("a should be excluded")
	}
	if _, ok := values["flag"]; ok {
		t.Errorf("flag should be excluded")
	}
	got, ok := values["b"]
	if !ok {
		t.Fatalf("b missing from values map")
	}
	if got != 99 {
		t.Errorf("b = %v, want 99 (byte cursor misaligned through packed_bool)", got)
	}
}

// TestReadRecordProjected_NilFilterFallsThrough verifies that passing a
// nil FieldFilter populates every field — identical behaviour to
// ReadRecordWithWide.
func TestReadRecordProjected_NilFilterFallsThrough(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "b", Type: FieldTypeU16, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	var buf bytes.Buffer
	if err := WriteFieldValue(&buf, FieldTypeU8, 1); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeU16, 2); err != nil {
		t.Fatalf("write b: %v", err)
	}
	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	if err := rr.ReadRecordWithWideProjected(values, nulls, wide, nil); err != nil {
		t.Fatalf("ReadRecordWithWideProjected: %v", err)
	}
	if values["a"] != 1 || values["b"] != 2 {
		t.Errorf("nil filter must keep every field, got %v", values)
	}
}
