package descriptor

import (
	"strings"

	"github.com/frankbardon/pulse/descriptor"
)

// Instance ontology — the base graph extended with the instance's
// extension registrations and pruned to its feature set (E1-S3).
//
// NewInstanceSnapshot builds it EAGERLY (pulse.New time) and caches it
// on the snapshot; every discovery path (skills list / get, examples
// search / get, example stats, the virtual intents / glossary skills)
// walks it. A profile-free, extension-free instance shares
// BaseOntology() by pointer.
//
// Extension nodes: every visible extension operator (any category,
// synth distributions and the reserved overlay-kind slot included) is
// an operator node carrying its Purpose edges (serves_intent, not_for,
// uses_term); an extension synth distribution also requires
// capability:synth. Every named table is a table node — Name
// `<table kind>/<table name>` (`label/region`, `range/fiscal`,
// `lookup/rates`), so ID `table:label/region` — the kind prefix keeps a
// label table and a range table of the same name apart. A label table
// requires capability:labels, a range table capability:range_tables; a
// lookup table needs nothing (the expr lookup() function is not a
// feature).
//
// Prune rules (pruneOntology), in dependency order:
//
//   - operator / capability node: hidden iff the instance Hides its
//     feature name. A node that is not a feature at all (a synth
//     distribution, a REG spec modifier) is hidden when a capability it
//     requires is hidden (capability:synth) or, for a REG spec modifier,
//     when EVERY regression is hidden. The flattened any-of
//     requires_capability edges of a FEATURE node are never read — the
//     resolved feature set already closed the profile under its
//     dependencies;
//   - mcp_tool / table node: hidden iff a node it requires is hidden;
//   - skill: hidden iff an operator or MCP tool it documents is hidden
//     (atomic skills follow their surface) or any `requires:` target is
//     hidden (topical requires mean AND). The virtual intents / glossary
//     skills are never pruned — they render from the pruned graph;
//   - example: hidden iff an operator it exemplifies (in-edge
//     exemplified_by from an OPERATOR) is hidden, or any out-edge
//     requires_capability / routes_to target is hidden. A skill's `## See`
//     edge into the example never prunes it;
//   - intent: hidden iff ≥1 operator serves it and every server is
//     hidden (an intent no operator serves — tooling — always stays);
//   - glossary term: hidden iff ≥1 operator uses it and every user is
//     hidden. Its SeeAlso edges go with it.
//
// Every edge with a hidden endpoint is dropped.

// tableNodeName spells a table node's Name.
func tableNodeName(kind, name string) string { return kind + "/" + name }

// The table kinds a table node Name is prefixed with.
const (
	tableKindLabel  = "label"
	tableKindRange  = "range"
	tableKindLookup = "lookup"
)

// instanceOntology returns the graph the instance walks: the base, plus
// ext's nodes when it registers any, pruned when inst hides anything.
// Shares BaseOntology() by pointer when neither applies.
func instanceOntology(ext *ExtensionsSnapshot, inst *InstanceSnapshot) *OntologyGraph {
	g := BaseOntology()
	if extensionsAddNodes(ext) {
		g = extendOntology(g, ext)
	}
	if inst.Scoped() && len(inst.hidden) > 0 {
		g = pruneOntology(g, inst)
	}
	return g
}

// extensionOperatorLists returns every operator list of ext.
func extensionOperatorLists(ext *ExtensionsSnapshot) [][]descriptor.OperatorMeta {
	return [][]descriptor.OperatorMeta{
		ext.Aggregators, ext.Attributes, ext.Filterers, ext.Groupers, ext.Windows,
		ext.Features, ext.Tests, ext.SynthDistributions, ext.OverlayKinds,
	}
}

func extensionsAddNodes(ext *ExtensionsSnapshot) bool {
	if ext == nil {
		return false
	}
	for _, l := range extensionOperatorLists(ext) {
		if len(l) > 0 {
			return true
		}
	}
	return len(ext.LookupTables)+len(ext.LabelTables)+len(ext.RangeTables) > 0
}

