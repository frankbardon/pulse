package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// TestManifest_WeightAware: the manifest stamps weight_aware on exactly
// the aggregators the shared class table (internal/weighting — the one
// the resolver and the engine read) marks weight-aware, and every
// built-in aggregator carries a class.
func TestManifest_WeightAware(t *testing.T) {
	m := BuildManifest()
	seen := map[string]bool{}
	for _, op := range m.Components.Aggregators {
		seen[op.Name] = true
		if op.WeightAware != weighting.IsAware(op.Name) {
			t.Errorf("%s: weight_aware = %v, class table says %v", op.Name, op.WeightAware, weighting.IsAware(op.Name))
		}
		if weighting.ClassOf(op.Name) == weighting.ClassNone {
			t.Errorf("%s has no weight class", op.Name)
		}
	}
	for _, at := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE, types.AGG_WEIGHTED_MEAN, types.AGG_VARIANCE, types.AGG_STDDEV, types.AGG_WELFORD} {
		if !seen[string(at)] || !weighting.IsAware(string(at)) {
			t.Errorf("%s must be a weight-aware manifest aggregator", at)
		}
	}
	for _, cat := range [][]descriptor.Operator{m.Components.Attributes, m.Components.Groupers, m.Components.Filterers, m.Components.Windows, m.Components.Features} {
		for _, op := range cat {
			if op.WeightAware {
				t.Errorf("%s: only aggregators are weight-aware", op.Name)
			}
		}
	}
}
