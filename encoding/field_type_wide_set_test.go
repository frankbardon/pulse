package encoding

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestFieldTypeWideSet_TypeBytes pins the on-wire type bytes for the two
// wide set rungs. They are an ADDITIVE extension appended after datetime —
// if either value ever moves, every .pulse file written since the types
// landed silently mis-decodes, and a set_u128 read as a decimal128 (the
// same 16-byte width) would not even mis-align.
func TestFieldTypeWideSet_TypeBytes(t *testing.T) {
	if got := byte(FieldTypeSetU128); got != 18 {
		t.Fatalf("FieldTypeSetU128 type byte = %d, want 18", got)
	}
	if got := byte(FieldTypeSetU256); got != 19 {
		t.Fatalf("FieldTypeSetU256 type byte = %d, want 19", got)
	}
	// datetime must not have moved to make room.
	if got := byte(FieldTypeDateTime); got != 17 {
		t.Fatalf("FieldTypeDateTime type byte = %d, want 17", got)
	}
	if !FieldTypeSetU128.IsKnown() || !FieldTypeSetU256.IsKnown() {
		t.Error("wide set types must report IsKnown() = true")
	}
	if FieldType(fieldTypeCount).IsKnown() {
		t.Errorf("FieldType(%d).IsKnown() = true, want false (sentinel)", fieldTypeCount)
	}
}

// TestFieldTypeWideSet_Predicates asserts the full predicate family for
// both new types in one table, so a future predicate addition that forgets
// the wide rungs shows up here.
func TestFieldTypeWideSet_Predicates(t *testing.T) {
	cases := []struct {
		ft           FieldType
		wantBytes    int
		wantMaxEntry uint32
	}{
		{FieldTypeSetU128, 16, 128},
		{FieldTypeSetU256, 32, 256},
	}
	for _, tc := range cases {
		t.Run(tc.ft.String(), func(t *testing.T) {
			preds := []struct {
				name string
				got  bool
				want bool
			}{
				{"IsSet", tc.ft.IsSet(), true},
				{"IsWideSet", tc.ft.IsWideSet(), true},
				{"HasDictionary", tc.ft.HasDictionary(), true},
				{"IsBitPacked", tc.ft.IsBitPacked(), false},
				{"IsCategorical", tc.ft.IsCategorical(), false},
				{"IsDecimal", tc.ft.IsDecimal(), false},
				{"IsNumeric", tc.ft.IsNumeric(), false},
				{"IsNumericForAnalytics", tc.ft.IsNumericForAnalytics(), false},
			}
			for _, p := range preds {
				if p.got != p.want {
					t.Errorf("%s.%s = %v, want %v", tc.ft, p.name, p.got, p.want)
				}
			}
			if got := tc.ft.ByteSize(); got != tc.wantBytes {
				t.Errorf("%s.ByteSize() = %d, want %d", tc.ft, got, tc.wantBytes)
			}
			if got := tc.ft.MaxSetEntries(); got != tc.wantMaxEntry {
				t.Errorf("%s.MaxSetEntries() = %d, want %d", tc.ft, got, tc.wantMaxEntry)
			}
			// Dictionary capacity is the bitmask width: cohesion.go's
			// union-merge width check reads MaxDictEntries, not MaxSetEntries.
			if got := tc.ft.MaxDictEntries(); got != tc.wantMaxEntry {
				t.Errorf("%s.MaxDictEntries() = %d, want %d", tc.ft, got, tc.wantMaxEntry)
			}
			if got := tc.ft.MaxCategoricalEntries(); got != 0 {
				t.Errorf("%s.MaxCategoricalEntries() = %d, want 0", tc.ft, got)
			}
		})
	}
}

// TestFieldType_IsWideSet_NarrowRungsExcluded pins the other half of the
// IsWideSet split: every narrow rung (and every non-set type) must answer
// false, or the uint64 value API would start refusing values it can
// carry perfectly well.
func TestFieldType_IsWideSet_NarrowRungsExcluded(t *testing.T) {
	wide := map[FieldType]bool{FieldTypeSetU128: true, FieldTypeSetU256: true}
	for ft := FieldType(0); ft < fieldTypeCount; ft++ {
		if got := ft.IsWideSet(); got != wide[ft] {
			t.Errorf("%s.IsWideSet() = %v, want %v", ft, got, wide[ft])
		}
	}
}

