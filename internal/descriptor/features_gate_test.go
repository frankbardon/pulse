package descriptor_test

import (
	"sort"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/internal/synth"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/types"
)

// This file is an external test package because the prompt registry lives
// in mcp/gosdk, which imports the root pulse package, which imports this
// package — an in-package test could not import it without a cycle.

// wantCapabilities is the approved capability inventory (interview
// decision "Capability inventory"). A capability is authored, not
// discovered, so the gate pins the exact list.
var wantCapabilities = []string{
	"process", "stream", "watch", "compose", "compose_sweep", "process_chain", "facet",
	"sample", "joins", "crosstab", "lookup", "index", "shard", "import",
	"export", "filter_to_file", "dedup", "widen", "templates", "synth",
	"labels", "range_tables", "weighting", "multiplicity", "matrices",
	"recommend", "explain",
}

// wantOperators collects every registered operator name across the
// types.All*Types() enums.
func wantOperators() []string {
	var out []string
	for _, v := range types.AllAggregationTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllAttributeTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllFiltererTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllGroupTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllWindowTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllFeatureTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllTestTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllRegressionTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllMatrixTypes() {
		out = append(out, string(v))
	}
	for _, v := range types.AllOverlayKinds() {
		out = append(out, string(v))
	}
	return out
}

// wantFeatures is the full expected table, keyed by name → kind, derived
// from the live registries plus the authored capability list.
func wantFeatures(t *testing.T) map[string]descx.FeatureKind {
	t.Helper()
	want := map[string]descx.FeatureKind{}
	add := func(name string, k descx.FeatureKind) {
		if prev, dup := want[name]; dup {
			t.Fatalf("registry name %q appears twice (kinds %s, %s): the flat feature namespace cannot hold it", name, prev, k)
		}
		want[name] = k
	}
	for _, c := range wantCapabilities {
		add(descx.FeatureName(descx.FeatureKindCapability, c), descx.FeatureKindCapability)
	}
	for _, f := range pio.Formats() {
		add(descx.FeatureName(descx.FeatureKindIOFormat, string(f)), descx.FeatureKindIOFormat)
	}
	add(descx.FeatureName(descx.FeatureKindMCPExtra, "cohort_resources"), descx.FeatureKindMCPExtra)
	prompts := descx.MCPPromptFeatures()
	for _, p := range gosdk.RegisteredPrompts() {
		feat, ok := prompts[p]
		if !ok {
			t.Errorf("MCP prompt %q has no mcp_extra binding in internal/descriptor/features.go (mcpPromptFeatures)", p)
			continue
		}
		add(feat, descx.FeatureKindMCPExtra)
	}
	for _, o := range wantOperators() {
		add(o, descx.FeatureKindOperator)
	}
	return want
}

// TestFeaturesHaveSince is the completeness gate for the internal feature
// table: every registered operator, I/O format, MCP prompt and approved
// capability has exactly one row with a parseable Since, there is no row
// without a live registry entry, and every MCP tool is bound to an
// existing capability or to the always-present core.
func TestFeaturesHaveSince(t *testing.T) {
	want := wantFeatures(t)

	seen := map[string]bool{}
	for _, f := range descx.Features() {
		if seen[f.Name] {
			t.Errorf("feature %q has more than one row", f.Name)
		}
		seen[f.Name] = true

		if _, _, _, ok := descx.ParseSince(f.Since); !ok {
			t.Errorf("feature %q has unparseable Since %q (want major.minor.patch)", f.Name, f.Since)
		}
		if f.Since != descx.BuiltinFeatureSince {
			t.Errorf("built-in feature %q has Since %q, want %q", f.Name, f.Since, descx.BuiltinFeatureSince)
		}
		if f.Name != descx.FeatureName(f.Kind, strings.TrimPrefix(f.Name, string(f.Kind)+":")) {
			t.Errorf("feature %q is not spelled for its kind %s", f.Name, f.Kind)
		}

		k, ok := want[f.Name]
		if !ok {
			t.Errorf("feature %q (%s) has no live registry entry — remove the row or register the surface", f.Name, f.Kind)
			continue
		}
		if k != f.Kind {
			t.Errorf("feature %q has kind %s, want %s", f.Name, f.Kind, k)
		}
	}
	var missing []string
	for name := range want {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("registry entry %q (%s) has no row in internal/descriptor/features.go", name, want[name])
	}

	// Core surfaces are not features and must never collide with one.
	for _, c := range descx.CoreSurfaces() {
		for _, k := range descx.AllFeatureKinds() {
			if _, clash := descx.LookupFeature(descx.FeatureName(k, c)); clash {
				t.Errorf("core surface %q collides with feature %q", c, descx.FeatureName(k, c))
			}
		}
	}

	// MCP tool bindings: both directions against toolmeta.
	tools := map[string]bool{}
	for _, n := range toolmeta.Names() {
		tools[n] = true
		if _, ok := descx.MCPToolBindingOf(n); !ok {
			t.Errorf("MCP tool %q has no binding in internal/descriptor/features.go (mcpToolBindings)", n)
		}
	}
	bound := map[string]bool{}
	for _, b := range descx.MCPToolBindings() {
		if bound[b.Tool] {
			t.Errorf("MCP tool %q is bound twice", b.Tool)
		}
		bound[b.Tool] = true
		if !tools[b.Tool] {
			t.Errorf("binding names MCP tool %q, which toolmeta does not register", b.Tool)
		}
		switch {
		case (b.Feature == "") == (b.Core == ""):
			t.Errorf("MCP tool %q binding must set exactly one of Feature and Core", b.Tool)
		case b.Core != "":
			if !descx.IsCoreSurface(b.Core) {
				t.Errorf("MCP tool %q is bound to unknown core surface %q", b.Tool, b.Core)
			}
		default:
			if k, ok := descx.FeatureKindOf(b.Feature); !ok || k != descx.FeatureKindCapability {
				t.Errorf("MCP tool %q is bound to %q, which is not an existing capability", b.Tool, b.Feature)
			}
		}
	}
}

