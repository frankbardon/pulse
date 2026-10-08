package pulse

import (
	stderrors "errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
)

// exportTree reads every file under dir on fsys into a map keyed by the
// slash-separated path relative to dir.
func exportTree(t *testing.T, fsys afero.Fs, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := afero.Walk(fsys, dir, func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := afero.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func exportPulse(t *testing.T) *Pulse {
	t.Helper()
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func codeOf(err error) errors.Code {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// TestExportReference_DefaultDeterministic (FR-33): the default instance
// exports to a MemMap fs, the tree carries the marker listing every
// other file, and a second export — in place, or into a fresh fs — is
// byte-identical.
func TestExportReference_DefaultDeterministic(t *testing.T) {
	p := exportPulse(t)
	fsys := afero.NewMemMapFs()
	if err := p.ExportReference(fsys, "out/ref", ExportReferenceOptions{}); err != nil {
		t.Fatalf("ExportReference: %v", err)
	}
	first := exportTree(t, fsys, "out/ref")
	for _, want := range []string{"SUMMARY.md", "index.md", "catalog.md", "glossary.md", "reading.md", "skills.md", docsExportMarker} {
		if _, ok := first[want]; !ok {
			t.Errorf("export lacks %s", want)
		}
	}
	nSkills := 0
	for path := range first {
		if strings.HasPrefix(path, "skills/") {
			nSkills++
		}
	}
	if nSkills == 0 {
		t.Error("export carries no skill page")
	}
	var listed []string
	for _, line := range strings.Split(first[docsExportMarker], "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			listed = append(listed, line)
		}
	}
	var files []string
	for path := range first {
		if path != docsExportMarker {
			files = append(files, path)
		}
	}
	slices.Sort(files)
	if !slices.Equal(listed, files) {
		t.Errorf("marker lists %d paths, export wrote %d other files", len(listed), len(files))
	}

	if err := p.ExportReference(fsys, "out/ref", ExportReferenceOptions{}); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	other := afero.NewMemMapFs()
	if err := p.ExportReference(other, "elsewhere", ExportReferenceOptions{}); err != nil {
		t.Fatalf("fresh export: %v", err)
	}
	for name, got := range map[string]map[string]string{
		"in place": exportTree(t, fsys, "out/ref"),
		"fresh fs": exportTree(t, other, "elsewhere"),
	} {
		if len(got) != len(first) {
			t.Errorf("%s: %d files, first export %d", name, len(got), len(first))
		}
		for path, body := range first {
			if got[path] != body {
				t.Errorf("%s: %s differs from the first export", name, path)
			}
		}
	}
}

// TestExportReference_OmitSkills (FR-17): the zero value includes the
// skills; OmitSkills drops skills.md and the skills/ tree.
func TestExportReference_OmitSkills(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := exportPulse(t).ExportReference(fsys, "out", ExportReferenceOptions{OmitSkills: true}); err != nil {
		t.Fatal(err)
	}
	for path := range exportTree(t, fsys, "out") {
		if path == "skills.md" || strings.HasPrefix(path, "skills/") {
			t.Errorf("OmitSkills export carries %s", path)
		}
	}
}

// TestExportReference_RemovesStaleFiles (FR-35): re-exporting into a
// marked dir deletes every file the previous export recorded and this
// one no longer renders — directories left empty included — and leaves
// a file the marker never listed alone. A marker line escaping the
// export root is ignored.
func TestExportReference_RemovesStaleFiles(t *testing.T) {
	p := exportPulse(t)
	fsys := afero.NewMemMapFs()
	if err := p.ExportReference(fsys, "out", ExportReferenceOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "out/notes.md", []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "outside.md", []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "out/old/page.md", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker, _ := afero.ReadFile(fsys, "out/"+docsExportMarker)
	marker = append(marker, []byte("old/page.md\n../outside.md\n")...)
	if err := afero.WriteFile(fsys, "out/"+docsExportMarker, marker, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := p.ExportReference(fsys, "out", ExportReferenceOptions{OmitSkills: true}); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	got := exportTree(t, fsys, "out")
	for path := range got {
		if path == "skills.md" || strings.HasPrefix(path, "skills/") || strings.HasPrefix(path, "old/") {
			t.Errorf("stale %s survived the re-export", path)
		}
	}
	for _, d := range []string{"out/skills", "out/old"} {
		if ok, _ := afero.Exists(fsys, d); ok {
			t.Errorf("emptied directory %s survived", d)
		}
	}
	if got["notes.md"] != "mine" {
		t.Error("a file the marker never listed was deleted")
	}
	if ok, _ := afero.Exists(fsys, "outside.md"); !ok {
		t.Error("a marker line escaping the export root deleted a file outside it")
	}
	if _, ok := got["catalog.md"]; !ok {
		t.Error("re-export lacks catalog.md")
	}
}

// TestExportReference_RefusesUnmarkedDir (FR-35, FR-37): a non-empty dir
// without the marker is refused with PULSE_DOCS_EXPORT_DIR_NOT_EMPTY,
// reachable through errors.As, and nothing is written. An empty
// existing dir is accepted.
func TestExportReference_RefusesUnmarkedDir(t *testing.T) {
	p := exportPulse(t)
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "out/keep.txt", []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := p.ExportReference(fsys, "out", ExportReferenceOptions{})
	if got := codeOf(err); got != errors.PULSE_DOCS_EXPORT_DIR_NOT_EMPTY {
		t.Fatalf("code = %q (%v), want PULSE_DOCS_EXPORT_DIR_NOT_EMPTY", got, err)
	}
	var ce *errors.CodedError
	_ = stderrors.As(err, &ce)
	if ce.Details["path"] != "out" || ce.Details["marker"] != docsExportMarker {
		t.Errorf("details = %v", ce.Details)
	}
	if got := exportTree(t, fsys, "out"); len(got) != 1 || got["keep.txt"] != "mine" {
		t.Errorf("refused export touched the dir: %d files", len(got))
	}

	if err := fsys.MkdirAll("empty", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.ExportReference(fsys, "empty", ExportReferenceOptions{}); err != nil {
		t.Fatalf("empty dir refused: %v", err)
	}
}

// TestExportReference_FilesystemErrorsAreDataFile (FR-37): a write
// failure and a dir path naming a file both surface as DATA_FILE with
// the path in details.
func TestExportReference_FilesystemErrorsAreDataFile(t *testing.T) {
	p := exportPulse(t)
	ro := afero.NewReadOnlyFs(afero.NewMemMapFs())
	err := p.ExportReference(ro, "out", ExportReferenceOptions{})
	if got := codeOf(err); got != errors.DATA_FILE {
		t.Fatalf("read-only fs: code = %q (%v), want DATA_FILE", got, err)
	}

	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "out", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = p.ExportReference(fsys, "out", ExportReferenceOptions{})
	if got := codeOf(err); got != errors.DATA_FILE {
		t.Fatalf("file as dir: code = %q (%v), want DATA_FILE", got, err)
	}
	var ce *errors.CodedError
	_ = stderrors.As(err, &ce)
	if ce.Details["path"] != "out" {
		t.Errorf("details = %v", ce.Details)
	}
}

// TestExportReference_NilFsUsesInstanceFs: a nil fs writes through the
// instance filesystem.
func TestExportReference_NilFsUsesInstanceFs(t *testing.T) {
	p := exportPulse(t)
	if err := p.ExportReference(nil, "ref", ExportReferenceOptions{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := afero.Exists(p.Fs(), "ref/"+docsExportMarker); !ok {
		t.Error("nil fs did not write through the instance filesystem")
	}
}
