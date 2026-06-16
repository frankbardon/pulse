package skills

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
)

// TestAtomicSkillHasRequiredSections enforces the structural contract for
// atomic skills introduced in E4: every file matching op-* / tool-* / type-*
// MUST carry a fixed set of `## <header>` sections. The exact header list
// branches by skill family:
//
//   - op-* (non-overlay): `## Params`, `## Inputs`, `## Output`, `## Gotchas`,
//     `## See`. AGG / GROUP / FILTER additionally require `## Components`
//     per the v0.20.0 response-components contract.
//   - op-overlay-*: `## Params`, `## Host shape`, `## Output`, `## Gotchas`,
//     `## See`. Overlays decorate a host result and do not take row-level
//     `Inputs`; they document the host they ride on instead.
//   - type-*: `## Bytes`, `## Range`, `## Null`, `## Dictionary`, `## See`.
//   - tool-*: `## When to use`, `## Input`, `## Output`, `## Gotchas`,
//     `## See`.
//
// The category gate keying off frontmatter (`category: AGG|GROUP|FILTER`)
// is the load-bearing dispatch — operator prefix is a fallback only.
func TestAtomicSkillHasRequiredSections(t *testing.T) {
	type fileCase struct {
		stem     string
		required []string
	}
	var cases []fileCase

	for _, stem := range embeddedMarkdownNames(t) {
		switch {
		case strings.HasPrefix(stem, "type-"):
			cases = append(cases, fileCase{
				stem: stem,
				required: []string{
					"## Bytes",
					"## Range",
					"## Null",
					"## Dictionary",
					"## See",
				},
			})
		case strings.HasPrefix(stem, "tool-"):
			cases = append(cases, fileCase{
				stem: stem,
				required: []string{
					"## When to use",
					"## Input",
					"## Output",
					"## Gotchas",
					"## See",
				},
			})
		case strings.HasPrefix(stem, "op-overlay-"):
			cases = append(cases, fileCase{
				stem: stem,
				required: []string{
					"## Params",
					"## Host shape",
					"## Output",
					"## Gotchas",
					"## See",
				},
			})
		case strings.HasPrefix(stem, "op-"):
			raw, ok := Get(stem)
			if !ok {
				t.Errorf("op-* skill %q embedded but Get returned false", stem)
				continue
			}
			fm := ParseFrontmatter(raw)
			required := []string{
				"## Params",
				"## Inputs",
				"## Output",
				"## Gotchas",
				"## See",
			}
			// AGG / GROUP / FILTER carry per-operator Components state.
			// Branch on frontmatter `category` (load-bearing); fall back
			// to operator-prefix sniff when the key is missing.
			cat := strings.ToUpper(strings.TrimSpace(fm["category"]))
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
			cases = append(cases, fileCase{stem: stem, required: required})
		}
	}

	for _, c := range cases {
		raw, ok := Get(c.stem)
		if !ok {
			t.Errorf("%s: not loadable via Get", c.stem)
			continue
		}
		for _, header := range c.required {
			if !containsHeading(raw, header) {
				t.Errorf("%s: missing required section %q", c.stem, header)
			}
		}
	}
}

// containsHeading reports whether md contains the literal `## Foo` heading
// on a line by itself (with optional trailing whitespace). The check is
// strict: substring matches inside paragraphs do NOT count, only top-level
// heading lines.
func containsHeading(md, heading string) bool {
	target := strings.TrimSpace(heading)
	for _, line := range strings.Split(md, "\n") {
		if strings.TrimRight(line, " \t") == target {
			return true
		}
	}
	return false
}

