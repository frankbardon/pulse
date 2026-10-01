package main

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/buildinfo"
)

// TestVersionSurfaces: `pulse --version` and the `pulse version` leaf both
// report the single build version from internal/buildinfo, never a literal.
func TestVersionSurfaces(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()

	out, err := runApp(t, "version")
	if err != nil {
		t.Fatalf("pulse version: %v", err)
	}
	if out != "pulse v0.0.0-test\n" {
		t.Errorf("pulse version = %q, want %q", out, "pulse v0.0.0-test\n")
	}

	if got := buildApp().Version; got != "v0.0.0-test" {
		t.Errorf("buildApp().Version = %q, want v0.0.0-test", got)
	}
	out, err = runApp(t, "--version")
	if err != nil {
		t.Fatalf("pulse --version: %v", err)
	}
	if !strings.Contains(out, "v0.0.0-test") {
		t.Errorf("pulse --version = %q, want it to carry v0.0.0-test", out)
	}
}