// extendOntology returns a new graph: base plus ext's operator and table
// nodes and their edges. base is not modified.
func extendOntology(base *OntologyGraph, ext *ExtensionsSnapshot) *OntologyGraph {
	b := &ontologyBuilder{
		nodes:       make(map[string]descriptor.OntologyNode, len(base.nodes)+8),
		edges:       make(map[descriptor.OntologyEdge]struct{}, len(base.edges)+8),
		featureKind: map[string]FeatureKind{},
		problems:    append([]string(nil), base.problems...),
	}
	for _, n := range base.nodes {
		b.nodes[n.ID] = n
	}
	for _, e := range base.edges {
		b.edges[e] = struct{}{}
	}
	for _, f := range Features() {
		b.featureKind[f.Name] = f.Kind
	}
	for _, l := range extensionOperatorLists(ext) {
		for _, op := range l {
			b.node(descriptor.OntologyNodeOperator, op.Name)
		}
	}
	for _, d := range ext.SynthDistributions {
		b.edge(OntologyID(descriptor.OntologyNodeOperator, d.Name), b.featureNode(featSynth, "extension synth distribution "+d.Name), descriptor.OntologyEdgeRequiresCapability, "extension synth distribution")
	}
	b.addPurposeEdges(ontologySources{purposes: ext.Purposes})
	for _, t := range ext.LabelTables {
		id := b.node(descriptor.OntologyNodeTable, tableNodeName(tableKindLabel, t.Name))
		b.edge(id, b.featureNode(featLabels, "label table "+t.Name), descriptor.OntologyEdgeRequiresCapability, "label table")
	}
	for _, t := range ext.RangeTables {
		id := b.node(descriptor.OntologyNodeTable, tableNodeName(tableKindRange, t.Name))
		b.edge(id, b.featureNode(featRangeTables, "range table "+t.Name), descriptor.OntologyEdgeRequiresCapability, "range table")
	}
	for _, t := range ext.LookupTables {
		b.node(descriptor.OntologyNodeTable, tableNodeName(tableKindLookup, t.Name))
	}
	return b.finish()
}

