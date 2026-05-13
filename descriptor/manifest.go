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
type CohortFieldType struct {
	Name                 string   `json:"name"`
	Categorical          bool     `json:"categorical"`
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
	FormatVersion      string             `json:"format_version"`
	Commands           []Command          `json:"commands"`
	Components         Components         `json:"components"`
	Tests              []TestMeta         `json:"tests"`
	PostTests          []TestMeta         `json:"post_tests"`
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
}

// commands returns the default set of CLI leaf commands.
func commands() []Command {
	return []Command{
		{Name: "process", Description: "Execute a processing request against a cohort"},
		{Name: "compose", Description: "Execute multiple processing requests in batch"},
		{Name: "sample", Description: "Return sample rows from a cohort"},
		{Name: "facet", Description: "Return distinct values for a field"},
		{Name: "inspect", Description: "Inspect a .pulse file header and schema"},
		{Name: "predict", Description: "Validate a request without executing"},
		{Name: "manifest", Description: "Output the root manifest"},
		{Name: "mcp", Description: "Serve the Model Context Protocol over stdio"},
		{Name: "synth", Description: "Generate synthetic .pulse cohorts from a schema or profile"},
		{Name: "profile", Description: "Capture statistical summaries of cohorts for synthesis"},
	}
}

// rawCohortFieldTypes returns the bare (name, categorical) tuples for
// every defined field type. Compatible* cross-refs are computed by
// cohortFieldTypes() from the operator capability tables.
func rawCohortFieldTypes() []CohortFieldType {
	var out []CohortFieldType
	for i := range 19 {
		ft := encoding.FieldType(i)
		name := ft.String()
		if len(name) > 7 && name[:7] == "unknown" {
			continue
		}
		out = append(out, CohortFieldType{
			Name:        name,
			Categorical: ft.IsCategorical(),
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

// BuildManifest constructs a deterministic Manifest from the current
// registries and capability tables. The result is safe to cache and
// share across goroutines; callers do not mutate the returned slices.
func BuildManifest() *Manifest {
	allTests := append([]TestMeta{}, testCapabilities()...)
	allTests = append(allTests, postTestCapabilities()...)
	tier1, tier2 := partitionTier(allTests)

	return &Manifest{
		FormatVersion: "1.0",
		Commands:      commands(),
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
