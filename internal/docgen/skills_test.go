package docgen

import (
	"strings"
	"testing"
)

func TestLinkSkillStems(t *testing.T) {
	set := map[string]bool{"op-test-t": true, "op-test-welch": true, "weighting": true}
	in := strings.Join([]string{
		"See `op-test-welch` and `weighting`; not `op-unknown`.",
		"Self `op-test-t` stays.",
		"- `pulse_examples_search tags=[t-test]` with `op-test-welch` stays plain.",
		"Already [`op-test-welch`](op-test-welch.md).",
		"```json",
		"`op-test-welch`",
		"```",
	}, "\n")
	want := strings.Join([]string{
		"See [`op-test-welch`](op-test-welch.md) and [`weighting`](weighting.md); not `op-unknown`.",
		"Self `op-test-t` stays.",
		"- `pulse_examples_search tags=[t-test]` with `op-test-welch` stays plain.",
		"Already [`op-test-welch`](op-test-welch.md).",
		"```json",
		"`op-test-welch`",
		"```",
	}, "\n")
	if got := linkSkillStems(in, "op-test-t", set); got != want {
		t.Errorf("linkSkillStems:\n got %q\nwant %q", got, want)
	}
}

func TestFenceFrontmatter(t *testing.T) {
	got := fenceFrontmatter("---\nname: x\nkind: operator\n---\n\nBody.\n")
	want := "```yaml\nname: x\nkind: operator\n```\n\nBody.\n"
	if got != want {
		t.Errorf("fenceFrontmatter = %q, want %q", got, want)
	}
	for _, s := range []string{"Body only.\n", "---\nunterminated\n"} {
		if got := fenceFrontmatter(s); got != s {
			t.Errorf("fenceFrontmatter(%q) = %q, want unchanged", s, got)
		}
	}
}

// TestRenderSkill_ServedBody: a skill page is the instance's served
// body with only the frontmatter fenced and stems linked — removing
// both book-only changes gives the served body back byte for byte.
// Render then applies one more book-only transform, escaping bare
// `<word>` placeholders outside code; nothing else changes.
func TestRenderSkill_ServedBody(t *testing.T) {
	g := newGen(nil, Options{})
	rendered := map[string]string{}
	for _, f := range Render(nil, Options{}) {
		rendered[f.Path] = string(f.Body)
	}
	linked := 0
	for _, md := range g.visibleSkills() {
		served, _ := g.disc.Skill(md.Name)
		page := g.renderSkill(md.Name)
		if page != linkSkillStems(fenceFrontmatter(served), md.Name, g.skillSet) {
			t.Fatalf("%s: page is not the served body", md.Name)
		}
		unlinked := page
		for stem := range g.skillSet {
			unlinked = strings.ReplaceAll(unlinked, "[`"+stem+"`]("+stem+".md)", "`"+stem+"`")
		}
		if unlinked != fenceFrontmatter(served) {
			t.Fatalf("%s: page changes more than the links", md.Name)
		}
		if unlinked != page {
			linked++
		}
		if rendered[skillPath(md.Name)] != escapePlaceholders(page) {
			t.Fatalf("%s: rendered file is not the escaped page", md.Name)
		}
	}
	if linked == 0 {
		t.Fatal("no skill page links another skill: vacuous")
	}
	if g.renderSkill("no-such-skill") != "" {
		t.Error("renderSkill of an unknown skill is not empty")
	}
}
