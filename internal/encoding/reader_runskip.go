package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
	"sync/atomic"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// RunSkipRecord is an optional extension of IndexedReusableRecord that
// lets the reuse decoders skip repopulating fields whose on-wire bytes
// did not change since the previous row.
//
// On a cohort sorted by a parent key most of a row's fields repeat the
// previous row byte for byte (a denormalised join holds the whole
// parent block constant across the parent's child rows). Comparing a
// field's 1–32 on-wire bytes against the previous row's is far cheaper
// than decoding them and writing the record, so when the record still
// holds exactly what the decoder wrote for the previous row, only the
// fields that changed are rewritten. The optimisation is self-activating:
// no declaration, no metadata, no format change.
//
// The contract has two halves, one per side:
//
//   - The READER guarantees the comparison baseline: it keeps the
//     previous row's bytes, and passes keep=true to BeginRunRow only
//     when those bytes were decoded, successfully and by this same
//     decode shape (full stride, or the same *DecodePlan), into this
//     same record.
//   - The RECORD guarantees its state is still what the decoder left:
//     BeginRunRow returns true only when keep is true, token names the
//     reader that last began a row on it, and nothing but that reader's
//     index-keyed writes has touched a schema field since. Any other
//     mutation of a schema field (Set, SetNull, SetWide, a name-keyed
//     decode write, ClearForRow) must make the next BeginRunRow return
//     false. It must also return false for any storage in which two
//     schema positions share state (a duplicated field name), because a
//     skipped position could then carry its sibling's value.
//
// BeginRunRow replaces ClearForRow on this path. When it returns false
// the record must have done a full ClearForRow and the decoder writes
// every field exactly as ReadRecordReused always has. When it returns
// true the record clears only per-row state the decoder never writes
// (off-schema null / wide marks, memoised views) and the decoder writes
// just the changed fields:
//
//   - a field whose value bytes AND null bit are unchanged is not
//     written at all;
//   - a field that is non-null and changed (value bytes differ, or it
//     was null on the previous row) gets its full value write —
//     numeric, plus the typed wide value for decimal128 / set_*;
//   - a field that turned null gets SetNullFieldAt + SetNumericAt(0),
//     exactly the full path's null signal; one that turned non-null
//     gets ClearNullAt. A field null on both rows is left alone.
//
// Because every write is a function of the field's own bytes and null
// bit, the record after a partial rewrite is observably identical to a
// full decode of the same row.
type RunSkipRecord interface {
	IndexedReusableRecord
	// BeginRunRow starts a row for the reader identified by token
	// (never 0). See the type documentation for the keep / return
	// contract.
	BeginRunRow(token uint64, keep bool) bool
	// ClearNullAt clears field idx's null mark, leaving its value and
	// wide value as they are.
	ClearNullAt(idx int)
}

// Adaptive backoff. Comparing costs a load and a branch per field, and
// on data that does not repeat (a cohort not sorted by its parent key)
// that branch mispredicts on roughly every field, making a compared row
// markedly slower than a plain full decode. So the reader measures: over
// every window of runProbeRows kept rows it counts the fields it had to
// rewrite, and when more than half of them changed it stops comparing
// for the next runBackoffRows rows — decoding them exactly as a
// non-run-skip record would (BeginRunRow with keep=false) — then probes
// again. On scattered data that bounds the cost to one probe window per
// backoff period; on sorted data the probe never trips.
//
// Variables, not constants, so tests can shrink them.
var (
	runProbeRows   = 32
	runBackoffRows = 1024
)

// runCanKeep reports whether the next row may compare against the
// baseline for (rs, plan), and consumes one backoff row if backing off.
func (rr *RecordReader) runCanKeep(rs RunSkipRecord, plan *DecodePlan) bool {
	if rr.runBackoff > 0 {
		rr.runBackoff--
		return false
	}
	return rr.prevValid && rr.prevSink == rs && rr.prevPlan == plan
}

