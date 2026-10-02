package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Wide-set coverage for ATTR_SET_POPCOUNT and ATTR_SET_HAS. Helpers
// (makeWideSetSchema, makeWideSetRecord, setOpNullRecord,
// maskWithBits, setOpLabel, setOpBoundaryBits) come from filterer_set_wide_test.go.

func setAttrRowLocal(t *testing.T, c AttributeComputer) RowLocalAttribute {
	t.Helper()
	rl, ok := c.(RowLocalAttribute)
	if !ok {
		t.Fatalf("%T does not implement RowLocalAttribute", c)
	}
	return rl
}

func TestAttrSetPopcountWide_CountsAbove64Bits(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	c, err := newSetPopcountAttribute(&types.Attribute{Type: types.ATTR_SET_POPCOUNT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPopcountAttribute: %v", err)
	}
	rl := setAttrRowLocal(t, c)

	// One bit at a time across every word boundary.
	for _, bit := range setOpBoundaryBits {
		got, err := rl.Row(makeWideSetRecord(schema, maskWithBits(bit)), "tags")
		if err != nil {
			t.Fatalf("bit %d: %v", bit, err)
		}
		if got != 1 {
			t.Errorf("popcount of a mask holding only bit %d = %v, want 1", bit, got)
		}
	}

	// All eight boundary bits at once — one per word plus its neighbour.
	if got, err := rl.Row(makeWideSetRecord(schema, maskWithBits(setOpBoundaryBits...)), "tags"); err != nil || got != 8 {
		t.Errorf("popcount over the boundary bits = %v (err %v), want 8", got, err)
	}

	// A saturated 256-bit mask: the case a uint64 popcount reports as 64.
	var full encoding.SetMask
	for i := 0; i < 256; i++ {
		full = full.WithBit(i)
	}
	if got, err := rl.Row(makeWideSetRecord(schema, full), "tags"); err != nil || got != 256 {
		t.Errorf("popcount of a saturated set_u256 = %v (err %v), want 256", got, err)
	}

	// 206 members, the motivating SPSS width.
	var m206 encoding.SetMask
	for i := 0; i < 206; i++ {
		m206 = m206.WithBit(i)
	}
	if got, err := rl.Row(makeWideSetRecord(schema, m206), "tags"); err != nil || got != 206 {
		t.Errorf("popcount of a 206-member mask = %v (err %v), want 206", got, err)
	}

	// Empty mask is 0 and so is a null, but only the empty mask is a real
	// zero — both surface 0 through the float64 attribute channel.
	if got, _ := rl.Row(makeWideSetRecord(schema, encoding.SetMask{}), "tags"); got != 0 {
		t.Errorf("popcount of an empty mask = %v, want 0", got)
	}
	if got, err := rl.Row(setOpNullRecord(schema), "tags"); err != nil || got != 0 {
		t.Errorf("popcount of a null = %v (err %v), want 0", got, err)
	}
}

func TestAttrSetPopcountWide_ComputeMatchesRow(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	c, err := newSetPopcountAttribute(&types.Attribute{Type: types.ATTR_SET_POPCOUNT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPopcountAttribute: %v", err)
	}
	recs := []*Record{
		makeWideSetRecord(schema, encoding.SetMask{}),
		makeWideSetRecord(schema, maskWithBits(0)),
		makeWideSetRecord(schema, maskWithBits(64, 128, 255)),
		setOpNullRecord(schema),
	}
	got, err := c.Compute(recs, "tags")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	want := []float64{0, 1, 3, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Compute[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAttrSetHasWide_WordBoundaries(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	for _, bit := range setOpBoundaryBits {
		c, err := newSetHasAttribute(&types.Attribute{
			Type:   types.ATTR_SET_HAS,
			Field:  "tags",
			Params: []byte(`{"label":"` + setOpLabel(bit) + `"}`),
		}, schema)
		if err != nil {
			t.Fatalf("bit %d: newSetHasAttribute: %v", bit, err)
		}
		rl := setAttrRowLocal(t, c)

		if got, err := rl.Row(makeWideSetRecord(schema, maskWithBits(bit)), "tags"); err != nil || got != 1 {
			t.Errorf("label at bit %d against its own mask = %v (err %v), want 1", bit, got, err)
		}
		for _, other := range setOpBoundaryBits {
			if other == bit {
				continue
			}
			if got, _ := rl.Row(makeWideSetRecord(schema, maskWithBits(other)), "tags"); got != 0 {
				t.Errorf("label at bit %d reported present on a mask holding only bit %d", bit, other)
			}
		}
		if got, _ := rl.Row(makeWideSetRecord(schema, encoding.SetMask{}), "tags"); got != 0 {
			t.Errorf("label at bit %d reported present on an empty mask", bit)
		}
		if got, err := rl.Row(setOpNullRecord(schema), "tags"); err != nil || got != 0 {
			t.Errorf("label at bit %d on a null = %v (err %v), want 0", bit, got, err)
		}
	}
}

func TestAttrSetHasWide_BuildAcceptsLabelAboveBit63(t *testing.T) {
	for _, tc := range []struct {
		ft      encoding.FieldType
		entries int
		bit     int
	}{
		{encoding.FieldTypeSetU128, 128, 64},
		{encoding.FieldTypeSetU128, 128, 127},
		{encoding.FieldTypeSetU256, 256, 206},
		{encoding.FieldTypeSetU256, 256, 255},
	} {
		schema := makeWideSetSchema(t, tc.ft, tc.entries)
		if _, err := newSetHasAttribute(&types.Attribute{
			Type:   types.ATTR_SET_HAS,
			Field:  "tags",
			Params: []byte(`{"label":"` + setOpLabel(tc.bit) + `"}`),
		}, schema); err != nil {
			t.Errorf("%s bit %d: ATTR_SET_HAS rejected a valid label: %v", tc.ft, tc.bit, err)
		}
	}
}

func TestAttrSetHasWide_RejectsLabelBeyondRungCapacity(t *testing.T) {
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU8, 16)
	if _, err := newSetHasAttribute(&types.Attribute{
		Type:   types.ATTR_SET_HAS,
		Field:  "tags",
		Params: []byte(`{"label":"` + setOpLabel(10) + `"}`),
	}, schema); err == nil {
		t.Fatal("expected a config error for a label beyond the rung's capacity, got nil")
	}
	if _, err := newSetHasAttribute(&types.Attribute{
		Type:   types.ATTR_SET_HAS,
		Field:  "tags",
		Params: []byte(`{"label":"` + setOpLabel(7) + `"}`),
	}, schema); err != nil {
		t.Fatalf("label at bit 7 of a set_u8 rejected: %v", err)
	}
}

// Constructed without a schema (the registry-probe path) ATTR_SET_HAS
// cannot know which bit the label maps to, so it must report absence
// rather than silently testing dictionary entry 0.
func TestAttrSetHasWide_NoSchemaReportsAbsent(t *testing.T) {
	c, err := newSetHasAttribute(&types.Attribute{
		Type:   types.ATTR_SET_HAS,
		Field:  "tags",
		Params: []byte(`{"label":"L000"}`),
	}, nil)
	if err != nil {
		t.Fatalf("newSetHasAttribute(nil schema): %v", err)
	}
	schema := makeWideSetSchema(t, encoding.FieldTypeSetU256, 256)
	rl := setAttrRowLocal(t, c)
	for _, bit := range []int{0, 1, 64, 255} {
		if got, err := rl.Row(makeWideSetRecord(schema, maskWithBits(bit)), "tags"); err != nil || got != 0 {
			t.Errorf("unresolved label vs mask bit %d = %v (err %v), want 0", bit, got, err)
		}
	}
}
