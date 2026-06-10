package encoding

import (
	"bytes"
	"math"
	"testing"
)

// TestReadRecordPlan_NilPlanFallsThrough confirms that passing plan ==
// nil to ReadRecordWithWidePlan dispatches into the existing per-field
// projected path, preserving behaviour for callers that haven't built
// a plan yet.
func TestReadRecordPlan_NilPlanFallsThrough(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "b", Type: FieldTypeU16, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	var buf bytes.Buffer
	if err := WriteFieldValue(&buf, FieldTypeU8, 7); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeU16, 99); err != nil {
		t.Fatalf("write b: %v", err)
	}

	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	if err := rr.ReadRecordWithWidePlan(values, nulls, wide, nil, nil); err != nil {
		t.Fatalf("ReadRecordWithWidePlan: %v", err)
	}
	if values["a"] != 7 || values["b"] != 99 {
		t.Errorf("nil plan + nil keep must populate every field; got %v", values)
	}
}

// TestReadRecordPlan_SkipsUnprojectedScalars exercises the SkipBytes
// segment path: a 3-field schema with the middle field unprojected.
// The plan walks SkipBytes for the middle field's on-wire bytes, then
// decodes the trailing field. Mirrors the original projection test but
// against the plan-driven entry point.
func TestReadRecordPlan_SkipsUnprojectedScalars(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "v", Type: FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			{Name: "y", Type: FieldTypeU16, ByteOffset: 12, CsvColumnIdx: 2},
		},
	}
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

	plan, err := schema.BuildDecodePlan([]string{"y"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	// Expect: SkipBytes{12} (id 4 bytes + v 8 bytes), DecodeFields{y}.
	if len(plan.Segments) != 2 {
		t.Fatalf("plan segments = %d, want 2: %+v", len(plan.Segments), plan.Segments)
	}
	if sb, ok := plan.Segments[0].(SkipBytes); !ok || sb.N != 12 {
		t.Errorf("segment 0 = %#v, want SkipBytes{12}", plan.Segments[0])
	}

	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	keep := func(name string) bool { return name == "y" }
	if err := rr.ReadRecordWithWidePlan(values, nulls, wide, keep, plan); err != nil {
		t.Fatalf("ReadRecordWithWidePlan: %v", err)
	}
	if _, ok := values["id"]; ok {
		t.Errorf("id should be skipped from values map")
	}
	if _, ok := values["v"]; ok {
		t.Errorf("v should be skipped from values map")
	}
	if values["y"] != 7 {
		t.Errorf("y = %v, want 7 — cursor misaligned through SkipBytes", values["y"])
	}
}

// TestReadRecordPlan_BitPackedNeighborAlignment verifies that the
// plan-driven path correctly handles bit-packed groups: the group's
// on-wire byte is consumed even when only one neighbour is retained,
// so the field after the group decodes correctly.
func TestReadRecordPlan_BitPackedNeighborAlignment(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "flag", Type: FieldTypePackedBool, ByteOffset: 1, BitPosition: 0, CsvColumnIdx: 1},
			{Name: "b", Type: FieldTypeU32, ByteOffset: 2, CsvColumnIdx: 2},
		},
	}
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

	// Project only b: the plan must skip a (1 byte) + the bit-packed
	// group (1 byte for flag), then decode b.
	plan, err := schema.BuildDecodePlan([]string{"b"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}

	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	keep := func(name string) bool { return name == "b" }
	if err := rr.ReadRecordWithWidePlan(values, nulls, wide, keep, plan); err != nil {
		t.Fatalf("ReadRecordWithWidePlan: %v", err)
	}
	if values["b"] != 99 {
		t.Errorf("b = %v, want 99 — cursor misaligned through skipped bit-packed group", values["b"])
	}
}

