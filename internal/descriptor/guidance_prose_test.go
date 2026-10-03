package descriptor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// manifestGuidanceBudget caps the bytes of the compact default manifest
// attributable to guidance (PRD FR-20): every "intents" key + value and
// the two virtual-skill skills[] entries. Guidance prose itself is
// served on demand, never inlined.
const manifestGuidanceBudget = 4096

// manifestGuidanceBytes returns the bytes of the compact manifest JSON
// attributable to guidance: every object key "intents" at any depth
// (the top-level taxonomy plus each entry's intents, so a new entry type
// is counted with no change here), every overlay "inferential" flag
// (the Interpretation half's only manifest projection), and every
// skills[] entry naming a
// reserved virtual skill. Each item is counted with its separating
// comma.
func manifestGuidanceBytes(t *testing.T, raw []byte) int {
	t.Helper()
	var root any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	size := func(v any) int {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return len(b)
	}
	virtual := map[string]bool{}
	for _, n := range skills.ReservedVirtualNames() {
		virtual[n] = true
	}
	total := 0
	var walk func(v any, top bool)
	walk = func(v any, top bool) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if k == "intents" || k == "inferential" {
					total += len(`"`+k+`":`) + size(child) + 1
					continue
				}
				if top && k == "skills" {
					entries, _ := child.([]any)
					for _, e := range entries {
						if m, ok := e.(map[string]any); ok && virtual[m["name"].(string)] {
							total += size(e) + 1
						}
					}
					continue
				}
				walk(child, false)
			}
		case []any:
			for _, e := range x {
				walk(e, false)
			}
		}
	}
	walk(root, true)
	return total
}

func marshalManifest(t *testing.T, m *descriptor.Manifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return b
}

// TestManifestGuidanceBudget is the binding guidance-bloat gate on the
// default (full-registry, profile-free) manifest: guidance-attributable
// bytes stay within manifestGuidanceBudget, and no declared guidance
// prose (GuidanceProse — every purpose, glossary term, intent and
// interpretation, including the shared rule sets) appears in it.
// Instance manifests are subsets of this one, so the full manifest
// bounds them. The Response / PredictResult half lives in the root
// package (it must execute).
func TestManifestGuidanceBudget(t *testing.T) {
	raw := marshalManifest(t, BuildManifest())
	got := manifestGuidanceBytes(t, raw)
	t.Logf("guidance bytes in default manifest: %d / %d (manifest %d bytes)", got, manifestGuidanceBudget, len(raw))
	if got > manifestGuidanceBudget {
		t.Errorf("guidance-attributable manifest bytes = %d, budget %d: guidance must stay on-demand (pulse.Glossary / pulse.Intents / virtual skills), not ride the manifest", got, manifestGuidanceBudget)
	}
	if got == 0 {
		t.Error("measured 0 guidance bytes: the measurement no longer sees the intents taxonomy — fix manifestGuidanceBytes")
	}
	for _, leak := range LeakedGuidanceProse(raw) {
		t.Errorf("default manifest carries guidance prose from %s: %q — serve it on demand instead", leak.Source, leak.Text)
	}
}

