package skills

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// embeddedMarkdownNames walks the //go:embed FS in skills.go and returns the
// stem names (without ".md") of every embedded markdown file. Tests use this
// to derive expected counts and membership directly from the filesystem, so
// the assertions stay valid as skills are added or removed without needing
// to bump a hardcoded literal.
func embeddedMarkdownNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(content, ".")
	if err != nil {
		t.Fatalf("read embedded skills dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".md"))
	}
	return names
}

// TestSkillsList_ReturnsAll asserts List() returns one Metadata per embedded
// *.md file. The expected count is derived from the embed walk rather than a
// hardcoded literal so the test does not need to change when skills are added
// or removed.
func TestSkillsList_ReturnsAll(t *testing.T) {
	want := embeddedMarkdownNames(t)
	items := List()
	if len(items) != len(want) {
		t.Fatalf("List() returned %d skills, want %d (one per embedded *.md)", len(items), len(want))
	}
}

func TestSkillsGet_ValidName(t *testing.T) {
	content, ok := Get("getting-started")
	if !ok {
		t.Fatal("Get(\"getting-started\") returned false")
	}
	if len(content) == 0 {
		t.Fatal("Get(\"getting-started\") returned empty content")
	}
	if !strings.HasPrefix(content, "---\n") {
		t.Fatal("getting-started.md does not start with YAML frontmatter")
	}
}

func TestSkillsGet_InvalidName(t *testing.T) {
	_, ok := Get("nonexistent")
	if ok {
		t.Fatal("Get(\"nonexistent\") should return false")
	}
}

func TestSkillsFrontmatter_Valid(t *testing.T) {
	for _, m := range List() {
		content, ok := Get(m.Name)
		if !ok {
			t.Errorf("skill %q listed by List() but file not found", m.Name)
			continue
		}
		fm := ParseFrontmatter(content)
		if len(fm) == 0 {
			t.Errorf("skill %q has no frontmatter", m.Name)
		}
	}
}

func TestSkillsFrontmatter_RequiredFields(t *testing.T) {
	required := []string{"name", "description", "type", "applies_to"}
	for _, m := range List() {
		content, ok := Get(m.Name)
		if !ok {
			t.Errorf("skill %q not found", m.Name)
			continue
		}
		fm := ParseFrontmatter(content)
		for _, key := range required {
			if _, exists := fm[key]; !exists {
				t.Errorf("skill %q missing frontmatter field %q", m.Name, key)
			}
		}
	}
}

// TestSkillsManifestConsistent walks the embedded *.md set directly (no
// index.json) and verifies that every file has a frontmatter `name:` matching
// its filename stem and that `applies_to:` only references valid CLI leaves.
// This is the post-E2-S2 replacement for the legacy index.json consistency
// check — the loader is now the source of truth.
func TestSkillsManifestConsistent(t *testing.T) {
	// Valid CLI leaves from the manifest. Must stay in sync with
	// coverage_test.go's TestSkillsCoverAllCliLeaves leaves list and
	// descriptor/manifest.go commands(). `process-chain` is the ProcessChain
	// leaf (E6-S9) — streaming-and-watching's chain-overlay recipe routes
	// off it.
	validLeaves := map[string]bool{
		"process":       true,
		"process-chain": true,
		"compose":       true,
		"sample":        true,
		"facet":         true,
		"inspect":       true,
		"predict":       true,
		"manifest":      true,
		"mcp":           true,
	}

	stems := embeddedMarkdownNames(t)
	for _, stem := range stems {
		raw, ok := Get(stem)
		if !ok {
			t.Errorf("skill %q embedded but Get returned false", stem)
			continue
		}
		fm := ParseFrontmatter(raw)
		if len(fm) == 0 {
			t.Errorf("skill %q has no frontmatter", stem)
			continue
		}
		if fm["name"] != stem {
			t.Errorf("skill %q: frontmatter name=%q does not match filename stem", stem, fm["name"])
		}
		md, ok := parseMetadata(raw)
		if !ok {
			t.Errorf("skill %q: parseMetadata failed", stem)
			continue
		}
		for _, leaf := range md.AppliesTo {
			if !validLeaves[leaf] {
				t.Errorf("skill %q: applies_to contains %q which is not a valid CLI leaf", stem, leaf)
			}
		}
	}
}

