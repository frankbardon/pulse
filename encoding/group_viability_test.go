package encoding

import (
	"bytes"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
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

// TestDedupGate_Floor: an unset, negative or non-finite floor is the
// default; any positive finite value is honoured as given.
func TestDedupGate_Floor(t *testing.T) {
	for in, want := range map[float64]float64{0: 2, -1: 2, math.Inf(1): 2, 1: 1, 1.5: 1.5, 3: 3} {
		if got := (DedupGate{RatioFloor: in}).Floor(); got != want {
			t.Fatalf("Floor(%v) = %v, want %v", in, got, want)
		}
	}
	if got := (DedupGate{RatioFloor: math.NaN()}).Floor(); got != DefaultDedupRatioFloor {
		t.Fatalf("Floor(NaN) = %v", got)
	}
}

// TestDedupGate_ScreenWidths: the width floor is inclusive (members
// exactly as wide as the index can never save), constant specs pass
// unjudged, surviving specs keep their declared ordinal, and strict
// turns the first narrow group into an error.
func TestDedupGate_ScreenWidths(t *testing.T) {
	flat := &Schema{Fields: []Field{
		{Name: "a", Type: FieldTypeU16}, {Name: "b", Type: FieldTypeU16}, // a+b = 4: narrow
		{Name: "c", Type: FieldTypeU32}, {Name: "d", Type: FieldTypeU8, Nullable: true}, // 5: wide enough
		{Name: "k", Type: FieldTypeU8}, // constant member
	}}
	specs := []GroupSpec{
		{Kind: GroupKindIndexed, Members: []string{"a", "b"}},
		{Kind: GroupKindIndexed, Members: []string{"c", "d"}, Key: []string{"c"}},
		{Kind: GroupKindConstant, Members: []string{"k"}},
	}
	admitted, views, warns, err := DedupGate{}.ScreenWidths(flat, specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(admitted) != 2 || admitted[0].Label(0) != "group 2 [key: c]" || admitted[1].Kind != GroupKindConstant {
		t.Fatalf("admitted %+v", admitted)
	}
	if views[0].Verdict != GroupVerdictDroppedTooNarrow || views[0].MemberRowBytes != 4 || views[0].IndexWidth != 4 {
		t.Fatalf("narrow view %+v", views[0])
	}
	if v := views[1]; v.Verdict != GroupVerdictAdmitted || v.MemberRowBytes != 5 || v.EntryWidth != 6 || v.BreakEvenRatio != 6 {
		t.Fatalf("wide view %+v (nullable member adds a bitmap byte to the entry)", v)
	}
	if v := views[2]; v.Verdict != GroupVerdictAdmitted || v.IndexWidth != 0 || v.BreakEvenRatio != 0 {
		t.Fatalf("constant view %+v", v)
	}
	if len(warns) != 1 || warns[0].Code != errors.PULSE_GROUP_TOO_NARROW || warns[0].Details["group_label"] != "group 1 [a,b]" {
		t.Fatalf("warnings %v", warns)
	}
	if _, _, _, err := (DedupGate{Strict: true}).ScreenWidths(flat, specs); !errors.HasCode(err, errors.PULSE_GROUP_TOO_NARROW) {
		t.Fatalf("strict err = %v", err)
	}
}
