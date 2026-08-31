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

// numericFieldTypesStrictScalar is the strict scalar numeric family —
// u8/u16/u32/u64/f32/f64 only. Excludes decimal128, date, and every
// bit-packed encoding (nullable_u4, nullable_bool, packed_bool). Used by
// aggregators that drive a single float64 hot path with no per-type
// branch (AGG_WELFORD — see processing/aggregator_welford.go's factory
// gate, which mirrors the TEST_WELCH field-type policy).
var numericFieldTypesStrictScalar = []string{
	"f32",
	"f64",
	"nullable_u16",
	"nullable_u8",
	"u16",
	"u32",
	"u64",
	"u8",
}

// allCohortFieldTypes lists every field type without restriction (used by
// COUNT, MODE, FREQUENCY, DISTINCT_COUNT which operate on any field).
// "every field type" is literal: a registered field type missing from
// this list under-declares the operators that carry it, so the manifest
// would tell a caller that AGG_COUNT cannot count a column it counts
// fine. Keep it in step with encoding's FieldType registry.
var allCohortFieldTypes = []string{
	"categorical_u16",
	"categorical_u32",
	"categorical_u8",
	"date",
	"datetime",
	"decimal128",
	"f32",
	"f64",
	"nullable_bool",
	"nullable_decimal128",
	"nullable_u16",
	"nullable_u4",
	"nullable_u8",
	"packed_bool",
	"set_u16",
	"set_u32",
	"set_u64",
	"set_u8",
	"u16",
	"u32",
	"u64",
	"u8",
}

// setFieldTypes lists the bitmask multi-select field types. Used by every
// AGG_SET_* / FILTER_SET_* / GROUP_SET_* / ATTR_SET_* operator — they
// reject non-set fields at construction time.
var setFieldTypes = []string{
	"set_u16",
	"set_u32",
	"set_u64",
	"set_u8",
}

// universalAggFloorKeys is the {n, n_null} pair every aggregator's
// component schema declares. The orchestrator's universal-floor pass
// emits these keys unconditionally so embedders can rely on them being
// present even when MetaAggregator returns an empty extra map. Declared
// centrally so the floor stays byte-equal across every entry.
func universalAggFloorKeys() []ComponentKey {
	return []ComponentKey{
		{Name: "n", Type: "int", Description: "Number of records aggregated (non-null inputs)."},
		{Name: "n_null", Type: "int", Description: "Number of null inputs encountered."},
	}
}

// aggSchema composes a ComponentSchema by prepending the universal
// floor to the operator-specific keys (in emission order) and tagging
// the result with the operator's mergeability classification. Keep the
// extra slice nil for floor-only operators (AGG_COUNT, AGG_NULL_COUNT)
// so the call site stays clean.
func aggSchema(merge ComponentsMergeability, extra ...ComponentKey) ComponentSchema {
	floor := universalAggFloorKeys()
	keys := make([]ComponentKey, 0, len(floor)+len(extra))
	keys = append(keys, floor...)
	keys = append(keys, extra...)
	return ComponentSchema{Keys: keys, Mergeability: merge}
}

