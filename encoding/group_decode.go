package encoding

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// Grouped (0x02) reuse decode — per-row cost independent of parent-block
// width.
//
// The reuse decoders (ReadRecordReused, ReadRecordReusedWithPlan) do NOT
// expand a grouped cohort's physical row into its logical row. They read
// the physical row as it is — one u32 index per indexed group, then the
// row fields, then the narrowed bitmap — and populate the record at
// LOGICAL field positions directly:
//
//   - row fields decode from their physical bytes exactly as the 0x01
//     decoders decode them, with E2 run-skip (byte compare against the
//     previous physical row, adaptive backoff) on a RunSkipRecord;
//   - a group whose index EQUALS the index the record already holds is
//     not touched at all — O(1) per group, no member byte compare, no
//     expansion. An unchanged index is a certain hit, so this needs no
//     backoff and ignores it;
//   - a group whose index changed (or a fresh / cleared record) has its
//     members written from a per-entry cache of decoded values (every
//     member's float64 and decimal128 value when the whole dictionary
//     fits; only the decimals, direct-mapped, when it does not — see
//     entryCache), filled on first touch and bounded by
//     groupEntryCacheBudget bytes per decode shape, so a dictionary of
//     any size costs a bounded amount of memory. Derived at read time,
//     never persisted.
//
// Projection is plan-aware: the retained set is read off the plan, and a
// group with no retained member is neither expanded nor decoded (its
// index is still bounds-checked, as expansion always did).
//
// The observable record state is exactly what the logical decode leaves
// — the same writes per field, in the same per-field order (value, then
// null signal). The map decoders (ReadRecordWithWide*, the record
// locator) and byte-level consumers keep the logical stream
// (NewLogicalStream), which is the right tool where a whole logical row
// is wanted.

// groupEntryCacheBudget bounds the decoded-entry cache of one decode
// shape (full, or one plan) of one RecordReader, in bytes, split evenly
// across the groups that write values (entryCache has the two modes).
// Allocated on the first changed index, never above what the dictionary
// needs. A variable only so tests can shrink it.
var groupEntryCacheBudget = 8 << 20

// decimalCacheBytes is the accounting weight of one cached decimal128:
// the value's pointer plus the big.Int it points at and its words.
const decimalCacheBytes = 64

// groupDecodeStats counts the grouped reuse decoder's work. Unexported:
// it exists for the deterministic work-count gates.
type groupDecodeStats struct {
	rows           int64 // physical rows decoded (whole-row skips excluded)
	groupSkips     int64 // retained groups left untouched: index unchanged on a kept record
	populations    int64 // retained groups whose members were written
	memberWrites   int64 // member VALUE writes (one per retained member per population)
	cacheFills     int64 // entries whose decimal members were decoded into the cache
	decimalDecodes int64 // decimal member values decoded with no cache slot
}

type gRowOp struct {
	fi    int
	off   int
	w     int
	bit   int // physical bitmap bit; -1 = not nullable
	field *Field
}

type gMember struct {
	fi       int
	off      int
	w        int
	nullable bool
	field    *Field
}

type gGroup struct {
	idxOff  int // physical offset of the u32 index; -1 = constant group
	count   uint32
	width   int
	bmOff   int // entry null-bitmap offset; -1 = none
	entries []byte
	members []gMember
}

func (gg *gGroup) entry(e uint32) []byte {
	return gg.entries[int(e)*gg.width : (int(e)+1)*gg.width]
}

func (gg *gGroup) memberNull(entry []byte, k int) bool {
	return gg.bmOff >= 0 && gg.members[k].nullable && BitmapIsNull(entry[gg.bmOff:], k)
}

// groupedDecoder is the compiled physical-row decoder of one grouped
// schema, owned by one RecordReader.
type groupedDecoder struct {
	src    *Schema // the grouped schema (RecordReader.schema is its Logical view)
	stride int
	body   int
	rows   []gRowOp
	groups []gGroup

	buf, prev []byte
	idx       []uint32 // per group: entry on the row in buf
	last      []uint32 // per group: entry the committed run-skip record holds
	read      bool     // idx is valid (the last row was read, not skipped whole)

	prevValid bool
	prevSink  RunSkipRecord
	prevPlan  *DecodePlan

	full *gSel
	sel  *gSel

	stats groupDecodeStats
}

// gSel is one decode shape's selection: which row fields and which group
// members get a value write and which surface a null.
type gSel struct {
	plan     *DecodePlan
	skipAll  bool
	rowVals  []int // indices into rows
	rowNulls []gNullOp
	groups   []gSelGroup
}

type gNullOp struct {
	ri    int
	inVal bool // the field also takes a value write
}

