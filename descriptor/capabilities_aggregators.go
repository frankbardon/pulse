package descriptor

import "github.com/frankbardon/pulse/types"

// numericFieldTypes is the canonical list of cohort field types that
// participate in numeric aggregations. Sorted alphabetically for golden
// stability.
var numericFieldTypes = []string{
	"date",
	"f32",
	"f64",
	"nullable_decimal128",
	"nullable_u16",
	"nullable_u4",
	"nullable_u8",
	"u16",
	"u32",
	"u64",
	"u8",
}

// numericFieldTypesNoDecimal lists numeric types excluding the
// fixed-point decimal types. Used by aggregators that operate in float64
// space (variance, stddev, skewness, kurtosis).
var numericFieldTypesNoDecimal = []string{
	"date",
	"f32",
	"f64",
	"nullable_u16",
	"nullable_u4",
	"nullable_u8",
	"u16",
	"u32",
	"u64",
	"u8",
}

// numericFieldTypesAnalytics widens numericFieldTypes with the bit-packed
// integer encodings (nullable_bool, packed_bool). These types store small
// non-negative integers (0/1 for the booleans, 0..14 for nullable_u4 with
// 0x0F as null sentinel) which the analytics aggregators consume as
// proportions or ordinal means without an ATTR_FORMULA cast. Sorted
// alphabetically for golden stability.
var numericFieldTypesAnalytics = []string{
	"date",
	"f32",
	"f64",
	"nullable_bool",
	"nullable_decimal128",
	"nullable_u16",
	"nullable_u4",
	"nullable_u8",
	"packed_bool",
	"u16",
	"u32",
	"u64",
	"u8",
}

// numericFieldTypesAnalyticsNoDecimal mirrors numericFieldTypesAnalytics
// without the decimal128 types — for aggregators that operate purely in
// float64 (stddev, variance, skewness, kurtosis, zscore).
var numericFieldTypesAnalyticsNoDecimal = []string{
	"date",
	"f32",
	"f64",
	"nullable_bool",
	"nullable_u16",
	"nullable_u4",
	"nullable_u8",
	"packed_bool",
	"u16",
	"u32",
	"u64",
	"u8",
}

// allCohortFieldTypes lists every field type without restriction (used by
// COUNT, MODE, FREQUENCY, DISTINCT_COUNT which operate on any field).
var allCohortFieldTypes = []string{
	"categorical_u16",
	"categorical_u32",
	"categorical_u8",
	"date",
	"decimal128",
	"f32",
	"f64",
	"nullable_bool",
	"nullable_decimal128",
	"nullable_u16",
	"nullable_u4",
	"nullable_u8",
	"packed_bool",
	"u16",
	"u32",
	"u64",
	"u8",
}