// TestFieldTypeWideSet_NameRoundTrip covers the String / ParseFieldType
// bijection for the two new names. internal/cli falls back to f64 on an
// unknown name, so a missing ParseFieldType case fails SILENTLY there.
func TestFieldTypeWideSet_NameRoundTrip(t *testing.T) {
	cases := []struct {
		ft   FieldType
		name string
	}{
		{FieldTypeSetU128, "set_u128"},
		{FieldTypeSetU256, "set_u256"},
	}
	for _, tc := range cases {
		if got := tc.ft.String(); got != tc.name {
			t.Errorf("FieldType(%d).String() = %q, want %q", tc.ft, got, tc.name)
		}
		back, ok := ParseFieldType(tc.name)
		if !ok {
			t.Errorf("ParseFieldType(%q) returned ok=false", tc.name)
			continue
		}
		if back != tc.ft {
			t.Errorf("ParseFieldType(%q) = %d, want %d", tc.name, back, tc.ft)
		}
	}
}

// TestFieldValue_WideSetRejected mirrors the decimal128 arms: the uint64
// value API cannot carry a 128- or 256-bit mask, so both directions must
// refuse with ENCODING_TYPE_MISMATCH and point at the wide-set API rather
// than truncate the mask to 64 bits.
func TestFieldValue_WideSetRejected(t *testing.T) {
	for _, ft := range []FieldType{FieldTypeSetU128, FieldTypeSetU256} {
		t.Run(ft.String(), func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteFieldValue(&buf, ft, 0xFF)
			if err == nil {
				t.Fatalf("WriteFieldValue(%s) returned nil error", ft)
			}
			if !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
				t.Errorf("WriteFieldValue(%s): want ENCODING_TYPE_MISMATCH, got %v", ft, err)
			}
			if !strings.Contains(err.Error(), "wide-set") {
				t.Errorf("WriteFieldValue(%s) message %q does not direct the caller to the wide-set API", ft, err.Error())
			}
			if buf.Len() != 0 {
				t.Errorf("WriteFieldValue(%s) wrote %d bytes on a rejection", ft, buf.Len())
			}

			_, rerr := ReadFieldValue(bytes.NewReader(make([]byte, 32)), ft)
			if rerr == nil {
				t.Fatalf("ReadFieldValue(%s) returned nil error", ft)
			}
			if !errors.HasCode(rerr, errors.ENCODING_TYPE_MISMATCH) {
				t.Errorf("ReadFieldValue(%s): want ENCODING_TYPE_MISMATCH, got %v", ft, rerr)
			}
			if !strings.Contains(rerr.Error(), "wide-set") {
				t.Errorf("ReadFieldValue(%s) message %q does not direct the caller to the wide-set API", ft, rerr.Error())
			}
		})
	}
}

// wideSetDict builds an n-entry dictionary for a wide set field.
func wideSetDict(t *testing.T, n int) *Dictionary {
	t.Helper()
	d := NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := d.Add("opt" + string(rune('a'+i%26)) + string(rune('0'+i/26))); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	return d
}