// noteKeptRow feeds one kept row's rewrite count into the probe window.
func (rr *RecordReader) noteKeptRow(written, fields int) {
	rr.runWinRows++
	rr.runWinWritten += written
	rr.runWinFields += fields
	if rr.runWinRows < runProbeRows {
		return
	}
	if 2*rr.runWinWritten > rr.runWinFields {
		rr.runBackoff = runBackoffRows
	}
	rr.runWinRows, rr.runWinWritten, rr.runWinFields = 0, 0, 0
}

// runTokens mints a process-unique, non-zero identity per RecordReader
// the first time it drives a RunSkipRecord. A token, not a pointer, so a
// record never keeps its last reader (and that reader's source bytes)
// alive.
var runTokens atomic.Uint64

func (rr *RecordReader) token() uint64 {
	if rr.runToken == 0 {
		rr.runToken = runTokens.Add(1)
	}
	return rr.runToken
}

// commitRunRow records buf as the comparison baseline for the next row
// decoded into rs under plan (nil = the full-stride path). buf is the
// reader's recBuf; the two buffers are swapped, never copied.
func (rr *RecordReader) commitRunRow(rs RunSkipRecord, plan *DecodePlan, buf []byte) {
	rr.recBuf, rr.prevRow = rr.prevRow, buf
	rr.prevValid = true
	rr.prevSink = rs
	rr.prevPlan = plan
}

// strideOp is one schema field's on-wire span within the record stride.
// win is the exclusive end of the field WINDOW starting at this field:
// the fields [i, win) lie wholly inside the 8 bytes at off (and inside
// the value region, never the bitmap), so one 8-byte compare clears them
// all. win <= i+1 means no multi-field window starts here.
type strideOp struct {
	off, w int32
	win    int32
}

// strideLayout is the schema-derived geometry of one record: stride,
// bitmap size and each field's span. Pure function of the schema, built
// once per reader instead of once per row.
type strideLayout struct {
	built  bool
	n      int // len(schema.Fields) it was built for
	stride int
	bmSize int
	ops    []strideOp
}

func (rr *RecordReader) strideLayout() *strideLayout {
	l := &rr.layout
	if l.built && l.n == len(rr.schema.Fields) {
		return l
	}
	fields := rr.schema.Fields
	ops := make([]strideOp, len(fields))
	off := 0
	for i := range fields {
		w := onWireWidth(fields[i].Type)
		ops[i] = strideOp{off: int32(off), w: int32(w)}
		off += w
	}
	body := off
	for i := range ops {
		end := int(ops[i].off) + 8
		if end > body {
			continue
		}
		j := i
		for j < len(ops) && int(ops[j].off+ops[j].w) <= end {
			j++
		}
		ops[i].win = int32(j)
	}
	*l = strideLayout{
		built:  true,
		n:      len(fields),
		stride: rr.schema.RecordByteSize(),
		bmSize: rr.schema.BitmapByteSize(),
		ops:    ops,
	}
	return l
}

// readStrideRunSkip is ReadRecordReused for a RunSkipRecord.
func (rr *RecordReader) readStrideRunSkip(rs RunSkipRecord) error {
	canKeep := rr.runCanKeep(rs, nil)
	rr.prevValid = false
	keep := rs.BeginRunRow(rr.token(), canKeep)

	l := rr.strideLayout()
	if cap(rr.recBuf) < l.stride {
		rr.recBuf = make([]byte, l.stride)
	}
	buf := rr.recBuf[:l.stride]
	if _, err := io.ReadFull(rr.r, buf); err != nil {
		return mapEOF(err)
	}
	if keep {
		written, err := rr.decodeStrideChanged(rs, l, buf, rr.prevRow[:l.stride])
		if err != nil {
			return err
		}
		rr.noteKeptRow(written, len(l.ops))
	} else if err := rr.decodeStrideIndexed(rs, buf); err != nil {
		return err
	}
	rr.commitRunRow(rs, nil, buf)
	return nil
}

