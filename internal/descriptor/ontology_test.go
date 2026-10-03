package descriptor

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

func ontoEdge(from, to string, kind descriptor.OntologyEdgeKind) descriptor.OntologyEdge {
	return descriptor.OntologyEdge{From: from, To: to, Kind: kind}
}

func opID(name string) string { return OntologyID(descriptor.OntologyNodeOperator, name) }
func skillID(name string) string {
	return OntologyID(descriptor.OntologyNodeSkill, name)
}

// hasEdge reports whether g carries exactly e.
func hasEdge(g *OntologyGraph, e descriptor.OntologyEdge) bool {
	return slices.Contains(g.Out(e.From, e.Kind), e)
}

// featureNodeID mirrors the builder's feature → node resolution.
func featureNodeID(t *testing.T, name string) string {
	t.Helper()
	if k, ok := FeatureKindOf(name); ok {
		if k == FeatureKindOperator {
			return opID(name)
		}
		return name
	}
	return opID(name)
}

func TestOntologyID_Spelling(t *testing.T) {
	cases := map[string]string{
		OntologyID(descriptor.OntologyNodeOperator, "AGG_COUNT"):            "operator:AGG_COUNT",
		OntologyID(descriptor.OntologyNodeSkill, "op-agg-count"):            "skill:op-agg-count",
		OntologyID(descriptor.OntologyNodeIntent, "describe"):               "intent:describe",
		OntologyID(descriptor.OntologyNodeCapability, "capability:crosstab"): "capability:crosstab",
		OntologyID(descriptor.OntologyNodeCapability, "io_format:csv"):      "io_format:csv",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("OntologyID = %q, want %q", got, want)
		}
	}
}

// TestOntology_BaseDeterministicAndSorted: two builds are identical,
// nodes are sorted unique by ID, edges sorted unique by (From, Kind, To),
// and every edge joins two existing nodes.
func TestOntology_BaseDeterministicAndSorted(t *testing.T) {
	a := buildOntology(builtinOntologySources()).Ontology()
	b := buildOntology(builtinOntologySources()).Ontology()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two base builds differ")
	}
	if len(a.Nodes) == 0 || len(a.Edges) == 0 {
		t.Fatalf("empty graph: %d nodes, %d edges", len(a.Nodes), len(a.Edges))
	}
	ids := map[string]bool{}
	for i, n := range a.Nodes {
		if i > 0 && a.Nodes[i-1].ID >= n.ID {
			t.Fatalf("nodes not strictly sorted at %d: %q >= %q", i, a.Nodes[i-1].ID, n.ID)
		}
		if n.ID != OntologyID(n.Kind, n.Name) {
			t.Errorf("node %q: ID does not follow the spelling rule for kind %q name %q", n.ID, n.Kind, n.Name)
		}
		ids[n.ID] = true
	}
	less := func(x, y descriptor.OntologyEdge) bool {
		if x.From != y.From {
			return x.From < y.From
		}
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		return x.To < y.To
	}
	for i, e := range a.Edges {
		if i > 0 && !less(a.Edges[i-1], e) {
			t.Fatalf("edges not strictly sorted at %d: %+v then %+v", i, a.Edges[i-1], e)
		}
		if !ids[e.From] || !ids[e.To] {
			t.Errorf("edge %+v joins a missing node", e)
		}
	}
}

// TestOntology_BaseHasNoProblems: every reference in the built-in
// metadata resolves to a node — in particular every skill `requires:`
// entry names a built-in feature.
func TestOntology_BaseHasNoProblems(t *testing.T) {
	if p := BaseOntology().Problems(); len(p) > 0 {
		t.Errorf("base ontology has %d unresolved references:\n%s", len(p), strings.Join(p, "\n"))
	}
}

