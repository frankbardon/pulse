package encoding

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestRecordCodec_AllTypes(t *testing.T) {
	cases := []struct {
		name string
		ft   FieldType
		val  uint64 // raw bits
	}{
		{"u8", FieldTypeU8, 42},
		{"u16", FieldTypeU16, 1000},
		{"u32", FieldTypeU32, 100000},
		{"u64", FieldTypeU64, 1<<48 | 7},
		{"f32", FieldTypeF32, uint64(math.Float32bits(3.14))},
		{"f64", FieldTypeF64, math.Float64bits(2.71828)},
		{"nullable_u8", FieldTypeNullableU8, 200},
		{"nullable_u16", FieldTypeNullableU16, 50000},
		{"date", FieldTypeDate, 19500}, // days since epoch
		{"categorical_u8", FieldTypeCategoricalU8, 5},
		{"categorical_u16", FieldTypeCategoricalU16, 300},
		{"categorical_u32", FieldTypeCategoricalU32, 70000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFieldValue(&buf, tc.ft, tc.val); err != nil {
				t.Fatalf("WriteFieldValue: %v", err)
			}

			got, err := ReadFieldValue(bytes.NewReader(buf.Bytes()), tc.ft)
			if err != nil {
				t.Fatalf("ReadFieldValue: %v", err)
			}
			if got != tc.val {
				t.Errorf("round-trip = %d, want %d", got, tc.val)
			}
		})
	}
}

func TestRecordCodec_PackedBool(t *testing.T) {
	// PackedBool: single bit. Write a byte with bit set, read back.
	var buf bytes.Buffer
	buf.WriteByte(0b00000101) // bits 0 and 2 set

	val, err := ReadBit(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("ReadBit(0): %v", err)
	}
	if !val {
		t.Error("bit 0 should be set")
	}

	val, err = ReadBit(bytes.NewReader(buf.Bytes()), 1)
	if err != nil {
		t.Fatalf("ReadBit(1): %v", err)
	}
	if val {
		t.Error("bit 1 should be clear")
	}

	val, err = ReadBit(bytes.NewReader(buf.Bytes()), 2)
	if err != nil {
		t.Fatalf("ReadBit(2): %v", err)
	}
	if !val {
		t.Error("bit 2 should be set")
	}
}

func TestRecordCodec_NullableBool(t *testing.T) {
	// NullableBool uses 2 bits: presence + value.
	var buf bytes.Buffer
	buf.WriteByte(0b00000011) // bits 0=present, 1=value (true)

	present, err := ReadBit(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Error("should be present")
	}

	val, err := ReadBit(bytes.NewReader(buf.Bytes()), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !val {
		t.Error("value should be true")
	}
}

func TestRecordCodec_NullableU4(t *testing.T) {
	// NullableU4 uses a null sentinel (0x0F = null, 0x00..0x0E = value).
	// It is packed as a nibble.
	var buf bytes.Buffer
	buf.WriteByte(0x35) // low nibble=5, high nibble=3

	r := bytes.NewReader(buf.Bytes())
	low, err := ReadNibble(r, false) // low nibble
	if err != nil {
		t.Fatal(err)
	}
	if low != 5 {
		t.Errorf("low nibble = %d, want 5", low)
	}

	r.Reset(buf.Bytes())
	high, err := ReadNibble(r, true) // high nibble
	if err != nil {
		t.Fatal(err)
	}
	if high != 3 {
		t.Errorf("high nibble = %d, want 3", high)
	}
}

func TestRecordCodec_CategoricalField(t *testing.T) {
	// Encode a categorical ID and verify it reads back correctly.
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(42))

	got, err := ReadFieldValue(bytes.NewReader(buf.Bytes()), FieldTypeCategoricalU16)
	if err != nil {
		t.Fatalf("ReadFieldValue: %v", err)
	}
	if got != 42 {
		t.Errorf("categorical ID = %d, want 42", got)
	}
}

