package encoding

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// setSchema builds a two-field schema — one u32 id, one set field with
// the supplied dictionary — with byte offsets derived from the types.
func setSchema(t *testing.T, ft FieldType, values []string) *Schema {
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

func seq(t *testing.T, prefix string, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, prefix+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	return out
}

func TestSetTypeFor_Ladder(t *testing.T) {
	cases := []struct {
		elements int
		want     FieldType
		ok       bool
	}{
		{0, 0, false},
		{-1, 0, false},
		{1, FieldTypeSetU8, true},
		{8, FieldTypeSetU8, true},
		{9, FieldTypeSetU16, true},
		{64, FieldTypeSetU64, true},
		{65, FieldTypeSetU128, true},
		{128, FieldTypeSetU128, true},
		{129, FieldTypeSetU256, true},
		{256, FieldTypeSetU256, true},
		{257, 0, false},
	}
	for _, c := range cases {
		got, ok := SetTypeFor(c.elements)
		if ok != c.ok || got != c.want {
			t.Errorf("SetTypeFor(%d) = (%v, %v), want (%v, %v)",
				c.elements, got, ok, c.want, c.ok)
		}
	}
	if WidestSetType() != FieldTypeSetU256 {
		t.Errorf("WidestSetType() = %v, want set_u256", WidestSetType())
	}
}

// A widen must recompute every downstream byte offset. The schema block
// persists ByteOffset verbatim, so a stale offset here describes a
// layout the records do not have and decodes as plausible garbage.
func TestWidenSchemaSetField_RecomputesOffsets(t *testing.T) {
	src := &Schema{Fields: []Field{
		{Name: "opts", Type: FieldTypeSetU8, ByteOffset: 0},
		{Name: "id", Type: FieldTypeU32, ByteOffset: 1},
		{Name: "score", Type: FieldTypeF64, ByteOffset: 5},
	}}
	out, err := WidenSchemaSetField(src, "opts", FieldTypeSetU16)
	if err != nil {
		t.Fatalf("WidenSchemaSetField: %v", err)
	}
	if out.Fields[0].Type != FieldTypeSetU16 {
		t.Errorf("widened type = %v, want set_u16", out.Fields[0].Type)
	}
	wantOffsets := []int{0, 2, 6}
	for i, want := range wantOffsets {
		if out.Fields[i].ByteOffset != want {
			t.Errorf("field %d offset = %d, want %d", i, out.Fields[i].ByteOffset, want)
		}
	}
	if out.RecordByteSize() != 14 {
		t.Errorf("widened stride = %d, want 14", out.RecordByteSize())
	}
	// The input must be untouched.
	if src.Fields[0].Type != FieldTypeSetU8 || src.Fields[1].ByteOffset != 1 {
		t.Error("WidenSchemaSetField mutated its input schema")
	}
}

func TestWidenSchemaSetField_Rejections(t *testing.T) {
	src := setSchema(t, FieldTypeSetU8, []string{"a"})
	if _, err := WidenSchemaSetField(src, "missing", FieldTypeSetU16); err == nil {
		t.Error("widen of an unknown field: expected an error")
	}
	if _, err := WidenSchemaSetField(src, "id", FieldTypeSetU16); err == nil {
		t.Error("widen of a non-set field: expected an error")
	}
	if _, err := WidenSchemaSetField(src, "opts", FieldTypeSetU8); err == nil {
		t.Error("widen to the same rung: expected an error")
	}
	if _, err := WidenSchemaSetField(nil, "opts", FieldTypeSetU16); err == nil {
		t.Error("widen of a nil schema: expected an error")
	}
}

func TestPlanSetWidening(t *testing.T) {
	t.Run("union fits: no plan", func(t *testing.T) {
		canonical := setSchema(t, FieldTypeSetU8, []string{"a", "b"})
		incoming := setSchema(t, FieldTypeSetU8, []string{"b", "c"})
		plans, err := PlanSetWidening(canonical, incoming)
		if err != nil {
			t.Fatalf("PlanSetWidening: %v", err)
		}
		if len(plans) != 0 {
			t.Fatalf("plans = %+v, want none (union of 3 fits set_u8)", plans)
		}
	})

	t.Run("union exactly filling the rung does not widen", func(t *testing.T) {
		// The boundary is the whole question: set_u8 addresses bits 0..7,
		// so a union of exactly 8 fits and widening it would re-stride an
		// archive for nothing.
		canonical := setSchema(t, FieldTypeSetU8, []string{"a", "b", "c", "d"})
		incoming := setSchema(t, FieldTypeSetU8, []string{"w", "x", "y", "z"})
		plans, err := PlanSetWidening(canonical, incoming)
		if err != nil {
			t.Fatalf("PlanSetWidening: %v", err)
		}
		if len(plans) != 0 {
			t.Fatalf("plans = %+v, want none (a union of exactly 8 fills set_u8 exactly)", plans)
		}
	})

	t.Run("union overflows the rung: promote to the narrowest that fits", func(t *testing.T) {
		canonical := setSchema(t, FieldTypeSetU8, []string{"a", "b", "c", "d", "e"})
		incoming := setSchema(t, FieldTypeSetU8, []string{"v", "w", "x", "y"})
		plans, err := PlanSetWidening(canonical, incoming)
		if err != nil {
			t.Fatalf("PlanSetWidening: %v", err)
		}
		if len(plans) != 1 {
			t.Fatalf("plans = %+v, want exactly one", plans)
		}
		p := plans[0]
		if p.Field != "opts" || p.FieldIndex != 1 {
			t.Errorf("plan identity = %q@%d, want opts@1", p.Field, p.FieldIndex)
		}
		if p.From != FieldTypeSetU8 || p.To != FieldTypeSetU16 {
			t.Errorf("plan rungs = %v -> %v, want set_u8 -> set_u16", p.From, p.To)
		}
		if p.UnionEntries != 9 {
			t.Errorf("UnionEntries = %d, want 9", p.UnionEntries)
		}
	})

	t.Run("union above the widest rung stays fatal", func(t *testing.T) {
		canonical := setSchema(t, FieldTypeSetU256, seq(t, "c", 200))
		incoming := setSchema(t, FieldTypeSetU256, seq(t, "i", 100))
		_, err := PlanSetWidening(canonical, incoming)
		if err == nil {
			t.Fatal("union of 300 entries: expected PULSE_SHARD_DICT_WIDTH_OVERFLOW")
		}
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW {
			t.Fatalf("error = %v, want PULSE_SHARD_DICT_WIDTH_OVERFLOW", err)
		}
	})

	t.Run("union exactly at the ceiling is accepted", func(t *testing.T) {
		canonical := setSchema(t, FieldTypeSetU128, seq(t, "c", 128))
		incoming := setSchema(t, FieldTypeSetU128, seq(t, "i", 128))
		plans, err := PlanSetWidening(canonical, incoming)
		if err != nil {
			t.Fatalf("PlanSetWidening at 256: %v", err)
		}
		if len(plans) != 1 || plans[0].To != FieldTypeSetU256 {
			t.Fatalf("plans = %+v, want one promotion to set_u256", plans)
		}
	})

	t.Run("mismatched schemas refuse", func(t *testing.T) {
		if _, err := PlanSetWidening(nil, setSchema(t, FieldTypeSetU8, nil)); err == nil {
			t.Error("nil canonical: expected an error")
		}
		canonical := setSchema(t, FieldTypeSetU8, []string{"a"})
		incoming := setSchema(t, FieldTypeSetU16, []string{"a"})
		if _, err := PlanSetWidening(canonical, incoming); err == nil {
			t.Error("divergent set rungs: expected PULSE_SHARD_SCHEMA_MISMATCH")
		}
	})
}

func TestSetWidthHeadroomFor(t *testing.T) {
	s := &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32},
		{Name: "opts", Type: FieldTypeSetU8, Dictionary: mustDict(t, "a", "b", "c")},
		{Name: "wide", Type: FieldTypeSetU256, Dictionary: mustDict(t, "z")},
	}}
	got := SetWidthHeadroomFor(s)
	if len(got) != 2 {
		t.Fatalf("headroom entries = %d, want 2 (set fields only)", len(got))
	}
	if got[0].Field != "opts" || got[0].Used != 3 || got[0].Capacity != 8 || got[0].Headroom != 5 {
		t.Errorf("opts headroom = %+v, want used=3 capacity=8 headroom=5", got[0])
	}
	if got[0].NextType != "set_u16" {
		t.Errorf("opts next_type = %q, want set_u16", got[0].NextType)
	}
	// The widest rung has nowhere to go; an empty NextType is the signal
	// that an overflow there is fatal rather than widenable.
	if got[1].NextType != "" {
		t.Errorf("set_u256 next_type = %q, want empty", got[1].NextType)
	}
	if got[1].Headroom != 255 {
		t.Errorf("set_u256 headroom = %d, want 255", got[1].Headroom)
	}
	if SetWidthHeadroomFor(nil) != nil {
		t.Error("SetWidthHeadroomFor(nil) must be nil")
	}
}

func mustDict(t *testing.T, values ...string) *Dictionary {
	t.Helper()
	d := NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	return d
}

// asCoded is a local errors.As for the typed CodedError, avoiding a
// stdlib errors import alongside the project's own errors package.
func asCoded(err error, target **errors.CodedError) bool {
	for cur := err; cur != nil; {
		if ce, ok := cur.(*errors.CodedError); ok {
			*target = ce
			return true
		}
		u, ok := cur.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}
