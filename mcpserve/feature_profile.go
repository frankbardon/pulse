package mcpserve

import (
	"fmt"
	"os"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/profilefile"
)

// envFeatureProfile names the environment variable NewPulse falls back
// to when Options.FeatureProfileFile is empty.
const envFeatureProfile = "PULSE_FEATURE_PROFILE"

// NewPulse constructs the *pulse.Pulse an MCP server serves, applying the
// MCP-layer feature profile first. The profile source, highest first:
//
//  1. opts.FeatureProfileFile — refused (PULSE_FEATURE_PROFILE_INVALID,
//     reason both_options_set) when popts already carries a profile.
//  2. A profile popts already carries (popts.FeatureProfile or
//     popts.FeatureProfileFile) — PULSE_FEATURE_PROFILE is then ignored.
//  3. The PULSE_FEATURE_PROFILE environment variable.
//
// The flag and env forms are host OS paths, read here with the OS
// filesystem: they configure the server process, so they resolve against
// its working directory rather than inside the cohort data root. An
// unreadable file is PULSE_FEATURE_PROFILE_INVALID, reason
// file_unreadable, with the path under the "path" detail; a malformed
// one carries pulse.ParseFeatureProfile's refusal plus the path. Name and
// dependency validation happen in pulse.New exactly as for any profile.
//
// The returned instance carries the profile, so serving it through Serve
// or ServeStdio (or gosdk.Register) honours behaviour.disable_cohort_scan.
func NewPulse(popts pulse.Options, opts Options) (*pulse.Pulse, error) {
	embedderSet := popts.FeatureProfile != nil || popts.FeatureProfileFile != ""

	path := opts.FeatureProfileFile
	switch {
	case path != "" && embedderSet:
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_FEATURE_PROFILE_INVALID,
			fmt.Sprintf("feature profile: mcpserve.Options.FeatureProfileFile %q conflicts with the profile already set on pulse.Options; set exactly one", path),
			map[string]any{"reason": "both_options_set", "path": path})
	case path == "" && !embedderSet:
		path = os.Getenv(envFeatureProfile)
	}

	if path != "" {
		fp, err := profilefile.ReadOS(path)
		if err != nil {
			return nil, err
		}
		popts.FeatureProfile = fp
	}
	return pulse.New(popts)
}
