package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/frankbardon/pulse/errors"
)

// ReusableRecord is the subset of *processing.Record needed by the reuse
// fast path. Declared here as an interface so encoding/ does not depend
// on processing/. Implementations (processing.Record) MUST clear their
// own null/wide maps before this call returns successfully; the reader
// populates them in place but only on fields where the value applies.
type ReusableRecord interface {
	SetNumeric(name string, value float64)
	SetNullField(name string)
	SetWideField(name string, value any)
	ClearForRow()
}

// ReadRecordReused reads one record into an existing ReusableRecord,
// reusing the record's internal maps. Returns io.EOF when the underlying
// reader is exhausted.
//
// Hot path semantics:
//   - Caller MUST consume the populated rec before the next call.
//   - The whole record stride (Schema.RecordByteSize(), including the
//     trailing null bitmap) is read into a reusable per-RecordReader
//     buffer with a SINGLE io.ReadFull, then every field is decoded from
//     a running cursor subslice of that buffer — no per-field read, no
//     copy. This eliminates the per-field io.ReadFull that dominates
//     wide-schema decode.
//   - Fields are walked in schema order with a running byte cursor, NOT by
//     Field.ByteOffset (bit-packed layout is cursor-driven; stored offsets
//     are unreliable). Each bit-packed field (u4, packed_bool) occupies a
//     whole on-wire byte, matching ReadBit/ReadNibble semantics.
//   - A short read at end-of-stream surfaces io.EOF exactly as before via
//     mapEOF; a partial trailing record surfaces as io.EOF as well
//     (io.ReadFull maps a nonzero short read to io.ErrUnexpectedEOF, which
//     mapEOF normalizes to io.EOF).
func (rr *RecordReader) ReadRecordReused(rec ReusableRecord) error {
	rec.ClearForRow()

	stride := rr.schema.RecordByteSize()
	if cap(rr.recBuf) < stride {
		rr.recBuf = make([]byte, stride)
	}
	buf := rr.recBuf[:stride]
	if _, err := io.ReadFull(rr.r, buf); err != nil {
		return mapEOF(err)
	}

	cursor := 0
	for _, field := range rr.schema.Fields {
		switch field.Type {
		case FieldTypePackedBool:
			// One whole byte on-wire; bit selected by BitPosition.
			b := buf[cursor]
			cursor++
			if (b>>uint(field.BitPosition))&1 == 1 {
				rec.SetNumeric(field.Name, 1)
			} else {
				rec.SetNumeric(field.Name, 0)
			}

		case FieldTypeU4:
			// One whole byte on-wire; high nibble when BitPosition > 0.
			b := buf[cursor]
			cursor++
			var v uint8
			if field.BitPosition > 0 {
				v = b >> 4
			} else {
				v = b & 0x0F
			}
			rec.SetNumeric(field.Name, float64(v))

		case FieldTypeDecimal128:
			var raw [16]byte
			copy(raw[:], buf[cursor:cursor+16])
			cursor += 16
			d := DecodeDecimal128(raw)
			rec.SetNumeric(field.Name, d.Float64(field.Scale))
			rec.SetWideField(field.Name, d)

		default:
			n := fixedWidthBytes(field.Type)
			if n == 0 {
				return errors.NewCodedError(errors.ENCODING_INVALID,
					fmt.Sprintf("unknown field type %d", field.Type))
			}
			sub := buf[cursor : cursor+n]
			cursor += n
			rec.SetNumeric(field.Name, decodeFixed(field.Type, sub))
			if field.Type.IsSet() {
				rec.SetWideField(field.Name, decodeSetMask(field.Type, sub))
			}
		}
	}

	// Trailing null bitmap, if the schema declares any nullable field.
	// It occupies the last bmSize bytes of the stride we already read.
	if bmSize := rr.schema.BitmapByteSize(); bmSize > 0 {
		bitmap := buf[cursor : cursor+bmSize]
		for i, field := range rr.schema.Fields {
			if !field.Nullable {
				continue
			}
			if BitmapIsNull(bitmap, i) {
				rec.SetNullField(field.Name)
				rec.SetNumeric(field.Name, 0)
			}
		}
	}

	return nil
}

