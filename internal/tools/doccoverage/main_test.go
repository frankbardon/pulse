package main

import (
	"bytes"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/doccoverage"
)

// TestRunWritesPage: the tool writes the generated page to -out (its
// currency against the committed page is TestDocsCoverageCurrent).
func TestRunWritesPage(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	for _, k := range []string{"PULSE_LABEL_TABLES_DIR", "PULSE_RANGE_TABLES_DIR", "PULSE_LOOKUP_TABLES_DIR",
		"PULSE_POPULATIONS_DIR", "PULSE_TEMPLATES_DIR", "PULSE_FEATURE_PROFILE"} {
		t.Setenv(k, "")
	}
	repo := afero.NewReadOnlyFs(afero.NewBasePathFs(afero.NewOsFs(), "../../.."))
	fsys := afero.NewCopyOnWriteFs(repo, afero.NewMemMapFs())
	if err := run(fsys, "out.md"); err != nil {
		t.Fatal(err)
	}
	got, err := afero.ReadFile(fsys, "out.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{"# Documentation Coverage", "## Rules", "| `pulse.New` |", "| `--json` |"} {
		if !bytes.Contains(got, []byte(frag)) {
			t.Errorf("written page lacks %q", frag)
		}
	}
	if _, err := repo.Stat(doccoverage.PagePath); err != nil {
		t.Fatalf("the committed page %s is missing: %v", doccoverage.PagePath, err)
	}
}
