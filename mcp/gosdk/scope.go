package gosdk

// scope.go is the single place the adapter asks "does this instance offer
// X?" before mounting it. A feature profile on the *pulse.Pulse hides
// tools, prompts and resources by never registering them, so a hidden
// surface is indistinguishable from one that does not exist. A
// profile-free instance answers yes to everything, which keeps the
// mounted surface byte-identical to an unprofiled Register.

import (
	"encoding/json"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
)

// cohortResourcesFeature gates the pulse:// cohort enumeration.
var cohortResourcesFeature = descx.FeatureName(descx.FeatureKindMCPExtra, "cohort_resources")

// instanceOf returns p's instance snapshot, or nil (unscoped: every
// name enabled) when the root hook is not installed.
func instanceOf(p *pulse.Pulse) *descx.InstanceSnapshot {
	if facadebridge.InstanceSnapshot == nil {
		return nil
	}
	return facadebridge.InstanceSnapshot(p)
}

// toolEnabled reports whether the instance offers the named MCP tool:
// a core-bound tool always, a feature-bound one iff the feature is
// enabled. A tool with no binding (impossible while
// TestFeaturesHaveSince pins the table to toolmeta) stays mounted.
func toolEnabled(inst *descx.InstanceSnapshot, tool string) bool {
	b, ok := descx.MCPToolBindingOf(tool)
	if !ok || b.Core != "" {
		return true
	}
	return inst.Enabled(b.Feature)
}

// promptEnabled reports whether the instance offers the named prompt
// (its mcp_extra:prompt_* feature is enabled).
func promptEnabled(inst *descx.InstanceSnapshot, prompt string) bool {
	feature, ok := descx.MCPPromptFeatures()[prompt]
	if !ok {
		return true
	}
	return inst.Enabled(feature)
}

// skillVisible reports whether the instance exposes the named embedded
// skill as a pulse-skill:// resource. It is the one seam the
// instance-scoped skill prune plugs into; both the enumeration and the
// reader consult it, so a pruned skill reads exactly like a nonexistent
// one. Today every skill is visible.
func skillVisible(_ *descx.InstanceSnapshot, _ string) bool {
	return true
}

// scrubbedSchema applies the instance's prose scrub to every description
// in a tool input schema. The schema is Pulse-built JSON, so a decode
// failure cannot happen; should it, the schema is withheld from scrubbing
// rather than mounted half-rewritten.
func scrubbedSchema(scrub descx.ProseScrub, raw json.RawMessage) json.RawMessage {
	out, err := scrub.SchemaDescriptions(raw)
	if err != nil {
		return raw
	}
	return out
}
