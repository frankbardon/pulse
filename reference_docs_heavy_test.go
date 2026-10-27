//go:build heavy

package pulse_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferenceDocPointersResolve is the dangling-pointer gate for the
// reference docs. (a) Every `.claude/reference/<path>.md` named in an agent-
// read file or in Go source exists. (b) A section-qualified pointer
// `<ref>.md` (Section) at a reference file names a heading that file — or,
// for a split index, one of its slices — carries, under the same
// sentence-case discriminator and prefix rule as
// TestSkillsCoverAllCrossReferences' shape (b). An anchor is a heading, a
// bold paragraph / bullet lead, or a table row's first cell; a parenthetical
// naming several anchors ("(Date-family field types, Time zones)") must
// resolve every one.
func TestReferenceDocPointersResolve(t *testing.T) {
	splits := referenceSplits(t)
	sources := referencePointerSources(t)

	// (a) paths, from markdown and Go source alike.
	goFiles := []string{}
	if err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".planning" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") {
			goFiles = append(goFiles, p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, src := range append(append([]string{}, sources...), goFiles...) {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range referencePathRe.FindAllString(string(raw), -1) {
			checked++
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s: dangling reference path %s", src, p)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no .claude/reference paths at all — the scan is broken")
	}

	// (b) section pointers, markdown only.
	sections := 0
	for _, src := range sources {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, loc := range referenceSectionRefRe.FindAllStringSubmatchIndex(text, -1) {
			rel := strings.TrimPrefix(text[loc[2]:loc[3]], ".claude/reference/")
			ref := filepath.Join(referenceDir, filepath.FromSlash(rel))
			if _, err := os.Stat(ref); err != nil {
				continue // not a reference doc (a skill, a docs/ page): other gates own those.
			}
			section := text[loc[4]:loc[5]]
			first := strings.TrimLeft(section, `"`+"`"+" “")
			if first == "" || first[0] < 'A' || first[0] > 'Z' {
				continue // prose annotation, not a section name.
			}
			sections++
			if !referenceSectionResolves(section, referenceHeadings(t, ref, splits)) {
				t.Errorf("%s:%d: section pointer `%s` (%s) names no heading in %s or its slices",
					src, 1+strings.Count(text[:loc[0]], "\n"), text[loc[2]:loc[3]], section, ref)
			}
		}
	}
	if sections == 0 {
		t.Fatal("found no section-qualified reference pointers — the scan is broken")
	}
}