// ReadRecordReusedWithPlan reads one record into an existing
// ReusableRecord by walking a precomputed DecodePlan, decoding only the
// fields the plan's DecodeFields segments carry and seeking past the
// SkipBytes ranges the caller will not consume. It is the plan-aware
// sibling of ReadRecordReused: same reuse contract (the record's
// null/wide maps are cleared once via ClearForRow, then populated in
// place), same io.EOF surfacing, but projection-honoring under reuse.
//
// plan == nil falls back to ReadRecordReused (full-decode reuse path),
// so an iterator that never installed a plan is byte-identical to today.
//
// keep governs record writes within DecodeFields segments, mirroring
// ReadRecordWithWidePlan / decodeFieldGroup / decodeBitmap exactly:
//
//   - Bit-packed members (u4, packed_bool) of a retained group are
//     ALWAYS consumed for cursor alignment (1 whole on-wire byte each),
//     but their record writes are suppressed when keep rejects them.
//   - The trailing bitmap segment reads the bitmap once and surfaces
//     nulls only for nullable fields the caller retains.
//
// Side effects match ReadRecordReused field-for-field:
//   - null → SetNullField(name) + SetNumeric(name, 0);
//   - decimal128 → SetNumeric(mean-ish scalar) + SetWideField(Decimal128);
//   - set_* → SetNumeric(float64 echo) + SetWideField(uint64 mask).
//
// Unlike ReadRecordReused, this path does NOT read the whole record
// stride: each DecodeFields group reads only its own on-wire bytes into
// the reusable buffer with a single io.ReadFull, and SkipBytes segments
// advance the reader past unread ranges with a single Seek (io.CopyN
// fallback). On a wide schema with a small retained set that is the
// projection win under reuse.
func (rr *RecordReader) ReadRecordReusedWithPlan(rec ReusableRecord, keep FieldFilter, plan *DecodePlan) error {
	if plan == nil {
		return rr.ReadRecordReused(rec)
	}

	rec.ClearForRow()

	// The trailing bitmap segment, if present, is always the LAST segment
	// in the plan (BuildDecodePlan appends it after every field-walk
	// flush). Detect it once so the per-segment dispatch below routes it
	// to the bitmap decoder rather than the field-group decoder.
	bitmapIdx := -1
	if rr.schema.HasBitmap() && len(plan.Segments) > 0 {
		last := len(plan.Segments) - 1
		if _, isDecode := plan.Segments[last].(DecodeFields); isDecode {
			bitmapIdx = last
		}
		// A trailing SkipBytes for the bitmap is handled transparently by
		// advanceReader — no special case needed.
	}

	segs := plan.Segments
	for i := 0; i < len(segs); i++ {
		switch seg := segs[i].(type) {
		case SkipBytes:
			if err := advanceReader(rr.r, seg.N); err != nil {
				return mapEOF(err)
			}

		case DecodeFields:
			if i == bitmapIdx {
				if err := rr.decodeBitmapReused(rec, keep); err != nil {
					return mapEOF(err)
				}
				continue
			}
			if err := rr.decodeFieldGroupReused(rec, keep, seg.Fields); err != nil {
				return mapEOF(err)
			}
		}
	}
	return nil
}

// groupOnWireBytes returns the on-wire byte width of a contiguous
// DecodeFields group: 1 byte per bit-packed member (u4, packed_bool —
// ReadBit/ReadNibble each consume a whole byte) and ByteSize() for every
// other field. Mirrors Schema.RecordByteSize accounting for the group.
func groupOnWireBytes(fields []*Field) int {
	total := 0
	for _, f := range fields {
		if f.Type.IsBitPacked() {
			total++
			continue
		}
		total += f.Type.ByteSize()
	}
	return total
}

// decodeFieldGroupReused reads the group's on-wire bytes into the
// reusable buffer with a single io.ReadFull, then decodes each field
// from a running cursor subslice — the S1 buffer-once style, scoped to
// the retained group. Record writes are suppressed for fields keep
// rejects, but every field's bytes are still consumed to keep the cursor
// aligned. Mirrors ReadRecordReused's per-field side effects exactly.
func (rr *RecordReader) decodeFieldGroupReused(rec ReusableRecord, keep FieldFilter, fields []*Field) error {
	groupBytes := groupOnWireBytes(fields)
	if groupBytes <= 0 {
		return nil
	}
	if cap(rr.recBuf) < groupBytes {
		rr.recBuf = make([]byte, groupBytes)
	}
	buf := rr.recBuf[:groupBytes]
	if _, err := io.ReadFull(rr.r, buf); err != nil {
		return err
	}

	cursor := 0
	for _, field := range fields {
		keepField := keep == nil || keep(field.Name)
		switch field.Type {
		case FieldTypePackedBool:
			b := buf[cursor]
			cursor++
			if !keepField {
				continue
			}
			if (b>>uint(field.BitPosition))&1 == 1 {
				rec.SetNumeric(field.Name, 1)
			} else {
				rec.SetNumeric(field.Name, 0)
			}

		case FieldTypeU4:
			b := buf[cursor]
			cursor++
			if !keepField {
				continue
			}
			var v uint8
			if field.BitPosition > 0 {
				v = b >> 4
			} else {
				v = b & 0x0F
			}
			rec.SetNumeric(field.Name, float64(v))

		case FieldTypeDecimal128:
			var raw [16]byte
			copy(raw[:], buf[cursor:cursor+16])
			cursor += 16
			if !keepField {
				continue
			}
			d := DecodeDecimal128(raw)
			rec.SetNumeric(field.Name, d.Float64(field.Scale))
			rec.SetWideField(field.Name, d)

		default:
			n := fixedWidthBytes(field.Type)
			if n == 0 {
				return errors.NewCodedError(errors.ENCODING_INVALID,
					fmt.Sprintf("unknown field type %d", field.Type))
			}
			sub := buf[cursor : cursor+n]
			cursor += n
			if !keepField {
				continue
			}
			rec.SetNumeric(field.Name, decodeFixed(field.Type, sub))
			if field.Type.IsSet() {
				rec.SetWideField(field.Name, decodeSetMask(field.Type, sub))
			}
		}
	}
	return nil
}

