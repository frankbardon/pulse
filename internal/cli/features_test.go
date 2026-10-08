package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
)

// runFeatures runs `features <args>` and returns stdout and the error.
func runFeatures(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := FeaturesCommand()
	var buf bytes.Buffer
	cmd.Writer = &buf
	for _, sub := range cmd.Commands {
		sub.Writer = &buf
	}
	err := cmd.Run(context.Background(), append([]string{"features"}, args...))
	return buf.String(), err
}

func writeFeaturesFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type featuresEnv struct {
	FormatVersion string                      `json:"format_version"`
	Data          json.RawMessage             `json:"data"`
	Errors        []*descriptor.EnvelopeEntry `json:"errors"`
	Warnings      []*descriptor.EnvelopeEntry `json:"warnings"`
}

func decodeFeaturesEnv(t *testing.T, out string) featuresEnv {
	t.Helper()
	var env featuresEnv
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	if env.FormatVersion != "1.1" {
		t.Fatalf("format_version = %q", env.FormatVersion)
	}
	return env
}

func codeOf(err error) perrors.Code {
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		return ""
	}
	return ce.Code
}

// TestFeaturesInit_WritesStrictProfile: plain init prints a profile that
// the strict decode accepts and that equals the library's result.
func TestFeaturesInit_WritesStrictProfile(t *testing.T) {
	out, err := runFeatures(t, "init")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	fp, err := pulse.ParseFeatureProfile([]byte(out))
	if err != nil {
		t.Fatalf("init output is not a strict profile: %v\n%s", err, out)
	}
	want, err := pulse.InitFeatureProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fp.Features, ",") != strings.Join(want.Features, ",") || fp.WrittenWith != want.WrittenWith {
		t.Fatalf("init output differs from pulse.InitFeatureProfile")
	}

	out, err = runFeatures(t, "init", "--from", "minimal", "--json")
	if err != nil {
		t.Fatalf("init --from --json: %v", err)
	}
	env := decodeFeaturesEnv(t, out)
	seeded, err := pulse.ParseFeatureProfile(env.Data)
	if err != nil || seeded.Profile != "minimal" {
		t.Fatalf("init --from minimal data = %s (%v)", env.Data, err)
	}
}

// TestFeaturesInit_UnknownExampleKeepsCode: a fatal error exits non-zero
// and the envelope carries the library's own code.
func TestFeaturesInit_UnknownExampleKeepsCode(t *testing.T) {
	out, err := runFeatures(t, "init", "--from", "no-such-example", "--json")
	if codeOf(err) != perrors.PULSE_FEATURE_PROFILE_INVALID {
		t.Fatalf("err = %v, want PULSE_FEATURE_PROFILE_INVALID", err)
	}
	env := decodeFeaturesEnv(t, out)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.PULSE_FEATURE_PROFILE_INVALID) {
		t.Fatalf("errors = %+v", env.Errors)
	}
}

