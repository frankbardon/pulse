package mcpserve

import (
	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
	core "github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
)

// ServeInfo is the effective serving configuration of an MCP server
// built from a *pulse.Pulse and Options, after the instance's feature
// profile is folded in. It is what a startup notice should report: the
// requested Options alone can say the cohort scan is on while the
// profile has turned it off.
type ServeInfo struct {
	// CohortScan reports whether the startup walk that enumerates the
	// data root's .pulse files runs. False when Options.DisableCohortScan
	// is set, OR the instance's feature profile sets
	// behaviour.disable_cohort_scan, OR the profile omits
	// mcp_extra:cohort_resources — the same OR gosdk.Register applies.
	CohortScan bool

	// FeatureProfileLoaded reports whether the instance was built with a
	// feature profile.
	FeatureProfileLoaded bool

	// FeatureProfile is the loaded profile's free-form "profile" label.
	// Empty when no profile is loaded or the profile carries no label.
	FeatureProfile string

	// DefaultReturn is the `return` preset an MCP request without its
	// own `return` block is shaped by. Effective order, first set wins:
	// Options.DefaultReturn (`pulse mcp --return`), then the instance
	// default (pulse.Options.DefaultReturn, else the feature profile's
	// `return`), then the built-in types.ReturnPresetStandard. Empty only
	// when the instance default is a preset-less include/exclude
	// selection. Not validated here: Serve refuses an unknown preset.
	DefaultReturn types.ReturnPreset

	// Limits is the instance's EFFECTIVE resource limits in the
	// manifest's `limits` shape (descriptor.Manifest.Limits): one entry
	// per limit in declaration order, -1 meaning no limit.
	Limits []descriptor.LimitMeta
}

// Describe reports the effective serving configuration Serve,
// ServeStdio or gosdk.Register would run p with under opts. It reads
// the instance; it starts nothing.
func Describe(p *pulse.Pulse, opts Options) ServeInfo {
	fp, loaded := p.FeatureProfile()
	snap := instanceSnapshot(p)
	scanOff := opts.DisableCohortScan || (loaded && fp.Behaviour != nil && fp.Behaviour.DisableCohortScan) ||
		!cohortResourcesEnabled(p)
	info := ServeInfo{
		CohortScan:           !scanOff,
		FeatureProfileLoaded: loaded,
		DefaultReturn:        effectiveDefaultReturn(opts.DefaultReturn, snap),
		Limits:               descx.LimitsFor(snap),
	}
	if loaded {
		info.FeatureProfile = fp.Profile
	}
	return info
}

// effectiveDefaultReturn is the preset the MCP return-default layer
// (internal/mcp return_default.go) resolves a block-less request to:
// the serving option, else the instance default's preset, else the
// built-in MCP default.
func effectiveDefaultReturn(opt types.ReturnPreset, snap *descx.InstanceSnapshot) types.ReturnPreset {
	if opt != "" {
		return opt
	}
	if r := snap.DefaultReturn(); r != nil {
		return r.Preset
	}
	return core.DefaultReturnPreset
}

// instanceSnapshot is p's descriptor snapshot, nil when the facade
// bridge is not wired (LimitsFor and DefaultReturn are nil-safe).
func instanceSnapshot(p *pulse.Pulse) *descx.InstanceSnapshot {
	if facadebridge.InstanceSnapshot == nil {
		return nil
	}
	return facadebridge.InstanceSnapshot(p)
}

// cohortResourcesEnabled reports whether p offers the pulse:// cohort
// enumeration (mcp_extra:cohort_resources). True without a feature
// profile.
func cohortResourcesEnabled(p *pulse.Pulse) bool {
	if facadebridge.InstanceSnapshot == nil {
		return true
	}
	return instanceSnapshot(p).Enabled(descx.FeatureName(descx.FeatureKindMCPExtra, "cohort_resources"))
}
