package encoding

import (
	"encoding/binary"
	"io"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// span is one contiguous byte copy: n bytes from src offset a to dst
// offset b.
type span struct{ a, b, n int }

// bitMove carries one null bit: source bit a to destination bit b.
type bitMove struct{ a, b int }

// groupSplice is the compiled form of one group for row expansion.
type groupSplice struct {
	idxOff  int // physical offset of the u32 index; -1 = constant group
	entries []byte
	width   int
	count   uint32
	members []span    // entry offset -> logical offset
	nulls   []bitMove // entry-bitmap bit -> logical bit (nullable members)
	bmOff   int       // entry null-bitmap offset, -1 = none
}

// rowLayout is the compiled physical <-> logical row mapping of a
// grouped schema. It is a pure function of the schema, built once per
// expander/encoder, and drives both directions.
type rowLayout struct {
	physStride, physBody, physBM int
	logStride, logBody, logBM    int
	rows                         []span    // physical offset -> logical offset (row fields)
	rowNulls                     []bitMove // physical bit -> logical bit (nullable row fields)
	groups                       []groupSplice
}

// compileRowLayout builds the mapping for a grouped schema. The caller
// has validated the schema (ReadSchema always does).
func compileRowLayout(s *encoding.Schema) (*rowLayout, error) {
	if err := validateGroupShape(s); err != nil {
		return nil, err
	}
	groupOf, _ := memberSets(s)
	l := &rowLayout{}

	logOff := make([]int, len(s.Fields))
	off := 0
	for i := range s.Fields {
		logOff[i] = off
		off += onWireWidth(s.Fields[i].Type)
	}
	l.logBody = off
	l.logBM = s.Logical().BitmapByteSize()
	l.logStride = l.logBody + l.logBM

	phys := 0
	for g := range s.Groups {
		if s.Groups[g].Kind == encoding.GroupKindIndexed {
			phys += encoding.GroupIndexWidth
		}
	}
	rowBit := 0
	for i := range s.Fields {
		if groupOf[i] >= 0 {
			continue
		}
		w := onWireWidth(s.Fields[i].Type)
		l.rows = appendSpan(l.rows, span{a: phys, b: logOff[i], n: w})
		if s.Fields[i].Nullable {
			l.rowNulls = append(l.rowNulls, bitMove{a: rowBit, b: i})
		}
		rowBit++
		phys += w
	}
	l.physBody = phys
	l.physBM = s.BitmapByteSize()
	l.physStride = l.physBody + l.physBM

	l.groups = make([]groupSplice, len(s.Groups))
	for g := range s.Groups {
		grp := &s.Groups[g]
		width, bmOff := groupEntryGeometry(s, g)
		gs := groupSplice{
			idxOff:  s.GroupIndexOffset(g),
			entries: grp.Entries,
			width:   width,
			count:   uint32(min(uint64(len(grp.Entries)/width), math.MaxUint32)),
			bmOff:   bmOff,
		}
		eoff := 0
		for k, m := range grp.Members {
			w := onWireWidth(s.Fields[m.Field].Type)
			gs.members = appendSpan(gs.members, span{a: eoff, b: logOff[m.Field], n: w})
			if s.Fields[m.Field].Nullable {
				gs.nulls = append(gs.nulls, bitMove{a: k, b: m.Field})
			}
			eoff += w
		}
		l.groups[g] = gs
	}
	return l, nil
}

// appendSpan appends sp, merging it into the previous span when both
// sides are contiguous.
func appendSpan(spans []span, sp span) []span {
	if n := len(spans); n > 0 {
		p := &spans[n-1]
		if p.a+p.n == sp.a && p.b+p.n == sp.b {
			p.n += sp.n
			return spans
		}
	}
	return append(spans, sp)
}

// RowExpander turns physical rows of a grouped schema into logical rows:
// the exact 0x01 row the ungrouped twin stores. Not safe for concurrent
// use (it caches the last entry spliced per group).
type RowExpander struct {
	l *rowLayout
	// last[g] is the entry whose member bytes currently sit in the
	// destination buffer lastDst; valid only while lastDst is the buffer
	// being expanded into.
	last    []uint32
	lastDst []byte
	idx     []uint32
}

// NewRowExpander compiles the expansion for a grouped schema.
func NewRowExpander(s *encoding.Schema) (*RowExpander, error) {
	l, err := compileRowLayout(s)
	if err != nil {
		return nil, err
	}
	return &RowExpander{l: l, last: make([]uint32, len(l.groups)), idx: make([]uint32, len(l.groups))}, nil
}

// PhysicalStride is the grouped schema's RecordByteSize.
func (x *RowExpander) PhysicalStride() int { return x.l.physStride }

// LogicalStride is the logical row width: Logical().RecordByteSize().
func (x *RowExpander) LogicalStride() int { return x.l.logStride }

// Expand writes the logical row for physical row phys into dst (grown to
// LogicalStride) and returns it. An index past its group's dictionary is
// ENCODING_INVALID. When dst is the same buffer as the previous call, a
// group whose index did not change is not re-spliced — its member bytes
// are already in place.
func (x *RowExpander) Expand(dst, phys []byte) ([]byte, error) {
	l := x.l
	if len(phys) < l.physStride {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"physical row shorter than the stride",
			map[string]any{"bytes": len(phys), "stride": l.physStride})
	}
	if cap(dst) < l.logStride {
		dst = make([]byte, l.logStride)
	}
	dst = dst[:l.logStride]
	reuse := len(x.lastDst) > 0 && &x.lastDst[0] == &dst[0]
	x.lastDst = nil // invalid until this expansion completes

	for _, sp := range l.rows {
		copy(dst[sp.b:sp.b+sp.n], phys[sp.a:sp.a+sp.n])
	}
	for g := range l.groups {
		gs := &l.groups[g]
		var e uint32
		if gs.idxOff >= 0 {
			e = binary.LittleEndian.Uint32(phys[gs.idxOff:])
		}
		if e >= gs.count {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"group index past the end of its dictionary",
				map[string]any{"group": g, "index": e, "entry_count": gs.count})
		}
		x.idx[g] = e
		if reuse && x.last[g] == e {
			continue
		}
		entry := gs.entries[int(e)*gs.width : (int(e)+1)*gs.width]
		for _, sp := range gs.members {
			copy(dst[sp.b:sp.b+sp.n], entry[sp.a:sp.a+sp.n])
		}
		x.last[g] = e
	}
	if l.logBM > 0 {
		bm := dst[l.logBody:]
		clear(bm)
		if l.physBM > 0 {
			pbm := phys[l.physBody:l.physStride]
			for _, mv := range l.rowNulls {
				if encoding.BitmapIsNull(pbm, mv.a) {
					encoding.BitmapSetNull(bm, mv.b)
				}
			}
		}
		for g := range l.groups {
			gs := &l.groups[g]
			if gs.bmOff < 0 {
				continue
			}
			ebm := gs.entries[int(x.idx[g])*gs.width+gs.bmOff : (int(x.idx[g])+1)*gs.width]
			for _, mv := range gs.nulls {
				if encoding.BitmapIsNull(ebm, mv.a) {
					encoding.BitmapSetNull(bm, mv.b)
				}
			}
		}
	}
	x.lastDst = dst
	return dst, nil
}

