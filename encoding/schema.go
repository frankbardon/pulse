package encoding

import (
	"encoding/binary"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// Field describes a single column in a .pulse schema.
type Field struct {
	Name         string
	Type         FieldType
	Nullable     bool // true ⇒ field participates in per-record null bitmap
	ByteOffset   int
	BitPosition  int
	CsvColumnIdx int
	Description  string      // empty = synthesized at inspect time
	Dictionary   *Dictionary // non-nil only for categorical types

	// Precision is the decimal128 precision (1-38). Meaningful only when
	// Type is FieldTypeDecimal128.
	Precision uint8
	// Scale is the decimal128 scale (0-Precision). Meaningful only when
	// Type is FieldTypeDecimal128.
	Scale uint8
}

// Schema holds all field descriptors for a .pulse file.
type Schema struct {
	Fields []Field
}

// HasBitmap reports whether any field in the schema is marked nullable.
// When true, every record carries a trailing null bitmap of
// ceil(field_count/8) bytes after the payload; when false, records have
// no bitmap (legacy fixed-stride path, zero overhead).
func (s *Schema) HasBitmap() bool {
	for i := range s.Fields {
		if s.Fields[i].Nullable {
			return true
		}
	}
	return false
}

// BitmapByteSize returns the number of bytes the null bitmap occupies
// per record, or 0 when no field is nullable.
func (s *Schema) BitmapByteSize() int {
	if !s.HasBitmap() {
		return 0
	}
	return (len(s.Fields) + 7) / 8
}

// RecordByteSize returns the on-wire stride of one record under this
// schema. Bit-packed fields (U4, PackedBool) report ByteSize()==0 but the
// wire format still consumes one whole byte per such field. When the
// schema declares at least one nullable field, the trailing null bitmap
// of ceil(field_count/8) bytes is appended to every record and included
// in the stride.
func (s *Schema) RecordByteSize() int {
	stride := 0
	for i := range s.Fields {
		ft := s.Fields[i].Type
		if ft.IsBitPacked() {
			stride++
			continue
		}
		stride += ft.ByteSize()
	}
	stride += s.BitmapByteSize()
	return stride
}

// Field returns a pointer to the named field, or nil if not found.
func (s *Schema) Field(name string) *Field {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return &s.Fields[i]
		}
	}
	return nil
}

// Categorical returns the dictionary for a named categorical field.
// Returns nil, false if the field is not found or is not categorical.
func (s *Schema) Categorical(name string) (*Dictionary, bool) {
	f := s.Field(name)
	if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
		return nil, false
	}
	return f.Dictionary, true
}

// SetField returns the dictionary for a named set-typed field.
// Returns nil, false if the field is not found or is not a set type.
func (s *Schema) SetField(name string) (*Dictionary, bool) {
	f := s.Field(name)
	if f == nil || !f.Type.IsSet() || f.Dictionary == nil {
		return nil, false
	}
	return f.Dictionary, true
}

