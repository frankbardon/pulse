package encoding

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
)

// Parent groups (format 0x02).
//
// A parent group is a set of fields whose values repeat as a unit across
// many rows — the parent block of a denormalised join. Instead of
// carrying those fields in every row, a 0x02 cohort stores each distinct
// member TUPLE once, in the group's dictionary inside the schema block,
// and every row carries a fixed-width u32 index into it. The stride stays
// fixed and derivable from the schema alone.
//
// Terms used throughout:
//
//   - LOGICAL schema: Schema.Fields, every field in its original order.
//     It is what operators, records, dictionaries, inspect and every
//     caller see. A field's position ("field index") is ALWAYS its
//     logical position; there is no physical field numbering in any API.
//   - LOGICAL row: the 0x01 on-wire row of the logical schema — each
//     field's on-wire bytes in field order (bit-packed = one whole byte),
//     then the ceil(len(Fields)/8)-byte null bitmap when any field is
//     nullable. It is exactly the row an ungrouped cohort stores.
//   - PHYSICAL row: what a 0x02 grouped cohort stores per record:
//     one u32 index per INDEXED group (group order), then the ROW fields
//     (fields in no group) in logical order, then the NARROWED null
//     bitmap over the row fields only. Schema.RecordByteSize,
//     BitmapByteSize and HasBitmap describe the physical row.
//   - ENTRY: one dictionary tuple — every member's logical on-wire bytes
//     in member order, then a ceil(members/8)-byte member null bitmap
//     when any member is nullable. Entries are fixed-width, so a group's
//     dictionary is one flat byte slice.
//
// Decode yields exactly the ungrouped twin's values. The map decoders
// expand a physical row back into its logical row (RowExpander) and run
// the unchanged 0x01 decoders over it; the reuse decoders write the
// physical row straight into the record at logical positions, skipping
// groups whose index did not change (group_decode.go).

// GroupKind is the per-group kind byte of the group descriptor.
type GroupKind uint8

const (
	// GroupKindIndexed is a dictionary of N entries addressed by a u32
	// index carried in every physical row.
	GroupKindIndexed GroupKind = 0
	// GroupKindConstant is a dictionary of exactly ONE entry and no
	// per-row index (index width 0): the members hold the same value —
	// or the same null — on every row. It is how a global-constant field
	// is elided from the row.
	GroupKindConstant GroupKind = 1
)

// GroupIndexWidth is the on-wire width of an indexed group's per-row
// index. The descriptor carries the width explicitly; this binary
// accepts only this value for GroupKindIndexed and 0 for
// GroupKindConstant.
const GroupIndexWidth = 4

// MaxGroupEntries is the entry-count ceiling of an indexed group: the
// u32 index space. A variable (not a const) only so tests can shrink it.
var MaxGroupEntries uint64 = math.MaxUint32 + 1

// GroupMember names one member of a group by its LOGICAL field index.
type GroupMember struct {
	// Field is the member's position in Schema.Fields.
	Field int
	// Key marks the member as part of the group's declared key: the
	// members whose values determine the tuple. Informational for
	// decode (every member is read from the entry either way); an
	// encoder validates that non-key members are functionally dependent
	// on the key, and a future precompute may rely on key members
	// being a bijection with the entry index.
	Key bool
}

// Group is one parent-group descriptor plus its dictionary.
type Group struct {
	Kind GroupKind
	// Members are in strictly ascending Field order.
	Members []GroupMember
	// Entries is the flat dictionary: EntryCount × EntryWidth bytes.
	// Entry e occupies Entries[e*w : (e+1)*w]. Never strings: a
	// categorical member's bytes are its own dictionary ID, and each
	// member field keeps its own Field.Dictionary unchanged.
	Entries []byte
}

// HasGroups reports whether the schema declares any parent group — i.e.
// whether its physical row differs from its logical row.
func (s *Schema) HasGroups() bool { return len(s.Groups) > 0 }

// Logical returns the schema's ungrouped view: the same Fields (the
// slice is SHARED, so *Field pointers and field indices agree), with no
// group declared. Its RecordByteSize / BitmapByteSize / HasBitmap
// describe the logical row. For a schema without groups it returns s.
func (s *Schema) Logical() *Schema {
	if !s.HasGroups() {
		return s
	}
	return &Schema{Fields: s.Fields}
}

// onWireWidth is declared in reader_runskip.go: 1 byte for a bit-packed
// field, 16 for decimal128, fixedWidthBytes otherwise.

