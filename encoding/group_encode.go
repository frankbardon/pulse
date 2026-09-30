package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// GroupSpec declares one parent group by member field NAME.
type GroupSpec struct {
	// Kind is GroupKindIndexed (a dictionary + a u32 per row) or
	// GroupKindConstant (one entry, nothing per row).
	Kind GroupKind
	// Members names the member fields, in any order.
	Members []string
	// Key optionally names the subset of Members whose values determine
	// the tuple. When set, the encoder REFUSES a row whose non-key
	// members disagree with an earlier row carrying the same key — a
	// declaration that is not actually a parent group is an error, not
	// a silently larger dictionary. Empty means every member is key.
	Key []string
}

// GroupEncoder turns logical rows (the 0x01 row of an ungrouped schema)
// into physical rows of the grouped schema, building each group's
// dictionary as it goes: a row's tuple is looked up, or appended as a
// new entry. It is the one encode path shared by import and
// retro-dedup; the dictionary must precede the records on the wire, so
// callers spool the physical rows and write the preamble (Schema) last.
type GroupEncoder struct {
	schema *Schema
	l      *rowLayout
	groups []encGroup
	rows   int64
}

type encGroup struct {
	keyOf   map[string]uint32
	keyAll  bool
	keySp   []span    // entry offset -> key-buffer offset (key members)
	keyBits []bitMove // entry bit -> key-buffer bit (key members)
	keyBM   int       // key-buffer bitmap offset
	keyLen  int
	entry   []byte // scratch
	key     []byte // scratch
}

