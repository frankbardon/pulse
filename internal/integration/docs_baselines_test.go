package integration_test

import (
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/doccheck"
)

// TestDocsBaselinesEmpty locks the documentation checks at zero known
// violations: every allowlist under internal/doccheck/allowlists/ holds
// header (#) lines only. A doc check that finds a problem fails, and the
// fix is to the docs, never a new allowlist line. The lists stay as files
// because each check still reads its own (doccheck.Load); a new check
// commits an empty one.
func TestDocsBaselinesEmpty(t *testing.T) {
	t.Parallel()

	problems, n, err := baselineEntries(afero.NewOsFs())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("no allowlist under %s — the walk is broken", doccheck.AllowlistDir)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestDocsBaselinesEmptyRefusesAnEntry pins the rule on a fixture: one
// entry in one list is one problem naming the file and the entry; a list
// of header lines only is none, and so is a non-.txt file.
func TestDocsBaselinesEmptyRefusesAnEntry(t *testing.T) {
	t.Parallel()

	mem := afero.NewMemMapFs()
	write := func(name, body string) {
		if err := afero.WriteFile(mem, path.Join(doccheck.AllowlistDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("clean.txt", "# header\n# Format: path<TAB>entry<TAB>reason.\n\n")
	write("dirty.txt", "# header\ndocs/src/x.md\tbroken.md\tbaseline\n")
	write("README", "not an allowlist\n")

	problems, n, err := baselineEntries(mem)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("read %d allowlists, want the two .txt files", n)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "dirty.txt") || !strings.Contains(problems[0], "broken.md") {
		t.Fatalf("problems = %q, want one naming dirty.txt and its entry", problems)
	}
}

// baselineEntries reads every .txt allowlist under doccheck.AllowlistDir
// and returns one problem per entry, plus how many lists it read.
func baselineEntries(fsys afero.Fs) ([]string, int, error) {
	infos, err := afero.ReadDir(fsys, doccheck.AllowlistDir)
	if err != nil {
		return nil, 0, err
	}
	var problems []string
	n := 0
	for _, fi := range infos {
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".txt") {
			continue
		}
		n++
		file := path.Join(doccheck.AllowlistDir, fi.Name())
		a, err := doccheck.Load(fsys, file)
		if err != nil {
			return nil, 0, err
		}
		for _, e := range a.Entries() {
			problems = append(problems, fmt.Sprintf("%s:%d: %s\t%s — the doc-check allowlists are locked empty: fix the violation in the docs and delete this line, never baseline it", file, e.Line, e.Path, e.Entry))
		}
	}
	return problems, n, nil
}
