package descriptor

import (
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// TestSkillExampleTagPointersResolve: every `pulse_examples_search
// tags=[…]` pointer a skill names anywhere (a `## See` line, a Use when
// line, a table cell, a fenced span) is a search some shipped example
// answers. A pointer at a tag no example carries returns nothing, so the
// reader follows a dead lead. The raw body is read, so a pointer inside a
// feature fence counts whether or not a profile renders it. The fixture arm proves a dead tag, a dead `a|b`
// alternative pair and a dead conjunction are each reported.
func TestSkillExampleTagPointersResolve(t *testing.T) {
	t.Parallel()

	lib := examples.All()
	bodies := map[string]string{}
	for _, md := range skills.List() {
		if body, ok := skills.Get(md.Name); ok {
			bodies[md.Name] = body
		}
	}
	for _, p := range unresolvedSkillTagPointers(bodies, lib) {
		t.Error(p)
	}

	fxLib := []*examples.Example{{Name: "a", Tags: []string{"compose", "regression"}}, {Name: "b", Tags: []string{"facet"}}}
	got := unresolvedSkillTagPointers(map[string]string{
		"live": "Use `pulse_examples_search tags=[compose, regression]` or `tags=[\"facet\"]` or `tags=[nope|facet]`.",
		"dead": "Run `pulse_examples_search tags=[synth]`, then `tags=[nope|gone]`, then `tags=[compose, facet]`.",
	}, fxLib)
	want := []string{
		`skill "dead": pulse_examples_search tags=[compose, facet] matches no example`,
		`skill "dead": pulse_examples_search tags=[nope|gone] matches no example`,
		`skill "dead": pulse_examples_search tags=[synth] matches no example`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("fixture problems =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// unresolvedSkillTagPointers returns, sorted, one problem per `tags=[…]`
// list in a skill body that no example in lib matches (seeTags parsing:
// quotes stripped, `a|b` alternatives, every list in the body).
func unresolvedSkillTagPointers(bodies map[string]string, lib []*examples.Example) []string {
	var out []string
	for name, body := range bodies {
		searches, _ := seeTags(body)
		for _, search := range searches {
			matched := false
			for _, ex := range lib {
				if search.matches(ex.Tags) {
					matched = true
					break
				}
			}
			if !matched {
				parts := make([]string, len(search))
				for i, alts := range search {
					parts[i] = strings.Join(alts, "|")
				}
				out = append(out, "skill \""+name+"\": pulse_examples_search tags=["+strings.Join(parts, ", ")+"] matches no example")
			}
		}
	}
	sort.Strings(out)
	return out
}
