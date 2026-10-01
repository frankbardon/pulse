package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/buildinfo"
)

func runVersion(t *testing.T, args ...string) string {
	t.Helper()
	cmd := VersionCommand()
	var buf bytes.Buffer
	cmd.Writer = &buf
	if err := cmd.Run(context.Background(), append([]string{"version"}, args...)); err != nil {
		t.Fatalf("version %v: %v", args, err)
	}
	return buf.String()
}

func TestVersionCommand_PlainOutput(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	if got, want := runVersion(t), "pulse v0.0.0-test\n"; got != want {
		t.Fatalf("plain output = %q, want %q", got, want)
	}
}

func TestVersionCommand_JSONEnvelope(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	out := runVersion(t, "--json")

	var env struct {
		FormatVersion string            `json:"format_version"`
		Data          map[string]string `json:"data"`
		Errors        []any             `json:"errors"`
		Warnings      []any             `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("envelope format_version = %q, want 1.1", env.FormatVersion)
	}
	if env.Errors == nil || env.Warnings == nil {
		t.Errorf("errors/warnings must be arrays, got %v / %v", env.Errors, env.Warnings)
	}
	d := env.Data
	if d["pulse_version"] != "v0.0.0-test" {
		t.Errorf("data.pulse_version = %q, want v0.0.0-test", d["pulse_version"])
	}
	if d["go_version"] != buildinfo.GoVersion() || d["go_version"] == "" {
		t.Errorf("data.go_version = %q, want %q", d["go_version"], buildinfo.GoVersion())
	}
	if d["format_version"] != env.FormatVersion {
		t.Errorf("data.format_version = %q, want envelope's %q", d["format_version"], env.FormatVersion)
	}
	for key, want := range map[string]string{"commit": buildinfo.Commit(), "commit_time": buildinfo.CommitTime()} {
		got, present := d[key]
		if want == "" && present {
			t.Errorf("data.%s present (%q) with no VCS info; want omitted", key, got)
		}
		if want != "" && got != want {
			t.Errorf("data.%s = %q, want %q", key, got, want)
		}
	}
}