type gSelGroup struct {
	g     int
	vals  []int // member slots taking a value write
	nulls []int // nullable member slots surfacing a null
	cache entryCache
}

// entryCache holds decoded member values per dictionary entry, in one
// of two modes chosen per group from its share of the budget:
//
//   - WHOLE (all): every entry of the dictionary has a slot (slot = entry)
//     holding every retained value member's float64 numeric, plus the
//     decoded Decimal128 of each retained decimal member. A changed index
//     then costs one load per member and no decode at all.
//   - DECIMALS: the dictionary does not fit, so only the retained
//     decimal members are cached, direct-mapped (slot = entry mod slots).
//     Decimal decode is the one expensive member decode (a big.Int per
//     value and a big.Float division for its echo); every other type
//     decodes from its 1–32 entry bytes about as cheaply as a cache load,
//     and decoding it directly (instead of caching it in a thrashing
//     direct-mapped table) keeps a huge dictionary from paying fill +
//     store on nearly every changed row.
//
// A group with no cache at all (no decimal and no whole fit) decodes
// every member from the entry bytes.
type entryCache struct {
	slots int   // 0 = no cache
	all   bool  // WHOLE mode: nums carries every value member
	nv    int   // floats per slot: len(vals) (WHOLE) or nd (DECIMALS)
	nd    int   // retained decimal members
	numOf []int // per vals position: index into a slot's floats, -1
	decOf []int // per vals position: index among the decimal members, -1
	tags  []uint32
	nums  []float64
	decs  []Decimal128
}

func newGroupedDecoder(s *Schema) (*groupedDecoder, error) {
	if err := s.validateGroupShape(); err != nil {
		return nil, err
	}
	groupOf, _ := s.memberSets()
	d := &groupedDecoder{
		src:    s,
		stride: s.RecordByteSize(),
		idx:    make([]uint32, len(s.Groups)),
		last:   make([]uint32, len(s.Groups)),
	}
	off := 0
	for g := range s.Groups {
		if s.Groups[g].Kind == GroupKindIndexed {
			off += GroupIndexWidth
		}
	}
	bit := 0
	for fi := range s.Fields {
		if groupOf[fi] >= 0 {
			continue
		}
		f := &s.Fields[fi]
		w := onWireWidth(f.Type)
		if w == 0 {
			return nil, errors.NewCodedError(errors.ENCODING_INVALID, fmt.Sprintf("unknown field type %d", f.Type))
		}
		op := gRowOp{fi: fi, off: off, w: w, bit: -1, field: f}
		if f.Nullable {
			op.bit = bit
		}
		d.rows = append(d.rows, op)
		off += w
		bit++
	}
	d.body = off
	d.groups = make([]gGroup, len(s.Groups))
	for g := range s.Groups {
		grp := &s.Groups[g]
		width, bmOff := s.groupEntryGeometry(g)
		gg := gGroup{
			idxOff:  s.GroupIndexOffset(g),
			count:   uint32(min(uint64(len(grp.Entries)/width), uint64(^uint32(0)))),
			width:   width,
			bmOff:   bmOff,
			entries: grp.Entries,
		}
		eoff := 0
		for _, m := range grp.Members {
			f := &s.Fields[m.Field]
			w := onWireWidth(f.Type)
			if w == 0 {
				return nil, errors.NewCodedError(errors.ENCODING_INVALID, fmt.Sprintf("unknown field type %d", f.Type))
			}
			gg.members = append(gg.members, gMember{fi: m.Field, off: eoff, w: w, nullable: f.Nullable, field: f})
			eoff += w
		}
		d.groups[g] = gg
	}
	return d, nil
}

// selection returns the decode shape for plan (nil = full decode). A
// plan's selection is keyed by plan identity: the iterators install a
// plan and its keep filter together, as run-skip already assumes.
func (d *groupedDecoder) selection(rr *RecordReader, keep FieldFilter, plan *DecodePlan) (*gSel, error) {
	n := len(rr.schema.Fields)
	if plan == nil {
		if d.full == nil {
			all := make([]bool, n)
			for i := range all {
				all[i] = true
			}
			d.full = d.buildSel(nil, false, all, all)
		}
		return d.full, nil
	}
	if d.sel != nil && d.sel.plan == plan {
		return d.sel, nil
	}
	val := make([]bool, n)
	null := make([]bool, n)
	kept := func(name string) bool { return keep == nil || keep(name) }
	bitmapIdx := rr.planBitmapIdx(plan)
	decodes := false
	for i, seg := range plan.Segments {
		df, ok := seg.(DecodeFields)
		if !ok {
			continue
		}
		decodes = true
		if i == bitmapIdx {
			// decodeBitmapBytes surfaces every nullable schema field keep
			// accepts, whatever the segment lists.
			for fi := range rr.schema.Fields {
				if rr.schema.Fields[fi].Nullable && kept(rr.schema.Fields[fi].Name) {
					null[fi] = true
				}
			}
			continue
		}
		indices, err := rr.segmentIndices(df)
		if err != nil {
			return nil, err
		}
		for k, f := range df.Fields {
			if kept(f.Name) {
				val[indices[k]] = true
			}
		}
	}
	d.sel = d.buildSel(plan, !decodes, val, null)
	return d.sel, nil
}

