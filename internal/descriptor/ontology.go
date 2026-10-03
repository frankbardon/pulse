package descriptor

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// Skill ontology — the base graph.
//
// BaseOntology() is the ONE entry point: the profile-independent graph of
// every built-in intent, operator, skill, example, glossary term,
// capability and MCP tool, built once per process (sync.Once) from
// metadata that already exists. It is never mutated after the build;
// per-instance views (extensions, feature-profile pruning) derive from
// it and a profile-free, extension-free instance shares it by pointer.
//
// Node IDs follow the public descriptor spelling rule — `<kind>:<name>`
// (OntologyID), except a capability node, whose ID is its feature
// spelling verbatim (`capability:crosstab`, `io_format:csv`,
// `mcp_extra:prompt_bootstrap`). An operator or capability node's Name
// is therefore always the feature name that owns it (an operator that is
// not a feature — a synth distribution, a REG spec modifier — owns no
// feature of its own).
//
// Edge sources (one function each, see buildOntology):
//
//   - skill `operator:` frontmatter   → operator documented_by skill
//   - tool-<kebab> skill stem         → mcp_tool documented_by skill
//   - MCPToolBindingOf (feature-bound) → mcp_tool requires_capability feature
//   - Purpose.Intents                 → operator serves_intent intent
//   - Purpose.NotFor[].Use            → operator not_for operator|capability
//   - Purpose.Glossary                → operator uses_term glossary_term
//   - glossary SeeAlso                → glossary_term routes_to glossary_term
//   - FeatureDependencies (DependsOn: request hosts, overlayHostKinds,
//     hardEdges; flattened — every member of every any-of group) →
//     operator|capability requires_capability operator|capability
//   - synth distribution              → operator requires_capability capability:synth
//   - example _meta.operators         → operator exemplified_by example
//   - example _meta.intents           → example serves_intent intent
//   - example _meta.capabilities      → example requires_capability feature
//   - example structural detectors (exampleCapabilityDetectors: crosstab
//     key, root requests, root stages, joins key, facet category/tag) →
//     example requires_capability capability
//   - example overlays[].kind         → operator exemplified_by example
//   - example description naming an operator not otherwise linked →
//     example routes_to operator (see ontology_examples.go)
//   - atomic `## See` backticked stem → skill routes_to skill
//   - atomic `## See` `tags=[…]`      → skill exemplified_by every example
//     carrying ALL the tags (what pulse_examples_search returns)
//   - topical `requires:` frontmatter → skill requires_capability feature
//   - virtual skills                  → intent documented_by skill:intents,
//     glossary_term documented_by skill:glossary
//
// A reference that resolves to no node emits no edge and is recorded in
// the graph's problems (TestOntology_BaseHasNoProblems keeps the base
// clean — notably an unknown feature in a skill's `requires:`).
//
// The REG spec-modifier rule (REG_RESAMPLE / REG_SELECTION hidden only
// when EVERY regression is hidden) is an any-of the edge model does not
// express; the pruner keeps it as a rule over the operator node.
//
// Instance graphs (ontology_instance.go) add extension operator nodes and
// TABLE nodes — Name `<label|range|lookup>/<table name>`, ID
// `table:label/region` — and prune per feature set; the prune rules are
// documented there.

// OntologyID spells a node ID: `<kind>:<name>`, or name itself for a
// capability node (a feature spelling is already kind-prefixed).
func OntologyID(kind descriptor.OntologyNodeKind, name string) string {
	if kind == descriptor.OntologyNodeCapability {
		return name
	}
	return string(kind) + ":" + name
}

// OntologyGraph is a built ontology plus its lookup indexes. Treat it as
// read-only; Ontology() hands out a copy.
type OntologyGraph struct {
	nodes    []descriptor.OntologyNode
	edges    []descriptor.OntologyEdge
	byID     map[string]int
	out      map[string][]int
	in       map[string][]int
	problems []string
}

