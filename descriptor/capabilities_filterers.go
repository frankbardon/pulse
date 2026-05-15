package descriptor

import "github.com/frankbardon/pulse/types"

// filtererCapabilities returns the metadata for every registered
// FiltererBuilder.
func filtererCapabilities() []Operator {
	return []Operator{
		{
			Name:          string(types.FILTER_INCLUDE),
			Category:      "filterer",
			Description:   "Keep records whose field value appears in the supplied Values list.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "record-level predicate (no emitted column)",
			Streamable:    true,
		},
		{
			Name:          string(types.FILTER_EXCLUDE),
			Category:      "filterer",
			Description:   "Drop records whose field value appears in the supplied Values list.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "record-level predicate (no emitted column)",
			Streamable:    true,
		},
		{
			Name:          string(types.FILTER_RANGE),
			Category:      "filterer",
			Description:   "Keep records whose numeric field value falls within [low, high]; Values=[low, high].",
			AcceptsTypes:  numericFieldTypes,
			EmitsTypeNote: "record-level predicate",
			Streamable:    true,
		},
		{
			Name:          string(types.FILTER_EXPRESSION),
			Category:      "filterer",
			Description:   "Keep records for which Expression evaluates truthy; reads any record field.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "record-level predicate",
			Streamable:    true,
		},
		{
			Name:          string(types.FILTER_GEO_WITHIN),
			Category:      "filterer",
			Description:   "Keep point_f64 records that lie inside the supplied polygon (Values lists vertices).",
			AcceptsTypes:  geoPointFieldTypes,
			EmitsTypeNote: "record-level predicate",
			Streamable:    true,
		},
		{
			Name:          string(types.FILTER_GEO_WITHIN_RADIUS_M),
			Category:      "filterer",
			Description:   "Keep point_f64 records within radius (m) of a reference point; Values=[lat, lon, radius_m].",
			AcceptsTypes:  geoPointFieldTypes,
			EmitsTypeNote: "record-level predicate",
			Streamable:    true,
		},
		{
			Name:          string(types.FILTER_NULL),
			Category:      "filterer",
			Description:   "Keep records based on null state of Field. Values=[\"is_null\"] keeps null-valued records; Values=[\"is_not_null\"] keeps non-null records.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "record-level predicate",
			Streamable:    true,
		},
	}
}