// TestOntology_SkillRequires: a topical `requires:` entry emits a
// requires_capability edge to the feature's node; an unknown feature is
// a problem (the gate above fails on it) and emits no edge.
func TestOntology_SkillRequires(t *testing.T) {
	src := ontologySources{
		features: Features(),
		skills: []skills.Metadata{
			{Name: "good", Kind: "design", Requires: []string{"capability:crosstab", "TEST_WELCH", "io_format:csv"}},
			{Name: "bad", Kind: "design", Requires: []string{"capability:nope"}},
		},
	}
	g := buildOntology(src)
	for _, to := range []string{"capability:crosstab", opID("TEST_WELCH"), "io_format:csv"} {
		if e := ontoEdge(skillID("good"), to, descriptor.OntologyEdgeRequiresCapability); !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
	if out := g.Out(skillID("bad"), ""); len(out) != 0 {
		t.Errorf("unknown requires emitted edges: %+v", out)
	}
	p := g.Problems()
	if len(p) != 1 || !strings.Contains(p[0], "capability:nope") {
		t.Errorf("problems = %q, want one naming capability:nope", p)
	}
}

// TestOntology_SkillOperatorFrontmatter: every atomic operator skill
// documents its operator, SYNTH and REG spec modifiers included.
func TestOntology_SkillOperatorFrontmatter(t *testing.T) {
	g := BaseOntology()
	n := 0
	for _, md := range skills.List() {
		if md.Kind != "operator" || md.Operator == "" {
			continue
		}
		n++
		if e := ontoEdge(opID(md.Operator), skillID(md.Name), descriptor.OntologyEdgeDocumentedBy); !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
	if n == 0 {
		t.Fatal("no operator skills")
	}
	for _, e := range []descriptor.OntologyEdge{
		ontoEdge(opID("normal"), skillID("op-synth-normal"), descriptor.OntologyEdgeDocumentedBy),
		ontoEdge(opID("REG_RESAMPLE"), skillID("op-reg-mod-resample"), descriptor.OntologyEdgeDocumentedBy),
	} {
		if !hasEdge(g, e) {
			t.Errorf("missing special case %+v", e)
		}
	}
}

// TestOntology_ToolEdges: each tool skill documents its pulse_<snake>
// tool; a feature-bound tool requires its feature, a core one nothing.
func TestOntology_ToolEdges(t *testing.T) {
	g := BaseOntology()
	for _, b := range MCPToolBindings() {
		tool := OntologyID(descriptor.OntologyNodeMCPTool, b.Tool)
		if _, ok := g.Node(tool); !ok {
			t.Errorf("no node for %s", tool)
		}
		req := g.Out(tool, descriptor.OntologyEdgeRequiresCapability)
		if b.Core != "" {
			if len(req) != 0 {
				t.Errorf("core tool %s requires %+v", b.Tool, req)
			}
			continue
		}
		if want := ontoEdge(tool, featureNodeID(t, b.Feature), descriptor.OntologyEdgeRequiresCapability); !hasEdge(g, want) {
			t.Errorf("missing %+v", want)
		}
	}
	if e := ontoEdge("mcp_tool:pulse_facet", skillID("tool-facet"), descriptor.OntologyEdgeDocumentedBy); !hasEdge(g, e) {
		t.Errorf("missing %+v", e)
	}
}

// TestOntology_PurposeEdges: every Purpose intent, NotFor alternative
// and glossary term becomes an edge.
func TestOntology_PurposeEdges(t *testing.T) {
	g := BaseOntology()
	for name, p := range builtinPurposes {
		for _, in := range p.Intents {
			if e := ontoEdge(opID(name), OntologyID(descriptor.OntologyNodeIntent, in), descriptor.OntologyEdgeServesIntent); !hasEdge(g, e) {
				t.Errorf("serves_intent: missing %+v", e)
			}
		}
		for _, alt := range p.NotFor {
			if e := ontoEdge(opID(name), featureNodeID(t, alt.Use), descriptor.OntologyEdgeNotFor); !hasEdge(g, e) {
				t.Errorf("not_for: missing %+v", e)
			}
		}
		for _, term := range p.Glossary {
			if e := ontoEdge(opID(name), OntologyID(descriptor.OntologyNodeGlossaryTerm, term), descriptor.OntologyEdgeUsesTerm); !hasEdge(g, e) {
				t.Errorf("uses_term: missing %+v", e)
			}
		}
	}
}

// TestOntology_OperatorsServing: the intent → operator reverse index
// equals a scan of the Purpose registry, for every intent.
func TestOntology_OperatorsServing(t *testing.T) {
	g := BaseOntology()
	for _, in := range IntentIDs() {
		var want []string
		for name, p := range builtinPurposes {
			if slices.Contains(p.Intents, in) {
				want = append(want, name)
			}
		}
		sort.Strings(want)
		if got := g.OperatorsServing(in); !slices.Equal(got, want) {
			t.Errorf("OperatorsServing(%q) = %q, want %q", in, got, want)
		}
	}
	if len(g.OperatorsServing("compare_groups")) == 0 {
		t.Error("compare_groups served by no operator")
	}
}

// TestOntology_GlossaryEdges: SeeAlso → routes_to; every term and intent
// is documented by its virtual skill.
func TestOntology_GlossaryEdges(t *testing.T) {
	g := BaseOntology()
	for _, term := range Glossary() {
		id := OntologyID(descriptor.OntologyNodeGlossaryTerm, term.ID)
		for _, see := range term.SeeAlso {
			if e := ontoEdge(id, OntologyID(descriptor.OntologyNodeGlossaryTerm, see), descriptor.OntologyEdgeRoutesTo); !hasEdge(g, e) {
				t.Errorf("missing %+v", e)
			}
		}
		if e := ontoEdge(id, skillID(skills.VirtualGlossary), descriptor.OntologyEdgeDocumentedBy); !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
	for _, in := range IntentIDs() {
		if e := ontoEdge(OntologyID(descriptor.OntologyNodeIntent, in), skillID(skills.VirtualIntents), descriptor.OntologyEdgeDocumentedBy); !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
	for _, v := range skills.ReservedVirtualNames() {
		if n, ok := g.Node(skillID(v)); !ok || n.Kind != descriptor.OntologyNodeSkill {
			t.Errorf("virtual skill %q: node %+v ok=%v, want kind skill", v, n, ok)
		}
	}
}

// TestOntology_DependencyEdges: every member of every DependsOn group
// (request hosts, overlay hosts, hard edges) is a requires_capability
// edge, and every synth distribution requires capability:synth.
func TestOntology_DependencyEdges(t *testing.T) {
	g := BaseOntology()
	for _, f := range Features() {
		for _, group := range f.DependsOn {
			for _, dep := range group {
				if e := ontoEdge(featureNodeID(t, f.Name), featureNodeID(t, dep), descriptor.OntologyEdgeRequiresCapability); !hasEdge(g, e) {
					t.Errorf("missing %+v", e)
				}
			}
		}
	}
	for _, e := range []descriptor.OntologyEdge{
		ontoEdge(opID("OVERLAY_YOY"), opID("GROUP_DATE"), descriptor.OntologyEdgeRequiresCapability),
		ontoEdge(opID("OVERLAY_INDEX_VS_POP"), "capability:facet", descriptor.OntologyEdgeRequiresCapability),
		ontoEdge("capability:filter_to_file", opID("FILTER_EXPRESSION"), descriptor.OntologyEdgeRequiresCapability),
		ontoEdge("capability:stream", "capability:process", descriptor.OntologyEdgeRequiresCapability),
	} {
		if !hasEdge(g, e) {
			t.Errorf("missing pinned %+v", e)
		}
	}
	for _, d := range distributionCapabilities() {
		if e := ontoEdge(opID(d.Name), "capability:synth", descriptor.OntologyEdgeRequiresCapability); !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}
}

// TestOntology_CapabilityNodes: every non-operator feature is a
// capability node whose ID and Name are its feature spelling.
func TestOntology_CapabilityNodes(t *testing.T) {
	g := BaseOntology()
	for _, f := range Features() {
		want := descriptor.OntologyNode{ID: f.Name, Kind: descriptor.OntologyNodeCapability, Name: f.Name}
		if f.Kind == FeatureKindOperator {
			want = descriptor.OntologyNode{ID: opID(f.Name), Kind: descriptor.OntologyNodeOperator, Name: f.Name}
		}
		if got, ok := g.Node(want.ID); !ok || got != want {
			t.Errorf("feature %s: node %+v ok=%v, want %+v", f.Name, got, ok, want)
		}
	}
}

// TestOntology_ExampleEdges: _meta.operators → exemplified_by,
// _meta.intents → serves_intent.
func TestOntology_ExampleEdges(t *testing.T) {
	g := BaseOntology()
	intents := examples.Intents()
	for _, s := range examples.Search("", nil, "") {
		id := OntologyID(descriptor.OntologyNodeExample, s.Name)
		for _, op := range s.Operators {
			if e := ontoEdge(featureNodeID(t, op), id, descriptor.OntologyEdgeExemplifiedBy); !hasEdge(g, e) {
				t.Errorf("missing %+v", e)
			}
		}
		for _, in := range intents[s.Name] {
			if e := ontoEdge(id, OntologyID(descriptor.OntologyNodeIntent, in), descriptor.OntologyEdgeServesIntent); !hasEdge(g, e) {
				t.Errorf("missing %+v", e)
			}
		}
	}
}

// TestOntology_SeeEdges: atomic `## See` stems route to skills and
// `tags=[…]` links every example carrying all the tags; prose spans and
// topical See sections emit nothing.
func TestOntology_SeeEdges(t *testing.T) {
	g := BaseOntology()
	if e := ontoEdge(skillID("op-test-welch"), skillID("op-test-t"), descriptor.OntologyEdgeRoutesTo); !hasEdge(g, e) {
		t.Errorf("missing %+v", e)
	}
	if e := ontoEdge(skillID("op-agg-count"), skillID("aggregation-design"), descriptor.OntologyEdgeRoutesTo); !hasEdge(g, e) {
		t.Errorf("missing %+v", e)
	}
	welch := examples.Search("", []string{"welch"}, "")
	if len(welch) == 0 {
		t.Fatal("no example tagged welch")
	}
	for _, s := range welch {
		if e := ontoEdge(skillID("op-test-welch"), OntologyID(descriptor.OntologyNodeExample, s.Name), descriptor.OntologyEdgeExemplifiedBy); !hasEdge(g, e) {
			t.Errorf("missing %+v", e)
		}
	}

	src := ontologySources{
		skills: []skills.Metadata{
			{Name: "op-a", Kind: "operator"},
			{Name: "op-b", Kind: "operator"},
			{Name: "topic", Kind: "design"},
		},
		skillBody: func(name string) (string, bool) {
			switch name {
			case "op-a":
				return "## Params\n`op-b` outside See\n## See\n\n- `pulse_examples_search tags=[x, y]`\n- Skills: `op-b`, `topic`, `nope`\n## After\n`topic`\n", true
			case "topic":
				return "## See\n`op-a`\n", true
			}
			return "", true
		},
		examples: []ontologyExample{
			{Name: "both", Tags: []string{"x", "y"}},
			{Name: "one", Tags: []string{"x"}},
		},
	}
	sg := buildOntology(src)
	want := []descriptor.OntologyEdge{
		ontoEdge(skillID("op-a"), "example:both", descriptor.OntologyEdgeExemplifiedBy),
		ontoEdge(skillID("op-a"), skillID("op-b"), descriptor.OntologyEdgeRoutesTo),
		ontoEdge(skillID("op-a"), skillID("topic"), descriptor.OntologyEdgeRoutesTo),
	}
	if got := sg.Ontology().Edges; !reflect.DeepEqual(got, want) {
		t.Errorf("synthetic See edges = %+v, want %+v", got, want)
	}
}

// TestOntology_CopyIsIndependent: Ontology() hands out a copy.
func TestOntology_CopyIsIndependent(t *testing.T) {
	g := BaseOntology()
	o := g.Ontology()
	first, firstEdge := o.Nodes[0], o.Edges[0]
	o.Nodes[0].ID = "mutated"
	o.Edges[0].Kind = "mutated"
	if n := g.Ontology(); n.Nodes[0] != first || n.Edges[0] != firstEdge {
		t.Error("mutating a returned Ontology reached the graph")
	}
	if BaseOntology() != g {
		t.Error("BaseOntology is not shared")
	}
}

func TestSeeParsing(t *testing.T) {
	body := "# T\n## Params\n`x`\n## See\n- `a` and `b`\n- `pulse_examples_search tags=[p, q]`\n## Gotchas\n`c`\n"
	if got := backtickSpans(seeSection(body)); !slices.Equal(got, []string{"a", "b", "pulse_examples_search tags=[p, q]"}) {
		t.Errorf("spans = %q", got)
	}
	if tags, ok := seeTags("tags=[feature-engineering]"); !ok || !slices.Equal(tags, []string{"feature-engineering"}) {
		t.Errorf("bare tags = %q %v", tags, ok)
	}
	if _, ok := seeTags("op-agg-count"); ok {
		t.Error("stem parsed as tags")
	}
	if got := backtickSpans("unclosed `x"); len(got) != 0 {
		t.Errorf("unclosed span = %q", got)
	}
}
