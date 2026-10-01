package processing

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func registryWithLabelTables(tables map[string]LabelTable) *ExtensionRegistry {
	return &ExtensionRegistry{LabelTables: tables}
}

func TestBuildLabelResolver_NilBindings(t *testing.T) {
	r, err := BuildLabelResolver(nil, nil)
	if err != nil {
		t.Fatalf("nil bindings should not error: %v", err)
	}
	if r != nil {
		t.Fatalf("expected nil resolver for nil bindings")
	}
}

func TestBuildLabelResolver_UnknownTable(t *testing.T) {
	_, err := BuildLabelResolver(
		[]*types.LabelBinding{{Field: "country", Table: "missing"}},
		registryWithLabelTables(map[string]LabelTable{}),
	)
	if err == nil {
		t.Fatal("expected PULSE_LABEL_TABLE_UNKNOWN")
	}
}

func TestLabelResolver_Replace_Hit(t *testing.T) {
	r, err := BuildLabelResolver(
		[]*types.LabelBinding{{Field: "country", Table: "names", Mode: types.LabelModeReplace}},
		registryWithLabelTables(map[string]LabelTable{
			"names": {Rows: map[string]string{"US": "United States"}},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	out, sibling, ok := r.Apply("country", "US")
	if !ok || out != "United States" || sibling != "" {
		t.Fatalf("expected (\"United States\", \"\", true), got (%q, %q, %v)", out, sibling, ok)
	}
}

func TestLabelResolver_Replace_Miss(t *testing.T) {
	r, _ := BuildLabelResolver(
		[]*types.LabelBinding{{Field: "country", Table: "names"}},
		registryWithLabelTables(map[string]LabelTable{
			"names": {Rows: map[string]string{"US": "United States"}},
		}),
	)
	out, _, ok := r.Apply("country", "ZZ")
	if ok || out != "ZZ" {
		t.Fatalf("expected miss falling back to raw; got (%q, %v)", out, ok)
	}
	ws := r.Warnings()
	if len(ws) == 0 {
		t.Fatal("expected miss warning")
	}
	if ws[0].Code != errors.PULSE_LABEL_LOOKUP_MISS {
		t.Fatalf("expected PULSE_LABEL_LOOKUP_MISS, got %s", ws[0].Code)
	}
}

func TestLabelResolver_Augment(t *testing.T) {
	r, _ := BuildLabelResolver(
		[]*types.LabelBinding{{Field: "country", Table: "names", Mode: types.LabelModeAugment}},
		registryWithLabelTables(map[string]LabelTable{
			"names": {Rows: map[string]string{"US": "United States"}},
		}),
	)
	out, sibling, ok := r.Apply("country", "US")
	if !ok || out != "US" || sibling != "United States" {
		t.Fatalf("expected (\"US\", \"United States\", true), got (%q, %q, %v)", out, sibling, ok)
	}
	if r.AugmentField("country") != "country_label" {
		t.Fatalf("expected augment sibling country_label, got %q", r.AugmentField("country"))
	}
}

// Rows-backed tables get a pre-pass so both colliding source values
// render disambiguated symmetrically.
func TestLabelResolver_ReplaceCollisionSymmetric(t *testing.T) {
	r, _ := BuildLabelResolver(
		[]*types.LabelBinding{{Field: "country", Table: "names", Mode: types.LabelModeReplace}},
		registryWithLabelTables(map[string]LabelTable{
			"names": {Rows: map[string]string{
				"US":  "United States",
				"USA": "United States",
				"CA":  "Canada",
			}},
		}),
	)
	usOut, _, _ := r.Apply("country", "US")
	if usOut != "United States (US)" {
		t.Fatalf("expected \"United States (US)\", got %q", usOut)
	}
	usaOut, _, _ := r.Apply("country", "USA")
	if usaOut != "United States (USA)" {
		t.Fatalf("expected \"United States (USA)\", got %q", usaOut)
	}
	caOut, _, _ := r.Apply("country", "CA")
	if caOut != "Canada" {
		t.Fatalf("expected clean \"Canada\", got %q", caOut)
	}
	ws := r.Warnings()
	if len(ws) == 0 {
		t.Fatal("expected PULSE_LABEL_COLLISION warning")
	}
	saw := false
	for _, w := range ws {
		if w.Code == errors.PULSE_LABEL_COLLISION {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("expected PULSE_LABEL_COLLISION among warnings; got %+v", ws)
	}
}

// Function-driven tables can only detect collisions online; the first
// source seen renders cleanly, subsequent collisions render
// disambiguated.
func TestLabelResolver_FuncBackedAsymmetricCollision(t *testing.T) {
	mapping := map[string]string{
		"US":  "United States",
		"USA": "United States",
	}
	r, _ := BuildLabelResolver(
		[]*types.LabelBinding{{Field: "country", Table: "names"}},
		registryWithLabelTables(map[string]LabelTable{
			"names": {Lookup: func(key string) (string, bool, error) {
				v, ok := mapping[key]
				return v, ok, nil
			}},
		}),
	)
	first, _, _ := r.Apply("country", "US")
	second, _, _ := r.Apply("country", "USA")
	if first != "United States" {
		t.Fatalf("expected clean first emission, got %q", first)
	}
	if second != "United States (USA)" {
		t.Fatalf("expected disambiguated second emission, got %q", second)
	}
}

func TestLabelResolver_NoBindingPassesThrough(t *testing.T) {
	r, _ := BuildLabelResolver(
		[]*types.LabelBinding{{Field: "country", Table: "names"}},
		registryWithLabelTables(map[string]LabelTable{
			"names": {Rows: map[string]string{"US": "United States"}},
		}),
	)
	out, sibling, ok := r.Apply("other_field", "anything")
	if ok || out != "anything" || sibling != "" {
		t.Fatalf("expected pass-through for unbound field; got (%q, %q, %v)", out, sibling, ok)
	}
}

func TestLabelResolver_FieldsWithAugment(t *testing.T) {
	r, _ := BuildLabelResolver(
		[]*types.LabelBinding{
			{Field: "a", Table: "t", Mode: types.LabelModeReplace},
			{Field: "b", Table: "t", Mode: types.LabelModeAugment},
		},
		registryWithLabelTables(map[string]LabelTable{
			"t": {Rows: map[string]string{"x": "y"}},
		}),
	)
	got := r.FieldsWithAugment()
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("expected [b], got %v", got)
	}
}
