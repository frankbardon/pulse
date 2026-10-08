package main

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestMCP_LimitFlag: a bad --limit fails startup with CLI_INPUT before
// the server comes up; good values (and a profile limits section beside
// them) let it start.
func TestMCP_LimitFlag(t *testing.T) {
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	dataDir := t.TempDir()
	for _, bad := range []string{"max_widgets=5", "max_groups=lots", "request_timeout=30", "max_groups"} {
		if code, _ := profileCode(runMCPBriefly(t, "--data-dir", dataDir, "--limit", bad)); code != errors.CLI_INPUT {
			t.Errorf("--limit %s: code %q, want CLI_INPUT", bad, code)
		}
	}
	profile := writeProfileFile(t, t.TempDir(), "p.json", `{"features": [], "limits": {"max_groups": 9}}`)
	for _, args := range [][]string{
		{"--limit", "max_groups=1000000", "--limit", "request_timeout=30s", "--limit", "max_matrix_dim=unlimited"},
		{"--feature-profile", profile, "--limit", "max_groups=10"},
	} {
		if err := runMCPBriefly(t, append([]string{"--data-dir", dataDir}, args...)...); err != nil {
			if code, _ := profileCode(err); code != "" {
				t.Fatalf("%v refused: %v", args, err)
			}
		}
	}
}
