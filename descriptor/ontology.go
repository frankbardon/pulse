package descriptor

// Skill ontology data model.
//
// The ontology is a typed node / edge graph over Pulse's discovery
// metadata: the intent taxonomy, the registered operators, the skill
// pack, the runnable examples, the glossary, the capabilities, the MCP
// tools and the named tables, joined by typed edges derived from
// metadata that already exists (skill frontmatter, operator Purposes,
// the feature table, example _meta). It carries no prose — bodies are
// read through the skill and example surfaces.
//
// Node IDs are stable strings spelled `<kind>:<name>` — `intent:compare_groups`,
// `operator:AGG_COUNT`, `skill:op-agg-count`, `example:<_meta.name>`,
// `glossary_term:p-value`, `mcp_tool:pulse_facet` — with ONE exception: a
// capability node's ID and Name are its feature-profile spelling verbatim,
// which is already kind-prefixed (`capability:crosstab`, `io_format:csv`,
// `mcp_extra:prompt_bootstrap`; every non-operator feature is a
// capability node). Node and edge kinds are closed and additive-only.

// OntologyNodeKind classifies an ontology node.
type OntologyNodeKind string

// The ontology node kinds.
const (
	// OntologyNodeIntent is an intent-taxonomy entry (Name = intent ID).
	OntologyNodeIntent OntologyNodeKind = "intent"
	// OntologyNodeOperator is a registered operator, overlay kind,
	// regression, regression spec modifier or synth distribution
	// (Name = its registered name).
	OntologyNodeOperator OntologyNodeKind = "operator"
	// OntologyNodeSkill is a skill-pack entry, virtual skills included
	// (Name = skill stem).
	OntologyNodeSkill OntologyNodeKind = "skill"
	// OntologyNodeExample is a runnable request example (Name = its
	// _meta.name).
	OntologyNodeExample OntologyNodeKind = "example"
	// OntologyNodeGlossaryTerm is a glossary term (Name = term ID).
	OntologyNodeGlossaryTerm OntologyNodeKind = "glossary_term"
	// OntologyNodeCapability is a non-operator feature (Name = its
	// feature-profile spelling, e.g. "capability:crosstab").
	OntologyNodeCapability OntologyNodeKind = "capability"
	// OntologyNodeMCPTool is an MCP tool (Name = tool name).
	OntologyNodeMCPTool OntologyNodeKind = "mcp_tool"
	// OntologyNodeTable is an instance-registered named table.
	OntologyNodeTable OntologyNodeKind = "table"
)

// OntologyEdgeKind classifies a directed ontology edge.
type OntologyEdgeKind string

// The ontology edge kinds. Each comment gives the From → To node kinds.
const (
	// OntologyEdgeServesIntent: operator | example → intent.
	OntologyEdgeServesIntent OntologyEdgeKind = "serves_intent"
	// OntologyEdgeDocumentedBy: operator | mcp_tool | intent |
	// glossary_term → the skill that documents it.
	OntologyEdgeDocumentedBy OntologyEdgeKind = "documented_by"
	// OntologyEdgeExemplifiedBy: operator | skill → example.
	OntologyEdgeExemplifiedBy OntologyEdgeKind = "exemplified_by"
	// OntologyEdgeNotFor: operator → the operator or capability to use
	// instead (Purpose.NotFor).
	OntologyEdgeNotFor OntologyEdgeKind = "not_for"
	// OntologyEdgeUsesTerm: operator → glossary_term.
	OntologyEdgeUsesTerm OntologyEdgeKind = "uses_term"
	// OntologyEdgeRoutesTo: skill → skill, glossary_term → glossary_term
	// (a navigational "see also").
	OntologyEdgeRoutesTo OntologyEdgeKind = "routes_to"
	// OntologyEdgeRequiresCapability: operator | capability | mcp_tool |
	// skill → the operator or capability it needs.
	OntologyEdgeRequiresCapability OntologyEdgeKind = "requires_capability"
)

// OntologyNode is one ontology node.
type OntologyNode struct {
	// ID is the stable node identifier (see the package spelling rule).
	ID string `json:"id"`
	// Kind classifies the node.
	Kind OntologyNodeKind `json:"kind"`
	// Name is the node's name in its own registry.
	Name string `json:"name"`
}

// OntologyEdge is one directed, typed ontology edge between two node IDs.
type OntologyEdge struct {
	// From is the source node ID.
	From string `json:"from"`
	// To is the target node ID.
	To string `json:"to"`
	// Kind classifies the edge.
	Kind OntologyEdgeKind `json:"kind"`
}

// Ontology is a node / edge graph. Nodes are sorted by ID; edges by
// From, then Kind, then To; no duplicates.
type Ontology struct {
	Nodes []OntologyNode `json:"nodes"`
	Edges []OntologyEdge `json:"edges"`
}
