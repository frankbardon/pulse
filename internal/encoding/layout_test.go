package encoding

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func TestCellWidth(t *testing.T) {
	for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
		want := ft.ByteSize()
		if ft.IsBitPacked() {
			want = 1
		}
		if got := CellWidth(ft); got != want {
			t.Errorf("CellWidth(%s) = %d, want %d", ft, got, want)
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
	payload := LayoutFields(s.Fields)
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
