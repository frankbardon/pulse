package descriptor

import "github.com/frankbardon/pulse/descriptor"

// facetCapability returns the canonical FacetCapability entry. Static
// today; bumps require updating skills/facet-design.md.
func facetCapability() descriptor.FacetCapability {
	return descriptor.FacetCapability{
		Name:                "facet_schema",
		SupportsDiscrete:    true,
		SupportsNumeric:     true,
		SupportsPercentiles: true,
		SupportsHistogram:   true,
		SupportsAdditive:    true,
		SupportsOverlays:    true,
		// Sorted alphabetically by canonical OverlayKind constant so
		// MCP / CLI surfaces see a deterministic enum.
		SupportedOverlayKinds: []string{
			"OVERLAY_CHISQ_VS_POP",
			"OVERLAY_INDEX_VS_POP",
			"OVERLAY_KS_VS_POP",
			"OVERLAY_ZSCORE_VS_POP",
		},
		StreamableConditions: []string{
			"NumericPercentiles is empty (percentiles force a buffered per-field value slice + sort)",
			"IncludeHistogram=false OR HistogramRange supplies caller-known [min, max] bounds",
			"every base filterer is row-local streamable (FILTER_INCLUDE / FILTER_EXCLUDE / FILTER_RANGE / FILTER_GEO_*)",
		},
	}
}
