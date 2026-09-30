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
//
// ReusableRecord is the NAME-keyed contract: every write pays a hash of
// the field name. It is retained as a compatibility shim — an
// implementation of only this interface still decodes correctly, routed
// through nameKeyedShim — but a record that also implements
// IndexedReusableRecord is driven through the index-keyed sibling
// instead. The reader methods still take a ReusableRecord so an existing
// implementation keeps compiling unchanged.
type ReusableRecord interface {
	SetNumeric(name string, value float64)
	SetNullField(name string)
	SetWideField(name string, value any)
	ClearForRow()
}

// IndexedReusableRecord is the index-keyed sibling of ReusableRecord. It
// exists for the same reason ReusableRecord does — it lets the decoder
// populate a *processing.Record without encoding/ importing processing/
// — but its writes are keyed by the field's POSITION in the reader's
// schema (the index into Schema.Fields) rather than by name, so a
// positional record implementation can store the value with a slice
// index instead of a map hash.
//
// The position is the decoder's own schema-walk counter, never a
// name→index lookup: ReadRecordReused counts it as it walks
// Schema.Fields, ReadRecordReusedWithPlan carries it in
// DecodeFields.Indices (filled by BuildDecodePlan's walk), and the
// bitmap decoder uses the bitmap bit index, which IS the field position.
// An implementation therefore MUST be built over the same schema (or a
// structurally identical one — same field order) the RecordReader was
// constructed with; the index is meaningless against any other.
//
// Side-effect contract is identical to ReusableRecord, field for field:
// ClearForRow once per row; a null surfaces as SetNullFieldAt(i) followed
// by SetNumericAt(i, 0); decimal128 and set_* fields write both the
// numeric echo and the typed wide value.
//
// When a record implements both interfaces the index-keyed methods win
// and the name-keyed ones are never called by the reuse decoders. A
// record that also implements TypedSetRecord receives set masks through
// it instead of SetWideFieldAt, and one that implements RunSkipRecord
// gets BeginRunRow in place of ClearForRow and only its changed fields
// rewritten (reader_runskip.go).
type IndexedReusableRecord interface {
	SetNumericAt(idx int, value float64)
	SetNullFieldAt(idx int)
	SetWideFieldAt(idx int, value any)
	ClearForRow()
}

// TypedSetRecord is an optional extension of IndexedReusableRecord that
// takes a set field's mask with its concrete type instead of through
// `any`. Boxing a uint64 mask (256 and above) or an encoding.SetMask into
// an interface costs one heap allocation per set field per row; a record
// implementing this interface stores the mask in typed storage and the
// reuse decoders never box it. Decimal128 needs no typed twin: it is a
// single-pointer struct, which Go stores in an interface without
// allocating.
//
// The decoder checks for this interface once per decode call (a row on
// the full-stride path, a group on the plan path) and, when it is
// present, calls SetNarrowSetAt for set_u8..set_u64 and SetWideSetAt for
// set_u128 / set_u256 IN PLACE OF SetWideFieldAt. The value, and the
// SetNumericAt echo written before it, are identical to the boxed path.
type TypedSetRecord interface {
	SetNarrowSetAt(idx int, mask uint64)
	SetWideSetAt(idx int, m SetMask)
}

// putSetField writes a set field's wide value: typed when the sink
// supports it, boxed through SetWideFieldAt otherwise.
func putSetField(sink IndexedReusableRecord, typed TypedSetRecord, ft FieldType, fi int, sub []byte) error {
	if ft.IsWideSet() {
		m, err := SetMaskFromBytes(ft, sub)
		if err != nil {
			return err
		}
		if typed != nil {
			typed.SetWideSetAt(fi, m)
		} else {
			sink.SetWideFieldAt(fi, m)
		}
		return nil
	}
	mask := decodeSetMask(ft, sub)
	if typed != nil {
		typed.SetNarrowSetAt(fi, mask)
	} else {
		sink.SetWideFieldAt(fi, mask)
	}
	return nil
}

// nameKeyedShim adapts a name-keyed ReusableRecord to the index-keyed
// decoder by resolving the position back to Field.Name — a direct slice
// index, not a lookup. It is embedded by value in RecordReader so
// adapting a record costs no allocation per row.
type nameKeyedShim struct {
	fields []Field
	rec    ReusableRecord
}

