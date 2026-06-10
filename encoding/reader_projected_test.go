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

// TestReadRecordProjected_SetFieldAlignment exercises the projected
// reader through a set_u8 field — once with the set field EXCLUDED
// from projection (cursor must advance past the mask byte so the
// scalar neighbour after it still decodes), and once with the set
// field INCLUDED (the wide map must carry the raw uint64 mask). Set
// fields share the default branch of the per-field dispatch with
// other scalar types but additionally populate wide[name] = raw so
// downstream operators can read the bitmask without float coercion.
// Existing projection tests cover scalars and bit-packed neighbours;
// neither covers set fields, leaving the mask-byte-advance contract
// untested.
func TestReadRecordProjected_SetFieldAlignment(t *testing.T) {
	// 3-field schema: id (u32, offset 0), issuers (set_u8, offset 4),
	// age (u8, offset 5). Total record stride: 6 bytes.
	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "issuers", Type: FieldTypeSetU8, ByteOffset: 4, CsvColumnIdx: 1},
			{Name: "age", Type: FieldTypeU8, ByteOffset: 5, CsvColumnIdx: 2},
		},
	}

	// Encode one record: id=42, issuers mask=0b1010 (two bits set), age=33.
	var buf bytes.Buffer
	if err := WriteFieldValue(&buf, FieldTypeU32, 42); err != nil {
		t.Fatalf("write id: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeSetU8, 0b1010); err != nil {
		t.Fatalf("write issuers: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeU8, 33); err != nil {
		t.Fatalf("write age: %v", err)
	}
	recordBytes := buf.Bytes()
	if len(recordBytes) != 6 {
		t.Fatalf("record stride: got %d, want 6", len(recordBytes))
	}

	// --- Sub-test 1: set field EXCLUDED from projection ---
	rr := NewRecordReader(bytes.NewReader(recordBytes), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	keep := func(name string) bool { return name == "id" || name == "age" }
	if err := rr.ReadRecordWithWideProjected(values, nulls, wide, keep); err != nil {
		t.Fatalf("ReadRecordWithWideProjected (issuers excluded): %v", err)
	}
	if _, ok := values["issuers"]; ok {
		t.Errorf("excluded issuers must not appear in values map")
	}
	if _, ok := wide["issuers"]; ok {
		t.Errorf("excluded issuers must not appear in wide map")
	}
	if got, ok := values["id"]; !ok || got != 42 {
		t.Errorf("id: got %v ok=%v, want 42", got, ok)
	}
	// Critical: this asserts the cursor advanced past the set_u8 mask
	// byte. If the projection path skipped the read, the u8 age field
	// would decode 0b1010 = 10 from the mask byte and EOF on the age
	// byte (or read past end into the bitmap).
	if got, ok := values["age"]; !ok || got != 33 {
		t.Errorf("age: got %v ok=%v, want 33 — cursor misaligned through skipped set_u8 mask", got, ok)
	}

	// --- Sub-test 2: set field INCLUDED in projection ---
	rr2 := NewRecordReader(bytes.NewReader(recordBytes), schema)
	values2 := make(map[string]float64)
	nulls2 := make(map[string]bool)
	wide2 := make(map[string]any)
	keepAll := func(name string) bool { return true }
	if err := rr2.ReadRecordWithWideProjected(values2, nulls2, wide2, keepAll); err != nil {
		t.Fatalf("ReadRecordWithWideProjected (issuers included): %v", err)
	}
	rawMask, ok := wide2["issuers"]
	if !ok {
		t.Fatal("issuers missing from wide map when projected in")
	}
	if rawMask.(uint64) != 0b1010 {
		t.Errorf("wide[issuers] = 0b%b, want 0b1010 (raw uint64 mask)", rawMask)
	}
	if values2["id"] != 42 || values2["age"] != 33 {
		t.Errorf("scalar neighbors corrupt with set field projected in: id=%v age=%v",
			values2["id"], values2["age"])
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
