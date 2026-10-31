package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// E3-S2 (FR-21): `synth from-profile --disclosure-sample / --no-disclosure`
// driven through the real leaf.

// TestSynthFromProfileCLI_DisclosureFlags: the report carries the
// disclosure section by default, --disclosure-sample caps its sample,
// --no-disclosure drops it, and a negative cap is refused by its code.
func TestSynthFromProfileCLI_DisclosureFlags(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source, profile := richFromProfileFixture(t, dir, 120, 3)
	run := func(name string, extra ...string) (*synth.FidelityReport, string, error) {
		report := filepath.Join(dir, name+".json")
		var buf bytes.Buffer
		args := append([]string{"from-profile", "--profile", profile, "--source", source,
			"--output", filepath.Join(dir, name+".pulse"), "--rows", "50", "--seed", "4",
			"--fidelity-report", report, "--json"}, extra...)
		if err := runSynthCLI(t, &buf, args...); err != nil {
			return nil, buf.String(), err
		}
		raw, err := os.ReadFile(report)
		if err != nil {
			t.Fatal(err)
		}
		var rep synth.FidelityReport
		if err := json.Unmarshal(raw, &rep); err != nil {
			t.Fatal(err)
		}
		return &rep, buf.String(), nil
	}

	rep, out, err := run("default")
	if err != nil {
		t.Fatalf("default: %v\n%s", err, out)
	}
	if d := rep.Disclosure; d == nil || d.SourceRows != 120 || d.SyntheticRows != 50 || d.SampleRows != 50 {
		t.Errorf("default disclosure = %+v, want 120 source, 50 generated, all sampled", d)
	}
	if rep, out, err = run("capped", "--disclosure-sample", "7"); err != nil {
		t.Fatalf("capped: %v\n%s", err, out)
	}
	if d := rep.Disclosure; d == nil || d.SampleRows != 7 {
		t.Errorf("capped disclosure = %+v, want 7 sampled", d)
	}
	if rep, out, err = run("off", "--no-disclosure"); err != nil {
		t.Fatalf("off: %v\n%s", err, out)
	}
	if rep.Disclosure != nil {
		t.Errorf("--no-disclosure wrote %+v", rep.Disclosure)
	}
	if _, out, err = run("negative", "--disclosure-sample", "-1"); err == nil || !bytes.Contains([]byte(out), []byte(`"SERVICE_VALIDATION"`)) {
		t.Errorf("negative cap: err %v, envelope %s; want SERVICE_VALIDATION", err, out)
	}
}
