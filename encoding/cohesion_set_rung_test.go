package encoding

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// setRungSchema builds a two-field schema (id u32, opts <ft>) whose set
// column carries the named dictionary entries. Byte offsets are the
// ones the field widths imply, which is what ValidateStructuralCohesion
// compares.
func setRungSchema(t *testing.T, ft FieldType, values ...string) *Schema {
	t.Helper()
	d := NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add %q: %v", v, err)
		}
	}
	return &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32, ByteOffset: 0},
		{Name: "opts", Type: ft, ByteOffset: 4, Dictionary: d},
	}}
}

// A shard arriving at a WIDER set rung than canonical is the case a new
// month's SPSS import actually produces: the source inferred set_u128
// against an archive that was seeded at set_u64. It must plan a widen to
// the incoming rung rather than being refused — the plan is what lets
// AddShard promote the archive instead of the caller re-importing every
// prior month to reach a layout a mechanical re-stride already produces.
func TestPlanSetWidening_IncomingWiderRungPlansArchiveWiden(t *testing.T) {
	canonical := setRungSchema(t, FieldTypeSetU8, "tv", "radio")
	incoming := setRungSchema(t, FieldTypeSetU64, "tv", "radio")

	plans, err := PlanSetWidening(canonical, incoming)
	if err != nil {
		t.Fatalf("a wider incoming rung must plan a widen, not error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want exactly one", plans)
	}
	p := plans[0]
	if p.Field != "opts" || p.From != FieldTypeSetU8 || p.IncomingFrom != FieldTypeSetU64 || p.To != FieldTypeSetU64 {
		t.Fatalf("plan = %+v, want opts canonical set_u8 / incoming set_u64 -> set_u64", p)
	}
}

// The reverse direction must NEVER narrow the archive — narrowing drops
// every selection above the target's ceiling. The incoming shard is
// promoted to canonical's rung instead, so the plan's target is
// canonical's own rung and only the incoming side moves.
func TestPlanSetWidening_IncomingNarrowerRungPromotesIncomingOnly(t *testing.T) {
	canonical := setRungSchema(t, FieldTypeSetU64, "tv", "radio")
	incoming := setRungSchema(t, FieldTypeSetU8, "tv", "radio")

	plans, err := PlanSetWidening(canonical, incoming)
	if err != nil {
		t.Fatalf("a narrower incoming rung must plan a promotion, not error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want exactly one", plans)
	}
	p := plans[0]
	if p.To != FieldTypeSetU64 {
		t.Fatalf("plan target = %v, want set_u64 (canonical's rung); narrowing would drop selections", p.To)
	}
	if p.From != FieldTypeSetU64 {
		t.Errorf("plan From = %v, want set_u64 — the archive does not move here", p.From)
	}
	if p.IncomingFrom != FieldTypeSetU8 {
		t.Errorf("plan IncomingFrom = %v, want set_u8", p.IncomingFrom)
	}
}

// Rung divergence and a dictionary union that outgrows BOTH rungs
// compose: the target is the narrowest rung holding the union, which may
// be wider than either side declared.
func TestPlanSetWidening_RungDivergencePlusUnionOverflowTakesTheWiderTarget(t *testing.T) {
	canonicalVals := []string{"a", "b", "c", "d", "e"}
	incomingVals := make([]string, 0, 62)
	for i := 0; i < 62; i++ {
		incomingVals = append(incomingVals, string(rune('A'+i%26))+string(rune('0'+i/26)))
	}
	canonical := setRungSchema(t, FieldTypeSetU8, canonicalVals...)
	incoming := setRungSchema(t, FieldTypeSetU64, incomingVals...)

	plans, err := PlanSetWidening(canonical, incoming)
	if err != nil {
		t.Fatalf("PlanSetWidening: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want exactly one", plans)
	}
	// 5 + 62 = 67 distinct members: past set_u64's 64-bit ceiling even
	// though the incoming shard's own rung is set_u64.
	if plans[0].UnionEntries != 67 {
		t.Fatalf("UnionEntries = %d, want 67", plans[0].UnionEntries)
	}
	if plans[0].To != FieldTypeSetU128 {
		t.Fatalf("plan target = %v, want set_u128", plans[0].To)
	}
}

// Identical rungs with a union that fits stay plan-free: the ordinary
// append must not be turned into a rewrite.
func TestPlanSetWidening_MatchingRungsWithFittingUnionPlanNothing(t *testing.T) {
	canonical := setRungSchema(t, FieldTypeSetU64, "tv", "radio")
	incoming := setRungSchema(t, FieldTypeSetU64, "radio", "print")

	plans, err := PlanSetWidening(canonical, incoming)
	if err != nil {
		t.Fatalf("PlanSetWidening: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("plans = %+v, want none", plans)
	}
}

// Only the set-rung dimension relaxes. A set column meeting a non-set
// column of the same name is still a hard structural mismatch.
func TestPlanSetWidening_SetMeetingNonSetStaysFatal(t *testing.T) {
	canonical := setRungSchema(t, FieldTypeSetU8, "tv")
	incoming := setRungSchema(t, FieldTypeSetU8, "tv")
	incoming.Fields[1].Type = FieldTypeU64
	incoming.Fields[1].Dictionary = nil

	_, err := PlanSetWidening(canonical, incoming)
	if !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
		t.Fatalf("error = %v, want PULSE_SHARD_SCHEMA_MISMATCH", err)
	}
}

// Above set_u256 there is nowhere to widen to, so the overflow stays
// fatal even when the rungs diverge.
func TestPlanSetWidening_UnionAboveWidestRungStaysFatal(t *testing.T) {
	canonicalVals := make([]string, 200)
	for i := range canonicalVals {
		canonicalVals[i] = "c" + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}
	incomingVals := make([]string, 100)
	for i := range incomingVals {
		incomingVals[i] = "i" + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}
	canonical := setRungSchema(t, FieldTypeSetU256, canonicalVals...)
	incoming := setRungSchema(t, FieldTypeSetU64, incomingVals...)

	_, err := PlanSetWidening(canonical, incoming)
	if !errors.HasCode(err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
		t.Fatalf("error = %v, want PULSE_SHARD_DICT_WIDTH_OVERFLOW", err)
	}
}
