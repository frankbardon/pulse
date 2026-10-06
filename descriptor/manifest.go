package descriptor

// Command describes a CLI leaf command in the manifest.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	// Annotations carries the three per-command capability hints
	// (streamable / deterministic / expensive). Embedders use these to
	// decide whether to wrap a command in caching or invoke it directly.
	// Always populated for built-in commands.
	Annotations CommandAnnotations `json:"annotations"`
}

// CommandAnnotations carries three capability flags per command.
//
//   - Streamable: the command has a streaming variant. Callers can
//     invoke the streaming form for incremental output.
//   - Deterministic: the command produces the same output given the
//     same inputs (including the source file's content hash). Callers
//     can safely cache results keyed by the request hash.
//   - Expensive: the command is worth caching. Cheap operations may
//     not be worth the cache machinery; expensive ones (regression,
//     filter-to-file, profile) typically are. Hint to consumers, not
//     a hard constraint.
type CommandAnnotations struct {
	Streamable    bool `json:"streamable"`
	Deterministic bool `json:"deterministic"`
	Expensive     bool `json:"expensive"`
}

// Components lists every registered processing component grouped by
// category. Each slice carries one Operator entry per component so
// LLM-side authoring has access to per-operator params, accepted field
// types, emit type, and streamability without further discovery
// round-trips.
type Components struct {
	Aggregators []Operator `json:"aggregators"`
	Attributes  []Operator `json:"attributes"`
	Filterers   []Operator `json:"filterers"`
	Groupers    []Operator `json:"groupers"`
	Windows     []Operator `json:"windows"`
	Features    []Operator `json:"features"`
}

// CohortFieldType describes a field type available in .pulse files and
// the operator catalog that accepts it. The Compatible* slices are
// derived from the per-operator AcceptsTypes declarations and let an
// LLM look up "what can I do with a date field" in one place.
//
// ShardedCapable reports whether the type participates in a shard
// archive without restriction. Every built-in field type is sharded-
// capable today; the flag exists for forward compatibility with future
// types that might not work across the union of shards (e.g. types
// whose semantics depend on per-shard locality). Embedders should treat
// the flag as advisory.
type CohortFieldType struct {
	Name                  string   `json:"name"`
	Categorical           bool     `json:"categorical"`
	ShardedCapable        bool     `json:"sharded_capable"`
	CompatibleAggregators []string `json:"compatible_aggregators,omitempty"`
	CompatibleAttributes  []string `json:"compatible_attributes,omitempty"`
	CompatibleFilterers   []string `json:"compatible_filterers,omitempty"`
	CompatibleGroupers    []string `json:"compatible_groupers,omitempty"`
	CompatibleWindows     []string `json:"compatible_windows,omitempty"`
	CompatibleFeatures    []string `json:"compatible_features,omitempty"`
}

// SkillMeta describes a bundled skill.
type SkillMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ComponentsSchemasBlock surfaces the per-operator components contract
// (ResponseComponents.Components[] payload shape) at manifest level so
// LLM clients can reason about meta-fields without crawling per-
// category Operator entries. Each map is keyed by operator name and
// sorted deterministically at serialization time.
//
// An entry's ComponentSchema mirrors the same value carried on the
// per-Operator entry under Components.Aggregators[i].ComponentSchema.
// The two surfaces are deliberately redundant — Operator-level keeps
// the per-operator entry self-contained for category-by-category
// drilldown, while this top-level block lets a client materialize the
// whole meta-fields catalog in one O(N) scan.
type ComponentsSchemasBlock struct {
	Aggregators map[string]ComponentSchema `json:"aggregators,omitempty"`
	Groupers    map[string]ComponentSchema `json:"groupers,omitempty"`
	Filterers   map[string]ComponentSchema `json:"filterers,omitempty"`
	Matrices    map[string]ComponentSchema `json:"matrices,omitempty"`
}

