package main

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestMCP_ReturnFlag asserts `pulse mcp --return` reaches the gosdk
// Config: an unknown preset fails startup with PULSE_RETURN_INVALID and
// a real one lets the server come up.
func TestMCP_ReturnFlag(t *testing.T) {
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	dataDir := t.TempDir()
	if code, _ := profileCode(runMCPBriefly(t, "--data-dir", dataDir, "--return", "lean")); code != errors.PULSE_RETURN_INVALID {
		t.Fatalf("--return lean: code %q, want PULSE_RETURN_INVALID", code)
	}
	if err := runMCPBriefly(t, "--data-dir", dataDir, "--return", "full"); err != nil {
		if code, _ := profileCode(err); code != "" {
			t.Fatalf("--return full refused: %v", err)
		}
	}
}
