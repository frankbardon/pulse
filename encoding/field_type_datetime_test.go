package encoding

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestFieldTypeDateTime_TypeByte pins the on-wire type byte. datetime is
// an ADDITIVE extension appended after set_u64 — if this value ever moves,
// every .pulse file written since the type landed silently mis-decodes.
func TestFieldTypeDateTime_TypeByte(t *testing.T) {
	if got := byte(FieldTypeDateTime); got != 17 {
		t.Fatalf("FieldTypeDateTime type byte = %d, want 17", got)
	}
	if !FieldTypeDateTime.IsKnown() {
		t.Error("FieldTypeDateTime.IsKnown() = false, want true")
	}
	// The first byte past the registry sentinel is always unknown. The
	// bound is derived, not literal: set_u128 / set_u256 were appended at
	// bytes 18 and 19 after this test was written, and a literal 18 here
	// would have asserted a registered type was unknown.
	if FieldType(fieldTypeCount).IsKnown() {
		t.Errorf("FieldType(%d).IsKnown() = true, want false (sentinel)", fieldTypeCount)
	}
}

// TestFieldTypeDateTime_Predicates asserts the full predicate family for
// the new type in one table so a future predicate addition that forgets
// datetime shows up here.
func TestFieldTypeDateTime_Predicates(t *testing.T) {
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"ByteSize==8", FieldTypeDateTime.ByteSize() == 8, true},
		{"HasDictionary", FieldTypeDateTime.HasDictionary(), false},
		{"IsBitPacked", FieldTypeDateTime.IsBitPacked(), false},
		{"IsCategorical", FieldTypeDateTime.IsCategorical(), false},
		{"IsSet", FieldTypeDateTime.IsSet(), false},
		{"IsDecimal", FieldTypeDateTime.IsDecimal(), false},
		{"IsNumeric", FieldTypeDateTime.IsNumeric(), false},
		{"IsNumericForAnalytics", FieldTypeDateTime.IsNumericForAnalytics(), true},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("datetime %s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if got := FieldTypeDateTime.MaxDictEntries(); got != 0 {
		t.Errorf("datetime MaxDictEntries() = %d, want 0", got)
	}
	if got := FieldTypeDateTime.MaxCategoricalEntries(); got != 0 {
		t.Errorf("datetime MaxCategoricalEntries() = %d, want 0", got)
	}
	if got := FieldTypeDateTime.MaxSetEntries(); got != 0 {
		t.Errorf("datetime MaxSetEntries() = %d, want 0", got)
	}
}

// TestFieldTypeDateTime_NameRoundTrip covers String/ParseFieldType.
func TestFieldTypeDateTime_NameRoundTrip(t *testing.T) {
	if got := FieldTypeDateTime.String(); got != "datetime" {
		t.Fatalf("String() = %q, want %q", got, "datetime")
	}
	ft, ok := ParseFieldType("datetime")
	if !ok {
		t.Fatal("ParseFieldType(\"datetime\") returned ok=false")
	}
	if ft != FieldTypeDateTime {
		t.Fatalf("ParseFieldType(\"datetime\") = %d, want %d", ft, FieldTypeDateTime)
	}
	// Every registered type must survive String → ParseFieldType.
	for ft := FieldType(0); ft < fieldTypeCount; ft++ {
		back, ok := ParseFieldType(ft.String())
		if !ok || back != ft {
			t.Errorf("round trip failed for %d (%q): got %d ok=%v", ft, ft.String(), back, ok)
		}
	}
}

// TestFieldTypeDateTime_FieldValueRoundTrip exercises the raw
// WriteFieldValue / ReadFieldValue codec pair across the epoch-seconds
// range, including the full 64-bit span (no float coercion here).
func TestFieldTypeDateTime_FieldValueRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		val  uint64
	}{
		{"epoch", 0},
		{"1970-01-02", 86400},
		{"2024-01-01T00:00:00Z", 1704067200},
		{"max int32 seconds", math.MaxInt32},
		{"max uint64", math.MaxUint64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFieldValue(&buf, FieldTypeDateTime, tc.val); err != nil {
				t.Fatalf("WriteFieldValue: %v", err)
			}
			if buf.Len() != 8 {
				t.Fatalf("wrote %d bytes, want 8", buf.Len())
			}
			// Little-endian, same as u64.
			if got := binary.LittleEndian.Uint64(buf.Bytes()); got != tc.val {
				t.Fatalf("on-wire bytes decode to %d, want %d", got, tc.val)
			}
			got, err := ReadFieldValue(bytes.NewReader(buf.Bytes()), FieldTypeDateTime)
			if err != nil {
				t.Fatalf("ReadFieldValue: %v", err)
			}
			if got != tc.val {
				t.Fatalf("round trip = %d, want %d", got, tc.val)
			}
		})
	}
}

// datetimeSchema builds a mixed schema with a datetime column so stride
// arithmetic is exercised alongside neighbouring fixed-width fields.
func datetimeSchema(nullable bool) *Schema {
	return &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0},
			{Name: "seen_at", Type: FieldTypeDateTime, ByteOffset: 4, Nullable: nullable},
			{Name: "day", Type: FieldTypeDate, ByteOffset: 12},
		},
	}
}

