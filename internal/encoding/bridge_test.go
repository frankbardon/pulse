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

// TestOnWireWidthMatchesByteSize pins this package's onWireWidth (built
// on fixedWidthBytes) to the public package's copy (1 for a bit-packed
// type, FieldType.ByteSize otherwise) for every type byte.
func TestOnWireWidthMatchesByteSize(t *testing.T) {
	for b := 0; b < 256; b++ {
		ft := encoding.FieldType(b)
		want := ft.ByteSize()
		if ft.IsBitPacked() {
			want = 1
		}
		if got := onWireWidth(ft); got != want {
			t.Errorf("onWireWidth(%d) = %d, public twin says %d", b, got, want)
		}
	}
}
