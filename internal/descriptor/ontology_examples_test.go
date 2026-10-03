package descriptor

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
)

// TestExamples_EdgeCoverage (binding): over the shipped library, every
// operator a body or description names is an edge and declared
// capabilities agree with the structural detectors. The fixture arms
// prove the check bites: a body naming an operator outside any edge
// source, and a declaration its detector contradicts, are reported.
func TestExamples_EdgeCoverage(t *testing.T) {
	src := builtinOntologySources()
	for _, p := range exampleEdgeCoverageProblems(BaseOntology(), src.examples, nil) {
		t.Error(p)
	}

	fx := ontologySources{
		features: Features(),
		examples: []ontologyExample{
			// AGG_SUM rides a non-`type` key: _meta.operators misses it,
			// no detector sees it.
			{Name: "stray", Body: json.RawMessage(`{"note":"AGG_SUM"}`)},
			{Name: "liar", Capabilities: []string{featCrosstab}, Body: json.RawMessage(`{}`)},
			{Name: "fine", Operators: []string{"AGG_SUM"}, Body: json.RawMessage(`{"aggregations":[{"type":"AGG_SUM"}]}`)},
		},
	}
	got := exampleEdgeCoverageProblems(buildOntology(fx), fx.examples, nil)
	want := []string{
		`example "liar": _meta.capabilities declares capability:crosstab but its structural detector does not fire`,
		`example "stray": body names AGG_SUM but no edge joins them`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("fixture problems =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestExamples_CapabilitiesFromFeatures (binding): every shipped
// `_meta.capabilities` value is a known non-operator feature; the fixture
// arm proves an unknown name and an operator are both reported.
func TestExamples_CapabilitiesFromFeatures(t *testing.T) {
	for _, p := range exampleCapabilityProblems(examples.Capabilities()) {
		t.Error(p)
	}
	got := exampleCapabilityProblems(map[string][]string{
		"ok":  {featStream},
		"bad": {"capability:astrology", "AGG_SUM"},
	})
	if len(got) != 2 || !strings.Contains(got[0], "capability:astrology") || !strings.Contains(got[1], "AGG_SUM") {
		t.Errorf("exampleCapabilityProblems = %v, want the unknown name and the operator", got)
	}
}

// TestOntology_ExampleCapabilityEdges: each detector, a declared
// capability, an overlay kind and a description mention produce their
// edges on fixtures; the shipped library carries one of each.
func TestOntology_ExampleCapabilityEdges(t *testing.T) {
	fx := ontologySources{
		features: Features(),
		examples: []ontologyExample{
			{Name: "xt", Body: json.RawMessage(`{"crosstab":{"rows":[]}}`)},
			{Name: "cmp", Body: json.RawMessage(`{"requests":[{"joins":[{}]}]}`)},
			{Name: "chain", Body: json.RawMessage(`{"stages":[]}`)},
			{Name: "fct", Category: "facet", Body: json.RawMessage(`{"fields":["a"]}`)},
			{Name: "ov", Body: json.RawMessage(`{"overlays":[{"kind":"OVERLAY_INDEX_VS_POP"}]}`)},
			{Name: "st", Capabilities: []string{featStream}, Description: "Unlike TEST_T, uses TEST_WELCH.", Operators: []string{"TEST_WELCH"}},
			// a nested `stages` key is not a chain root
			{Name: "nested", Body: json.RawMessage(`{"x":{"stages":[]}}`)},
		},
	}
	g := buildOntology(fx)
	if p := g.Problems(); len(p) > 0 {
		t.Fatalf("fixture problems: %v", p)
	}
	ex := func(n string) string { return OntologyID(descriptor.OntologyNodeExample, n) }
	req := descriptor.OntologyEdgeRequiresCapability
	for _, e := range []descriptor.OntologyEdge{
		ontoEdge(ex("xt"), featCrosstab, req),
		ontoEdge(ex("cmp"), featCompose, req),
		ontoEdge(ex("cmp"), featJoins, req),
		ontoEdge(ex("chain"), featProcessChain, req),
		ontoEdge(ex("fct"), featFacet, req),
		ontoEdge(opID("OVERLAY_INDEX_VS_POP"), ex("ov"), descriptor.OntologyEdgeExemplifiedBy),
		ontoEdge(ex("st"), featStream, req),
		ontoEdge(ex("st"), opID("TEST_T"), descriptor.OntologyEdgeRoutesTo),
	} {
		if !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
	// TEST_WELCH is already an operators edge: no duplicate routes_to.
	if hasEdge(g, ontoEdge(ex("st"), opID("TEST_WELCH"), descriptor.OntologyEdgeRoutesTo)) {
		t.Error("description mention duplicated an operators edge")
	}
	if out := g.Out(ex("nested"), ""); len(out) != 0 {
		t.Errorf("nested stages produced edges %v", out)
	}

	base := BaseOntology()
	for _, e := range []descriptor.OntologyEdge{
		ontoEdge(ex("facet_simple_one_field"), featFacet, req),
		ontoEdge(ex("facet-index-vs-pop"), featFacet, req),
		ontoEdge(opID("OVERLAY_INDEX_VS_POP"), ex("facet-index-vs-pop"), descriptor.OntologyEdgeExemplifiedBy),
	} {
		if !hasEdge(base, e) {
			t.Errorf("base missing %+v", e)
		}
	}
}

// TestExamples_EmptyOperatorExamplesDeclareCapabilities: an example with
// no `_meta.operators` declares its capabilities explicitly, so its only
// non-intent edges are never left to a body-token scan.
func TestExamples_EmptyOperatorExamplesDeclareCapabilities(t *testing.T) {
	caps := examples.Capabilities()
	for _, s := range examples.Search("", nil, "") {
		if len(s.Operators) == 0 && len(caps[s.Name]) == 0 {
			t.Errorf("example %q has no _meta.operators and no _meta.capabilities", s.Name)
		}
	}
}
