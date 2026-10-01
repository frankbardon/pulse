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
	// Fields is the LOGICAL schema: every field, in original order. A
	// field index anywhere in the API is a position in this slice.
	Fields []Field
	// Groups are the parent-group descriptors (format 0x02, see
	// group.go). Empty for every 0x01 cohort. When non-empty, member
	// fields are stored in the groups' dictionaries rather than in the
	// row, and RecordByteSize / BitmapByteSize / HasBitmap describe the
	// PHYSICAL (reduced) row; Logical() is the ungrouped view.
	Groups []Group
}

// HasBitmap reports whether the record carries a per-record null
// bitmap: whether any field stored IN THE ROW is nullable. Without
// groups that is any field; with groups, member fields' null bits ride
// their dictionary entry, so only the row fields count — a cohort whose
// every nullable field is a group member has no per-row bitmap.
func (s *Schema) HasBitmap() bool {
	if s.HasGroups() {
		_, _, has := s.groupedRowSizes()
		return has
	}
	for i := range s.Fields {
		if s.Fields[i].Nullable {
			return true
		}
	}
	return false
}

// BitmapByteSize returns the number of bytes the null bitmap occupies
// per record, or 0 when no field is nullable. With groups it is the
// NARROWED bitmap: ceil(row_field_count/8), bit j = the j-th row field
// (fields in no group, logical order).
func (s *Schema) BitmapByteSize() int {
	if s.HasGroups() {
		_, bm, _ := s.groupedRowSizes()
		return bm
	}
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
//
// With groups it is the PHYSICAL stride: one GroupIndexWidth index per
// indexed group, plus the row fields, plus the narrowed bitmap. It is
// still fixed and a pure function of the schema.
func (s *Schema) RecordByteSize() int {
	if s.HasGroups() {
		body, bm, _ := s.groupedRowSizes()
		return body + bm
	}
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

// RequiredFormatVersion returns the .pulse format version this schema's
// content needs on the wire. It is the single place a writer's version
// is chosen — never a global flag — so a schema that uses no 0x02
// feature is always written at 0x01, byte-identical to a pre-0x02 file.
//
// A schema requires 0x02 exactly when it declares a parent group.
func (s *Schema) RequiredFormatVersion() byte {
	if s.HasGroups() {
		return FormatVersionV2
	}
	return FormatVersionV1
}

// WriteSchema serializes s's schema block to w at the baseline 0x01
// layout, to follow a [WriteHeader]. A schema whose content requires a
// newer version is refused rather than written in a layout its header
// would contradict; Pulse's own writers emit a grouped schema with a
// matching 0x02 header.
//
// The 0x01 schema block:
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
//
// A 0x02 schema block is the 0x01 block followed by the schema
// extension block (see group_wire.go for the payload):
//
//	u64 extension_length
//	extension_length bytes of extension payload (tagged sections)
//
// The length prefix is what makes the record region's start derivable
// without understanding the payload.
func WriteSchema(w io.Writer, s *Schema) error {
	if req := s.RequiredFormatVersion(); req != FormatVersion {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"schema requires a newer pulse format version than WriteSchema emits; write it with WritePreamble",
			map[string]any{"required_version": req, "version": FormatVersion})
	}
	return writeSchemaVersion(w, s, FormatVersion)
}

// writeSchemaVersion writes the schema block in version v's layout.
func writeSchemaVersion(w io.Writer, s *Schema, v byte) error {
	if err := writeFieldDescriptors(w, s); err != nil {
		return err
	}
	switch v {
	case FormatVersionV1:
		return nil
	case FormatVersionV2:
		return writeSchemaExtension(w, s)
	default:
		return unsupportedVersionError(v)
	}
}

// writeFieldDescriptors writes field_count and every field descriptor —
// the part of the schema block shared by every format version.
func writeFieldDescriptors(w io.Writer, s *Schema) error {
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

// ReadSchema deserializes a schema block written at format version v —
// the version [ReadHeader] returned for the same stream. On return r is
// positioned at the first record byte. An unsupported v is
// ENCODING_INVALID; unknown field-type bytes fail loud here, at parse
// time, for every version.
func ReadSchema(r io.Reader, v byte) (*Schema, error) {
	if !IsSupportedFormatVersion(v) {
		return nil, unsupportedVersionError(v)
	}
	s, err := readFieldDescriptors(r)
	if err != nil {
		return nil, err
	}
	if v >= FormatVersionV2 {
		if err := readSchemaExtension(r, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// readFieldDescriptors reads field_count and every field descriptor —
// the part of the schema block shared by every format version.
func readFieldDescriptors(r io.Reader) (*Schema, error) {
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

// RecordCountForPayload derives how many whole records occupy a payload
// of payloadBytes under this schema, together with the leftover bytes
// that do not complete a record.
//
// It is the ONE derivation behind every "how many records does this
// cohort hold" answer for a single-file cohort — internal/service.CountRecords
// (the header-fast facade path and the parallel-decode eligibility gate)
// and internal/descriptor.Inspect (the header-only reporting path) both call it.
// They lived as two independent floor divisions until they were lifted
// here; identical arithmetic written twice is one edit away from two
// different record counts over the same bytes, and nothing on either
// wire says which arm produced the number a caller is holding.
//
// count is always the FLOOR. trailing is payloadBytes % stride and is
// non-zero only for a truncated tail — a cohort whose last record was
// half-written. The two arms deliberately differ in what they do with
// it, and only in that: Inspect has an envelope and raises an
// ENCODING_INVALID warning naming the leftover bytes, while
// CountRecords returns (uint64, error) with no warning channel and
// stays silent, because it is a counter feeding an eligibility gate
// rather than a diagnostic and a truncated tail must not make a cohort
// that still processes fail to count. The count itself is identical on
// both arms by construction.
//
// ok is false when the stride is not positive (a field-less schema has
// no records to count) or payloadBytes is negative (the file is shorter
// than its own header + schema); the caller reports no count rather
// than a fabricated zero.
func (s *Schema) RecordCountForPayload(payloadBytes int64) (count, trailing int64, ok bool) {
	stride := int64(s.RecordByteSize())
	if stride <= 0 {
		return 0, 0, false
	}
	if payloadBytes < 0 {
		return 0, 0, false
	}
	return payloadBytes / stride, payloadBytes % stride, true
}