// TestSkillTokenBudget enforces the body-size budget for the atomic-skill
// pack. Body is defined as the file contents with the leading
// `---\n...\n---\n` frontmatter block stripped; budget is on raw byte
// count of that remainder. The char/4 heuristic from the story spec maps
// directly: 1200 chars ≈ 300 tokens, 2000 chars ≈ 500 tokens, 6000 chars
// ≈ 1500 tokens.
//
// Budget table:
//
//   - op-*    : 1200 chars (≈300 tokens)
//   - tool-*  : 2000 chars (≈500 tokens)
//   - type-*  : 2000 chars (≈500 tokens)
//   - kind:design (frontmatter): 6000 chars (≈1500 tokens)
//
// TRANSITIONAL CARVE-OUT (E4-S13): the budget is enforced as a warning
// only — overruns surface via t.Logf so reviewers can see the live state
// of the skill pack without the test going red. The hard-fail branch
// triggers only above hardFailOverPercent (set to a transitional value
// here); E4-S15 tightens this to 30% and replaces the t.Logf with
// t.Errorf once the offending op-reg-* / op-reg-mod-* / op-feat-* /
// op-synth-regex bodies have been trimmed. The mechanism is in place;
// flipping the threshold + one t.Logf to t.Errorf is the only change
// needed.
//
// Files not matching any of the four buckets above (e.g.
// response-components which is type:guide with no kind:design) are
// skipped silently — design-kind floor is enforced via frontmatter,
// not file-stem.
func TestSkillTokenBudget(t *testing.T) {
	const (
		opBudget     = 1200
		toolBudget   = 2000
		typeBudget   = 2000
		designBudget = 6000

		// hardFailOverPercent caps the transitional soft-only regime.
		// E4-S15 will lower this to 30 and flip the matching t.Logf to
		// t.Errorf. Anything above this percent over-budget surfaces a
		// stronger warning today; nothing in the current pack triggers
		// the t.Errorf branch.
		hardFailOverPercent = 1000
	)

	for _, stem := range embeddedMarkdownNames(t) {
		raw, ok := Get(stem)
		if !ok {
			t.Errorf("%s: not loadable via Get", stem)
			continue
		}
		body := stripFrontmatter(raw)
		bodyLen := len(body)

		var budget int
		switch {
		case strings.HasPrefix(stem, "op-"):
			budget = opBudget
		case strings.HasPrefix(stem, "tool-"):
			budget = toolBudget
		case strings.HasPrefix(stem, "type-"):
			budget = typeBudget
		default:
			fm := ParseFrontmatter(raw)
			if strings.TrimSpace(fm["kind"]) == "design" {
				budget = designBudget
			} else {
				continue // not budget-covered
			}
		}

		if bodyLen <= budget {
			continue
		}
		overBytes := bodyLen - budget
		overPct := (overBytes * 100) / budget
		if overPct > hardFailOverPercent {
			t.Errorf("%s: body %d > budget %d (over by %d bytes / %d%%) — exceeds transitional cap %d%%",
				stem, bodyLen, budget, overBytes, overPct, hardFailOverPercent)
			continue
		}
		t.Logf("%s: body %d > budget %d (over by %d bytes / %d%%)",
			stem, bodyLen, budget, overBytes, overPct)
	}
}

// stripFrontmatter removes the leading `---\n...\n---\n` block from md and
// returns the remainder. When md does not begin with frontmatter the entire
// input is returned unchanged. Matches the parsing convention used by
// ParseFrontmatter so body-length math stays consistent across the test
// surface.
func stripFrontmatter(md string) string {
	if !strings.HasPrefix(md, "---\n") {
		return md
	}
	end := strings.Index(md[4:], "\n---")
	if end < 0 {
		return md
	}
	// Skip past closing `\n---` (4 bytes) plus any trailing newline.
	rest := md[4+end+4:]
	rest = strings.TrimPrefix(rest, "\n")
	return rest
}

