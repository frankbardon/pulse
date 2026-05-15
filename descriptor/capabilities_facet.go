package descriptor

// FacetCapability describes the rich-facet endpoint surfaced by
// pulse.FacetSchema / pulse_facet_schema. The manifest carries one
// FacetCapability entry under Manifest.Facet so LLM clients can detect
// the endpoint's feature set without inspecting the source.
type FacetCapability struct {
	// Name is the canonical entry identifier ("facet_schema").
	Name string `json:"name"`

	// SupportsDiscrete reports whether per-value count summaries are
	// available for categorical / boolean / geo fields.
	SupportsDiscrete bool `json:"supports_discrete"`

	// SupportsNumeric reports whether streaming statistics
	// (count, sum, min, max, mean, stddev) are produced for numeric
	// fields.
	SupportsNumeric bool `json:"supports_numeric"`

	// SupportsPercentiles reports whether NumericPercentiles are
	// computed via a buffered second-stage sort.
	SupportsPercentiles bool `json:"supports_percentiles"`

	// SupportsHistogram reports whether IncludeHistogram +
	// HistogramRange + HistogramBins are honoured.
	SupportsHistogram bool `json:"supports_histogram"`

	// SupportsAdditive reports whether AdditiveFields contribution
	// counts are computed.
	SupportsAdditive bool `json:"supports_additive"`

	// StreamableConditions lists the human-readable rules that govern
	// which requests run in a single pass vs. force the buffered
	// secondary sort. The order is stable and intended for LLM-side
	// reasoning, not strict parsing.
	StreamableConditions []string `json:"streamable_conditions"`
}

// facetCapability returns the canonical FacetCapability entry. Static
// today; bumps require updating skills/facet-design.md.
func facetCapability() FacetCapability {
	return FacetCapability{
		Name:                "facet_schema",
		SupportsDiscrete:    true,
		SupportsNumeric:     true,
		SupportsPercentiles: true,
		SupportsHistogram:   true,
		SupportsAdditive:    true,
		StreamableConditions: []string{
			"NumericPercentiles is empty (percentiles force a buffered per-field value slice + sort)",
			"IncludeHistogram=false OR HistogramRange supplies caller-known [min, max] bounds",
			"every base filterer is row-local streamable (FILTER_INCLUDE / FILTER_EXCLUDE / FILTER_RANGE / FILTER_GEO_*)",
		},
	}
}
