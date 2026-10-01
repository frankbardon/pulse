package encoding

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

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
	flat := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU16}, {Name: "b", Type: encoding.FieldTypeU16}, // a+b = 4: narrow
		{Name: "c", Type: encoding.FieldTypeU32}, {Name: "d", Type: encoding.FieldTypeU8, Nullable: true}, // 5: wide enough
		{Name: "k", Type: encoding.FieldTypeU8}, // constant member
	}}
	specs := []GroupSpec{
		{Kind: encoding.GroupKindIndexed, Members: []string{"a", "b"}},
		{Kind: encoding.GroupKindIndexed, Members: []string{"c", "d"}, Key: []string{"c"}},
		{Kind: encoding.GroupKindConstant, Members: []string{"k"}},
	}
	admitted, views, warns, err := DedupGate{}.ScreenWidths(flat, specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(admitted) != 2 || admitted[0].Label(0) != "group 2 [key: c]" || admitted[1].Kind != encoding.GroupKindConstant {
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
