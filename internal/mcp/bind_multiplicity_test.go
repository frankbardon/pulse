package mcp

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
)

// multiplicityFeatures is a profile with every multiplicity host (the
// request, compose, chain and facet roots, tests and overlays), without
// capability:multiplicity.
var multiplicityFeatures = []string{
	"capability:process", "capability:compose", "capability:process_chain", "capability:facet", "capability:crosstab",
	"AGG_SUM", "AGG_WELFORD", "GROUP_CATEGORY", "TEST_T", "OVERLAY_SHARE_OF_ROW", "OVERLAY_T_VS_REF", "OVERLAY_ZSCORE_VS_POP",
}

func multiplicityInstance(t *testing.T, with bool) *descx.InstanceSnapshot {
	t.Helper()
	features := append([]string(nil), multiplicityFeatures...)
	if with {
		features = append(features, descx.FeatureMultiplicity)
	}
	_, inst := instanceFor(t, &pulse.FeatureProfile{Features: features}, pulse.Extensions{})
	return inst
}

// multiplicityPaths are the `multiplicity` properties every bound tool
// schema carries when the instance offers capability:multiplicity — the
// Request root, tests / post-tests, the Request and Facet overlay hosts,
// the ComposedRequest root and its Compose overlays — reached through
// pulse_predict's alternative roots too.
var multiplicityPaths = map[string][]string{
	toolmeta.ToolProcess: {
		"properties.multiplicity",
		"properties.tests.items.properties.multiplicity",
		"properties.post_tests.items.properties.multiplicity",
		"properties.overlays.items.properties.multiplicity",
	},
	toolmeta.ToolCompose: {
		"properties.multiplicity",
		"properties.overlays.items.properties.multiplicity",
		"properties.requests.items.properties.multiplicity",
		"properties.requests.items.properties.tests.items.properties.multiplicity",
	},
	toolmeta.ToolProcessChain: {
		"properties.stages.items.properties.request.properties.multiplicity",
	},
	toolmeta.ToolFacetSchema: {
		"properties.overlays.items.properties.multiplicity",
	},
	toolmeta.ToolPredict: {
		"properties.multiplicity",
		"properties.tests.items.properties.multiplicity",
		"properties.composed.properties.multiplicity",
		"properties.composed.properties.overlays.items.properties.multiplicity",
		"properties.facet.properties.overlays.items.properties.multiplicity",
		"properties.chain.properties.stages.items.properties.request.properties.multiplicity",
	},
}

// schemaAt walks a dotted path of object keys ("items" included).
func schemaAt(node map[string]any, path string) map[string]any {
	for _, k := range strings.Split(path, ".") {
		next, _ := node[k].(map[string]any)
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

// TestBindForInstance_MultiplicityAdvertised: an instance offering
// capability:multiplicity advertises the `multiplicity` block at every
// slot the engine accepts it — profile-free and profiled alike — with
// the closed method vocabulary and the surface's own families.
func TestBindForInstance_MultiplicityAdvertised(t *testing.T) {
	schema := makeBindSchema()
	_, free := instanceFor(t, nil, pulse.Extensions{})
	for label, inst := range map[string]*descx.InstanceSnapshot{"profile-free": free, "profiled": multiplicityInstance(t, true)} {
		bound, err := BindForInstance(schema, inst)
		if err != nil {
			t.Fatal(err)
		}
		for tool, paths := range multiplicityPaths {
			root := decodeBoundRequest(t, bound[tool])
			for _, p := range paths {
				m := schemaAt(root, p)
				if m == nil {
					t.Errorf("%s %s: no %s", label, tool, p)
					continue
				}
				method := schemaAt(m, "properties.method")
				if got, want := toStrings(method["enum"]), stringSlice(types.AllMultiplicityMethods()); !slices.Equal(got, want) {
					t.Errorf("%s %s %s: method enum %v, want %v", label, tool, p, got, want)
				}
				if schemaAt(m, "properties.family") == nil {
					t.Errorf("%s %s %s: no family property", label, tool, p)
				}
			}
		}
	}
}

// TestBindForInstance_MultiplicitySurfaceFamilies: each block lists only
// the families its surface accepts, and a test's block carries no
// `alpha` (a test reads its own Test.alpha; one set on its block is
// refused).
func TestBindForInstance_MultiplicitySurfaceFamilies(t *testing.T) {
	bound, err := BindForInstance(makeBindSchema(), multiplicityInstance(t, true))
	if err != nil {
		t.Fatal(err)
	}
	all := stringSlice(types.AllMultiplicityFamilies())
	cases := []struct {
		tool, path string
		families   []string
		alpha      bool
	}{
		{toolmeta.ToolProcess, "properties.multiplicity", all, true},
		{toolmeta.ToolProcess, "properties.tests.items.properties.multiplicity", []string{"request", "compose"}, false},
		{toolmeta.ToolProcess, "properties.overlays.items.properties.multiplicity", []string{"layer", "row", "column", "request", "compose"}, true},
		{toolmeta.ToolCompose, "properties.multiplicity", all, true},
		{toolmeta.ToolCompose, "properties.overlays.items.properties.multiplicity", []string{"layer", "row", "column", "compose"}, true},
		{toolmeta.ToolFacetSchema, "properties.overlays.items.properties.multiplicity", []string{"layer", "row", "column"}, true},
	}
	for _, c := range cases {
		m := schemaAt(decodeBoundRequest(t, bound[c.tool]), c.path)
		if m == nil {
			t.Errorf("%s: no %s", c.tool, c.path)
			continue
		}
		if got := toStrings(schemaAt(m, "properties.family")["enum"]); !slices.Equal(got, c.families) {
			t.Errorf("%s %s: family enum %v, want %v", c.tool, c.path, got, c.families)
		}
		if got := schemaAt(m, "properties.alpha") != nil; got != c.alpha {
			t.Errorf("%s %s: alpha advertised = %v, want %v", c.tool, c.path, got, c.alpha)
		}
	}
}

// TestBindForInstance_MultiplicityHidden: with capability:multiplicity
// hidden no bound tool schema carries a `multiplicity` property — root,
// nested or under a pulse_predict alternative root.
func TestBindForInstance_MultiplicityHidden(t *testing.T) {
	schema := makeBindSchema()
	enabled, err := BindForInstance(schema, multiplicityInstance(t, true))
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := BindForInstance(schema, multiplicityInstance(t, false))
	if err != nil {
		t.Fatal(err)
	}
	for tool := range multiplicityPaths {
		if !bytes.Contains(enabled[tool], []byte(`"multiplicity":`)) {
			t.Fatalf("vacuous: %s with multiplicity carries no multiplicity property", tool)
		}
	}
	for tool, body := range hidden {
		if bytes.Contains(body, []byte(`"multiplicity":`)) {
			t.Errorf("%s: multiplicity hidden, bound schema still carries a multiplicity property", tool)
		}
	}
}

// toStrings converts a decoded JSON array to strings.
func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}
