package mcpserve

import (
	stderrors "errors"
	"fmt"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
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
		fp, err := readFeatureProfileOS(path)
		if err != nil {
			return nil, err
		}
		popts.FeatureProfile = fp
	}
	return pulse.New(popts)
}

// readFeatureProfileOS reads a profile from a host OS path. This is the
// one deliberate read outside the instance afero Fs: the path names
// server configuration, not a cohort.
func readFeatureProfileOS(path string) (*pulse.FeatureProfile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_FEATURE_PROFILE_INVALID,
			fmt.Sprintf("feature profile: cannot read %q: %v", path, err),
			map[string]any{"reason": "file_unreadable", "path": path})
	}
	fp, err := pulse.ParseFeatureProfile(raw)
	if err != nil {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			details := make(map[string]any, len(ce.Details)+1)
			for k, v := range ce.Details {
				details[k] = v
			}
			details["path"] = path
			return nil, errors.NewCodedErrorWithDetails(ce.Code,
				fmt.Sprintf("feature profile: %q: %s", path, strings.TrimPrefix(ce.Message, "feature profile: ")),
				details)
		}
		return nil, err
	}
	return fp, nil
}
