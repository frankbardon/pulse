package descriptor

import (
	"sort"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/examples"
	"github.com/frankbardon/pulse/skills"
)

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
// sorted deterministically at serialization time. Aggregators are
// populated in E1-S3; Groupers + Filterers fill in E2-S2 / E2-S8.
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
	FormatVersion string    `json:"format_version"`
	Commands      []Command `json:"commands"`

	// Operations enumerates library-only entry points that do not back a
	// CLI leaf (today: filter_to_file, watch, process_stream). Each entry
	// carries the same CommandAnnotations as a CLI leaf so consumers can
	// reason uniformly about caching / streaming. The slice is sorted by
	// Name for determinism.
	Operations         []Command          `json:"operations"`
	Components         Components         `json:"components"`
	Tests              []TestMeta         `json:"tests"`
	PostTests          []TestMeta         `json:"post_tests"`
	Regressions        []RegressionMeta   `json:"regressions"`
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
	Facet FacetCapability `json:"facet"`

	// ProcessChain is the source-rooted linear chain endpoint
	// capability descriptor (one entry today: process_chain).
	// Carries the mergeable-operator allowlist and rejection rules
	// so LLM clients can route between chain and per-stage fallback.
	ProcessChain ProcessChainCapability `json:"process_chain"`

	// Join is the pushdown hash-join capability descriptor (one
	// entry today: hash_join). Carries the kind allowlist, spill
	// envelope, and v1 limitations.
	Join JoinCapability `json:"join"`

	// Crosstab is the cross-tabulation endpoint capability
	// descriptor (Request.Crosstab). Carries the normalize / shape
	// allowlists plus the per-aggregator margin-reducibility
	// classification so LLM clients can decide which cell aggregator
	// will recompute its margin and which is summable.
	Crosstab CrosstabCapability `json:"crosstab"`

	// Export is the cross-format export envelope. Carries one
	// ExportFormatCapability entry per format the export dispatcher
	// supports, declaring the per-format overlay-embedding shape
	// (sidecar / sheets / trailing_block / warn_and_skip) so LLM
	// planners can route Response.Overlays through ExportJob without
	// inspecting the io/ packages. Wired by E9-S11 once the per-
	// format adapters (E9-S2..S6) landed.
	Export ExportCapability `json:"export"`

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
	// across categories as a name-keyed map. Aggregators populated in
	// E1-S3; Groupers + Filterers wired in E2-S2 / E2-S8. Embedders
	// rely on this block to plan ResponseComponents.Components[]
	// consumption without iterating Components.* per category.
	ComponentsSchemas ComponentsSchemasBlock `json:"components_schemas"`
}

// operations returns the library-only entry points that do not back a
// CLI leaf. They carry the same CommandAnnotations as commands so the
// manifest exposes a uniform shape across CLI and library surfaces.
func operations() []Command {
	return []Command{
		{Name: "filter_to_file", Description: "Filter a cohort and write the result to a deterministic .pulse output", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "process_stream", Description: "Execute a processing request and return a structured StreamResult of incremental rows", Annotations: CommandAnnotations{Streamable: true, Deterministic: true, Expensive: true}},
		{Name: "synth_stream", Description: "Generate synthetic respondents incrementally via a structured StreamResult", Annotations: CommandAnnotations{Streamable: true, Deterministic: false, Expensive: true}},
		{Name: "watch", Description: "Observe .pulse file changes and emit ChangeEvent records", Annotations: CommandAnnotations{Streamable: true, Deterministic: false, Expensive: false}},
	}
}

