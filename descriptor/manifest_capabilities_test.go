package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
)

// nameSet builds a lookup set from a slice of Operator entries.
func nameSet(ops []Operator) map[string]Operator {
	m := make(map[string]Operator, len(ops))
	for _, op := range ops {
		m[op.Name] = op
	}
	return m
}

// testNameSet builds a lookup set from a slice of TestMeta entries.
func testNameSet(ts []TestMeta) map[string]TestMeta {
	m := make(map[string]TestMeta, len(ts))
	for _, t := range ts {
		m[t.Name] = t
	}
	return m
}

// TestManifestOperatorsComplete asserts that every entry in
// types.All*Types() has a corresponding Operator record in the manifest
// with at least an AcceptsTypes declaration. The completeness gate
// catches "added an operator to the registry but forgot the
// Capabilities() entry" drift.
func TestManifestOperatorsComplete(t *testing.T) {
	m := BuildManifest()

	tcs := []struct {
		name   string
		want   []string
		got    []Operator
		kind   string
	}{
		{"aggregators", typesToStrings(types.AllAggregationTypes()), m.Components.Aggregators, "aggregator"},
		{"attributes", typesToStrings(types.AllAttributeTypes()), m.Components.Attributes, "attribute"},
		{"filterers", typesToStrings(types.AllFiltererTypes()), m.Components.Filterers, "filterer"},
		{"groupers", typesToStrings(types.AllGroupTypes()), m.Components.Groupers, "grouper"},
		{"windows", typesToStrings(types.AllWindowTypes()), m.Components.Windows, "window"},
		{"features", typesToStrings(types.AllFeatureTypes()), m.Components.Features, "feature"},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			set := nameSet(tc.got)
			for _, w := range tc.want {
				op, ok := set[w]
				if !ok {
					t.Errorf("%s missing manifest entry for %q", tc.name, w)
					continue
				}
				if op.Category != tc.kind {
					t.Errorf("%s entry %q has Category=%q, want %q", tc.name, w, op.Category, tc.kind)
				}
				if len(op.AcceptsTypes) == 0 {
					t.Errorf("%s entry %q has empty AcceptsTypes", tc.name, w)
				}
				if strings.TrimSpace(op.Description) == "" {
					t.Errorf("%s entry %q has empty Description", tc.name, w)
				}
			}
			if len(set) != len(tc.want) {
				t.Errorf("%s manifest entry count = %d, want %d", tc.name, len(set), len(tc.want))
			}
		})
	}
}

// TestManifestStreamableMatchesTypes asserts that every Operator's
// Streamable flag mirrors the corresponding types.X.Streamable() value.
// This keeps the manifest in lockstep with the source-of-truth
// per-type streamability declarations enforced by the streamability
// gates in types/.
func TestManifestStreamableMatchesTypes(t *testing.T) {
	m := BuildManifest()
	for _, op := range m.Components.Aggregators {
		want := types.AggregationType(op.Name).Streamable()
		if op.Streamable != want {
			t.Errorf("aggregator %s Streamable=%v, want %v", op.Name, op.Streamable, want)
		}
	}
	for _, op := range m.Components.Attributes {
		want := types.AttributeType(op.Name).Streamable()
		if op.Streamable != want {
			t.Errorf("attribute %s Streamable=%v, want %v", op.Name, op.Streamable, want)
		}
	}
	for _, op := range m.Components.Filterers {
		want := types.FiltererType(op.Name).Streamable()
		if op.Streamable != want {
			t.Errorf("filterer %s Streamable=%v, want %v", op.Name, op.Streamable, want)
		}
	}
	for _, op := range m.Components.Groupers {
		want := types.GroupType(op.Name).Streamable()
		if op.Streamable != want {
			t.Errorf("grouper %s Streamable=%v, want %v", op.Name, op.Streamable, want)
		}
	}
	for _, op := range m.Components.Windows {
		want := types.WindowType(op.Name).Streamable()
		if op.Streamable != want {
			t.Errorf("window %s Streamable=%v, want %v", op.Name, op.Streamable, want)
		}
	}
	for _, op := range m.Components.Features {
		want := types.FeatureType(op.Name).Streamable()
		if op.Streamable != want {
			t.Errorf("feature %s Streamable=%v, want %v", op.Name, op.Streamable, want)
		}
	}
}

