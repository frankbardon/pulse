package skills

import (
	"embed"
	"encoding/json"
	"io/fs"
	"strings"
)

//go:embed *.md
var content embed.FS

//go:embed index.json
var indexJSON []byte

// Metadata describes a bundled skill.
type Metadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	AppliesTo   []string `json:"applies_to"`
}

// List returns all skills defined in index.json.
func List() []Metadata {
	var out []Metadata
	if err := json.Unmarshal(indexJSON, &out); err != nil {
		panic("skills: corrupt index.json: " + err.Error())
	}
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
