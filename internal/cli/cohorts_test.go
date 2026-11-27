package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
)

// runCohorts runs `pulse cohorts args...` and returns its stdout.
func runCohorts(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := CohortsCommand()
	var buf bytes.Buffer
	cmd.Writer = &buf
	err := cmd.Run(context.Background(), append([]string{"cohorts"}, args...))
	return buf.String(), err
}

// TestCohortsCommand_FormatsTheLibraryListing: `pulse cohorts --json` is
// the Pulse.ListCohorts result in an envelope, and the text form prints
// one line per cohort.
// serial: sets PULSE_DATA_DIR.
func TestCohortsCommand_FormatsTheLibraryListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.csv"), []byte("id,region\n1,north\n2,south\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "src.csv", Handle: "sales"}); err != nil {
		t.Fatal(err)
	}
	want, err := p.ListCohorts(context.Background(), pulse.ListCohortsOptions{})
	if err != nil || len(want) != 1 {
		t.Fatalf("ListCohorts = %+v, %v", want, err)
	}
	t.Setenv("PULSE_DATA_DIR", dir)

	out, err := runCohorts(t, "--json")
	if err != nil {
		t.Fatalf("cohorts --json: %v", err)
	}
	var env struct {
		FormatVersion string                  `json:"format_version"`
		Data          []pulse.CohortListEntry `json:"data"`
		Errors        []any                   `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, out)
	}
	if env.FormatVersion != "1.1" || len(env.Errors) != 0 || len(env.Data) != 1 ||
		env.Data[0].Path != want[0].Path || !env.Data[0].Managed || env.Data[0].Handle != "sales" || env.Data[0].RecordCount != 2 {
		t.Errorf("envelope = %+v, want the library listing %+v", env, want)
	}

	text, err := runCohorts(t)
	if err != nil {
		t.Fatalf("cohorts: %v", err)
	}
	if !strings.Contains(text, "imports/sales.pulse  2 records  managed import sales  fields id:") {
		t.Errorf("text = %q", text)
	}

	if _, err := runCohorts(t, "--json", "no-such-dir"); err == nil {
		t.Error("cohorts no-such-dir: want a fatal error")
	}
}
