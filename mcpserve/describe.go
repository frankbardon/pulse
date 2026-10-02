package mcpserve

import (
	"github.com/frankbardon/pulse"
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
	// is set OR the instance's feature profile sets
	// behaviour.disable_cohort_scan — the same OR gosdk.Register applies.
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
	scanOff := opts.DisableCohortScan
	if facadebridge.CohortScanDisabled != nil && facadebridge.CohortScanDisabled(p) {
		scanOff = true
	}
	info := ServeInfo{CohortScan: !scanOff}
	if facadebridge.FeatureProfileName != nil {
		info.FeatureProfile, info.FeatureProfileLoaded = facadebridge.FeatureProfileName(p)
	}
	return info
}