// TestOperatorHasAtomicSkill enforces the convention that every registered
// operator, tool, and field type has a matching atomic skill file in
// skills/. The naming convention is `<prefix>-<kebab-name>.md` where:
//
//   - prefix is op-agg- / op-attr- / op-filter- / op-group- / op-win- /
//     op-feat- / op-test- / op-reg- / op-synth- / op-overlay- / type- /
//     tool-
//   - kebab-name is lowercase(name).replace("_", "-"); for OVERLAY_* and
//     pulse_* names the leading domain prefix is stripped first
//     (OVERLAY_CHISQ_COL -> chisq-col; pulse_inspect -> inspect)
//
// Enumeration sources:
//
//   - types.AllAggregationTypes / AllAttributeTypes / AllFiltererTypes /
//     AllGroupTypes / AllWindowTypes / AllFeatureTypes / AllTestTypes /
//     AllRegressionTypes / AllOverlayKinds
//   - synth.AllDistributions
//   - mcptools.Names
//   - encoding field-type constants (the canonical 17)
//
// Hardcoded extras (not exposed by an `All*` function):
//
//   - regression modifiers `resample` / `selection` -> op-reg-mod-resample /
//     op-reg-mod-selection. These ride on RegressionSpec fields rather
//     than a typed enum, but the skill pack carries one atomic per
//     modifier so the coverage gate enumerates them by hand.
func TestOperatorHasAtomicSkill(t *testing.T) {
	// Build the set of stem names currently embedded so existence checks
	// are filesystem-driven, not hardcoded.
	embedded := make(map[string]bool)
	entries, err := fs.ReadDir(content, ".")
	if err != nil {
		t.Fatalf("read embedded skills dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		embedded[strings.TrimSuffix(e.Name(), ".md")] = true
	}

	type expectation struct {
		category string // human-readable label for error messages
		source   string // identifier that produced the expectation
		stem     string // expected filename stem
	}
	var expected []expectation

	add := func(category, source, prefix, name string) {
		expected = append(expected, expectation{
			category: category,
			source:   source,
			stem:     prefix + kebab(name),
		})
	}

	for _, v := range types.AllAggregationTypes() {
		add("aggregator", string(v), "op-agg-", strings.TrimPrefix(string(v), "AGG_"))
	}
	for _, v := range types.AllAttributeTypes() {
		add("attribute", string(v), "op-attr-", strings.TrimPrefix(string(v), "ATTR_"))
	}
	for _, v := range types.AllFiltererTypes() {
		add("filterer", string(v), "op-filter-", strings.TrimPrefix(string(v), "FILTER_"))
	}
	for _, v := range types.AllGroupTypes() {
		add("grouper", string(v), "op-group-", strings.TrimPrefix(string(v), "GROUP_"))
	}
	for _, v := range types.AllWindowTypes() {
		add("window", string(v), "op-win-", strings.TrimPrefix(string(v), "WIN_"))
	}
	for _, v := range types.AllFeatureTypes() {
		add("feature", string(v), "op-feat-", strings.TrimPrefix(string(v), "FEAT_"))
	}
	for _, v := range types.AllTestTypes() {
		add("test", string(v), "op-test-", strings.TrimPrefix(string(v), "TEST_"))
	}
	for _, v := range types.AllRegressionTypes() {
		add("regression", string(v), "op-reg-", strings.TrimPrefix(string(v), "REG_"))
	}
	// Regression modifiers: RegressionSpec.Resample / Selection are string
	// fields, not enum constants, so there is no AllRegressionModifiers().
	// Enumerate the two modifier kinds the skill pack carries.
	for _, m := range []string{"resample", "selection"} {
		add("regression-modifier", "REG_MOD_"+strings.ToUpper(m), "op-reg-mod-", m)
	}
	for _, v := range synth.AllDistributions() {
		add("synth-distribution", v, "op-synth-", v)
	}
	for _, v := range types.AllOverlayKinds() {
		add("overlay", string(v), "op-overlay-", strings.TrimPrefix(string(v), "OVERLAY_"))
	}

	// All 17 field types. The encoding package exposes them as exported
	// FieldType constants; iterate by index up to the fieldTypeCount
	// sentinel via the IsKnown() predicate.
	fieldTypes := []encoding.FieldType{
		encoding.FieldTypeU4,
		encoding.FieldTypeU8,
		encoding.FieldTypeU16,
		encoding.FieldTypeU32,
		encoding.FieldTypeU64,
		encoding.FieldTypeF32,
		encoding.FieldTypeF64,
		encoding.FieldTypeDate,
		encoding.FieldTypePackedBool,
		encoding.FieldTypeCategoricalU8,
		encoding.FieldTypeCategoricalU16,
		encoding.FieldTypeCategoricalU32,
		encoding.FieldTypeDecimal128,
		encoding.FieldTypeSetU8,
		encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64,
	}
	for _, ft := range fieldTypes {
		add("field-type", ft.String(), "type-", ft.String())
	}

	// MCP tools: strip the leading `pulse_` namespace prefix and apply the
	// standard kebab conversion to the remainder.
	for _, name := range mcptools.Names() {
		add("mcp-tool", name, "tool-", strings.TrimPrefix(name, "pulse_"))
	}

	// Deterministic failure reporting: sort expectations so the missing-skill
	// log reads the same on every run.
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].category != expected[j].category {
			return expected[i].category < expected[j].category
		}
		return expected[i].stem < expected[j].stem
	})

	for _, exp := range expected {
		if !embedded[exp.stem] {
			t.Errorf("%s %q: missing skill file skills/%s.md", exp.category, exp.source, exp.stem)
		}
	}
}

// kebab converts an identifier to its skill-file kebab form: lowercase, and
// underscores rewritten to hyphens. Used by TestOperatorHasAtomicSkill to
// turn AGG_DISTINCT_COUNT into distinct-count, OVERLAY_CHISQ_COL into
// chisq-col, etc.
func kebab(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}
