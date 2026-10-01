package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// GroupSpec declares one parent group by member field NAME.
type GroupSpec struct {
	// Kind is GroupKindIndexed (a dictionary + a u32 per row) or
	// GroupKindConstant (one entry, nothing per row).
	Kind encoding.GroupKind
	// Members names the member fields, in any order.
	Members []string
	// Key optionally names the subset of Members whose values determine
	// the tuple. When set, the encoder REFUSES a row whose non-key
	// members disagree with an earlier row carrying the same key — a
	// declaration that is not actually a parent group is an error, not
	// a silently larger dictionary. Empty means every member is key.
	Key []string
	// Ordinal, when > 0, is the 1-based position Label reports instead
	// of the spec's position in the list it was passed in. A caller
	// that drops a declared group before encoding (the viability gate)
	// sets it so the surviving groups keep the names the user declared
	// them under. Never written to the file.
	Ordinal int
}

// Label is the display name error details and reports use for spec g
// (0-based position in the spec list). The format has no group-name
// slot, so a group is named by its 1-based declaration position and
// its key (or, with no key, its members): "group 2 [key: order_id]".
func (sp GroupSpec) Label(g int) string {
	n := g + 1
	if sp.Ordinal > 0 {
		n = sp.Ordinal
	}
	if len(sp.Key) > 0 {
		return fmt.Sprintf("group %d [key: %s]", n, strings.Join(sp.Key, ","))
	}
	return fmt.Sprintf("group %d [%s]", n, strings.Join(sp.Members, ","))
}

// declErr is a declaration-level refusal: the spec list, not the data,
// is wrong. Codes are PULSE_GROUP_* so a caller can tell a bad
// declaration from a corrupt file (ENCODING_INVALID).
func declErr(code errors.Code, msg string, details map[string]any) error {
	return errors.NewCodedErrorWithDetails(code, "parent group: "+msg, details)
}

// GroupEncoder turns logical rows (the 0x01 row of an ungrouped schema)
// into physical rows of the grouped schema, building each group's
// dictionary as it goes: a row's tuple is looked up, or appended as a
// new entry. It is the one encode path shared by import and
// retro-dedup; the dictionary must precede the records on the wire, so
// callers spool the physical rows and write the preamble (Schema) last.
type GroupEncoder struct {
	schema *encoding.Schema
	l      *rowLayout
	groups []encGroup
	labels []string
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
// names are resolved here: an unknown name is PULSE_GROUP_FIELD_UNKNOWN,
// a field named by two groups PULSE_GROUP_FIELD_CONFLICT (naming both
// groups by Label), and an empty group, a field named twice in one
// group or a key naming a non-member PULSE_GROUP_DECLARATION_INVALID. A
// shape the format cannot hold (e.g. a zero stride) is ENCODING_INVALID.
func NewGroupEncoder(flat *encoding.Schema, specs []GroupSpec) (*GroupEncoder, error) {
	if flat.HasGroups() {
		return nil, groupErr("encoder input must be an ungrouped schema (use Logical())", nil)
	}
	byName := make(map[string]int, len(flat.Fields))
	for i := range flat.Fields {
		byName[flat.Fields[i].Name] = i
	}
	owner := make(map[int]int)
	out := &encoding.Schema{Fields: append([]encoding.Field(nil), flat.Fields...)}
	keyed := make([]map[int]bool, len(specs))
	labels := make([]string, len(specs))
	for g, sp := range specs {
		labels[g] = sp.Label(g)
	}
	for g, sp := range specs {
		if len(sp.Members) == 0 {
			return nil, declErr(errors.PULSE_GROUP_DECLARATION_INVALID, labels[g]+" has no members",
				map[string]any{"group": g, "group_label": labels[g]})
		}
		var members []int
		for _, name := range sp.Members {
			fi, ok := byName[name]
			if !ok {
				return nil, declErr(errors.PULSE_GROUP_FIELD_UNKNOWN,
					fmt.Sprintf("%s names field %q, which is not in the schema", labels[g], name),
					map[string]any{"group": g, "group_label": labels[g], "field": name})
			}
			if prev, dup := owner[fi]; dup {
				if prev == g {
					return nil, declErr(errors.PULSE_GROUP_DECLARATION_INVALID,
						fmt.Sprintf("%s names field %q twice", labels[g], name),
						map[string]any{"group": g, "group_label": labels[g], "field": name})
				}
				return nil, declErr(errors.PULSE_GROUP_FIELD_CONFLICT,
					fmt.Sprintf("field %q is a member of two groups: %s and %s", name, labels[prev], labels[g]),
					map[string]any{"field": name, "groups": []int{prev, g}, "group_labels": []string{labels[prev], labels[g]}})
			}
			owner[fi] = g
			members = append(members, fi)
		}
		keyed[g] = map[int]bool{}
		for _, name := range sp.Key {
			fi, ok := byName[name]
			if og, member := owner[fi]; !ok || !member || og != g {
				return nil, declErr(errors.PULSE_GROUP_DECLARATION_INVALID,
					fmt.Sprintf("%s: key %q is not a member of the group", labels[g], name),
					map[string]any{"group": g, "group_label": labels[g], "field": name})
			}
			keyed[g][fi] = true
		}
		sortInts(members)
		grp := encoding.Group{Kind: sp.Kind}
		for _, fi := range members {
			grp.Members = append(grp.Members, encoding.GroupMember{Field: fi, Key: len(sp.Key) == 0 || keyed[g][fi]})
		}
		out.Groups = append(out.Groups, grp)
	}
	l, err := compileRowLayout(out)
	if err != nil {
		return nil, err
	}
	e := &GroupEncoder{schema: out, l: l, groups: make([]encGroup, len(specs)), labels: labels}
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
func (e *GroupEncoder) Schema() *encoding.Schema {
	for g := range e.schema.Groups {
		e.schema.Groups[g].Entries = e.l.groups[g].entries
	}
	return e.schema
}

// EncodeRow appends the physical row for logical row lrow to dst.
//
// Failures carry details naming the group (index and Label), the
// offending field and the row index (0-based, counted by this encoder):
// a constant group whose members change, or a keyed group whose non-key
// member disagrees with the entry its key already selected, is
// PULSE_GROUP_MEMBER_NOT_CONSTANT; a dictionary that would outgrow
// MaxGroupEntries is PULSE_GROUP_ENTRIES_EXHAUSTED (never a wrap). A
// logical row of the wrong width is ENCODING_INVALID.
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
				if encoding.BitmapIsNull(lbm, mv.b) {
					encoding.BitmapSetNull(ent[gs.bmOff:], mv.a)
				}
			}
		}
		key := e.entryKey(g, ent)
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
			if uint64(gs.count)+1 > encoding.MaxGroupEntries {
				return dst[:start], declErr(errors.PULSE_GROUP_ENTRIES_EXHAUSTED,
					fmt.Sprintf("%s: dictionary would exceed the u32 index space at record %d", e.labels[g], e.rows),
					map[string]any{"group": g, "group_label": e.labels[g], "entry_count": gs.count, "max_entries": encoding.MaxGroupEntries, "row": e.rows})
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
			if encoding.BitmapIsNull(lbm, mv.b) {
				encoding.BitmapSetNull(pbm, mv.a)
			}
		}
	}
	e.rows++
	return dst, nil
}

