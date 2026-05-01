package descriptor

import (
	"sort"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Command describes a CLI leaf command in the manifest.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Components lists all registered processing components.
type Components struct {
	Aggregators []string `json:"aggregators"`
	Attributes  []string `json:"attributes"`
	Filterers   []string `json:"filterers"`
	Groupers    []string `json:"groupers"`
	Windows     []string `json:"windows"`
}

// CohortFieldType describes a field type available in .pulse files.
type CohortFieldType struct {
	Name        string `json:"name"`
	Categorical bool   `json:"categorical"`
}

// SkillMeta describes a bundled skill.
type SkillMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Manifest is the root self-description of the Pulse system.
type Manifest struct {
	FormatVersion string            `json:"format_version"`
	Commands      []Command         `json:"commands"`
	Components    Components        `json:"components"`
	CohortTypes   []CohortFieldType `json:"cohort_types"`
	Skills        []SkillMeta       `json:"skills"`
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
	}
}

// cohortFieldTypes returns all field types as CohortFieldType descriptors.
func cohortFieldTypes() []CohortFieldType {
	var out []CohortFieldType
	for i := range 15 {
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

// Component type lists are immutable after process init: the registries in
// types/ never mutate at runtime. We compute the sorted slices exactly once
// and return the cached value on subsequent calls. Callers do not mutate
// these slices (they JSON-marshal the manifest), so sharing the underlying
// array across calls is safe.
var (
	sortedAggregationTypesOnce sync.Once
	sortedAggregationTypesVal  []string

	sortedAttributeTypesOnce sync.Once
	sortedAttributeTypesVal  []string

	sortedFiltererTypesOnce sync.Once
	sortedFiltererTypesVal  []string

	sortedGroupTypesOnce sync.Once
	sortedGroupTypesVal  []string

	sortedWindowTypesOnce sync.Once
	sortedWindowTypesVal  []string
)

// sortedAggregationTypes returns the cached sorted list of aggregation type
// identifiers. The list is computed on first call and shared on every call
// thereafter.
func sortedAggregationTypes() []string {
	sortedAggregationTypesOnce.Do(func() {
		raw := types.AllAggregationTypes()
		out := make([]string, len(raw))
		for i, v := range raw {
			out[i] = string(v)
		}
		sort.Strings(out)
		sortedAggregationTypesVal = out
	})
	return sortedAggregationTypesVal
}

func sortedAttributeTypes() []string {
	sortedAttributeTypesOnce.Do(func() {
		raw := types.AllAttributeTypes()
		out := make([]string, len(raw))
		for i, v := range raw {
			out[i] = string(v)
		}
		sort.Strings(out)
		sortedAttributeTypesVal = out
	})
	return sortedAttributeTypesVal
}

func sortedFiltererTypes() []string {
	sortedFiltererTypesOnce.Do(func() {
		raw := types.AllFiltererTypes()
		out := make([]string, len(raw))
		for i, v := range raw {
			out[i] = string(v)
		}
		sort.Strings(out)
		sortedFiltererTypesVal = out
	})
	return sortedFiltererTypesVal
}

func sortedGroupTypes() []string {
	sortedGroupTypesOnce.Do(func() {
		raw := types.AllGroupTypes()
		out := make([]string, len(raw))
		for i, v := range raw {
			out[i] = string(v)
		}
		sort.Strings(out)
		sortedGroupTypesVal = out
	})
	return sortedGroupTypesVal
}

func sortedWindowTypes() []string {
	sortedWindowTypesOnce.Do(func() {
		raw := types.AllWindowTypes()
		out := make([]string, len(raw))
		for i, v := range raw {
			out[i] = string(v)
		}
		sort.Strings(out)
		sortedWindowTypesVal = out
	})
	return sortedWindowTypesVal
}

// BuildManifest constructs a deterministic Manifest from the current registries.
func BuildManifest() *Manifest {
	return &Manifest{
		FormatVersion: "1.0",
		Commands:      commands(),
		Components: Components{
			Aggregators: sortedAggregationTypes(),
			Attributes:  sortedAttributeTypes(),
			Filterers:   sortedFiltererTypes(),
			Groupers:    sortedGroupTypes(),
			Windows:     sortedWindowTypes(),
		},
		CohortTypes: cohortFieldTypes(),
		Skills:      []SkillMeta{},
	}
}