// aggregatorCapabilities is the static metadata table for every
// registered aggregator. Order is irrelevant; manifest assembly sorts by
// Name. The TestManifestOperatorsComplete gate enforces that every entry
// in types.AllAggregationTypes() has a row here.
func aggregatorCapabilities() []Operator {
	return []Operator{
		{
			Name:          string(types.AGG_COUNT),
			Category:      "aggregator",
			Description:   "Count records that pass the active filter, optionally by group.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_SUM),
			Category:      "aggregator",
			Description:   "Sum the numeric values of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64 (decimal128 preserved when input is decimal)",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_AVERAGE),
			Category:      "aggregator",
			Description:   "Arithmetic mean of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_MIN),
			Category:      "aggregator",
			Description:   "Smallest non-null value of the field.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_MAX),
			Category:      "aggregator",
			Description:   "Largest non-null value of the field.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_STDDEV),
			Category:      "aggregator",
			Description:   "Population standard deviation via Welford's online algorithm.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_RANGE),
			Category:      "aggregator",
			Description:   "Spread (max minus min) of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_FREQUENCY),
			Category:      "aggregator",
			Description:   "Per-distinct-value count of the field (returned as map in Details).",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "map[string]int64",
			Streamable:    true,
		},
		{
			Name:           string(types.AGG_ZSCORE),
			Category:       "aggregator",
			Description:    "Standardized z-score aggregate (mean-centered, stddev-scaled summary).",
			AcceptsTypes:   numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote:  "scalar float64",
			Streamable:     false,
			StreamableHint: "Streaming uses online Welford moments; the ZSCORE aggregate finalize step needs the full deviation sum.",
		},
		{
			Name:           string(types.AGG_MEDIAN),
			Category:       "aggregator",
			Description:    "50th percentile of the field; requires sorting the full value set.",
			AcceptsTypes:   numericFieldTypesAnalytics,
			EmitsTypeNote:  "scalar float64",
			Streamable:     false,
			StreamableHint: "Use AGG_AVERAGE for a streaming central-tendency proxy, or accept the buffered path.",
		},
		{
			Name:          string(types.AGG_VARIANCE),
			Category:      "aggregator",
			Description:   "Population variance via Welford's online algorithm.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_MODE),
			Category:      "aggregator",
			Description:   "Most-frequent value of the field (ties broken by first-seen order).",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "string (echoes the dictionary value or stringified scalar)",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_SKEWNESS),
			Category:      "aggregator",
			Description:   "Bias-corrected skewness via online moments.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_KURTOSIS),
			Category:      "aggregator",
			Description:   "Bias-corrected excess kurtosis via online moments.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:          string(types.AGG_DISTINCT_COUNT),
			Category:      "aggregator",
			Description:   "Count of distinct non-null values across the input set.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
		},
		{
			Name:        string(types.AGG_PERCENTILE),
			Category:    "aggregator",
			Description: "Configurable percentile of the field; requires sorting the full value set.",
			Params: []Param{
				{
					Name:        "percentile",
					Type:        "float",
					Required:    true,
					Description: "Percentile to compute, in [0, 100]. e.g. 95 for p95.",
				},
			},
			AcceptsTypes:   numericFieldTypesAnalytics,
			EmitsTypeNote:  "scalar float64",
			Streamable:     false,
			StreamableHint: "Use AGG_AVERAGE or accept the buffered path; exact percentiles need sorted input.",
		},
		{
			Name:          string(types.AGG_NULL_COUNT),
			Category:      "aggregator",
			Description:   "Count records where the field is null. Inverse of AGG_COUNT, which counts non-null records.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
		},
		{
			Name:        string(types.AGG_WEIGHTED_MEAN),
			Category:    "aggregator",
			Description: "Weighted arithmetic mean: sum(field * weight) / sum(weight). Streaming Chan-Welford recurrence.",
			Params: []Param{
				{
					Name:        "weight_field",
					Type:        "string",
					Required:    true,
					Description: "Schema field whose value is the per-row weight. Rows with a null weight or weight==0 are skipped.",
				},
			},
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
		},
		{
			Name:        string(types.AGG_RATIO),
			Category:    "aggregator",
			Description: "Emits sum(numerator_field) / sum(denominator_field). The Aggregation's own Field is ignored. Denominator-zero yields NaN.",
			Params: []Param{
				{
					Name:        "numerator_field",
					Type:        "string",
					Required:    true,
					Description: "Schema field summed as the numerator.",
				},
				{
					Name:        "denominator_field",
					Type:        "string",
					Required:    true,
					Description: "Schema field summed as the denominator.",
				},
			},
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "scalar float64 (NaN when denominator sum == 0)",
			Streamable:    true,
		},
		{
			Name:        string(types.AGG_CI_LOWER),
			Category:    "aggregator",
			Description: "Lower bound of the confidence interval for the mean. Method \"normal\" streams via Welford and the Beasley-Springer-Moro inverse-normal quantile.",
			Params: []Param{
				{
					Name:        "confidence",
					Type:        "float",
					Required:    false,
					Default:     0.95,
					Description: "Confidence level in the open interval (0, 1). Default 0.95.",
				},
				{
					Name:        "method",
					Type:        "string",
					Required:    false,
					Default:     "normal",
					Description: "\"normal\" (streamable Welford) today; \"bootstrap\" reserved for a buffered follow-up.",
				},
			},
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64 (NaN when n < 2)",
			Streamable:    true,
		},
		{
			Name:        string(types.AGG_CI_UPPER),
			Category:    "aggregator",
			Description: "Upper bound of the confidence interval for the mean. See AGG_CI_LOWER for params and methods.",
			Params: []Param{
				{
					Name:        "confidence",
					Type:        "float",
					Required:    false,
					Default:     0.95,
					Description: "Confidence level in the open interval (0, 1). Default 0.95.",
				},
				{
					Name:        "method",
					Type:        "string",
					Required:    false,
					Default:     "normal",
					Description: "\"normal\" (streamable Welford) today; \"bootstrap\" reserved for a buffered follow-up.",
				},
			},
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64 (NaN when n < 2)",
			Streamable:    true,
		},
	}
}
