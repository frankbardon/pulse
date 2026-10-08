package pulse

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/docgen"
)

// updateDocs rewrites docs/src/guide/** and the SUMMARY.md span from the
// default instance — the same output `make docs` produces.
var updateDocs = flag.Bool("update", false, "rewrite docs/src/guide/** and the docs/src/SUMMARY.md span (prefer `make docs`)")

const (
	docsGuideDir   = "docs/src/guide"
	docsBookSumm   = "docs/src/SUMMARY.md"
	docsGuidePrefx = "guide/"
	docsStaleHint  = "the committed Analysis Guide is stale: run `make docs` and commit docs/src/guide/** and docs/src/SUMMARY.md. " +
		"Never hand-edit docs/src/guide — edit the source (the guidance registries in internal/descriptor, a skill under internal/skills, " +
		"or internal/docgen/components_intro.md for the Components intro) and regenerate."
)

// TestDocsGeneratedCurrent keeps the public book's generated Analysis
// Guide honest: it exports the default instance (the instance `make
// docs` exports — no feature profile, no table or template directories)
// into memory and requires docs/src/guide/** to match it byte for byte,
// file for file (marker included, no extra file), and the
// docs/src/SUMMARY.md span between the docgen:summary lines to be the
// export's SUMMARY fragment spliced in. A Purpose, Interpretation,
// glossary or skill edit without `make docs` fails here.
func TestDocsGeneratedCurrent(t *testing.T) {
	for _, k := range []string{"PULSE_LABEL_TABLES_DIR", "PULSE_RANGE_TABLES_DIR", "PULSE_TEMPLATES_DIR", "PULSE_FEATURE_PROFILE"} {
		t.Setenv(k, "")
	}
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	mem := afero.NewMemMapFs()
	if err := p.ExportReference(mem, "/guide", ExportReferenceOptions{}); err != nil {
		t.Fatal(err)
	}
	want, err := docsTree(mem, "/guide")
	if err != nil {
		t.Fatal(err)
	}
	book, err := os.ReadFile(docsBookSumm)
	if err != nil {
		t.Fatal(err)
	}
	wantBook, err := docgen.SpliceSummary(book, want["SUMMARY.md"], docsGuidePrefx)
	if err != nil {
		t.Fatalf("%s: %v", docsBookSumm, err)
	}

	if *updateDocs {
		if err := p.ExportReference(afero.NewOsFs(), docsGuideDir, ExportReferenceOptions{}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(book, wantBook) {
			if err := os.WriteFile(docsBookSumm, wantBook, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}

	got, err := docsTree(afero.NewOsFs(), docsGuideDir)
	if err != nil {
		t.Fatalf("%v\n%s", err, docsStaleHint)
	}
	if diffs := docsTreeDiff(want, got); len(diffs) > 0 {
		t.Fatalf("%s\n%d file(s) differ:\n  %s", docsStaleHint, len(diffs), strings.Join(diffs, "\n  "))
	}
	if !bytes.Equal(book, wantBook) {
		t.Fatalf("%s\n%s: the docgen:summary span does not match the export's SUMMARY.md", docsStaleHint, docsBookSumm)
	}
}

// docsTree reads every file under dir on fsys, keyed by slash path
// relative to dir.
func docsTree(fsys afero.Fs, dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := afero.Walk(fsys, dir, func(p string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		body, err := afero.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = body
		return nil
	})
	return out, err
}

// docsTreeDiff lists every path missing from got, extra in got, or
// different, sorted.
func docsTreeDiff(want, got map[string][]byte) []string {
	var diffs []string
	for p, w := range want {
		g, ok := got[p]
		switch {
		case !ok:
			diffs = append(diffs, "missing: "+p)
		case !bytes.Equal(w, g):
			diffs = append(diffs, "differs: "+p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			diffs = append(diffs, "extra:   "+p)
		}
	}
	sort.Strings(diffs)
	return diffs
}
