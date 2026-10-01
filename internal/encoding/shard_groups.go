package encoding

import (
	"bytes"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Shard archives with parent groups (format 0x02 shards).
//
// Every shard of an archive is decoded with the archive's CANONICAL
// schema, so a grouped archive has ONE group layout — the same group
// count, kinds, members and key flags on every shard — and every shard's
// per-row group indices address the canonical group dictionaries. The
// canonical dictionary is a union built canonical-first, exactly like a
// categorical's: an arriving shard's entries are looked up in it (or
// appended after it), so every entry a stored shard references keeps its
// index and each stored shard's own group dictionary is a PREFIX of the
// canonical one. That is the invariant ValidateDictPrefixRule checks for
// groups at `pulse shard verify` time.
//
// The byte work happens on the LOGICAL (0x01) form of a shard: flatten,
// reconcile exactly as an ungrouped shard is reconciled (set-rung
// widening, dictionary union, categorical remap — all of which speak
// logical offsets), then re-encode the logical rows against the archive's
// group layout with a GroupEncoder seeded from the canonical
// dictionaries. The service layer owns that orchestration
// (service/shard_groups.go); the pieces here are layout-level.

// FlattenCohortBytes returns the ungrouped (0x01) twin of a single-file
// cohort: the logical schema's preamble followed by every logical row,
// in order. A cohort without groups is returned unchanged (the same
// slice). The logical rows are exactly what every decoder yields, so the
// twin decodes identically; for a cohort deduped from a 0x01 original
// it reproduces that original byte-for-byte.
//
// A payload ending mid-record is ENCODING_INVALID rather than a silently
// dropped tail (the walk is ForEachRecord's).
func FlattenCohortBytes(cohort []byte) ([]byte, *encoding.Schema, error) {
	src := bytes.NewReader(cohort)
	schema, _, err := ReadPreamble(src)
	if err != nil {
		return nil, nil, err
	}
	if !schema.HasGroups() {
		return cohort, schema, nil
	}
	lr, flat, err := NewLogicalStream(src, schema)
	if err != nil {
		return nil, nil, err
	}
	var out bytes.Buffer
	out.Grow(len(cohort))
	if err := WritePreamble(&out, flat); err != nil {
		return nil, nil, err
	}
	if _, err := ForEachRecord(lr, flat.RecordByteSize(), func(rec []byte) error {
		out.Write(rec)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return out.Bytes(), flat, nil
}

// sameGroupDescriptor reports (as a coded error) the first dimension in
// which group g of a and b differ: kind, member count, member field, or
// member key flag. Dictionaries are NOT compared — dictionary cohesion
// is the prefix rule's job.
func sameGroupDescriptor(a, b *encoding.Schema, g int) error {
	ga, gb := &a.Groups[g], &b.Groups[g]
	mismatch := func(dim string, ca, cb any) error {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			fmt.Sprintf("parent group %d %s differs: canonical=%v, incoming=%v", g, dim, ca, cb),
			map[string]any{"group": g, "dimension": dim, "canonical": ca, "incoming": cb})
	}
	if ga.Kind != gb.Kind {
		return mismatch("kind", ga.Kind.String(), gb.Kind.String())
	}
	if len(ga.Members) != len(gb.Members) {
		return mismatch("member_count", len(ga.Members), len(gb.Members))
	}
	for k := range ga.Members {
		if ga.Members[k].Field != gb.Members[k].Field {
			return mismatch("member", fieldNameAt(a, ga.Members[k].Field), fieldNameAt(b, gb.Members[k].Field))
		}
		if ga.Members[k].Key != gb.Members[k].Key {
			return mismatch("key", fmt.Sprintf("%s key=%v", fieldNameAt(a, ga.Members[k].Field), ga.Members[k].Key),
				fmt.Sprintf("%s key=%v", fieldNameAt(b, gb.Members[k].Field), gb.Members[k].Key))
		}
	}
	return nil
}

func fieldNameAt(s *encoding.Schema, fi int) string {
	if fi < 0 || fi >= len(s.Fields) {
		return fmt.Sprintf("#%d", fi)
	}
	return s.Fields[fi].Name
}

// ValidateGroupCohesion is the parent-group dimension of structural
// cohesion: canonical and incoming must declare the identical group
// layout — the same group count and, per group, the same kind, members
// and key flags. It is what makes the physical stride of every shard
// equal the canonical stride, so a 0x01 shard in a 0x02 archive (or the
// reverse, or two different groupings) is PULSE_SHARD_SCHEMA_MISMATCH.
// Group DICTIONARIES are not compared here; they follow the prefix rule
// (ValidateDictPrefixRule). ValidateStructuralCohesion calls it, so
// `pulse shard verify` refuses a genuinely mixed-layout archive.
func ValidateGroupCohesion(canonical, incoming *encoding.Schema) error {
	if len(canonical.Groups) != len(incoming.Groups) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			fmt.Sprintf("parent group count differs: canonical=%d, incoming=%d (a shard's group layout must equal the archive's)",
				len(canonical.Groups), len(incoming.Groups)),
			map[string]any{
				"dimension":             "group_count",
				"canonical_group_count": len(canonical.Groups),
				"incoming_group_count":  len(incoming.Groups),
			})
	}
	for g := range canonical.Groups {
		if err := sameGroupDescriptor(canonical, incoming, g); err != nil {
			return err
		}
	}
	return nil
}

