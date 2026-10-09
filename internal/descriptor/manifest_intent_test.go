package descriptor

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
)

// scopedManifestMaxShare is the size gate: an analytic intent's scoped
// slim manifest is at most this share of the full slim manifest. Never
// raise it; an intent that busts it goes in scopedManifestExceptions
// with its measured ceiling.
const scopedManifestMaxShare = 0.15

// scopedManifestExceptions names an analytic intent allowed past
// scopedManifestMaxShare, with its measured ceiling (a share of the full
// slim manifest). Raise an entry only with a fresh measurement; never
// add one to dodge a regression.
var scopedManifestExceptions = map[string]float64{
	// describe is served by 27 aggregators — every summary statistic —
	// so its operator list alone is about a quarter of the catalog.
	// Measured 25.3% (51,218 of 202,559 bytes) at guidance-mcp E3-S1.
	IntentDescribe: 0.26,
}

func compactJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestManifestForIntent_SizeGate: every analytic intent's scoped slim
// manifest is at most 15% of the full slim manifest (compact JSON, the
// form pulse_manifest serves).
func TestManifestForIntent_SizeGate(t *testing.T) {
	inst := hidingSnapshot()
	full := len(compactJSON(t, SlimManifest(BuildManifestForInstance(inst))))
	for _, in := range Intents() {
		if !in.Analytic {
			continue
		}
		m, err := BuildManifestForIntent(inst, in.ID)
		if err != nil {
			t.Fatalf("%s: %v", in.ID, err)
		}
		size := len(compactJSON(t, SlimManifest(m)))
		share := float64(size) / float64(full)
		limit := scopedManifestMaxShare
		if c, ok := scopedManifestExceptions[in.ID]; ok {
			limit = c
		}
		t.Logf("%-20s %7d bytes  %.1f%% of %d", in.ID, size, 100*share, full)
		if share > limit {
			t.Errorf("%s: scoped slim manifest is %d bytes, %.1f%% of the full slim %d (limit %.0f%%)",
				in.ID, size, 100*share, full, 100*limit)
		}
	}
	for id := range scopedManifestExceptions {
		if !IsIntent(id) {
			t.Errorf("exception %q names no intent", id)
		}
	}
}

// TestManifestForIntent_Contents: every operator-keyed entry serves the
// intent, the name-keyed schemas follow the remaining operators, skills
// is SkillsForIntent's list, scope carries the record, and every elided
// key is absent on the wire while the kept ones stay.
func TestManifestForIntent_Contents(t *testing.T) {
	inst := hidingSnapshot()
	for _, intent := range []string{IntentCompareGroups, IntentRelationship, IntentDescribe, IntentLookup} {
		t.Run(intent, func(t *testing.T) {
			m, err := BuildManifestForIntent(inst, intent)
			if err != nil {
				t.Fatal(err)
			}
			check := func(section, name string, intents []string) {
				if !slices.Contains(intents, intent) {
					t.Errorf("%s %s does not serve %s: %v", section, name, intent, intents)
				}
			}
			ops := map[string]bool{}
			for section, list := range map[string][]descriptor.Operator{
				"aggregators": m.Components.Aggregators, "attributes": m.Components.Attributes,
				"filterers": m.Components.Filterers, "groupers": m.Components.Groupers,
				"windows": m.Components.Windows, "features": m.Components.Features,
			} {
				for _, o := range list {
					check(section, o.Name, o.Intents)
					ops[o.Name] = true
				}
			}
			for _, x := range append(slices.Clone(m.Tests), m.PostTests...) {
				check("tests", x.Name, x.Intents)
			}
			for _, x := range m.Regressions {
				check("regressions", x.Name, x.Intents)
			}
			for _, x := range m.Matrices {
				check("matrices", x.Name, x.Intents)
				ops[x.Name] = true
			}
			for _, x := range m.Overlays {
				check("overlays", string(x.Kind), x.Intents)
			}
			// components_schemas is elided as a duplicate: each remaining
			// operator's own component_schema is the full block's entry.
			fullBlock := BuildManifestForInstance(inst).ComponentsSchemas
			lossless := func(block map[string]descriptor.ComponentSchema, name string, own descriptor.ComponentSchema) {
				if s, ok := block[name]; ok && !reflect.DeepEqual(s, own) {
					t.Errorf("%s: component_schema differs from components_schemas entry", name)
				}
			}
			for _, o := range m.Components.Aggregators {
				lossless(fullBlock.Aggregators, o.Name, o.ComponentSchema)
			}
			for _, o := range m.Components.Groupers {
				lossless(fullBlock.Groupers, o.Name, o.ComponentSchema)
			}
			for _, o := range m.Components.Filterers {
				lossless(fullBlock.Filterers, o.Name, o.ComponentSchema)
			}
			for _, x := range m.Matrices {
				lossless(fullBlock.Matrices, x.Name, x.ComponentSchema)
			}
			want, _ := inst.SkillsForIntent(intent)
			if got := skillMetaNames(m.Skills); !slices.Equal(got, skillNames(want)) {
				t.Errorf("skills = %v, want %v", got, skillNames(want))
			}
			if m.Scope == nil || m.Scope.Intent.ID != intent || m.Scope.Intent.Label == "" {
				t.Fatalf("scope = %+v", m.Scope)
			}

			var wire map[string]json.RawMessage
			if err := json.Unmarshal(compactJSON(t, SlimManifest(m)), &wire); err != nil {
				t.Fatal(err)
			}
			if !slices.IsSorted(m.Elided) {
				t.Errorf("elided not sorted: %v", m.Elided)
			}
			for _, k := range descriptor.ScopedManifestElidedKeys() {
				if !slices.Contains(m.Elided, k) {
					t.Errorf("elided lacks fixed key %s", k)
				}
			}
			for _, k := range m.Elided {
				if _, ok := wire[k]; ok {
					t.Errorf("elided key %s present on the wire", k)
				}
			}
			for _, k := range []string{"format_version", "pulse_version", "feature_set_digest", "limits_digest",
				"components", "tests", "skills", "intents", "return_presets", "scope", "elided"} {
				if _, ok := wire[k]; !ok {
					t.Errorf("kept key %s absent", k)
				}
			}
			if (m.Crosstab == nil) != slices.Contains(m.Elided, "crosstab") {
				t.Errorf("crosstab %v vs elided %v", m.Crosstab != nil, m.Elided)
			}
			if m.Crosstab != nil {
				for _, n := range m.Crosstab.SummableAggregators {
					if !ops[n] {
						t.Errorf("crosstab lists %s, not a remaining aggregator", n)
					}
				}
			}
		})
	}
}