func (d *groupedDecoder) buildSel(plan *DecodePlan, skipAll bool, val, null []bool) *gSel {
	sel := &gSel{plan: plan, skipAll: skipAll}
	for ri, op := range d.rows {
		if val[op.fi] {
			sel.rowVals = append(sel.rowVals, ri)
		}
		if op.bit >= 0 && null[op.fi] {
			sel.rowNulls = append(sel.rowNulls, gNullOp{ri: ri, inVal: val[op.fi]})
		}
	}
	for g := range d.groups {
		gg := &d.groups[g]
		sg := gSelGroup{g: g}
		for k, m := range gg.members {
			if val[m.fi] {
				sg.vals = append(sg.vals, k)
			}
			if m.nullable && null[m.fi] {
				sg.nulls = append(sg.nulls, k)
			}
		}
		if len(sg.vals) > 0 || len(sg.nulls) > 0 {
			sel.groups = append(sel.groups, sg)
		}
	}
	// Split the cache budget evenly across the groups that write values.
	valued := 0
	for i := range sel.groups {
		if len(sel.groups[i].vals) > 0 {
			valued++
		}
	}
	for i := range sel.groups {
		sg := &sel.groups[i]
		if len(sg.vals) == 0 {
			continue
		}
		c := &sg.cache
		c.decOf = make([]int, len(sg.vals))
		c.numOf = make([]int, len(sg.vals))
		for j, k := range sg.vals {
			c.decOf[j] = -1
			if d.groups[sg.g].members[k].field.Type == FieldTypeDecimal128 {
				c.decOf[j] = c.nd
				c.nd++
			}
		}
		share := groupEntryCacheBudget / valued
		count := int(d.groups[sg.g].count)
		if whole := 4 + 8*len(sg.vals) + decimalCacheBytes*c.nd; count <= share/whole {
			c.all, c.slots, c.nv = true, count, len(sg.vals)
			for j := range c.numOf {
				c.numOf[j] = j
			}
			continue
		}
		for j := range c.numOf {
			c.numOf[j] = c.decOf[j]
		}
		if c.nd > 0 {
			c.nv = c.nd
			c.slots = min(count, share/(4+(8+decimalCacheBytes)*c.nd))
		}
	}
	return sel
}

// lookup returns entry e's cache slot, decoding its cached members on a
// miss, or -1 when the group has no cache.
func (d *groupedDecoder) lookup(sg *gSelGroup, gg *gGroup, e uint32) int {
	c := &sg.cache
	if c.slots <= 0 {
		return -1
	}
	if c.tags == nil {
		c.tags = make([]uint32, c.slots)
		c.nums = make([]float64, c.slots*c.nv)
		if c.nd > 0 {
			c.decs = make([]Decimal128, c.slots*c.nd)
		}
	}
	slot := int(e % uint32(c.slots))
	if c.tags[slot] == e+1 {
		return slot
	}
	d.stats.cacheFills++
	entry := gg.entry(e)
	nums := c.nums[slot*c.nv : (slot+1)*c.nv]
	for j, k := range sg.vals {
		ni := c.numOf[j]
		if ni < 0 {
			continue
		}
		m := &gg.members[k]
		sub := entry[m.off : m.off+m.w]
		if di := c.decOf[j]; di >= 0 {
			var raw [16]byte
			copy(raw[:], sub)
			dec := DecodeDecimal128(raw)
			c.decs[slot*c.nd+di] = dec
			nums[ni] = dec.Float64(m.field.Scale)
			continue
		}
		nums[ni] = memberNumeric(m.field, sub)
	}
	c.tags[slot] = e + 1
	return slot
}

// memberNumeric is writeFieldBytes' numeric value for a non-decimal
// field, without the write.
func memberNumeric(f *Field, sub []byte) float64 {
	switch f.Type {
	case FieldTypePackedBool:
		return float64((sub[0] >> uint(f.BitPosition)) & 1)
	case FieldTypeU4:
		if f.BitPosition > 0 {
			return float64(sub[0] >> 4)
		}
		return float64(sub[0] & 0x0F)
	}
	return decodeFixed(f.Type, sub)
}

