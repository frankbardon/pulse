package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRegistryStreamabilityMatchesTypes asserts that for every registered
// aggregator, the runtime capability (does the constructed instance
// implement OnlineAggregator?) matches the type's declared Streamable()
// value. This catches drift between types/streamability.go and the
// processing implementations.
func TestRegistryStreamabilityMatchesTypes(t *testing.T) {
	for _, aggType := range types.AllAggregationTypes() {
		factory, ok := aggregatorRegistry[aggType]
		if !ok {
			t.Errorf("aggregator %s not in registry", aggType)
			continue
		}
		instance, err := factory(&types.Aggregation{Type: aggType, Field: "x"}, nil)
		if err != nil {
			t.Errorf("aggregator %s factory error: %v", aggType, err)
			continue
		}
		_, runtimeOnline := instance.(OnlineAggregator)
		declared := aggType.Streamable()
		if runtimeOnline != declared {
			t.Errorf("aggregator %s: types.Streamable()=%v but runtime OnlineAggregator=%v — update types/streamability.go to match implementation",
				aggType, declared, runtimeOnline)
		}
	}
}
