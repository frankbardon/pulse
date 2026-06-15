package skills

import (
	"embed"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed *.md
var content embed.FS

// indexJSON is a deprecated stub kept only so skills_test.go (which still
// references it as part of the legacy index.json checks) compiles during the
// E2-S2 → E2-S3 window. E2-S3 removes the test references; once that lands
// this symbol disappears. Do not use in new code — the loader walks `content`
// directly via List().
//
// Deprecated: removed in E2-S3. See .planning/skill-pack-overhaul/.
var indexJSON = []byte("[]")

// Metadata describes a bundled skill.
type Metadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	AppliesTo   []string `json:"applies_to"`

	// Kind classifies the skill: "operator" | "tool" | "type" | "design".
	Kind string `json:"kind,omitempty"`
	// Category is the operator family: AGG | ATTR | FILTER | GROUP | WIN | FEAT | TEST | REG | OVERLAY | SYNTH; empty when not operator-scoped.
	Category string `json:"category,omitempty"`
	// Operator is the full operator constant (e.g. TEST_WELCH); empty when not operator-scoped.
	Operator string `json:"operator,omitempty"`
	// Covers lists operators/tools/types covered by a design skill.
	Covers []string `json:"covers,omitempty"`
	// ExamplesTags lists the runnable-example tags referenced by a "## See" section in an atomic skill.
	ExamplesTags []string `json:"examples_tags,omitempty"`
}

// List walks the embedded content for *.md files, parses their YAML
// frontmatter, and returns the resulting Metadata slice sorted by Name.
//
// Frontmatter-less files are skipped silently. A frontmatter-less file in
// skills/ would be a code-review smell, but the loader is the wrong place to
// crash on it — coverage gates (TestSkillsCoverAllComponents and friends) and
// reviewers will catch a missing skill. Non-`.md` entries are ignored.
func List() []Metadata {
	entries, err := fs.ReadDir(content, ".")
	if err != nil {
		// content is a //go:embed FS rooted at the package directory; ReadDir
		// against "." cannot meaningfully fail at runtime. Treat any error as
		// a corrupt embed and surface it loudly.
		panic("skills: cannot read embedded content: " + err.Error())
	}
	out := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		data, err := fs.ReadFile(content, name)
		if err != nil {
			continue
		}
		md, ok := parseMetadata(string(data))
		if !ok {
			continue
		}
		// Default Name to the file stem when frontmatter omits it. The
		// existing skill set always sets `name:`, but this keeps the loader
		// robust against a missing key for additive future skills.
		if md.Name == "" {
			md.Name = strings.TrimSuffix(name, ".md")
		}
		out = append(out, md)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the markdown content for the named skill.
// The name should not include the .md extension.
// Returns the content and true if found, or empty string and false otherwise.
func Get(name string) (string, bool) {
	data, err := fs.ReadFile(content, name+".md")
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Names returns the sorted list of skill names.
func Names() []string {
	items := List()
	out := make([]string, len(items))
	for i, m := range items {
		out[i] = m.Name
	}
	return out
}

// ParseFrontmatter extracts YAML frontmatter fields from markdown content.
// It returns key-value pairs from the --- delimited header.
//
// List-valued fields (applies_to, covers, examples_tags) are stored under
// their key as the raw post-colon string — callers that need a parsed slice
// should use parseList or go through Metadata via parseMetadata.
func ParseFrontmatter(md string) map[string]string {
	result := make(map[string]string)
	if !strings.HasPrefix(md, "---\n") {
		return result
	}
	end := strings.Index(md[4:], "\n---")
	if end < 0 {
		return result
	}
	block := md[4 : 4+end]
	for _, line := range strings.Split(block, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			result[key] = val
		}
	}
	return result
}

// parseMetadata extracts the typed Metadata struct from a markdown file's
// YAML frontmatter. Returns (zero, false) when the file has no frontmatter
// block.
func parseMetadata(md string) (Metadata, bool) {
	fm := ParseFrontmatter(md)
	if len(fm) == 0 {
		return Metadata{}, false
	}
	return Metadata{
		Name:         fm["name"],
		Description:  fm["description"],
		Type:         fm["type"],
		AppliesTo:    parseList(fm["applies_to"]),
		Kind:         fm["kind"],
		Category:     fm["category"],
		Operator:     fm["operator"],
		Covers:       parseList(fm["covers"]),
		ExamplesTags: parseList(fm["examples_tags"]),
	}, true
}

// parseList parses a YAML list value that ParseFrontmatter captured as a
// single trimmed string. Both supported forms reduce to the same slice:
//
//   - bracket inline: `["a", "b", "c"]`
//   - comma-separated CSV: `a, b, c`
//
// Empty input returns nil. Quoted entries have surrounding single/double
// quotes stripped. Empty fragments (e.g. trailing comma) are dropped.
func parseList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = raw[1 : len(raw)-1]
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`)
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
