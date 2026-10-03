package descriptor

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/buildinfo"
)

// TestIntentRegistry_ExactIDs: the taxonomy is the closed set of fifteen
// IDs — twelve analytic, three routing to tooling.
func TestIntentRegistry_ExactIDs(t *testing.T) {
	analytic := []string{
		"benchmark", "change_over_time", "compare_groups", "composition",
		"data_quality", "describe", "distribution_shape", "drivers",
		"flows", "measure_construct", "relationship", "segment",
	}
	tooling := []string{"lookup", "prepare", "simulate"}
	var gotAnalytic, gotTooling []string
	for _, in := range Intents() {
		if in.Analytic {
			gotAnalytic = append(gotAnalytic, in.ID)
		} else {
			gotTooling = append(gotTooling, in.ID)
		}
	}
	slices.Sort(gotAnalytic)
	slices.Sort(gotTooling)
	if !slices.Equal(gotAnalytic, analytic) {
		t.Errorf("analytic intents = %v, want %v", gotAnalytic, analytic)
	}
	if !slices.Equal(gotTooling, tooling) {
		t.Errorf("non-analytic intents = %v, want %v", gotTooling, tooling)
	}
	want := append(append([]string{}, analytic...), tooling...)
	slices.Sort(want)
	if got := IntentIDs(); !slices.Equal(got, want) {
		t.Errorf("IntentIDs() = %v, want %v", got, want)
	}
	for _, id := range want {
		if !IsIntent(id) {
			t.Errorf("IsIntent(%q) = false", id)
		}
	}
	if IsIntent("not_an_intent") {
		t.Error("IsIntent accepted an unknown ID")
	}
}

// TestIntentRegistry_WellFormed: unique IDs, a label and phrasings on
// every intent, and every shape role names ≥1 valid field kind with a
// coherent cardinality.
func TestIntentRegistry_WellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, in := range Intents() {
		if in.ID == "" {
			t.Error("intent with empty ID")
		}
		if seen[in.ID] {
			t.Errorf("intent %q declared twice", in.ID)
		}
		seen[in.ID] = true
		if strings.TrimSpace(in.Label) == "" {
			t.Errorf("intent %q has no Label", in.ID)
		}
		if len(in.Sounds) == 0 {
			t.Errorf("intent %q has no Sounds", in.ID)
		}
		for _, s := range in.Sounds {
			if strings.TrimSpace(s) == "" {
				t.Errorf("intent %q has an empty Sounds entry", in.ID)
			}
		}
		if len(in.Shapes) == 0 {
			t.Errorf("intent %q declares no Shapes", in.ID)
		}
		for si, sh := range in.Shapes {
			if len(sh.Roles) == 0 {
				t.Errorf("intent %q shape %d has no roles", in.ID, si)
			}
			names := map[string]bool{}
			for _, r := range sh.Roles {
				if r.Name == "" {
					t.Errorf("intent %q shape %d has a role with no name", in.ID, si)
				}
				if names[r.Name] {
					t.Errorf("intent %q shape %d repeats role %q", in.ID, si, r.Name)
				}
				names[r.Name] = true
				if len(r.Kinds) == 0 {
					t.Errorf("intent %q role %q names no field kind", in.ID, r.Name)
				}
				for _, k := range r.Kinds {
					if !isFieldKind(k) {
						t.Errorf("intent %q role %q names unknown field kind %q", in.ID, r.Name, k)
					}
				}
				if r.Min < 0 {
					t.Errorf("intent %q role %q has negative Min %d", in.ID, r.Name, r.Min)
				}
				if r.Max != descriptor.RoleUnbounded && (r.Max < 1 || r.Min > r.Max) {
					t.Errorf("intent %q role %q has Min %d > Max %d (or Max < 1)", in.ID, r.Name, r.Min, r.Max)
				}
			}
		}
	}
}

