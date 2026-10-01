package encoding

import (
	"bytes"
	"testing"
)

// TestGroupDescriptorBytes: the viability gate's per-group descriptor
// cost is exactly what writeGroupsSection emits beyond the entries and
// the section's u16 group_count, for any member count.
func TestGroupDescriptorBytes(t *testing.T) {
	s := &Schema{}
	for i := 0; i < 9; i++ {
		s.Fields = append(s.Fields, Field{Name: string(rune('a' + i)), Type: FieldTypeU16})
	}
	for members := 1; members <= 9; members++ {
		g := Group{Kind: GroupKindIndexed}
		for fi := 0; fi < members; fi++ {
			g.Members = append(g.Members, GroupMember{Field: fi})
		}
		g.Entries = make([]byte, 3*2*members) // three entries
		gs := &Schema{Fields: s.Fields, Groups: []Group{g}}
		var b bytes.Buffer
		writeGroupsSection(&b, gs)
		if got := b.Len() - 2 - len(g.Entries); got != groupDescriptorBytes(members) {
			t.Fatalf("%d members: section spends %d descriptor bytes, groupDescriptorBytes says %d", members, got, groupDescriptorBytes(members))
		}
	}
}
