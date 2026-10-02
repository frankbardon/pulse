package descriptor

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// hidingSnapshot builds a scoped snapshot that offers every reached
// built-in except hide.
func hidingSnapshot(hide ...string) *InstanceSnapshot {
	var enabled []string
	for _, n := range ReachedFeatureNames(buildinfo.Version()) {
		if !slices.Contains(hide, n) {
			enabled = append(enabled, n)
		}
	}
	return NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: hide})
}

func skillNames(md []skills.Metadata) []string {
	out := make([]string, len(md))
	for i, m := range md {
		out[i] = m.Name
	}
	return out
}

func exampleNames(ex []examples.ExampleSummary) []string {
	out := make([]string, len(ex))
	for i, e := range ex {
		out[i] = e.Name
	}
	return out
}

func TestDiscovery_NothingHiddenPassesThrough(t *testing.T) {
	for name, inst := range map[string]*InstanceSnapshot{
		"nil":      nil,
		"unscoped": UnscopedInstanceSnapshot(&ExtensionsSnapshot{}),
		"full":     hidingSnapshot(),
	} {
		d := inst.Discovery()
		if d != fullDiscovery {
			t.Errorf("%s: Discovery() is not the pass-through view", name)
		}
		if !slices.Equal(skillNames(d.Skills()), skills.Names()) {
			t.Errorf("%s: skills differ from the embedded pack", name)
		}
		n, cats, tags := d.ExampleStats()
		if n != examples.Count() || !slices.Equal(cats, examples.AllCategories()) || !slices.Equal(tags, examples.AllTags()) {
			t.Errorf("%s: example stats differ from the library", name)
		}
	}
}

func TestDiscovery_ComputedOncePerInstance(t *testing.T) {
	inst := hidingSnapshot("REG_GLM")
	first, second := inst.Discovery(), inst.Discovery()
	if first != second {
		t.Error("Discovery() rebuilt on second call")
	}
	if other := hidingSnapshot("REG_GLM"); other.Discovery() == inst.Discovery() {
		t.Error("two instances share one Discovery")
	}
}

func TestDiscovery_PrunesAtomicSkills(t *testing.T) {
	for _, tc := range []struct {
		hide    []string
		gone    []string
		visible []string
	}{
		// operator skill; topical and type skills stay.
		{hide: []string{"REG_GLM"}, gone: []string{"op-reg-glm"}, visible: []string{"op-reg-ols", "op-reg-mod-resample", "regression-modeling", "type-u8"}},
		// tool skill follows its tool's feature; core tools stay.
		{hide: []string{featLookup}, gone: []string{"tool-lookup"}, visible: []string{"tool-inspect", "tool-skills-get"}},
		// synth distributions follow capability:synth.
		{hide: []string{featSynth}, gone: []string{"op-synth-normal"}},
		// overlay kind.
		{hide: []string{"OVERLAY_YOY"}, gone: []string{"op-overlay-yoy"}, visible: []string{"op-overlay-rank"}},
		// a REG spec modifier goes only with every regression.
		{hide: regressionNames(), gone: []string{"op-reg-mod-resample", "op-reg-mod-selection"}},
	} {
		d := hidingSnapshot(tc.hide...).Discovery()
		listed := skillNames(d.Skills())
		for _, g := range tc.gone {
			if slices.Contains(listed, g) || d.SkillVisible(g) {
				t.Errorf("hide %v: %s still visible", tc.hide, g)
			}
			if body, ok := d.Skill(g); ok || body != "" {
				t.Errorf("hide %v: Skill(%s) = (%d bytes, %v), want nonexistent", tc.hide, g, len(body), ok)
			}
		}
		for _, v := range tc.visible {
			body, ok := d.Skill(v)
			want, _ := skills.Get(v)
			if !slices.Contains(listed, v) || !ok || body != want {
				t.Errorf("hide %v: %s should be listed and served whole", tc.hide, v)
			}
		}
	}
}

