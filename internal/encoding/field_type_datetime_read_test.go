package encoding

import (
	"bytes"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// writeDateTimeRecords encodes rows for datetimeSchema. nullRow, when >= 0,
// marks the seen_at column null on that row via the bitmap.
func writeDateTimeRecords(t *testing.T, s *encoding.Schema, rows [][3]uint64, nullRow int) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i, row := range rows {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, row[0]); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeDateTime, row[1]); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeDate, row[2]); err != nil {
			t.Fatal(err)
		}
		if bm := s.BitmapByteSize(); bm > 0 {
			bitmap := make([]byte, bm)
			if i == nullRow {
				encoding.BitmapSetNull(bitmap, 1)
			}
			if err := encoding.WriteBitmap(&buf, bitmap); err != nil {
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

func datetimeSchema(nullable bool) *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
			{Name: "seen_at", Type: encoding.FieldTypeDateTime, ByteOffset: 4, Nullable: nullable},
			{Name: "day", Type: encoding.FieldTypeDate, ByteOffset: 12},
		},
	}
}
