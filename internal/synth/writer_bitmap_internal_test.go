package synth

import (
	"bytes"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// recordBitmaps returns each record's trailing null bitmap, read
// straight off the payload bytes rather than through a RecordReader: a
// reader is free to ignore the bit of a non-nullable field, so a decode
// round-trip could not see the bit this pins.
func recordBitmaps(t *testing.T, data []byte) (*encoding.Schema, [][]byte) {
	t.Helper()
	r := bytes.NewReader(data)
	version, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r, version)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	payload, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	stride, bm := schema.RecordByteSize(), schema.BitmapByteSize()
	if bm == 0 || len(payload)%stride != 0 {
		t.Fatalf("payload %d bytes is not whole records of stride %d (bitmap %d)", len(payload), stride, bm)
	}
	var out [][]byte
	for off := 0; off < len(payload); off += stride {
		out = append(out, payload[off+stride-bm:off+stride])
	}
	return schema, out
}

// TestEncodeRow_NonNullableFieldBitStaysZero pins #457: the null mask
// may say a NON-nullable field is null (a null_together block naming
// one only warns, and generation itself writes any field's mask), but
// the file must not record a null the schema says cannot exist. Every
// field is marked null here; only the nullable one may set its bit, at
// its WRITTEN position (the helper is skipped and takes no bit).
func TestEncodeRow_NonNullableFieldBitStaysZero(t *testing.T) {
	t.Parallel()

	off := false
	spec := &Spec{RowCount: 1, Fields: []FieldSpec{
		{Name: "a", Type: "u8", Distribution: DistConstant, Params: map[string]any{"value": 1.0}},
		{Name: "h", Type: "u8", Nullable: true, Distribution: DistConstant, Params: map[string]any{"value": 1.0}, Output: &off},
		{Name: "b", Type: "u8", Nullable: true, Distribution: DistConstant, Params: map[string]any{"value": 1.0}},
		{Name: "c", Type: "u8", Distribution: DistConstant, Params: map[string]any{"value": 1.0}},
	}}
	schema, wfs, err := buildSchema(spec)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	row := map[string]any{"a": 1.0, "h": 1.0, "b": 1.0, "c": 1.0}
	mask := map[string]bool{"a": true, "h": true, "b": true, "c": true}
	var buf bytes.Buffer
	if err := encodeRow(&buf, wfs, row, mask, schema.BitmapByteSize()); err != nil {
		t.Fatalf("encodeRow: %v", err)
	}
	got := buf.Bytes()
	bitmap := got[len(got)-schema.BitmapByteSize():]
	// Written fields a, b, c: only b (written index 1) may be null.
	if want := []byte{0x02}; !bytes.Equal(bitmap, want) {
		t.Fatalf("bitmap = %08b, want %08b: a non-nullable field's bit must stay 0", bitmap, want)
	}
}

// TestSynth_NullTogetherOnNonNullableMemberWritesNoNullBit is the same
// pin end to end: a null_together block copying its gate's nulls onto a
// non-nullable member warns and generates, and the member's bit stays
// 0 in every record of the file.
func TestSynth_NullTogetherOnNonNullableMemberWritesNoNullBit(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 300, Fields: []FieldSpec{
		{Name: "gate", Type: "u8", Nullable: true, NullRate: 0.5, Distribution: DistUniform,
			Params: map[string]any{"min": 0.0, "max": 9.0}},
		{Name: "member", Type: "u8", Distribution: DistUniform, Params: map[string]any{"min": 0.0, "max": 9.0}},
	}, Rules: []RuleSpec{{NullTogether: []string{"gate", "member"}}}}
	data, _, err := SynthBytes(spec, Options{Seed: 5})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	schema, bitmaps := recordBitmaps(t, data)
	if schema.Fields[1].Nullable {
		t.Fatal("fixture: member must be non-nullable")
	}
	gateNulls := 0
	for i, bm := range bitmaps {
		if encoding.BitmapIsNull(bm, 0) {
			gateNulls++
		}
		if encoding.BitmapIsNull(bm, 1) {
			t.Fatalf("record %d: non-nullable member's null bit is set", i)
		}
	}
	if gateNulls == 0 {
		t.Fatal("fixture degenerate: the gate was never null, so the block never fired")
	}
}

// TestWriter_U4ClampsLikeTheWiderUnsigned pins FR-10: a u4 draw outside
// [0, 15] clamps to the nearest end, as u8..u64 do, instead of masking
// to the low nibble (16 -> 0, 17 -> 1, -1 -> 15).
func TestWriter_U4ClampsLikeTheWiderUnsigned(t *testing.T) {
	t.Parallel()

	field := &encoding.Field{Name: "x", Type: encoding.FieldTypeU4}
	for _, tc := range []struct {
		in   float64
		want byte
	}{
		{-3, 0}, {-0.6, 0}, {-0.4, 0}, {0, 0}, {7.4, 7}, {7.5, 8}, {15, 15},
		{15.4, 15}, {15.5, 15}, {16, 15}, {17, 15}, {31, 15}, {300, 15}, {1e12, 15},
	} {
		var buf bytes.Buffer
		if err := writeFieldValueForField(&buf, field, tc.in, false); err != nil {
			t.Fatalf("%v: %v", tc.in, err)
		}
		if got := buf.Bytes(); len(got) != 1 || got[0] != tc.want {
			t.Errorf("u4 %v encoded as %v, want %d", tc.in, got, tc.want)
		}
	}

	// End to end: a sampler whose whole support is above (or below)
	// the u4 range lands on the end, never wraps.
	spec := &Spec{RowCount: 200, Fields: []FieldSpec{
		{Name: "hi", Type: "u4", Distribution: DistUniform, Params: map[string]any{"min": 16.0, "max": 40.0}},
		{Name: "lo", Type: "u4", Distribution: DistUniform, Params: map[string]any{"min": -9.0, "max": -1.0}},
	}}
	data, _, err := SynthBytes(spec, Options{Seed: 9})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	nums, _ := decodeCohort(t, data)
	for i := range nums["hi"] {
		if nums["hi"][i] != 15 || nums["lo"][i] != 0 {
			t.Fatalf("row %d: hi=%v lo=%v, want 15 and 0", i, nums["hi"][i], nums["lo"][i])
		}
	}
}