// NewGroupEncoder prepares an encoder that groups flat (a schema without
// groups — pass a grouped schema's Logical() view) by specs. Field
// names are resolved here: an unknown name, a field named by two
// groups, a key naming a non-member, or a shape the format cannot hold
// is ENCODING_INVALID naming the field and groups.
func NewGroupEncoder(flat *Schema, specs []GroupSpec) (*GroupEncoder, error) {
	if flat.HasGroups() {
		return nil, groupErr("encoder input must be an ungrouped schema (use Logical())", nil)
	}
	byName := make(map[string]int, len(flat.Fields))
	for i := range flat.Fields {
		byName[flat.Fields[i].Name] = i
	}
	owner := make(map[int]int)
	out := &Schema{Fields: append([]Field(nil), flat.Fields...)}
	keyed := make([]map[int]bool, len(specs))
	for g, sp := range specs {
		if len(sp.Members) == 0 {
			return nil, groupErr("a group needs at least one member", map[string]any{"group": g})
		}
		var members []int
		for _, name := range sp.Members {
			fi, ok := byName[name]
			if !ok {
				return nil, groupErr(fmt.Sprintf("group member %q names no field in the schema", name),
					map[string]any{"group": g, "field": name})
			}
			if prev, dup := owner[fi]; dup {
				return nil, groupErr(fmt.Sprintf("field %q is a member of two groups", name),
					map[string]any{"field": name, "groups": []int{prev, g}})
			}
			owner[fi] = g
			members = append(members, fi)
		}
		keyed[g] = map[int]bool{}
		for _, name := range sp.Key {
			fi, ok := byName[name]
			if og, member := owner[fi]; !ok || !member || og != g {
				return nil, groupErr(fmt.Sprintf("group key %q is not a member of the group", name),
					map[string]any{"group": g, "field": name})
			}
			keyed[g][fi] = true
		}
		sortInts(members)
		grp := Group{Kind: sp.Kind}
		for _, fi := range members {
			grp.Members = append(grp.Members, GroupMember{Field: fi, Key: len(sp.Key) == 0 || keyed[g][fi]})
		}
		out.Groups = append(out.Groups, grp)
	}
	l, err := compileRowLayout(out)
	if err != nil {
		return nil, err
	}
	e := &GroupEncoder{schema: out, l: l, groups: make([]encGroup, len(specs))}
	for g := range out.Groups {
		eg := &e.groups[g]
		eg.keyOf = map[string]uint32{}
		gs := &l.groups[g]
		eg.entry = make([]byte, gs.width)
		eg.keyAll = len(specs[g].Key) == 0
		if eg.keyAll {
			continue
		}
		// Key buffer: key members' bytes, then their null bits.
		eoff, koff, kbit := 0, 0, 0
		var nKey int
		for _, m := range out.Groups[g].Members {
			if m.Key {
				nKey++
			}
		}
		for k, m := range out.Groups[g].Members {
			w := onWireWidth(out.Fields[m.Field].Type)
			if m.Key {
				eg.keySp = append(eg.keySp, span{a: eoff, b: koff, n: w})
				if out.Fields[m.Field].Nullable {
					eg.keyBits = append(eg.keyBits, bitMove{a: k, b: kbit})
				}
				koff += w
				kbit++
			}
			eoff += w
		}
		eg.keyBM = koff
		eg.keyLen = koff + (nKey+7)/8
		eg.key = make([]byte, eg.keyLen)
	}
	return e, nil
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// PhysicalStride is the grouped schema's RecordByteSize.
func (e *GroupEncoder) PhysicalStride() int { return e.l.physStride }

// LogicalStride is the width of the logical rows EncodeRow takes.
func (e *GroupEncoder) LogicalStride() int { return e.l.logStride }

// Rows is the number of rows encoded so far.
func (e *GroupEncoder) Rows() int64 { return e.rows }

// Schema returns the grouped schema with every dictionary built so far.
// Its Entries alias the encoder's storage: take it after the last row.
func (e *GroupEncoder) Schema() *Schema {
	for g := range e.schema.Groups {
		e.schema.Groups[g].Entries = e.l.groups[g].entries
	}
	return e.schema
}

// EncodeRow appends the physical row for logical row lrow to dst.
//
// Failures are ENCODING_INVALID with details naming the group, the
// offending field and the row index (0-based, counted by this encoder):
// a constant group whose members change, a keyed group whose non-key
// member disagrees with the entry its key already selected, or a
// dictionary that would outgrow MaxGroupEntries.
func (e *GroupEncoder) EncodeRow(dst, lrow []byte) ([]byte, error) {
	l := e.l
	if len(lrow) != l.logStride {
		return dst, groupErr("logical row has the wrong width", map[string]any{"bytes": len(lrow), "stride": l.logStride, "row": e.rows})
	}
	start := len(dst)
	dst = append(dst, make([]byte, l.physStride)...)
	phys := dst[start:]
	var lbm []byte
	if l.logBM > 0 {
		lbm = lrow[l.logBody:]
	}
	for g := range l.groups {
		gs := &l.groups[g]
		eg := &e.groups[g]
		ent := eg.entry
		clear(ent)
		for _, sp := range gs.members {
			copy(ent[sp.a:sp.a+sp.n], lrow[sp.b:sp.b+sp.n])
		}
		if gs.bmOff >= 0 {
			for _, mv := range gs.nulls {
				if BitmapIsNull(lbm, mv.b) {
					BitmapSetNull(ent[gs.bmOff:], mv.a)
				}
			}
		}
		key := ent
		if !eg.keyAll {
			clear(eg.key)
			for _, sp := range eg.keySp {
				copy(eg.key[sp.b:sp.b+sp.n], ent[sp.a:sp.a+sp.n])
			}
			for _, mv := range eg.keyBits {
				if BitmapIsNull(ent[gs.bmOff:], mv.a) {
					BitmapSetNull(eg.key[eg.keyBM:], mv.b)
				}
			}
			key = eg.key
		}
		idx, ok := eg.keyOf[string(key)]
		switch {
		case ok:
			if !eg.keyAll || gs.idxOff < 0 {
				have := gs.entries[int(idx)*gs.width : (int(idx)+1)*gs.width]
				if !bytes.Equal(have, ent) {
					return dst[:start], e.mismatch(g, have, ent)
				}
			}
		case gs.idxOff < 0 && gs.count > 0:
			// Constant group, and this row's tuple differs from row 0's.
			return dst[:start], e.mismatch(g, gs.entries[:gs.width], ent)
		default:
			if uint64(gs.count)+1 > MaxGroupEntries {
				return dst[:start], groupErr("group dictionary would exceed the u32 index space",
					map[string]any{"group": g, "entry_count": gs.count, "max_entries": MaxGroupEntries, "row": e.rows})
			}
			idx = gs.count
			gs.entries = append(gs.entries, ent...)
			gs.count++
			eg.keyOf[string(key)] = idx
		}
		if gs.idxOff >= 0 {
			binary.LittleEndian.PutUint32(phys[gs.idxOff:], idx)
		}
	}
	for _, sp := range l.rows {
		copy(phys[sp.a:sp.a+sp.n], lrow[sp.b:sp.b+sp.n])
	}
	if l.physBM > 0 {
		pbm := phys[l.physBody:]
		for _, mv := range l.rowNulls {
			if BitmapIsNull(lbm, mv.b) {
				BitmapSetNull(pbm, mv.a)
			}
		}
	}
	e.rows++
	return dst, nil
}

// mismatch names the first member whose bytes or null bit differ
// between the entry the key selected and this row's tuple.
func (e *GroupEncoder) mismatch(g int, have, got []byte) error {
	s := e.schema
	field := ""
	for k, m := range s.Groups[g].Members {
		if !bytes.Equal(s.GroupMemberBytes(g, k, have), s.GroupMemberBytes(g, k, got)) ||
			s.GroupMemberIsNull(g, k, have) != s.GroupMemberIsNull(g, k, got) {
			field = s.Fields[m.Field].Name
			break
		}
	}
	msg := "group member is not constant within the group's key"
	if s.Groups[g].Kind == GroupKindConstant {
		msg = "constant group member changes value"
	}
	return groupErr(fmt.Sprintf("%s: field %q", msg, field),
		map[string]any{"group": g, "field": field, "row": e.rows})
}

// DedupCohort reads a whole cohort (header, schema, records — any
// version, grouped or not) from src and writes its grouped equivalent,
// grouped by specs, to dst. The source is read through its LOGICAL
// stream, so a grouped source is regrouped from scratch. The physical
// rows are spooled in memory because the dictionaries precede the
// records on the wire. A truncated trailing record is ENCODING_INVALID
// rather than silently dropped. Returns the written schema and the
// record count.
func DedupCohort(dst io.Writer, src io.Reader, specs []GroupSpec) (*Schema, int64, error) {
	schema, _, err := ReadPreamble(src)
	if err != nil {
		return nil, 0, err
	}
	lr, flat, err := NewLogicalStream(src, schema)
	if err != nil {
		return nil, 0, err
	}
	enc, err := NewGroupEncoder(flat, specs)
	if err != nil {
		return nil, 0, err
	}
	row := make([]byte, enc.LogicalStride())
	var spool []byte
	for {
		n, err := io.ReadFull(lr, row)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			return nil, 0, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"dedup: cohort payload ends mid-record",
				map[string]any{"trailing_bytes": n, "records_read": enc.Rows()})
		}
		if err != nil {
			return nil, 0, err
		}
		if spool, err = enc.EncodeRow(spool, row); err != nil {
			return nil, 0, err
		}
	}
	out := enc.Schema()
	if err := WritePreamble(dst, out); err != nil {
		return nil, 0, err
	}
	if _, err := dst.Write(spool); err != nil {
		return nil, 0, errors.WrapCodedError(err, errors.ENCODING_IO, "dedup: writing records")
	}
	return out, enc.Rows(), nil
}