// TestFeaturesCheck covers the valid, warning and failing paths.
func TestFeaturesCheck(t *testing.T) {
	ok := writeFeaturesFile(t, `{"features": ["capability:process", "AGG_COUNT"]}`)
	out, err := runFeatures(t, "check", ok)
	if err != nil {
		t.Fatalf("check valid: %v", err)
	}
	if !strings.Contains(out, "valid (2 features") {
		t.Fatalf("check text = %q", out)
	}

	ext := writeFeaturesFile(t, `{"features": ["capability:process", "AGG_ACME_FOO"]}`)
	out, err = runFeatures(t, "check", "--json", ext)
	if err != nil {
		t.Fatalf("offline check of an extension-like name must pass: %v", err)
	}
	env := decodeFeaturesEnv(t, out)
	if len(env.Warnings) != 1 || env.Warnings[0].Code != string(perrors.PULSE_FEATURE_PROFILE_UNKNOWN) ||
		env.Warnings[0].Details["reason"] != "unverified_extension" {
		t.Fatalf("warnings = %+v", env.Warnings)
	}
	out, err = runFeatures(t, "check", ext)
	if err != nil || !strings.Contains(out, "Warning [PULSE_FEATURE_PROFILE_UNKNOWN]") {
		t.Fatalf("text warning missing: %q (%v)", out, err)
	}

	bad := writeFeaturesFile(t, `{"features": ["AGG_SUMM"]}`)
	out, err = runFeatures(t, "check", "--json", bad)
	if codeOf(err) != perrors.PULSE_FEATURE_PROFILE_UNKNOWN {
		t.Fatalf("err = %v, want PULSE_FEATURE_PROFILE_UNKNOWN (non-zero exit)", err)
	}
	env = decodeFeaturesEnv(t, out)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.PULSE_FEATURE_PROFILE_UNKNOWN) {
		t.Fatalf("errors = %+v", env.Errors)
	}
	if _, err := runFeatures(t, "check", bad); codeOf(err) != perrors.PULSE_FEATURE_PROFILE_UNKNOWN {
		t.Fatalf("text path err = %v", err)
	}

	malformed := writeFeaturesFile(t, `{"features": [], "limits": {"max_rows": 1}}`)
	_, err = runFeatures(t, "check", malformed)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_FEATURE_PROFILE_INVALID || ce.Details["reason"] != "unknown_key" {
		t.Fatalf("malformed err = %v, want PULSE_FEATURE_PROFILE_INVALID/unknown_key", err)
	}
	if _, err := runFeatures(t, "check", filepath.Join(t.TempDir(), "absent.json")); codeOf(err) != perrors.PULSE_FEATURE_PROFILE_INVALID {
		t.Fatalf("unreadable err = %v", err)
	}
	out, err = runFeatures(t, "check", "--json")
	if codeOf(err) != perrors.CLI_INPUT {
		t.Fatalf("missing FILE err = %v", err)
	}
	if env := decodeFeaturesEnv(t, out); len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.CLI_INPUT) {
		t.Fatalf("missing FILE errors = %+v", env.Errors)
	}
}

// TestFeaturesDiff_ListsNewerFeature: a profile written with an older
// release sees the running build's features as missing and new.
func TestFeaturesDiff_ListsNewerFeature(t *testing.T) {
	path := writeFeaturesFile(t, `{"written_with": "0.9.0", "features": ["capability:process", "AGG_SUMM"]}`)
	out, err := runFeatures(t, "diff", "--json", path)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	var diff pulse.FeatureProfileDiff
	if err := json.Unmarshal(decodeFeaturesEnv(t, out).Data, &diff); err != nil {
		t.Fatal(err)
	}
	var sawNew bool
	for _, m := range diff.Missing {
		if m.Name == "capability:process" {
			t.Fatalf("listed feature reported missing")
		}
		if m.Name == "AGG_COUNT" && m.New {
			sawNew = true
		}
	}
	if !sawNew {
		t.Fatalf("AGG_COUNT not listed as a new missing feature: %+v", diff.Missing)
	}
	if len(diff.Unknown) != 1 || diff.Unknown[0].Name != "AGG_SUMM" {
		t.Fatalf("unknown = %+v", diff.Unknown)
	}

	out, err = runFeatures(t, "diff", path)
	if err != nil {
		t.Fatalf("diff text: %v", err)
	}
	if !strings.Contains(out, "  AGG_COUNT  since 1.0.0  [new]\n") || !strings.Contains(out, "unknown (1):\n  AGG_SUMM: unregistered\n") {
		t.Fatalf("diff text = %s", out)
	}
}

// TestFeaturesShow describes every listed feature on both paths.
func TestFeaturesShow(t *testing.T) {
	path := writeFeaturesFile(t, `{"profile": "p", "features": ["capability:process", "AGG_COUNT"]}`)
	out, err := runFeatures(t, "show", "--json", path)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var desc pulse.FeatureProfileDescription
	if err := json.Unmarshal(decodeFeaturesEnv(t, out).Data, &desc); err != nil {
		t.Fatal(err)
	}
	if desc.Profile != "p" || len(desc.Features) != 2 {
		t.Fatalf("desc = %+v", desc)
	}

	out, err = runFeatures(t, "show", path)
	if err != nil {
		t.Fatalf("show text: %v", err)
	}
	if !strings.Contains(out, "  AGG_COUNT  operator/AGG  builtin  since 1.0.0  depends on capability:process|") {
		t.Fatalf("show text = %s", out)
	}

	if _, err := runFeatures(t, "show", writeFeaturesFile(t, `{}`)); codeOf(err) != perrors.PULSE_FEATURE_PROFILE_INVALID {
		t.Fatalf("show of a profile with no features: err = %v", err)
	}
}
