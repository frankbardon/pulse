package skills

import "strings"

// Family rules shared by the embedded-pack gates (atomic_test.go) and the
// embedder skill validation at pulse.New (internal/descriptor,
// extension_skills.go): the required `##` section set and the body
// budget per skill family. One table, so a family rule cannot drift
// between the built-in pack and an embedder's skills.

// Body budgets, in bytes of the post-frontmatter body as Get serves it
// (feature-fence markers and generated-section markers stripped).
// chars / 4 ≈ tokens. Every budget is HARD — one byte over fails
// TestSkillTokenBudget for a built-in and refuses an embedder skill at
// pulse.New. The atomic tiers were frozen in U38 from the measured pack
// (.claude/reference/skill-pack.md, Token budget); a body that outgrows
// its tier moves prose into a design skill, never raises the tier.
const (
	OpBudget        = 1800 // op-* not matched by a narrower tier
	OpTestBudget    = 2200 // op-test-*
	OpOverlayBudget = 2400 // op-overlay-*
	OpRegBudget     = 3000 // op-reg-* (op-reg-mod-* included)
	ToolBudget      = 2500
	TypeBudget      = 2000
	DesignBudget    = 6000
)

// BudgetTier is one row of the atomic budget table: a stem prefix and
// the body budget of every skill whose stem starts with it.
type BudgetTier struct {
	Prefix string
	Budget int
}

// bodyBudgetTiers is ordered most specific first: the first prefix a
// stem matches decides its budget (TestBodyBudgetTiers_MostSpecificFirst).
var bodyBudgetTiers = []BudgetTier{
	{"op-test-", OpTestBudget},
	{"op-overlay-", OpOverlayBudget},
	{"op-reg-", OpRegBudget},
	{"op-", OpBudget},
	{"tool-", ToolBudget},
	{"type-", TypeBudget},
}

// BodyBudgetTiers returns a copy of the atomic budget table, most
// specific prefix first. Topical (kind: design) skills are not in it:
// they share DesignBudget.
func BodyBudgetTiers() []BudgetTier {
	return append([]BudgetTier(nil), bodyBudgetTiers...)
}

// RequiredSections returns the `##` headings a skill of the given stem
// and frontmatter category must carry; nil for a family with no required
// set (topical, virtual). The category (`AGG`, `GROUP`, …) is
// accepted for op-* skills (every op family shares one set: the
// registry-rendered `## Contract` carries the component keys, so
// `## Components` is optional prose for per-key meaning only), with the
// stem prefix as the fallback when it is empty.
func RequiredSections(stem, category string) []string {
	switch {
	case strings.HasPrefix(stem, "type-"):
		return []string{"## Bytes", "## Range", "## Null", "## Dictionary", "## See"}
	case strings.HasPrefix(stem, "tool-"):
		return []string{"## When to use", "## Input", "## Output", "## Gotchas", "## See"}
	case strings.HasPrefix(stem, "op-overlay-"):
		return []string{"## Params", "## Host shape", "## Output", "## Gotchas", "## See"}
	case strings.HasPrefix(stem, "op-"):
		return []string{"## Params", "## Inputs", "## Output", "## Gotchas", "## See"}
	}
	return nil
}

// BodyBudget returns the body budget of a skill of the given stem and
// frontmatter kind — the first matching prefix of the atomic tier table,
// else DesignBudget for kind: design; ok is false for a family no budget
// covers.
func BodyBudget(stem, kind string) (budget int, ok bool) {
	for _, tier := range bodyBudgetTiers {
		if strings.HasPrefix(stem, tier.Prefix) {
			return tier.Budget, true
		}
	}
	if strings.TrimSpace(kind) == "design" {
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
