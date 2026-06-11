package encoding

// DecodePlan is a pre-computed walk of one record's on-wire bytes that
// elides ranges the caller will not consume. It is produced by
// Schema.BuildDecodePlan given a retained-field set, and consumed by
// the streaming iterator (introduced in E2-S2) so unprojected byte
// ranges turn into a single advance-cursor step instead of a per-field
// typed read.
//
// A DecodePlan is a pure function of (Schema, retained-set). It carries
// no file content, no offsets into a particular reader, and no mutable
// state. Two calls with the same inputs return byte-equivalent Segments
// in identical order.
type DecodePlan struct {
	Segments []Segment
}

// Segment is the sealed sum type of decode steps emitted by
// Schema.BuildDecodePlan. The unexported marker method blocks
// out-of-package implementations so the iterator's switch is exhaustive.
type Segment interface {
	isSegment()
}

// SkipBytes advances the underlying reader by N bytes without decoding.
// Coalesces a run of contiguous unprojected, non-bit-packed fields, or
// an entire bit-packed group when no member is retained, or the trailing
// null bitmap when no nullable field is retained. N > 0 always.
type SkipBytes struct {
	N int
}

func (SkipBytes) isSegment() {}

// DecodeFields runs the existing per-field decoder loop for the listed
// fields together. The Fields slice is in schema order; the caller walks
// it exactly as the unprojected decoder would, so bit-packed neighbours
// (which on-wire each consume one byte via ReadBit / ReadNibble) stay
// correctly aligned.
//
// Fields is non-empty for any segment the builder emits.
type DecodeFields struct {
	Fields []*Field
}

func (DecodeFields) isSegment() {}

