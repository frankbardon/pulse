package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
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
// and discovery) and their bodies are served unchanged. No prune rule
// matches a reference-kind skill; this pins that a new rule keeps it so.
func TestGuidanceSkills_NeverPrunedByFeatureProfile(t *testing.T) {
	for _, name := range featureSetFixtures {
		t.Run(name, func(t *testing.T) {
			p := newFixturePulse(t, name, Options{})
			var manifest []string
			for _, s := range p.Manifest(context.Background()).Skills {
				manifest = append(manifest, s.Name)
			}
			d := p.svc.InstanceSnapshot().Discovery()
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
				want, _ := skills.Get(v)
				if got, ok := d.Skill(v); !ok || got != want {
					t.Errorf("discovery body of %s pruned or rendered (ok=%v)", v, ok)
				}
			}
		})
	}
}
