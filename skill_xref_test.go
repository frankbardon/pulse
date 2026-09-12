package pulse_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/skills"
)

// TestSkillsCoverAllCrossReferences is a non-skippable CI gate over the two
// shapes of phantom cross-reference this repo has repeatedly shipped by hand:
//
//	(a) a backticked SKILL STEM that skills.Get() answers ok=false for
//	    — e.g. `extension-points`, `error-code-reference`, `aggregation-guide`,
//	    eight distinct stems across 25 sites, every one found by accident;
//	(b) a SECTION-QUALIFIED reference `skills/<stem>.md` (Some Section)
//	    naming a heading the target file does not carry.
//
// TestSkillsCoverAllCliLeaves already gates the same class for CLI leaf names:
// prose that names a surface which does not exist is a lie an agent will act
// on. Cross-references had no such gate and the class kept recurring.
//
// The gate lives in the root package because shape (b) reads CLAUDE.md and
// .claude/reference/*.md, the established pattern of TestClaudeMdMentionsAllEnvVars
// and friends; shape (a) resolves through the skills package's embedded FS, so it
// asserts what skills.Get() actually answers rather than what is on disk.
func TestSkillsCoverAllCrossReferences(t *testing.T) {
	t.Run("SkillStems", func(t *testing.T) { checkSkillStemRefs(t) })
	t.Run("MarkdownPaths", func(t *testing.T) { checkSkillMarkdownPathRefs(t) })
	t.Run("Sections", func(t *testing.T) { checkSectionQualifiedRefs(t) })
}

// nonSkillKebabTokens is the allowlist for shape (a): backticked tokens that
// are SHAPED like a skill stem (lowercase, hyphen-containing, no spaces, no
// dots) but are deliberately not skill names.
//
// The pattern is intentionally NOT narrowed to stop these matching — a
// hand-audit that narrowed its candidate list missed three live phantoms. Every
// kebab-shaped backticked token must therefore be either a resolvable skill or
// listed here, so admitting a new non-skill token is a deliberate one-line act
// rather than a silent hole.
var nonSkillKebabTokens = map[string]string{
	"n-1":                 "statistics: the sample-variance denominator",
	"1e-6":                "float tolerance literal",
	"expr-lang":           "the github.com/expr-lang/expr dependency",
	"windows-1252":        "a charset name (SPSS charset folding)",
	"time-series":         "an examples/ taxonomy tag",
	"tier-1-test":         "an examples/ taxonomy tag",
	"experiment-analysis": "an examples/ taxonomy tag",
	"from-schema":         "a CLI subcommand leaf (`pulse synth from-schema`)",
}

var (
	// A backticked span. Skill prose never puts a newline inside one.
	backtickSpanRe = regexp.MustCompile("`([^`\n]+)`")
	// Shaped like a skill stem: lowercase alnum words joined by hyphens. A
	// flag (`--fit-shape`) cannot match — it does not start with alnum — and
	// neither can a wire key (`n_null`, underscore) or a qualified identifier
	// (`Response.Components`, dot + uppercase).
	kebabStemRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)
	// `skills/<stem>.md` and the bare `<stem>.md` form.
	skillPathRe     = regexp.MustCompile(`^skills/([a-z0-9-]+)\.md$`)
	bareSkillPathRe = regexp.MustCompile(`^([a-z0-9]+(?:-[a-z0-9]+)+)\.md$`)
	// Any backticked repo-relative markdown path inside the skill pack.
	mdPathRe = regexp.MustCompile(`^((?:skills|docs|\.claude)/[A-Za-z0-9_./-]+\.md)$`)
	// Heading at level >= 2 (the pack mixes ## and ###; see checkSectionQualifiedRefs).
	headingRe = regexp.MustCompile(`(?m)^#{2,6}\s+(.+)$`)
	// A section-qualified reference: a backticked markdown path immediately
	// followed by a parenthetical.
	sectionRefRe = regexp.MustCompile("`((?:skills/)?[a-z0-9-]+\\.md)` ?\\(([^)]*)\\)")
)

// skillPackFiles returns every skills/*.md filename. It reads the directory
// rather than skills.List() so that a frontmatter-less file is still scanned.
func skillPackFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("skills")
	if err != nil {
		t.Fatalf("reading skills/: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		out = append(out, e.Name())
	}
	if len(out) == 0 {
		t.Fatal("found no skills/*.md files")
	}
	sort.Strings(out)
	return out
}