// decodeBitmapReused reads the trailing per-record null bitmap and
// surfaces nulls for retained nullable fields into the reusable record.
// Mirrors ReadRecordReused's bitmap branch and decodeBitmap's keep
// filtering: reads the bitmap once, walks every nullable schema field,
// and surfaces (SetNullField + SetNumeric 0) only for fields keep
// accepts.
func (rr *RecordReader) decodeBitmapReused(rec ReusableRecord, keep FieldFilter) error {
	bmSize := rr.schema.BitmapByteSize()
	if bmSize <= 0 {
		return nil
	}
	if cap(rr.recBuf) < bmSize {
		rr.recBuf = make([]byte, bmSize)
	}
	bitmap := rr.recBuf[:bmSize]
	if _, err := io.ReadFull(rr.r, bitmap); err != nil {
		return err
	}
	for i := range rr.schema.Fields {
		field := &rr.schema.Fields[i]
		if !field.Nullable {
			continue
		}
		if !BitmapIsNull(bitmap, i) {
			continue
		}
		if keep != nil && !keep(field.Name) {
			continue
		}
		rec.SetNullField(field.Name)
		rec.SetNumeric(field.Name, 0)
	}
	return nil
}

// fixedWidthBytes returns the on-wire width of a fixed-size scalar
// field type. Returns 0 for variable / bit-packed / 16-byte / unknown
// types so callers can fall through to typed readers.
func fixedWidthBytes(ft FieldType) int {
	switch ft {
	case FieldTypeU8, FieldTypeCategoricalU8, FieldTypeSetU8:
		return 1
	case FieldTypeU16, FieldTypeCategoricalU16, FieldTypeSetU16:
		return 2
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32, FieldTypeF32, FieldTypeSetU32:
		return 4
	case FieldTypeU64, FieldTypeF64, FieldTypeSetU64:
		return 8
	default:
		return 0
	}
}

// decodeFixed turns a scratch slice of the right width into the float64
// representation used by Record.values. Mirrors rawToFloat64 in reader.go
// but operates on bytes directly so no intermediate uint64 allocation
// is required (the uint64 is stack-resident).
func decodeFixed(ft FieldType, buf []byte) float64 {
	switch ft {
	case FieldTypeU8, FieldTypeCategoricalU8, FieldTypeSetU8:
		return float64(buf[0])
	case FieldTypeU16, FieldTypeCategoricalU16, FieldTypeSetU16:
		return float64(binary.LittleEndian.Uint16(buf))
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32, FieldTypeSetU32:
		return float64(binary.LittleEndian.Uint32(buf))
	case FieldTypeU64, FieldTypeSetU64:
		return float64(binary.LittleEndian.Uint64(buf))
	case FieldTypeF32:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(buf)))
	case FieldTypeF64:
		return math.Float64frombits(binary.LittleEndian.Uint64(buf))
	}
	return 0
}

// decodeSetMask returns the full uint64 bitmask payload for a set-typed
// field. Used to populate the wide map so consumers get bit-level
// precision (the float64 echo in values map can lose high bits for
// set_u64).
func decodeSetMask(ft FieldType, buf []byte) uint64 {
	switch ft {
	case FieldTypeSetU8:
		return uint64(buf[0])
	case FieldTypeSetU16:
		return uint64(binary.LittleEndian.Uint16(buf))
	case FieldTypeSetU32:
		return uint64(binary.LittleEndian.Uint32(buf))
	case FieldTypeSetU64:
		return binary.LittleEndian.Uint64(buf)
	}
	return 0
}

func mapEOF(err error) error {
	if err == nil {
		return nil
	}
	if err == io.EOF || isEOF(err) {
		return io.EOF
	}
	return err
}