// writeMembers writes the retained value members of group sg from entry
// e: the same numeric + typed wide writes a full decode of the members'
// bytes makes, taken from the cache where the member has a slot and
// decoded from the entry bytes otherwise. A cached Decimal128 is shared
// by every record it is written to, which is safe because Decimal128 is
// immutable (every operation allocates its result and Mantissa returns a
// copy).
func (d *groupedDecoder) writeMembers(sink IndexedReusableRecord, typed TypedSetRecord, sg *gSelGroup, e uint32) error {
	gg := &d.groups[sg.g]
	d.stats.populations++
	if len(sg.vals) == 0 {
		return nil
	}
	d.stats.memberWrites += int64(len(sg.vals))
	entry := gg.entry(e)
	c := &sg.cache
	slot := d.lookup(sg, gg, e)
	if slot < 0 {
		for _, k := range sg.vals {
			m := &gg.members[k]
			if m.field.Type == FieldTypeDecimal128 {
				d.stats.decimalDecodes++
			}
			if err := writeFieldBytes(sink, typed, m.field, m.fi, entry[m.off:m.off+m.w]); err != nil {
				return err
			}
		}
		return nil
	}
	nums := c.nums[slot*c.nv : (slot+1)*c.nv]
	for j, k := range sg.vals {
		m := &gg.members[k]
		ni := c.numOf[j]
		if ni < 0 {
			if err := writeFieldBytes(sink, typed, m.field, m.fi, entry[m.off:m.off+m.w]); err != nil {
				return err
			}
			continue
		}
		sink.SetNumericAt(m.fi, nums[ni])
		switch {
		case c.decOf[j] >= 0:
			sink.SetWideFieldAt(m.fi, c.decs[slot*c.nd+c.decOf[j]])
		case m.field.Type.IsSet():
			if err := putSetField(sink, typed, m.field.Type, m.fi, entry[m.off:m.off+m.w]); err != nil {
				return err
			}
		}
	}
	return nil
}

// readRow reads one physical row into d.buf and resolves every group's
// index, bounds-checked exactly as RowExpander.Expand checks it.
func (d *groupedDecoder) readRow(r io.Reader) ([]byte, error) {
	if cap(d.buf) < d.stride {
		d.buf = make([]byte, d.stride)
	}
	buf := d.buf[:d.stride]
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, mapEOF(err)
	}
	for g := range d.groups {
		gg := &d.groups[g]
		var e uint32
		if gg.idxOff >= 0 {
			e = binary.LittleEndian.Uint32(buf[gg.idxOff:])
		}
		if e >= gg.count {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"group index past the end of its dictionary",
				map[string]any{"group": g, "index": e, "entry_count": gg.count})
		}
		d.idx[g] = e
	}
	d.read = true
	d.stats.rows++
	return buf, nil
}

// decodeAll writes every selected field of the row in buf into a record
// that holds nothing of the previous row (cleared, or fresh).
func (d *groupedDecoder) decodeAll(sink IndexedReusableRecord, sel *gSel, buf []byte) error {
	typed, _ := sink.(TypedSetRecord)
	for _, ri := range sel.rowVals {
		op := &d.rows[ri]
		if err := writeFieldBytes(sink, typed, op.field, op.fi, buf[op.off:op.off+op.w]); err != nil {
			return err
		}
	}
	for i := range sel.groups {
		sg := &sel.groups[i]
		if err := d.writeMembers(sink, typed, sg, d.idx[sg.g]); err != nil {
			return err
		}
	}
	bm := buf[d.body:]
	for _, nop := range sel.rowNulls {
		op := &d.rows[nop.ri]
		if BitmapIsNull(bm, op.bit) {
			sink.SetNullFieldAt(op.fi)
			sink.SetNumericAt(op.fi, 0)
		}
	}
	for i := range sel.groups {
		sg := &sel.groups[i]
		gg := &d.groups[sg.g]
		entry := gg.entry(d.idx[sg.g])
		for _, k := range sg.nulls {
			if gg.memberNull(entry, k) {
				fi := gg.members[k].fi
				sink.SetNullFieldAt(fi)
				sink.SetNumericAt(fi, 0)
			}
		}
	}
	return nil
}

