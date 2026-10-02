package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestMergeableAggregatorsAreRegistered: the shared merge rule
// (internal/mergegate) admits a built-in aggregator on its Mergeable()
// allow-list alone — it cannot see aggregatorRegistry — so every name
// on that list must resolve in the registry, or a mergeable-but-
// unregistered name would fan out across workers instead of surfacing
// the buffered path's unknown-aggregator error.
func TestMergeableAggregatorsAreRegistered(t *testing.T) {
	for _, a := range types.AllAggregationTypes() {
		if !a.Mergeable() {
			continue
		}
		if _, ok := aggregatorRegistry[a]; !ok {
			t.Errorf("%s is Mergeable() but not in aggregatorRegistry", a)
		}
	}
}

// TestChainRefusal_RegistryFacts: the registry adapter answers the
// extension half of the shared gate — a nil registry is built-ins only.
func TestChainRefusal_RegistryFacts(t *testing.T) {
	ok := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n"}}}
	if err := ChainRefusal(ok, nil, nil, 0, "s0"); err != nil {
		t.Fatalf("nil registry refused a built-in stage: %v", err)
	}
	if !CanChainRequest(ok, nil) || CanChainRequest(&types.Request{}, nil) {
		t.Fatal("CanChainRequest disagrees with ChainRefusal")
	}
	f := (*ExtensionRegistry)(nil).mergeFacts()
	for _, probe := range []func(string) (bool, bool){f.Aggregator, f.Grouper, f.Filterer, f.Attribute} {
		if _, known := probe("X_Y_Z"); known {
			t.Fatal("nil registry claimed an extension")
		}
	}
}
