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
	ByteOffset   int
	BitPosition  int
	CsvColumnIdx int
	Description  string      // empty = synthesized at inspect time
	Dictionary   *Dictionary // non-nil only for categorical types

	// Precision is the decimal128 precision (1-38). Meaningful only when
	// Type is FieldTypeDecimal128 or FieldTypeNullableDecimal128.
	Precision uint8
	// Scale is the decimal128 scale (0-Precision). Meaningful only when
	// Type is FieldTypeDecimal128 or FieldTypeNullableDecimal128.
	Scale uint8
	// H3Resolution is the native cell resolution recorded at import time
	// (0-15). Meaningful only when Type is FieldTypeH3Cell. The sentinel
	// 0xFF means "unspecified" and is treated as missing metadata.
	H3Resolution uint8
}

// Schema holds all field descriptors for a .pulse file.
type Schema struct {
	Fields []Field
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

// WriteSchema serializes a schema to w.
// Format:
//
//	u16 field_count
//	per field:
//	  u8 type
//	  u16 name_length + utf8 name
//	  u32 byte_offset
//	  u8 bit_position
//	  u16 csv_column_idx
//	  u16 description_length + utf8 description
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

		// Decimal precision/scale metadata for decimal128 variants.
		if f.Type.IsDecimal() {
			if err := binary.Write(w, binary.LittleEndian, f.Precision); err != nil {
				return errors.WrapCodedError(err, errors.ENCODING_IO, "writing decimal precision")
			}
			if err := binary.Write(w, binary.LittleEndian, f.Scale); err != nil {
				return errors.WrapCodedError(err, errors.ENCODING_IO, "writing decimal scale")
			}
		}

		// H3 native resolution for h3_cell fields.
		if f.Type == FieldTypeH3Cell {
			if err := binary.Write(w, binary.LittleEndian, f.H3Resolution); err != nil {
				return errors.WrapCodedError(err, errors.ENCODING_IO, "writing h3 resolution")
			}
		}

		// Dictionary block for categorical types.
		if f.Type.IsCategorical() && f.Dictionary != nil {
			if _, err := f.Dictionary.WriteTo(w); err != nil {
				return err
			}
		} else if f.Type.IsCategorical() {
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

		// H3 native resolution.
		if f.Type == FieldTypeH3Cell {
			if err := binary.Read(r, binary.LittleEndian, &f.H3Resolution); err != nil {
				return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading h3 resolution")
			}
		}

		// Dictionary for categorical types.
		if f.Type.IsCategorical() {
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
