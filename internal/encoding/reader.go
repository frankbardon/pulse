package encoding

import (
	"io"
	"math"

	"github.com/frankbardon/pulse/encoding"
)

// RecordReader reads records one at a time from a binary stream.
// It reads directly from the io.Reader without buffering the entire file.
type RecordReader struct {
	r      io.Reader
	schema *encoding.Schema
	// groups is the logical-stream wrapper over a grouped (0x02)
	// cohort's physical rows; nil for an ungrouped schema. schema is
	// then the grouped schema's Logical() view.
	groups *logicalReader
	// gd decodes a grouped cohort's PHYSICAL rows straight into a reuse
	// record (group_decode.go): the reuse paths never expand a row. nil
	// for an ungrouped schema.
	gd *groupedDecoder

	// recBuf is a reusable per-record scratch buffer owned by the
	// RecordReader. ReadRecordReused reads the whole record stride into
	// this buffer with a single io.ReadFull, then decodes each field from
	// a running cursor subslice — eliminating the per-field io.ReadFull
	// that dominates wide-schema decode. Grown on demand, never shrunk.
	recBuf []byte

	// shim adapts a name-keyed ReusableRecord to the index-keyed reuse
	// decoder. Held by value so binding a record per row allocates
	// nothing; see indexedSink.
	shim nameKeyedShim

	// Run-skip state (reader_runskip.go). prevRow holds the on-wire
	// bytes of the last row decoded into prevSink — the whole stride on
	// the full path, the concatenated DecodeFields groups of prevPlan on
	// the plan path — and prevValid says it may be compared against.
	// Swapped with recBuf after each run-skip decode, never copied.
	runToken uint64
	// Backoff probe state (runCanKeep / noteKeptRow): rows left to
	// decode without comparing, and the current probe window's tallies.
	runBackoff    int
	runWinRows    int
	runWinWritten int
	runWinFields  int
	prevRow       []byte
	prevValid     bool
	prevSink      RunSkipRecord
	prevPlan      *DecodePlan
	// layout caches the schema-derived row geometry (stride, bitmap
	// size, per-field on-wire spans) the reuse decoders need every row;
	// built once per reader (strideLayout).
	layout strideLayout
	// shape caches the per-segment widths of the plan last driven
	// through the run-skip plan path.
	shape runPlanShape
}

// NewRecordReader creates a RecordReader. The reader must be positioned
// immediately after the header and schema (i.e., at the first record byte).
//
// For a grouped (0x02) schema the reader decodes the LOGICAL record:
// same field indices, same values, same nulls as the ungrouped twin.
// The map decoders read r through the logical stream (every physical
// row expanded into the exact row the twin stores); the reuse decoders
// (ReadRecordReused*, run-skip included) decode the physical row
// directly and never expand it (group_decode.go). Exactly one physical
// row is consumed per record on every path, so r's position after a
// record is the end of that physical record.
func NewRecordReader(r io.Reader, schema *encoding.Schema) *RecordReader {
	if schema != nil && schema.HasGroups() {
		lr, logical, err := NewLogicalStream(r, schema)
		if err != nil {
			return &RecordReader{r: errReader{err}, schema: schema.Logical()}
		}
		d, err := newGroupedDecoder(schema)
		if err != nil {
			return &RecordReader{r: errReader{err}, schema: schema.Logical()}
		}
		rr := &RecordReader{r: lr, schema: logical, gd: d}
		rr.groups, _ = lr.(*logicalReader)
		return rr
	}
	return &RecordReader{r: r, schema: schema}
}

// errReader fails every read with err (a grouped schema whose layout
// could not be compiled).
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// GroupIndex reports the dictionary entry index group g (a position in
// the grouped schema's Groups) resolved to on the record just decoded:
// every member of g carries that entry's values. ok is false for an
// ungrouped schema, before the first record, or when the last record
// was skipped whole by a decode plan. It is the row -> entry map a
// per-entry precompute (filter precompute) tests instead of decoding the
// members.
func (rr *RecordReader) GroupIndex(g int) (uint32, bool) {
	if rr.groups == nil {
		return 0, false
	}
	if rr.gd != nil && rr.gd.read {
		return rr.gd.idx[g], true
	}
	return rr.groups.GroupIndex(g)
}

// noteLogicalRead marks that the next record is decoded through the
// logical stream, so GroupIndex reports that stream's row.
func (rr *RecordReader) noteLogicalRead() {
	if rr.gd != nil {
		rr.gd.read = false
	}
}

// ReadRecord reads a single record from the stream, populating the values and
// nulls maps. Returns io.EOF when no more records are available.
//
// The caller provides pre-allocated maps to avoid per-record allocation.
// Maps are cleared at the start of each call.
//
// Reuse contract: the maps are owned by the caller. ReadRecord does not retain
// references to them after returning. If the caller plans to reuse the same
// maps across calls (the typical pattern), they must consume the populated
// values BEFORE invoking ReadRecord again, because the next call clears and
// repopulates the maps in-place. If the caller needs to retain the values
// past the next call (e.g., collecting Records into a slice for later
// aggregation), it must pass distinct map instances per record OR copy the
// contents out before the next ReadRecord call.
//
// To populate typed wide values for fields whose representation does not
// fit in float64 (decimal128), call ReadRecordWithWide instead and pass a
// third map.
func (rr *RecordReader) ReadRecord(values map[string]float64, nulls map[string]bool) error {
	return rr.ReadRecordWithWide(values, nulls, nil)
}

// FieldFilter returns true for field names whose values should be
// written into the caller's maps. Used by ReadRecordWithWideProjected
// to skip map writes for fields the request doesn't read. A nil
// FieldFilter is equivalent to "keep every field."
type FieldFilter func(name string) bool