// decodeKept brings a record holding the decode of prev (same reader,
// same decode shape) to the decode of cur, writing only what changed.
// compare is false while the row-field backoff is active: every selected
// row field is then rewritten without a byte compare. Groups ignore it —
// an unchanged index is a certain hit. It returns the row-field rewrite
// count for the backoff probe.
func (d *groupedDecoder) decodeKept(rs RunSkipRecord, sel *gSel, cur, prev []byte, compare bool) (int, error) {
	typed, _ := rs.(TypedSetRecord)
	written := 0
	for _, ri := range sel.rowVals {
		op := &d.rows[ri]
		sub := cur[op.off : op.off+op.w]
		if compare && spanEqual(sub, prev[op.off:op.off+op.w]) {
			continue
		}
		written++
		if err := writeFieldBytes(rs, typed, op.field, op.fi, sub); err != nil {
			return written, err
		}
	}
	curBM, prevBM := cur[d.body:], prev[d.body:]
	for _, nop := range sel.rowNulls {
		op := &d.rows[nop.ri]
		cn, pn := BitmapIsNull(curBM, op.bit), BitmapIsNull(prevBM, op.bit)
		if !cn && !pn {
			continue
		}
		sub := cur[op.off : op.off+op.w]
		wrote := nop.inVal && (!compare || !spanEqual(sub, prev[op.off:op.off+op.w]))
		switch {
		case cn && pn && !wrote:
			// null on both rows, nothing written this row
		case cn:
			rs.SetNullFieldAt(op.fi)
			rs.SetNumericAt(op.fi, 0)
		default: // non-null now, null before
			rs.ClearNullAt(op.fi)
			if nop.inVal && !wrote {
				if err := writeFieldBytes(rs, typed, op.field, op.fi, sub); err != nil {
					return written, err
				}
			}
		}
	}
	for i := range sel.groups {
		sg := &sel.groups[i]
		e, o := d.idx[sg.g], d.last[sg.g]
		if e == o {
			d.stats.groupSkips++
			continue
		}
		if err := d.writeMembers(rs, typed, sg, e); err != nil {
			return written, err
		}
		gg := &d.groups[sg.g]
		ne, oe := gg.entry(e), gg.entry(o)
		for _, k := range sg.nulls {
			fi := gg.members[k].fi
			switch {
			case gg.memberNull(ne, k):
				rs.SetNullFieldAt(fi)
				rs.SetNumericAt(fi, 0)
			case gg.memberNull(oe, k):
				rs.ClearNullAt(fi)
			}
		}
	}
	return written, nil
}

// readGrouped is ReadRecordReused / ReadRecordReusedWithPlan over a
// grouped schema.
func (rr *RecordReader) readGrouped(rec ReusableRecord, keep FieldFilter, plan *DecodePlan) error {
	d := rr.gd
	sel, err := d.selection(rr, keep, plan)
	if err != nil {
		return err
	}
	raw := rr.groups.r
	rr.groups.fresh = false
	sink := rr.indexedSink(rec)
	rs, runSkip := sink.(RunSkipRecord)
	gi, _ := rec.(GroupIndexRecord)

	if !runSkip {
		sink.ClearForRow()
		if sel.skipAll {
			d.read = false
			if gi != nil {
				gi.SetGroupIndices(d.src, nil)
			}
			return mapEOF(advanceReader(raw, d.stride))
		}
		buf, err := d.readRow(raw)
		if err != nil {
			return err
		}
		if err := d.decodeAll(sink, sel, buf); err != nil {
			return err
		}
		if gi != nil {
			gi.SetGroupIndices(d.src, d.idx)
		}
		return nil
	}

	canKeep := d.prevValid && d.prevSink == rs && d.prevPlan == plan
	d.prevValid = false
	kept := rs.BeginRunRow(rr.token(), canKeep)
	if sel.skipAll {
		d.read = false
		if gi != nil {
			gi.SetGroupIndices(d.src, nil)
		}
		if err := advanceReader(raw, d.stride); err != nil {
			return mapEOF(err)
		}
		d.prevValid, d.prevSink, d.prevPlan = true, rs, plan
		return nil
	}
	buf, err := d.readRow(raw)
	if err != nil {
		return err
	}
	if kept {
		compare := true
		if rr.runBackoff > 0 {
			rr.runBackoff--
			compare = false
		}
		written, err := d.decodeKept(rs, sel, buf, d.prev[:d.stride], compare)
		if err != nil {
			return err
		}
		if compare {
			rr.noteKeptRow(written, len(sel.rowVals))
		}
	} else if err := d.decodeAll(rs, sel, buf); err != nil {
		return err
	}
	d.buf, d.prev = d.prev, buf
	copy(d.last, d.idx)
	d.prevValid, d.prevSink, d.prevPlan = true, rs, plan
	if gi != nil {
		gi.SetGroupIndices(d.src, d.idx)
	}
	return nil
}
