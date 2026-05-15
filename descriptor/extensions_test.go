package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// extensionsManifestFromSnapshot populated path — covers
// sortOperatorMeta / sortExprFunctionMeta / sortLookupTableMeta + the
// "nil-out -> empty slice" coercions.

func TestExtensionsManifestFromSnapshot_NilYieldsEmpty(t *testing.T) {
	got := extensionsManifestFromSnapshot(nil)
	// Every slice must be a populated (non-nil) zero-length slice.
	if got.Aggregators == nil || got.Attributes == nil || got.Filterers == nil ||
		got.Groupers == nil || got.Windows == nil || got.Features == nil ||
		got.Tests == nil || got.SynthDistributions == nil ||
		got.ExprFunctions == nil || got.LookupTables == nil {
		t.Fatalf("nil snapshot must produce empty (non-nil) slices: %#v", got)
	}
	if len(got.Aggregators)+len(got.Attributes)+len(got.Filterers)+len(got.Groupers)+
		len(got.Windows)+len(got.Features)+len(got.Tests)+len(got.SynthDistributions)+
		len(got.ExprFunctions)+len(got.LookupTables) != 0 {
		t.Errorf("nil snapshot produced non-empty slices: %#v", got)
	}
}

func TestExtensionsManifestFromSnapshot_PopulatedSortsByName(t *testing.T) {
	snap := &ExtensionsSnapshot{
		Aggregators: []OperatorMeta{
			{Name: "AGG_ACME_Z"},
			{Name: "AGG_ACME_A"},
			{Name: "AGG_ACME_M"},
		},
		Attributes: []OperatorMeta{{Name: "ATTR_ACME_B"}, {Name: "ATTR_ACME_A"}},
		Filterers:  []OperatorMeta{{Name: "FILTER_ACME_B"}, {Name: "FILTER_ACME_A"}},
		Groupers:   []OperatorMeta{{Name: "GROUP_ACME_B"}, {Name: "GROUP_ACME_A"}},
		Windows:    []OperatorMeta{{Name: "WIN_ACME_B"}, {Name: "WIN_ACME_A"}},
		Features:   []OperatorMeta{{Name: "FEAT_ACME_B"}, {Name: "FEAT_ACME_A"}},
		Tests:      []OperatorMeta{{Name: "TEST_ACME_B"}, {Name: "TEST_ACME_A"}},
		SynthDistributions: []OperatorMeta{
			{Name: "SYNTH_ACME_B"}, {Name: "SYNTH_ACME_A"},
		},
		ExprFunctions: []ExprFunctionMeta{
			{Name: "zfunc"}, {Name: "afunc"}, {Name: "mfunc"},
		},
		LookupTables: []LookupTableMeta{
			{Name: "ztable"}, {Name: "atable"},
		},
	}
	got := extensionsManifestFromSnapshot(snap)
	if got.Aggregators[0].Name != "AGG_ACME_A" || got.Aggregators[2].Name != "AGG_ACME_Z" {
		t.Errorf("Aggregators not sorted: %v", got.Aggregators)
	}
	if got.Attributes[0].Name != "ATTR_ACME_A" {
		t.Errorf("Attributes not sorted: %v", got.Attributes)
	}
	if got.Filterers[0].Name != "FILTER_ACME_A" {
		t.Errorf("Filterers not sorted: %v", got.Filterers)
	}
	if got.Groupers[0].Name != "GROUP_ACME_A" {
		t.Errorf("Groupers not sorted: %v", got.Groupers)
	}
	if got.Windows[0].Name != "WIN_ACME_A" {
		t.Errorf("Windows not sorted: %v", got.Windows)
	}
	if got.Features[0].Name != "FEAT_ACME_A" {
		t.Errorf("Features not sorted: %v", got.Features)
	}
	if got.Tests[0].Name != "TEST_ACME_A" {
		t.Errorf("Tests not sorted: %v", got.Tests)
	}
	if got.SynthDistributions[0].Name != "SYNTH_ACME_A" {
		t.Errorf("SynthDistributions not sorted: %v", got.SynthDistributions)
	}
	if got.ExprFunctions[0].Name != "afunc" || got.ExprFunctions[2].Name != "zfunc" {
		t.Errorf("ExprFunctions not sorted: %v", got.ExprFunctions)
	}
	if got.LookupTables[0].Name != "atable" {
		t.Errorf("LookupTables not sorted: %v", got.LookupTables)
	}
}

