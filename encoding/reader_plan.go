package encoding

import (
	"io"
)

// ReadRecordWithWidePlan reads one record by walking a precomputed
// DecodePlan instead of iterating every schema field. SkipBytes
// segments advance the underlying reader by N bytes with a single seek
// (or io.CopyN fallback); DecodeFields segments run the same per-field
// decode loop the unprojected reader uses, but only for the fields the
// segment carries.
//
// keep governs map writes within DecodeFields segments. The plan builder
// emits bit-packed groups together (every member of a group whose
// retained subset is non-empty), so bit-packed members that the caller
// did NOT request are still decoded — their on-wire bytes must be
// consumed to keep the cursor aligned — but their map writes are
// suppressed by keep. Similarly the trailing bitmap segment carries
// every nullable field so the iterator can read the bitmap once and
// surface nulls; keep filters those map writes.
//
// keep == nil and plan == nil both fall back to the existing full-decode
// behaviour. Passing keep == nil with a non-nil plan widens every
// DecodeFields segment to "keep all members" (useful for golden parity
// tests).
//
// Behaviour invariants matched against the per-field readRecord loop:
//   - Caller-supplied maps are cleared in place before population.
//   - Decimal128 fields populate wide[name] with the typed Decimal128
//     value (only when wide != nil and the field is retained).
//   - Set-typed fields populate wide[name] with the raw uint64 mask
//     (only when wide != nil and the field is retained).
//   - Null surfacing clears values[name] back to 0 and deletes wide[name]
//     for nullable fields the bitmap marks null AND the caller retains.
func (rr *RecordReader) ReadRecordWithWidePlan(
	values map[string]float64,
	nulls map[string]bool,
	wide map[string]any,
	keep FieldFilter,
	plan *DecodePlan,
) error {
	if plan == nil {
		return rr.readRecord(values, nulls, wide, keep)
	}

	// Clear caller-provided maps. Mirrors readRecord.
	for k := range values {
		delete(values, k)
	}
	for k := range nulls {
		delete(nulls, k)
	}
	for k := range wide {
		delete(wide, k)
	}

	// The trailing bitmap segment, if present, is always the LAST
	// segment in the plan (BuildDecodePlan appends it after every
	// field-walk flush). Detect it once so the per-segment dispatch
	// below doesn't have to re-check.
	bitmapIdx := -1
	if rr.schema.HasBitmap() && len(plan.Segments) > 0 {
		last := len(plan.Segments) - 1
		if _, isDecode := plan.Segments[last].(DecodeFields); isDecode {
			bitmapIdx = last
		}
		// If the last segment is SkipBytes, the bitmap is just a
		// generic byte skip — advanceReader handles it transparently.
	}

	// Walk segments by index (no range-copy of the Segment interface)
	// so the per-record hot path stays as cheap as possible.
	segs := plan.Segments
	for i := 0; i < len(segs); i++ {
		switch seg := segs[i].(type) {
		case SkipBytes:
			if err := advanceReader(rr.r, seg.N); err != nil {
				if isEOF(err) {
					return io.EOF
				}
				return err
			}

		case DecodeFields:
			if i == bitmapIdx {
				if err := rr.decodeBitmap(values, nulls, wide, keep); err != nil {
					if isEOF(err) {
						return io.EOF
					}
					return err
				}
				continue
			}
			if err := rr.decodeFieldGroup(values, nulls, wide, keep, seg.Fields); err != nil {
				if isEOF(err) {
					return io.EOF
				}
				return err
			}
		}
	}
	return nil
}

// advanceReader skips n bytes from the reader without decoding. The
// fast path is io.Seeker (satisfied by *bytes.Reader, which the
// streaming iterator wraps around both mmap and afero.ReadFile bytes).
// The fallback is io.CopyN, which works for any io.Reader but allocates
// a small discard buffer per call.
//
// n is assumed > 0 — the plan builder never emits SkipBytes{N:0}.
func advanceReader(r io.Reader, n int) error {
	if n <= 0 {
		return nil
	}
	if sk, ok := r.(io.Seeker); ok {
		_, err := sk.Seek(int64(n), io.SeekCurrent)
		return err
	}
	_, err := io.CopyN(io.Discard, r, int64(n))
	return err
}

// decodeFieldGroup runs the existing per-field decoder for each field
// in the group, writing only to maps for fields keep accepts. The
// caller has already verified plan != nil; the group is a non-bitmap
// DecodeFields segment.
//
// Mirrors the readRecord per-field switch exactly so byte-equivalence
// holds.
func (rr *RecordReader) decodeFieldGroup(
	values map[string]float64,
	nulls map[string]bool,
	wide map[string]any,
	keep FieldFilter,
	fields []*Field,
) error {
	for fi := 0; fi < len(fields); fi++ {
		field := fields[fi]
		keepField := keep == nil || keep(field.Name)
		switch field.Type {
		case FieldTypePackedBool:
			v, err := ReadBit(rr.r, uint(field.BitPosition))
			if err != nil {
				return err
			}
			if !keepField {
				continue
			}
			if v {
				values[field.Name] = 1
			} else {
				values[field.Name] = 0
			}

		case FieldTypeU4:
			v, err := ReadNibble(rr.r, field.BitPosition > 0)
			if err != nil {
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = float64(v)

		case FieldTypeDecimal128:
			d, err := ReadDecimal128(rr.r)
			if err != nil {
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = d.Float64(field.Scale)
			if wide != nil {
				wide[field.Name] = d
			}

		default:
			raw, err := ReadFieldValue(rr.r, field.Type)
			if err != nil {
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = rawToFloat64(field.Type, raw)
			if field.Type.IsSet() && wide != nil {
				wide[field.Name] = raw
			}
		}
	}
	return nil
}

// decodeBitmap reads the trailing per-record null bitmap and surfaces
// nulls for retained nullable fields. Mirrors the bitmap branch of the
// per-field readRecord loop exactly.
func (rr *RecordReader) decodeBitmap(
	values map[string]float64,
	nulls map[string]bool,
	wide map[string]any,
	keep FieldFilter,
) error {
	bmSize := rr.schema.BitmapByteSize()
	if bmSize <= 0 {
		return nil
	}
	bitmap, err := ReadBitmap(rr.r, bmSize)
	if err != nil {
		return err
	}
	schemaFields := rr.schema.Fields
	for i := 0; i < len(schemaFields); i++ {
		field := &schemaFields[i]
		if !field.Nullable {
			continue
		}
		if !BitmapIsNull(bitmap, i) {
			continue
		}
		keepField := keep == nil || keep(field.Name)
		if !keepField {
			continue
		}
		nulls[field.Name] = true
		values[field.Name] = 0
		if wide != nil {
			delete(wide, field.Name)
		}
	}
	return nil
}
