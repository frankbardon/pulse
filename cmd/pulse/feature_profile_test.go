package main

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
)

// runAppCtx is runApp with a caller-owned context, so a `pulse mcp` run
// that gets past construction stops serving when ctx expires instead of
// blocking on stdin.
func runAppCtx(t *testing.T, ctx context.Context, args ...string) error {
	t.Helper()
	app := buildApp()
	var buf bytes.Buffer
	setWriterRecursive(app, &buf)
	return app.Run(ctx, append([]string{"pulse"}, args...))
}

// runMCPBriefly runs `pulse mcp` for at most a short window and returns
// its error. A run that constructs successfully serves until the window
// closes, so any profile refusal surfaces before the deadline.
func runMCPBriefly(t *testing.T, args ...string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	return runAppCtx(t, ctx, append([]string{"mcp"}, args...)...)
}

func profileCode(err error) (errors.Code, string) {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return "", ""
	}
	reason, _ := ce.Details["reason"].(string)
	return ce.Code, reason
}

func writeProfileFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestMCP_FeatureProfileFlag asserts --feature-profile reaches pulse.New:
// an invalid profile fails startup with the coded error, and a valid one
// lets the server come up. The path is cwd-relative even though
// --data-dir names another directory.
func TestMCP_FeatureProfileFlag(t *testing.T) {
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	dataDir := t.TempDir()
	cwd := t.TempDir()
	writeProfileFile(t, cwd, "broken.json", `{"features": [], "limits": {}}`)
	writeProfileFile(t, cwd, "ok.json", `{"features": []}`)
	t.Chdir(cwd)

	err := runMCPBriefly(t, "--data-dir", dataDir, "--feature-profile", "broken.json")
	if code, reason := profileCode(err); code != errors.PULSE_FEATURE_PROFILE_INVALID || reason != "unknown_key" {
		t.Fatalf("invalid profile: err = %v, want PULSE_FEATURE_PROFILE_INVALID/unknown_key", err)
	}

	err = runMCPBriefly(t, "--data-dir", dataDir, "--feature-profile", "ok.json")
	if code, _ := profileCode(err); code != "" {
		t.Fatalf("valid cwd-relative profile refused: %v", err)
	}
}

// TestMCP_FeatureProfileEnv asserts PULSE_FEATURE_PROFILE reaches the mcp
// leaf, and that the flag beats it.
func TestMCP_FeatureProfileEnv(t *testing.T) {
	dataDir := t.TempDir()
	dir := t.TempDir()
	broken := writeProfileFile(t, dir, "broken.json", `{"features": [], "limits": {}}`)
	ok := writeProfileFile(t, dir, "ok.json", `{"features": []}`)
	t.Setenv("PULSE_FEATURE_PROFILE", broken)

	err := runMCPBriefly(t, "--data-dir", dataDir)
	if code, _ := profileCode(err); code != errors.PULSE_FEATURE_PROFILE_INVALID {
		t.Fatalf("env profile: err = %v, want PULSE_FEATURE_PROFILE_INVALID", err)
	}

	err = runMCPBriefly(t, "--data-dir", dataDir, "--feature-profile", ok)
	if code, _ := profileCode(err); code != "" {
		t.Fatalf("flag did not beat env: %v", err)
	}
}

// TestNonMCPLeavesIgnoreFeatureProfileEnv asserts the rest of the CLI is
// unprofiled: with PULSE_FEATURE_PROFILE naming an invalid file, leaves
// that construct a Pulse still succeed.
func TestNonMCPLeavesIgnoreFeatureProfileEnv(t *testing.T) {
	dir := t.TempDir()
	broken := writeProfileFile(t, dir, "broken.json", `{"features": [], "limits": {}}`)
	t.Setenv("PULSE_FEATURE_PROFILE", broken)
	pulsePath := createTestPulseFile(t, dir)

	if _, err := runApp(t, "manifest", "--json"); err != nil {
		t.Errorf("manifest --json: %v", err)
	}
	if _, err := runApp(t, "api", "sample", "--input", pulsePath, "--count", "2"); err != nil {
		t.Errorf("api sample: %v", err)
	}
	if _, err := runApp(t, "cohort", "inspect", "--json", pulsePath); err != nil {
		t.Errorf("cohort inspect: %v", err)
	}
}

// TestFeatures_InitThenCheck is the fresh-checkout acceptance: the
// profile `pulse features init` prints passes `pulse features check`,
// with no data directory configured.
func TestFeatures_InitThenCheck(t *testing.T) {
	t.Setenv("PULSE_DATA_DIR", "")
	out, err := runApp(t, "features", "init")
	if err != nil {
		t.Fatalf("features init: %v", err)
	}
	path := writeProfileFile(t, t.TempDir(), "p.json", out)
	out, err = runApp(t, "features", "check", path)
	if err != nil {
		t.Fatalf("features check of init output: %v\n%s", err, out)
	}
	if !strings.Contains(out, ": valid (") {
		t.Fatalf("features check output = %q", out)
	}
}
