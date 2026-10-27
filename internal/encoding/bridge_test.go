package encoding

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/encodingbridge"
)

// TestEncodingBridgeHooksInstalled: importing the public package installs
// every hook this package reaches through internal/encodingbridge.
func TestEncodingBridgeHooksInstalled(t *testing.T) {
	t.Parallel()

	if encodingbridge.WriteHeaderVersion == nil || encodingbridge.WriteSchemaVersion == nil ||
		encodingbridge.ValidateGroupShape == nil || encodingbridge.MemberSets == nil ||
		encodingbridge.GroupEntryGeometry == nil || encodingbridge.GroupDescriptorBytes == nil {
		t.Fatal("public encoding did not install every encodingbridge hook")
	}
	var b bytes.Buffer
	if err := writeHeaderVersion(&b, encoding.FormatVersionV2); err != nil {
		t.Fatal(err)
	}
	if b.Len() != encoding.HeaderSize || b.Bytes()[8] != encoding.FormatVersionV2 {
		t.Fatalf("bridged header = %v", b.Bytes())
	}
}

// TestOnWireWidthMatchesByteSize pins the ONE field-aware width,
// encoding.Schema.FieldWidth, for every type byte: 1 for a bit-packed
// type, FieldType.ByteSize otherwise (0 for an unknown byte), and — for
// every type the decoders' generic arm sizes from fixedWidthBytes — the
// same bytes that arm consumes, so a decode cursor and the stride it
// walks can never disagree. The duplicate onWireWidth tables this test
// used to pin together are gone; every stride site calls FieldWidth.
func TestOnWireWidthMatchesByteSize(t *testing.T) {
	t.Parallel()

	for b := 0; b < 256; b++ {
		ft := encoding.FieldType(b)
		s := &encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeU8}, {Name: "b", Type: ft}}}
		want := ft.ByteSize()
		if ft.IsBitPacked() {
			want = 1
		}
		if got := s.FieldWidth(1); got != want {
			t.Errorf("FieldWidth(type %d) = %d, want %d", b, got, want)
		}
		if n := fixedWidthBytes(ft); n != 0 && n != s.FieldWidth(1) {
			t.Errorf("type %d: decode arm consumes %d bytes, FieldWidth says %d", b, n, s.FieldWidth(1))
		}
	}
	s := &encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeU64}}}
	for _, i := range []int{-1, 1, 7} {
		if got := s.FieldWidth(i); got != 0 {
			t.Errorf("FieldWidth(%d) out of range = %d, want 0", i, got)
		}
	}
}