// entryKey returns the lookup key of entry ent in group g: the entry
// itself for a group with no declared key, else the key members' bytes
// and null bits packed into the group's scratch key buffer (valid until
// the next call for the same group).
func (e *GroupEncoder) entryKey(g int, ent []byte) []byte {
	eg := &e.groups[g]
	if eg.keyAll {
		return ent
	}
	gs := &e.l.groups[g]
	clear(eg.key)
	for _, sp := range eg.keySp {
		copy(eg.key[sp.b:sp.b+sp.n], ent[sp.a:sp.a+sp.n])
	}
	for _, mv := range eg.keyBits {
		if encoding.BitmapIsNull(ent[gs.bmOff:], mv.a) {
			encoding.BitmapSetNull(eg.key[eg.keyBM:], mv.b)
		}
	}
	return eg.key
}

// Seed pre-loads every group dictionary with seed's entries, in order,
// before any row is encoded: a row whose tuple (or key) is already there
// gets that entry's index, and a new tuple is appended AFTER them. It is
// how a shard joins an archive — the archive's canonical dictionary is
// the seed, so every entry the archive's stored shards reference keeps
// its index (the union is canonical-first, the prefix rule every shard
// relies on) and only the arriving shard's rows are renumbered.
//
// seed must declare the encoder's exact group layout — same group
// count, kinds, members and key flags — with the same entry widths;
// anything else is ENCODING_INVALID (a layout reconciliation is the
// caller's job, before the encoder is built). Seeding goes through the
// same key rule EncodeRow uses, so a keyed group whose seed repeats a
// key is refused as a corrupt dictionary, and the u32 ceiling applies
// to the seeded count exactly as to an appended one
// (PULSE_GROUP_ENTRIES_EXHAUSTED). Seed must be called before the
// first EncodeRow.
func (e *GroupEncoder) Seed(seed *encoding.Schema) error {
	if e.rows > 0 {
		return groupErr("Seed must precede the first encoded row", map[string]any{"rows": e.rows})
	}
	if len(seed.Groups) != len(e.schema.Groups) {
		return groupErr("seed schema declares a different group count",
			map[string]any{"seed_groups": len(seed.Groups), "encoder_groups": len(e.schema.Groups)})
	}
	for g := range e.schema.Groups {
		if err := sameGroupDescriptor(e.schema, seed, g); err != nil {
			return err
		}
		gs := &e.l.groups[g]
		eg := &e.groups[g]
		if w := seed.GroupEntryWidth(g); w != gs.width {
			return groupErr("seed group entry width differs from the encoder's",
				map[string]any{"group": g, "seed_width": w, "encoder_width": gs.width})
		}
		entries := seed.Groups[g].Entries
		if gs.width <= 0 || len(entries)%gs.width != 0 {
			return groupErr("seed group dictionary is not a whole number of entries",
				map[string]any{"group": g, "entry_width": gs.width, "bytes": len(entries)})
		}
		for off := 0; off < len(entries); off += gs.width {
			ent := entries[off : off+gs.width]
			key := e.entryKey(g, ent)
			if _, dup := eg.keyOf[string(key)]; dup {
				return groupErr("seed group dictionary repeats a key",
					map[string]any{"group": g, "entry": off / gs.width})
			}
			if uint64(gs.count)+1 > encoding.MaxGroupEntries {
				return declErr(errors.PULSE_GROUP_ENTRIES_EXHAUSTED,
					fmt.Sprintf("%s: seed dictionary exceeds the u32 index space", e.labels[g]),
					map[string]any{"group": g, "group_label": e.labels[g], "entry_count": gs.count, "max_entries": encoding.MaxGroupEntries})
			}
			eg.keyOf[string(key)] = gs.count
			gs.entries = append(gs.entries, ent...)
			gs.count++
		}
	}
	return nil
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
	msg := "member is not constant within the group's key"
	if s.Groups[g].Kind == encoding.GroupKindConstant {
		msg = "constant member changes value"
	}
	return declErr(errors.PULSE_GROUP_MEMBER_NOT_CONSTANT,
		fmt.Sprintf("%s: %s: field %q at record %d", e.labels[g], msg, field, e.rows),
		map[string]any{"group": g, "group_label": e.labels[g], "field": field, "row": e.rows})
}