// checkGroupPrefix applies the append-only prefix rule to group g's
// dictionary. It returns the extended entries when incoming extends
// canonical, nil when incoming is a prefix of (or equal to) canonical,
// and PULSE_SHARD_DICT_DIVERGENCE when neither is a prefix of the other.
// Structural (group) cohesion must already hold.
func checkGroupPrefix(canonical, incoming *encoding.Schema, g int) ([]byte, error) {
	ce, ne := canonical.Groups[g].Entries, incoming.Groups[g].Entries
	switch {
	case bytes.HasPrefix(ce, ne):
		return nil, nil
	case bytes.HasPrefix(ne, ce):
		if n := uint64(incoming.GroupEntryCount(g)); n > encoding.MaxGroupEntries {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
				fmt.Sprintf("%s dictionary (%d entries) exceeds the u32 index space (%d entries)",
					GroupSpecOf(canonical, g).Label(g), n, encoding.MaxGroupEntries),
				map[string]any{"group": g, "group_label": GroupSpecOf(canonical, g).Label(g),
					"capacity": encoding.MaxGroupEntries, "incoming_entries": n})
		}
		return ne, nil
	default:
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_DIVERGENCE,
			fmt.Sprintf("%s dictionaries are not prefix-related: the shard's group entries are not the archive's in the archive's order",
				GroupSpecOf(canonical, g).Label(g)),
			map[string]any{
				"group":             g,
				"group_label":       GroupSpecOf(canonical, g).Label(g),
				"canonical_entries": canonical.GroupEntryCount(g),
				"incoming_entries":  incoming.GroupEntryCount(g),
			})
	}
}

// GroupIndexHeadroom reports how much of one parent group's index space
// the canonical dictionary has consumed. It is the group counterpart of
// SetWidthHeadroom and exists for the same reason: a union past the u32
// index space is fatal (PULSE_SHARD_DICT_WIDTH_OVERFLOW) with nowhere to
// widen to, and a constant group that an arriving shard disagrees with
// is promoted to an indexed group by an ARCHIVE-WIDE rewrite — both are
// foreseeable from here rather than discovered by paying for them.
type GroupIndexHeadroom struct {
	Group int    `json:"group"`
	Label string `json:"label"`
	// Kind is "indexed" or "constant".
	Kind string `json:"kind"`
	// Entries is the canonical dictionary's entry count.
	Entries int `json:"entries"`
	// Capacity is the entry ceiling: the u32 index space for an indexed
	// group, exactly 1 for a constant group.
	Capacity uint64 `json:"capacity"`
	// Headroom is Capacity - Entries: how many more distinct tuples the
	// archive can absorb. A constant group always reports 0 — the next
	// distinct value promotes it to an indexed group across the archive.
	Headroom uint64 `json:"headroom"`
}

// GroupIndexHeadroomFor returns one GroupIndexHeadroom per parent group
// of s, in group order; nil for a schema without groups.
func GroupIndexHeadroomFor(s *encoding.Schema) []GroupIndexHeadroom {
	if s == nil || !s.HasGroups() {
		return nil
	}
	out := make([]GroupIndexHeadroom, 0, len(s.Groups))
	for g := range s.Groups {
		n := s.GroupEntryCount(g)
		capacity := encoding.MaxGroupEntries
		if s.Groups[g].Kind == encoding.GroupKindConstant {
			capacity = 1
		}
		var head uint64
		if uint64(n) < capacity {
			head = capacity - uint64(n)
		}
		out = append(out, GroupIndexHeadroom{
			Group:    g,
			Label:    GroupSpecOf(s, g).Label(g),
			Kind:     s.Groups[g].Kind.String(),
			Entries:  n,
			Capacity: capacity,
			Headroom: head,
		})
	}
	return out
}

// GroupSpecsOf returns the name-level declaration of every group of s,
// in group order — the specs a GroupEncoder needs to reproduce s's exact
// group layout (members, kinds, key flags) over s.Logical().
func GroupSpecsOf(s *encoding.Schema) []GroupSpec {
	if s == nil {
		return nil
	}
	out := make([]GroupSpec, len(s.Groups))
	for g := range s.Groups {
		out[g] = GroupSpecOf(s, g)
	}
	return out
}
