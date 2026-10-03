// Package profilefile reads a feature profile from a host OS path. It is
// the one shared reader behind every entry point that takes a profile
// path naming process configuration rather than a cohort: the MCP
// server's --feature-profile flag and PULSE_FEATURE_PROFILE
// (mcpserve.NewPulse) and the `pulse features` CLI leaves. The root
// package never imports it, so it may import the root.
package profilefile

import (
	stderrors "errors"
	"fmt"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
)

// ReadOS reads and strictly decodes the profile at path with the OS
// filesystem — a deliberate read outside any instance afero Fs. An
// unreadable file is PULSE_FEATURE_PROFILE_INVALID reason
// file_unreadable; a decode refusal keeps pulse.ParseFeatureProfile's
// code and reason. Either way the path rides the message and the "path"
// detail.
func ReadOS(path string) (*pulse.FeatureProfile, error) {
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
