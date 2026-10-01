package gosdk_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/frankbardon/pulse/mcp/gosdk"
)

// TestConfigCore_EmptyVersionDefaultsToBuildVersion: an embedder that leaves
// Config.Version blank must get the real build version in the core catalog,
// never an empty string or a stale hardcoded literal.
func TestConfigCore_EmptyVersionDefaultsToBuildVersion(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	if got := (gosdk.Config{}).Core().Version; got != "v0.0.0-test" || got != pulse.Version() {
		t.Fatalf("Config{}.Core().Version = %q, want pulse.Version() = %q", got, pulse.Version())
	}
}

// TestConfigCore_ExplicitVersionWins: a caller-supplied identity is never
// overridden by the build version.
func TestConfigCore_ExplicitVersionWins(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	if got := (gosdk.Config{Version: "9.9.9"}).Core().Version; got != "9.9.9" {
		t.Fatalf("Config{Version: 9.9.9}.Core().Version = %q, want 9.9.9", got)
	}
}
