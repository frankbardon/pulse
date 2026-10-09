package descriptor

import (
	"slices"
	"sort"

	"github.com/frankbardon/pulse/descriptor"
)

// BuildManifestForIntent constructs the instance's manifest narrowed to
// ONE intent — what ManifestForIntent, pulse_manifest {intent} and
// `pulse --json --intent` serve. The intent resolves through checkIntent
// (an unknown or hidden one is PULSE_RECOMMEND_INTENT_UNKNOWN,
// details.valid = the instance's intents). Starting from
// BuildManifestForInstance(inst), so it composes with a feature profile:
//
//   - the operator-keyed sections (components.*, tests, post_tests,
//     regressions, matrices, overlays) keep only entries whose intents
//     name the intent;
//   - skills is SkillsForIntent's ranked list;
//   - crosstab stays only when a remaining aggregator can be a crosstab
//     cell (its operator lists narrowed to the remaining aggregators),
//     matrix only when a matrix operator remains;
//   - the fixed sections (descriptor.ScopedManifestElidedKeys) are
//     dropped, zero in the Go value and absent on the wire —
//     components_schemas among them, since every remaining operator
//     carries the same schema as its own component_schema;
//   - version, digests, intents, return_presets and the examples counts
//     are kept; scope carries the full intent record and elided lists,
//     sorted, every key dropped here that the unscoped manifest carries.
func BuildManifestForIntent(inst *InstanceSnapshot, intent string) (*descriptor.Manifest, error) {
	if err := inst.checkIntent("manifest", intent); err != nil {
		return nil, err
	}
	linked, err := inst.SkillsForIntent(intent)
	if err != nil {
		return nil, err
	}
	m := BuildManifestForInstance(inst)
	serves := func(intents []string) bool { return slices.Contains(intents, intent) }

	m.Components = descriptor.Components{
		Aggregators: filterSlice(m.Components.Aggregators, func(o descriptor.Operator) bool { return serves(o.Intents) }),
		Attributes:  filterSlice(m.Components.Attributes, func(o descriptor.Operator) bool { return serves(o.Intents) }),
		Filterers:   filterSlice(m.Components.Filterers, func(o descriptor.Operator) bool { return serves(o.Intents) }),
		Groupers:    filterSlice(m.Components.Groupers, func(o descriptor.Operator) bool { return serves(o.Intents) }),
		Windows:     filterSlice(m.Components.Windows, func(o descriptor.Operator) bool { return serves(o.Intents) }),
		Features:    filterSlice(m.Components.Features, func(o descriptor.Operator) bool { return serves(o.Intents) }),
	}
	m.Tests = filterSlice(m.Tests, func(t descriptor.TestMeta) bool { return serves(t.Intents) })
	m.PostTests = filterSlice(m.PostTests, func(t descriptor.TestMeta) bool { return serves(t.Intents) })
	m.Regressions = filterSlice(m.Regressions, func(r descriptor.RegressionMeta) bool { return serves(r.Intents) })
	m.Matrices = filterSlice(m.Matrices, func(x descriptor.MatrixMeta) bool { return serves(x.Intents) })
	m.Overlays = filterSlice(m.Overlays, func(o descriptor.OverlayCapability) bool { return serves(o.Intents) })

	aggs := opNameSet(m.Components.Aggregators)

	m.Skills = make([]descriptor.SkillMeta, len(linked))
	for i, md := range linked {
		m.Skills[i] = descriptor.SkillMeta{Name: md.Name, Description: md.Description}
	}

	elided := descriptor.ScopedManifestElidedKeys()
	if m.Crosstab != nil {
		c := *m.Crosstab
		in := func(n string) bool { return aggs[n] }
		c.SummableAggregators = filterNames(c.SummableAggregators, in)
		c.MeanReducibleAggregators = filterNames(c.MeanReducibleAggregators, in)
		c.IndependentAggregators = filterNames(c.IndependentAggregators, in)
		c.RecomputeAggregators = filterNames(c.RecomputeAggregators, in)
		c.MapValuedCellAggregators = filterNames(c.MapValuedCellAggregators, in)
		if len(c.SummableAggregators)+len(c.MeanReducibleAggregators)+len(c.IndependentAggregators)+
			len(c.RecomputeAggregators)+len(c.MapValuedCellAggregators) == 0 {
			m.Crosstab = nil
			elided = append(elided, "crosstab")
		} else {
			m.Crosstab = &c
		}
	}
	if m.Matrix != nil && len(m.Matrices) == 0 {
		m.Matrix = nil
		elided = append(elided, "matrix")
	}
	sort.Strings(elided)

	// The fixed sections: zero in the Go value; the scoped wire form
	// omits their keys.
	m.Commands, m.Operations = nil, nil
	m.SynthDistributions = nil
	m.ErrorCodesCount, m.ErrorDomains, m.ErrorCodes = 0, nil, nil
	m.MCPTools, m.CohortTypes = nil, nil
	m.Extensions = descriptor.ExtensionsManifest{}
	m.Facet, m.ProcessChain, m.Join, m.Export, m.Import = nil, nil, nil, nil, nil
	m.Limits = nil
	m.ComponentsSchemas = descriptor.ComponentsSchemasBlock{}

	rec, _ := intentRecord(intent)
	m.Scope = &descriptor.ManifestScope{Intent: rec}
	m.Elided = elided
	return m, nil
}

// intentRecord is the taxonomy record of id (a deep copy).
func intentRecord(id string) (descriptor.Intent, bool) {
	for _, in := range Intents() {
		if in.ID == id {
			return in, true
		}
	}
	return descriptor.Intent{}, false
}

func opNameSet(ops []descriptor.Operator) map[string]bool {
	out := make(map[string]bool, len(ops))
	for _, o := range ops {
		out[o.Name] = true
	}
	return out
}
