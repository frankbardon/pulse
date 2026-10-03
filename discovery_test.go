package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/spf13/afero"
)

// discoveryTokens splits s into identifier-shaped tokens (operator and
// MCP tool names are [A-Za-z0-9_] runs).
func discoveryTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
	})
}

// hiddenOperatorNames is the instance's hidden operator spellings (the
// bare feature names; kind-prefixed capabilities never appear in prose).
func hiddenOperatorNames(p *Pulse) map[string]struct{} {
	out := map[string]struct{}{}
	for _, n := range p.svc.InstanceSnapshot().HiddenNames() {
		if !strings.Contains(n, ":") {
			out[n] = struct{}{}
		}
	}
	return out
}

// TestManifestSkills_DescriptionsNameNoHidden: under a feature profile a
// visible skill's manifest description is rendered like its
// pulse_skills_list entry, so it names no hidden operator; the full
// manifest's (cached) descriptions are never written by that render.
func TestManifestSkills_DescriptionsNameNoHidden(t *testing.T) {
	full := descx.BuildManifest()
	fullDesc := map[string]string{}
	for _, s := range full.Skills {
		fullDesc[s.Name] = s.Description
	}
	for _, fixture := range []string{"minimal", "survey-crosstab"} {
		t.Run(fixture, func(t *testing.T) {
			p := newFixturePulse(t, fixture, Options{})
			hidden := hiddenOperatorNames(p)
			m := p.Manifest(context.Background())
			listed := map[string]string{}
			for _, md := range p.Skills() {
				listed[md.Name] = md.Description
			}
			rendered := 0
			for _, s := range m.Skills {
				for _, tok := range discoveryTokens(s.Description) {
					if _, ok := hidden[tok]; ok {
						t.Errorf("skill %s: manifest description names hidden %s: %q", s.Name, tok, s.Description)
					}
				}
				if want, ok := listed[s.Name]; !ok || want != s.Description {
					t.Errorf("skill %s: manifest description %q, pulse_skills_list %q (listed=%v)", s.Name, s.Description, want, ok)
				}
				if s.Description != fullDesc[s.Name] {
					rendered++
				}
			}
			// The pack's descriptions name no other feature (the description
			// rule of TestSkillsCoverFeatureFences), so the metadata scrub is
			// a backstop that renders nothing on the shipped pack; the checks
			// above bind only when the fixture really hides operators.
			if len(hidden) == 0 {
				t.Fatalf("vacuous: fixture %s hides no operator", fixture)
			}
			if rendered != 0 {
				t.Errorf("%d shipped skill descriptions changed under %s: a description names a feature the profile hides", rendered, fixture)
			}
		})
	}
	for _, s := range descx.BuildManifest().Skills {
		if s.Description != fullDesc[s.Name] {
			t.Errorf("skill %s: a scoped manifest render wrote the shared full description", s.Name)
		}
	}
}

// TestFacade_OntologySkillsAgree: p.Ontology(), p.Skills(), p.Skill()
// and the manifest skills list describe the same instance — the skill
// nodes ARE the listed skills, every listed skill reads, a pruned one
// reads exactly as a nonexistent one, and the graph is a fresh copy.
func TestFacade_OntologySkillsAgree(t *testing.T) {
	fullP, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	fullNames := skillNames(fullP.Skills())
	for _, fixture := range []string{"", "minimal", "survey-crosstab", "empty"} {
		t.Run("profile="+fixture, func(t *testing.T) {
			p := fullP
			if fixture != "" {
				p = newFixturePulse(t, fixture, Options{})
			}
			names := skillNames(p.Skills())
			var nodes []string
			for _, n := range p.Ontology().Nodes {
				if n.Kind == descriptor.OntologyNodeSkill {
					nodes = append(nodes, n.Name)
				}
			}
			if !slices.Equal(names, nodes) {
				t.Errorf("Skills() names %v\n!= ontology skill nodes %v", names, nodes)
			}
			var manifest []string
			for _, s := range p.Manifest(context.Background()).Skills {
				manifest = append(manifest, s.Name)
			}
			if !slices.Equal(names, manifest) {
				t.Errorf("Skills() names != manifest skills")
			}
			for _, n := range names {
				if body, ok := p.Skill(n); !ok || body == "" {
					t.Errorf("listed skill %s does not read", n)
				}
			}
			pruned := 0
			for _, n := range fullNames {
				if slices.Contains(names, n) {
					continue
				}
				pruned++
				if body, ok := p.Skill(n); ok || body != "" {
					t.Errorf("pruned skill %s reads (%v, %d bytes)", n, ok, len(body))
				}
			}
			if _, ok := p.Skill("no-such-skill"); ok {
				t.Errorf("nonexistent skill reads")
			}
			if fixture != "" && pruned == 0 {
				t.Errorf("vacuous: profile %s prunes no skill", fixture)
			}
			// Ontology() is a copy: mutating it leaves the next call whole.
			g := p.Ontology()
			before, _ := json.Marshal(g)
			g.Nodes[0].Name = "mutated"
			g.Edges = nil
			if after, _ := json.Marshal(p.Ontology()); string(after) != string(before) {
				t.Errorf("Ontology() result aliases the instance graph")
			}
		})
	}
}

func skillNames(mds []SkillMetadata) []string {
	out := make([]string, len(mds))
	for i, m := range mds {
		out[i] = m.Name
	}
	return out
}
