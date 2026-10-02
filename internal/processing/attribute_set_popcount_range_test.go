package processing

import (
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// popcountRangeSchema builds a one-field set schema of the given rung
// with a full-width dictionary.
func popcountRangeSchema(t *testing.T, ft encoding.FieldType) *encoding.Schema {
	t.Helper()
	d := encoding.NewDictionary()
	for i := range int(ft.MaxSetEntries()) {
		if _, err := d.Add("m" + strconv.Itoa(i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: ft, Nullable: true, Dictionary: d, Description: "Multi-select survey response tags"},
	}}
}

func popcountMaskOfFirstN(n int) encoding.SetMask {
	var m encoding.SetMask
	for i := range n {
		m = m.WithBit(i)
	}
	return m
}

// TestAttrSetPopcount_TopOfRangeSurvivesTheWholeChannel pins the wire
// side of the ATTR_SET_POPCOUNT emit-type question at the exact
// boundary the declaration got wrong.
//
// ATTR_SET_POPCOUNT declared EmitsType "u8" — correct while set_u64 was
// the top rung (popcount 0..64), wrong the moment set_u256 landed,
// because a fully-selected set_u256 has popcount 256 and u8 stops at
// 255. The question that decides whether that is a doc bug or a silent
// truncation is whether anything on the value path narrows to a byte.
//
// Nothing does: encoding.SetMask.PopCount returns int, the
// AttributeComputer contract is []float64, and EmitsType has no runtime
// consumer at all. So 255 and 256 both arrive intact and the fix is the
// declaration — pinned by
// descriptor.TestCapabilities_SetPopcountEmitsTypeHoldsWidestRung.
//
// 255 and 256 are both asserted deliberately: a u8 narrowing would pass
// at 255 and wrap to 0 at 256, so testing only the saturated mask could
// not tell a correct answer from a wrapped one.
func TestAttrSetPopcount_TopOfRangeSurvivesTheWholeChannel(t *testing.T) {
	cases := []struct {
		ft    encoding.FieldType
		bits  int
		label string
	}{
		{encoding.FieldTypeSetU8, 8, "set_u8 saturated"},
		{encoding.FieldTypeSetU64, 64, "set_u64 saturated"},
		{encoding.FieldTypeSetU128, 128, "set_u128 saturated"},
		{encoding.FieldTypeSetU256, 254, "set_u256 one below the u8 ceiling"},
		{encoding.FieldTypeSetU256, 255, "set_u256 at the u8 ceiling"},
		{encoding.FieldTypeSetU256, 256, "set_u256 saturated — one past the u8 ceiling"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			schema := popcountRangeSchema(t, tc.ft)
			c, err := newSetPopcountAttribute(
				&types.Attribute{Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "n_tags"}, schema)
			if err != nil {
				t.Fatalf("newSetPopcountAttribute: %v", err)
			}

			rec := NewRecordWithWide(schema, map[string]float64{}, nil,
				map[string]any{"tags": popcountMaskOfFirstN(tc.bits)})

			// The buffered channel: Compute returns []float64.
			got, err := c.Compute([]*Record{rec}, "tags")
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("Compute returned %d values, want 1", len(got))
			}
			if got[0] != float64(tc.bits) {
				t.Errorf("Compute popcount = %v, want %d — a value narrowed somewhere on the attribute channel",
					got[0], tc.bits)
			}

			// The streaming channel: RowLocalAttribute.Row.
			rl, ok := c.(RowLocalAttribute)
			if !ok {
				t.Fatalf("%T does not implement RowLocalAttribute", c)
			}
			row, err := rl.Row(rec, "tags")
			if err != nil {
				t.Fatalf("Row: %v", err)
			}
			if row != float64(tc.bits) {
				t.Errorf("Row popcount = %v, want %d", row, tc.bits)
			}
			if row != got[0] {
				t.Errorf("Row (%v) and Compute (%v) disagree", row, got[0])
			}
		})
	}
}
