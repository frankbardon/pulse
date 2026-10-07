package pulse

import (
	"time"

	"github.com/frankbardon/pulse/internal/limits"
)

// FeatureProfileLimits is a feature profile's `limits` section: the
// instance resource limits the profile supplies. Keys are the
// snake_case limit names (the spelling `pulse mcp --limit` and every
// PULSE_LIMIT_* error's `limit` detail use). The encoding matches
// Options.Limits: an absent or 0 key means "use the built-in default",
// -1 (pulse.Unlimited) means no limit, and any other negative value is
// refused with PULSE_FEATURE_PROFILE_INVALID reason "invalid_limits".
//
// Per field, a non-zero Options.Limits value wins over the profile's,
// which wins over the built-in default. Limits are behaviour, not
// features: they never enter the feature-set digest.
type FeatureProfileLimits struct {
	// RequestTimeout is a Go duration string ("30s", "2m"). "" or "0"
	// means the default; "-1" or "unlimited" means no timeout. Any other
	// negative or unparseable value is refused.
	RequestTimeout string `json:"request_timeout,omitempty"`

	// MaxGroups is Options.Limits.MaxGroups' profile twin.
	MaxGroups int64 `json:"max_groups,omitempty"`

	// MaxCrosstabCells is Options.Limits.MaxCrosstabCells' profile twin.
	MaxCrosstabCells int64 `json:"max_crosstab_cells,omitempty"`

	// MaxEstimatedMemory is Options.Limits.MaxEstimatedMemory's profile
	// twin, in bytes.
	MaxEstimatedMemory int64 `json:"max_estimated_memory,omitempty"`

	// MaxMatrixDim is Options.Limits.MaxMatrixDim's profile twin.
	MaxMatrixDim int64 `json:"max_matrix_dim,omitempty"`

	// MaxComposeSlots is Options.Limits.MaxComposeSlots' profile twin.
	MaxComposeSlots int64 `json:"max_compose_slots,omitempty"`

	// MaxChainStages is Options.Limits.MaxChainStages' profile twin.
	MaxChainStages int64 `json:"max_chain_stages,omitempty"`

	// MaxJoinBuildRows is Options.Limits.MaxJoinBuildRows' profile twin.
	MaxJoinBuildRows int64 `json:"max_join_build_rows,omitempty"`
}

// featureProfileLimitsValue converts a profile `limits` section into the
// resolver's input layer. It refuses an unparseable request_timeout and
// any value below Unlimited, returning the offending limit name and its
// value as written; ok is false on a refusal.
func featureProfileLimitsValue(fl *FeatureProfileLimits) (l limits.Limits, bad limits.Name, value any, ok bool) {
	switch fl.RequestTimeout {
	case "", "0":
	case "-1", "unlimited":
		l.RequestTimeout = limits.Unlimited
	default:
		d, err := time.ParseDuration(fl.RequestTimeout)
		if err != nil {
			return limits.Limits{}, limits.RequestTimeout, fl.RequestTimeout, false
		}
		l.RequestTimeout = d
	}
	l.MaxGroups = fl.MaxGroups
	l.MaxCrosstabCells = fl.MaxCrosstabCells
	l.MaxEstimatedMemory = fl.MaxEstimatedMemory
	l.MaxMatrixDim = fl.MaxMatrixDim
	l.MaxComposeSlots = fl.MaxComposeSlots
	l.MaxChainStages = fl.MaxChainStages
	l.MaxJoinBuildRows = fl.MaxJoinBuildRows
	for _, n := range limits.Names() {
		if v := limits.Value(l, n); v < limits.Unlimited {
			if n == limits.RequestTimeout {
				return limits.Limits{}, n, fl.RequestTimeout, false
			}
			return limits.Limits{}, n, v, false
		}
	}
	return l, "", nil, true
}

// validateFeatureProfileLimits is the structural check of the profile's
// `limits` section, shared by pulse.New and CheckFeatureProfile: the
// first refused key is PULSE_FEATURE_PROFILE_INVALID reason
// "invalid_limits" with details {limit, value} (value as written).
// Unknown keys inside the section never reach it — the strict decode
// refuses them as unknown_key.
func validateFeatureProfileLimits(fp *FeatureProfile, path string) error {
	if fp.Limits == nil {
		return nil
	}
	_, name, value, ok := featureProfileLimitsValue(fp.Limits)
	if ok {
		return nil
	}
	details := map[string]any{"limit": string(name), "value": value}
	if path != "" {
		details["path"] = path
	}
	msg := "feature profile: limits." + string(name) + " is invalid; use 0 (or omit the key) for the default, -1 for no limit, or a positive value"
	if name == limits.RequestTimeout {
		msg += " (request_timeout is a Go duration string such as \"30s\", or \"unlimited\")"
	}
	return featureProfileInvalid(featureProfileReasonInvalidLimits, msg, details)
}