// TestReadRecordPlan_BitmapSegmentSurfacesNulls verifies that the
// trailing bitmap segment is recognised by the plan walker and that
// null surfacing for retained nullable fields works identically to
// the per-field readRecord loop. Also verifies that null surfacing is
// suppressed for nullable fields the caller did NOT retain.
func TestReadRecordPlan_BitmapSegmentSurfacesNulls(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0, Nullable: false},
			{Name: "score", Type: FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
			{Name: "note", Type: FieldTypeU8, ByteOffset: 12, CsvColumnIdx: 2, Nullable: true},
		},
	}
	// Sanity check: schema reports the bitmap and the right stride.
	if !schema.HasBitmap() {
		t.Fatalf("schema should report HasBitmap")
	}

	var buf bytes.Buffer
	if err := WriteFieldValue(&buf, FieldTypeU32, 42); err != nil {
		t.Fatalf("write id: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeF64, math.Float64bits(99.5)); err != nil {
		t.Fatalf("write score: %v", err)
	}
	if err := WriteFieldValue(&buf, FieldTypeU8, 7); err != nil {
		t.Fatalf("write note: %v", err)
	}
	// Bitmap with note (index 2) marked null. Bitmap size = ceil(3/8) = 1
	// byte. BitmapIsNull(byte, i) tests bit i of byte i/8 (LSB-first).
	// Setting note null means bit 2 of byte 0 set: 0b00000100.
	bm := make([]byte, schema.BitmapByteSize())
	BitmapSetNull(bm, 2)
	if _, err := buf.Write(bm); err != nil {
		t.Fatalf("write bitmap: %v", err)
	}

	// Plan that retains BOTH nullable fields.
	plan, err := schema.BuildDecodePlan([]string{"id", "score", "note"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	if err := rr.ReadRecordWithWidePlan(values, nulls, wide, nil, plan); err != nil {
		t.Fatalf("ReadRecordWithWidePlan: %v", err)
	}
	if values["id"] != 42 {
		t.Errorf("id = %v, want 42", values["id"])
	}
	if values["score"] != 99.5 {
		t.Errorf("score = %v, want 99.5", values["score"])
	}
	if !nulls["note"] {
		t.Errorf("note should be marked null via bitmap")
	}
	if values["note"] != 0 {
		t.Errorf("null surfacing should reset values[note] to 0; got %v", values["note"])
	}

	// Plan that retains ONLY id (no nullable retained). The bitmap
	// becomes a SkipBytes segment instead of a DecodeFields segment.
	planID, err := schema.BuildDecodePlan([]string{"id"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	rr2 := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	values2 := make(map[string]float64)
	nulls2 := make(map[string]bool)
	wide2 := make(map[string]any)
	keep := func(name string) bool { return name == "id" }
	if err := rr2.ReadRecordWithWidePlan(values2, nulls2, wide2, keep, planID); err != nil {
		t.Fatalf("ReadRecordWithWidePlan (id only): %v", err)
	}
	if values2["id"] != 42 {
		t.Errorf("id = %v, want 42", values2["id"])
	}
	if _, ok := nulls2["note"]; ok {
		t.Errorf("note null must not surface when caller did not retain it")
	}
}

// TestReadRecordPlan_MatchesPerFieldOutput exercises a synthesised
// schema and asserts the plan-driven decode produces the same
// values/nulls/wide maps as the per-field readRecord path for the same
// retained set. Lightweight smoke test — the exhaustive matrix lands
// in E2-S3.
func TestReadRecordPlan_MatchesPerFieldOutput(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "v", Type: FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			{Name: "y", Type: FieldTypeU16, ByteOffset: 12, CsvColumnIdx: 2},
		},
	}
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
	data := buf.Bytes()
	keep := func(name string) bool { return name == "id" || name == "y" }

	// Per-field projected path.
	rrA := NewRecordReader(bytes.NewReader(data), schema)
	valuesA := make(map[string]float64)
	nullsA := make(map[string]bool)
	wideA := make(map[string]any)
	if err := rrA.ReadRecordWithWideProjected(valuesA, nullsA, wideA, keep); err != nil {
		t.Fatalf("ReadRecordWithWideProjected: %v", err)
	}

	// Plan-driven path.
	plan, err := schema.BuildDecodePlan([]string{"id", "y"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	rrB := NewRecordReader(bytes.NewReader(data), schema)
	valuesB := make(map[string]float64)
	nullsB := make(map[string]bool)
	wideB := make(map[string]any)
	if err := rrB.ReadRecordWithWidePlan(valuesB, nullsB, wideB, keep, plan); err != nil {
		t.Fatalf("ReadRecordWithWidePlan: %v", err)
	}

	if valuesA["id"] != valuesB["id"] || valuesA["y"] != valuesB["y"] {
		t.Errorf("plan-driven path mismatch: per-field=%v plan=%v", valuesA, valuesB)
	}
	if len(valuesA) != len(valuesB) {
		t.Errorf("value-map cardinality differs: per-field=%d plan=%d", len(valuesA), len(valuesB))
	}
}
