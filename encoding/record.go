package encoding

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// WriteFieldValue writes a single field value (as raw bits in uint64) to w.
// For packed types (PackedBool, NullableBool, NullableU4), use WriteBit/WriteNibble instead.
func WriteFieldValue(w io.Writer, ft FieldType, val uint64) error {
	switch ft {
	case FieldTypeU8, FieldTypeNullableU8, FieldTypeCategoricalU8:
		return binary.Write(w, binary.LittleEndian, uint8(val))
	case FieldTypeU16, FieldTypeNullableU16, FieldTypeCategoricalU16:
		return binary.Write(w, binary.LittleEndian, uint16(val))
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32:
		return binary.Write(w, binary.LittleEndian, uint32(val))
	case FieldTypeU64:
		return binary.Write(w, binary.LittleEndian, val)
	case FieldTypeF32:
		return binary.Write(w, binary.LittleEndian, uint32(val))
	case FieldTypeF64:
		return binary.Write(w, binary.LittleEndian, val)
	case FieldTypePackedBool, FieldTypeNullableBool, FieldTypeNullableU4:
		// These are bit-packed; callers should use WriteBit/WriteNibble.
		return errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("use bit-level API for %s", ft))
	default:
		return errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("unknown field type %d", ft))
	}
}

// ReadFieldValue reads a single field value from r, returning raw bits as uint64.
// For packed types (PackedBool, NullableBool, NullableU4), use ReadBit/ReadNibble instead.
func ReadFieldValue(r io.Reader, ft FieldType) (uint64, error) {
	switch ft {
	case FieldTypeU8, FieldTypeNullableU8, FieldTypeCategoricalU8:
		var v uint8
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case FieldTypeU16, FieldTypeNullableU16, FieldTypeCategoricalU16:
		var v uint16
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32:
		var v uint32
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case FieldTypeU64:
		var v uint64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case FieldTypeF32:
		var v uint32
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case FieldTypeF64:
		var v uint64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case FieldTypePackedBool, FieldTypeNullableBool, FieldTypeNullableU4:
		return 0, errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("use bit-level API for %s", ft))
	default:
		return 0, errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("unknown field type %d", ft))
	}
}

// ReadBit reads a single bit from the byte at the current position in r.
// bitPos is 0-7 within that byte.
func ReadBit(r io.Reader, bitPos uint) (bool, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return false, errors.WrapCodedError(err, errors.ENCODING_IO, "reading bit")
	}
	return (b[0]>>bitPos)&1 == 1, nil
}

// WriteBit writes a byte with a single bit set or cleared at bitPos.
func WriteBit(w io.Writer, bitPos uint, val bool) error {
	var b byte
	if val {
		b = 1 << bitPos
	}
	_, err := w.Write([]byte{b})
	return err
}

// ReadNibble reads a 4-bit value from a byte. If high is true,
// it reads bits 4-7; otherwise bits 0-3.
func ReadNibble(r io.Reader, high bool) (uint8, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_IO, "reading nibble")
	}
	if high {
		return b[0] >> 4, nil
	}
	return b[0] & 0x0F, nil
}

// WriteNibble writes a 4-bit value into a byte. If high is true,
// it writes to bits 4-7; otherwise bits 0-3. The other nibble is zero.
func WriteNibble(w io.Writer, high bool, val uint8) error {
	var b byte
	if high {
		b = (val & 0x0F) << 4
	} else {
		b = val & 0x0F
	}
	_, err := w.Write([]byte{b})
	return err
}
