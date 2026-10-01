package encoding

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// DecodeGroupEntry writes entry e of group g into sink at the members'
// LOGICAL field indices, with exactly the side effects a full row decode
// has for those fields (value + typed wide value; a null member gets
// SetNullFieldAt + SetNumericAt(0)). It does not call ClearForRow and
// touches no non-member field. It is the per-entry evaluation hook: a
// predicate over member fields can be evaluated once per entry through a
// record built over this schema.
func DecodeGroupEntry(s *encoding.Schema, g, e int, sink IndexedReusableRecord) error {
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
	s       *encoding.Schema
	g       int
	count   int
	width   int
	bmOff   int
	members []gMember // the chosen members; off is the offset in the entry
	slots   []int     // member slot of each chosen member (its null bit)
}

// NewGroupEntryDecoder compiles a decoder for the members of group g at
// the given LOGICAL field indices (each must be a member of g).
func NewGroupEntryDecoder(s *encoding.Schema, g int, fields []int) (*GroupEntryDecoder, error) {
	if g < 0 || g >= len(s.Groups) {
		return nil, groupErr("group out of range", map[string]any{"group": g})
	}
	width, bmOff := groupEntryGeometry(s, g)
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
		if d.bmOff >= 0 && m.nullable && encoding.BitmapIsNull(entry[d.bmOff:], d.slots[i]) {
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

// RefuseGroups returns a coded error when s declares parent groups and
// the caller's operation rewrites or reinterprets physical row bytes
// without understanding them yet (shard-archive assembly, set-field
// widening, categorical byte remap). It keeps such a path loud rather
// than silently misreading a grouped row by its logical offsets. nil for
// an ungrouped schema.
func RefuseGroups(s *encoding.Schema, operation string, code errors.Code) error {
	if s == nil || !s.HasGroups() {
		return nil
	}
	return errors.NewCodedErrorWithDetails(code,
		fmt.Sprintf("%s does not yet support cohorts with parent groups (format 0x02); export or re-import the cohort without groups first", operation),
		map[string]any{"operation": operation, "group_count": len(s.Groups)})
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
func GroupSpecOf(s *encoding.Schema, g int) GroupSpec {
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
