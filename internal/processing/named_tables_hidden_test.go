package processing

import (
	"slices"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// TestNamedTableFeatureNames pins the engine's spelling of the named
// tables' capabilities to the feature table's.
func TestNamedTableFeatureNames(t *testing.T) {
	for _, tc := range []struct{ engine, bare string }{
		{featureLabels, "labels"},
		{featureRangeTables, "range_tables"},
	} {
		want := descx.FeatureName(descx.FeatureKindCapability, tc.bare)
		if tc.engine != want {
			t.Errorf("engine spells %q, feature table %q", tc.engine, want)
		}
		if !slices.Contains(descx.FeatureNames(), want) {
			t.Errorf("%s is not a feature", want)
		}
	}
}

// TestNamedTables_HiddenCapabilityMisses: a registered label / range
// table misses on a registry whose instance hides its capability, and
// resolves when only the other capability is hidden.
func TestNamedTables_HiddenCapabilityMisses(t *testing.T) {
	base := &ExtensionRegistry{
		LabelTables: map[string]LabelTable{"lt": {Rows: map[string]string{"1": "one"}}},
		RangeTables: map[string]RangeTable{"rt": {}},
	}
	hide := func(names ...string) *ExtensionRegistry {
		return base.WithHidden(func(n string) bool { return slices.Contains(names, n) })
	}
	cases := []struct {
		name       string
		r          *ExtensionRegistry
		label, rng bool
	}{
		{"unscoped", base, true, true},
		{"labels hidden", hide(featureLabels), false, true},
		{"range tables hidden", hide(featureRangeTables), true, false},
		{"both hidden", hide(featureLabels, featureRangeTables), false, false},
	}
	for _, tc := range cases {
		if _, ok := tc.r.LookupLabelTable("lt"); ok != tc.label {
			t.Errorf("%s: LookupLabelTable ok=%v, want %v", tc.name, ok, tc.label)
		}
		if _, ok := tc.r.LookupRangeTable("rt"); ok != tc.rng {
			t.Errorf("%s: LookupRangeTable ok=%v, want %v", tc.name, ok, tc.rng)
		}
	}
}
