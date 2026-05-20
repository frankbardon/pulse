package types

// FacetRequest specifies a multi-field rich facet computation. It is the
// input shape consumed by pulse.FacetSchema. The simpler single-field
// pulse.Facet entry point accepts only (path, field) and returns
// distinct values; FacetSchema returns per-value counts, null tallies,
// numeric statistics, optional percentiles, optional histograms, and
// optional "additive" contribution counts.
type FacetRequest struct {
	// Cohort selects the .pulse file. Same shape as Request.Cohort.
	Cohort *Cohort `json:"cohort,omitempty"`

	// Fields is the explicit list of fields to summarise. Empty is an
	// error.
	Fields []string `json:"fields"`

	// Filterers optionally narrow the record set before accumulation.
	// Same semantics as Request.Filterers.
	Filterers []*Filterer `json:"filterers,omitempty"`

	// AdditiveFields lists fields for which the engine computes
	// "contribution" counts — accumulated independently of the matching
	// base filter, with the named field's own predicates stripped so all
	// values are counted equally. Empty = no additive analysis.
	AdditiveFields []string `json:"additive_fields,omitempty"`

	// DiscreteTopK caps the number of distinct values returned per
	// discrete field. Zero = no cap (return all). Capped fields carry a
	// TruncatedAt count in the response so callers know what was dropped.
	DiscreteTopK int `json:"discrete_top_k,omitempty"`

	// NumericPercentiles lists percentile points (strictly in (0, 1)) to
	// compute for each numeric field. Nil/empty = none. Adding any
	// percentile forces the buffered execution path.
	NumericPercentiles []float64 `json:"numeric_percentiles,omitempty"`

	// IncludeHistogram, when true, computes a fixed-width histogram for
	// each numeric field with HistogramBins buckets. Defaults to 20 bins
	// when bins unset and IncludeHistogram=true. Streaming-compatible
	// only when HistogramRange is supplied.
	IncludeHistogram bool `json:"include_histogram,omitempty"`

	// HistogramBins is the bucket count when IncludeHistogram is true.
	// Defaults to 20 when zero. Capped at 256.
	HistogramBins int `json:"histogram_bins,omitempty"`

	// HistogramRange supplies the [min, max] bounds for fixed-width
	// histogram binning. Required when IncludeHistogram is true. The two
	// values must satisfy min < max.
	HistogramRange [2]float64 `json:"histogram_range,omitempty"`

	// Labels rewrites or augments per-value keys in the response
	// using embedder-registered label tables. Each binding's Field
	// must appear in Fields and reference a categorical column;
	// numeric fields ignore labels. See types.LabelBinding for
	// semantics.
	Labels []*LabelBinding `json:"labels,omitempty"`
}

// FacetResult is the response shape returned by pulse.FacetSchema.
type FacetResult struct {
	// Fields maps each requested field name to its summary.
	Fields map[string]*FacetField `json:"fields"`

	// Additive maps each AdditiveFields entry to its independent
	// contribution counts. Same FacetField shape; values reflect counts
	// with the field's own filter predicates stripped.
	Additive map[string]*FacetField `json:"additive,omitempty"`

	// TotalRecords is the cohort's header record count (unfiltered).
	TotalRecords int64 `json:"total_records"`

	// FilteredRecords is the count of records passing Filterers.
	FilteredRecords int64 `json:"filtered_records"`

	// Warnings carry per-field diagnostics (top-K truncation, etc.).
	Warnings []string `json:"warnings,omitempty"`
}

// FacetField wraps either a discrete or numeric summary.
type FacetField struct {
	// Kind is "discrete" | "numeric".
	Kind string `json:"kind"`

	// TypeName is the field's Pulse type as a string ("u8", "f64",
	// "categorical_u16", ...).
	TypeName string `json:"type_name"`

	// Description is the field's schema description.
	Description string `json:"description,omitempty"`

	// NullCount is the number of filtered records where this field was
	// null.
	NullCount int64 `json:"null_count"`

	// Discrete is non-nil when Kind == "discrete".
	Discrete *FacetDiscrete `json:"discrete,omitempty"`

	// Numeric is non-nil when Kind == "numeric".
	Numeric *FacetNumeric `json:"numeric,omitempty"`
}

// FacetDiscrete summarises a categorical/boolean field.
type FacetDiscrete struct {
	// Values is the per-value count list, sorted descending by count
	// (ties broken ascending by value-string for determinism).
	Values []FacetValueCount `json:"values"`

	// DistinctCount is the total distinct non-null values seen. May
	// exceed len(Values) when DiscreteTopK was set.
	DistinctCount int64 `json:"distinct_count"`

	// TruncatedAt is the count of values dropped by DiscreteTopK
	// truncation. Zero means no cap was applied.
	TruncatedAt int `json:"truncated_at,omitempty"`
}

// FacetValueCount is one value/count tuple inside FacetDiscrete.Values.
type FacetValueCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// FacetNumeric summarises a numeric field. Min, Max, Mean, StdDev, Sum,
// and Count are always populated; Percentiles and Histogram are
// populated when the request asked for them.
type FacetNumeric struct {
	Min         float64            `json:"min"`
	Max         float64            `json:"max"`
	Mean        float64            `json:"mean"`
	StdDev      float64            `json:"stddev"`
	Count       int64              `json:"count"`
	Sum         float64            `json:"sum"`
	Percentiles map[string]float64 `json:"percentiles,omitempty"`
	Histogram   *FacetHistogram    `json:"histogram,omitempty"`
}

// FacetHistogram is a fixed-width binning of a numeric field. Bins is a
// slice of length HistogramBins; bin i covers [Min + i*step, Min +
// (i+1)*step) where step = (Max-Min)/HistogramBins. Values that fall on
// Max land in the last bin.
type FacetHistogram struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Bins []int64 `json:"bins"`
}
