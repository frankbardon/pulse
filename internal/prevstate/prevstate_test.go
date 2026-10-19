package prevstate

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func keySchemaFixture(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, s := range []string{"free", "paid"} {
		if _, err := dict.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64},
		{Name: "seen", Type: encoding.FieldTypeDate},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Description: "plan"},
		{Name: "active", Type: encoding.FieldTypePackedBool},
		{Name: "n", Type: encoding.FieldTypeU8},
	}}
}

func prevAttr(field, label string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_PREVIOUS_STATE, Field: field, SubjectField: "id", OrderField: "seen", Label: label}
}

// TestKeySchema_NoSlotIsTheSameSchema: a request without a contributing
// ATTR_PREVIOUS_STATE slot gets the cohort schema itself back — the
// same pointer — so every consumer reading the view is byte-identical.
func TestKeySchema_NoSlotIsTheSameSchema(t *testing.T) {
	s := keySchemaFixture(t)
	for name, attrs := range map[string][]*types.Attribute{
		"none":            nil,
		"other attribute": {{Type: types.ATTR_ZSCORE, Field: "n", Label: "z"}},
		"nil slot":        {nil},
		"unknown state":   {prevAttr("nope", "p")},
		"coded state":     {prevAttr("n", "p")},
		"label collides":  {prevAttr("plan", "n")},
	} {
		if got := KeySchema(attrs, s); got != s {
			t.Errorf("%s: KeySchema returned a new schema %+v", name, got.Fields)
		}
	}
	if KeySchema([]*types.Attribute{prevAttr("plan", "p")}, nil) != nil {
		t.Error("nil schema: want nil")
	}
}

// TestKeySchema_InheritsTypeAndDictionary: each output column is a
// nullable virtual field of its state field's type and dictionary
// (packed_bool keeps its type), appended after the cohort's fields
// (every index stays put); the default label is TYPE_field; the cohort
// schema is never mutated.
func TestKeySchema_InheritsTypeAndDictionary(t *testing.T) {
	s := keySchemaFixture(t)
	n := len(s.Fields)
	view := KeySchema([]*types.Attribute{prevAttr("plan", "prev"), prevAttr("active", ""), prevAttr("plan", "prev")}, s)
	if view == s || len(s.Fields) != n {
		t.Fatalf("cohort schema mutated or reused: %d fields", len(s.Fields))
	}
	if len(view.Fields) != n+2 {
		t.Fatalf("view fields %+v, want %d (duplicate label once)", view.Fields, n+2)
	}
	for i := range s.Fields {
		if view.Fields[i].Name != s.Fields[i].Name {
			t.Fatalf("index %d moved: %s", i, view.Fields[i].Name)
		}
	}
	prev := view.Field("prev")
	if prev == nil || prev.Type != encoding.FieldTypeCategoricalU8 || !prev.Nullable || prev.Dictionary != s.Field("plan").Dictionary {
		t.Fatalf("prev %+v, want a nullable categorical_u8 on plan's dictionary", prev)
	}
	if d, ok := view.Categorical("prev"); !ok || d.Resolve(1) != "paid" {
		t.Fatalf("prev dictionary does not resolve index 1 to paid")
	}
	b := view.Field(string(types.ATTR_PREVIOUS_STATE) + "_active")
	if b == nil || b.Type != encoding.FieldTypePackedBool || !b.Nullable {
		t.Fatalf("default-labelled packed_bool output %+v", b)
	}
}

// TestBeforeFilters_OnlyPreviousState: the before-filters property is
// ATTR_PREVIOUS_STATE's alone, across every built-in attribute type.
func TestBeforeFilters_OnlyPreviousState(t *testing.T) {
	for _, at := range types.AllAttributeTypes() {
		if got, want := BeforeFilters(at), at == types.ATTR_PREVIOUS_STATE; got != want {
			t.Errorf("BeforeFilters(%s) = %v, want %v", at, got, want)
		}
	}
}

// TestBeforeFiltersLabels: the effective output labels of the
// before-filters slots in declared order (the default label when the
// slot names none), routed — a hidden type contributes nothing — and
// nil when no slot is.
func TestBeforeFiltersLabels(t *testing.T) {
	attrs := []*types.Attribute{
		{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", Label: "b"},
		nil,
		{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
		{Type: types.ATTR_PREVIOUS_STATE, Field: "plan"},
	}
	if got, want := BeforeFiltersLabels(attrs, nil), []string{"b", "ATTR_PREVIOUS_STATE_plan"}; !slices.Equal(got, want) {
		t.Errorf("labels %v, want %v", got, want)
	}
	hide := func(types.AttributeType) types.AttributeType { return "" }
	if got := BeforeFiltersLabels(attrs, hide); got != nil {
		t.Errorf("hidden route: %v, want nil", got)
	}
	if got := BeforeFiltersLabels(attrs[2:3], nil); got != nil {
		t.Errorf("no before-filters slot: %v, want nil", got)
	}
}