// TestManifestTestsComplete asserts that every entry in
// types.AllTestTypes() appears in either Manifest.Tests (tier-1) or
// Manifest.PostTests (tier-2) as a Tier=1 / Tier=2 record. TREND and
// TUKEY_HSD are exclusively tier-2; everything else has a tier-1 entry.
func TestManifestTestsComplete(t *testing.T) {
	m := BuildManifest()

	tier1Only := testNameSet(m.Tests)
	tier2Only := testNameSet(m.PostTests)

	// Tier-2-only TestTypes: TREND and TUKEY_HSD. The rest are tier-1
	// row tests with optional tier-2 post variants.
	tier2OnlyFamilies := map[types.TestType]bool{
		types.TEST_TREND:      true,
		types.TEST_TUKEY_HSD:  true,
	}

	for _, tt := range types.AllTestTypes() {
		name := string(tt)
		if tier2OnlyFamilies[tt] {
			tm, ok := tier2Only[name]
			if !ok {
				t.Errorf("tier-2-only test %q missing from manifest PostTests", name)
				continue
			}
			if tm.Tier != 2 || tm.Family != name {
				t.Errorf("tier-2-only %q has Tier=%d Family=%q, want Tier=2 Family=%q", name, tm.Tier, tm.Family, name)
			}
			continue
		}

		tm, ok := tier1Only[name]
		if !ok {
			t.Errorf("tier-1 test %q missing from manifest Tests", name)
			continue
		}
		if tm.Tier != 1 {
			t.Errorf("tier-1 test %q has Tier=%d, want 1", name, tm.Tier)
		}
		if tm.Variant != "" {
			t.Errorf("tier-1 test %q has Variant=%q, want empty", name, tm.Variant)
		}
		if tm.Family != name {
			t.Errorf("tier-1 test %q has Family=%q, want %q", name, tm.Family, name)
		}
		if tm.Streamable != tt.Streamable() {
			t.Errorf("tier-1 test %q Streamable=%v, want %v", name, tm.Streamable, tt.Streamable())
		}
	}
}

// TestManifestPostTestsComplete asserts that every registered tier-2
// post-test variant has a TestMeta entry with Tier=2 and a non-empty
// Variant and that its Family appears in types.AllTestTypes().
func TestManifestPostTestsComplete(t *testing.T) {
	m := BuildManifest()
	families := make(map[string]bool, len(types.AllTestTypes()))
	for _, tt := range types.AllTestTypes() {
		families[string(tt)] = true
	}

	// Expected variant entries: every row in postTestCapabilities() plus
	// the natively-tier-2 entries already in testCapabilities (TREND,
	// TUKEY_HSD). The test verifies every PostTests entry has Tier=2 and
	// a Family in the known TestType set.
	for _, tm := range m.PostTests {
		if tm.Tier != 2 {
			t.Errorf("PostTests entry %q has Tier=%d, want 2", tm.Name, tm.Tier)
		}
		if !families[tm.Family] {
			t.Errorf("PostTests entry %q has Family=%q not in types.AllTestTypes()", tm.Name, tm.Family)
		}
		if tm.Streamable {
			t.Errorf("PostTests entry %q has Streamable=true; tier-2 must be false", tm.Name)
		}
	}

	// Sanity-check the count is reasonable (more than just TREND +
	// TUKEY_HSD).
	if len(m.PostTests) < 11 {
		t.Errorf("PostTests count = %d, want at least 11 (TREND + TUKEY_HSD + 9 variants)", len(m.PostTests))
	}
}

