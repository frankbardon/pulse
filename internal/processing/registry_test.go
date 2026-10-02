package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestAggregatorRegistryComplete(t *testing.T) {
	expected := types.AllAggregationTypes()
	for _, aggType := range expected {
		if _, ok := aggregatorRegistry[aggType]; !ok {
			t.Errorf("aggregator not registered: %s", aggType)
		}
	}
	if len(aggregatorRegistry) != len(expected) {
		t.Errorf("aggregator registry has %d entries, want %d", len(aggregatorRegistry), len(expected))
	}
}

func TestAttributeRegistryComplete(t *testing.T) {
	expected := types.AllAttributeTypes()
	for _, attrType := range expected {
		if _, ok := attributeRegistry[attrType]; !ok {
			t.Errorf("attribute not registered: %s", attrType)
		}
	}
	if len(attributeRegistry) != len(expected) {
		t.Errorf("attribute registry has %d entries, want %d", len(attributeRegistry), len(expected))
	}
}

func TestFiltererRegistryComplete(t *testing.T) {
	expected := types.AllFiltererTypes()
	for _, filterType := range expected {
		if _, ok := filtererRegistry[filterType]; !ok {
			t.Errorf("filterer not registered: %s", filterType)
		}
	}
	if len(filtererRegistry) != len(expected) {
		t.Errorf("filterer registry has %d entries, want %d", len(filtererRegistry), len(expected))
	}
}

func TestGrouperRegistryComplete(t *testing.T) {
	expected := types.AllGroupTypes()
	for _, groupType := range expected {
		if _, ok := grouperRegistry[groupType]; !ok {
			t.Errorf("grouper not registered: %s", groupType)
		}
	}
	if len(grouperRegistry) != len(expected) {
		t.Errorf("grouper registry has %d entries, want %d", len(grouperRegistry), len(expected))
	}
}

func TestNoAdjustmentAttribute(t *testing.T) {
	// Verify no "adjustment" attribute exists
	for key := range attributeRegistry {
		if key == "ATTR_ADJUSTMENT" {
			t.Error("adjustment attribute should not exist")
		}
	}
}
