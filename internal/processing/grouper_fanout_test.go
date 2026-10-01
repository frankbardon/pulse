package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestGrouperFanOutMatchesTypes is the runtime half of the
// GroupType.FansOut() contract. For every grouper in grouperRegistry
// it constructs an instance and asserts that the declared trait
// (types/streamability.go) matches whether the constructed grouper
// implements MultiKeyStreamingGrouper — the interface that actually
// fans one record into many keys at runtime.
//
// The trait lives in types/ because descriptor/ cannot reach this
// interface assertion (TestPredictNoExecutionImports forbids importing
// internal/processing/). This test is what catches a future fan-out grouper
// added without flipping the flag: implement KeysForRow and forget
// FansOut(), and the two halves disagree here.
//
// Probe construction mirrors axisStreamable in crosstab_fused_gate.go:
// build from the registry factory, run ApplyGrouperExtensions (a named
// range `table:` resolves lazily through that hook), then assert.
func TestGrouperFanOutMatchesTypes(t *testing.T) {
	fixtures := allGroupParityFixtures(t)

	for _, groupType := range types.AllGroupTypes() {
		factory, ok := grouperRegistry[groupType]
		if !ok {
			t.Errorf("grouper %s not in registry", groupType)
			continue
		}
		fix, ok := fixtures[groupType]
		if !ok {
			t.Fatalf("no group parity fixture for %s — add one in allGroupParityFixtures", groupType)
		}
		spec := &types.Group{
			Type:     groupType,
			Field:    fix.field,
			Interval: fix.interval,
			Params:   fix.params,
		}
		instance, err := factory(spec, fix.schema)
		if err != nil {
			t.Errorf("grouper %s factory error: %v", groupType, err)
			continue
		}
		ApplyGrouperExtensions(instance, nil)

		_, runtimeMulti := instance.(MultiKeyStreamingGrouper)
		declared := groupType.FansOut()
		if runtimeMulti != declared {
			t.Errorf("grouper %s: types.FansOut()=%v but runtime MultiKeyStreamingGrouper=%v — update types/streamability.go to match implementation",
				groupType, declared, runtimeMulti)
		}
	}
}

// TestGrouperFanOutCountsExceedRecords is the behavioural reason the
// trait exists: under a fan-out grouper the per-bucket counts sum to
// MORE than the record total, so an n read off a slab total
// double-counts records. A single-key grouper over the same cohort
// sums to exactly the record total.
func TestGrouperFanOutCountsExceedRecords(t *testing.T) {
	fixtures := allGroupParityFixtures(t)

	fanOut, ok := fixtures[types.GROUP_SET_PER_ELEMENT]
	if !ok {
		t.Fatalf("no fixture for GROUP_SET_PER_ELEMENT")
	}
	if !types.GROUP_SET_PER_ELEMENT.FansOut() {
		t.Fatalf("GROUP_SET_PER_ELEMENT.FansOut() = false, want true")
	}

	factory := grouperRegistry[types.GROUP_SET_PER_ELEMENT]
	instance, err := factory(&types.Group{
		Type:  types.GROUP_SET_PER_ELEMENT,
		Field: fanOut.field,
	}, fanOut.schema)
	if err != nil {
		t.Fatalf("set-per-element factory: %v", err)
	}
	multi, ok := instance.(MultiKeyStreamingGrouper)
	if !ok {
		t.Fatalf("set-per-element grouper does not implement MultiKeyStreamingGrouper")
	}

	totalKeys := 0
	rowsWithKeys := 0
	for _, rec := range fanOut.records {
		keys, ok, err := multi.KeysForRow(rec, fanOut.field)
		if err != nil {
			t.Fatalf("KeysForRow: %v", err)
		}
		if !ok {
			continue
		}
		rowsWithKeys++
		totalKeys += len(keys)
	}
	if rowsWithKeys == 0 {
		t.Fatalf("fixture produced no keyed rows — cannot demonstrate fan-out")
	}
	if totalKeys <= rowsWithKeys {
		t.Errorf("total emitted keys = %d over %d keyed rows; want > %d so the fan-out is observable",
			totalKeys, rowsWithKeys, rowsWithKeys)
	}
}