// TestManifestForIntent_CrosstabFollowsAggregators: crosstab is kept
// for an intent with crosstab-cell aggregators and dropped (and listed
// in elided) for one without any aggregator.
func TestManifestForIntent_CrosstabFollowsAggregators(t *testing.T) {
	inst := hidingSnapshot()
	withAgg, err := BuildManifestForIntent(inst, IntentDescribe)
	if err != nil {
		t.Fatal(err)
	}
	if withAgg.Crosstab == nil {
		t.Error("describe: crosstab dropped although aggregators remain")
	}
	for _, in := range Intents() {
		m, err := BuildManifestForIntent(inst, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Components.Aggregators) == 0 && m.Crosstab != nil {
			t.Errorf("%s: crosstab kept with no aggregator", in.ID)
		}
		if len(m.Matrices) == 0 && m.Matrix != nil {
			t.Errorf("%s: matrix block kept with no matrix operator", in.ID)
		}
	}
}

// TestManifestForIntent_Refusals: an unknown intent, and one a feature
// profile hides, are PULSE_RECOMMEND_INTENT_UNKNOWN with details.valid.
func TestManifestForIntent_Refusals(t *testing.T) {
	want := func(inst *InstanceSnapshot, intent string) map[string]any {
		t.Helper()
		_, err := BuildManifestForIntent(inst, intent)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RECOMMEND_INTENT_UNKNOWN {
			t.Fatalf("%q: err = %v, want PULSE_RECOMMEND_INTENT_UNKNOWN", intent, err)
		}
		return ce.Details
	}
	if d := want(nil, "not_an_intent"); !slices.Contains(d["valid"].([]string), IntentDrivers) {
		t.Errorf("valid = %v", d["valid"])
	}
	hidden := hidingSnapshot(BaseOntology().OperatorsServing(IntentDrivers)...)
	if d := want(hidden, IntentDrivers); slices.Contains(d["valid"].([]string), IntentDrivers) {
		t.Error("hidden intent listed as valid")
	}
}

// TestManifestForIntent_ComposesWithProfile: on a profiled instance the
// scoped manifest lists no hidden operator, keeps the instance digest,
// and drops only what scoping drops (a block the profile already hid is
// not listed as elided).
func TestManifestForIntent_ComposesWithProfile(t *testing.T) {
	hide := []string{"TEST_T", featCrosstab}
	inst := hidingSnapshot(hide...)
	m, err := BuildManifestForIntent(inst, IntentCompareGroups)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range append(slices.Clone(m.Tests), m.PostTests...) {
		if x.Name == "TEST_T" || x.Family == "TEST_T" {
			t.Errorf("hidden %s listed", x.Name)
		}
	}
	if m.Crosstab != nil || slices.Contains(m.Elided, "crosstab") {
		t.Errorf("profile-hidden crosstab: block %v, elided %v", m.Crosstab != nil, m.Elided)
	}
	if m.FeatureSetDigest != inst.Digest() {
		t.Errorf("digest = %s, want the instance's %s", m.FeatureSetDigest, inst.Digest())
	}
	full, err := BuildManifestForIntent(hidingSnapshot(), IntentCompareGroups)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tests) >= len(full.Tests) {
		t.Errorf("profiled tests %d, unprofiled %d: TEST_T not pruned", len(m.Tests), len(full.Tests))
	}
}

// TestManifestForIntent_UnscopedUnchanged: scoping never touches the
// unscoped manifest's wire form (no scope / elided keys).
func TestManifestForIntent_UnscopedUnchanged(t *testing.T) {
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(compactJSON(t, BuildManifest()), &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"scope", "elided"} {
		if _, ok := wire[k]; ok {
			t.Errorf("unscoped manifest carries %s", k)
		}
	}
	for _, k := range descriptor.ScopedManifestElidedKeys() {
		if _, ok := wire[k]; !ok && k != "facet" && k != "process_chain" && k != "join" && k != "export" && k != "import" {
			t.Errorf("unscoped manifest lacks %s", k)
		}
	}
}

func skillMetaNames(in []descriptor.SkillMeta) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.Name
	}
	return out
}
