package pulse_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The long-form contract under .claude/reference/ is loaded by agents BEFORE
// work, so its size is a cost paid on every story. A reference that outgrew
// one load is split into an INDEX (`<name>.md`, the file every pointer names)
// plus per-area SLICES in a same-named subdirectory (`<name>/<area>.md`), and
// the agent loads the index plus only the slice its change triggers. A
// `history.md` slice holds superseded wording and is never loaded by default,
// so it carries no budget.
//
// These ceilings are the point of the split: a slice that grows past its
// ceiling is split again, never raised (the same rule as CLAUDE.md's
// TestClaudeMdSizeBudget).
const (
	referenceIndexCeiling = 25 * 1024
	referenceSliceCeiling = 32 * 1024
)

const referenceDir = ".claude/reference"

// referenceSplits returns every split reference: index path -> slice paths.
func referenceSplits(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(referenceDir)
	if err != nil {
		t.Fatalf("reading %s: %v", referenceDir, err)
	}
	out := map[string][]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		index := filepath.Join(referenceDir, e.Name()+".md")
		if _, err := os.Stat(index); err != nil {
			t.Errorf("%s/%s/ has no index %s — a split reference keeps its index at the old path so every pointer still lands", referenceDir, e.Name(), index)
			continue
		}
		slices, err := filepath.Glob(filepath.Join(referenceDir, e.Name(), "*.md"))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(slices)
		out[index] = slices
	}
	return out
}

// TestReferenceDocSplitLayout pins the index + slices shape: every slice is
// named by its index (an unlisted slice is one no agent will find), the
// index stays an index, and every live slice stays loadable on its own.
func TestReferenceDocSplitLayout(t *testing.T) {
	splits := referenceSplits(t)
	for _, name := range []string{"update-demand", "execution-modes", "matrix-and-vectors"} {
		if _, ok := splits[filepath.Join(referenceDir, name+".md")]; !ok {
			t.Errorf("%s/%s.md is no longer split into an index + %s/ slices", referenceDir, name, name)
		}
	}
	for index, slices := range splits {
		raw, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if len(raw) > referenceIndexCeiling {
			t.Errorf("%s is %d bytes, over the %d-byte index ceiling — move detail into a slice", index, len(raw), referenceIndexCeiling)
		}
		if !strings.Contains(body, "How to load") {
			t.Errorf("%s carries no \"How to load\" note telling an agent to load only its slice", index)
		}
		dir := strings.TrimSuffix(filepath.Base(index), ".md")
		if len(slices) == 0 {
			t.Errorf("%s/ holds no slices", dir)
		}
		for _, s := range slices {
			rel := dir + "/" + filepath.Base(s)
			if !strings.Contains(body, rel) {
				t.Errorf("%s does not name its slice %s", index, rel)
			}
			if filepath.Base(s) == "history.md" {
				continue
			}
			info, err := os.Stat(s)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() > referenceSliceCeiling {
				t.Errorf("%s is %d bytes, over the %d-byte slice ceiling — split it into another slice (and name it in the index), do not raise the ceiling", s, info.Size(), referenceSliceCeiling)
			}
		}
	}
}

var (
	// A full repo-relative reference path anywhere in a source file.
	referencePathRe = regexp.MustCompile(`\.claude/reference/[A-Za-z0-9_./-]*[A-Za-z0-9_-]\.md`)
	// A backticked reference file named relative to the reference dir — the
	// form CLAUDE.md and the reference docs use (`byte-layout.md`,
	// `update-demand/gates.md`) — immediately followed by a parenthetical.
	referenceSectionRefRe = regexp.MustCompile("`((?:\\.claude/reference/)?[a-z0-9-]+(?:/[a-z0-9-]+)?\\.md)` ?\\(([^)]*)\\)")
)

// referencePointerSources is every file an agent reads that points into
// .claude/reference: CLAUDE.md, the reference docs themselves, the subagent
// personas, the skill pack and the book source. docs/roadmap is a planning
// record and is not scanned.
func referencePointerSources(t *testing.T) []string {
	t.Helper()
	out := []string{"CLAUDE.md"}
	for _, root := range []string{".claude", skillPackDir, filepath.Join("docs", "src")} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	sort.Strings(out)
	return out
}

// referenceHeadings returns the heading variants a section pointer at ref may
// land on. A split index resolves through itself AND every slice: a pointer
// written against the pre-split file names a heading that now lives in a
// slice, and the index's section map routes the reader there.
func referenceHeadings(t *testing.T, ref string, splits map[string][]string) []string {
	t.Helper()
	files := []string{ref}
	files = append(files, splits[ref]...)
	var out []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range headingRe.FindAllStringSubmatch(string(raw), -1) {
			out = append(out, headingVariants(m[1])...)
		}
		// The reference docs anchor most rules on a bold paragraph or bullet
		// lead ("**Predict and the validators.**", "- **`MAT_PCA` (U24 …).**")
		// and on table rows rather than on headings; both are pointer targets.
		for _, m := range boldLeadRe.FindAllStringSubmatch(string(raw), -1) {
			out = append(out, headingVariants(m[1])...)
		}
		for _, m := range tableRowLeadRe.FindAllStringSubmatch(string(raw), -1) {
			out = append(out, rowAnchorPrefix+normaliseRef(m[1]))
		}
	}
	return out
}

var (
	// A bold lead at the start of a paragraph or list item.
	boldLeadRe = regexp.MustCompile(`(?m)^\s*(?:[-*] |\d+\. )?\*\*([^*\n]+)\*\*`)
	// The first cell of a table row (the trigger column of an Update Demand
	// row), so "(`Request.Return` row)" resolves to the row it names.
	tableRowLeadRe = regexp.MustCompile(`(?m)^\| ([^|\n]+)\|`)
	// Table-row anchors are stored with this marker: a row is named by any
	// identifier its trigger cell carries ("(`Request.Return` row)" names the
	// row whose cell reads "`types.Request.Return` (`return`, …"), so a row
	// pointer matches by containment, and ONLY a pointer that says "row" does.
	rowAnchorPrefix = "\x00row:"
	// A pointer that names the KIND of anchor after its name: "(Multiplicity
	// row)", "(Inference bullet)", "(Predict bullet of the Matrix slot section)".
	anchorKindSuffixRe = regexp.MustCompile(`\s+(?:row|bullet|paragraph|section)\b.*$`)
)

// referenceSectionResolves resolves one parenthetical: whole first (a heading
// may itself carry a comma), then each comma / semicolon separated part.
func referenceSectionResolves(section string, anchors []string) bool {
	try := func(s string) bool {
		norm := normaliseRef(s)
		if norm == "" {
			return true
		}
		if sectionRefResolves(norm, anchors) {
			return true
		}
		if row, ok := strings.CutSuffix(norm, " row"); ok && row != "" {
			for _, a := range anchors {
				if cell, isRow := strings.CutPrefix(a, rowAnchorPrefix); isRow && strings.Contains(cell, row) {
					return true
				}
			}
		}
		stripped := normaliseRef(anchorKindSuffixRe.ReplaceAllString(norm, ""))
		return stripped != "" && stripped != norm && sectionRefResolves(stripped, anchors)
	}
	if try(section) {
		return true
	}
	parts := strings.FieldsFunc(section, func(r rune) bool { return r == ',' || r == ';' })
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if !try(p) {
			return false
		}
	}
	return true
}

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
