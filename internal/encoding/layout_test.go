package encoding

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestCellWidth: LayoutFields steps every field by Schema.FieldWidth —
// one whole byte for a bit-packed type, ByteSize otherwise — for every
// registered type, each placed after a one-byte lead field.
func TestCellWidth(t *testing.T) {
	for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
		want := ft.ByteSize()
		if ft.IsBitPacked() {
			want = 1
		}
		s := &encoding.Schema{Fields: []encoding.Field{
			{Name: "lead", Type: encoding.FieldTypeU8},
			{Name: "x", Type: ft},
			{Name: "tail", Type: encoding.FieldTypeU8},
		}}
		payload := LayoutFields(s)
		if got := s.FieldWidth(1); got != want {
			t.Errorf("FieldWidth(%s) = %d, want %d", ft, got, want)
		}
		if s.Fields[2].ByteOffset != 1+want || payload != 2+want {
			t.Errorf("%s: tail offset %d / payload %d, want %d / %d", ft, s.Fields[2].ByteOffset, payload, 1+want, 2+want)
		}
	}
}

// LayoutFields gives each bit-packed field its own byte at bit 0 and
// agrees with Schema.RecordByteSize on the payload width.
func TestLayoutFields_BitPacked(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU4, ByteOffset: 9, BitPosition: 5},
		{Name: "b", Type: encoding.FieldTypePackedBool, BitPosition: 7},
		{Name: "c", Type: encoding.FieldTypeU16},
		{Name: "d", Type: encoding.FieldTypePackedBool},
		{Name: "e", Type: encoding.FieldTypeSetU128},
		{Name: "f", Type: encoding.FieldTypeU4},
	}}
	payload := LayoutFields(s)
	for i, want := range []int{0, 1, 2, 4, 5, 21} {
		f := s.Fields[i]
		if f.ByteOffset != want || f.BitPosition != 0 {
			t.Errorf("%s offset/bit = %d/%d, want %d/0", f.Name, f.ByteOffset, f.BitPosition, want)
		}
	}
	if payload != 22 || payload != s.RecordByteSize() {
		t.Errorf("payload = %d, RecordByteSize = %d, want 22", payload, s.RecordByteSize())
	}
	offsets, wp := widenFieldOffsets(s)
	for i := range offsets {
		if offsets[i] != s.Fields[i].ByteOffset {
			t.Errorf("widenFieldOffsets[%d] = %d, LayoutFields %d", i, offsets[i], s.Fields[i].ByteOffset)
		}
	}
	if wp != payload {
		t.Errorf("widen payload %d != %d", wp, payload)
	}
}