// isExtensionTestType — 0% before. Covers all three branches:
// nil opts, opts with no extensions, opts with matching test name.

func TestIsExtensionTestType_NilOpts(t *testing.T) {
	if isExtensionTestType(nil, "TEST_ACME_FOO") {
		t.Error("isExtensionTestType(nil, ...) must be false")
	}
}

func TestIsExtensionTestType_NoExtensions(t *testing.T) {
	opts := &PredictOptions{}
	if isExtensionTestType(opts, "TEST_ACME_FOO") {
		t.Error("isExtensionTestType with no Extensions must be false")
	}
}

func TestIsExtensionTestType_Match(t *testing.T) {
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Tests: []OperatorMeta{{Name: "TEST_ACME_FOO"}},
		},
	}
	if !isExtensionTestType(opts, "TEST_ACME_FOO") {
		t.Error("isExtensionTestType must match overlay-registered test")
	}
	if isExtensionTestType(opts, "TEST_ACME_BAR") {
		t.Error("isExtensionTestType must not match absent name")
	}
}

// isExtensionWindowType / isExtensionFeatureType nil-opts branch — was
// 75% before; pushes to 100%.

func TestIsExtensionWindowType_NilOpts(t *testing.T) {
	if isExtensionWindowType(nil, "WIN_ACME_FOO") {
		t.Error("isExtensionWindowType(nil, ...) must be false")
	}
}

func TestIsExtensionWindowType_Match(t *testing.T) {
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Windows: []OperatorMeta{{Name: "WIN_ACME_FOO"}},
		},
	}
	if !isExtensionWindowType(opts, "WIN_ACME_FOO") {
		t.Error("isExtensionWindowType must match overlay-registered window")
	}
	if isExtensionWindowType(opts, "WIN_ACME_BAR") {
		t.Error("isExtensionWindowType must not match absent name")
	}
}

func TestIsExtensionFeatureType_NilOpts(t *testing.T) {
	if isExtensionFeatureType(nil, "FEAT_ACME_FOO") {
		t.Error("isExtensionFeatureType(nil, ...) must be false")
	}
}

func TestIsExtensionFeatureType_Match(t *testing.T) {
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Features: []OperatorMeta{{Name: "FEAT_ACME_FOO"}},
		},
	}
	if !isExtensionFeatureType(opts, "FEAT_ACME_FOO") {
		t.Error("isExtensionFeatureType must match overlay-registered feature")
	}
	if isExtensionFeatureType(opts, "FEAT_ACME_BAR") {
		t.Error("isExtensionFeatureType must not match absent name")
	}
}

// extensionStreamable — was 17.6% (only one category branch hit).
// Sweep all 7 categories plus the "unknown" + "nil snapshot" paths.

func TestExtensionStreamable_AllCategories(t *testing.T) {
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Aggregators: []OperatorMeta{{Name: "AGG_ACME_A", Streamable: true}},
			Attributes:  []OperatorMeta{{Name: "ATTR_ACME_A", Streamable: false}},
			Filterers:   []OperatorMeta{{Name: "FILTER_ACME_A", Streamable: true}},
			Groupers:    []OperatorMeta{{Name: "GROUP_ACME_A", Streamable: false}},
			Windows:     []OperatorMeta{{Name: "WIN_ACME_A", Streamable: true}},
			Features:    []OperatorMeta{{Name: "FEAT_ACME_A", Streamable: false}},
			Tests:       []OperatorMeta{{Name: "TEST_ACME_A", Streamable: true}},
		},
	}
	cases := []struct {
		cat, name string
		want      bool
	}{
		{"aggregator", "AGG_ACME_A", true},
		{"attribute", "ATTR_ACME_A", false},
		{"filterer", "FILTER_ACME_A", true},
		{"grouper", "GROUP_ACME_A", false},
		{"window", "WIN_ACME_A", true},
		{"feature", "FEAT_ACME_A", false},
		{"test", "TEST_ACME_A", true},
	}
	for _, c := range cases {
		got, found := extensionStreamable(opts, c.cat, c.name)
		if !found {
			t.Errorf("extensionStreamable(%q, %q) not found", c.cat, c.name)
			continue
		}
		if got != c.want {
			t.Errorf("extensionStreamable(%q, %q) = %v, want %v", c.cat, c.name, got, c.want)
		}
	}
}