// TestSchema_WideSetRoundTrip is the foundation assertion: a schema
// carrying a wide set column survives WriteSchema → ReadSchema with the
// name, type byte, nullable flag, byte offset, description and inline
// dictionary all intact.
func TestSchema_WideSetRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		ft       FieldType
		typeByte byte
		entries  int
	}{
		{FieldTypeSetU128, 18, 70},
		{FieldTypeSetU256, 19, 130},
	} {
		t.Run(tc.ft.String(), func(t *testing.T) {
			in := &Schema{Fields: []Field{
				{Name: "id", Type: FieldTypeU32, ByteOffset: 0},
				{
					Name:        "symptoms",
					Type:        tc.ft,
					Nullable:    true,
					ByteOffset:  4,
					Description: "multi-select symptom checklist",
					Dictionary:  wideSetDict(t, tc.entries),
				},
			}}

			var buf bytes.Buffer
			if err := WriteSchema(&buf, in); err != nil {
				t.Fatalf("WriteSchema: %v", err)
			}
			// The type byte lands on the wire verbatim.
			raw := buf.Bytes()
			if !bytes.Contains(raw, append([]byte{tc.typeByte, 0x01}, byte(len("symptoms")), 0x00)) {
				t.Errorf("serialized schema does not carry type byte %d followed by nullable=1", tc.typeByte)
			}

			out, err := ReadSchema(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("ReadSchema: %v", err)
			}
			if len(out.Fields) != 2 {
				t.Fatalf("read %d fields, want 2", len(out.Fields))
			}
			got := out.Fields[1]
			if got.Name != "symptoms" {
				t.Errorf("name = %q, want %q", got.Name, "symptoms")
			}
			if got.Type != tc.ft {
				t.Errorf("type = %d (%s), want %d (%s)", got.Type, got.Type, tc.ft, tc.ft)
			}
			if !got.Nullable {
				t.Error("nullable flag lost on round trip")
			}
			if got.ByteOffset != 4 {
				t.Errorf("byte offset = %d, want 4", got.ByteOffset)
			}
			if got.Description != "multi-select symptom checklist" {
				t.Errorf("description = %q", got.Description)
			}
			if got.Dictionary == nil || got.Dictionary.Count() != tc.entries {
				t.Fatalf("dictionary lost: %+v", got.Dictionary)
			}
			if _, ok := out.SetField("symptoms"); !ok {
				t.Error("SetField() does not resolve the wide set column")
			}
		})
	}
}

