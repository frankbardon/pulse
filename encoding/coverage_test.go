package encoding

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// errWriter is an io.Writer that always returns an error.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// --- WriteBit / WriteNibble coverage ---

func TestWriteBit(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteBit(&buf, 0, true); err != nil {
		t.Fatalf("WriteBit: %v", err)
	}
	if buf.Bytes()[0] != 1 {
		t.Errorf("byte = 0x%02x, want 0x01", buf.Bytes()[0])
	}

	buf.Reset()
	if err := WriteBit(&buf, 3, true); err != nil {
		t.Fatalf("WriteBit: %v", err)
	}
	if buf.Bytes()[0] != 0x08 {
		t.Errorf("byte = 0x%02x, want 0x08", buf.Bytes()[0])
	}

	buf.Reset()
	if err := WriteBit(&buf, 7, false); err != nil {
		t.Fatalf("WriteBit: %v", err)
	}
	if buf.Bytes()[0] != 0 {
		t.Errorf("byte = 0x%02x, want 0x00", buf.Bytes()[0])
	}
}

func TestWriteNibble(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteNibble(&buf, false, 0x0A); err != nil {
		t.Fatalf("WriteNibble low: %v", err)
	}
	if buf.Bytes()[0] != 0x0A {
		t.Errorf("low nibble byte = 0x%02x, want 0x0A", buf.Bytes()[0])
	}

	buf.Reset()
	if err := WriteNibble(&buf, true, 0x07); err != nil {
		t.Fatalf("WriteNibble high: %v", err)
	}
	if buf.Bytes()[0] != 0x70 {
		t.Errorf("high nibble byte = 0x%02x, want 0x70", buf.Bytes()[0])
	}
}

// --- WriteFieldValue for packed types returns error ---

func TestWriteFieldValue_PackedBool(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFieldValue(&buf, FieldTypePackedBool, 0)
	if err == nil {
		t.Fatal("expected error for packed bool")
	}
	if !errors.HasCode(err, errors.ENCODING_TYPE_MISMATCH) {
		t.Errorf("expected ENCODING_TYPE_MISMATCH, got: %v", err)
	}
}

func TestWriteFieldValue_NullableBool(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFieldValue(&buf, FieldTypeNullableBool, 0)
	if err == nil {
		t.Fatal("expected error for nullable bool")
	}
}

func TestWriteFieldValue_NullableU4(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFieldValue(&buf, FieldTypeNullableU4, 0)
	if err == nil {
		t.Fatal("expected error for nullable u4")
	}
}

func TestReadFieldValue_PackedBool(t *testing.T) {
	r := bytes.NewReader([]byte{0})
	_, err := ReadFieldValue(r, FieldTypePackedBool)
	if err == nil {
		t.Fatal("expected error for packed bool")
	}
}

func TestReadFieldValue_NullableBool(t *testing.T) {
	r := bytes.NewReader([]byte{0})
	_, err := ReadFieldValue(r, FieldTypeNullableBool)
	if err == nil {
		t.Fatal("expected error for nullable bool")
	}
}

func TestReadFieldValue_NullableU4(t *testing.T) {
	r := bytes.NewReader([]byte{0})
	_, err := ReadFieldValue(r, FieldTypeNullableU4)
	if err == nil {
		t.Fatal("expected error for nullable u4")
	}
}

// --- IO error paths ---

func TestWriteHeader_IOError(t *testing.T) {
	err := WriteHeader(errWriter{})
	if err == nil {
		t.Fatal("expected IO error")
	}
	if !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("expected ENCODING_IO, got: %v", err)
	}
}

func TestWriteDescription_IOError(t *testing.T) {
	err := WriteDescription(errWriter{}, "test")
	if err == nil {
		t.Fatal("expected IO error")
	}
}

func TestReadDescription_Truncated(t *testing.T) {
	// Write a length that promises bytes, then truncate.
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(100))
	// Only write 2 bytes of the promised 100.
	buf.Write([]byte{0x41, 0x42})

	_, err := ReadDescription(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated description")
	}
}

func TestReadDescription_TruncatedLength(t *testing.T) {
	_, err := ReadDescription(bytes.NewReader([]byte{0x01})) // only 1 byte for u16
	if err == nil {
		t.Fatal("expected error for truncated length")
	}
}

func TestDictionary_WriteTo_IOError(t *testing.T) {
	d := NewDictionary()
	d.Add("test")
	_, err := d.WriteTo(errWriter{})
	if err == nil {
		t.Fatal("expected IO error")
	}
}

