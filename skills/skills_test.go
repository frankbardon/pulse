package skills

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestSkillsList_ReturnsAll(t *testing.T) {
	items := List()
	if len(items) != 18 {
		t.Fatalf("List() returned %d skills, want 18", len(items))
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
			t.Errorf("skill %q listed in index.json but file not found", m.Name)
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

func TestSkillsManifestConsistent(t *testing.T) {
	// Verify index.json is valid JSON
	var raw []json.RawMessage
	if err := json.Unmarshal(indexJSON, &raw); err != nil {
		t.Fatalf("index.json is not valid JSON: %v", err)
	}

	// Valid CLI leaves from the manifest
	validLeaves := map[string]bool{
		"process":  true,
		"compose":  true,
		"sample":   true,
		"facet":    true,
		"inspect":  true,
		"predict":  true,
		"manifest": true,
		"mcp":      true,
	}

	items := List()
	for _, m := range items {
		// Every skill in index.json must exist as a file
		content, ok := Get(m.Name)
		if !ok {
			t.Errorf("skill %q in index.json but %s.md not found", m.Name, m.Name)
			continue
		}

		// Frontmatter name must match
		fm := ParseFrontmatter(content)
		if fm["name"] != m.Name {
			t.Errorf("skill %q: frontmatter name=%q does not match index.json name", m.Name, fm["name"])
		}

		// applies_to must reference valid CLI leaves
		for _, leaf := range m.AppliesTo {
			if !validLeaves[leaf] {
				t.Errorf("skill %q: applies_to contains %q which is not a valid CLI leaf", m.Name, leaf)
			}
		}
	}
}

func TestSkillsNames(t *testing.T) {
	names := Names()
	if len(names) != 18 {
		t.Fatalf("Names() returned %d, want 18", len(names))
	}
	if !slices.Contains(names, "getting-started") {
		t.Error("Names() does not contain 'getting-started'")
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