// TestFeatures_NonFeaturesAbsent pins what the table must NOT carry:
// synth distributions, tier-qualified TEST_X/variant spellings and
// kind-prefixed operator spellings.
func TestFeatures_NonFeaturesAbsent(t *testing.T) {
	for _, d := range synth.AllDistributions() {
		for _, k := range descx.AllFeatureKinds() {
			if _, ok := descx.LookupFeature(descx.FeatureName(k, d)); ok {
				t.Errorf("synth distribution %q is a feature; capability:synth gates distributions", d)
			}
		}
	}
	for _, f := range descx.Features() {
		if strings.Contains(f.Name, "/") {
			t.Errorf("feature %q is tier-qualified; TEST_X is one row covering both tiers", f.Name)
		}
		if strings.HasPrefix(f.Name, "operator:") || strings.HasPrefix(f.Name, "SYNTH_") {
			t.Errorf("feature %q is not a valid built-in spelling", f.Name)
		}
	}
	if _, ok := descx.LookupFeature("operator:AGG_SUM"); ok {
		t.Error(`"operator:AGG_SUM" resolved; operators are spelled bare`)
	}
}

// TestFeatures_LookupAPI exercises the lookup surface later stories build
// on: by name, all names, kind-of, core membership.
func TestFeatures_LookupAPI(t *testing.T) {
	cases := []struct {
		name string
		kind descx.FeatureKind
	}{
		{"AGG_SUM", descx.FeatureKindOperator},
		{"TEST_ANOVA_F", descx.FeatureKindOperator},
		{"OVERLAY_T_CELL", descx.FeatureKindOperator},
		{"REG_OLS", descx.FeatureKindOperator},
		{"capability:process", descx.FeatureKindCapability},
		{"capability:range_tables", descx.FeatureKindCapability},
		{"io_format:spss", descx.FeatureKindIOFormat},
		{"mcp_extra:cohort_resources", descx.FeatureKindMCPExtra},
	}
	for _, c := range cases {
		f, ok := descx.LookupFeature(c.name)
		if !ok || f.Name != c.name || f.Kind != c.kind {
			t.Errorf("LookupFeature(%q) = %+v, %v; want kind %s", c.name, f, ok, c.kind)
		}
		if k, ok := descx.FeatureKindOf(c.name); !ok || k != c.kind {
			t.Errorf("FeatureKindOf(%q) = %s, %v; want %s", c.name, k, ok, c.kind)
		}
	}
	for _, bad := range []string{"process", "csv", "inspect", "capability:inspect", "agg_sum", ""} {
		if _, ok := descx.LookupFeature(bad); ok {
			t.Errorf("LookupFeature(%q) resolved; want unknown", bad)
		}
	}

	names := descx.FeatureNames()
	if len(names) != len(descx.Features()) {
		t.Fatalf("FeatureNames has %d entries, Features %d", len(names), len(descx.Features()))
	}
	for i, f := range descx.Features() {
		if names[i] != f.Name {
			t.Fatalf("FeatureNames()[%d] = %q, want %q", i, names[i], f.Name)
		}
	}

	wantCore := []string{"open", "inspect", "predict", "count_records", "manifest", "payload_schema", "skills", "examples", "errors_lookup", "cohort_artifacts"}
	got := descx.CoreSurfaces()
	if strings.Join(got, ",") != strings.Join(wantCore, ",") {
		t.Errorf("CoreSurfaces() = %v, want %v", got, wantCore)
	}
	for _, c := range wantCore {
		if !descx.IsCoreSurface(c) {
			t.Errorf("IsCoreSurface(%q) = false", c)
		}
	}
	if descx.IsCoreSurface("process") {
		t.Error(`IsCoreSurface("process") = true; process is a capability`)
	}
}

func TestParseSince(t *testing.T) {
	good := map[string][3]int{"1.0.0": {1, 0, 0}, "10.20.300": {10, 20, 300}}
	for s, w := range good {
		a, b, c, ok := descx.ParseSince(s)
		if !ok || [3]int{a, b, c} != w {
			t.Errorf("ParseSince(%q) = %d.%d.%d, %v; want %v", s, a, b, c, ok, w)
		}
	}
	for _, s := range []string{"", "1", "1.0", "1.0.0.0", "v1.0.0", "1.0.0-alpha.1", "1.0.0+meta", "1.-1.0", "1..0", "devel", "1.x.0"} {
		if _, _, _, ok := descx.ParseSince(s); ok {
			t.Errorf("ParseSince(%q) accepted; want refused", s)
		}
	}
}