func TestDictionary_ReadFrom_Truncated(t *testing.T) {
	// Truncated count.
	d := NewDictionary()
	_, err := d.ReadFrom(bytes.NewReader([]byte{0x01}))
	if err == nil {
		t.Fatal("expected error for truncated count")
	}
}

func TestDictionary_ReadFrom_TruncatedString(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(1))   // 1 entry
	binary.Write(&buf, binary.LittleEndian, uint16(100))  // string of 100 bytes
	buf.Write([]byte{0x41}) // only 1 byte

	d := NewDictionary()
	_, err := d.ReadFrom(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated string")
	}
}

func TestDictionary_ReadFrom_TruncatedStringLen(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(1)) // 1 entry
	buf.Write([]byte{0x01})                             // only 1 byte for u16 strlen

	d := NewDictionary()
	_, err := d.ReadFrom(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated string length")
	}
}

// --- Schema IO error paths ---

func TestWriteSchema_IOError(t *testing.T) {
	s := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}}
	err := WriteSchema(errWriter{}, s)
	if err == nil {
		t.Fatal("expected IO error")
	}
}

func TestReadSchema_TruncatedFieldCount(t *testing.T) {
	_, err := ReadSchema(bytes.NewReader([]byte{0x01}))
	if err == nil {
		t.Fatal("expected error for truncated field count")
	}
}

func TestReadSchema_TruncatedFieldType(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // 1 field
	// no more data

	_, err := ReadSchema(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated field type")
	}
}

func TestReadSchema_TruncatedFieldName(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // 1 field
	binary.Write(&buf, binary.LittleEndian, uint8(0))    // type U8
	binary.Write(&buf, binary.LittleEndian, uint16(100)) // name len 100
	buf.Write([]byte{0x41})                               // only 1 byte

	_, err := ReadSchema(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated field name")
	}
}

func TestReadSchema_TruncatedNameLen(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // 1 field
	binary.Write(&buf, binary.LittleEndian, uint8(0))  // type U8
	buf.Write([]byte{0x01})                             // truncated name len

	_, err := ReadSchema(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated name length")
	}
}

func TestReadSchema_TruncatedByteOffset(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // 1 field
	binary.Write(&buf, binary.LittleEndian, uint8(0))  // type U8
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // name len 1
	buf.Write([]byte{'x'})                              // name
	buf.Write([]byte{0x01})                             // truncated byte offset

	_, err := ReadSchema(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated byte offset")
	}
}

func TestReadSchema_TruncatedBitPos(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // 1 field
	binary.Write(&buf, binary.LittleEndian, uint8(0))   // type U8
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // name len 1
	buf.Write([]byte{'x'})                               // name
	binary.Write(&buf, binary.LittleEndian, uint32(0))  // byte offset
	// no bit position

	_, err := ReadSchema(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated bit position")
	}
}

func TestReadSchema_TruncatedCsvIdx(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // 1 field
	binary.Write(&buf, binary.LittleEndian, uint8(0))   // type U8
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // name len 1
	buf.Write([]byte{'x'})                               // name
	binary.Write(&buf, binary.LittleEndian, uint32(0))  // byte offset
	binary.Write(&buf, binary.LittleEndian, uint8(0))   // bit pos
	// no csv idx

	_, err := ReadSchema(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated csv index")
	}
}

// --- WriteSchema with categorical but nil dictionary ---

