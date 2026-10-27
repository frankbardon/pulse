//go:build heavy

package pulse

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestExportReference_DefaultDeterministic (FR-33): the default instance
// exports to a MemMap fs, the tree carries the marker listing every
// other file, and a second export — in place, or into a fresh fs — is
// byte-identical.
func TestExportReference_DefaultDeterministic(t *testing.T) {
	t.Parallel()

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