// stripFrontmatter drops the leading --- delimited YAML block. Frontmatter keys
// are gated by TestAtomicSkillHasRequiredSections and friends; scanning it here
// would pick up examples_tags values that are not cross-references.
func stripFrontmatter(md string) string {
	if !strings.HasPrefix(md, "---\n") {
		return md
	}
	end := strings.Index(md[4:], "\n---")
	if end < 0 {
		return md
	}
	return md[4+end+len("\n---"):]
}

// checkSkillStemRefs is shape (a): every kebab-shaped backticked token in the
// skill pack body must resolve through skills.Get or be allowlisted.
func checkSkillStemRefs(t *testing.T) {
	for _, name := range skillPackFiles(t) {
		data, err := os.ReadFile(filepath.Join("skills", name))
		if err != nil {
			t.Fatalf("reading skills/%s: %v", name, err)
		}
		body := stripFrontmatter(string(data))
		for _, m := range backtickSpanRe.FindAllStringSubmatch(body, -1) {
			tok := m[1]
			stem := ""
			switch {
			case kebabStemRe.MatchString(tok):
				stem = tok
			case skillPathRe.MatchString(tok):
				stem = skillPathRe.FindStringSubmatch(tok)[1]
			case bareSkillPathRe.MatchString(tok):
				stem = bareSkillPathRe.FindStringSubmatch(tok)[1]
			default:
				continue
			}
			if _, ok := skills.Get(stem); ok {
				continue
			}
			if _, ok := nonSkillKebabTokens[stem]; ok {
				continue
			}
			t.Errorf("skills/%s: phantom skill reference `%s` — skills.Get(%q) answers ok=false. "+
				"Re-point it at a skill that exists (see skills.Names()), or add it to "+
				"nonSkillKebabTokens in skill_xref_test.go with the reason it is not a skill name.",
				name, tok, stem)
		}
	}
}

// checkSkillMarkdownPathRefs asserts every backticked skills/ docs/ or .claude/
// markdown PATH named in the skill pack exists on disk. Breadth: the same drift
// that repoints a stem repoints a path.
func checkSkillMarkdownPathRefs(t *testing.T) {
	for _, name := range skillPackFiles(t) {
		data, err := os.ReadFile(filepath.Join("skills", name))
		if err != nil {
			t.Fatalf("reading skills/%s: %v", name, err)
		}
		body := stripFrontmatter(string(data))
		for _, m := range backtickSpanRe.FindAllStringSubmatch(body, -1) {
			path := m[1]
			if !mdPathRe.MatchString(path) {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("skills/%s: phantom markdown path `%s` — no such file in the repo. "+
					"Re-point it at a file that exists.", name, path)
			}
		}
	}
}

