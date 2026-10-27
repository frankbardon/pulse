//go:build heavy

package descriptor

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// TestManifestGuidanceBudget is the binding guidance-bloat gate on the
// default (full-registry, profile-free) manifest: guidance-attributable
// bytes stay within both caps — manifestGuidancePerEntryCap on every
// entry's intents, manifestGuidanceFixedCap on the rest — and no declared guidance
// prose (GuidanceProse — every purpose, glossary term, intent and
// interpretation, including the shared rule sets) appears in it.
// Instance manifests are subsets of this one, so the full manifest
// bounds them. The Response / PredictResult half lives in the root
// package (it must execute).
func TestManifestGuidanceBudget(t *testing.T) {
	raw := marshalManifest(t, BuildManifest())
	got := manifestGuidanceBytes(t, raw)
	longest := 0
	for _, e := range got.Entries {
		longest = max(longest, e.Bytes)
	}
	t.Logf("guidance bytes in default manifest: fixed %d / %d; %d entries, longest %d / %d per entry; total %d (manifest %d bytes)",
		got.Fixed, manifestGuidanceFixedCap, len(got.Entries), longest, manifestGuidancePerEntryCap, got.Total(), len(raw))
	for _, p := range guidanceBudgetProblems(got) {
		t.Errorf("%s — guidance must stay on-demand (pulse.Glossary / pulse.Intents / virtual skills), not ride the manifest", p)
	}
	if got.Fixed == 0 || len(got.Entries) == 0 {
		t.Error("measured no fixed or no per-entry guidance bytes: the measurement no longer sees the intents taxonomy or the entry intents — fix manifestGuidanceBytes")
	}
	for _, leak := range LeakedGuidanceProse(raw) {
		t.Errorf("default manifest carries guidance prose from %s: %q — serve it on demand instead", leak.Source, leak.Text)
	}
}

// TestManifestGuidanceBudget_Falsifiers proves the gate's checks bite:
// injected prose is reported, an inflated taxonomy breaks the fixed cap
// and a prose-carrying entry intents list breaks the per-entry cap.
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
		{name: "inflated intents taxonomy", over: true, mutate: func(m *descriptor.Manifest) {
			m.Intents = append(m.Intents, strings.Repeat("x", manifestGuidanceFixedCap))
		}},
		{name: "prose in one entry's intents", over: true, mutate: func(m *descriptor.Manifest) {
			m.Components.Aggregators[0].Intents = append(m.Components.Aggregators[0].Intents, "summarise the typical value of a numeric field")
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
				if probs := guidanceBudgetProblems(manifestGuidanceBytes(t, raw)); len(probs) != 1 {
					t.Errorf("inflated manifest: want exactly one cap broken, got %v", probs)
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