func TestWriteSchema_CategoricalNilDict(t *testing.T) {
	s := &Schema{
		Fields: []Field{
			{Name: "cat", Type: FieldTypeCategoricalU8, Dictionary: nil},
		},
	}
	var buf bytes.Buffer
	if err := WriteSchema(&buf, s); err != nil {
		t.Fatalf("WriteSchema with nil dict: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	s2, err := ReadSchema(reader)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if s2.Fields[0].Dictionary == nil {
		t.Fatal("expected non-nil dictionary after read")
	}
	if s2.Fields[0].Dictionary.Count() != 0 {
		t.Errorf("expected empty dict, got %d entries", s2.Fields[0].Dictionary.Count())
	}
}

// --- ReadBit/ReadNibble IO errors ---

func TestReadBit_Empty(t *testing.T) {
	_, err := ReadBit(bytes.NewReader(nil), 0)
	if err == nil {
		t.Fatal("expected error on empty reader")
	}
}

func TestReadNibble_Empty(t *testing.T) {
	_, err := ReadNibble(bytes.NewReader(nil), false)
	if err == nil {
		t.Fatal("expected error on empty reader")
	}
}

// --- Dictionary WriteTo with string length > 0 and IO error ---

type limitWriter struct {
	n   int
	buf bytes.Buffer
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.n <= 0 {
		return 0, io.ErrClosedPipe
	}
	if len(p) > lw.n {
		n, _ := lw.buf.Write(p[:lw.n])
		lw.n = 0
		return n, io.ErrClosedPipe
	}
	n, err := lw.buf.Write(p)
	lw.n -= n
	return n, err
}

func TestDictionary_WriteTo_StringLenIOError(t *testing.T) {
	d := NewDictionary()
	d.Add("test")
	// Allow count (4 bytes) then fail on string length write.
	lw := &limitWriter{n: 4}
	_, err := d.WriteTo(lw)
	if err == nil {
		t.Fatal("expected IO error on string length write")
	}
}

func TestDictionary_WriteTo_StringBodyIOError(t *testing.T) {
	d := NewDictionary()
	d.Add("test")
	// Allow count (4) + strlen (2) = 6 bytes, then fail on string body.
	lw := &limitWriter{n: 6}
	_, err := d.WriteTo(lw)
	if err == nil {
		t.Fatal("expected IO error on string body write")
	}
}

func TestWriteDescription_LengthIOError(t *testing.T) {
	// errWriter fails immediately, so the u16 length write fails.
	err := WriteDescription(errWriter{}, "")
	if err == nil {
		t.Fatal("expected IO error")
	}
}

func TestWriteDescription_BodyIOError(t *testing.T) {
	// Allow u16 length (2 bytes) then fail on body.
	lw := &limitWriter{n: 2}
	err := WriteDescription(lw, "hello")
	if err == nil {
		t.Fatal("expected IO error on body write")
	}
}

// --- WriteSchema error paths deeper in ---

func TestWriteSchema_FieldCountIOError(t *testing.T) {
	err := WriteSchema(errWriter{}, &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}})
	if err == nil {
		t.Fatal("expected IO error on field count")
	}
}

func TestWriteSchema_TypeByteIOError(t *testing.T) {
	lw := &limitWriter{n: 2} // allow field count u16, fail on type byte
	s := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}}
	err := WriteSchema(lw, s)
	if err == nil {
		t.Fatal("expected IO error on type byte")
	}
}

func TestWriteSchema_NameLenIOError(t *testing.T) {
	lw := &limitWriter{n: 3} // u16 count + u8 type = 3, fail on name len
	s := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}}
	err := WriteSchema(lw, s)
	if err == nil {
		t.Fatal("expected IO error on name length")
	}
}

func TestWriteSchema_NameIOError(t *testing.T) {
	lw := &limitWriter{n: 5} // u16 count + u8 type + u16 namelen = 5, fail on name bytes
	s := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}}
	err := WriteSchema(lw, s)
	if err == nil {
		t.Fatal("expected IO error on name bytes")
	}
}

func TestWriteSchema_ByteOffsetIOError(t *testing.T) {
	lw := &limitWriter{n: 6} // count(2) + type(1) + namelen(2) + name(1) = 6, fail on offset
	s := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}}
	err := WriteSchema(lw, s)
	if err == nil {
		t.Fatal("expected IO error on byte offset")
	}
}

func TestWriteSchema_BitPosIOError(t *testing.T) {
	lw := &limitWriter{n: 10} // count(2)+type(1)+namelen(2)+name(1)+offset(4) = 10
	s := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}}
	err := WriteSchema(lw, s)
	if err == nil {
		t.Fatal("expected IO error on bit position")
	}
}

func TestWriteSchema_CsvIdxIOError(t *testing.T) {
	lw := &limitWriter{n: 11} // +bitpos(1) = 11
	s := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeU8}}}
	err := WriteSchema(lw, s)
	if err == nil {
		t.Fatal("expected IO error on csv index")
	}
}

func TestWriteSchema_CategoricalEmptyDictIOError(t *testing.T) {
	// Allow everything up to the empty dict write, then fail.
	// count(2)+type(1)+namelen(2)+name(3)+offset(4)+bitpos(1)+csvidx(2)+desclen(2) = 17
	lw := &limitWriter{n: 17}
	s := &Schema{Fields: []Field{{Name: "cat", Type: FieldTypeCategoricalU8}}}
	err := WriteSchema(lw, s)
	if err == nil {
		t.Fatal("expected IO error on empty dict write")
	}
}
