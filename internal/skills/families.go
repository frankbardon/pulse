package skills

import "strings"

// Family rules shared by the embedded-pack gates (atomic_test.go) and the
// embedder skill validation at pulse.New (internal/descriptor,
// extension_skills.go): the required `##` section set and the body
// budget per skill family. One table, so a family rule cannot drift
// between the built-in pack and an embedder's skills.

// Body budgets, in bytes of the post-frontmatter body as Get serves it
// (feature-fence markers stripped). chars / 4 ≈ tokens.
const (
	OpBudget     = 1200
	ToolBudget   = 2000
	TypeBudget   = 2000
	DesignBudget = 6000
)

// RequiredSections returns the `##` headings a skill of the given stem
// and frontmatter category must carry; nil for a family with no required
// set (topical, virtual). The category (`AGG`, `GROUP`, …) is
// load-bearing for op-* skills — AGG / GROUP / FILTER also carry
// `## Components` — with the stem prefix as the fallback when it is
// empty.
func RequiredSections(stem, category string) []string {
	switch {
	case strings.HasPrefix(stem, "type-"):
		return []string{"## Bytes", "## Range", "## Null", "## Dictionary", "## See"}
	case strings.HasPrefix(stem, "tool-"):
		return []string{"## When to use", "## Input", "## Output", "## Gotchas", "## See"}
	case strings.HasPrefix(stem, "op-overlay-"):
		return []string{"## Params", "## Host shape", "## Output", "## Gotchas", "## See"}
	case strings.HasPrefix(stem, "op-"):
		required := []string{"## Params", "## Inputs", "## Output", "## Gotchas", "## See"}
		cat := strings.ToUpper(strings.TrimSpace(category))
		if cat == "" {
			switch {
			case strings.HasPrefix(stem, "op-agg-"):
				cat = "AGG"
			case strings.HasPrefix(stem, "op-group-"):
				cat = "GROUP"
			case strings.HasPrefix(stem, "op-filter-"):
				cat = "FILTER"
			}
		}
		if cat == "AGG" || cat == "GROUP" || cat == "FILTER" {
			required = append(required, "## Components")
		}
		return required
	}
	return nil
}

// BodyBudget returns the body budget of a skill of the given stem and
// frontmatter kind; ok is false for a family no budget covers.
func BodyBudget(stem, kind string) (budget int, ok bool) {
	switch {
	case strings.HasPrefix(stem, "op-"):
		return OpBudget, true
	case strings.HasPrefix(stem, "tool-"):
		return ToolBudget, true
	case strings.HasPrefix(stem, "type-"):
		return TypeBudget, true
	case strings.TrimSpace(kind) == "design":
		return DesignBudget, true
	}
	return 0, false
}

// HasHeading reports whether md carries heading (`## Foo`) on a line of
// its own (trailing whitespace allowed). A substring inside a paragraph
// does not count.
func HasHeading(md, heading string) bool {
	target := strings.TrimSpace(heading)
	for _, line := range strings.Split(md, "\n") {
		if strings.TrimRight(line, " \t") == target {
			return true
		}
	}
	return false
}

// StripFrontmatter returns md without its leading `---\n…\n---\n` block;
// md unchanged when it does not begin with one. The budget gates measure
// what it returns.
func StripFrontmatter(md string) string {
	if !strings.HasPrefix(md, "---\n") {
		return md
	}
	end := strings.Index(md[4:], "\n---")
	if end < 0 {
		return md
	}
	rest := md[4+end+4:]
	return strings.TrimPrefix(rest, "\n")
}

// ParseMetadata parses md's frontmatter into Metadata exactly as List
// does for an embedded file; ok is false when md has no frontmatter.
func ParseMetadata(md string) (Metadata, bool) { return parseMetadata(md) }

// OperatorStem is the atomic skill stem of an extension operator:
// `op-` + the name lowercased with `_` → `-` (AGG_ACME_TRIM →
// op-agg-acme-trim). Built-in SCREAMING_SNAKE operators follow the same
// rule; built-in synth distributions and the REG spec modifiers are
// spelled by TestOperatorHasAtomicSkill.
func OperatorStem(name string) string {
	return "op-" + strings.ReplaceAll(strings.ToLower(name), "_", "-")
}