// TestSkillsNames asserts Names() matches the embedded *.md set member-for-
// member. No hardcoded count — the expected slice is derived from the embed
// walk so adding or removing a skill does not require updating the test.
func TestSkillsNames(t *testing.T) {
	want := embeddedMarkdownNames(t)
	slices.Sort(want)
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("Names() returned %d entries, want %d (one per embedded *.md)", len(got), len(want))
	}
	for _, stem := range want {
		if !slices.Contains(got, stem) {
			t.Errorf("Names() missing embedded skill %q", stem)
		}
	}
	if !slices.Contains(got, "getting-started") {
		t.Error("Names() does not contain 'getting-started'")
	}
}

// TestSkillsEveryEmbeddedFileParses asserts every embedded *.md file parses
// through ParseFrontmatter / parseMetadata without error. This is the
// filesystem-scan replacement for the count-based assertions: it guarantees
// the loader contract holds for every file actually shipped.
func TestSkillsEveryEmbeddedFileParses(t *testing.T) {
	for _, stem := range embeddedMarkdownNames(t) {
		raw, ok := Get(stem)
		if !ok {
			t.Errorf("Get(%q) returned false for embedded file", stem)
			continue
		}
		fm := ParseFrontmatter(raw)
		if len(fm) == 0 {
			t.Errorf("skill %q: ParseFrontmatter returned empty map", stem)
			continue
		}
		if _, ok := parseMetadata(raw); !ok {
			t.Errorf("skill %q: parseMetadata returned ok=false", stem)
		}
	}
}

func TestParseFrontmatter_Empty(t *testing.T) {
	fm := ParseFrontmatter("no frontmatter here")
	if len(fm) != 0 {
		t.Errorf("expected empty frontmatter, got %v", fm)
	}
}

func TestParseFrontmatter_Valid(t *testing.T) {
	md := "---\nname: test\ndescription: a test\ntype: guide\napplies_to: process\n---\n\n# Content"
	fm := ParseFrontmatter(md)
	if fm["name"] != "test" {
		t.Errorf("name = %q, want %q", fm["name"], "test")
	}
	if fm["type"] != "guide" {
		t.Errorf("type = %q, want %q", fm["type"], "guide")
	}
}

func TestParseFrontmatter_NoClosingDelimiter(t *testing.T) {
	md := "---\nname: test\nno closing delimiter"
	fm := ParseFrontmatter(md)
	if len(fm) != 0 {
		t.Errorf("expected empty frontmatter for unclosed block, got %v", fm)
	}
}

func TestParseFrontmatter_LineWithoutColon(t *testing.T) {
	md := "---\nname: test\njust a line\ntype: guide\n---\n"
	fm := ParseFrontmatter(md)
	if fm["name"] != "test" {
		t.Errorf("name = %q, want %q", fm["name"], "test")
	}
	if fm["type"] != "guide" {
		t.Errorf("type = %q, want %q", fm["type"], "guide")
	}
	// Line without colon should not produce a key
	if len(fm) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(fm), fm)
	}
}

func TestSkillsList_AllFieldsPopulated(t *testing.T) {
	for _, m := range List() {
		if m.Name == "" {
			t.Error("skill has empty name")
		}
		if m.Description == "" {
			t.Errorf("skill %q has empty description", m.Name)
		}
		if m.Type == "" {
			t.Errorf("skill %q has empty type", m.Name)
		}
		if len(m.AppliesTo) == 0 {
			t.Errorf("skill %q has empty applies_to", m.Name)
		}
	}
}

func TestSkillsGet_AllSkills(t *testing.T) {
	for _, m := range List() {
		content, ok := Get(m.Name)
		if !ok {
			t.Errorf("Get(%q) returned false", m.Name)
			continue
		}
		if len(content) < 100 {
			t.Errorf("skill %q content too short (%d bytes), expected substantive content", m.Name, len(content))
		}
	}
}
