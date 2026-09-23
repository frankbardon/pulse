package encoding

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// The rung-agnostic slot codec readSetMaskSlot / writeSetMaskSlot is the
// piece the widen engine leans on, and its narrow arms are only PARTLY
// reachable from a widen: set_u8 can never be a widen TARGET (nothing is
// narrower than it), and a mask that overflows its rung cannot arise from
// widening at all, since the target is wider by construction. Both arms
// still have to be right — the codec is the mirror decodeSetMask never
// had, and the next caller will not be a widen. So they are pinned here,
// in-package, rather than left to an end-to-end test that cannot reach
// them.

func widenSlotMask(bits ...int) SetMask {
	var m SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	return m
}

func TestSetMaskSlot_RoundTripsEveryRung(t *testing.T) {
	rungs := []FieldType{
		FieldTypeSetU8, FieldTypeSetU16, FieldTypeSetU32,
		FieldTypeSetU64, FieldTypeSetU128, FieldTypeSetU256,
	}
	for _, ft := range rungs {
		t.Run(ft.String(), func(t *testing.T) {
			width := ft.ByteSize()
			top := int(ft.MaxSetEntries()) - 1
			for _, m := range []SetMask{
				widenSlotMask(),
				widenSlotMask(0),
				widenSlotMask(top),
				widenSlotMask(0, top/2, top),
			} {
				// Two sentinel bytes past the field so an arm that writes
				// wider than its rung is caught rather than tolerated.
				buf := make([]byte, width+2)
				buf[width], buf[width+1] = 0xAA, 0x55
				if err := writeSetMaskSlot(buf, ft, m); err != nil {
					t.Fatalf("writeSetMaskSlot(%s, %v): %v", ft, m.Words(), err)
				}
				if buf[width] != 0xAA || buf[width+1] != 0x55 {
					t.Fatalf("%s: write ran past the field width into %x", ft, buf[width:])
				}
				got, err := readSetMaskSlot(buf, ft)
				if err != nil {
					t.Fatalf("readSetMaskSlot(%s): %v", ft, err)
				}
				if !got.Equal(m) {
					t.Fatalf("%s round trip: want %v, got %v", ft, m.Words(), got.Words())
				}
			}
		})
	}
}

func TestWriteSetMaskSlot_RefusesAMaskBeyondTheRung(t *testing.T) {
	// A dropped selection is indistinguishable from one never made, so a
	// narrow rung must refuse rather than silently truncate.
	buf := make([]byte, 8)
	err := writeSetMaskSlot(buf, FieldTypeSetU8, widenSlotMask(8))
	if err == nil {
		t.Fatalf("want a refusal for bit 8 in set_u8")
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("want ENCODING_INVALID, got %v", err)
	}
	for i, b := range buf {
		if b != 0 {
			t.Errorf("refused write still touched byte %d (%#x)", i, b)
		}
	}
}

func TestSetMaskSlot_RefusesNonSetAndShortBuffers(t *testing.T) {
	var ce *errors.CodedError

	if _, err := readSetMaskSlot(make([]byte, 8), FieldTypeU64); err == nil {
		t.Errorf("readSetMaskSlot must refuse a non-set type")
	} else if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_TYPE_MISMATCH {
		t.Errorf("want ENCODING_TYPE_MISMATCH, got %v", err)
	}

	if err := writeSetMaskSlot(make([]byte, 8), FieldTypeCategoricalU8, SetMask{}); err == nil {
		t.Errorf("writeSetMaskSlot must refuse a non-set type")
	} else if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_TYPE_MISMATCH {
		t.Errorf("want ENCODING_TYPE_MISMATCH, got %v", err)
	}

	if _, err := readSetMaskSlot(make([]byte, 2), FieldTypeSetU32); err == nil {
		t.Errorf("readSetMaskSlot must refuse a short buffer")
	} else if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Errorf("want ENCODING_INVALID, got %v", err)
	}

	if err := writeSetMaskSlot(make([]byte, 2), FieldTypeSetU32, SetMask{}); err == nil {
		t.Errorf("writeSetMaskSlot must refuse a short buffer")
	} else if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Errorf("want ENCODING_INVALID, got %v", err)
	}
}