// commands returns the default set of CLI leaf commands. Each entry
// carries its capability annotations (streamable / deterministic /
// expensive) so manifest consumers can decide whether to cache.
func commands() []Command {
	return []Command{
		{Name: "process", Description: "Execute a processing request against a cohort", Annotations: CommandAnnotations{Streamable: true, Deterministic: true, Expensive: true}},
		{Name: "compose", Description: "Execute multiple processing requests in batch", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "process-chain", Description: "Execute a source-rooted linear chain of mergeable processing stages", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "sample", Description: "Return sample rows from a cohort", Annotations: CommandAnnotations{Streamable: true, Deterministic: false, Expensive: false}},
		{Name: "facet", Description: "Return distinct values for a field", Annotations: CommandAnnotations{Streamable: true, Deterministic: true, Expensive: false}},
		{Name: "inspect", Description: "Inspect a .pulse file header and schema", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "predict", Description: "Validate a request without executing", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "manifest", Description: "Output the root manifest", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "mcp", Description: "Serve the Model Context Protocol over stdio", Annotations: CommandAnnotations{Streamable: true, Deterministic: false, Expensive: false}},
		{Name: "synth", Description: "Generate synthetic .pulse cohorts from a schema or profile", Annotations: CommandAnnotations{Streamable: true, Deterministic: false, Expensive: true}},
		{Name: "profile", Description: "Capture statistical summaries of cohorts for synthesis", Annotations: CommandAnnotations{Streamable: true, Deterministic: true, Expensive: true}},
		{Name: "shard create", Description: "Create a new shard archive from one or more single-file .pulse shards", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "shard add", Description: "Append a shard to an existing archive", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "shard remove", Description: "Remove a shard from an archive by basename", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "shard list", Description: "List shards inside an archive", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "shard compact", Description: "Rewrite a shard archive to reclaim orphan bytes and refresh canonical metadata", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "shard verify", Description: "Re-validate every shard's header + cohesion against the canonical schema", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "shard extract", Description: "Extract a shard's standalone .pulse bytes to stdout", Annotations: CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
	}
}

// rawCohortFieldTypes returns the bare (name, categorical) tuples for
// every defined field type. Compatible* cross-refs are computed by
// cohortFieldTypes() from the operator capability tables.
func rawCohortFieldTypes() []CohortFieldType {
	var out []CohortFieldType
	for i := range 17 {
		ft := encoding.FieldType(i)
		name := ft.String()
		if len(name) > 7 && name[:7] == "unknown" {
			continue
		}
		out = append(out, CohortFieldType{
			Name:           name,
			Categorical:    ft.IsCategorical(),
			ShardedCapable: true,
		})
	}
	return out
}

// cohortFieldTypes returns CohortFieldType descriptors enriched with
// Compatible* cross-references derived deterministically from the
// per-operator AcceptsTypes declarations.
func cohortFieldTypes() []CohortFieldType {
	base := rawCohortFieldTypes()
	aggs := aggregatorCapabilities()
	attrs := attributeCapabilities()
	filts := filtererCapabilities()
	grps := grouperCapabilities()
	wins := windowCapabilities()
	feats := featureCapabilities()

	indexByType := func(ops []Operator) map[string][]string {
		m := make(map[string][]string)
		for _, op := range ops {
			for _, t := range op.AcceptsTypes {
				m[t] = append(m[t], op.Name)
			}
		}
		for k := range m {
			sort.Strings(m[k])
		}
		return m
	}

	aggIdx := indexByType(aggs)
	attrIdx := indexByType(attrs)
	filtIdx := indexByType(filts)
	grpIdx := indexByType(grps)
	winIdx := indexByType(wins)
	featIdx := indexByType(feats)

	for i := range base {
		t := base[i].Name
		base[i].CompatibleAggregators = aggIdx[t]
		base[i].CompatibleAttributes = attrIdx[t]
		base[i].CompatibleFilterers = filtIdx[t]
		base[i].CompatibleGroupers = grpIdx[t]
		base[i].CompatibleWindows = winIdx[t]
		base[i].CompatibleFeatures = featIdx[t]
	}
	return base
}

// sortByName sorts an Operator slice lexically by Name. Used to keep the
// manifest payload deterministic.
func sortByName(ops []Operator) []Operator {
	out := make([]Operator, len(ops))
	copy(out, ops)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sortTestsByName sorts a TestMeta slice lexically by Name.
func sortTestsByName(ts []TestMeta) []TestMeta {
	out := make([]TestMeta, len(ts))
	copy(out, ts)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// partitionTier separates a TestMeta slice into tier-1 and tier-2 entries.
// Tier-1 entries become Manifest.Tests; tier-2 become Manifest.PostTests.
// Both slices are sorted by Name for determinism.
func partitionTier(all []TestMeta) (tier1, tier2 []TestMeta) {
	for _, m := range all {
		if m.Tier == 2 {
			tier2 = append(tier2, m)
		} else {
			tier1 = append(tier1, m)
		}
	}
	return sortTestsByName(tier1), sortTestsByName(tier2)
}

// sortDistributions sorts a DistributionMeta slice lexically by Name.
func sortDistributions(ds []DistributionMeta) []DistributionMeta {
	out := make([]DistributionMeta, len(ds))
	copy(out, ds)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sortRegressions sorts a RegressionMeta slice lexically by Name.
func sortRegressions(rs []RegressionMeta) []RegressionMeta {
	out := make([]RegressionMeta, len(rs))
	copy(out, rs)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// componentsSchemasBlock collects every populated ComponentSchema from
// the per-category capability tables into the top-level
// ComponentsSchemasBlock projection. Map serialization is sorted by
// encoding/json since Go 1.12, so the golden manifest stays
// deterministic without a wrapping sort step. Groupers are wired in
// E2-S2; Filterers remain empty until E2-S8 populates their
// ComponentSchema surface; an empty map for that category elides under
// `omitempty`.
func componentsSchemasBlock() ComponentsSchemasBlock {
	out := ComponentsSchemasBlock{}
	if aggs := aggregatorCapabilities(); len(aggs) > 0 {
		m := make(map[string]ComponentSchema, len(aggs))
		for _, op := range aggs {
			if len(op.ComponentSchema.Keys) == 0 && op.ComponentSchema.Mergeability == "" {
				continue
			}
			m[op.Name] = op.ComponentSchema
		}
		if len(m) > 0 {
			out.Aggregators = m
		}
	}
	if grps := grouperCapabilities(); len(grps) > 0 {
		m := make(map[string]ComponentSchema, len(grps))
		for _, op := range grps {
			if len(op.ComponentSchema.Keys) == 0 && op.ComponentSchema.Mergeability == "" {
				continue
			}
			m[op.Name] = op.ComponentSchema
		}
		if len(m) > 0 {
			out.Groupers = m
		}
	}
	return out
}

// BuildManifest constructs a deterministic Manifest from the current
// registries and capability tables. The result is safe to cache and
// share across goroutines; callers do not mutate the returned slices.
func BuildManifest() *Manifest {
	return BuildManifestWithExtensions(nil)
}

// BuildManifestWithExtensions constructs a Manifest that includes the
// embedder-registered extension surface. A nil snapshot is equivalent
// to BuildManifest — the Extensions block becomes the empty manifest
// (every category is `[]`, not `null`).
//
// descriptor stays free of service / processing imports; the snapshot
// is the only way the live ExtensionRegistry reaches this layer.
func BuildManifestWithExtensions(snap *ExtensionsSnapshot) *Manifest {
	allTests := append([]TestMeta{}, testCapabilities()...)
	allTests = append(allTests, postTestCapabilities()...)
	tier1, tier2 := partitionTier(allTests)

	return &Manifest{
		FormatVersion: "1.0",
		Commands:      commands(),
		Operations:    operations(),
		Components: Components{
			Aggregators: sortByName(aggregatorCapabilities()),
			Attributes:  sortByName(attributeCapabilities()),
			Filterers:   sortByName(filtererCapabilities()),
			Groupers:    sortByName(grouperCapabilities()),
			Windows:     sortByName(windowCapabilities()),
			Features:    sortByName(featureCapabilities()),
		},
		Tests:              tier1,
		PostTests:          tier2,
		Regressions:        sortRegressions(regressionCapabilities()),
		SynthDistributions: sortDistributions(distributionCapabilities()),
		ErrorCodesCount:    errorCodesCount(),
		ErrorDomains:       errorDomains(),
		ErrorCodes:         errorCodeNames(),
		MCPTools:           mcpToolCapabilities(),
		CohortTypes:        cohortFieldTypes(),
		Skills:             sortedSkills(),
		ExamplesCount:      examples.Count(),
		ExampleCategories:  examples.AllCategories(),
		ExampleTags:        examples.AllTags(),
		Extensions:         extensionsManifestFromSnapshot(snap),
		Facet:              facetCapability(),
		ProcessChain:       processChainCapability(),
		Join:               joinCapability(),
		Crosstab:           crosstabCapability(),
		Export:             exportCapability(),
		Overlays:           OverlayCapabilities(),
		ComponentsSchemas:  componentsSchemasBlock(),
	}
}

// sortedSkills returns the embedded skill metadata as descriptor SkillMeta
// records, sorted by Name for deterministic output. The skill index is
// immutable for the process lifetime, so we cache the result.
var (
	sortedSkillsOnce sync.Once
	sortedSkillsVal  []SkillMeta
)

func sortedSkills() []SkillMeta {
	sortedSkillsOnce.Do(func() {
		raw := skills.List()
		out := make([]SkillMeta, len(raw))
		for i, s := range raw {
			out[i] = SkillMeta{Name: s.Name, Description: s.Description}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		sortedSkillsVal = out
	})
	return sortedSkillsVal
}