// Manifest is the root self-description of the Pulse system. One bootstrap
// call returns every fact an LLM needs to author a valid Pulse request:
// CLI command list, per-operator capabilities, per-test metadata (tier-1
// and tier-2 as peer slices), synth distribution catalog, MCP tool list,
// cohort field-type catalog with operator cross-references, and embedded
// skill index. Error coverage is name-only — fetch per-code prose via
// the `pulse_errors_lookup` MCP tool or `pulse errors lookup CODE` CLI
// leaf on demand to keep the bootstrap payload lean.
//
// The payload is deterministic and free of cohort data. Clients cache it
// for a session.
type Manifest struct {
	FormatVersion string `json:"format_version"`
	// PulseVersion is the build version of the binary or library that
	// produced the manifest (buildinfo.Version: ldflags, then module
	// version, then "devel[+rev]"). It identifies the build, not the
	// payload contract — that is the envelope format_version.
	PulseVersion string `json:"pulse_version"`
	// TZDataVersion is the IANA tz database release embedded in the
	// build (e.g. "2026c"). Every zone name Pulse accepts and every
	// offset it applies comes from that copy alone — never the host's
	// zoneinfo — so two builds with the same value resolve every zone
	// identically. A build fact like PulseVersion: not part of
	// feature_set_digest.
	TZDataVersion string `json:"tzdata_version"`
	// FeatureSetDigest identifies the feature set the manifest
	// describes: "fs1:" + sha256hex over the instance's sorted enabled
	// feature names and its effective behaviour switches. Two manifests
	// with the same pulse_version and digest describe the same surface,
	// so it keys caches of self-description output. Every manifest
	// carries one; a profile-free instance (and the CLI) carries the
	// full-registry digest.
	FeatureSetDigest string    `json:"feature_set_digest"`
	Commands         []Command `json:"commands"`

	// Operations enumerates library-only entry points that do not back a
	// CLI leaf (today: filter_to_file, watch, process_stream). Each entry
	// carries the same CommandAnnotations as a CLI leaf so consumers can
	// reason uniformly about caching / streaming. The slice is sorted by
	// Name for determinism.
	Operations  []Command        `json:"operations"`
	Components  Components       `json:"components"`
	Tests       []TestMeta       `json:"tests"`
	PostTests   []TestMeta       `json:"post_tests"`
	Regressions []RegressionMeta `json:"regressions"`
	// Matrices lists the registered MAT_* matrix operators (empty when
	// the instance hides capability:matrices).
	Matrices           []MatrixMeta       `json:"matrices"`
	SynthDistributions []DistributionMeta `json:"synth_distributions"`
	// ErrorCodesCount is the total number of registered error codes.
	ErrorCodesCount int `json:"error_codes_count"`
	// ErrorDomains is the alphabetized list of distinct domain prefixes
	// (e.g. "CLI", "DATA", "ENCODING", "PROCESSING", "PULSE",
	// "SERVICE"). One entry per domain, six entries in v1.
	ErrorDomains []string `json:"error_domains"`
	// ErrorCodes is the alphabetized list of code identifiers.
	// Per-code Message + Fixup prose lives behind the
	// `pulse_errors_lookup` MCP tool / `pulse errors lookup CODE` CLI
	// leaf — depth-on-demand, not common-path.
	ErrorCodes        []string          `json:"error_codes"`
	MCPTools          []MCPTool         `json:"mcp_tools"`
	CohortTypes       []CohortFieldType `json:"cohort_types"`
	Skills            []SkillMeta       `json:"skills"`
	ExamplesCount     int               `json:"examples_count"`
	ExampleCategories []string          `json:"example_categories"`
	ExampleTags       []string          `json:"example_tags"`
	// Extensions enumerates embedder-registered operators + expression
	// state. Built-in operators continue to live in Components; this
	// block is the additive layer registered via
	// pulse.Options.Extensions. Empty slices on every field for a
	// host with no extensions.
	Extensions ExtensionsManifest `json:"extensions"`

	// Facet is the rich-facet endpoint capability descriptor. One
	// entry today (facet_schema); future variants land under a slice
	// when added.
	// Omitted (nil) when the instance does not offer capability:facet.
	Facet *FacetCapability `json:"facet,omitempty"`

	// ProcessChain is the source-rooted linear chain endpoint
	// capability descriptor (one entry today: process_chain).
	// Carries the mergeable-operator allowlist and rejection rules
	// so LLM clients can route between chain and per-stage fallback.
	// Omitted (nil) when the instance does not offer capability:process_chain.
	ProcessChain *ProcessChainCapability `json:"process_chain,omitempty"`

	// Join is the pushdown hash-join capability descriptor (one
	// entry today: hash_join). Carries the kind allowlist, spill
	// envelope, and v1 limitations.
	// Omitted (nil) when the instance does not offer capability:joins.
	Join *JoinCapability `json:"join,omitempty"`

	// Crosstab is the cross-tabulation endpoint capability
	// descriptor (Request.Crosstab). Carries the normalize / shape
	// allowlists plus the per-aggregator margin-reducibility
	// classification so LLM clients can decide which cell aggregator
	// will recompute its margin and which is summable.
	// Omitted (nil) when the instance does not offer capability:crosstab.
	Crosstab *CrosstabCapability `json:"crosstab,omitempty"`

	// Export is the cross-format export envelope. Carries one
	// ExportFormatCapability entry per format the export dispatcher
	// supports, declaring the per-format overlay-embedding shape
	// (sidecar / sheets / trailing_block / warn_and_skip) so LLM
	// planners can route Response.Overlays through ExportJob without
	// inspecting the io/ packages.
	// Omitted (nil) when the instance does not offer capability:export. Formats lists only the I/O formats the instance offers.
	Export *ExportCapability `json:"export,omitempty"`

	// Import is the cross-format import envelope — the read-side peer
	// of Export. Carries one ImportFormatCapability per format
	// io.NewReader factory accepts, declaring the file extensions that
	// resolve to it, whether its .pulse schema comes from the source's
	// own dictionary ("authoritative") or from the shared inference
	// pass ("inferred"), and whether the same format can also be
	// written. Read and write surfaces are NOT symmetric — SPSS is
	// import-only — so a planner must consult both blocks rather than
	// assuming one implies the other.
	// Omitted (nil) when the instance does not offer capability:import. Formats lists only the I/O formats the instance offers.
	Import *ImportCapability `json:"import,omitempty"`

	// Matrix is the matrix-slot capability descriptor
	// (Request.Vectors + Request.Matrices): encodings, matrix kinds,
	// missing modes, the merge block size and the v1 limitations.
	// Omitted (nil) when the instance does not offer
	// capability:matrices.
	Matrix *MatrixCapability `json:"matrix,omitempty"`

	// Overlays enumerates the registered overlay catalog — one
	// OverlayCapability per types.AllOverlayKinds() entry. Each entry
	// declares the supported OverlayShape × OverlayScope × OverlayRef
	// kinds the kind accepts plus its Buffered flag (the inverse of
	// types.OverlayStreamable). LLM clients route between overlay
	// catalog lookup and per-spec validation without crawling the
	// type system. Sorted alphabetically by Kind via
	// OverlayCapabilities() so the golden manifest stays stable.
	Overlays []OverlayCapability `json:"overlays"`

	// ComponentsSchemas projects the per-operator components contract
	// across categories as a name-keyed map. Embedders rely on this
	// block to plan ResponseComponents.Components[] consumption without
	// iterating Components.* per category.
	ComponentsSchemas ComponentsSchemasBlock `json:"components_schemas"`

	// Intents lists the intent-taxonomy IDs, sorted — the closed set of
	// question kinds every entry's intents list draws from. IDs only:
	// the full Intent records (labels, phrasings, data shapes) are
	// fetched on demand. Static: the same list under every feature
	// profile.
	Intents []string `json:"intents"`

	// ReturnPresets lists the named `return` presets (full / standard /
	// minimal, in that order), each with the response paths it selects
	// EXPANDED against this instance: a path a hidden feature owns is
	// absent. Always present: `return` is a plain request slot, not a
	// feature.
	ReturnPresets []ReturnPresetMeta `json:"return_presets"`
}

// ReturnPresetMeta is one named `return` preset as the manifest lists
// it.
type ReturnPresetMeta struct {
	// Name is the preset name (a types.ReturnPreset).
	Name string `json:"name"`
	// Paths is the selection the preset expands to on this instance, in
	// table order. Nested `warnings` slots are kept wherever their
	// parent is emitted and are not listed; full lists every visible
	// top-level key.
	Paths []string `json:"paths"`
}
