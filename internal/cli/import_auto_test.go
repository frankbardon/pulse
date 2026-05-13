package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// withTempDataDir gives each test a hermetic PULSE_DATA_DIR rooted at
// a t.TempDir(). The pulse.New constructed in the CLI commands reads
// the env var; restoring it after the test is t.Setenv's job.
func withTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dir)
	t.Setenv("PULSE_IMPORTS_DIR", "imports")
	t.Setenv("PULSE_IMPORT_TTL", "7d")
	return dir
}

func writeCSVFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// runImportCLI drives a fresh ImportCommand with the supplied args.
// stdout is redirected to a discard writer so the test doesn't pollute
// `go test -v` output; the test asserts side effects on disk rather
// than captured stdout to stay robust against cli/v3 Writer wiring
// changes.
func runImportCLI(t *testing.T, args ...string) error {
	t.Helper()
	root := ImportCommand()
	root.Writer = io.Discard
	return root.Run(context.Background(), append([]string{"import"}, args...))
}

func TestImportAutoCLI_CSVImport(t *testing.T) {
	dir := withTempDataDir(t)
	_ = writeCSVFile(t, dir, "data.csv")

	if err := runImportCLI(t, "auto", "data.csv"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse")); err != nil {
		t.Errorf("imports/data.pulse missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse.meta.json")); err != nil {
		t.Errorf("sidecar missing: %v", err)
	}
}

func TestImportAutoCLI_PulsePassthrough(t *testing.T) {
	dir := withTempDataDir(t)
	if err := os.WriteFile(filepath.Join(dir, "curated.pulse"), []byte("PULSE\x00\x00\x00\x01"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runImportCLI(t, "auto", "curated.pulse"); err != nil {
		t.Fatalf("run: %v", err)
	}
	// No sidecar should exist for passthrough.
	if _, err := os.Stat(filepath.Join(dir, "imports", "curated.pulse.meta.json")); !os.IsNotExist(err) {
		t.Errorf("sidecar created for passthrough; err=%v", err)
	}
}

func TestImportListAndDropCLI(t *testing.T) {
	dir := withTempDataDir(t)
	_ = writeCSVFile(t, dir, "data.csv")

	if err := runImportCLI(t, "auto", "data.csv"); err != nil {
		t.Fatalf("auto: %v", err)
	}

	// List should not error and should not change disk state.
	if err := runImportCLI(t, "list"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse")); err != nil {
		t.Errorf("list removed file: %v", err)
	}

	if err := runImportCLI(t, "drop", "data"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse")); !os.IsNotExist(err) {
		t.Errorf("managed file present after drop: err=%v", err)
	}
}

func TestImportAutoCLI_OverwriteRequired(t *testing.T) {
	dir := withTempDataDir(t)
	_ = writeCSVFile(t, dir, "data.csv")

	if err := runImportCLI(t, "auto", "data.csv"); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second call without --overwrite must fail.
	if err := runImportCLI(t, "auto", "data.csv"); err == nil {
		t.Errorf("collision second call accepted; expected error")
	}
	if err := runImportCLI(t, "auto", "--overwrite", "data.csv"); err != nil {
		t.Errorf("overwrite call failed: %v", err)
	}
}