// TestSchema_RecordByteSize_WideSet pins the stride contribution of each
// wide rung, including alongside bit-packed neighbours (which consume a
// whole byte each) and a trailing null bitmap. Stride is a pure function
// of the type byte — a wrong ByteSize() silently corrupts every offset
// downstream of the column.
func TestSchema_RecordByteSize_WideSet(t *testing.T) {
	cases := []struct {
		name   string
		fields []Field
		want   int
	}{
		{
			name:   "set_u128 alone",
			fields: []Field{{Name: "a", Type: FieldTypeSetU128}},
			want:   16,
		},
		{
			name:   "set_u256 alone",
			fields: []Field{{Name: "a", Type: FieldTypeSetU256}},
			want:   32,
		},
		{
			name: "wide sets beside narrow ones",
			fields: []Field{
				{Name: "n8", Type: FieldTypeSetU8},
				{Name: "n64", Type: FieldTypeSetU64},
				{Name: "w128", Type: FieldTypeSetU128},
				{Name: "w256", Type: FieldTypeSetU256},
			},
			want: 1 + 8 + 16 + 32,
		},
		{
			name: "bit-packed neighbours",
			fields: []Field{
				{Name: "flag", Type: FieldTypePackedBool, BitPosition: 0},
				{Name: "w128", Type: FieldTypeSetU128},
				{Name: "nib", Type: FieldTypeU4},
				{Name: "w256", Type: FieldTypeSetU256},
			},
			want: 1 + 16 + 1 + 32,
		},
		{
			name: "nullable wide set adds bitmap only",
			fields: []Field{
				{Name: "id", Type: FieldTypeU32},
				{Name: "w128", Type: FieldTypeSetU128, Nullable: true},
			},
			// 4 + 16 payload, plus ceil(2/8) = 1 bitmap byte. No in-band
			// sentinel is reserved for the null.
			want: 4 + 16 + 1,
		},
		{
			name: "nullable set_u256 adds bitmap only",
			fields: []Field{
				{Name: "id", Type: FieldTypeU32},
				{Name: "w256", Type: FieldTypeSetU256, Nullable: true},
			},
			want: 4 + 32 + 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Schema{Fields: tc.fields}
			if got := s.RecordByteSize(); got != tc.want {
				t.Errorf("RecordByteSize() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestWideSetNull_RidesBitmapOnly asserts the decimal128-style contract:
// a null wide set is recorded in the per-record null bitmap ONLY, the
// payload bytes stay in place, and an all-zero mask with the bitmap bit
// clear is a valid "no selection" — distinct from null.
func TestWideSetNull_RidesBitmapOnly(t *testing.T) {
	for _, tc := range []struct {
		ft    FieldType
		width int
	}{
		{FieldTypeSetU128, 16},
		{FieldTypeSetU256, 32},
	} {
		t.Run(tc.ft.String(), func(t *testing.T) {
			s := &Schema{Fields: []Field{
				{Name: "tags", Type: tc.ft, Nullable: true, Dictionary: wideSetDict(t, 3)},
				{Name: "id", Type: FieldTypeU8, ByteOffset: tc.width},
			}}
			bitmapBytes := s.BitmapByteSize()
			if bitmapBytes != 1 {
				t.Fatalf("BitmapByteSize() = %d, want 1", bitmapBytes)
			}
			if got, want := s.RecordByteSize(), tc.width+1+bitmapBytes; got != want {
				t.Fatalf("stride = %d, want %d (no in-band null sentinel)", got, want)
			}

			// Row 0: empty mask, NOT null.
			// Row 1: empty mask, null via the bitmap.
			var buf bytes.Buffer
			buf.Write(make([]byte, tc.width)) // empty mask
			buf.WriteByte(7)                  // id
			buf.Write([]byte{0x00})           // bitmap: nothing null

			nullBitmap := make([]byte, bitmapBytes)
			BitmapSetNull(nullBitmap, 0)
			buf.Write(make([]byte, tc.width))
			buf.WriteByte(8)
			buf.Write(nullBitmap)

			raw := buf.Bytes()
			stride := s.RecordByteSize()
			if len(raw) != 2*stride {
				t.Fatalf("hand-built payload is %d bytes, stride says %d", len(raw), 2*stride)
			}

			bm0, err := ReadBitmap(bytes.NewReader(raw[stride-bitmapBytes:stride]), bitmapBytes)
			if err != nil {
				t.Fatalf("ReadBitmap row 0: %v", err)
			}
			if BitmapIsNull(bm0, 0) {
				t.Error("row 0: empty mask must not read as null")
			}
			bm1, err := ReadBitmap(bytes.NewReader(raw[2*stride-bitmapBytes:2*stride]), bitmapBytes)
			if err != nil {
				t.Fatalf("ReadBitmap row 1: %v", err)
			}
			if !BitmapIsNull(bm1, 0) {
				t.Error("row 1: null wide set must read as null from the bitmap")
			}

			// Record count derives from the same stride.
			count, trailing, ok := s.RecordCountForPayload(int64(len(raw)))
			if !ok || count != 2 || trailing != 0 {
				t.Errorf("RecordCountForPayload = (%d, %d, %v), want (2, 0, true)", count, trailing, ok)
			}
		})
	}
}

// TestReadSchema_RejectsTypeByteAboveWideSets keeps the tolerated type
// byte range from widening by accident: the first byte past the sentinel
// is still ENCODING_INVALID, not a silently accepted column.
func TestReadSchema_RejectsTypeByteAboveWideSets(t *testing.T) {
	for _, b := range []byte{byte(fieldTypeCount), 200, 250} {
		var buf bytes.Buffer
		buf.Write([]byte{0x01, 0x00}) // field_count = 1
		buf.WriteByte(b)              // type byte
		buf.WriteByte(0)              // nullable
		buf.Write([]byte{0x01, 0x00}) // name_len = 1
		buf.WriteByte('x')
		buf.Write([]byte{0, 0, 0, 0}) // byte offset
		buf.WriteByte(0)              // bit position
		buf.Write([]byte{0, 0})       // csv column index
		buf.Write([]byte{0, 0})       // description length

		if _, err := ReadSchema(bytes.NewReader(buf.Bytes())); err == nil {
			t.Errorf("type byte %d: expected rejection", b)
		} else if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("type byte %d: want ENCODING_INVALID, got %v", b, err)
		}
	}
}
