package mcpserve_test

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/frankbardon/pulse/mcpserve"
)

const (
	// scanOffProfile is valid and observable: its behaviour switch is
	// readable through facadebridge.CohortScanDisabled.
	scanOffProfile = `{"features": [], "behaviour": {"disable_cohort_scan": true}}`
	// plainProfile is valid and leaves the scan on.
	plainProfile = `{"features": []}`
	// brokenProfile is refused by the strict decode.
	brokenProfile = `{"features": [], "limits": {"max_rows": 1}}`
)

func writeProfile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func requireInvalid(t *testing.T, err error, reason, path string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not a *errors.CodedError", err)
	}
	if ce.Code != errors.PULSE_FEATURE_PROFILE_INVALID {
		t.Fatalf("code = %s, want PULSE_FEATURE_PROFILE_INVALID (%v)", ce.Code, err)
	}
	if got := ce.Details["reason"]; got != reason {
		t.Errorf("reason = %v, want %s", got, reason)
	}
	if got := ce.Details["path"]; got != path {
		t.Errorf("path = %v, want %s", got, path)
	}
}

// TestNewPulse_FeatureProfileFileIsACwdRelativeOSPath asserts the field is
// read from the process working directory, not the instance Fs: with
// DataDir set to a different directory, a relative path still resolves
// against cwd, and the parsed profile reaches the instance.
func TestNewPulse_FeatureProfileFileIsACwdRelativeOSPath(t *testing.T) {
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	cwd := t.TempDir()
	writeProfile(t, cwd, "profile.json", scanOffProfile)
	t.Chdir(cwd)

	p, err := mcpserve.NewPulse(pulse.Options{DataDir: t.TempDir()}, mcpserve.Options{FeatureProfileFile: "profile.json"})
	if err != nil {
		t.Fatalf("NewPulse: %v", err)
	}
	if !facadebridge.CohortScanDisabled(p) {
		t.Error("profile from the cwd-relative path did not reach the instance")
	}
}

// TestNewPulse_InvalidProfileFails asserts a profile pulse.New would
// refuse, an unreadable path and a decode refusal all fail construction
// with the coded error, path attached.
func TestNewPulse_InvalidProfileFails(t *testing.T) {
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	dir := t.TempDir()

	broken := writeProfile(t, dir, "broken.json", brokenProfile)
	_, err := mcpserve.NewPulse(pulse.Options{DataDir: dir}, mcpserve.Options{FeatureProfileFile: broken})
	requireInvalid(t, err, "unknown_key", broken)

	missing := filepath.Join(dir, "absent.json")
	_, err = mcpserve.NewPulse(pulse.Options{DataDir: dir}, mcpserve.Options{FeatureProfileFile: missing})
	requireInvalid(t, err, "file_unreadable", missing)

	unknown := writeProfile(t, dir, "unknown.json", `{"features": ["AGG_NOT_A_THING"]}`)
	_, err = mcpserve.NewPulse(pulse.Options{DataDir: dir}, mcpserve.Options{FeatureProfileFile: unknown})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FEATURE_PROFILE_UNKNOWN {
		t.Errorf("unknown feature: err = %v, want PULSE_FEATURE_PROFILE_UNKNOWN", err)
	}
}

// TestNewPulse_EnvPrecedence pins the source order: the field beats the
// env var, the env var is the fallback, and a profile already on
// pulse.Options silences the env var entirely.
func TestNewPulse_EnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	scanOff := writeProfile(t, dir, "scan-off.json", scanOffProfile)
	plain := writeProfile(t, dir, "plain.json", plainProfile)
	broken := writeProfile(t, dir, "broken.json", brokenProfile)

	t.Run("env is the fallback", func(t *testing.T) {
		t.Setenv("PULSE_FEATURE_PROFILE", scanOff)
		p, err := mcpserve.NewPulse(pulse.Options{DataDir: dir}, mcpserve.Options{})
		if err != nil {
			t.Fatalf("NewPulse: %v", err)
		}
		if !facadebridge.CohortScanDisabled(p) {
			t.Error("PULSE_FEATURE_PROFILE was not applied")
		}
	})
	t.Run("invalid env fails construction", func(t *testing.T) {
		t.Setenv("PULSE_FEATURE_PROFILE", broken)
		_, err := mcpserve.NewPulse(pulse.Options{DataDir: dir}, mcpserve.Options{})
		requireInvalid(t, err, "unknown_key", broken)
	})
	t.Run("field beats env", func(t *testing.T) {
		t.Setenv("PULSE_FEATURE_PROFILE", broken)
		p, err := mcpserve.NewPulse(pulse.Options{DataDir: dir}, mcpserve.Options{FeatureProfileFile: plain})
		if err != nil {
			t.Fatalf("NewPulse: %v (the env var was read despite the field)", err)
		}
		if facadebridge.CohortScanDisabled(p) {
			t.Error("env profile applied over the field")
		}
	})
	t.Run("env ignored when pulse.Options carries a profile", func(t *testing.T) {
		t.Setenv("PULSE_FEATURE_PROFILE", broken)
		_, err := mcpserve.NewPulse(pulse.Options{DataDir: dir, FeatureProfile: &pulse.FeatureProfile{Features: []string{}}}, mcpserve.Options{})
		if err != nil {
			t.Fatalf("NewPulse with FeatureProfile: %v", err)
		}
		_, err = mcpserve.NewPulse(pulse.Options{DataDir: dir, FeatureProfileFile: "plain.json"}, mcpserve.Options{})
		if err != nil {
			t.Fatalf("NewPulse with FeatureProfileFile: %v", err)
		}
	})
	t.Run("field conflicts with a pulse.Options profile", func(t *testing.T) {
		t.Setenv("PULSE_FEATURE_PROFILE", "")
		_, err := mcpserve.NewPulse(pulse.Options{DataDir: dir, FeatureProfile: &pulse.FeatureProfile{Features: []string{}}},
			mcpserve.Options{FeatureProfileFile: plain})
		requireInvalid(t, err, "both_options_set", plain)
	})
}
