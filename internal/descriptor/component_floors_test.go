package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// TestComponentFloors_PrefixEverySchema: every built-in ComponentSchema
// of a slot opens with that slot's floor keys, so the floor the
// generated reference documents is the one the capability tables emit.
func TestComponentFloors_PrefixEverySchema(t *testing.T) {
	m := BuildManifestForInstance(nil)
	bySlot := map[string]map[string]descriptor.ComponentSchema{
		"aggregations": m.ComponentsSchemas.Aggregators,
		"groupers":     m.ComponentsSchemas.Groupers,
		"filterers":    m.ComponentsSchemas.Filterers,
		"matrices":     m.ComponentsSchemas.Matrices,
	}
	floors := ComponentFloors()
	if len(floors) != len(bySlot) {
		t.Fatalf("ComponentFloors has %d slots, want %d", len(floors), len(bySlot))
	}
	for _, f := range floors {
		schemas, ok := bySlot[f.Slot]
		if !ok || len(schemas) == 0 {
			t.Fatalf("slot %s: no schemas", f.Slot)
		}
		for name, s := range schemas {
			if len(s.Keys) < len(f.Keys) || !slices.Equal(s.Keys[:len(f.Keys)], f.Keys) {
				t.Errorf("%s (%s) does not open with the slot floor", name, f.Slot)
			}
		}
	}
	names := []string{}
	for _, k := range WeightFloorKeys() {
		names = append(names, k.Name)
	}
	if !slices.Equal(names, []string{"sum_weights", "n_eff", "n_weight_invalid"}) {
		t.Errorf("WeightFloorKeys = %v", names)
	}
}
