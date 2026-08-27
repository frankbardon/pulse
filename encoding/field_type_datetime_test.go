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
	if FieldType(18).IsKnown() {
		t.Error("FieldType(18).IsKnown() = true, want false (sentinel is 18)")
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
	got, err := ReadSchema(bytes.NewReader(buf.Bytes()))
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

// writeDateTimeRecords encodes rows for datetimeSchema. nullRow, when >= 0,
// marks the seen_at column null on that row via the bitmap.
func writeDateTimeRecords(t *testing.T, s *Schema, rows [][3]uint64, nullRow int) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i, row := range rows {
		if err := WriteFieldValue(&buf, FieldTypeU32, row[0]); err != nil {
			t.Fatal(err)
		}
		if err := WriteFieldValue(&buf, FieldTypeDateTime, row[1]); err != nil {
			t.Fatal(err)
		}
		if err := WriteFieldValue(&buf, FieldTypeDate, row[2]); err != nil {
			t.Fatal(err)
		}
		if bm := s.BitmapByteSize(); bm > 0 {
			bitmap := make([]byte, bm)
			if i == nullRow {
				BitmapSetNull(bitmap, 1)
			}
			if err := WriteBitmap(&buf, bitmap); err != nil {
				t.Fatal(err)
			}
		}
	}
	return buf.Bytes()
}

// TestFieldTypeDateTime_RecordReadPaths asserts every decode path
// (per-field readRecord, the buffer-once reuse fast path, and the
// projected plan path) agrees on a datetime column.
func TestFieldTypeDateTime_RecordReadPaths(t *testing.T) {
	s := datetimeSchema(false)
	rows := [][3]uint64{
		{1, 0, 0},
		{2, 1704067200, 19723},
		{3, math.MaxInt32, 65535},
	}
	raw := writeDateTimeRecords(t, s, rows, -1)

	rr := NewRecordReader(bytes.NewReader(raw), s)
	for i, want := range rows {
		values := make(map[string]float64)
		nulls := make(map[string]bool)
		if err := rr.ReadRecord(values, nulls); err != nil {
			t.Fatalf("row %d: ReadRecord: %v", i, err)
		}
		if got := values["seen_at"]; got != float64(want[1]) {
			t.Errorf("row %d: seen_at = %v, want %v", i, got, float64(want[1]))
		}
		if got := values["day"]; got != float64(want[2]) {
			t.Errorf("row %d: day = %v, want %v", i, got, float64(want[2]))
		}
	}

	// The reuse fast path must agree with the reference decoder byte for byte.
	stride := s.RecordByteSize()
	for i := range rows {
		compareReusedAgainstReference(t, s, raw[i*stride:(i+1)*stride], "datetime row")
	}
}

// TestFieldTypeDateTime_NullBitmap asserts nullability is orthogonal:
// a null datetime rides the shared per-record bitmap with no in-band
// sentinel value.
func TestFieldTypeDateTime_NullBitmap(t *testing.T) {
	s := datetimeSchema(true)
	if !s.HasBitmap() {
		t.Fatal("schema with nullable datetime must report HasBitmap()")
	}
	if got, want := s.RecordByteSize(), 4+8+4+1; got != want {
		t.Fatalf("RecordByteSize() = %d, want %d", got, want)
	}

	rows := [][3]uint64{
		{1, 1704067200, 19723},
		{2, 1704067200, 19724}, // row 1 is the null one; value bytes stay non-zero
	}
	raw := writeDateTimeRecords(t, s, rows, 1)

	rr := NewRecordReader(bytes.NewReader(raw), s)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	if err := rr.ReadRecord(values, nulls); err != nil {
		t.Fatalf("row 0: %v", err)
	}
	if nulls["seen_at"] {
		t.Error("row 0: seen_at unexpectedly null")
	}
	if err := rr.ReadRecord(values, nulls); err != nil {
		t.Fatalf("row 1: %v", err)
	}
	if !nulls["seen_at"] {
		t.Error("row 1: seen_at should be null via the bitmap")
	}
	if values["seen_at"] != 0 {
		t.Errorf("row 1: null datetime should surface as 0, got %v", values["seen_at"])
	}

	stride := s.RecordByteSize()
	compareReusedAgainstReference(t, s, raw[stride:2*stride], "null datetime row")
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

	if _, err := ReadSchema(&buf); err == nil {
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
	if err := ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	got, err := ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(got.Fields) != 2 || got.Fields[1].Type != FieldTypeDate {
		t.Fatalf("schema mis-decoded: %+v", got.Fields)
	}
}
