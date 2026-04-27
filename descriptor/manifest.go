package descriptor

import (
	"sort"

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
	}
}

// cohortFieldTypes returns all field types as CohortFieldType descriptors.
func cohortFieldTypes() []CohortFieldType {
	var out []CohortFieldType
	for i := encoding.FieldType(0); i < encoding.FieldType(15); i++ {
		name := i.String()
		if len(name) > 7 && name[:7] == "unknown" {
			continue
		}
		out = append(out, CohortFieldType{
			Name:        name,
			Categorical: i.IsCategorical(),
		})
	}
	return out
}

// sortedStrings returns a sorted copy of string representations.
func sortedAggregationTypes() []string {
	raw := types.AllAggregationTypes()
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}

func sortedAttributeTypes() []string {
	raw := types.AllAttributeTypes()
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}

func sortedFiltererTypes() []string {
	raw := types.AllFiltererTypes()
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}

func sortedGroupTypes() []string {
	raw := types.AllGroupTypes()
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
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
		},
		CohortTypes: cohortFieldTypes(),
		Skills:      []SkillMeta{},
	}
}