var (
	baseOntologyOnce sync.Once
	baseOntology     *OntologyGraph
)

// BaseOntology returns the process-wide, profile-independent base graph,
// built on first use. Shared: never mutate it.
func BaseOntology() *OntologyGraph {
	baseOntologyOnce.Do(func() { baseOntology = buildOntology(builtinOntologySources()) })
	return baseOntology
}

// Ontology returns a deep copy of the graph's sorted nodes and edges.
func (g *OntologyGraph) Ontology() descriptor.Ontology {
	return descriptor.Ontology{
		Nodes: append([]descriptor.OntologyNode{}, g.nodes...),
		Edges: append([]descriptor.OntologyEdge{}, g.edges...),
	}
}

// Node returns the node with the given ID.
func (g *OntologyGraph) Node(id string) (descriptor.OntologyNode, bool) {
	i, ok := g.byID[id]
	if !ok {
		return descriptor.OntologyNode{}, false
	}
	return g.nodes[i], true
}

// Out returns the edges leaving id, of the given kind ("" = every kind),
// in graph order.
func (g *OntologyGraph) Out(id string, kind descriptor.OntologyEdgeKind) []descriptor.OntologyEdge {
	return g.pick(g.out[id], kind)
}

// In returns the edges entering id, of the given kind ("" = every kind),
// in graph order.
func (g *OntologyGraph) In(id string, kind descriptor.OntologyEdgeKind) []descriptor.OntologyEdge {
	return g.pick(g.in[id], kind)
}

func (g *OntologyGraph) pick(idx []int, kind descriptor.OntologyEdgeKind) []descriptor.OntologyEdge {
	var out []descriptor.OntologyEdge
	for _, i := range idx {
		if kind == "" || g.edges[i].Kind == kind {
			out = append(out, g.edges[i])
		}
	}
	return out
}