// TestFieldTypeDateTime_SchemaRoundTrip asserts the schema block survives
// a write/read cycle and that stride arithmetic picks up the 8 bytes.
func TestFieldTypeDateTime_SchemaRoundTrip(t *testing.T) {
	orig := datetimeSchema(false)
	if got, want := orig.RecordByteSize(), 4+8+4; got != want {
		t.Fatalf("RecordByteSize() = %d, want %d", got, want)
	}

	var buf bytes.Buffer
	if err := WriteSchema(&buf, orig); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	got, err := ReadSchema(bytes.NewReader(buf.Bytes()), FormatVersion)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(got.Fields) != 3 {
		t.Fatalf("field count = %d, want 3", len(got.Fields))
	}
	if got.Fields[1].Type != FieldTypeDateTime {
		t.Fatalf("seen_at type = %s, want datetime", got.Fields[1].Type)
	}
	if got.RecordByteSize() != orig.RecordByteSize() {
		t.Fatalf("stride drifted across round trip: %d vs %d",
			got.RecordByteSize(), orig.RecordByteSize())
	}
}

// TestReadSchema_RejectsTypeByteAboveDateTime is the forward-compat guard:
// a reader that predates a type byte must reject it with ENCODING_INVALID
// rather than mis-parse the record stream. Byte 18 stands in for what an
// older binary saw when handed datetime's byte 17.
func TestReadSchema_RejectsTypeByteAboveDateTime(t *testing.T) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint16(1)); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte(byte(fieldTypeCount)) // 18 — one past the sentinel
	buf.WriteByte(0)                    // nullable flag
	if err := binary.Write(&buf, binary.LittleEndian, uint16(1)); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte('x')
	if err := binary.Write(&buf, binary.LittleEndian, uint32(0)); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte(0)
	if err := binary.Write(&buf, binary.LittleEndian, uint16(0)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint16(0)); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadSchema(&buf, FormatVersion); err == nil {
		t.Fatal("expected ReadSchema to reject a type byte past the sentinel")
	} else if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("expected ENCODING_INVALID, got %v", err)
	}
}

// TestPreDateTimeTypeBytesUnchanged is the backwards-compatibility pin:
// appending datetime must not have moved any pre-existing type byte, so a
// .pulse file written before this change decodes byte-identically.
func TestPreDateTimeTypeBytesUnchanged(t *testing.T) {
	legacy := []struct {
		ft   FieldType
		byte byte
		name string
	}{
		{FieldTypeU8, 0, "u8"},
		{FieldTypeU16, 1, "u16"},
		{FieldTypeU32, 2, "u32"},
		{FieldTypeU64, 3, "u64"},
		{FieldTypeF32, 4, "f32"},
		{FieldTypeF64, 5, "f64"},
		{FieldTypeU4, 6, "u4"},
		{FieldTypeDate, 7, "date"},
		{FieldTypePackedBool, 8, "packed_bool"},
		{FieldTypeCategoricalU8, 9, "categorical_u8"},
		{FieldTypeCategoricalU16, 10, "categorical_u16"},
		{FieldTypeCategoricalU32, 11, "categorical_u32"},
		{FieldTypeDecimal128, 12, "decimal128"},
		{FieldTypeSetU8, 13, "set_u8"},
		{FieldTypeSetU16, 14, "set_u16"},
		{FieldTypeSetU32, 15, "set_u32"},
		{FieldTypeSetU64, 16, "set_u64"},
	}
	for _, tc := range legacy {
		if byte(tc.ft) != tc.byte {
			t.Errorf("%s moved to byte %d, want %d", tc.name, byte(tc.ft), tc.byte)
		}
		if tc.ft.String() != tc.name {
			t.Errorf("byte %d String() = %q, want %q", tc.byte, tc.ft.String(), tc.name)
		}
	}
}

// TestPreDateTimeCohortBytesIdentical writes a cohort that uses only
// pre-datetime types and asserts the encoded bytes match the exact
// sequence produced before byte 17 existed.
func TestPreDateTimeCohortBytesIdentical(t *testing.T) {
	s := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32, ByteOffset: 0},
			{Name: "day", Type: FieldTypeDate, ByteOffset: 4},
		},
	}

	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := WriteSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	if err := WriteFieldValue(&buf, FieldTypeU32, 7); err != nil {
		t.Fatal(err)
	}
	if err := WriteFieldValue(&buf, FieldTypeDate, 19723); err != nil {
		t.Fatal(err)
	}

	want := []byte{
		// 9-byte header: magic + format version.
		'P', 'U', 'L', 'S', 'E', 0x00, 0x00, 0x00, 0x01,
		// field_count = 2
		0x02, 0x00,
		// field 0: type u32 (2), nullable 0, name_len 2, "id",
		// byte_offset 0, bit_pos 0, csv_col 0, desc_len 0
		0x02, 0x00, 0x02, 0x00, 'i', 'd',
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		// field 1: type date (7), nullable 0, name_len 3, "day",
		// byte_offset 4, bit_pos 0, csv_col 0, desc_len 0
		0x07, 0x00, 0x03, 0x00, 'd', 'a', 'y',
		0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		// record: id=7 (u32 LE), day=19723 (u32 LE)
		0x07, 0x00, 0x00, 0x00,
		0x0B, 0x4D, 0x00, 0x00,
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("pre-datetime cohort bytes drifted:\n got=% x\nwant=% x", buf.Bytes(), want)
	}

	// And it still reads back.
	r := bytes.NewReader(buf.Bytes())
	pulseVersion, err := ReadHeader(r)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	got, err := ReadSchema(r, pulseVersion)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(got.Fields) != 2 || got.Fields[1].Type != FieldTypeDate {
		t.Fatalf("schema mis-decoded: %+v", got.Fields)
	}
}