// onWireWidth is one field's on-wire width: 1 byte for a bit-packed
// field, 16 for decimal128, fixedWidthBytes otherwise (0 = unknown).
func onWireWidth(ft encoding.FieldType) int {
	switch {
	case ft.IsBitPacked():
		return 1
	case ft == encoding.FieldTypeDecimal128:
		return 16
	}
	return fixedWidthBytes(ft)
}

// decodeStrideChanged is decodeStrideIndexed for a row whose record
// already holds the decode of prev: it writes only the fields whose
// bytes or null bit differ between cur and prev.
//
// The field walk compares VALUE BYTES only, eight at a time where a
// window of whole fields fits (a run of unchanged narrow fields — a
// repeated parent block — costs one load per 8 bytes), and writes each
// field whose bytes changed. Nulls are settled afterwards by
// settleNulls, which visits only the fields null on either row.
//
// It returns the number of fields the walk rewrote, for the backoff
// probe.
func (rr *RecordReader) decodeStrideChanged(rs RunSkipRecord, l *strideLayout, cur, prev []byte) (int, error) {
	typed, _ := rs.(TypedSetRecord)
	fields := rr.schema.Fields
	ops := l.ops
	written := 0
	for fi := 0; fi < len(ops); {
		op := ops[fi]
		end := fi + 1
		if int(op.win) > end {
			end = int(op.win)
			if binary.LittleEndian.Uint64(cur[op.off:]) == binary.LittleEndian.Uint64(prev[op.off:]) {
				fi = end
				continue
			}
		}
		for ; fi < end; fi++ {
			op := ops[fi]
			sub := cur[op.off : op.off+op.w]
			if spanEqual(sub, prev[op.off:op.off+op.w]) {
				continue
			}
			written++
			if err := writeFieldBytes(rs, typed, &fields[fi], fi, sub); err != nil {
				return written, err
			}
		}
	}
	if l.bmSize > 0 {
		body := len(cur) - l.bmSize
		err := rr.settleNulls(rs, typed, nil, cur[body:], prev[body:], func(fi int) ([]byte, []byte) {
			op := ops[fi]
			return cur[op.off : op.off+op.w], prev[op.off : op.off+op.w]
		})
		return written, err
	}
	return written, nil
}

