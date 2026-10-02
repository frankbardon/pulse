package mcpserve

import (
	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
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
}

// Describe reports the effective serving configuration Serve,
// ServeStdio or gosdk.Register would run p with under opts. It reads
// the instance; it starts nothing.
func Describe(p *pulse.Pulse, opts Options) ServeInfo {
	fp, loaded := p.FeatureProfile()
	scanOff := opts.DisableCohortScan || (loaded && fp.Behaviour != nil && fp.Behaviour.DisableCohortScan) ||
		!cohortResourcesEnabled(p)
	info := ServeInfo{CohortScan: !scanOff, FeatureProfileLoaded: loaded}
	if loaded {
		info.FeatureProfile = fp.Profile
	}
	return info
}

// cohortResourcesEnabled reports whether p offers the pulse:// cohort
// enumeration (mcp_extra:cohort_resources). True without a feature
// profile.
func cohortResourcesEnabled(p *pulse.Pulse) bool {
	if facadebridge.InstanceSnapshot == nil {
		return true
	}
	return facadebridge.InstanceSnapshot(p).Enabled(descx.FeatureName(descx.FeatureKindMCPExtra, "cohort_resources"))
}