// aggregatorCapabilities is the static metadata table for every
// registered aggregator. Order is irrelevant; manifest assembly sorts by
// Name. The TestManifestOperatorsComplete gate enforces that every entry
// in types.AllAggregationTypes() has a row here.
func aggregatorCapabilities() []Operator {
	return []Operator{
		{
			Name:            string(types.AGG_COUNT),
			Category:        "aggregator",
			Description:     "Count records that pass the active filter, optionally by group.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "scalar int64",
			Streamable:      true,
			ComponentSchema: aggSchema(Mergeable),
		},
		{
			Name:          string(types.AGG_SUM),
			Category:      "aggregator",
			Description:   "Sum the numeric values of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64 (decimal128 preserved when input is decimal)",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "sum", Type: "float64", Description: "Running sum of non-null field values."},
			),
		},
		{
			Name:          string(types.AGG_AVERAGE),
			Category:      "aggregator",
			Description:   "Arithmetic mean of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "sum", Type: "float64", Description: "Running sum of non-null field values; combined with n to recover the mean."},
			),
		},
		{
			Name:          string(types.AGG_MIN),
			Category:      "aggregator",
			Description:   "Smallest non-null value of the field.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "min", Type: "float64", Description: "Smallest non-null value observed."},
			),
		},
		{
			Name:          string(types.AGG_MAX),
			Category:      "aggregator",
			Description:   "Largest non-null value of the field.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "max", Type: "float64", Description: "Largest non-null value observed."},
			),
		},
		{
			Name:          string(types.AGG_STDDEV),
			Category:      "aggregator",
			Description:   "Population standard deviation via Welford's online algorithm.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mean", Type: "float64", Description: "Running Welford mean of non-null field values."},
				ComponentKey{Name: "m2", Type: "float64", Description: "Welford second-moment accumulator (sum of squared deviations from the running mean)."},
				ComponentKey{Name: "variance", Type: "float64", Description: "Population variance derived from m2 / n."},
				ComponentKey{Name: "stddev", Type: "float64", Description: "Population standard deviation (square root of variance)."},
			),
		},
		{
			Name:          string(types.AGG_RANGE),
			Category:      "aggregator",
			Description:   "Spread (max minus min) of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "min", Type: "float64", Description: "Smallest non-null value observed."},
				ComponentKey{Name: "max", Type: "float64", Description: "Largest non-null value observed."},
			),
		},
		{
			Name:          string(types.AGG_FREQUENCY),
			Category:      "aggregator",
			Description:   "Per-distinct-value count of the field (returned as map in Details).",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "map[string]int64",
			Streamable:    true,
			ComponentSchema: aggSchema(Partial,
				ComponentKey{Name: "distinct_count", Type: "int", Description: "Number of distinct values observed."},
				ComponentKey{Name: "mode_value", Type: "any", Description: "Most-frequent value (ties broken by first-seen order)."},
				ComponentKey{Name: "mode_count", Type: "int", Description: "Row count of the modal value."},
			),
		},
		{
			Name:           string(types.AGG_ZSCORE),
			Category:       "aggregator",
			Description:    "Standardized z-score aggregate (mean-centered, stddev-scaled summary).",
			AcceptsTypes:   numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote:  "scalar float64",
			Streamable:     false,
			StreamableHint: "Streaming uses online Welford moments; the ZSCORE aggregate finalize step needs the full deviation sum.",
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "pop_mean", Type: "float64", Description: "Population mean used as the z-score center."},
				ComponentKey{Name: "pop_stddev", Type: "float64", Description: "Population standard deviation used as the z-score scale."},
				ComponentKey{Name: "target_value", Type: "float64", Description: "Value being standardized against the population summary."},
				ComponentKey{Name: "zscore", Type: "float64", Description: "Standardized score: (target_value - pop_mean) / pop_stddev."},
			),
		},
		{
			Name:           string(types.AGG_MEDIAN),
			Category:       "aggregator",
			Description:    "50th percentile of the field; requires sorting the full value set.",
			AcceptsTypes:   numericFieldTypesAnalytics,
			EmitsTypeNote:  "scalar float64",
			Streamable:     false,
			StreamableHint: "Use AGG_AVERAGE for a streaming central-tendency proxy, or accept the buffered path.",
			ComponentSchema: aggSchema(None,
				ComponentKey{Name: "position_low", Type: "int", Description: "Lower index used to bracket the median in the sorted value set."},
				ComponentKey{Name: "position_high", Type: "int", Description: "Upper index used to bracket the median in the sorted value set."},
				ComponentKey{Name: "median", Type: "float64", Description: "Resolved median value (linear interpolation between the bracketing positions)."},
			),
		},
		{
			Name:          string(types.AGG_VARIANCE),
			Category:      "aggregator",
			Description:   "Population variance via Welford's online algorithm.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mean", Type: "float64", Description: "Running Welford mean of non-null field values."},
				ComponentKey{Name: "m2", Type: "float64", Description: "Welford second-moment accumulator (sum of squared deviations from the running mean)."},
				ComponentKey{Name: "variance", Type: "float64", Description: "Population variance derived from m2 / n."},
			),
		},
		{
			Name:          string(types.AGG_MODE),
			Category:      "aggregator",
			Description:   "Most-frequent value of the field (ties broken by first-seen order).",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "string (echoes the dictionary value or stringified scalar)",
			Streamable:    true,
			ComponentSchema: aggSchema(Partial,
				ComponentKey{Name: "value", Type: "any", Description: "Most-frequent value observed (first-seen tie-break)."},
				ComponentKey{Name: "count", Type: "int", Description: "Row count of the modal value."},
				ComponentKey{Name: "distinct_count", Type: "int", Description: "Number of distinct values observed."},
				ComponentKey{Name: "tie_count", Type: "int", Description: "Number of values tied with the mode at the same maximum count."},
			),
		},
		{
			Name:          string(types.AGG_SKEWNESS),
			Category:      "aggregator",
			Description:   "Bias-corrected skewness via online moments.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mean", Type: "float64", Description: "Running mean of non-null field values."},
				ComponentKey{Name: "m2", Type: "float64", Description: "Second-moment accumulator (sum of squared deviations from the running mean)."},
				ComponentKey{Name: "m3", Type: "float64", Description: "Third-moment accumulator (sum of cubed deviations from the running mean)."},
				ComponentKey{Name: "skewness", Type: "float64", Description: "Bias-corrected skewness derived from m2, m3, and n."},
			),
		},
		{
			Name:          string(types.AGG_KURTOSIS),
			Category:      "aggregator",
			Description:   "Bias-corrected excess kurtosis via online moments.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mean", Type: "float64", Description: "Running mean of non-null field values."},
				ComponentKey{Name: "m2", Type: "float64", Description: "Second-moment accumulator (sum of squared deviations from the running mean)."},
				ComponentKey{Name: "m3", Type: "float64", Description: "Third-moment accumulator (sum of cubed deviations from the running mean)."},
				ComponentKey{Name: "m4", Type: "float64", Description: "Fourth-moment accumulator (sum of fourth-power deviations from the running mean)."},
				ComponentKey{Name: "kurtosis", Type: "float64", Description: "Bias-corrected excess kurtosis derived from m2, m4, and n."},
			),
		},
		{
			Name:          string(types.AGG_DISTINCT_COUNT),
			Category:      "aggregator",
			Description:   "Count of distinct non-null values across the input set.",
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
			ComponentSchema: aggSchema(Partial,
				ComponentKey{Name: "cardinality", Type: "int", Description: "Number of distinct non-null values observed."},
			),
		},
		{
			Name:        string(types.AGG_DISTINCT_SUM),
			Category:    "aggregator",
			Description: "Sum the field once per distinct key named by distinct_by. A key seen on N records contributes its value once, not N times; the first value observed for a key wins.",
			Params: []Param{
				{
					Name:        "distinct_by",
					Type:        "string",
					Required:    true,
					Description: "Schema field whose value is the distinct key. Required — an absent or empty distinct_by is refused, never defaulted to the aggregation's own field. Rows with a null key or a null value contribute nothing and register no key.",
				},
			},
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64 (sum over distinct keys)",
			Streamable:    true,
			ComponentSchema: aggSchema(Partial,
				ComponentKey{Name: "sum", Type: "float64", Description: "Sum of the first value observed for each distinct key."},
				ComponentKey{Name: "distinct_count", Type: "int", Description: "Number of distinct keys that contributed to the sum."},
			),
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
			ComponentSchema: aggSchema(None,
				ComponentKey{Name: "p", Type: "float64", Description: "Percentile requested, in [0, 100]."},
				ComponentKey{Name: "position", Type: "int", Description: "Index into the sorted value set used to resolve the percentile."},
				ComponentKey{Name: "lower", Type: "float64", Description: "Lower bracketing value used during interpolation."},
				ComponentKey{Name: "upper", Type: "float64", Description: "Upper bracketing value used during interpolation."},
				ComponentKey{Name: "method", Type: "string", Description: "Interpolation method (e.g. \"linear\")."},
				ComponentKey{Name: "value", Type: "float64", Description: "Resolved percentile value."},
			),
		},
		{
			Name:            string(types.AGG_NULL_COUNT),
			Category:        "aggregator",
			Description:     "Count records where the field is null. Inverse of AGG_COUNT, which counts non-null records.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "scalar int64",
			Streamable:      true,
			ComponentSchema: aggSchema(Mergeable),
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
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "sum_weighted", Type: "float64", Description: "Running sum of (field * weight) across contributing rows."},
				ComponentKey{Name: "sum_weights", Type: "float64", Description: "Running sum of weights across contributing rows."},
				ComponentKey{Name: "weighted_mean", Type: "float64", Description: "Resolved weighted mean: sum_weighted / sum_weights."},
			),
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
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "numerator", Type: "float64", Description: "Running sum of the numerator field."},
				ComponentKey{Name: "denominator", Type: "float64", Description: "Running sum of the denominator field."},
				ComponentKey{Name: "ratio", Type: "float64", Description: "Resolved ratio: numerator / denominator (NaN when denominator is zero)."},
			),
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
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mean", Type: "float64", Description: "Welford-running mean of the field."},
				ComponentKey{Name: "stderr", Type: "float64", Description: "Standard error of the mean derived from variance and n."},
				ComponentKey{Name: "alpha", Type: "float64", Description: "Significance level (1 - confidence)."},
				ComponentKey{Name: "t_critical", Type: "float64", Description: "Critical value used to scale the standard error."},
				ComponentKey{Name: "lower", Type: "float64", Description: "Resolved lower bound of the confidence interval."},
			),
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
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mean", Type: "float64", Description: "Welford-running mean of the field."},
				ComponentKey{Name: "stderr", Type: "float64", Description: "Standard error of the mean derived from variance and n."},
				ComponentKey{Name: "alpha", Type: "float64", Description: "Significance level (1 - confidence)."},
				ComponentKey{Name: "t_critical", Type: "float64", Description: "Critical value used to scale the standard error."},
				ComponentKey{Name: "upper", Type: "float64", Description: "Resolved upper bound of the confidence interval."},
			),
		},
		{
			Name:          string(types.AGG_WELFORD),
			Category:      "aggregator",
			Description:   "Streaming Welford-Pébaÿ moment triple — running mean, unbiased sample variance (n-1), and observed count — over a numeric field. Returns the running mean as the scalar fallback; rich payload is a typed WelfordTriple for overlay handlers (OVERLAY_T_CELL / OVERLAY_Z_CELL) to consume without re-deriving variance from raw rows. Margin recompute (variance does not pool by addition).",
			AcceptsTypes:  numericFieldTypesStrictScalar,
			EmitsTypeNote: "scalar float64 (running mean; NaN when no rows); rich WelfordTriple{Mean, Variance, N}",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mean", Type: "float64", Description: "Running Welford mean of non-null field values."},
				ComponentKey{Name: "m2", Type: "float64", Description: "Welford second-moment accumulator (sum of squared deviations from the running mean)."},
				ComponentKey{Name: "variance", Type: "float64", Description: "Unbiased sample variance derived from m2 / (n-1)."},
				ComponentKey{Name: "stddev", Type: "float64", Description: "Sample standard deviation (square root of variance)."},
			),
		},
		{
			Name:          string(types.AGG_SET_UNION),
			Category:      "aggregator",
			Description:   "Bitwise-OR a set field across rows; returns the resolved labels for every bit set in any contributing row.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "rich []string (resolved labels); scalar fallback = popcount of the union mask",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mask_union", Type: "uint64", Description: "Bitwise OR of every contributing row's set mask."},
				ComponentKey{Name: "popcount", Type: "int", Description: "Number of bits set in mask_union."},
				ComponentKey{Name: "labels", Type: "[]string", Description: "Resolved dictionary labels for every bit set in mask_union."},
			),
		},
		{
			Name:          string(types.AGG_SET_INTERSECTION),
			Category:      "aggregator",
			Description:   "Bitwise-AND a set field across rows; returns the resolved labels for every bit set in every contributing row.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "rich []string (resolved labels); scalar fallback = popcount of the intersection mask",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mask_intersection", Type: "uint64", Description: "Bitwise AND of every contributing row's set mask."},
				ComponentKey{Name: "popcount", Type: "int", Description: "Number of bits set in mask_intersection."},
				ComponentKey{Name: "labels", Type: "[]string", Description: "Resolved dictionary labels for every bit set in mask_intersection."},
			),
		},
		{
			Name:          string(types.AGG_SET_FREQUENCY),
			Category:      "aggregator",
			Description:   "Per-bit row count: how many rows had each set label selected.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "rich map[string]int (label→row count); scalar fallback = max single-label frequency",
			Streamable:    true,
			// Partial: per_label_count merges across chunks but the
			// map allocation makes it more expensive than a pure
			// fold; orchestrator may stage merge at terminal flush.
			ComponentSchema: aggSchema(Partial,
				ComponentKey{Name: "total_label_observations", Type: "int", Description: "Sum of popcounts across contributing rows (total label selections seen)."},
				ComponentKey{Name: "distinct_labels", Type: "int", Description: "Number of distinct labels observed at least once."},
				ComponentKey{Name: "per_label_count", Type: "map[string]int", Description: "Per-label row count: how many rows had each label selected."},
			),
		},
		{
			Name:          string(types.AGG_SET_CARDINALITY_SUM),
			Category:      "aggregator",
			Description:   "Sum of popcounts across contributing rows — total selections seen.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "sum_cardinality", Type: "int", Description: "Sum of popcounts across contributing rows — total label selections seen."},
			),
		},
		{
			Name:          string(types.AGG_SET_CARDINALITY_AVG),
			Category:      "aggregator",
			Description:   "Average popcount per contributing row — typical number of selections.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "sum_cardinality", Type: "int", Description: "Sum of popcounts across contributing rows."},
				ComponentKey{Name: "avg_cardinality", Type: "float64", Description: "Average popcount per contributing row (sum_cardinality / n)."},
			),
		},
		{
			Name:          string(types.AGG_SET_DISTINCT_VALUES),
			Category:      "aggregator",
			Description:   "Count of distinct exact mask values seen; each combination is atomic.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
			ComponentSchema: aggSchema(Mergeable,
				ComponentKey{Name: "mask_union", Type: "uint64", Description: "Bitwise OR of every contributing row's set mask."},
				ComponentKey{Name: "popcount", Type: "int", Description: "Number of bits set in mask_union (count of distinct labels observed)."},
				ComponentKey{Name: "labels", Type: "[]string", Description: "Resolved dictionary labels for every bit set in mask_union."},
			),
		},
	}
}