// memberSets returns, per logical field, the owning group (or -1) and
// the member slot within it. It trusts the descriptor's shape only as
// far as bounds; ValidateGroups is the full check.
func (s *Schema) memberSets() (groupOf, slotOf []int) {
	groupOf = make([]int, len(s.Fields))
	slotOf = make([]int, len(s.Fields))
	for i := range groupOf {
		groupOf[i] = -1
	}
	for g := range s.Groups {
		for k, m := range s.Groups[g].Members {
			if m.Field >= 0 && m.Field < len(s.Fields) {
				groupOf[m.Field] = g
				slotOf[m.Field] = k
			}
		}
	}
	return groupOf, slotOf
}

// FieldGroup reports whether logical field fi is a group member and, if
// so, which group and member slot. It is the entry point for code that
// wants to work per dictionary entry instead of per row (filter
// precompute): the member's value in entry e is
// GroupMemberBytes(g, k, GroupEntry(g, e)).
func (s *Schema) FieldGroup(fi int) (g, k int, ok bool) {
	for g := range s.Groups {
		for k, m := range s.Groups[g].Members {
			if m.Field == fi {
				return g, k, true
			}
		}
	}
	return -1, -1, false
}

// GroupEntryWidth returns the byte width of one entry of group g: the
// members' on-wire widths plus the member null bitmap when any member is
// nullable.
func (s *Schema) GroupEntryWidth(g int) int {
	w, _ := s.groupEntryGeometry(g)
	return w
}

// GroupMemberRowBytes returns the on-wire bytes group g's members
// occupy in a LOGICAL row, null bits excluded: what each row stops
// carrying when the group is formed, before an indexed group's
// GroupIndexWidth is added back. With GroupEntryCount and
// GroupEntryWidth it is everything a viability check needs.
func (s *Schema) GroupMemberRowBytes(g int) int {
	w, bm := s.groupEntryGeometry(g)
	if bm >= 0 {
		return bm
	}
	return w
}

// groupEntryGeometry returns group g's entry width and the offset of the
// member null bitmap within an entry (-1 when no member is nullable).
func (s *Schema) groupEntryGeometry(g int) (width, bmOff int) {
	grp := &s.Groups[g]
	anyNullable := false
	for _, m := range grp.Members {
		if m.Field < 0 || m.Field >= len(s.Fields) {
			continue
		}
		width += onWireWidth(s.Fields[m.Field].Type)
		anyNullable = anyNullable || s.Fields[m.Field].Nullable
	}
	if !anyNullable {
		return width, -1
	}
	return width + (len(grp.Members)+7)/8, width
}

// GroupEntryCount returns the number of entries in group g's dictionary.
func (s *Schema) GroupEntryCount(g int) int {
	w := s.GroupEntryWidth(g)
	if w <= 0 {
		return 0
	}
	return len(s.Groups[g].Entries) / w
}

// GroupEntry returns entry e of group g (a subslice of Entries — do not
// mutate it).
func (s *Schema) GroupEntry(g, e int) []byte {
	w := s.GroupEntryWidth(g)
	return s.Groups[g].Entries[e*w : (e+1)*w]
}

// GroupMemberBytes returns member k's on-wire bytes within entry — the
// same bytes the member occupies in a logical row.
func (s *Schema) GroupMemberBytes(g, k int, entry []byte) []byte {
	off := 0
	for j, m := range s.Groups[g].Members {
		w := onWireWidth(s.Fields[m.Field].Type)
		if j == k {
			return entry[off : off+w]
		}
		off += w
	}
	return nil
}

// GroupMemberIsNull reports whether member k is null in entry.
func (s *Schema) GroupMemberIsNull(g, k int, entry []byte) bool {
	_, bmOff := s.groupEntryGeometry(g)
	if bmOff < 0 || !s.Fields[s.Groups[g].Members[k].Field].Nullable {
		return false
	}
	return BitmapIsNull(entry[bmOff:], k)
}

// GroupIndexOffset returns the physical byte offset of group g's u32
// index within a physical row, or -1 for a constant group (no per-row
// index).
func (s *Schema) GroupIndexOffset(g int) int {
	if s.Groups[g].Kind != GroupKindIndexed {
		return -1
	}
	off := 0
	for i := 0; i < g; i++ {
		if s.Groups[i].Kind == GroupKindIndexed {
			off += GroupIndexWidth
		}
	}
	return off
}