func regressionNames() []string {
	var out []string
	for _, r := range regressionCapabilities() {
		out = append(out, r.Name)
	}
	return out
}

func TestDiscovery_PrunesExamples(t *testing.T) {
	for _, tc := range []struct {
		hide []string
		gone string
	}{
		// _meta.operators names it.
		{hide: []string{"AGG_SUM"}, gone: firstExampleUsing(t, "AGG_SUM")},
		// only the body names the overlay kind (_meta.operators is empty).
		{hide: []string{"OVERLAY_INDEX_VS_POP"}, gone: "facet-index-vs-pop"},
		// a facet request needs capability:facet.
		{hide: []string{featFacet}, gone: "facet_simple_one_field"},
		// a crosstab slot needs capability:crosstab.
		{hide: []string{featCrosstab}, gone: firstExampleWithSlot(t, "crosstab")},
	} {
		d := hidingSnapshot(tc.hide...).Discovery()
		if _, ok := examples.Get(tc.gone); !ok {
			t.Fatalf("premise: example %q does not exist", tc.gone)
		}
		if slices.Contains(exampleNames(d.ExamplesSearch("", nil, "")), tc.gone) {
			t.Errorf("hide %v: search lists %s", tc.hide, tc.gone)
		}
		if ex, ok := d.Example(tc.gone); ok || ex != nil {
			t.Errorf("hide %v: Example(%s) found, want nonexistent", tc.hide, tc.gone)
		}
		n, _, _ := d.ExampleStats()
		if n != len(d.ExamplesSearch("", nil, "")) || n >= examples.Count() {
			t.Errorf("hide %v: ExampleStats count %d inconsistent", tc.hide, n)
		}
	}
}

func TestDiscovery_ExampleStatsDropEmptiedCategories(t *testing.T) {
	d := hidingSnapshot(featFacet).Discovery()
	_, cats, _ := d.ExampleStats()
	for _, ex := range d.ExamplesSearch("", nil, "facet") {
		t.Errorf("facet example %s survives a hidden capability:facet", ex.Name)
	}
	if slices.Contains(cats, "facet") {
		t.Errorf("categories still list facet: %v", cats)
	}
}

func firstExampleUsing(t *testing.T, op string) string {
	t.Helper()
	for _, ex := range examples.Search("", nil, "") {
		if slices.Contains(ex.Operators, op) {
			return ex.Name
		}
	}
	t.Fatalf("no example uses %s", op)
	return ""
}

func firstExampleWithSlot(t *testing.T, key string) string {
	t.Helper()
	for _, sum := range examples.Search("", nil, "crosstab") {
		ex, _ := examples.Get(sum.Name)
		if jsonHasKeyRaw(t, ex.Body, key) && len(ex.Operators) > 0 {
			return ex.Name
		}
	}
	t.Fatalf("no crosstab example carries %q", key)
	return ""
}

func jsonHasKeyRaw(t *testing.T, raw []byte, key string) bool {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode example body: %v", err)
	}
	return jsonHasKey(v, key)
}

// TestDiscovery_ExampleOperatorRule pins the _meta.operators rule on its
// own: today every tagged operator also appears in the body, so the
// library alone cannot tell it from the body-token rule.
func TestDiscovery_ExampleOperatorRule(t *testing.T) {
	inst := hidingSnapshot("AGG_SUM")
	tagged := &examples.Example{Operators: []string{"AGG_SUM"}, Body: []byte(`{}`)}
	if !slices.ContainsFunc(examplePruneRules, func(rule func(*InstanceSnapshot, *examples.Example) bool) bool {
		return rule(inst, tagged)
	}) {
		t.Error("an example tagged with a hidden operator is not pruned")
	}
	if exampleOperatorHidden(inst, &examples.Example{Operators: []string{"AGG_COUNT"}, Body: []byte(`{}`)}) {
		t.Error("an example tagged only with enabled operators is pruned")
	}
}