// GroupIndex returns the entry index group g resolved to in the most
// recent successful Expand (0 for a constant group).
func (x *RowExpander) GroupIndex(g int) uint32 { return x.idx[g] }

// logicalReader presents the physical record region of a grouped cohort
// as the logical (0x01-shaped) record stream. It reads exactly one
// physical row per logical row and never reads ahead, so the underlying
// reader's position after a whole logical record is exactly the end of
// that physical record (filter-to-file copies physical rows by position).
type logicalReader struct {
	r     io.Reader
	x     *RowExpander
	phys  []byte
	row   []byte
	pos   int // read cursor within row; == len(row) when drained
	fresh bool
	off   int64
}

// NewLogicalStream returns a reader of the logical record stream of s
// over r (positioned at the first physical record), and the schema that
// describes that stream (s.Logical()). Byte-level record consumers — a
// decoder of their own, an exporter — read through it and walk the
// returned schema exactly as they walk a 0x01 cohort. For a schema
// without groups it returns (r, s) unchanged.
func NewLogicalStream(r io.Reader, s *encoding.Schema) (io.Reader, *encoding.Schema, error) {
	if !s.HasGroups() {
		return r, s, nil
	}
	x, err := NewRowExpander(s)
	if err != nil {
		return nil, nil, err
	}
	return &logicalReader{r: r, x: x, phys: make([]byte, x.PhysicalStride())}, s.Logical(), nil
}

func (lr *logicalReader) next() error {
	if _, err := io.ReadFull(lr.r, lr.phys); err != nil {
		return err // io.EOF on a clean boundary, io.ErrUnexpectedEOF mid-record
	}
	row, err := lr.x.Expand(lr.row, lr.phys)
	if err != nil {
		lr.row = lr.row[:0]
		lr.pos = 0
		return err
	}
	lr.row, lr.pos, lr.fresh = row, 0, true
	return nil
}

func (lr *logicalReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if lr.pos >= len(lr.row) {
		if err := lr.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, lr.row[lr.pos:])
	lr.pos += n
	lr.off += int64(n)
	return n, nil
}

// Seek supports only forward relative seeks (whence io.SeekCurrent,
// offset >= 0) — what a decode plan's SkipBytes issues. Whole logical
// rows are skipped as whole physical rows without being expanded.
func (lr *logicalReader) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekCurrent || offset < 0 {
		return 0, errors.NewCodedError(errors.ENCODING_INTERNAL,
			"grouped record stream supports only forward relative seeks")
	}
	for offset > 0 {
		if lr.pos >= len(lr.row) {
			ls := int64(lr.x.LogicalStride())
			if offset >= ls {
				if err := advanceReader(lr.r, lr.x.PhysicalStride()); err != nil {
					return lr.off, err
				}
				lr.fresh = false
				offset -= ls
				lr.off += ls
				continue
			}
			if err := lr.next(); err != nil {
				return lr.off, err
			}
		}
		n := int64(len(lr.row) - lr.pos)
		if offset < n {
			n = offset
		}
		lr.pos += int(n)
		lr.off += n
		offset -= n
	}
	return lr.off, nil
}

// GroupIndex reports the entry index group g resolved to on the
// physical row most recently expanded, and false when no row has been
// expanded yet or the last row was skipped whole.
func (lr *logicalReader) GroupIndex(g int) (uint32, bool) {
	if !lr.fresh {
		return 0, false
	}
	return lr.x.GroupIndex(g), true
}