// pruneOntology returns g without the nodes inst hides (rules in the
// file comment) and every edge touching one. g is not modified.
func pruneOntology(g *OntologyGraph, inst *InstanceSnapshot) *OntologyGraph {
	hidden := map[string]struct{}{}
	isHidden := func(id string) bool { _, ok := hidden[id]; return ok }
	anyHidden := func(edges []descriptor.OntologyEdge, to bool, kinds ...descriptor.OntologyNodeKind) bool {
		for _, e := range edges {
			id := e.From
			if to {
				id = e.To
			}
			if len(kinds) > 0 {
				n, _ := g.Node(id)
				if !nodeKindIn(n.Kind, kinds) {
					continue
				}
			}
			if isHidden(id) {
				return true
			}
		}
		return false
	}
	// allHidden: ≥1 edge from an operator, and every such operator hidden.
	allOperatorsHidden := func(edges []descriptor.OntologyEdge) bool {
		n := 0
		for _, e := range edges {
			if from, _ := g.Node(e.From); from.Kind != descriptor.OntologyNodeOperator {
				continue
			}
			n++
			if !isHidden(e.From) {
				return false
			}
		}
		return n > 0
	}

	// Pass 1: features (by name), then the non-feature operators.
	for _, n := range g.nodes {
		if (n.Kind == descriptor.OntologyNodeOperator || n.Kind == descriptor.OntologyNodeCapability) && inst.Hidden(n.Name) {
			hidden[n.ID] = struct{}{}
		}
	}
	allRegressionsHidden := true
	for _, r := range regressionCapabilities() {
		if !inst.Hidden(r.Name) {
			allRegressionsHidden = false
			break
		}
	}
	for _, n := range g.nodes {
		if n.Kind != descriptor.OntologyNodeOperator || isHidden(n.ID) {
			continue
		}
		if _, isFeature := FeatureKindOf(n.Name); isFeature {
			continue
		}
		if anyHidden(g.Out(n.ID, descriptor.OntologyEdgeRequiresCapability), true) ||
			strings.HasPrefix(n.Name, "REG_") && allRegressionsHidden {
			hidden[n.ID] = struct{}{}
		}
	}
	// Pass 2: tools and tables follow what they require.
	for _, n := range g.nodes {
		if (n.Kind == descriptor.OntologyNodeMCPTool || n.Kind == descriptor.OntologyNodeTable) &&
			anyHidden(g.Out(n.ID, descriptor.OntologyEdgeRequiresCapability), true) {
			hidden[n.ID] = struct{}{}
		}
	}
	// Pass 3: skills (their surface is settled), then examples, intents
	// and glossary terms. Order matters only for the example rule's
	// operator-only in-edge filter: a skill's See edge must not prune.
	for _, n := range g.nodes {
		if n.Kind == descriptor.OntologyNodeSkill &&
			(anyHidden(g.In(n.ID, descriptor.OntologyEdgeDocumentedBy), false, descriptor.OntologyNodeOperator, descriptor.OntologyNodeMCPTool) ||
				anyHidden(g.Out(n.ID, descriptor.OntologyEdgeRequiresCapability), true)) {
			hidden[n.ID] = struct{}{}
		}
	}
	for _, n := range g.nodes {
		var drop bool
		switch n.Kind {
		case descriptor.OntologyNodeExample:
			drop = anyHidden(g.In(n.ID, descriptor.OntologyEdgeExemplifiedBy), false, descriptor.OntologyNodeOperator) ||
				anyHidden(g.Out(n.ID, descriptor.OntologyEdgeRequiresCapability), true) ||
				anyHidden(g.Out(n.ID, descriptor.OntologyEdgeRoutesTo), true)
		case descriptor.OntologyNodeIntent:
			drop = allOperatorsHidden(g.In(n.ID, descriptor.OntologyEdgeServesIntent))
		case descriptor.OntologyNodeGlossaryTerm:
			drop = allOperatorsHidden(g.In(n.ID, descriptor.OntologyEdgeUsesTerm))
		}
		if drop {
			hidden[n.ID] = struct{}{}
		}
	}
	return g.without(hidden)
}

func nodeKindIn(k descriptor.OntologyNodeKind, kinds []descriptor.OntologyNodeKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

// without returns a new graph lacking the hidden nodes and every edge
// touching one; order is preserved, so the result stays sorted.
func (g *OntologyGraph) without(hidden map[string]struct{}) *OntologyGraph {
	out := &OntologyGraph{
		byID:     make(map[string]int, len(g.nodes)),
		out:      map[string][]int{},
		in:       map[string][]int{},
		problems: g.problems,
	}
	out.nodes = make([]descriptor.OntologyNode, 0, len(g.nodes)-len(hidden))
	for _, n := range g.nodes {
		if _, h := hidden[n.ID]; !h {
			out.byID[n.ID] = len(out.nodes)
			out.nodes = append(out.nodes, n)
		}
	}
	out.edges = make([]descriptor.OntologyEdge, 0, len(g.edges))
	for _, e := range g.edges {
		_, hf := hidden[e.From]
		_, ht := hidden[e.To]
		if hf || ht {
			continue
		}
		i := len(out.edges)
		out.edges = append(out.edges, e)
		out.out[e.From] = append(out.out[e.From], i)
		out.in[e.To] = append(out.in[e.To], i)
	}
	return out
}

// Has reports whether the graph carries the node id.
func (g *OntologyGraph) Has(id string) bool {
	_, ok := g.byID[id]
	return ok
}
