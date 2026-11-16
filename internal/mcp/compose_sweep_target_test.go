package mcp

import (
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestSweepTarget_MCPSchemaFollowsParams (#521): the bound sweep schema
// offers an axis `target` only where capability:params is enabled — on
// the sampling and the enumeration variant alike.
func TestSweepTarget_MCPSchemaFollowsParams(t *testing.T) {
	t.Parallel()

	with := func(extra ...string) *descx.InstanceSnapshot {
		_, inst := instanceFor(t, &pulse.FeatureProfile{Features: append(append([]string(nil), sweepSamplingFeatures...), extra...)}, pulse.Extensions{})
		return inst
	}
	for _, c := range []struct {
		name string
		inst *descx.InstanceSnapshot
		want bool
	}{
		{"hidden", with(), false},
		{"hidden sampling", with("capability:compose_sweep_sampling"), false},
		{"enabled", with("capability:params", "FILTER_EXPRESSION"), true},
		{"enabled sampling", with("capability:params", "FILTER_EXPRESSION", "capability:compose_sweep_sampling"), true},
		{"unscoped", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			axis := sweepSchema(c.inst)["properties"].(map[string]any)["axes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
			if _, has := axis["target"]; has != c.want {
				t.Fatalf("axis target present = %v, want %v", has, c.want)
			}
		})
	}
}