// BuildDecodePlan produces a deterministic DecodePlan for this schema
// given the set of field names the caller intends to consume.
//
// Rules:
//
//   - Contiguous unprojected, non-bit-packed fields coalesce into a
//     single SkipBytes whose N is the sum of those fields' ByteSize().
//   - Bit-packed neighbours (any contiguous run of FieldType.IsBitPacked()
//     fields) form one group. The group becomes one DecodeFields segment
//     if any member is retained; otherwise it becomes one SkipBytes
//     whose N equals the group's on-wire byte size — which mirrors
//     Schema.RecordByteSize: 1 byte per bit-packed field. (BitPosition
//     is a per-field annotation; ReadBit / ReadNibble each consume a
//     full byte today, so a run of K bit-packed fields occupies K
//     bytes on-wire.)
//   - Trailing null bitmap: if Schema.HasBitmap() and any nullable
//     field is retained, append a DecodeFields segment carrying every
//     nullable field in schema order (the iterator decodes the
//     bitmap, then walks this list to surface null flags). If
//     HasBitmap() but no nullable field is retained, append a
//     SkipBytes{N: Schema.BitmapByteSize()}. If !HasBitmap(), no
//     bitmap segment is emitted.
//   - Empty retained set: the plan is a single SkipBytes covering the
//     full record stride (RecordByteSize, which already includes the
//     bitmap if any).
//   - Full retained set (every schema field present in retained): no
//     SkipBytes segments — the output is equivalent to the existing
//     per-field walk.
//
// retained is consulted as a set (order does not matter); duplicates
// and names not present in the schema are silently ignored. The function
// never returns an error today, but the signature carries one for
// forward-compatibility with future shape validation (e.g. once
// extension-driven retained sets carry wildcard tokens).
func (s *Schema) BuildDecodePlan(retained []string) (*DecodePlan, error) {
	plan := &DecodePlan{}

	// Materialize retained as a hash set for O(1) lookup. A nil retained
	// slice and an empty retained slice are both treated as "nothing
	// kept" — the iterator wouldn't typically construct this case but
	// the contract is well-defined.
	keep := make(map[string]struct{}, len(retained))
	for _, name := range retained {
		keep[name] = struct{}{}
	}

	// Fast path: empty retained set ⇒ single SkipBytes for the whole
	// record stride (includes the trailing bitmap when present).
	if len(keep) == 0 {
		if stride := s.RecordByteSize(); stride > 0 {
			plan.Segments = append(plan.Segments, SkipBytes{N: stride})
		}
		return plan, nil
	}

	// pendingSkip accumulates the byte width of consecutive unprojected
	// non-bit-packed fields. pendingDecode accumulates retained fields
	// that the current decode segment should walk. We flush these on
	// transitions (skip↔decode, or whenever a bit-packed group ends).
	var pendingSkip int
	var pendingDecode []*Field

	flushSkip := func() {
		if pendingSkip > 0 {
			plan.Segments = append(plan.Segments, SkipBytes{N: pendingSkip})
			pendingSkip = 0
		}
	}
	flushDecode := func() {
		if len(pendingDecode) > 0 {
			// Copy so subsequent appends don't reuse the underlying
			// array — the iterator may retain the slice header.
			fields := make([]*Field, len(pendingDecode))
			copy(fields, pendingDecode)
			plan.Segments = append(plan.Segments, DecodeFields{Fields: fields})
			pendingDecode = pendingDecode[:0]
		}
	}

	i := 0
	for i < len(s.Fields) {
		f := &s.Fields[i]
		if f.Type.IsBitPacked() {
			// Identify the full contiguous bit-packed run starting at i.
			j := i
			anyRetained := false
			for j < len(s.Fields) && s.Fields[j].Type.IsBitPacked() {
				if _, ok := keep[s.Fields[j].Name]; ok {
					anyRetained = true
				}
				j++
			}
			// On-wire byte size of the group: 1 byte per bit-packed
			// field, mirroring Schema.RecordByteSize. ReadBit and
			// ReadNibble each consume a full byte today even though the
			// stored value is < 8 bits.
			groupBytes := j - i

			if anyRetained {
				// Flush any pending skip so the decode segment starts
				// cleanly. Then emit ONE DecodeFields covering every
				// member of the group — even members the caller did not
				// retain, because the iterator walks the group together
				// (ReadBit / ReadNibble each consume a byte and the
				// loop must visit every field to stay aligned).
				flushSkip()
				for k := i; k < j; k++ {
					pendingDecode = append(pendingDecode, &s.Fields[k])
				}
				flushDecode()
			} else {
				// Whole bit-packed group is unprojected ⇒ fold into the
				// running skip accumulator. Flush any pending decode
				// first so we don't merge across an unprojected run.
				flushDecode()
				pendingSkip += groupBytes
			}
			i = j
			continue
		}

		// Non-bit-packed field.
		if _, ok := keep[f.Name]; ok {
			// Retained ⇒ flush any pending skip, then add to the
			// current decode batch.
			flushSkip()
			pendingDecode = append(pendingDecode, f)
		} else {
			// Unprojected ⇒ flush any pending decode, then add this
			// field's byte width to the skip accumulator.
			flushDecode()
			pendingSkip += f.Type.ByteSize()
		}
		i++
	}

	// Flush trailing accumulators from the field loop.
	flushSkip()
	flushDecode()

	// Trailing null bitmap segment, if any.
	if s.HasBitmap() {
		// Does the retained set include at least one nullable field?
		anyNullableRetained := false
		var nullable []*Field
		for k := range s.Fields {
			f := &s.Fields[k]
			if !f.Nullable {
				continue
			}
			nullable = append(nullable, f)
			if _, ok := keep[f.Name]; ok {
				anyNullableRetained = true
			}
		}
		bitmapBytes := s.BitmapByteSize()
		if anyNullableRetained {
			// Iterator decodes the bitmap and surfaces null flags for
			// every nullable field; the field slice carries the full
			// nullable set (including unprojected ones — the iterator
			// can still filter map writes there, mirroring the existing
			// ReadRecordWithWideProjected behaviour). Carrying the
			// nullable set in schema order keeps the plan
			// self-contained: the iterator does not need to re-walk
			// the schema after consulting the segment.
			plan.Segments = append(plan.Segments, DecodeFields{Fields: nullable})
		} else if bitmapBytes > 0 {
			plan.Segments = append(plan.Segments, SkipBytes{N: bitmapBytes})
		}
	}

	return plan, nil
}
