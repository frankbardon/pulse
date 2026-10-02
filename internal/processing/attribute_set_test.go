package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestAttrSetPopcount_RowDerivesCount(t *testing.T) {
	schema := makeSetTestSchema(t)
	c, err := newSetPopcountAttribute(&types.Attribute{Type: types.ATTR_SET_POPCOUNT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPopcountAttribute: %v", err)
	}
	rl := c.(RowLocalAttribute)
	cases := []struct {
		mask uint64
		want float64
	}{
		{0b0000, 0},
		{0b0001, 1},
		{0b0011, 2},
		{0b1111, 4},
	}
	for _, ca := range cases {
		got, err := rl.Row(makeSetRecord(schema, ca.mask), "tags")
		if err != nil {
			t.Fatalf("Row: %v", err)
		}
		if got != ca.want {
			t.Errorf("mask %b: got %v, want %v", ca.mask, got, ca.want)
		}
	}
}

func TestAttrSetHas_LabelBitTest(t *testing.T) {
	schema := makeSetTestSchema(t)
	c, err := newSetHasAttribute(&types.Attribute{
		Type:   types.ATTR_SET_HAS,
		Field:  "tags",
		Params: []byte(`{"label":"AMEX"}`),
	}, schema)
	if err != nil {
		t.Fatalf("newSetHasAttribute: %v", err)
	}
	rl := c.(RowLocalAttribute)
	cases := []struct {
		mask uint64
		want float64
	}{
		{0b0000, 0},
		{0b0001, 0}, // VISA only
		{0b0100, 1}, // AMEX
		{0b0111, 1}, // includes AMEX
	}
	for _, ca := range cases {
		got, _ := rl.Row(makeSetRecord(schema, ca.mask), "tags")
		if got != ca.want {
			t.Errorf("mask %b: got %v, want %v", ca.mask, got, ca.want)
		}
	}
}

func TestAttrSetHas_UnknownLabelRejected(t *testing.T) {
	schema := makeSetTestSchema(t)
	_, err := newSetHasAttribute(&types.Attribute{
		Type:   types.ATTR_SET_HAS,
		Field:  "tags",
		Params: []byte(`{"label":"NOPE"}`),
	}, schema)
	if err == nil {
		t.Fatal("expected error for unknown label, got nil")
	}
}