func TestExtensionStreamable_UnknownCategory(t *testing.T) {
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Aggregators: []OperatorMeta{{Name: "AGG_ACME_A", Streamable: true}},
		},
	}
	if _, found := extensionStreamable(opts, "not-a-category", "AGG_ACME_A"); found {
		t.Error("extensionStreamable on unknown category must return found=false")
	}
}

func TestExtensionStreamable_NilSnapshot(t *testing.T) {
	if _, found := extensionStreamable(nil, "aggregator", "AGG_ACME_A"); found {
		t.Error("extensionStreamable with nil opts must return found=false")
	}
}

func TestExtensionStreamable_NameMissingInCategory(t *testing.T) {
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Aggregators: []OperatorMeta{{Name: "AGG_ACME_A", Streamable: true}},
		},
	}
	if _, found := extensionStreamable(opts, "aggregator", "AGG_ACME_NOTHERE"); found {
		t.Error("extensionStreamable on missing name must return found=false")
	}
}

// streamableWithOverlay fall-through path — overlay miss returns
// caller-supplied builtin value.

func TestStreamableWithOverlay_FallsThroughToBuiltin(t *testing.T) {
	opts := &PredictOptions{Extensions: &ExtensionsSnapshot{}}
	if !streamableWithOverlay(opts, "aggregator", string(types.AGG_COUNT), true) {
		t.Error("overlay miss must return builtin=true")
	}
	if streamableWithOverlay(opts, "aggregator", string(types.AGG_COUNT), false) {
		t.Error("overlay miss must return builtin=false")
	}
}

func TestStreamableWithOverlay_OverlayHitWins(t *testing.T) {
	// Overlay declares streamable=false; caller's builtin=true must
	// be overridden.
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Aggregators: []OperatorMeta{{Name: "AGG_ACME_X", Streamable: false}},
		},
	}
	if streamableWithOverlay(opts, "aggregator", "AGG_ACME_X", true) {
		t.Error("overlay false must override builtin true")
	}
	// Inverse: overlay true, builtin false.
	opts2 := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			Aggregators: []OperatorMeta{{Name: "AGG_ACME_Y", Streamable: true}},
		},
	}
	if !streamableWithOverlay(opts2, "aggregator", "AGG_ACME_Y", false) {
		t.Error("overlay true must override builtin false")
	}
}

func TestExtensionsManifestFromSnapshot_PartialPopulationCoercesEmpty(t *testing.T) {
	// Only Aggregators populated; every other slice must surface as
	// empty (non-nil) — exercises the per-category nil-coercion fan in
	// extensionsManifestFromSnapshot.
	snap := &ExtensionsSnapshot{
		Aggregators: []OperatorMeta{{Name: "AGG_ACME_X"}},
	}
	got := extensionsManifestFromSnapshot(snap)
	if len(got.Aggregators) != 1 {
		t.Errorf("Aggregators = %v, want 1 entry", got.Aggregators)
	}
	for name, s := range map[string]any{
		"Attributes":         got.Attributes,
		"Filterers":          got.Filterers,
		"Groupers":           got.Groupers,
		"Windows":            got.Windows,
		"Features":           got.Features,
		"Tests":              got.Tests,
		"SynthDistributions": got.SynthDistributions,
		"ExprFunctions":      got.ExprFunctions,
		"LookupTables":       got.LookupTables,
	} {
		switch v := s.(type) {
		case []OperatorMeta:
			if v == nil {
				t.Errorf("%s is nil; must be empty slice", name)
			}
		case []ExprFunctionMeta:
			if v == nil {
				t.Errorf("%s is nil; must be empty slice", name)
			}
		case []LookupTableMeta:
			if v == nil {
				t.Errorf("%s is nil; must be empty slice", name)
			}
		}
	}
}

// snapshotHasName direct coverage — empty + populated.

func TestSnapshotHasName_EmptyAndHit(t *testing.T) {
	if snapshotHasName(nil, "X") {
		t.Error("nil metas must report no match")
	}
	metas := []OperatorMeta{{Name: "A"}, {Name: "B"}}
	if !snapshotHasName(metas, "B") {
		t.Error("snapshotHasName must find B")
	}
	if snapshotHasName(metas, "C") {
		t.Error("snapshotHasName must not find C")
	}
}
