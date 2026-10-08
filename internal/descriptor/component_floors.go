package descriptor

import "github.com/frankbardon/pulse/descriptor"

// ComponentFloor is one Response.Components slot's floor: the keys the
// orchestrator fills on every entry of the slot, ahead of the
// operator's own keys. Every built-in ComponentSchema of the slot opens
// with them (TestComponentFloors_PrefixEverySchema).
type ComponentFloor struct {
	// Slot is the Response.Components field the floor belongs to
	// ("aggregations", "groupers", "filterers", "matrices"), which is
	// also the Manifest.ComponentsSchemas sub-map of its operators.
	Slot string
	// Keys are the floor keys, in emission order.
	Keys []descriptor.ComponentKey
}

// ComponentFloors returns the floor of every Response.Components slot
// that carries per-operator schemas, in Response.Components order. The
// generated reference (internal/docgen) reads it so the floor it
// documents is the floor the capability tables declare.
func ComponentFloors() []ComponentFloor {
	return []ComponentFloor{
		{Slot: "aggregations", Keys: universalAggFloorKeys()},
		{Slot: "groupers", Keys: universalGrouperFloorKeys()},
		{Slot: "filterers", Keys: universalFiltererFloorKeys()},
		{Slot: "matrices", Keys: matrixFloorKeys()},
	}
}

// WeightFloorKeys returns the optional weighted floor keys (sum_weights,
// n_eff, n_weight_invalid) a weighted aggregator or matrix slot adds
// beside its floor.
func WeightFloorKeys() []descriptor.ComponentKey { return weightFloorKeys() }