// settleNulls finishes a kept row's nullable fields after a field walk
// that compared value bytes only. It visits every nullable field keep
// accepts that is null on the current OR the previous row — the only
// fields whose state can differ from "value decoded from its bytes" —
// and brings each to exactly what a full decode leaves:
//
//   - null now: the full path's null signal (SetNullFieldAt +
//     SetNumericAt 0), unless it was also null before with the same
//     bytes, in which case the walk wrote nothing and the state stands;
//   - non-null now, null before: ClearNullAt, and the value is written
//     back from its bytes if the walk skipped it (bytes unchanged — the
//     record still holds the null's zeroed value).
//
// spans returns field fi's current and previous on-wire bytes; a field
// with no span in this decode (a plan that surfaces the bitmap but does
// not decode the field's group) returns nil and is never rewritten,
// matching the full path, which never decodes its bytes either.
func (rr *RecordReader) settleNulls(rs RunSkipRecord, typed TypedSetRecord, keep FieldFilter, curBM, prevBM []byte, spans func(fi int) (cur, prev []byte)) error {
	fields := rr.schema.Fields
	for b := range curBM {
		x := curBM[b] | prevBM[b]
		for x != 0 {
			fi := b*8 + bits.TrailingZeros8(x)
			x &= x - 1
			if fi >= len(fields) || !fields[fi].Nullable {
				continue
			}
			if keep != nil && !keep(fields[fi].Name) {
				continue
			}
			cn, pn := encoding.BitmapIsNull(curBM, fi), encoding.BitmapIsNull(prevBM, fi)
			cur, prev := spans(fi)
			same := cur != nil && spanEqual(cur, prev)
			switch {
			case cn && pn && (same || cur == nil):
				// null on both rows, nothing written by the walk
			case cn:
				rs.SetNullFieldAt(fi)
				rs.SetNumericAt(fi, 0)
			default: // non-null now, null before
				rs.ClearNullAt(fi)
				if same {
					if err := writeFieldBytes(rs, typed, &fields[fi], fi, cur); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// writeFieldBytes writes one non-null field's value (and typed wide
// value) from its on-wire bytes: the per-field body of
// decodeStrideIndexed, byte for byte.
func writeFieldBytes(sink IndexedReusableRecord, typed TypedSetRecord, field *encoding.Field, fi int, sub []byte) error {
	switch field.Type {
	case encoding.FieldTypePackedBool:
		if (sub[0]>>uint(field.BitPosition))&1 == 1 {
			sink.SetNumericAt(fi, 1)
		} else {
			sink.SetNumericAt(fi, 0)
		}
	case encoding.FieldTypeU4:
		var v uint8
		if field.BitPosition > 0 {
			v = sub[0] >> 4
		} else {
			v = sub[0] & 0x0F
		}
		sink.SetNumericAt(fi, float64(v))
	case encoding.FieldTypeDecimal128:
		var raw [16]byte
		copy(raw[:], sub)
		d := encoding.DecodeDecimal128(raw)
		sink.SetNumericAt(fi, d.Float64(field.Scale))
		sink.SetWideFieldAt(fi, d)
	default:
		if fixedWidthBytes(field.Type) == 0 {
			return errors.NewCodedError(errors.ENCODING_INVALID,
				fmt.Sprintf("unknown field type %d", field.Type))
		}
		sink.SetNumericAt(fi, decodeFixed(field.Type, sub))
		if field.Type.IsSet() {
			return putSetField(sink, typed, field.Type, fi, sub)
		}
	}
	return nil
}

// spanEqual reports whether two equal-length on-wire spans hold the
// same bytes, with single loads for the common widths.
func spanEqual(a, b []byte) bool {
	switch len(a) {
	case 1:
		return a[0] == b[0]
	case 2:
		return binary.LittleEndian.Uint16(a) == binary.LittleEndian.Uint16(b)
	case 4:
		return binary.LittleEndian.Uint32(a) == binary.LittleEndian.Uint32(b)
	case 8:
		return binary.LittleEndian.Uint64(a) == binary.LittleEndian.Uint64(b)
	}
	return string(a) == string(b)
}

// planOp is one field a plan's DecodeFields groups carry: its schema
// position and its span within the ASSEMBLED row (every DecodeFields
// group's bytes, in plan order, SkipBytes ranges elided).
type planOp struct {
	fi     int
	off, w int32
	field  *encoding.Field
}

// runPlanShape is the flattened form of the plan last driven through
// readPlanRunSkip, built once per plan: steps replays the plan's reads
// and skips (a negative n skips -n bytes, a positive n reads n bytes
// into the assembled row), ops lists the decoded fields, total is the
// assembled row's length and bmOff the bitmap's offset in it (-1 when
// the plan decodes no bitmap).
type runPlanShape struct {
	plan  *DecodePlan
	steps []int
	ops   []planOp
	opOf  []int32 // schema position → index into ops, -1 = not decoded
	total int
	bmOff int
}

func (rr *RecordReader) planShapeFor(plan *DecodePlan) (*runPlanShape, error) {
	if rr.shape.plan == plan {
		return &rr.shape, nil
	}
	bitmapIdx := rr.planBitmapIdx(plan)
	s := runPlanShape{plan: plan, bmOff: -1}
	for i, seg := range plan.Segments {
		switch seg := seg.(type) {
		case SkipBytes:
			if seg.N > 0 {
				s.steps = append(s.steps, -seg.N)
			}
		case DecodeFields:
			if i == bitmapIdx {
				s.bmOff = s.total
				s.steps = append(s.steps, rr.schema.BitmapByteSize())
				s.total += rr.schema.BitmapByteSize()
				continue
			}
			indices, err := rr.segmentIndices(seg)
			if err != nil {
				return nil, err
			}
			n := 0
			for k, f := range seg.Fields {
				w := onWireWidth(f.Type)
				if w == 0 {
					return nil, errors.NewCodedError(errors.ENCODING_INVALID,
						fmt.Sprintf("unknown field type %d", f.Type))
				}
				s.ops = append(s.ops, planOp{fi: indices[k], off: int32(s.total + n), w: int32(w), field: f})
				n += w
			}
			if n > 0 {
				s.steps = append(s.steps, n)
				s.total += n
			}
		}
	}
	s.opOf = make([]int32, len(rr.schema.Fields))
	for i := range s.opOf {
		s.opOf[i] = -1
	}
	for k, op := range s.ops {
		s.opOf[op.fi] = int32(k)
	}
	rr.shape = s
	return &rr.shape, nil
}

// readPlanRunSkip is ReadRecordReusedWithPlan for a RunSkipRecord. The
// plan path reads group by group rather than the whole stride, so the
// row is first ASSEMBLED — every DecodeFields group's bytes, in plan
// order, into one buffer, SkipBytes ranges advanced past — and then
// decoded; the assembled buffer is what the next row compares against.
// Assembling first also puts the null bitmap in hand before any field
// is decoded, which the per-field null comparison needs.
//
// keep must be the same filter for every row decoded under one plan
// (the iterators install the two together); the comparison baseline is
// keyed by plan identity.
func (rr *RecordReader) readPlanRunSkip(rs RunSkipRecord, keep FieldFilter, plan *DecodePlan) error {
	canKeep := rr.runCanKeep(rs, plan)
	rr.prevValid = false
	keepRow := rs.BeginRunRow(rr.token(), canKeep)

	shape, err := rr.planShapeFor(plan)
	if err != nil {
		return err
	}
	if cap(rr.recBuf) < shape.total {
		rr.recBuf = make([]byte, shape.total)
	}
	buf := rr.recBuf[:shape.total]
	off := 0
	for _, n := range shape.steps {
		if n < 0 {
			if err := advanceReader(rr.r, -n); err != nil {
				return mapEOF(err)
			}
			continue
		}
		if _, err := io.ReadFull(rr.r, buf[off:off+n]); err != nil {
			return mapEOF(err)
		}
		off += n
	}

	typed, _ := rs.(TypedSetRecord)
	var curBM, prevBM []byte
	if shape.bmOff >= 0 {
		curBM = buf[shape.bmOff:]
	}
	if keepRow {
		prev := rr.prevRow[:shape.total]
		if curBM != nil {
			prevBM = prev[shape.bmOff:]
		}
		compared, written := 0, 0
		for _, op := range shape.ops {
			if keep != nil && !keep(op.field.Name) {
				continue
			}
			compared++
			sub := buf[op.off : op.off+op.w]
			if spanEqual(sub, prev[op.off:op.off+op.w]) {
				continue
			}
			written++
			if err := writeFieldBytes(rs, typed, op.field, op.fi, sub); err != nil {
				return err
			}
		}
		rr.noteKeptRow(written, compared)
		if curBM != nil {
			err := rr.settleNulls(rs, typed, keep, curBM, prevBM, func(fi int) ([]byte, []byte) {
				k := shape.opOf[fi]
				if k < 0 {
					return nil, nil
				}
				op := shape.ops[k]
				return buf[op.off : op.off+op.w], prev[op.off : op.off+op.w]
			})
			if err != nil {
				return err
			}
		}
	} else {
		for _, op := range shape.ops {
			if keep != nil && !keep(op.field.Name) {
				continue
			}
			if err := writeFieldBytes(rs, typed, op.field, op.fi, buf[op.off:op.off+op.w]); err != nil {
				return err
			}
		}
		if curBM != nil {
			rr.decodeBitmapBytes(rs, keep, curBM)
		}
	}
	rr.commitRunRow(rs, plan, buf)
	return nil
}