// DecodeGroupEntry writes entry e of group g into sink at the members'
// LOGICAL field indices, with exactly the side effects a full row decode
// has for those fields (value + typed wide value; a null member gets
// SetNullFieldAt + SetNumericAt(0)). It does not call ClearForRow and
// touches no non-member field. It is the per-entry evaluation hook: a
// predicate over member fields can be evaluated once per entry through a
// record built over this schema.
func (s *Schema) DecodeGroupEntry(g, e int, sink IndexedReusableRecord) error {
	if g < 0 || g >= len(s.Groups) || e < 0 || e >= s.GroupEntryCount(g) {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"group entry out of range",
			map[string]any{"group": g, "entry": e})
	}
	typed, _ := sink.(TypedSetRecord)
	entry := s.GroupEntry(g, e)
	for k, m := range s.Groups[g].Members {
		f := &s.Fields[m.Field]
		if s.GroupMemberIsNull(g, k, entry) {
			sink.SetNullFieldAt(m.Field)
			sink.SetNumericAt(m.Field, 0)
			continue
		}
		if err := writeFieldBytes(sink, typed, f, m.Field, s.GroupMemberBytes(g, k, entry)); err != nil {
			return err
		}
	}
	return nil
}

// GroupEntryDecoder writes a chosen subset of one group's members from
// any entry into a record, with the member geometry resolved once. It is
// DecodeGroupEntry restricted to the members a per-entry consumer (filter
// precompute) actually reads, at O(chosen members) per entry instead of
// O(all members²): DecodeGroupEntry re-derives each member's offset and
// the entry geometry per member, which dominates on a wide parent block.
type GroupEntryDecoder struct {
	s       *Schema
	g       int
	count   int
	width   int
	bmOff   int
	members []gMember // the chosen members; off is the offset in the entry
	slots   []int     // member slot of each chosen member (its null bit)
}

// NewGroupEntryDecoder compiles a decoder for the members of group g at
// the given LOGICAL field indices (each must be a member of g).
func (s *Schema) NewGroupEntryDecoder(g int, fields []int) (*GroupEntryDecoder, error) {
	if g < 0 || g >= len(s.Groups) {
		return nil, groupErr("group out of range", map[string]any{"group": g})
	}
	width, bmOff := s.groupEntryGeometry(g)
	d := &GroupEntryDecoder{s: s, g: g, width: width, bmOff: bmOff, count: s.GroupEntryCount(g)}
	for _, fi := range fields {
		off, found := 0, false
		for k, m := range s.Groups[g].Members {
			f := &s.Fields[m.Field]
			w := onWireWidth(f.Type)
			if m.Field == fi {
				d.members = append(d.members, gMember{fi: fi, off: off, w: w, nullable: f.Nullable, field: f})
				d.slots = append(d.slots, k)
				found = true
				break
			}
			off += w
		}
		if !found {
			return nil, groupErr("field is not a member of the group", map[string]any{"group": g, "field_index": fi})
		}
	}
	return d, nil
}

// Decode writes the chosen members of entry e into sink with exactly
// DecodeGroupEntry's side effects for those members.
func (d *GroupEntryDecoder) Decode(e int, sink IndexedReusableRecord) error {
	if e < 0 || e >= d.count {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"group entry out of range",
			map[string]any{"group": d.g, "entry": e})
	}
	typed, _ := sink.(TypedSetRecord)
	entry := d.s.Groups[d.g].Entries[e*d.width : (e+1)*d.width]
	for i := range d.members {
		m := &d.members[i]
		if d.bmOff >= 0 && m.nullable && BitmapIsNull(entry[d.bmOff:], d.slots[i]) {
			sink.SetNullFieldAt(m.fi)
			sink.SetNumericAt(m.fi, 0)
			continue
		}
		if err := writeFieldBytes(sink, typed, m.field, m.fi, entry[m.off:m.off+m.w]); err != nil {
			return err
		}
	}
	return nil
}

// groupedRowSizes returns the physical body size (indices + row fields),
// the narrowed bitmap size and whether any row field is nullable.
func (s *Schema) groupedRowSizes() (body, bm int, hasBM bool) {
	groupOf, _ := s.memberSets()
	rowFields := 0
	for g := range s.Groups {
		if s.Groups[g].Kind == GroupKindIndexed {
			body += GroupIndexWidth
		}
	}
	for i := range s.Fields {
		if groupOf[i] >= 0 {
			continue
		}
		rowFields++
		body += onWireWidth(s.Fields[i].Type)
		hasBM = hasBM || s.Fields[i].Nullable
	}
	if hasBM {
		bm = (rowFields + 7) / 8
	}
	return body, bm, hasBM
}

