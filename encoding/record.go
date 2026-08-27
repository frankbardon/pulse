package encoding

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// WriteFieldValue writes a single field value (as raw bits in uint64) to w.
// For bit-packed types (U4, PackedBool), use WriteBit/WriteNibble instead.
// For decimal128 (16-byte type), use the dedicated WriteDecimal128 helper;
// this function will reject those types with ENCODING_TYPE_MISMATCH.
func WriteFieldValue(w io.Writer, ft FieldType, val uint64) error {
	switch ft {
	case FieldTypeU8, FieldTypeCategoricalU8, FieldTypeSetU8:
		return binary.Write(w, binary.LittleEndian, uint8(val))
	case FieldTypeU16, FieldTypeCategoricalU16, FieldTypeSetU16:
		return binary.Write(w, binary.LittleEndian, uint16(val))
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32, FieldTypeSetU32:
		return binary.Write(w, binary.LittleEndian, uint32(val))
	case FieldTypeU64, FieldTypeSetU64, FieldTypeDateTime:
		return binary.Write(w, binary.LittleEndian, val)
	case FieldTypeF32:
		return binary.Write(w, binary.LittleEndian, uint32(val))
	case FieldTypeF64:
		return binary.Write(w, binary.LittleEndian, val)
	case FieldTypeU4, FieldTypePackedBool:
		// Bit-packed; callers must use WriteBit/WriteNibble.
		return errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("use bit-level API for %s", ft))
	case FieldTypeDecimal128:
		return errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("use 16-byte API for %s", ft))
	default:
		return errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("unknown field type %d", ft))
	}
}

// ReadFieldValue reads a single field value from r, returning raw bits as uint64.
// For bit-packed types (U4, PackedBool), use ReadBit/ReadNibble instead.
func ReadFieldValue(r io.Reader, ft FieldType) (uint64, error) {
	switch ft {
	case FieldTypeU8, FieldTypeCategoricalU8, FieldTypeSetU8:
		var v uint8
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case FieldTypeU16, FieldTypeCategoricalU16, FieldTypeSetU16:
		var v uint16
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32, FieldTypeSetU32:
		var v uint32
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case FieldTypeU64, FieldTypeSetU64, FieldTypeDateTime:
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
	case FieldTypeU4, FieldTypePackedBool:
		return 0, errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("use bit-level API for %s", ft))
	case FieldTypeDecimal128:
		return 0, errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("use 16-byte API for %s", ft))
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

// ReadBitmap reads a per-record null bitmap of the given byte length and
// returns the raw bytes. byteLen MUST be Schema.BitmapByteSize(); callers
// invoke this only when the schema has at least one nullable field.
func ReadBitmap(r io.Reader, byteLen int) ([]byte, error) {
	if byteLen <= 0 {
		return nil, nil
	}
	buf := make([]byte, byteLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "reading null bitmap")
	}
	return buf, nil
}

// WriteBitmap writes a per-record null bitmap. The slice MUST be exactly
// Schema.BitmapByteSize() bytes; this helper is intentionally thin to keep
// the writer responsible for allocating and populating the buffer.
func WriteBitmap(w io.Writer, bitmap []byte) error {
	if len(bitmap) == 0 {
		return nil
	}
	if _, err := w.Write(bitmap); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing null bitmap")
	}
	return nil
}

// BitmapIsNull reports whether field at index i is marked null in bitmap.
// Bit ordering: field index i → byte i/8, bit i%8 (LSB-first within each
// byte). 1 = null, 0 = present.
func BitmapIsNull(bitmap []byte, i int) bool {
	if len(bitmap) == 0 || i < 0 {
		return false
	}
	byteIdx := i / 8
	if byteIdx >= len(bitmap) {
		return false
	}
	return bitmap[byteIdx]&(1<<uint(i%8)) != 0
}

// BitmapSetNull marks the field at index i as null in bitmap. The caller
// must have pre-allocated bitmap to at least ceil((i+1)/8) bytes.
func BitmapSetNull(bitmap []byte, i int) {
	if len(bitmap) == 0 || i < 0 {
		return
	}
	byteIdx := i / 8
	if byteIdx >= len(bitmap) {
		return
	}
	bitmap[byteIdx] |= 1 << uint(i%8)
}
