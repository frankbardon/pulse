package pulse

import (
	"time"

	"github.com/frankbardon/pulse/internal/limits"
)

// Limits holds the instance resource limits set through
// Options.Limits. On input a zero field means "use the built-in
// default" and Unlimited (-1) means no limit; any other negative value
// fails New() with PULSE_LIMIT_INVALID. The effective limits
// (Pulse.Limits) never carry a zero: every field is a positive bound or
// Unlimited. A breach raises PULSE_LIMIT_EXCEEDED with details
// {limit, configured, observed, option}.
type Limits = limits.Limits

// Unlimited disables a limit. Untyped, so it is assignable to every
// Limits field (time.Duration and int64 alike).
const Unlimited = limits.Unlimited

// Built-in limit defaults — the effective value of a Limits field left
// zero on every layer. RequestTimeout and MaxEstimatedMemory are opt-in
// (Unlimited); the rest are high enough that no legitimate request
// trips them. Raising a default is compatible; lowering one is not.
const (
	DefaultRequestTimeout     time.Duration = limits.DefaultRequestTimeout
	DefaultMaxGroups          int64         = limits.DefaultMaxGroups
	DefaultMaxCrosstabCells   int64         = limits.DefaultMaxCrosstabCells
	DefaultMaxEstimatedMemory int64         = limits.DefaultMaxEstimatedMemory
	DefaultMaxMatrixDim       int64         = limits.DefaultMaxMatrixDim
	DefaultMaxComposeSlots    int64         = limits.DefaultMaxComposeSlots
	DefaultMaxChainStages     int64         = limits.DefaultMaxChainStages
	DefaultMaxJoinBuildRows   int64         = limits.DefaultMaxJoinBuildRows
)

// Limits returns a copy of the instance's effective resource limits:
// per field, a non-zero Options.Limits value, else the feature
// profile's value, else the built-in default.
func (p *Pulse) Limits() Limits {
	return p.svc.Limits()
}

// resolveLimits validates Options.Limits and resolves the effective
// limits against the feature profile's layer and the built-in defaults.
func resolveLimits(opts Options, profile *FeatureProfile) (limits.Limits, error) {
	if err := limits.Validate(opts.Limits); err != nil {
		return limits.Limits{}, err
	}
	return limits.Resolve(opts.Limits, profileLimits(profile)), nil
}

// profileLimits returns the feature profile's `limits` layer, nil when
// the profile carries none. The profile model has no `limits` section
// yet (the key is reserved and refused at decode), so this is nil for
// every profile today.
func profileLimits(_ *FeatureProfile) *limits.Limits {
	return nil
}