// sectionRefSources returns the files scanned for shape (b): CLAUDE.md, every
// long-form reference doc, and the skill pack itself.
func sectionRefSources(t *testing.T) []string {
	t.Helper()
	out := []string{"CLAUDE.md"}
	refs, err := filepath.Glob(filepath.Join(".claude", "reference", "*.md"))
	if err != nil {
		t.Fatalf("globbing .claude/reference: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("found no .claude/reference/*.md files")
	}
	sort.Strings(refs)
	out = append(out, refs...)
	for _, name := range skillPackFiles(t) {
		out = append(out, filepath.Join("skills", name))
	}
	return out
}

// normaliseRef lowercases and strips the decoration a heading and a reference
// spell differently: backticks, straight quotes, whitespace runs, trailing
// sentence punctuation.
func normaliseRef(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("`", "", `"`, "", "“", "", "”", "").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " .:,;")
}

// headingVariants returns the matchable forms of one heading. The pack writes
// headings that carry their own qualifier — "Model recovery (`models`)",
// "Writing `.sav` — `pulse export spss`" — while a reference names the stable
// part, so the trailing parenthetical and the post-em-dash tail are each
// stripped to produce a shorter variant.
func headingVariants(heading string) []string {
	base := normaliseRef(heading)
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.Trim(v, " .:,;-—")
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(base)
	// Strip a trailing "(...)" qualifier.
	if i := strings.LastIndex(base, "("); i > 0 {
		add(base[:i])
	}
	// Strip an em-dash / arrow tail.
	for _, sep := range []string{" — ", " -- ", " → ", " - "} {
		if i := strings.Index(base, sep); i > 0 {
			add(base[:i])
		}
	}
	return out
}

// checkSectionQualifiedRefs is shape (b).
//
// Target set: SKILL files only. The skill pack is the embedded LLM surface and
// the one target set whose headings this gate can resolve authoritatively;
// docs/src/*.md section references use a different convention (numbered "§4"
// pointers) and are left to a follow-up.
//
// A parenthetical is treated as a SECTION reference — rather than the prose
// annotation the Update Demand tables also put after a path — when its first
// character is an uppercase ASCII letter (quotes stripped). Every real
// section-qualified reference in this repo names a sentence-case heading; every
// prose annotation begins lowercase ("the canonical Components contract
// topical", "step 7 Compose return shape", "+ a per-format topical skill").
//
// MATCHING RULE, and why this strength: a reference resolves when some heading
// variant of the target is a PREFIX of the reference, or the reference is a
// prefix of some heading variant. Strict equality is too strong — the corrected
// form of a real reference is "(Detecting rules from the data — the co-missing
// detector row)", which DESCRIBES a table row inside the heading "Detecting
// rules from the data (`--suggest-rules`)" — and "(Sharded)" legitimately names
// "## Sharded cohorts" from the other direction. Free substring matching is too
// weak: it would let "(Compose-host fold)" pass on any heading containing the
// word "fold". Prefix in either direction keeps the anchor at the start of the
// heading, which is the part a reference is naming.
//
// Headings are collected at level >= 2, not strictly ##: the pack puts real
// reference targets at ### ("### Boolean marginals", "### Fused mergeable path").
func checkSectionQualifiedRefs(t *testing.T) {

	headings := map[string][]string{} // skill stem -> heading variants
	for _, name := range skillPackFiles(t) {
		data, err := os.ReadFile(filepath.Join("skills", name))
		if err != nil {
			t.Fatalf("reading skills/%s: %v", name, err)
		}
		stem := strings.TrimSuffix(name, ".md")
		for _, m := range headingRe.FindAllStringSubmatch(string(data), -1) {
			headings[stem] = append(headings[stem], headingVariants(m[1])...)
		}
	}

	for _, src := range sectionRefSources(t) {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("reading %s: %v", src, err)
		}
		text := string(data)
		lineOf := func(idx int) int { return 1 + strings.Count(text[:idx], "\n") }

		for _, loc := range sectionRefRe.FindAllStringSubmatchIndex(text, -1) {
			path := text[loc[2]:loc[3]]
			section := text[loc[4]:loc[5]]
			stem := strings.TrimSuffix(strings.TrimPrefix(path, "skills/"), ".md")
			if _, ok := skills.Get(stem); !ok {
				continue // not a skill target; docs/ paths are out of scope here.
			}
			ref := normaliseRef(section)
			if ref == "" {
				continue
			}
			// Section-reference discriminator: sentence-case start.
			first := strings.TrimLeft(section, `"`+"`"+" “")
			if first == "" || first[0] < 'A' || first[0] > 'Z' {
				continue
			}
			if sectionRefResolves(ref, headings[stem]) {
				continue
			}
			t.Errorf("%s:%d: phantom section reference `%s` (%s) — skills/%s.md carries no heading matching %q. "+
				"Headings it does carry: %s. Re-point the reference at one of them rather than inventing a heading to match the prose.",
				src, lineOf(loc[0]), path, section, stem, section, strings.Join(skillHeadingList(t, stem), " | "))
		}
	}
}

func sectionRefResolves(ref string, variants []string) bool {
	for _, v := range variants {
		if v == "" {
			continue
		}
		if strings.HasPrefix(ref, v) || strings.HasPrefix(v, ref) {
			return true
		}
	}
	return false
}

// skillHeadingList returns the verbatim heading texts of one skill, for the
// failure message — a reader must not have to grep to find the right target.
func skillHeadingList(t *testing.T, stem string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("skills", stem+".md"))
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range headingRe.FindAllStringSubmatch(string(data), -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}
