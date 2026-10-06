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
// read) with WeightAware and its WeightKinds and splices the optional
// weighted floor keys in after {n, n_null}. A key the operator already declares among its
// own keys (AGG_WEIGHTED_MEAN carries sum_weights and n_eff in its
// operator map) is not repeated.
func withWeightAware(ops []descriptor.Operator) []descriptor.Operator {
	for i := range ops {
		if !weighting.IsAware(ops[i].Name) {
			continue
		}
		ops[i].WeightAware = true
		ops[i].WeightKinds = weighting.KindsOf(ops[i].Name)
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

// withWeightKinds stamps WeightAware + WeightKinds on each operator of
// a non-aggregator family (attributes, groupers) the class table marks
// weighted under at least one kind. No floor keys: the weighted floor
// is the aggregator Components contract.
func withWeightKinds(ops []descriptor.Operator) []descriptor.Operator {
	for i := range ops {
		if kinds := weighting.KindsOf(ops[i].Name); kinds != nil {
			ops[i].WeightAware, ops[i].WeightKinds = true, kinds
		}
	}
	return ops
}

// stampWeightKinds sets weight_kinds on the manifest's tests (tier 1
// only — a post-test reads aggregated rows), regressions, matrices and
// overlays.
// Called only when capability:weighting is offered.
func stampWeightKinds(m *descriptor.Manifest) {
	for i := range m.Tests {
		m.Tests[i].WeightKinds = weighting.KindsOf(m.Tests[i].Family)
	}
	for i := range m.Regressions {
		m.Regressions[i].WeightKinds = weighting.KindsOf(m.Regressions[i].Name)
	}
	for i := range m.Matrices {
		m.Matrices[i].WeightKinds = weighting.KindsOf(m.Matrices[i].Name)
	}
	for i := range m.Overlays {
		m.Overlays[i].WeightKinds = OverlayWeightKinds(m.Overlays[i].Kind)
	}
}