// TestIntents_ReturnsCopy: mutating the returned taxonomy never reaches
// the registry.
func TestIntents_ReturnsCopy(t *testing.T) {
	a := Intents()
	a[0].Sounds[0] = "mutated"
	a[0].Shapes[0].Roles[0].Kinds[0] = "mutated"
	b := Intents()
	if b[0].Sounds[0] == "mutated" || b[0].Shapes[0].Roles[0].Kinds[0] == "mutated" {
		t.Error("Intents() shares backing arrays with the registry")
	}
}

// TestFieldKindOf_Total: every registered field type maps to a valid
// coarse kind; an unknown type byte does not.
func TestFieldKindOf_Total(t *testing.T) {
	n := 0
	for i := range 256 {
		ft := encoding.FieldType(i)
		if !ft.IsKnown() {
			if _, ok := FieldKindOf(ft); ok {
				t.Errorf("unknown field type byte %d mapped to a kind", i)
			}
			break
		}
		k, ok := FieldKindOf(ft)
		if !ok || !isFieldKind(k) {
			t.Errorf("field type %s maps to no valid kind (got %q, %v)", ft, k, ok)
		}
		n++
	}
	if n == 0 {
		t.Fatal("walked no field types")
	}
	spot := map[encoding.FieldType]descriptor.FieldKind{
		encoding.FieldTypeDecimal128:     descriptor.FieldKindNumeric,
		encoding.FieldTypeCategoricalU16: descriptor.FieldKindCategorical,
		encoding.FieldTypeDateTime:       descriptor.FieldKindDate,
		encoding.FieldTypePackedBool:     descriptor.FieldKindBool,
		encoding.FieldTypeSetU256:        descriptor.FieldKindSet,
	}
	for ft, want := range spot {
		if got, _ := FieldKindOf(ft); got != want {
			t.Errorf("FieldKindOf(%s) = %q, want %q", ft, got, want)
		}
	}
}

// TestManifestIntents_TopLevel: the manifest lists the sorted taxonomy
// IDs, and with no purpose declared no entry carries an intents key.
func TestManifestIntents_TopLevel(t *testing.T) {
	m := BuildManifest()
	if !slices.Equal(m.Intents, IntentIDs()) {
		t.Errorf("manifest intents = %v, want %v", m.Intents, IntentIDs())
	}
	if len(builtinPurposes) > 0 {
		t.Skip("purposes declared; the omitempty check below assumes none")
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(b), `"intents":`); c != 1 {
		t.Errorf(`manifest JSON carries %d "intents" keys, want only the top-level one`, c)
	}
}

// TestManifestIntents_StaticUnderProfile: a scoped instance — even one
// enabling nothing — carries the same static taxonomy.
func TestManifestIntents_StaticUnderProfile(t *testing.T) {
	for name, set := range map[string]FeatureSet{
		"empty":   {},
		"one-agg": {Enabled: []string{"AGG_SUM"}},
	} {
		got := BuildManifestForInstance(NewInstanceSnapshot(nil, set)).Intents
		if !slices.Equal(got, IntentIDs()) {
			t.Errorf("%s: instance manifest intents = %v, want %v", name, got, IntentIDs())
		}
	}
}

// injectPurposes swaps the purpose seam for the test's lifetime.
func injectPurposes(t *testing.T, ps map[string]descriptor.Purpose) {
	t.Helper()
	prev := purposeLookup
	purposeLookup = func(name string) (descriptor.Purpose, bool) {
		p, ok := ps[name]
		return p, ok
	}
	t.Cleanup(func() { purposeLookup = prev })
}

