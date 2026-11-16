package mcp

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestSweepAdaptive_MCPSchemaVariants: the bound sweep schema carries
// `budget`, `de`, `bo` and modes de and bo on an instance offering capability:compose_sweep_adaptive
// (the full one, which also lists every mode), and drops them on one
// hiding it — whether only the adaptive modes are hidden (the
// non-adaptive variant: every sampling key kept) or the sampling modes
// with them (the enumeration variant).
func TestSweepAdaptive_MCPSchemaVariants(t *testing.T) {
	t.Parallel()

	sampling := append(append([]string(nil), sweepSamplingFeatures...), "capability:compose_sweep_sampling")
	_, enumeration := instanceFor(t, &pulse.FeatureProfile{Features: sweepSamplingFeatures}, pulse.Extensions{})
	_, nonAdaptive := instanceFor(t, &pulse.FeatureProfile{Features: sampling}, pulse.Extensions{})
	_, adaptive := instanceFor(t, &pulse.FeatureProfile{Features: append(append([]string(nil), sampling...), "capability:compose_sweep_adaptive")}, pulse.Extensions{})
	for _, c := range []struct {
		name            string
		inst            *descx.InstanceSnapshot
		budget, samples bool
	}{
		{"enumeration", enumeration, false, false},
		{"non-adaptive", nonAdaptive, false, true},
		{"adaptive", adaptive, true, true},
	} {
		props := sweepSchema(c.inst)["properties"].(map[string]any)
		_, budget := props["budget"]
		_, samples := props["samples"]
		if budget != c.budget || samples != c.samples {
			t.Errorf("%s: budget %v samples %v, want %v %v", c.name, budget, samples, c.budget, c.samples)
		}
		modes := props["mode"].(map[string]any)["enum"].([]string)
		if c.samples && !slices.Contains(modes, "lhs") {
			t.Errorf("%s: modes %v", c.name, modes)
		}
		// Mode de and its knobs ride the adaptive capability (E3-S3).
		if _, de := props["de"]; de != c.budget || slices.Contains(modes, "de") != c.budget {
			t.Errorf("%s: de key %v, modes %v; want de offered %v", c.name, de, modes, c.budget)
		}
		// Mode bo and its knobs likewise (E4-S2).
		if _, bo := props["bo"]; bo != c.budget || slices.Contains(modes, "bo") != c.budget {
			t.Errorf("%s: bo key %v, modes %v; want bo offered %v", c.name, bo, modes, c.budget)
		}
	}
}