// ReadRecordWithWide reads a record and populates a wide map with typed
// values for decimal128 fields. The wide map may be nil to skip wide
// population.
func (rr *RecordReader) ReadRecordWithWide(values map[string]float64, nulls map[string]bool, wide map[string]any) error {
	return rr.readRecord(values, nulls, wide, nil)
}

// ReadRecordWithWideProjected reads a record but only writes the
// fields for which keep(name) returns true into the caller's maps.
// Bytes for excluded fields are still consumed from the underlying
// reader so byte offsets stay aligned — projection saves map
// allocations, not decode work.
//
// keep == nil falls back to the full-decode path.
func (rr *RecordReader) ReadRecordWithWideProjected(values map[string]float64, nulls map[string]bool, wide map[string]any, keep FieldFilter) error {
	return rr.readRecord(values, nulls, wide, keep)
}

func (rr *RecordReader) readRecord(values map[string]float64, nulls map[string]bool, wide map[string]any, keep FieldFilter) error {
	rr.noteLogicalRead()
	// Clear caller-provided maps.
	for k := range values {
		delete(values, k)
	}
	for k := range nulls {
		delete(nulls, k)
	}
	for k := range wide {
		delete(wide, k)
	}

	for _, field := range rr.schema.Fields {
		keepField := keep == nil || keep(field.Name)
		switch field.Type {
		case encoding.FieldTypePackedBool:
			v, err := encoding.ReadBit(rr.r, uint(field.BitPosition))
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
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

		case encoding.FieldTypeU4:
			v, err := encoding.ReadNibble(rr.r, field.BitPosition > 0)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = float64(v)

		case encoding.FieldTypeDecimal128:
			d, err := encoding.ReadDecimal128(rr.r)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = d.Float64(field.Scale)
			if wide != nil {
				wide[field.Name] = d
			}

		case encoding.FieldTypeSetU128, encoding.FieldTypeSetU256:
			// The wide rungs cannot ride ReadFieldValue's uint64 return —
			// it refuses them rather than hand back a truncated selection.
			// The authoritative value is the SetMask in the wide map; the
			// values echo is deliberately lossy (see setMaskFloatEcho).
			m, err := encoding.ReadSetMask(rr.r, field.Type)
			if err != nil {
				if err == io.EOF || isEOF(err) {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = setMaskFloatEcho(m)
			if wide != nil {
				wide[field.Name] = m
			}

		default:
			raw, err := encoding.ReadFieldValue(rr.r, field.Type)
			if err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					return io.EOF
				}
				return err
			}
			if !keepField {
				continue
			}
			values[field.Name] = rawToFloat64(field.Type, raw)
			// Set types: expose the full uint64 mask via the wide map so
			// operators that need bit-level precision (everything beyond
			// the 2^53 float64 limit, plus set_u64 generally) can read
			// without losing high bits to float coercion. The float64
			// echo in values stays for any caller that only consults it.
			if field.Type.IsSet() && wide != nil {
				wide[field.Name] = raw
			}
		}
	}

	// Trailing null bitmap, if the schema declares any nullable field.
	if bmSize := rr.schema.BitmapByteSize(); bmSize > 0 {
		bitmap, err := encoding.ReadBitmap(rr.r, bmSize)
		if err != nil {
			if err == io.EOF || isEOF(err) {
				return io.EOF
			}
			return err
		}
		for i, field := range rr.schema.Fields {
			if !field.Nullable {
				continue
			}
			if !encoding.BitmapIsNull(bitmap, i) {
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
	}

	return nil
}

// isEOF checks if an error wraps an EOF.
func isEOF(err error) bool {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return true
	}
	// Check wrapped errors.
	unwrapped := err
	for unwrapped != nil {
		if unwrapped == io.EOF || unwrapped == io.ErrUnexpectedEOF {
			return true
		}
		if u, ok := unwrapped.(interface{ Unwrap() error }); ok {
			unwrapped = u.Unwrap()
		} else {
			break
		}
	}
	return false
}

// setMaskFloatEcho returns the float64 echo a wide set field writes into
// the values map. It is the LOW 64 bits and nothing else, which makes a
// set_u128 whose selections all sit below bit 64 echo exactly what a
// set_u64 carrying the same selections echoes — the same byte-identity
// the wire word order gives (see the SetMask doc comment).
//
// The echo is DELIBERATELY lossy and is not a value any caller may treat
// as the selection: two masks differing only above bit 63 echo the same
// float64, and a mask with bits only above 63 echoes 0, which a narrow
// rung would read as an empty selection. It exists so a set field is
// never ABSENT from the values map — a missing key reads as "field not
// decoded", which is a different and worse failure than a lossy echo.
// The authoritative value is the SetMask in the wide map; the same
// caution already applies to set_u64 above 2^53.
func setMaskFloatEcho(m encoding.SetMask) float64 {
	low, _ := m.Uint64()
	return float64(low)
}

// rawToFloat64 converts raw uint64 bits to float64 based on field type.
func rawToFloat64(ft encoding.FieldType, raw uint64) float64 {
	switch ft {
	case encoding.FieldTypeF32:
		return float64(math.Float32frombits(uint32(raw)))
	case encoding.FieldTypeF64:
		return math.Float64frombits(raw)
	case encoding.FieldTypeDate:
		// The date word is two's-complement int32 epoch days: a pre-1970
		// day must decode negative, never zero-extend to ~4.29e9.
		return float64(encoding.DateDays(raw))
	case encoding.FieldTypeDateTime:
		// The datetime word is two's-complement int64 epoch seconds.
		return float64(encoding.DateTimeSeconds(raw))
	default:
		return float64(raw)
	}
}