// TestManifestIntents_EntriesAndHidePath: a declared purpose stamps its
// sorted intent IDs on every kind of manifest entry, and a feature
// profile hiding the operator drops the entry — and so its intents —
// through the existing hide path while visible entries keep theirs.
func TestManifestIntents_EntriesAndHidePath(t *testing.T) {
	const (
		agg     = "AGG_SUM"
		aggKeep = "AGG_COUNT"
		test    = "TEST_ANOVA_WELCH" // has a tier-2 variant
		reg     = "REG_OLS"
		overlay = "OVERLAY_SHARE_OF_TOTAL"
	)
	injectPurposes(t, map[string]descriptor.Purpose{
		agg:         {Intents: []string{IntentDescribe}},
		aggKeep:     {Intents: []string{IntentDescribe, IntentDataQuality}},
		test:        {Intents: []string{IntentCompareGroups}},
		reg:         {Intents: []string{IntentDrivers}},
		overlay:     {Intents: []string{IntentComposition}},
		"lognormal": {Intents: []string{IntentSimulate}},
	})

	full := BuildManifest()
	findOp := func(ops []descriptor.Operator, n string) *descriptor.Operator {
		for i := range ops {
			if ops[i].Name == n {
				return &ops[i]
			}
		}
		return nil
	}
	if op := findOp(full.Components.Aggregators, agg); op == nil || !slices.Equal(op.Intents, []string{IntentDescribe}) {
		t.Errorf("%s intents = %v", agg, op)
	}
	if op := findOp(full.Components.Aggregators, aggKeep); op == nil || !slices.Equal(op.Intents, []string{IntentDataQuality, IntentDescribe}) {
		t.Errorf("%s intents not sorted / missing: %v", aggKeep, op)
	}
	tier2 := 0
	for _, tm := range append(append([]descriptor.TestMeta{}, full.Tests...), full.PostTests...) {
		if tm.Family == test || tm.Name == test {
			if !slices.Equal(tm.Intents, []string{IntentCompareGroups}) {
				t.Errorf("test entry %s intents = %v", tm.Name, tm.Intents)
			}
			if tm.Tier == 2 {
				tier2++
			}
		}
	}
	if tier2 == 0 {
		t.Errorf("no tier-2 %s entry carried its family's intents", test)
	}
	regOK := false
	for _, r := range full.Regressions {
		if r.Name == reg {
			regOK = slices.Equal(r.Intents, []string{IntentDrivers})
		}
	}
	if !regOK {
		t.Errorf("%s did not carry its intents", reg)
	}
	ovOK := false
	for _, o := range full.Overlays {
		if string(o.Kind) == overlay {
			ovOK = slices.Equal(o.Intents, []string{IntentComposition})
		}
	}
	if !ovOK {
		t.Errorf("%s did not carry its intents", overlay)
	}
	distOK := false
	for _, d := range full.SynthDistributions {
		if d.Name == "lognormal" {
			distOK = slices.Equal(d.Intents, []string{IntentSimulate})
		}
	}
	if !distOK {
		t.Error("lognormal distribution did not carry its intents")
	}

	// Hide every injected operator except aggKeep.
	hide := map[string]bool{agg: true, test: true, reg: true, overlay: true, featSynth: true}
	var enabled, hidden []string
	for _, n := range ReachedFeatureNames(buildinfo.Version()) {
		if hide[n] {
			hidden = append(hidden, n)
		} else {
			enabled = append(enabled, n)
		}
	}
	scoped := BuildManifestForInstance(NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: hidden}))
	b, err := json.Marshal(scoped)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, gone := range []string{`"` + IntentCompareGroups + `"`, `"` + IntentDrivers + `"`, `"` + IntentComposition + `"`, `"` + IntentSimulate + `"`} {
		// Each appears once in the top-level taxonomy; any further
		// occurrence is a hidden entry's leaked intents.
		if c := strings.Count(js, gone); c != 1 {
			t.Errorf("scoped manifest mentions %s %d times, want 1 (top-level only)", gone, c)
		}
	}
	if findOp(scoped.Components.Aggregators, agg) != nil {
		t.Errorf("hidden %s still listed", agg)
	}
	if op := findOp(scoped.Components.Aggregators, aggKeep); op == nil || !slices.Equal(op.Intents, []string{IntentDataQuality, IntentDescribe}) {
		t.Errorf("visible %s lost its intents under the profile: %v", aggKeep, op)
	}
	if !slices.Equal(scoped.Intents, IntentIDs()) {
		t.Errorf("scoped top-level intents = %v", scoped.Intents)
	}
}