// TestManifestGuidanceBudget_Falsifiers proves the gate's checks bite:
// injected prose is reported, and an inflated intents list blows the
// budget.
func TestManifestGuidanceBudget_Falsifiers(t *testing.T) {
	plain := BuiltinPurposes()["AGG_AVERAGE"].Plain
	var short, label string
	for _, term := range Glossary() {
		if len(term.Short) >= GuidanceProseMinLen {
			short = term.Short
			break
		}
	}
	for _, in := range Intents() {
		if len(in.Sounds) > 0 {
			label = in.Sounds[0]
			break
		}
	}
	interp := BuiltinInterpretations()["TEST_ANOVA_F"][0].Means
	shared, _ := SharedInterpretation(SharedPValue)
	sharedCaveat := shared.Caveats[0]
	cases := []struct {
		name   string
		mutate func(m *descriptor.Manifest)
		leak   string
		over   bool
	}{
		{name: "purpose plain inlined in operator description", leak: plain, mutate: func(m *descriptor.Manifest) {
			m.Components.Aggregators[0].Description += " " + plain
		}},
		{name: "glossary short inlined in skill description", leak: short, mutate: func(m *descriptor.Manifest) {
			m.Skills[0].Description = short
		}},
		{name: "intent phrasing inlined in test description", leak: label, mutate: func(m *descriptor.Manifest) {
			m.Tests[0].Description = label
		}},
		{name: "interpretation means inlined in test description", leak: interp, mutate: func(m *descriptor.Manifest) {
			m.Tests[0].Description += " " + interp
		}},
		{name: "shared p-value caveat inlined in overlay description", leak: sharedCaveat, mutate: func(m *descriptor.Manifest) {
			m.Overlays[0].Description = sharedCaveat
		}},
		{name: "5 KB of intents", over: true, mutate: func(m *descriptor.Manifest) {
			pad := strings.Repeat("x", 100)
			for i := 0; i < 50; i++ {
				m.Intents = append(m.Intents, pad)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.leak == "" && !tc.over {
				t.Fatal("fixture has nothing to inject")
			}
			m := BuildManifest()
			// BuildManifest hands out fresh slices, but copy what we
			// mutate so a cached table can never be touched.
			m.Components.Aggregators = slices.Clone(m.Components.Aggregators)
			m.Skills = slices.Clone(m.Skills)
			m.Tests = slices.Clone(m.Tests)
			m.Intents = slices.Clone(m.Intents)
			m.Overlays = slices.Clone(m.Overlays)
			tc.mutate(m)
			raw := marshalManifest(t, m)
			if tc.over {
				if got := manifestGuidanceBytes(t, raw); got <= manifestGuidanceBudget {
					t.Errorf("inflated manifest measured %d guidance bytes, want > %d", got, manifestGuidanceBudget)
				}
				return
			}
			found := false
			for _, l := range LeakedGuidanceProse(raw) {
				if l.Text == tc.leak {
					found = true
				}
			}
			if !found {
				t.Errorf("injected prose %q not reported as leaked", tc.leak)
			}
		})
	}
}

// pendingProseTypes are guidance types whose registry does not exist
// yet; a new guidance type lists itself here until its registry appends
// a source to proseSources.
var pendingProseTypes = map[reflect.Type]string{}

// TestGuidanceProseSourcesComplete proves GuidanceProse sweeps every
// guidance type that carries prose: each has a proseSource, or is
// listed pending (and a pending type with a source is stale).
func TestGuidanceProseSourcesComplete(t *testing.T) {
	guidanceTypes := []reflect.Type{
		reflect.TypeOf(descriptor.Purpose{}),
		reflect.TypeOf(descriptor.Interpretation{}),
		reflect.TypeOf(descriptor.Term{}),
		reflect.TypeOf(descriptor.Intent{}),
	}
	sourced := map[reflect.Type]bool{}
	for _, s := range proseSources {
		sourced[s.typ] = true
		if len(s.values()) == 0 {
			t.Errorf("prose source for %s yields no values", s.typ)
		}
	}
	for _, typ := range guidanceTypes {
		_, pending := pendingProseTypes[typ]
		switch {
		case sourced[typ] && pending:
			t.Errorf("%s has a prose source but is still listed in pendingProseTypes — delete the pending entry", typ)
		case !sourced[typ] && !pending:
			t.Errorf("%s carries guidance prose but no proseSource feeds GuidanceProse", typ)
		}
	}
	// Every built-in Plain is swept (registry-driven: a new purpose is
	// covered with no change to the gate).
	all := map[string]bool{}
	for _, p := range GuidanceProse() {
		all[p.Text] = true
	}
	for name, p := range BuiltinPurposes() {
		if !all[p.Plain] {
			t.Errorf("purpose %s Plain not swept by GuidanceProse", name)
		}
	}
	for name, ins := range BuiltinInterpretations() {
		for _, in := range ins {
			if in.Means != "" && !all[in.Means] {
				t.Errorf("interpretation %s.%s Means not swept by GuidanceProse", name, in.Field)
			}
		}
	}
	for _, k := range SharedInterpretationKeys() {
		if in, _ := SharedInterpretation(k); !all[in.Means] {
			t.Errorf("shared interpretation %s Means not swept by GuidanceProse", k)
		}
	}
}

// TestCollectProse pins which fields the reflective walker treats as
// prose, including every Interpretation prose field so the E4 registry
// is covered the moment it is plugged in.
func TestCollectProse(t *testing.T) {
	lo := 0.2
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"purpose", descriptor.Purpose{
			Plain: "plain", Intents: []string{"describe"}, Questions: []string{"q?"},
			UseCases:    map[descriptor.Domain]string{descriptor.DomainOps: "ops", descriptor.DomainSurvey: "survey"},
			NotFor:      []descriptor.Alternative{{When: "when", Use: "AGG_MEDIAN"}},
			Assumptions: []string{"assume"}, Level: descriptor.LevelBasic, Glossary: []string{"mean"},
		}, []string{"plain", "q?", "ops", "survey", "when", "assume"}},
		{"interpretation", descriptor.Interpretation{
			Field: "details.effect_size.cohens_d", Means: "means",
			Bands:      []descriptor.Band{{Min: &lo, Label: "small"}},
			Convention: "Cohen (1988)", Sign: map[string]string{"+": "up", "-": "down"},
			Caveats: []string{"caveat"}, Shared: "p-value",
		}, []string{"means", "small", "Cohen (1988)", "down", "up", "caveat"}},
		{"term", descriptor.Term{
			ID: "p-value", Short: "short", WhyCare: "why", SeeAlso: []string{"alpha"}, Forms: []string{"p value"},
		}, []string{"short", "why"}},
		{"intent", descriptor.Intent{
			ID: "describe", Label: "label", Sounds: []string{"sound"},
			Shapes: []descriptor.Shape{{Roles: []descriptor.Role{{Name: "outcome", Kinds: []descriptor.FieldKind{descriptor.FieldKindNumeric}}}}},
		}, []string{"label", "sound"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CollectProse(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CollectProse = %q, want %q", got, tc.want)
			}
		})
	}
}