// ValidateGroups checks the group descriptors against the schema's
// fields. A schema with no groups is always valid. Rules:
//
//   - every group has a known kind and at least one member;
//   - members are in range, strictly ascending, and no field belongs to
//     two groups (groups are independent, never nested);
//   - Entries is a whole number of entries; an indexed group has at
//     most MaxGroupEntries entries, a constant group exactly one;
//   - the physical stride is positive (a cohort whose every field is
//     constant-elided would have a zero stride and an underivable
//     record count).
func (s *Schema) ValidateGroups() error {
	if err := s.validateGroupShape(); err != nil {
		return err
	}
	for g := range s.Groups {
		grp := &s.Groups[g]
		w := s.GroupEntryWidth(g)
		if len(grp.Entries)%w != 0 {
			return groupErr("group dictionary is not a whole number of entries", map[string]any{"group": g, "entry_width": w, "bytes": len(grp.Entries)})
		}
		n := uint64(len(grp.Entries) / w)
		switch grp.Kind {
		case GroupKindIndexed:
			if n > MaxGroupEntries {
				return groupErr("group dictionary exceeds the u32 index space", map[string]any{"group": g, "entry_count": n, "max_entries": MaxGroupEntries})
			}
		case GroupKindConstant:
			if n != 1 {
				return groupErr("a constant group holds exactly one entry", map[string]any{"group": g, "entry_count": n})
			}
		}
	}
	return nil
}

// validateGroupShape is ValidateGroups without the dictionary checks:
// kinds, members and the physical stride — everything an encoder can
// check before any entry exists.
func (s *Schema) validateGroupShape() error {
	if !s.HasGroups() {
		return nil
	}
	if len(s.Groups) > math.MaxUint16 {
		return groupErr("too many groups", map[string]any{"group_count": len(s.Groups)})
	}
	owner := make([]int, len(s.Fields))
	for i := range owner {
		owner[i] = -1
	}
	for g := range s.Groups {
		grp := &s.Groups[g]
		if grp.Kind != GroupKindIndexed && grp.Kind != GroupKindConstant {
			return groupErr("unknown group kind", map[string]any{"group": g, "kind": uint8(grp.Kind)})
		}
		if len(grp.Members) == 0 || len(grp.Members) > math.MaxUint16 {
			return groupErr("a group needs between 1 and 65535 members", map[string]any{"group": g, "member_count": len(grp.Members)})
		}
		prev := -1
		for _, m := range grp.Members {
			if m.Field < 0 || m.Field >= len(s.Fields) {
				return groupErr("group member names no field", map[string]any{"group": g, "field_index": m.Field, "field_count": len(s.Fields)})
			}
			if m.Field <= prev {
				return groupErr("group members must be in strictly ascending field order", map[string]any{"group": g, "field_index": m.Field})
			}
			if owner[m.Field] >= 0 {
				return groupErr(fmt.Sprintf("field %q is a member of two groups", s.Fields[m.Field].Name),
					map[string]any{"field": s.Fields[m.Field].Name, "groups": []int{owner[m.Field], g}})
			}
			owner[m.Field] = g
			prev = m.Field
		}
	}
	if s.RecordByteSize() <= 0 {
		return groupErr("grouped schema leaves a zero-byte record stride", map[string]any{"field_count": len(s.Fields)})
	}
	return nil
}

func groupErr(msg string, details map[string]any) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID, "parent group: "+msg, details)
}

// RefuseGroups returns a coded error when s declares parent groups and
// the caller's operation rewrites or reinterprets physical row bytes
// without understanding them yet (shard-archive assembly, set-field
// widening, categorical byte remap). It keeps such a path loud rather
// than silently misreading a grouped row by its logical offsets. nil for
// an ungrouped schema.
func RefuseGroups(s *Schema, operation string, code errors.Code) error {
	if s == nil || !s.HasGroups() {
		return nil
	}
	return errors.NewCodedErrorWithDetails(code,
		fmt.Sprintf("%s does not yet support cohorts with parent groups (format 0x02); export or re-import the cohort without groups first", operation),
		map[string]any{"operation": operation, "group_count": len(s.Groups)})
}

// String names the kind as reports print it: "indexed" or "constant".
func (k GroupKind) String() string {
	switch k {
	case GroupKindIndexed:
		return "indexed"
	case GroupKindConstant:
		return "constant"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// GroupSpecOf reconstructs the name-level declaration of group g from
// the descriptor, for display: Members in member (logical field) order,
// and Key only when the key is a strict subset of the members — the
// encoder marks every member key when none was declared, so an
// all-key group reads back as "no key", exactly as the declaration
// that produced it most likely did. The format carries no group name
// and no declaration ordinal, so the spec's Label numbers the group by
// its position in THIS file: a group dropped by the viability gate at
// import shifts later groups down by one relative to their declared
// names.
func (s *Schema) GroupSpecOf(g int) GroupSpec {
	grp := &s.Groups[g]
	sp := GroupSpec{Kind: grp.Kind}
	var key []string
	for _, m := range grp.Members {
		if m.Field < 0 || m.Field >= len(s.Fields) {
			continue
		}
		name := s.Fields[m.Field].Name
		sp.Members = append(sp.Members, name)
		if m.Key {
			key = append(key, name)
		}
	}
	if len(key) < len(sp.Members) {
		sp.Key = key
	}
	return sp
}
