package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

// runDocs runs `docs <args>` and returns stdout and the error.
func runDocs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := DocsCommand()
	var buf bytes.Buffer
	cmd.Writer = &buf
	for _, sub := range cmd.Commands {
		sub.Writer = &buf
	}
	err := cmd.Run(context.Background(), append([]string{"docs"}, args...))
	return buf.String(), err
}

// exportedFiles lists every regular file under dir, slash-separated and
// relative to it.
func exportedFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// repoPath resolves a repo-root-relative path from this test file.
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", filepath.FromSlash(rel))
}

// TestDocsExport_DefaultNeedsNoDataDir: with PULSE_DATA_DIR unset the
// leaf exports the full reference, skills included, plus the marker.
func TestDocsExport_DefaultNeedsNoDataDir(t *testing.T) {
	t.Setenv("PULSE_DATA_DIR", "")
	os.Unsetenv("PULSE_DATA_DIR")
	out := filepath.Join(t.TempDir(), "ref")
	stdout, err := runDocs(t, "export", "--out", out)
	if err != nil {
		t.Fatalf("export: %v\n%s", err, stdout)
	}
	files := exportedFiles(t, out)
	for _, want := range []string{"SUMMARY.md", "skills.md", ".pulse-docs-export"} {
		if !files[want] {
			t.Errorf("missing %s in export (%d files)", want, len(files))
		}
	}
	hasSkill := false
	for f := range files {
		if filepath.Dir(f) == "skills" {
			hasSkill = true
			break
		}
	}
	if !hasSkill {
		t.Error("default export wrote no skills/ pages")
	}
}

// TestDocsExport_FeatureProfileShrinksTree: the minimal example feature
// profile exports strictly fewer files than the default instance.
func TestDocsExport_FeatureProfileShrinksTree(t *testing.T) {
	base := t.TempDir()
	full := filepath.Join(base, "full")
	minDir := filepath.Join(base, "min")
	if _, err := runDocs(t, "export", "--out", full); err != nil {
		t.Fatalf("full export: %v", err)
	}
	profile := repoPath(t, "examples/profiles/minimal.json")
	if _, err := runDocs(t, "export", "--out", minDir, "--feature-profile", profile); err != nil {
		t.Fatalf("profiled export: %v", err)
	}
	nFull, nMin := len(exportedFiles(t, full)), len(exportedFiles(t, minDir))
	if nMin == 0 || nMin >= nFull {
		t.Fatalf("profiled export has %d files, full has %d: want 0 < profiled < full", nMin, nFull)
	}
}

// TestDocsExport_NoSkillsOmitsSkillPages: --no-skills leaves out
// skills.md and the skills/ tree; --json reports it.
func TestDocsExport_NoSkillsOmitsSkillPages(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ref")
	stdout, err := runDocs(t, "export", "--out", out, "--no-skills", "--json")
	if err != nil {
		t.Fatalf("export: %v\n%s", err, stdout)
	}
	env := decodeFeaturesEnv(t, stdout)
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %+v", env.Errors)
	}
	var res DocsExportResult
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Out != out || res.Skills {
		t.Fatalf("data = %+v, want out %q skills false", res, out)
	}
	files := exportedFiles(t, out)
	if files["skills.md"] {
		t.Error("--no-skills still wrote skills.md")
	}
	for f := range files {
		if filepath.Dir(f) == "skills" {
			t.Fatalf("--no-skills still wrote %s", f)
		}
	}
	if !files["SUMMARY.md"] {
		t.Error("--no-skills export lost SUMMARY.md")
	}
}

// TestDocsExport_UnmarkedNonEmptyDirRefused: an --out holding a file
// the export did not write fails non-zero, keeps the library code in
// errors[0], and leaves the directory untouched.
func TestDocsExport_UnmarkedNonEmptyDirRefused(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "notes.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, err := runDocs(t, "export", "--out", out, "--json")
	if err == nil {
		t.Fatal("export into an unmarked non-empty dir succeeded")
	}
	if got := codeOf(err); got != perrors.PULSE_DOCS_EXPORT_DIR_NOT_EMPTY {
		t.Fatalf("returned error code = %q", got)
	}
	env := decodeFeaturesEnv(t, stdout)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.PULSE_DOCS_EXPORT_DIR_NOT_EMPTY) {
		t.Fatalf("errors = %+v", env.Errors)
	}
	if files := exportedFiles(t, out); len(files) != 1 {
		t.Fatalf("refused export touched the dir: %v", files)
	}
}

// TestDocsExport_BadFeatureProfileIsCoded: an unreadable profile path
// surfaces the profile reader's own code, not the placeholder.
func TestDocsExport_BadFeatureProfileIsCoded(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ref")
	stdout, err := runDocs(t, "export", "--out", out, "--feature-profile", filepath.Join(t.TempDir(), "absent.json"), "--json")
	if err == nil {
		t.Fatal("export with a missing profile succeeded")
	}
	env := decodeFeaturesEnv(t, stdout)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.PULSE_FEATURE_PROFILE_INVALID) {
		t.Fatalf("errors = %+v", env.Errors)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("failed export created %s", out)
	}
}