// OperatorsServing is the intent → operator reverse index: the sorted
// names of the operators whose Purpose declares intentID.
func (g *OntologyGraph) OperatorsServing(intentID string) []string {
	var out []string
	for _, e := range g.In(OntologyID(descriptor.OntologyNodeIntent, intentID), descriptor.OntologyEdgeServesIntent) {
		if n, ok := g.Node(e.From); ok && n.Kind == descriptor.OntologyNodeOperator {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Problems returns the references the build could not resolve to a
// node, sorted. Empty on a healthy base graph.
func (g *OntologyGraph) Problems() []string {
	return append([]string(nil), g.problems...)
}

// ontologyExample is the slice of one example's _meta the graph reads.
type ontologyExample struct {
	Name         string
	Category     string
	Description  string
	Operators    []string
	Intents      []string
	Capabilities []string
	Tags         []string
	Body         json.RawMessage // request body, _meta stripped
}

// ontologySources are the builder's inputs, injectable for tests.
type ontologySources struct {
	skills             []skills.Metadata
	skillBody          func(name string) (string, bool)
	features           []Feature
	tools              []MCPToolBinding
	purposes           map[string]descriptor.Purpose
	synthDistributions []string
	intents            []string
	terms              []descriptor.Term
	examples           []ontologyExample
}

func builtinOntologySources() ontologySources {
	src := ontologySources{
		skills:    skills.List(),
		skillBody: skills.Get,
		features:  Features(),
		tools:     MCPToolBindings(),
		purposes:  builtinPurposes,
		intents:   IntentIDs(),
		terms:     Glossary(),
	}
	for _, d := range distributionCapabilities() {
		src.synthDistributions = append(src.synthDistributions, d.Name)
	}
	intents, caps := examples.Intents(), examples.Capabilities()
	for _, s := range examples.Search("", nil, "") {
		ex := ontologyExample{
			Name: s.Name, Category: s.Category, Description: s.Description,
			Operators: s.Operators, Intents: intents[s.Name], Capabilities: caps[s.Name], Tags: s.Tags,
		}
		if full, ok := examples.Get(s.Name); ok {
			ex.Body = full.Body
		}
		src.examples = append(src.examples, ex)
	}
	return src
}

// ontologyBuilder accumulates nodes and edges before the final sort.
type ontologyBuilder struct {
	nodes       map[string]descriptor.OntologyNode
	edges       map[descriptor.OntologyEdge]struct{}
	featureKind map[string]FeatureKind
	problems    []string
}

func (b *ontologyBuilder) node(kind descriptor.OntologyNodeKind, name string) string {
	id := OntologyID(kind, name)
	if _, ok := b.nodes[id]; !ok {
		b.nodes[id] = descriptor.OntologyNode{ID: id, Kind: kind, Name: name}
	}
	return id
}

// edge adds from → to when both nodes exist; otherwise it records why.
func (b *ontologyBuilder) edge(from, to string, kind descriptor.OntologyEdgeKind, source string) {
	if _, ok := b.nodes[from]; !ok {
		b.problems = append(b.problems, source+": unknown source node "+from)
		return
	}
	if to == "" {
		return
	}
	if _, ok := b.nodes[to]; !ok {
		b.problems = append(b.problems, source+": "+from+" → unknown node "+to)
		return
	}
	if from == to {
		return
	}
	b.edges[descriptor.OntologyEdge{From: from, To: to, Kind: kind}] = struct{}{}
}

// featureNode resolves a feature-profile spelling (or a non-feature
// operator name) to its node ID; "" plus a problem when nothing matches.
func (b *ontologyBuilder) featureNode(name, source string) string {
	if k, ok := b.featureKind[name]; ok {
		if k == FeatureKindOperator {
			return OntologyID(descriptor.OntologyNodeOperator, name)
		}
		return OntologyID(descriptor.OntologyNodeCapability, name)
	}
	if id := OntologyID(descriptor.OntologyNodeOperator, name); b.has(id) {
		return id
	}
	b.problems = append(b.problems, source+": unknown feature "+name)
	return ""
}

func (b *ontologyBuilder) has(id string) bool {
	_, ok := b.nodes[id]
	return ok
}

func buildOntology(src ontologySources) *OntologyGraph {
	b := &ontologyBuilder{
		nodes:       map[string]descriptor.OntologyNode{},
		edges:       map[descriptor.OntologyEdge]struct{}{},
		featureKind: map[string]FeatureKind{},
	}
	b.addNodes(src)
	b.addSkillEdges(src)
	b.addToolEdges(src)
	b.addPurposeEdges(src)
	b.addGlossaryEdges(src)
	b.addDependencyEdges(src)
	b.addExampleEdges(src)
	b.addSeeEdges(src)
	return b.finish()
}

func (b *ontologyBuilder) addNodes(src ontologySources) {
	for _, f := range src.features {
		b.featureKind[f.Name] = f.Kind
		if f.Kind == FeatureKindOperator {
			b.node(descriptor.OntologyNodeOperator, f.Name)
		} else {
			b.node(descriptor.OntologyNodeCapability, f.Name)
		}
	}
	for name := range src.purposes {
		b.node(descriptor.OntologyNodeOperator, name)
	}
	for _, d := range src.synthDistributions {
		b.node(descriptor.OntologyNodeOperator, d)
	}
	for _, md := range src.skills {
		b.node(descriptor.OntologyNodeSkill, md.Name)
		if md.Kind == "operator" && md.Operator != "" {
			b.node(descriptor.OntologyNodeOperator, md.Operator)
		}
	}
	for _, t := range src.tools {
		b.node(descriptor.OntologyNodeMCPTool, t.Tool)
	}
	for _, id := range src.intents {
		b.node(descriptor.OntologyNodeIntent, id)
	}
	for _, t := range src.terms {
		b.node(descriptor.OntologyNodeGlossaryTerm, t.ID)
	}
	for _, ex := range src.examples {
		b.node(descriptor.OntologyNodeExample, ex.Name)
	}
}

// addSkillEdges: operator / tool documented_by its atomic skill, the
// virtual skills' registries, and topical `requires:`.
func (b *ontologyBuilder) addSkillEdges(src ontologySources) {
	for _, md := range src.skills {
		skill := OntologyID(descriptor.OntologyNodeSkill, md.Name)
		switch {
		case md.Kind == "operator" && md.Operator != "":
			b.edge(OntologyID(descriptor.OntologyNodeOperator, md.Operator), skill, descriptor.OntologyEdgeDocumentedBy, "skill operator")
		case md.Kind == "tool" && strings.HasPrefix(md.Name, "tool-"):
			tool := "pulse_" + strings.ReplaceAll(strings.TrimPrefix(md.Name, "tool-"), "-", "_")
			b.edge(OntologyID(descriptor.OntologyNodeMCPTool, tool), skill, descriptor.OntologyEdgeDocumentedBy, "tool skill "+md.Name)
		}
		for _, req := range md.Requires {
			b.edge(skill, b.featureNode(req, "skill "+md.Name+" requires"), descriptor.OntologyEdgeRequiresCapability, "skill requires")
		}
	}
	if b.has(OntologyID(descriptor.OntologyNodeSkill, skills.VirtualIntents)) {
		for _, id := range src.intents {
			b.edge(OntologyID(descriptor.OntologyNodeIntent, id), OntologyID(descriptor.OntologyNodeSkill, skills.VirtualIntents), descriptor.OntologyEdgeDocumentedBy, "intents skill")
		}
	}
	if b.has(OntologyID(descriptor.OntologyNodeSkill, skills.VirtualGlossary)) {
		for _, t := range src.terms {
			b.edge(OntologyID(descriptor.OntologyNodeGlossaryTerm, t.ID), OntologyID(descriptor.OntologyNodeSkill, skills.VirtualGlossary), descriptor.OntologyEdgeDocumentedBy, "glossary skill")
		}
	}
}

// addToolEdges: a feature-bound MCP tool needs its feature; a core-bound
// one needs nothing.
func (b *ontologyBuilder) addToolEdges(src ontologySources) {
	for _, t := range src.tools {
		if t.Feature == "" {
			continue
		}
		b.edge(OntologyID(descriptor.OntologyNodeMCPTool, t.Tool), b.featureNode(t.Feature, "tool "+t.Tool), descriptor.OntologyEdgeRequiresCapability, "tool binding")
	}
}

// addPurposeEdges: serves_intent, not_for and uses_term from each
// operator's Purpose.
func (b *ontologyBuilder) addPurposeEdges(src ontologySources) {
	for name, p := range src.purposes {
		from := OntologyID(descriptor.OntologyNodeOperator, name)
		for _, in := range p.Intents {
			b.edge(from, OntologyID(descriptor.OntologyNodeIntent, in), descriptor.OntologyEdgeServesIntent, "purpose intents")
		}
		for _, alt := range p.NotFor {
			b.edge(from, b.featureNode(alt.Use, "purpose not_for "+name), descriptor.OntologyEdgeNotFor, "purpose not_for")
		}
		for _, term := range p.Glossary {
			b.edge(from, OntologyID(descriptor.OntologyNodeGlossaryTerm, term), descriptor.OntologyEdgeUsesTerm, "purpose glossary")
		}
	}
}

func (b *ontologyBuilder) addGlossaryEdges(src ontologySources) {
	for _, t := range src.terms {
		for _, see := range t.SeeAlso {
			b.edge(OntologyID(descriptor.OntologyNodeGlossaryTerm, t.ID), OntologyID(descriptor.OntologyNodeGlossaryTerm, see), descriptor.OntologyEdgeRoutesTo, "glossary see_also")
		}
	}
}

// addDependencyEdges: each feature's dependency expression, flattened
// (every member of every any-of group — the AND/OR structure stays in
// the feature table), and every synth distribution → capability:synth.
func (b *ontologyBuilder) addDependencyEdges(src ontologySources) {
	for _, f := range src.features {
		from := b.featureNode(f.Name, "feature")
		for _, group := range f.DependsOn {
			for _, dep := range group {
				b.edge(from, b.featureNode(dep, "depends_on "+f.Name), descriptor.OntologyEdgeRequiresCapability, "depends_on")
			}
		}
	}
	for _, d := range src.synthDistributions {
		b.edge(OntologyID(descriptor.OntologyNodeOperator, d), b.featureNode(featSynth, "synth distribution "+d), descriptor.OntologyEdgeRequiresCapability, "synth distribution")
	}
}

// addSeeEdges parses each ATOMIC skill's `## See` section: a backticked
// span naming a skill is a routes_to edge; a `tags=[a, b]` span links
// every example carrying all the tags. Other spans are prose.
func (b *ontologyBuilder) addSeeEdges(src ontologySources) {
	if src.skillBody == nil {
		return
	}
	for _, md := range src.skills {
		if md.Kind != "operator" && md.Kind != "tool" && md.Kind != "type" {
			continue
		}
		body, ok := src.skillBody(md.Name)
		if !ok {
			continue
		}
		from := OntologyID(descriptor.OntologyNodeSkill, md.Name)
		for _, span := range backtickSpans(seeSection(body)) {
			if tags, ok := seeTags(span); ok {
				// A tag search matching nothing is an empty search
				// result, not a broken reference: no edge, no problem.
				for _, ex := range src.examples {
					if hasAllTags(ex.Tags, tags) {
						b.edge(from, OntologyID(descriptor.OntologyNodeExample, ex.Name), descriptor.OntologyEdgeExemplifiedBy, "see tags")
					}
				}
				continue
			}
			if to := OntologyID(descriptor.OntologyNodeSkill, span); b.has(to) {
				b.edge(from, to, descriptor.OntologyEdgeRoutesTo, "see stem")
			}
		}
	}
}

// seeSection returns the text of the body's `## See` section (up to the
// next `## ` heading), or "".
func seeSection(body string) string {
	var out []string
	in := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(line) == "## See"
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// backtickSpans returns the contents of every `...` span in s.
func backtickSpans(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+1+j])
		s = s[i+1+j+1:]
	}
}

// seeTags parses the `tags=[a, b]` list out of a See span.
func seeTags(span string) ([]string, bool) {
	i := strings.Index(span, "tags=[")
	if i < 0 {
		return nil, false
	}
	rest := span[i+len("tags=["):]
	j := strings.IndexByte(rest, ']')
	if j < 0 {
		return nil, false
	}
	var tags []string
	for _, t := range strings.Split(rest[:j], ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags, len(tags) > 0
}

func hasAllTags(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// finish sorts nodes by ID and edges by (From, Kind, To) and builds the
// lookup indexes.
func (b *ontologyBuilder) finish() *OntologyGraph {
	g := &OntologyGraph{
		byID: make(map[string]int, len(b.nodes)),
		out:  map[string][]int{},
		in:   map[string][]int{},
	}
	g.nodes = make([]descriptor.OntologyNode, 0, len(b.nodes))
	for _, n := range b.nodes {
		g.nodes = append(g.nodes, n)
	}
	sort.Slice(g.nodes, func(i, j int) bool { return g.nodes[i].ID < g.nodes[j].ID })
	for i, n := range g.nodes {
		g.byID[n.ID] = i
	}
	g.edges = make([]descriptor.OntologyEdge, 0, len(b.edges))
	for e := range b.edges {
		g.edges = append(g.edges, e)
	}
	sort.Slice(g.edges, func(i, j int) bool {
		a, c := g.edges[i], g.edges[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		return a.To < c.To
	})
	for i, e := range g.edges {
		g.out[e.From] = append(g.out[e.From], i)
		g.in[e.To] = append(g.in[e.To], i)
	}
	g.problems = append([]string(nil), b.problems...)
	sort.Strings(g.problems)
	return g
}
