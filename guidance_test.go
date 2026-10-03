package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// TestGlossaryIntentsFacade: the facade serves the registries in
// declaration order, and each call hands out a deep copy — mutating
// every nested slice of one result leaves the next call untouched.
func TestGlossaryIntentsFacade(t *testing.T) {
	g := Glossary()
	if len(g) == 0 || !reflect.DeepEqual(g, descx.Glossary()) {
		t.Fatal("Glossary() does not mirror the registry")
	}
	want := snapshot(t, Glossary())
	g[0].ID, g[0].Short = "mutated", "mutated"
	for i := range g {
		if len(g[i].SeeAlso) > 0 {
			g[i].SeeAlso[0] = "mutated"
		}
		if len(g[i].Forms) > 0 {
			g[i].Forms[0] = "mutated"
		}
	}
	if snapshot(t, Glossary()) != want {
		t.Error("mutating a Glossary() result changed the registry")
	}

	in := Intents()
	if len(in) == 0 || !reflect.DeepEqual(in, descx.Intents()) {
		t.Fatal("Intents() does not mirror the registry")
	}
	wantIn := snapshot(t, Intents())
	in[0].ID = "mutated"
	for i := range in {
		in[i].Sounds[0] = "mutated"
		in[i].Shapes[0].Roles[0].Name = "mutated"
		in[i].Shapes[0].Roles[0].Kinds[0] = descriptor.FieldKind("mutated")
	}
	if snapshot(t, Intents()) != wantIn {
		t.Error("mutating an Intents() result changed the registry")
	}
}

// snapshot freezes v as JSON, independent of any shared backing array.
func snapshot(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestGuidanceSkills_NeverPrunedByFeatureProfile: under every private
// fixture profile the glossary and intents skills stay listed (manifest
// and discovery) and served. Their bodies render from the instance's
// pruned ontology: every section heading served is one the registry
// render carries, and a registry heading is missing exactly when its
// intent / glossary-term node was pruned from the instance graph.
func TestGuidanceSkills_NeverPrunedByFeatureProfile(t *testing.T) {
	kinds := map[string]descriptor.OntologyNodeKind{
		skills.VirtualIntents:  descriptor.OntologyNodeIntent,
		skills.VirtualGlossary: descriptor.OntologyNodeGlossaryTerm,
	}
	for _, name := range featureSetFixtures {
		t.Run(name, func(t *testing.T) {
			p := newFixturePulse(t, name, Options{})
			var manifest []string
			for _, s := range p.Manifest(context.Background()).Skills {
				manifest = append(manifest, s.Name)
			}
			inst := p.svc.InstanceSnapshot()
			d := inst.Discovery()
			var listed []string
			for _, md := range d.Skills() {
				listed = append(listed, md.Name)
			}
			for _, v := range skills.ReservedVirtualNames() {
				if !slices.Contains(manifest, v) {
					t.Errorf("manifest skills lacks %s", v)
				}
				if !slices.Contains(listed, v) {
					t.Errorf("discovery skills lacks %s", v)
				}
				got, ok := d.Skill(v)
				if !ok {
					t.Errorf("discovery does not serve %s", v)
					continue
				}
				full, _ := skills.Get(v)
				served := sectionIDs(got)
				for _, id := range served {
					if !slices.Contains(sectionIDs(full), id) {
						t.Errorf("%s serves section %q the registry render lacks", v, id)
					}
				}
				for _, id := range sectionIDs(full) {
					kept := inst.Ontology().Has(descx.OntologyID(kinds[v], id))
					if on := slices.Contains(served, id); kept != on {
						t.Errorf("%s section %q: served=%v, node kept=%v", v, id, on, kept)
					}
				}
			}
		})
	}
}

// sectionIDs returns the IDs of a virtual skill's term / intent
// sections (`## <id>` in glossary, `### <id>` in intents).
func sectionIDs(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "### "):
			out = append(out, strings.TrimPrefix(line, "### "))
		case strings.HasPrefix(line, "## ") && !strings.Contains(line, " intents"):
			out = append(out, strings.TrimPrefix(line, "## "))
		}
	}
	return out
}
