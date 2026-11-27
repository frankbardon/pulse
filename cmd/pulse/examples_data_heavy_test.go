//go:build heavy

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// TestExamplesData_MatchesBuildScript runs the repo's
// internal/examples/fixtures/build.sh (its `pulse` the test binary
// re-executed as the CLI) into one directory and `pulse examples data`
// into another: the same files come out, every one byte-identical
// except the point-lookup index (it fingerprints its cohort's mtime),
// which must be fresh, and the Analysis Guide's worked examples answer
// identically on both.
func TestExamplesData_MatchesBuildScript(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("build.sh needs bash: %v", err)
	}
	tmp := t.TempDir()
	script := filepath.Join(tmp, "pulse.sh")
	wrapper := "#!/bin/sh\nexec " + shellQuote(os.Args[0]) + " -test.run='^TestCLIHelperProcess$' -- \"$@\"\n"
	if err := os.WriteFile(script, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(tmp, "build_sh")
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "internal", "examples", "fixtures", "build.sh"))
	cmd.Env = append(os.Environ(), cliHelperEnv+"=1", "PULSE_FEATURE_PROFILE=", "PULSE_BIN="+script, "PULSE_DATA_DIR="+built)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build.sh: %v\n%s", err, stderr.String())
	}

	written := filepath.Join(tmp, "examples_data")
	if out, err := runApp(t, "examples", "data", "--out", written, "--json"); err != nil {
		t.Fatalf("examples data: %v\n%s", err, out)
	}

	names := func(dir string) []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	want, got := names(built), names(written)
	if !slices.Equal(got, want) {
		t.Fatalf("files written:\n got %v\nwant (build.sh) %v", got, want)
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewOsFs()})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		if strings.HasSuffix(name, ".idx") {
			continue
		}
		a, _ := os.ReadFile(filepath.Join(built, name))
		b, _ := os.ReadFile(filepath.Join(written, name))
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from build.sh's (%d vs %d bytes)", name, len(b), len(a))
		}
	}
	v, err := p.VerifyIndex(context.Background(), filepath.Join(written, "experiment.pulse"), []string{"id"})
	if err != nil || !v.Fresh {
		t.Errorf("experiment.pulse index on id: fresh %v, %v", v != nil && v.Fresh, err)
	}

	answer := func(dir, example string) string {
		t.Helper()
		p, err := pulse.New(pulse.Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		ex, ok := p.ExampleGet(example)
		if !ok {
			t.Fatalf("example %s not served", example)
		}
		var req pulse.Request
		if err := json.Unmarshal(ex.Body, &req); err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(context.Background(), &req)
		if err != nil {
			t.Fatalf("%s on %s: %v", example, dir, err)
		}
		body, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(body), dir, "<dir>")
	}
	for _, ex := range []string{"t_test_two_sample", "paired_t_before_after"} {
		if a, b := answer(built, ex), answer(written, ex); a != b {
			t.Errorf("%s answers differently:\nbuild.sh:      %s\nexamples data: %s", ex, a, b)
		}
	}
}

// shellQuote single-quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