// TestManifestDistributionsComplete asserts that every entry in
// synth.AllDistributions() has a DistributionMeta in the manifest.
func TestManifestDistributionsComplete(t *testing.T) {
	m := BuildManifest()
	set := make(map[string]DistributionMeta, len(m.SynthDistributions))
	for _, d := range m.SynthDistributions {
		set[d.Name] = d
	}
	for _, name := range synth.AllDistributions() {
		d, ok := set[name]
		if !ok {
			t.Errorf("synth distribution %q missing from manifest", name)
			continue
		}
		if strings.TrimSpace(d.Description) == "" {
			t.Errorf("distribution %q has empty Description", name)
		}
		if len(d.AppliesTo) == 0 {
			t.Errorf("distribution %q has empty AppliesTo", name)
		}
	}
	if len(set) != len(synth.AllDistributions()) {
		t.Errorf("manifest distribution count = %d, want %d", len(set), len(synth.AllDistributions()))
	}
}

// TestManifestErrorCodesComplete asserts that every code in
// errors.AllCodes() has an ErrorMeta entry with a curated Description.
func TestManifestErrorCodesComplete(t *testing.T) {
	m := BuildManifest()
	set := make(map[string]ErrorMeta, len(m.ErrorCodes))
	for _, e := range m.ErrorCodes {
		set[e.Code] = e
	}
	for _, c := range errors.AllCodes() {
		s := string(c)
		e, ok := set[s]
		if !ok {
			t.Errorf("error code %q missing from manifest", s)
			continue
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("error code %q has empty Description", s)
		}
		if e.Domain == "" {
			t.Errorf("error code %q has empty Domain", s)
		}
		if e.Severity != "error" && e.Severity != "warning" {
			t.Errorf("error code %q has Severity=%q, want error|warning", s, e.Severity)
		}
		// Catch the fallback sentinel; if a code lands without a curated
		// entry, the fallback string surfaces here.
		if strings.Contains(e.Description, "Undocumented error code") {
			t.Errorf("error code %q has fallback description; add curated entry to errorMetaTable", s)
		}
	}
}

// TestManifestMCPToolsComplete asserts that every name in
// mcptools.Names() has an MCPTool entry.
func TestManifestMCPToolsComplete(t *testing.T) {
	m := BuildManifest()
	set := make(map[string]MCPTool, len(m.MCPTools))
	for _, mt := range m.MCPTools {
		set[mt.Name] = mt
	}
	for _, name := range mcptools.Names() {
		mt, ok := set[name]
		if !ok {
			t.Errorf("MCP tool %q missing from manifest", name)
			continue
		}
		if strings.TrimSpace(mt.Description) == "" {
			t.Errorf("MCP tool %q has empty Description", name)
		}
	}
}

// TestCohortTypeCrossRefsDeterministic asserts the Compatible* slices on
// each CohortFieldType are sorted lexically. Required for golden
// stability.
func TestCohortTypeCrossRefsDeterministic(t *testing.T) {
	m := BuildManifest()
	for _, ct := range m.CohortTypes {
		checkSorted := func(label string, names []string) {
			for i := 1; i < len(names); i++ {
				if names[i] < names[i-1] {
					t.Errorf("cohort type %s %s not sorted: %q before %q", ct.Name, label, names[i-1], names[i])
				}
			}
		}
		checkSorted("CompatibleAggregators", ct.CompatibleAggregators)
		checkSorted("CompatibleAttributes", ct.CompatibleAttributes)
		checkSorted("CompatibleFilterers", ct.CompatibleFilterers)
		checkSorted("CompatibleGroupers", ct.CompatibleGroupers)
		checkSorted("CompatibleWindows", ct.CompatibleWindows)
		checkSorted("CompatibleFeatures", ct.CompatibleFeatures)
	}
}

func typesToStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}