func TestRecordCodec_WriteFieldValue_InvalidType(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFieldValue(&buf, FieldType(200), 0)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestRecordCodec_ReadFieldValue_InvalidType(t *testing.T) {
	r := bytes.NewReader([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	_, err := ReadFieldValue(r, FieldType(200))
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestPulseFileRoundTrip(t *testing.T) {
	// Build a schema with mixed types.
	dict := NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")

	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0, Description: "Record identifier"},
			{Name: "age", Type: FieldTypeU8, ByteOffset: 4, CsvColumnIdx: 1, Description: "Patient age"},
			{Name: "score", Type: FieldTypeF64, ByteOffset: 5, CsvColumnIdx: 2, Description: ""},
			{Name: "color", Type: FieldTypeCategoricalU8, ByteOffset: 13, CsvColumnIdx: 3, Description: "Primary color", Dictionary: dict},
			{Name: "enrolled", Type: FieldTypeDate, ByteOffset: 14, CsvColumnIdx: 4, Description: "Enrollment date"},
		},
	}

	// Write the full .pulse file (header + schema + records).
	var buf bytes.Buffer

	// Write header.
	if err := WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Write schema.
	if err := WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	// Write some records.
	records := [][]uint64{
		{100, 25, math.Float64bits(98.6), 0, 19500},
		{101, 30, math.Float64bits(87.3), 1, 19501},
		{102, 45, math.Float64bits(92.1), 2, 19502},
	}
	for _, rec := range records {
		for i, field := range schema.Fields {
			if err := WriteFieldValue(&buf, field.Type, rec[i]); err != nil {
				t.Fatalf("WriteFieldValue field %s: %v", field.Name, err)
			}
		}
	}

	// Read it all back.
	reader := bytes.NewReader(buf.Bytes())

	// Read header.
	if err := ReadHeader(reader); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	// Read schema.
	schema2, err := ReadSchema(reader)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	if len(schema2.Fields) != len(schema.Fields) {
		t.Fatalf("schema field count = %d, want %d", len(schema2.Fields), len(schema.Fields))
	}

	for i, f := range schema2.Fields {
		orig := schema.Fields[i]
		if f.Name != orig.Name {
			t.Errorf("field[%d].Name = %q, want %q", i, f.Name, orig.Name)
		}
		if f.Type != orig.Type {
			t.Errorf("field[%d].Type = %v, want %v", i, f.Type, orig.Type)
		}
		if f.ByteOffset != orig.ByteOffset {
			t.Errorf("field[%d].ByteOffset = %d, want %d", i, f.ByteOffset, orig.ByteOffset)
		}
		if f.CsvColumnIdx != orig.CsvColumnIdx {
			t.Errorf("field[%d].CsvColumnIdx = %d, want %d", i, f.CsvColumnIdx, orig.CsvColumnIdx)
		}
		if f.Description != orig.Description {
			t.Errorf("field[%d].Description = %q, want %q", i, f.Description, orig.Description)
		}
		if orig.Type.IsCategorical() {
			if f.Dictionary == nil {
				t.Errorf("field[%d] should have dictionary", i)
			} else if f.Dictionary.Count() != orig.Dictionary.Count() {
				t.Errorf("field[%d] dict count = %d, want %d", i, f.Dictionary.Count(), orig.Dictionary.Count())
			}
		}
	}

	// Read records back.
	for ri, rec := range records {
		for fi, field := range schema2.Fields {
			got, err := ReadFieldValue(reader, field.Type)
			if err != nil {
				t.Fatalf("record[%d] field[%d] ReadFieldValue: %v", ri, fi, err)
			}
			if got != rec[fi] {
				t.Errorf("record[%d] field %s = %d, want %d", ri, field.Name, got, rec[fi])
			}
		}
	}
}

func TestPulseFileRoundTrip_EmptySchema(t *testing.T) {
	schema := &Schema{Fields: nil}

	var buf bytes.Buffer
	WriteHeader(&buf)
	if err := WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema empty: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	ReadHeader(reader)
	schema2, err := ReadSchema(reader)
	if err != nil {
		t.Fatalf("ReadSchema empty: %v", err)
	}
	if len(schema2.Fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(schema2.Fields))
	}
}
