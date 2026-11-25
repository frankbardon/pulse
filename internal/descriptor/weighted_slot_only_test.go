package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// TestWeightedSlotOnlyKey: the predicate selects every weighted floor
// key and an operator's own weighted moments, never an always-emitted
// key, and only optional keys carry the weighted-only lead.
func TestWeightedSlotOnlyKey(t *testing.T) {
	t.Parallel()

	for _, k := range weightFloorKeys() {
		if !WeightedSlotOnlyKey(k) {
			t.Errorf("weighted floor key %s not selected", k.Name)
		}
	}
	found := map[string]bool{}
	tables := [][]descriptor.Operator{aggregatorCapabilityTable(), attributeCapabilities(), filtererCapabilities(), grouperCapabilities()}
	for _, ops := range tables {
		for _, op := range ops {
			for _, k := range op.ComponentSchema.Keys {
				if weightedSlotOnlyLead.MatchString(k.Description) && !k.Optional {
					t.Errorf("%s.%s: a weighted-only key must be optional", op.Name, k.Name)
				}
				if WeightedSlotOnlyKey(k) {
					found[k.Name] = true
				}
			}
		}
	}
	for _, want := range []string{"sum_weighted", "weighted_mean", "m2_weighted", "sum_weights_sq", "weighted_variance", "se_note"} {
		if !found[want] {
			t.Errorf("weighted-only key %s not selected", want)
		}
	}
	for _, never := range []string{"n", "n_null", "sum", "mean"} {
		if found[never] {
			t.Errorf("always-emitted key %s selected", never)
		}
	}
	if WeightedSlotOnlyKey(descriptor.ComponentKey{Name: "x", Description: "Weighted slots only: x."}) {
		t.Error("a non-optional key selected")
	}
}