func (s *nameKeyedShim) SetNumericAt(idx int, value float64) {
	s.rec.SetNumeric(s.fields[idx].Name, value)
}

func (s *nameKeyedShim) SetNullFieldAt(idx int) {
	s.rec.SetNullField(s.fields[idx].Name)
}

func (s *nameKeyedShim) SetWideFieldAt(idx int, value any) {
	s.rec.SetWideField(s.fields[idx].Name, value)
}

func (s *nameKeyedShim) ClearForRow() {
	s.rec.ClearForRow()
}

// indexedSink returns the index-keyed view of rec: rec itself when it
// implements IndexedReusableRecord (index-keyed wins), otherwise the
// reader's name-keyed shim bound to rec and the reader's schema.
func (rr *RecordReader) indexedSink(rec ReusableRecord) IndexedReusableRecord {
	if ix, ok := rec.(IndexedReusableRecord); ok {
		return ix
	}
	rr.shim.fields = rr.schema.Fields
	rr.shim.rec = rec
	return &rr.shim
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
//   - A record implementing RunSkipRecord is decoded by readStrideRunSkip:
//     same result, but fields whose bytes repeat the previous row are not
//     rewritten.
func (rr *RecordReader) ReadRecordReused(rec ReusableRecord) error {
	if rr.gd != nil {
		return rr.readGrouped(rec, nil, nil)
	}
	sink := rr.indexedSink(rec)
	if rs, ok := sink.(RunSkipRecord); ok {
		return rr.readStrideRunSkip(rs)
	}
	sink.ClearForRow()

	stride := rr.strideLayout().stride
	if cap(rr.recBuf) < stride {
		rr.recBuf = make([]byte, stride)
	}
	buf := rr.recBuf[:stride]
	if _, err := io.ReadFull(rr.r, buf); err != nil {
		return mapEOF(err)
	}
	return rr.decodeStrideIndexed(sink, buf)
}

// decodeStrideIndexed decodes one whole record stride already resident
// in buf into sink. It is the full-decode body of ReadRecordReused, split
// out so the row bytes and the write loop are separable: run-skip
// (decodeStrideChanged) compares buf against the previous row instead
// when it can keep the record's state.
//
// The schema position handed to every sink write is fi, the index of the
// running walk over rr.schema.Fields — the same walk that advances the
// byte cursor. No name is consulted.
func (rr *RecordReader) decodeStrideIndexed(sink IndexedReusableRecord, buf []byte) error {
	typed, _ := sink.(TypedSetRecord)
	cursor := 0
	fields := rr.schema.Fields
	for fi := range fields {
		field := &fields[fi]
		switch field.Type {
		case FieldTypePackedBool:
			// One whole byte on-wire; bit selected by BitPosition.
			b := buf[cursor]
			cursor++
			if (b>>uint(field.BitPosition))&1 == 1 {
				sink.SetNumericAt(fi, 1)
			} else {
				sink.SetNumericAt(fi, 0)
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
			sink.SetNumericAt(fi, float64(v))

		case FieldTypeDecimal128:
			var raw [16]byte
			copy(raw[:], buf[cursor:cursor+16])
			cursor += 16
			d := DecodeDecimal128(raw)
			sink.SetNumericAt(fi, d.Float64(field.Scale))
			sink.SetWideFieldAt(fi, d)

		default:
			n := fixedWidthBytes(field.Type)
			if n == 0 {
				return errors.NewCodedError(errors.ENCODING_INVALID,
					fmt.Sprintf("unknown field type %d", field.Type))
			}
			sub := buf[cursor : cursor+n]
			cursor += n
			sink.SetNumericAt(fi, decodeFixed(field.Type, sub))
			if field.Type.IsSet() {
				if err := putSetField(sink, typed, field.Type, fi, sub); err != nil {
					return err
				}
			}
		}
	}

	// Trailing null bitmap, if the schema declares any nullable field.
	// It occupies the last bmSize bytes of the stride we already read.
	// The bitmap bit index IS the schema position.
	if bmSize := rr.strideLayout().bmSize; bmSize > 0 {
		bitmap := buf[cursor : cursor+bmSize]
		for i := range fields {
			if !fields[i].Nullable {
				continue
			}
			if BitmapIsNull(bitmap, i) {
				sink.SetNullFieldAt(i)
				sink.SetNumericAt(i, 0)
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
//   - set_u8..set_u64 → SetNumeric(float64 echo) + SetWideField(uint64
//     mask); set_u128 / set_u256 → SetNumeric(low-64-bit echo) +
//     SetWideField(SetMask).
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
	if rr.gd != nil {
		return rr.readGrouped(rec, keep, plan)
	}

	sink := rr.indexedSink(rec)
	if rs, ok := sink.(RunSkipRecord); ok {
		return rr.readPlanRunSkip(rs, keep, plan)
	}
	sink.ClearForRow()

	// The trailing bitmap segment, if present, is always the LAST segment
	// in the plan (BuildDecodePlan appends it after every field-walk
	// flush). Detect it once so the per-segment dispatch below routes it
	// to the bitmap decoder rather than the field-group decoder.
	bitmapIdx := rr.planBitmapIdx(plan)

	segs := plan.Segments
	for i := 0; i < len(segs); i++ {
		switch seg := segs[i].(type) {
		case SkipBytes:
			if err := advanceReader(rr.r, seg.N); err != nil {
				return mapEOF(err)
			}

		case DecodeFields:
			if i == bitmapIdx {
				if err := rr.decodeBitmapReused(sink, keep); err != nil {
					return mapEOF(err)
				}
				continue
			}
			indices, err := rr.segmentIndices(seg)
			if err != nil {
				return err
			}
			if err := rr.decodeFieldGroupReused(sink, keep, seg.Fields, indices); err != nil {
				return mapEOF(err)
			}
		}
	}
	return nil
}

// planBitmapIdx returns the index of plan's trailing bitmap DecodeFields
// segment, or -1 when the plan decodes no bitmap. The bitmap segment, if
// present, is always the LAST segment (BuildDecodePlan appends it after
// every field-walk flush); a trailing SkipBytes for the bitmap is
// handled transparently by advanceReader — no special case needed.
func (rr *RecordReader) planBitmapIdx(plan *DecodePlan) int {
	if rr.schema.HasBitmap() && len(plan.Segments) > 0 {
		last := len(plan.Segments) - 1
		if _, isDecode := plan.Segments[last].(DecodeFields); isDecode {
			return last
		}
	}
	return -1
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
//
// indices[k] is the schema position of fields[k] (DecodeFields.Indices,
// resolved by segmentIndices); every sink write is keyed by it.
func (rr *RecordReader) decodeFieldGroupReused(sink IndexedReusableRecord, keep FieldFilter, fields []*Field, indices []int) error {
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
	return decodeGroupBytes(sink, keep, fields, indices, buf)
}

// decodeGroupBytes decodes one DecodeFields group whose on-wire bytes
// are already resident in buf: the write loop of decodeFieldGroupReused,
// shared with the run-skip plan path's write-everything arm.
func decodeGroupBytes(sink IndexedReusableRecord, keep FieldFilter, fields []*Field, indices []int, buf []byte) error {
	typed, _ := sink.(TypedSetRecord)
	cursor := 0
	for gi, field := range fields {
		fi := indices[gi]
		keepField := keep == nil || keep(field.Name)
		switch field.Type {
		case FieldTypePackedBool:
			b := buf[cursor]
			cursor++
			if !keepField {
				continue
			}
			if (b>>uint(field.BitPosition))&1 == 1 {
				sink.SetNumericAt(fi, 1)
			} else {
				sink.SetNumericAt(fi, 0)
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
			sink.SetNumericAt(fi, float64(v))

		case FieldTypeDecimal128:
			var raw [16]byte
			copy(raw[:], buf[cursor:cursor+16])
			cursor += 16
			if !keepField {
				continue
			}
			d := DecodeDecimal128(raw)
			sink.SetNumericAt(fi, d.Float64(field.Scale))
			sink.SetWideFieldAt(fi, d)

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
			sink.SetNumericAt(fi, decodeFixed(field.Type, sub))
			if field.Type.IsSet() {
				if err := putSetField(sink, typed, field.Type, fi, sub); err != nil {
					return err
				}
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
func (rr *RecordReader) decodeBitmapReused(sink IndexedReusableRecord, keep FieldFilter) error {
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
	rr.decodeBitmapBytes(sink, keep, bitmap)
	return nil
}

// decodeBitmapBytes surfaces the nulls of a bitmap already resident in
// bitmap: the write loop of decodeBitmapReused, shared with the run-skip
// plan path's write-everything arm.
func (rr *RecordReader) decodeBitmapBytes(sink IndexedReusableRecord, keep FieldFilter, bitmap []byte) {
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
		sink.SetNullFieldAt(i)
		sink.SetNumericAt(i, 0)
	}
}

// segmentIndices returns the schema position of every field in seg.
// A plan from BuildDecodePlan carries them in DecodeFields.Indices,
// recorded by the builder's own walk of Schema.Fields, so the hot path
// is a slice hand-off. DecodeFields is an exported struct, though, and a
// hand-built segment may omit Indices; for that case only, the positions
// are recovered by matching each *Field against rr.schema.Fields (by
// pointer, then by name) so an older hand-built plan keeps decoding
// correctly rather than panicking. A field absent from the reader's
// schema has no position and is an ENCODING_INVALID plan.
func (rr *RecordReader) segmentIndices(seg DecodeFields) ([]int, error) {
	if len(seg.Indices) == len(seg.Fields) {
		return seg.Indices, nil
	}
	return resolveSegmentIndices(rr.schema, seg.Fields)
}

// resolveSegmentIndices is the slow fallback behind segmentIndices for a
// DecodeFields segment built without Indices. Not on the BuildDecodePlan
// path.
func resolveSegmentIndices(schema *Schema, fields []*Field) ([]int, error) {
	out := make([]int, len(fields))
	for k, f := range fields {
		out[k] = -1
		for i := range schema.Fields {
			if &schema.Fields[i] == f {
				out[k] = i
				break
			}
		}
		if out[k] >= 0 {
			continue
		}
		for i := range schema.Fields {
			if schema.Fields[i].Name == f.Name {
				out[k] = i
				break
			}
		}
		if out[k] < 0 {
			return nil, errors.NewCodedError(errors.ENCODING_INVALID,
				fmt.Sprintf("decode plan names field %q absent from the reader schema", f.Name))
		}
	}
	return out, nil
}

// fixedWidthBytes returns the on-wire width of a field type the
// buffer-once decoder can consume as one contiguous run of bytes.
// Returns 0 for bit-packed, decimal128 and unknown types so callers
// fall through to their own explicit case (decimal128) or raise
// ENCODING_INVALID (unknown).
//
// The wide set rungs DO answer here (16 and 32 bytes): unlike
// decimal128 they have no dedicated case in the caller's switch, and a
// 0 would make a legal field type indistinguishable from an unknown
// type byte. Their payload is still handed to SetMaskFromBytes rather
// than decoded here — this function only sizes the run.
func fixedWidthBytes(ft FieldType) int {
	switch ft {
	case FieldTypeU8, FieldTypeCategoricalU8, FieldTypeSetU8:
		return 1
	case FieldTypeU16, FieldTypeCategoricalU16, FieldTypeSetU16:
		return 2
	case FieldTypeU32, FieldTypeDate, FieldTypeCategoricalU32, FieldTypeF32, FieldTypeSetU32:
		return 4
	case FieldTypeU64, FieldTypeF64, FieldTypeSetU64, FieldTypeDateTime:
		return 8
	case FieldTypeSetU128:
		return 16
	case FieldTypeSetU256:
		return 32
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
	case FieldTypeU64, FieldTypeSetU64, FieldTypeDateTime:
		return float64(binary.LittleEndian.Uint64(buf))
	case FieldTypeF32:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(buf)))
	case FieldTypeF64:
		return math.Float64frombits(binary.LittleEndian.Uint64(buf))
	case FieldTypeSetU128, FieldTypeSetU256:
		// Wide sets echo their LOW 64 bits — words[0] is the low word and
		// occupies the first 8 bytes of the payload, so this is exactly
		// setMaskFloatEcho of the mask the wide map carries. Deliberately
		// lossy; the SetMask in the wide map is authoritative.
		return float64(binary.LittleEndian.Uint64(buf[:8]))
	}
	return 0
}

// decodeSetMask returns the full uint64 bitmask payload for a NARROW
// set-typed field (set_u8..set_u64). Used to populate the wide map so
// consumers get bit-level precision (the float64 echo in values map can
// lose high bits for set_u64).
//
// The wide rungs are absent on purpose: their mask does not fit a
// uint64, so they route through SetMaskFromBytes and land in the wide
// map as a SetMask. A wide rung reaching here would read as an empty
// selection, so callers must branch on FieldType.IsWideSet() first.
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
