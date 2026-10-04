package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/weighting"
)

// weightFloorKeys are the universal-floor keys a WEIGHTED aggregator
// slot adds beside {n, n_null}. All three are optional: the
// orchestrator emits them only when a weight is applied to the slot,
// and n_eff only under kind probability, so their absence is the
// per-slot "unweighted" signal (.claude/reference/weighting.md, Floor
// keys).
func weightFloorKeys() []descriptor.ComponentKey {
	return []descriptor.ComponentKey{
		{Name: "sum_weights", Type: "float64", Optional: true,
			Description: "Weighted slots only: the sum of the valid weights of the rows whose value was present."},
		{Name: "n_eff", Type: "float64", Optional: true,
			Description: "Weighted slots of kind probability only: Kish effective sample size (sum of weights)^2 / sum of squared weights."},
		{Name: "n_weight_invalid", Type: "int", Optional: true,
			Description: "Weighted slots only: rows with a present value whose weight was invalid (null, negative, NaN/Inf, non-integer frequency) and was excluded."},
	}
}

// withWeightAware stamps each weight-aware aggregator (the
// internal/weighting class table — the one the resolver and the engine
// read) with WeightAware and splices the optional weighted floor keys
// in after {n, n_null}. A key the operator already declares among its
// own keys (AGG_WEIGHTED_MEAN carries sum_weights and n_eff in its
// operator map) is not repeated.
func withWeightAware(ops []descriptor.Operator) []descriptor.Operator {
	for i := range ops {
		if !weighting.IsAware(ops[i].Name) {
			continue
		}
		ops[i].WeightAware = true
		keys := ops[i].ComponentSchema.Keys
		have := make(map[string]bool, len(keys))
		for _, k := range keys {
			have[k.Name] = true
		}
		floor := len(universalAggFloorKeys())
		out := make([]descriptor.ComponentKey, 0, len(keys)+3)
		out = append(out, keys[:floor]...)
		for _, k := range weightFloorKeys() {
			if !have[k.Name] {
				out = append(out, k)
			}
		}
		out = append(out, keys[floor:]...)
		ops[i].ComponentSchema.Keys = out
	}
	return ops
}