// ForEachRecord reads fixed-width records of stride bytes from r and
// calls fn with each one, returning how many it read. The slice fn
// receives is reused between calls. A clean end of input between two
// records ends the walk; input ending MID-record is ENCODING_INVALID
// (details trailing_bytes, records_read) rather than a silently dropped
// tail. Every whole-cohort record pass of the dedup family — encoding,
// constant detection, candidate detection — walks through here, so the
// three cannot disagree about where a cohort ends.
func ForEachRecord(r io.Reader, stride int, fn func(rec []byte) error) (int64, error) {
	if stride <= 0 {
		return 0, groupErr("record stride must be positive", map[string]any{"stride": stride})
	}
	rec := make([]byte, stride)
	var n int64
	for {
		got, err := io.ReadFull(r, rec)
		if err == io.EOF {
			return n, nil
		}
		if err == io.ErrUnexpectedEOF {
			return n, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"cohort payload ends mid-record",
				map[string]any{"trailing_bytes": got, "records_read": n})
		}
		if err != nil {
			return n, err
		}
		if err := fn(rec); err != nil {
			return n, err
		}
		n++
	}
}

// EncodeStream encodes every logical row read from lr (a logical record
// stream of the encoder's input schema, e.g. from NewLogicalStream) and
// writes the physical rows to spool, in order. It returns the number of
// rows encoded. The dictionaries precede the records on the wire, so
// spool is NOT the cohort: once the stream is drained the caller writes
// WritePreamble(Schema()) and then the spooled bytes. Holding the spool
// is the caller's choice — memory (DedupCohort) or a temp file
// (io.DedupJob, whose memory stays O(dictionaries) whatever the cohort
// size). A failing row leaves spool holding only the rows before it.
func (e *GroupEncoder) EncodeStream(spool io.Writer, lr io.Reader) (int64, error) {
	var phys []byte
	n, err := ForEachRecord(lr, e.LogicalStride(), func(row []byte) error {
		var err error
		if phys, err = e.EncodeRow(phys[:0], row); err != nil {
			return err
		}
		if _, err := spool.Write(phys); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "dedup: spooling records")
		}
		return nil
	})
	return n, err
}

// DedupCohort reads a whole cohort (header, schema, records — any
// version, grouped or not) from src and writes its grouped equivalent,
// grouped by specs, to dst. The source is read through its LOGICAL
// stream, so a grouped source is regrouped from scratch. The physical
// rows are spooled in memory because the dictionaries precede the
// records on the wire — it is the small-cohort form of the one encode
// path (GroupEncoder.EncodeStream) that io.DedupJob drives with a
// temp-file spool. A truncated trailing record is ENCODING_INVALID
// rather than silently dropped. Returns the written schema and the
// record count.
func DedupCohort(dst io.Writer, src io.Reader, specs []GroupSpec) (*encoding.Schema, int64, error) {
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
	var spool bytes.Buffer
	rows, err := enc.EncodeStream(&spool, lr)
	if err != nil {
		return nil, 0, err
	}
	out := enc.Schema()
	if err := WritePreamble(dst, out); err != nil {
		return nil, 0, err
	}
	if _, err := spool.WriteTo(dst); err != nil {
		return nil, 0, errors.WrapCodedError(err, errors.ENCODING_IO, "dedup: writing records")
	}
	return out, rows, nil
}
