package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
)

// builtinPurposes is the per-operator Purpose registry, keyed by the
// manifest entry name: the operator constant (AGG_*, ATTR_*, FILTER_*,
// GROUP_*, WIN_*, FEAT_*), the TEST_* family (one purpose covers both
// tiers), the REG_* type, the OVERLAY_* kind or the synth distribution
// kind. It is assembled from one map per category, each in its own file
// (aggregatorPurposes in purposes_aggregators.go, attributePurposes in purposes_attributes.go, filtererPurposes in purposes_filterers.go, grouperPurposes in purposes_groupers.go, windowPurposes in purposes_window.go, featurePurposes in purposes_features.go, statTestPurposes in purposes_stattests.go,
// overlayPurposes in purposes_overlays.go, regressionPurposes in
// purposes_regressions.go, synthPurposes in purposes_synth.go);
// an undeclared name simply carries no intents.
//
// purposeLookup is the seam the manifest builder reads through, so a
// test can inject purposes without touching the registry.
var (
	builtinPurposes = mergePurposes(aggregatorPurposes, attributePurposes, filtererPurposes, grouperPurposes, windowPurposes, featurePurposes, statTestPurposes, overlayPurposes, regressionPurposes, synthPurposes)
	purposeLookup   = func(name string) (descriptor.Purpose, bool) {
		p, ok := builtinPurposes[name]
		return p, ok
	}
)

// PurposeOf returns the declared Purpose of a built-in manifest entry.
func PurposeOf(name string) (descriptor.Purpose, bool) {
	return purposeLookup(name)
}

// intentsOf returns the sorted intent IDs name's purpose declares, or
// nil when it declares none (so the entry's intents key is omitted).
func intentsOf(name string) []string {
	p, ok := purposeLookup(name)
	if !ok || len(p.Intents) == 0 {
		return nil
	}
	out := append([]string(nil), p.Intents...)
	sort.Strings(out)
	return out
}

// withOpIntents and its siblings stamp each manifest entry with its purpose's intent IDs.
// Every slice it is handed is already a fresh copy of the capability
// table, so writing in place never reaches the shared tables.
func withOpIntents(ops []descriptor.Operator) []descriptor.Operator {
	for i := range ops {
		ops[i].Intents = intentsOf(ops[i].Name)
	}
	return ops
}

func withTestIntents(ts []descriptor.TestMeta) []descriptor.TestMeta {
	for i := range ts {
		key := ts[i].Family
		if key == "" {
			key = ts[i].Name
		}
		ts[i].Intents = intentsOf(key)
	}
	return ts
}

func withRegressionIntents(rs []descriptor.RegressionMeta) []descriptor.RegressionMeta {
	for i := range rs {
		rs[i].Intents = intentsOf(rs[i].Name)
	}
	return rs
}

func withDistributionIntents(ds []descriptor.DistributionMeta) []descriptor.DistributionMeta {
	for i := range ds {
		ds[i].Intents = intentsOf(ds[i].Name)
	}
	return ds
}

func withOverlayIntents(os []descriptor.OverlayCapability) []descriptor.OverlayCapability {
	for i := range os {
		os[i].Intents = intentsOf(string(os[i].Kind))
	}
	return os
}

// mergePurposes assembles the per-category Purpose maps into one
// registry. A name declared by two category maps is a programming error
// and panics at package init, so the split can never silently shadow a
// declaration.
func mergePurposes(maps ...map[string]descriptor.Purpose) map[string]descriptor.Purpose {
	out := map[string]descriptor.Purpose{}
	for _, m := range maps {
		for name, p := range m {
			if _, dup := out[name]; dup {
				panic("descriptor: Purpose for " + name + " declared by two category maps")
			}
			out[name] = p
		}
	}
	return out
}