// WriteSchema serializes a schema to w.
// Format:
//
//	u16 field_count
//	per field:
//	  u8 type
//	  u8 nullable (0 or 1)
//	  u16 name_length + utf8 name
//	  u32 byte_offset
//	  u8 bit_position
//	  u16 csv_column_idx
//	  u16 description_length + utf8 description
//	  (if decimal128) u8 precision + u8 scale
//	  (if categorical) dictionary block
func WriteSchema(w io.Writer, s *Schema) error {
	fieldCount := uint16(len(s.Fields))
	if err := binary.Write(w, binary.LittleEndian, fieldCount); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing field count")
	}

	for _, f := range s.Fields {
		// Type byte.
		if err := binary.Write(w, binary.LittleEndian, byte(f.Type)); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing field type")
		}

		// Nullable flag.
		var nullableByte uint8
		if f.Nullable {
			nullableByte = 1
		}
		if err := binary.Write(w, binary.LittleEndian, nullableByte); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing nullable flag")
		}

		// Name: u16 len + bytes.
		nameBytes := []byte(f.Name)
		if err := binary.Write(w, binary.LittleEndian, uint16(len(nameBytes))); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing field name length")
		}
		if _, err := w.Write(nameBytes); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing field name")
		}

		// Byte offset.
		if err := binary.Write(w, binary.LittleEndian, uint32(f.ByteOffset)); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing byte offset")
		}

		// Bit position.
		if err := binary.Write(w, binary.LittleEndian, uint8(f.BitPosition)); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing bit position")
		}

		// CSV column index.
		if err := binary.Write(w, binary.LittleEndian, uint16(f.CsvColumnIdx)); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing csv column index")
		}

		// Description suffix.
		if err := WriteDescription(w, f.Description); err != nil {
			return err
		}

		// Decimal precision/scale metadata for decimal128.
		if f.Type.IsDecimal() {
			if err := binary.Write(w, binary.LittleEndian, f.Precision); err != nil {
				return errors.WrapCodedError(err, errors.ENCODING_IO, "writing decimal precision")
			}
			if err := binary.Write(w, binary.LittleEndian, f.Scale); err != nil {
				return errors.WrapCodedError(err, errors.ENCODING_IO, "writing decimal scale")
			}
		}

		// Dictionary block for types that carry one (categorical_* and set_*).
		if f.Type.HasDictionary() && f.Dictionary != nil {
			if _, err := f.Dictionary.WriteTo(w); err != nil {
				return err
			}
		} else if f.Type.HasDictionary() {
			// Write empty dictionary.
			if err := binary.Write(w, binary.LittleEndian, uint32(0)); err != nil {
				return errors.WrapCodedError(err, errors.ENCODING_IO, "writing empty dictionary")
			}
		}
	}
	return nil
}

// ReadSchema deserializes a schema from r.
func ReadSchema(r io.Reader) (*Schema, error) {
	var fieldCount uint16
	if err := binary.Read(r, binary.LittleEndian, &fieldCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading field count")
	}

	s := &Schema{
		Fields: make([]Field, 0, fieldCount),
	}

	for i := 0; i < int(fieldCount); i++ {
		var f Field

		// Type byte.
		var typeByte uint8
		if err := binary.Read(r, binary.LittleEndian, &typeByte); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading field type")
		}
		f.Type = FieldType(typeByte)
		// Reject unknown type bytes loud at parse time so files written by a
		// future-version binary fail fast here, not silently mid-record.
		if !f.Type.IsKnown() {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"unknown field type byte",
				map[string]any{"byte": typeByte, "field_index": i})
		}

		// Nullable flag.
		var nullableByte uint8
		if err := binary.Read(r, binary.LittleEndian, &nullableByte); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading nullable flag")
		}
		f.Nullable = nullableByte != 0

		// Name.
		var nameLen uint16
		if err := binary.Read(r, binary.LittleEndian, &nameLen); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading field name length")
		}
		nameBuf := make([]byte, nameLen)
		if _, err := io.ReadFull(r, nameBuf); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading field name")
		}
		f.Name = string(nameBuf)

		// Byte offset.
		var byteOffset uint32
		if err := binary.Read(r, binary.LittleEndian, &byteOffset); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading byte offset")
		}
		f.ByteOffset = int(byteOffset)

		// Bit position.
		var bitPos uint8
		if err := binary.Read(r, binary.LittleEndian, &bitPos); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading bit position")
		}
		f.BitPosition = int(bitPos)

		// CSV column index.
		var csvIdx uint16
		if err := binary.Read(r, binary.LittleEndian, &csvIdx); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading csv column index")
		}
		f.CsvColumnIdx = int(csvIdx)

		// Description.
		desc, err := ReadDescription(r)
		if err != nil {
			return nil, err
		}
		f.Description = desc

		// Decimal precision/scale metadata.
		if f.Type.IsDecimal() {
			if err := binary.Read(r, binary.LittleEndian, &f.Precision); err != nil {
				return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading decimal precision")
			}
			if err := binary.Read(r, binary.LittleEndian, &f.Scale); err != nil {
				return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading decimal scale")
			}
		}

		// Dictionary for types that carry one (categorical_* and set_*).
		if f.Type.HasDictionary() {
			dict := NewDictionary()
			if _, err := dict.ReadFrom(r); err != nil {
				return nil, err
			}
			f.Dictionary = dict
		}

		s.Fields = append(s.Fields, f)
	}

	return s, nil
}
