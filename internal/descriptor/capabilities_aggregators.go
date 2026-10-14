package descriptor

import (
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Field-type declaration policy
//
// Every name below MUST round-trip through encoding.ParseFieldType.
// Nullability is orthogonal to type (CLAUDE.md, byte-layout invariants):
// `encoding.Field.Nullable` is a per-field FLAG that enrols the field in
// the per-record null bitmap, and it never produces a distinct type. The
// `nullable_u4` / `nullable_u8` / `nullable_u16` / `nullable_bool` /
// `nullable_decimal128` spellings these lists once carried were stale
// aliases for `u4` / `u8` / `u16` / `packed_bool` / `decimal128`, not
// narrower claims — nothing parsed them, so they reached `accepts_types`
// in the manifest naming column shapes the codec rejects on sight while
// `u4`, which every one of these operators reads, was named nowhere at
// all. Gated by TestCapabilities_AcceptsTypesAreRegisteredNames and
// TestCapabilities_NoNullableTypeNames.
//
// Sorted (byte order, so `u4` falls between `u32` and `u64`) for golden
// stability; TestCapabilities_AllCohortFieldTypesMatchesRegistry pins it.

// numericFieldTypes is the canonical list of cohort field types that
// participate in numeric aggregations, decimal128 included. Consumers
// read the field through Record.NumericValue, which decimal128 answers
// via the float64 echo the decoder writes alongside the exact value.
var numericFieldTypes = []string{
	"date",
	"decimal128",
	"f32",
	"f64",
	"u16",
	"u32",
	"u4",
	"u64",
	"u8",
}

// numericFieldTypesNoDecimal lists numeric types excluding the
// fixed-point decimal types. Used by aggregators that operate in float64
// space (variance, stddev, skewness, kurtosis) and by the window /
// attribute / feature families. Mirrors predict_window.isNumericType,
// which gates the same set at predict time and has always admitted u4.
var numericFieldTypesNoDecimal = []string{
	"date",
	"f32",
	"f64",
	"u16",
	"u32",
	"u4",
	"u64",
	"u8",
}

// numericFieldTypesAnalytics widens numericFieldTypes with the
// bit-packed boolean encoding (packed_bool) and the second temporal
// ordinal (datetime). The bit-packed types store small non-negative
// integers (0/1 for packed_bool, 0..15 for u4) which the analytics
// aggregators consume as proportions or ordinal means without an
// ATTR_FORMULA cast.
//
// EQUAL to encoding.FieldType.IsNumericForAnalytics, in both directions,
// and TestCapabilities_AnalyticsListsMatchNumericPredicate holds it
// there. The predicate is not decoration: every aggregator carrying this
// list reads its column through Record.NumericValue, which answers for
// exactly the types the decoder writes into the float64 values map.
//
// `datetime` (type byte 17, epoch SECONDS) used to be missing while
// `date` (epoch days) was present — an asymmetry the runtime never made.
// Both decode through the same encoding.decodeFixed branch and both
// reach collectValues identically, so the manifest was simply telling a
// caller that AGG_SUM could not sum a column it sums fine. Note that the
// day-truncation adapter (internal/processing/date_field.go) sits on the date
// FAMILY grouper/filter boundary, not on the aggregation path: a
// datetime aggregate is in seconds, a date aggregate in days.
var numericFieldTypesAnalytics = []string{
	"date",
	"datetime",
	"decimal128",
	"f32",
	"f64",
	"packed_bool",
	"u16",
	"u32",
	"u4",
	"u64",
	"u8",
}

// numericFieldTypesAnalyticsNoDecimal mirrors numericFieldTypesAnalytics
// without decimal128 — for aggregators that operate purely in float64
// (skewness, kurtosis, zscore, the weighted and interval family) and for
// the three order-statistic aggregators (AGG_RANGE, AGG_MEDIAN,
// AGG_PERCENTILE) that predict refuses on a decimal field with
// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL.
//
// AGG_VARIANCE and AGG_STDDEV are NOT here. They read the decimal-
// carrying list, because internal/processing/aggregator_decimal.go computes both
// in decimal128 two-pass form and predict has always permitted the
// pairing (decimalSupportedAggregations). Carrying them here was an
// under-declaration, not a restriction anything enforced.
//
// Carries datetime for the same reason the parent list does; the two
// lists differ in decimal128 and nothing else.
var numericFieldTypesAnalyticsNoDecimal = []string{
	"date",
	"datetime",
	"f32",
	"f64",
	"packed_bool",
	"u16",
	"u32",
	"u4",
	"u64",
	"u8",
}

// numericFieldTypesStrictScalar is the strict scalar numeric family —
// u8/u16/u32/u64/f32/f64 only. Excludes decimal128, date, and every
// bit-packed encoding (u4, packed_bool). Used by aggregators that drive
// a single float64 hot path with no per-type branch (AGG_WELFORD — see
// internal/processing/aggregator_welford.go's factory gate, which admits exactly
// these six and mirrors the TEST_WELCH field-type policy).
var numericFieldTypesStrictScalar = []string{
	"f32",
	"f64",
	"u16",
	"u32",
	"u64",
	"u8",
}

// allCohortFieldTypes lists every field type without restriction (used
// by AGG_COUNT / AGG_NULL_COUNT / FILTER_NULL and the other operators
// that ask only whether a field ANSWERED, which every type can do).
// "every field type" is literal: a registered field type missing from
// this list under-declares the operators that carry it, so the manifest
// would tell a caller that AGG_COUNT cannot count a column it counts
// fine. Equality with encoding's FieldType registry is asserted by
// TestCapabilities_AllCohortFieldTypesMatchesRegistry, so this list
// cannot drift in either direction again.
//
// Operators that need the field's NUMBER rather than its presence take
// nonSetFieldTypes instead — see its doc comment.
var allCohortFieldTypes = []string{
	"categorical_u16",
	"categorical_u32",
	"categorical_u8",
	"date",
	"datetime",
	"decimal128",
	"f32",
	"f64",
	"packed_bool",
	"set_u128",
	"set_u16",
	"set_u256",
	"set_u32",
	"set_u64",
	"set_u8",
	"u16",
	"u32",
	"u4",
	"u64",
	"u8",
	"vec_f32",
	"vec_f64",
}

// setFieldTypes lists the bitmask multi-select field types. Used by every
// AGG_SET_* / FILTER_SET_* / GROUP_SET_* / ATTR_SET_* operator — they
// reject non-set fields at construction time.
//
// Every registered rung belongs here, narrow and wide alike: the set
// operators read encoding.SetMask, which is width-agnostic, so a list
// that stopped at set_u64 would tell a caller AGG_SET_UNION cannot fold
// a column it folds fine.
var setFieldTypes = []string{
	"set_u128",
	"set_u16",
	"set_u256",
	"set_u32",
	"set_u64",
	"set_u8",
}

// nonSetFieldTypes is allCohortFieldTypes minus every set rung (and
// minus the vector types, which have no scalar value either). It
// belongs to the operators that accept "any field" in the sense of "any
// field that has a NUMBER" — they read Record.NumericValue, which
// refuses set columns outright.
//
// A set column has no meaningful numeric value: the decoder's float64
// echo is the LOW 64 BITS of the membership bitmask for set_u128 /
// set_u256 and is already lossy above 2^53 for set_u64. Declaring a set
// type on such an operator promises something the runtime cannot keep,
// and BOTH of its failure modes are silent — before the NumericValue
// refusal these operators matched the echo (a plausible wrong answer),
// after it they drop every row (a plausible empty answer). The
// declaration is therefore part of the fix, alongside the construction
// -time refusals in internal/processing/ (rejectSetFieldForNumericFilter,
// rejectSetFieldForNumericGrouper, rejectSetFieldForNumericAggregator,
// rejectSetFieldForNumericAttribute).
//
// Callers wanting set semantics use the AGG_SET_* / FILTER_SET_* /
// GROUP_SET_* / ATTR_SET_* families instead.
var nonSetFieldTypes = nonSetSubset(allCohortFieldTypes)

// nonVectorFieldTypes is allCohortFieldTypes minus the vector types:
// the operators that read a field's single value whatever its type
// (FILTER_TRUE / FILTER_FALSE truthiness, a window's ORDER BY key, a
// stratify key). A vector has dim values per row, so every such slot
// refuses it with PULSE_VECTOR_FIELD_NOT_SCALAR (the field-reference
// walk's vectorScalarRefusals).
var nonVectorFieldTypes = func() []string {
	out := make([]string, 0, len(allCohortFieldTypes))
	for _, n := range allCohortFieldTypes {
		if !strings.HasPrefix(n, "vec_") {
			out = append(out, n)
		}
	}
	return out
}()

// nonSetNonDecimalFieldTypes is nonSetFieldTypes without decimal128,
// for a value-matching aggregator with no decimal implementation
// (AGG_FREQUENCY).
var nonSetNonDecimalFieldTypes = func() []string {
	out := make([]string, 0, len(nonSetFieldTypes))
	for _, n := range nonSetFieldTypes {
		if n != "decimal128" {
			out = append(out, n)
		}
	}
	return out
}()

// nonSetSubset returns names with every "set_" and "vec_" prefixed
// entry removed, preserving order — neither a set bitmask nor a vector
// has the single NUMBER these operators read. Derived from allCohortFieldTypes rather than
// written out so a newly registered rung cannot land in one list and
// miss the other.
func nonSetSubset(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.HasPrefix(n, "set_") || strings.HasPrefix(n, "vec_") {
			continue
		}
		out = append(out, n)
	}
	return out
}

// universalAggFloorKeys is the {n, n_null} pair every aggregator's
// component schema declares. The orchestrator's universal-floor pass
// emits these keys unconditionally so embedders can rely on them being
// present even when MetaAggregator returns an empty extra map. Declared
// centrally so the floor stays byte-equal across every entry.
func universalAggFloorKeys() []descriptor.ComponentKey {
	return []descriptor.ComponentKey{
		{Name: "n", Type: "int", Description: "Number of records aggregated (non-null inputs)."},
		{Name: "n_null", Type: "int", Description: "Number of null inputs encountered."},
	}
}

// aggSchema composes a ComponentSchema by prepending the universal
// floor to the operator-specific keys (in emission order) and tagging
// the result with the operator's mergeability classification. Keep the
// extra slice nil for floor-only operators (AGG_COUNT, AGG_NULL_COUNT)
// so the call site stays clean.
func aggSchema(merge descriptor.ComponentsMergeability, extra ...descriptor.ComponentKey) descriptor.ComponentSchema {
	floor := universalAggFloorKeys()
	keys := make([]descriptor.ComponentKey, 0, len(floor)+len(extra))
	keys = append(keys, floor...)
	keys = append(keys, extra...)
	return descriptor.ComponentSchema{Keys: keys, Mergeability: merge}
}

// aggregatorCapabilities is the static metadata table for every
// registered aggregator. Order is irrelevant; manifest assembly sorts by
// Name. The TestManifestOperatorsComplete gate enforces that every entry
// in types.AllAggregationTypes() has a row here.
func aggregatorCapabilities() []descriptor.Operator {
	return withWeightAware(aggregatorCapabilityTable())
}

// aggregatorCapabilityTable is the literal declaration table;
// aggregatorCapabilities stamps the weight classification onto it.
func aggregatorCapabilityTable() []descriptor.Operator {
	return []descriptor.Operator{
		{
			Name:            string(types.AGG_COUNT),
			Category:        "aggregator",
			Description:     "Count records that pass the active filter, optionally by group.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "scalar int64",
			Streamable:      true,
			ComponentSchema: aggSchema(descriptor.Mergeable),
		},
		{
			Name:          string(types.AGG_SUM),
			Category:      "aggregator",
			Description:   "Sum the numeric values of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64 (decimal128 preserved when input is decimal)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "sum", Type: "float64", Description: "Running sum of non-null field values."},
			),
		},
		{
			Name:          string(types.AGG_AVERAGE),
			Category:      "aggregator",
			Description:   "Arithmetic mean of the field across the input set.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "sum", Type: "float64", Description: "Running sum of non-null field values; combined with n to recover the mean (the weighted sum Σw·x on a weighted slot)."},
				descriptor.ComponentKey{Name: "sum_weighted", Type: "float64", Optional: true, Description: "Weighted slots only: Σw·x over the contributing rows."},
				descriptor.ComponentKey{Name: "weighted_mean", Type: "float64", Optional: true, Description: "Weighted slots only: Σw·x / Σw (the scalar)."},
				descriptor.ComponentKey{Name: "m2_weighted", Type: "float64", Optional: true, Description: "Weighted slots only: Σw(x − mean)², the weighted second central moment."},
				descriptor.ComponentKey{Name: "sum_weights_sq", Type: "float64", Optional: true, Description: "Weighted slots only: Σw², the Kish n_eff denominator."},
				descriptor.ComponentKey{Name: "weighted_variance", Type: "float64", Optional: true, Description: "Weighted slots only: m2_weighted / (Σw − 1), the frequency-weights sample variance; null (NaN) when Σw ≤ 1."},
			),
		},
		{
			Name:          string(types.AGG_MIN),
			Category:      "aggregator",
			Description:   "Smallest non-null value of the field.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "min", Type: "float64", Description: "Smallest non-null value observed."},
			),
		},
		{
			Name:          string(types.AGG_MAX),
			Category:      "aggregator",
			Description:   "Largest non-null value of the field.",
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "max", Type: "float64", Description: "Largest non-null value observed."},
			),
		},
		{
			Name:        string(types.AGG_STDDEV),
			Category:    "aggregator",
			Description: "Population standard deviation via Welford's online algorithm. On a decimal128 field the engine runs a decimal two-pass instead, falling back to float64 only if an intermediate would overflow.",
			// decimal128 included: predict permits the pairing
			// (decimalSupportedAggregations) and
			// internal/processing/aggregator_decimal.go implements it.
			// Gated by TestCapabilities_DecimalDeclaredOnlyWhereSupported.
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64 (decimal128 input yields a decimal-scaled result)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mean", Type: "float64", Description: "Running Welford mean of non-null field values."},
				descriptor.ComponentKey{Name: "m2", Type: "float64", Description: "Welford second-moment accumulator (sum of squared deviations from the running mean)."},
				descriptor.ComponentKey{Name: "variance", Type: "float64", Description: "Population variance derived from m2 / n."},
				descriptor.ComponentKey{Name: "stddev", Type: "float64", Description: "Population standard deviation (square root of variance)."},
			),
		},
		{
			Name:        string(types.AGG_RANGE),
			Category:    "aggregator",
			Description: "Spread (max minus min) of the field across the input set.",
			// No decimal128: predict refuses this pairing with
			// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL
			// (decimalSupportedAggregations), so offering it in the
			// manifest would be a promise predict itself declines.
			// Gated by TestCapabilities_DecimalDeclaredOnlyWhereSupported.
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "min", Type: "float64", Description: "Smallest non-null value observed."},
				descriptor.ComponentKey{Name: "max", Type: "float64", Description: "Largest non-null value observed."},
			),
		},
		{
			Name:          string(types.AGG_MODE_COUNT),
			Category:      "aggregator",
			Description:   "Modal count: how many rows hold the field's most common value. Under GROUP_CATEGORY on the same field it is each group's row count.",
			AcceptsTypes:  nonSetFieldTypes,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Partial,
				descriptor.ComponentKey{Name: "distinct_count", Type: "int", Description: "Number of distinct values observed."},
				descriptor.ComponentKey{Name: "mode_value", Type: "any", Description: "Most-frequent value — the largest sum of weights on a weighted slot (ties broken by the smallest value, matching AGG_MODE)."},
				descriptor.ComponentKey{Name: "mode_count", Type: "int", Description: "Row count of the modal value; on a weighted slot its sum of weights (a float)."},
			),
		},
		{
			Name:        string(types.AGG_FREQUENCY),
			Category:    "aggregator",
			Description: "Number of non-null rows whose field equals params.value (0 when none does); on a weighted slot the sum of their weights. The count of the most common value is AGG_MODE_COUNT.",
			Params: []descriptor.Param{
				{
					Name:        "value",
					Type:        "string",
					Required:    true,
					Description: "Value to count, matched as FILTER_INCLUDE matches one: a category label, otherwise a number (date in epoch days, datetime in epoch seconds, packed_bool 1 or 0). A JSON number is accepted.",
				},
			},
			// No set_* (a mask is not a value) and no decimal128: the
			// runtime has no decimal implementation and predict warns
			// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL on the pairing.
			AcceptsTypes:  nonSetNonDecimalFieldTypes,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "match_count", Type: "int", Description: "Non-null rows equal to params.value (= the scalar); on a weighted slot their sum of weights (a float)."},
				descriptor.ComponentKey{Name: "share", Type: "float64", Description: "match_count / n; omitted when n is 0. On a weighted slot the matching sum of weights over the sum of weights of the non-null rows (omitted when that is 0)."},
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
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "pop_mean", Type: "float64", Description: "Population mean used as the z-score center."},
				descriptor.ComponentKey{Name: "pop_stddev", Type: "float64", Description: "Population standard deviation used as the z-score scale."},
				descriptor.ComponentKey{Name: "target_value", Type: "float64", Description: "Value being standardized against the population summary."},
				descriptor.ComponentKey{Name: "zscore", Type: "float64", Description: "Standardized score: (target_value - pop_mean) / pop_stddev; null (NaN) when pop_stddev is 0."},
			),
		},
		{
			Name:        string(types.AGG_MEDIAN),
			Category:    "aggregator",
			Description: "50th percentile of the field; requires sorting the full value set.",
			// No decimal128 — see AGG_RANGE.
			AcceptsTypes:   numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote:  "scalar float64",
			Streamable:     false,
			StreamableHint: "Use AGG_AVERAGE for a streaming central-tendency proxy, or accept the buffered path.",
			ComponentSchema: aggSchema(descriptor.None,
				descriptor.ComponentKey{Name: "position_low", Type: "int", Description: "Lower index used to bracket the median in the sorted value set (weighted: the expanded-index floor of 0.5·(Σw − 1), probability weights rescaled to Σw = n)."},
				descriptor.ComponentKey{Name: "position_high", Type: "int", Description: "Upper index used to bracket the median in the sorted value set (weighted: the expanded-index ceiling of 0.5·(Σw − 1), probability weights rescaled to Σw = n)."},
				descriptor.ComponentKey{Name: "median", Type: "float64", Description: "Resolved median value (linear interpolation between the bracketing positions)."},
			),
		},
		{
			Name:        string(types.AGG_VARIANCE),
			Category:    "aggregator",
			Description: "Population variance via Welford's online algorithm. On a decimal128 field the engine runs a decimal two-pass instead, falling back to float64 only if an intermediate would overflow.",
			// decimal128 included — see AGG_STDDEV.
			AcceptsTypes:  numericFieldTypesAnalytics,
			EmitsTypeNote: "scalar float64 (decimal128 input yields a decimal-scaled result)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mean", Type: "float64", Description: "Running Welford mean of non-null field values."},
				descriptor.ComponentKey{Name: "m2", Type: "float64", Description: "Welford second-moment accumulator (sum of squared deviations from the running mean)."},
				descriptor.ComponentKey{Name: "variance", Type: "float64", Description: "Population variance derived from m2 / n."},
			),
		},
		{
			Name:          string(types.AGG_MODE),
			Category:      "aggregator",
			Description:   "Most-frequent value of the field (ties broken by the smallest value).",
			AcceptsTypes:  nonSetFieldTypes,
			EmitsTypeNote: "scalar float64 (the modal value; for a categorical field, its dictionary index, for a packed_bool field its 0/1 truth value — a code, not a magnitude, so an overlay scoring it over such a field (params.field, else aggregations[0]; as a crosstab cell, any MATRIX kind reading cell values; on a later chain stage, over a column a code-emitting aggregation wrote) is refused PROCESSING_CONFIG reason categorical_code)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Partial,
				descriptor.ComponentKey{Name: "value", Type: "any", Description: "Most-frequent value observed (smallest-value tie-break)."},
				descriptor.ComponentKey{Name: "count", Type: "int", Description: "Row count of the modal value; on a weighted slot its sum of weights (a float)."},
				descriptor.ComponentKey{Name: "distinct_count", Type: "int", Description: "Number of distinct values observed."},
				descriptor.ComponentKey{Name: "tie_count", Type: "int", Description: "Number of values tied with the mode at the same maximum count."},
			),
		},
		{
			Name:          string(types.AGG_SKEWNESS),
			Category:      "aggregator",
			Description:   "Population skewness g1 (m3 / m2^1.5, dividing by n) via online moments.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mean", Type: "float64", Description: "Running mean of non-null field values."},
				descriptor.ComponentKey{Name: "m2", Type: "float64", Description: "Second-moment accumulator (sum of squared deviations from the running mean)."},
				descriptor.ComponentKey{Name: "m3", Type: "float64", Description: "Third-moment accumulator (sum of cubed deviations from the running mean)."},
				descriptor.ComponentKey{Name: "skewness", Type: "float64", Description: "Population skewness g1 = m3 / (n * sd^3) from m2, m3 and n (not the small-sample-adjusted G1); null (NaN) when n <= 1 or the variance is zero. Weighted: the moments are Σw-weighted and Σw replaces n."},
			),
		},
		{
			Name:          string(types.AGG_KURTOSIS),
			Category:      "aggregator",
			Description:   "Population excess kurtosis g2 (m4 / m2^2 - 3, dividing by n) via online moments.",
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mean", Type: "float64", Description: "Running mean of non-null field values."},
				descriptor.ComponentKey{Name: "m2", Type: "float64", Description: "Second-moment accumulator (sum of squared deviations from the running mean)."},
				descriptor.ComponentKey{Name: "m3", Type: "float64", Description: "Third-moment accumulator (sum of cubed deviations from the running mean)."},
				descriptor.ComponentKey{Name: "m4", Type: "float64", Description: "Fourth-moment accumulator (sum of fourth-power deviations from the running mean)."},
				descriptor.ComponentKey{Name: "kurtosis", Type: "float64", Description: "Population excess kurtosis g2 = m4 / (n * variance^2) - 3 from m2, m4 and n (not the small-sample-adjusted G2); null (NaN) when n <= 1 or the variance is zero. Weighted: the moments are Σw-weighted and Σw replaces n."},
			),
		},
		{
			Name:          string(types.AGG_DISTINCT_COUNT),
			Category:      "aggregator",
			Description:   "Count of distinct non-null values across the input set.",
			AcceptsTypes:  nonSetFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Partial,
				descriptor.ComponentKey{Name: "cardinality", Type: "int", Description: "Number of distinct non-null values observed."},
			),
		},
		{
			Name:        string(types.AGG_DISTINCT_SUM),
			Category:    "aggregator",
			Description: "Sum the field once per distinct key named by distinct_by. A key seen on N records contributes its value once, not N times; the first value observed for a key wins.",
			Params: []descriptor.Param{
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
			ComponentSchema: aggSchema(descriptor.Partial,
				descriptor.ComponentKey{Name: "sum", Type: "float64", Description: "Sum of the first value observed for each distinct key."},
				descriptor.ComponentKey{Name: "distinct_count", Type: "int", Description: "Number of distinct keys that contributed to the sum."},
			),
		},
		{
			Name:        string(types.AGG_PERCENTILE),
			Category:    "aggregator",
			Description: "Configurable percentile of the field; requires sorting the full value set.",
			Params: []descriptor.Param{
				{
					Name:        "percentile",
					Type:        "float",
					Required:    true,
					Description: "Percentile to compute, in [0, 100]. e.g. 95 for p95.",
				},
			},
			// No decimal128 — see AGG_RANGE.
			AcceptsTypes:   numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote:  "scalar float64",
			Streamable:     false,
			StreamableHint: "Use AGG_AVERAGE or accept the buffered path; exact percentiles need sorted input.",
			ComponentSchema: aggSchema(descriptor.None,
				descriptor.ComponentKey{Name: "p", Type: "float64", Description: "Percentile requested, in [0, 100]."},
				descriptor.ComponentKey{Name: "position", Type: "int", Description: "Index into the sorted value set used to resolve the percentile (weighted: the expanded-index floor of p·(Σw − 1), probability weights rescaled to Σw = n)."},
				descriptor.ComponentKey{Name: "lower", Type: "float64", Description: "Lower bracketing value used during interpolation."},
				descriptor.ComponentKey{Name: "upper", Type: "float64", Description: "Upper bracketing value used during interpolation."},
				descriptor.ComponentKey{Name: "method", Type: "string", Description: "Interpolation method (e.g. \"linear\")."},
				descriptor.ComponentKey{Name: "value", Type: "float64", Description: "Resolved percentile value."},
			),
		},
		{
			Name:            string(types.AGG_NULL_COUNT),
			Category:        "aggregator",
			Description:     "Count records where the field is null. Inverse of AGG_COUNT, which counts non-null records.",
			AcceptsTypes:    allCohortFieldTypes,
			EmitsTypeNote:   "scalar int64",
			Streamable:      true,
			ComponentSchema: aggSchema(descriptor.Mergeable),
		},
		{
			Name:        string(types.AGG_WEIGHTED_MEAN),
			Category:    "aggregator",
			Description: "Weighted arithmetic mean: sum(field * weight) / sum(weight) — the weighted average under its own name and components.",
			Params: []descriptor.Param{
				{
					Name:        "weight_field",
					Type:        "string",
					Required:    false,
					Description: "Schema field whose value is the per-row weight (kind probability); sugar for a slot weight. Absent: the slot or request weight, else the instance default weight; none resolving is refused. Rows with an invalid (null, negative, NaN/Inf) or zero weight are excluded from the mean and every weighted moment; the universal floor n / n_null still counts them (it keys on the value field only).",
				},
			},
			AcceptsTypes:  numericFieldTypesAnalyticsNoDecimal,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "sum_weighted", Type: "float64", Description: "Running sum of (field * weight) across contributing rows."},
				descriptor.ComponentKey{Name: "sum_weights", Type: "float64", Description: "Running sum of weights across contributing rows."},
				descriptor.ComponentKey{Name: "weighted_mean", Type: "float64", Description: "Resolved weighted mean: sum_weighted / sum_weights."},
				descriptor.ComponentKey{Name: "m2_weighted", Type: "float64", Description: "Weighted second central moment: sum(weight * (field - weighted_mean)^2)."},
				descriptor.ComponentKey{Name: "sum_weights_sq", Type: "float64", Description: "Running sum of squared weights; feeds the Kish effective sample size."},
				descriptor.ComponentKey{Name: "weighted_variance", Type: "float64", Description: "Frequency-weights variance m2_weighted / (sum_weights - 1); null (NaN) when sum_weights <= 1."},
				descriptor.ComponentKey{Name: "n_eff", Type: "float64", Description: "Kish effective sample size sum_weights^2 / sum_weights_sq; 0 for an empty cell."},
			),
		},
		{
			Name:        string(types.AGG_RATIO),
			Category:    "aggregator",
			Description: "Emits sum(numerator_field) / sum(denominator_field) — on a weighted slot sum(w*num) / sum(w*den). The Aggregation's own Field is ignored — the two summed fields come from Params. Denominator-zero yields NaN (null in JSON).",
			Params: []descriptor.Param{
				{
					Name:        "numerator_field",
					Type:        "field",
					Required:    true,
					FieldFilter: "any",
					Description: "Schema field summed as the numerator. Read through the row's numeric channel, so a non-numeric field contributes its encoded value (a categorical contributes its dictionary code) rather than erroring.",
				},
				{
					Name:        "denominator_field",
					Type:        "field",
					Required:    true,
					FieldFilter: "any",
					Description: "Schema field summed as the denominator. Same numeric-channel read as the numerator.",
				},
			},
			// IgnoresField, and AcceptsTypes therefore says only that no
			// type is refused in a slot the operator discards. The wire
			// form still requires Aggregation.Field; every type is equally
			// fine there because none of them is read.
			IgnoresField:  true,
			AcceptsTypes:  allCohortFieldTypes,
			EmitsTypeNote: "scalar float64 (NaN, null in JSON, when denominator sum == 0)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "numerator", Type: "float64", Description: "Running sum of the numerator field (weighted slot: of weight * numerator)."},
				descriptor.ComponentKey{Name: "denominator", Type: "float64", Description: "Running sum of the denominator field (weighted slot: of weight * denominator)."},
				descriptor.ComponentKey{Name: "ratio", Type: "float64", Description: "Resolved ratio: numerator / denominator (NaN, null in JSON, when denominator is zero)."},
			),
		},
		modelErrorCapability(types.AGG_MAPE,
			"Mean absolute percentage error of a prediction against an actual: mean(|field - prediction| / |field|) as a FRACTION (0.12 = 12%), over rows where both sides answer and the actual is non-zero (a zero actual is excluded and counted in n_zero_actual). Weighted: sum(w*loss) / sum(w). No contributing row yields NaN (null in JSON).",
			descriptor.ComponentKey{Name: "n_zero_actual", Type: "int", Description: "Rows whose actual and prediction both answered but whose actual is exactly 0: their percentage error is undefined, so they are excluded from the figure."},
			descriptor.ComponentKey{Name: "mape", Type: "float64", Description: "Resolved MAPE fraction: loss_sum / weight_sum (NaN, null in JSON, when no row contributed)."},
		),
		modelErrorCapability(types.AGG_RMSE,
			"Root mean squared error of a prediction against an actual: sqrt(mean((field - prediction)^2)) over rows where both sides answer; in the actual's units. Weighted: sqrt(sum(w*(y - yhat)^2) / sum(w)) — the square root of the pooled MSE. No contributing row yields NaN (null in JSON).",
			descriptor.ComponentKey{Name: "mse", Type: "float64", Description: "Mean squared error loss_sum / weight_sum; rmse is its square root (NaN, null in JSON, when no row contributed)."},
			descriptor.ComponentKey{Name: "rmse", Type: "float64", Description: "Resolved RMSE: sqrt(mse)."},
		),
		modelErrorCapability(types.AGG_MAE,
			"Mean absolute error of a prediction against an actual: mean(|field - prediction|) over rows where both sides answer; in the actual's units. Weighted: sum(w*|y - yhat|) / sum(w). No contributing row yields NaN (null in JSON).",
			descriptor.ComponentKey{Name: "mae", Type: "float64", Description: "Resolved MAE: loss_sum / weight_sum (NaN, null in JSON, when no row contributed)."},
		),
		{
			Name:        string(types.AGG_CI_LOWER),
			Category:    "aggregator",
			Description: "Lower bound of the confidence interval for the mean. Method \"normal\" streams via Welford and the Beasley-Springer-Moro inverse-normal quantile.",
			Params: []descriptor.Param{
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
			EmitsTypeNote: "scalar float64 (NaN, null in JSON, when n < 2)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mean", Type: "float64", Description: "Welford-running mean of the field."},
				descriptor.ComponentKey{Name: "stderr", Type: "float64", Description: "Standard error of the mean derived from variance and n."},
				descriptor.ComponentKey{Name: "alpha", Type: "float64", Description: "Significance level (1 - confidence)."},
				descriptor.ComponentKey{Name: "t_critical", Type: "float64", Description: "Critical value used to scale the standard error."},
				descriptor.ComponentKey{Name: "lower", Type: "float64", Description: "Resolved lower bound of the confidence interval."},
			),
		},
		{
			Name:        string(types.AGG_CI_UPPER),
			Category:    "aggregator",
			Description: "Upper bound of the confidence interval for the mean. See AGG_CI_LOWER for params and methods.",
			Params: []descriptor.Param{
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
			EmitsTypeNote: "scalar float64 (NaN, null in JSON, when n < 2)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mean", Type: "float64", Description: "Welford-running mean of the field."},
				descriptor.ComponentKey{Name: "stderr", Type: "float64", Description: "Standard error of the mean derived from variance and n."},
				descriptor.ComponentKey{Name: "alpha", Type: "float64", Description: "Significance level (1 - confidence)."},
				descriptor.ComponentKey{Name: "t_critical", Type: "float64", Description: "Critical value used to scale the standard error."},
				descriptor.ComponentKey{Name: "upper", Type: "float64", Description: "Resolved upper bound of the confidence interval."},
			),
		},
		{
			Name:          string(types.AGG_WELFORD),
			Category:      "aggregator",
			Description:   "Streaming Welford-Pébaÿ moment triple — running mean, unbiased sample variance (n-1), and observed count — over a numeric field. Returns the running mean as the scalar fallback; rich payload is a typed WelfordTriple for overlay handlers (OVERLAY_T_CELL / OVERLAY_Z_CELL) to consume without re-deriving variance from raw rows. Margin recompute (variance does not pool by addition).",
			AcceptsTypes:  numericFieldTypesStrictScalar,
			EmitsTypeNote: "scalar float64 (running mean; NaN, null in JSON, when no rows); rich WelfordTriple{Mean, Variance, N}; the row value is non-scalar, so an overlay scoring it (params.field, else aggregations[0]) is refused PROCESSING_CONFIG reason non_scalar — except the two-sample t / z vs-ref overlays, which read the triple from the slot's per-group components (components disabled on that slot: PULSE_OVERLAY_COMPONENTS_REQUIRED reason components_disabled)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mean", Type: "float64", Description: "Running Welford mean of non-null field values."},
				descriptor.ComponentKey{Name: "m2", Type: "float64", Description: "Welford second-moment accumulator (sum of squared deviations from the running mean)."},
				descriptor.ComponentKey{Name: "variance", Type: "float64", Description: "Unbiased sample variance derived from m2 / (n-1); null (NaN) when n < 2 (weighted: sum_weights <= 1)."},
				descriptor.ComponentKey{Name: "stddev", Type: "float64", Description: "Sample standard deviation (square root of variance)."},
			),
		},
		{
			Name:          string(types.AGG_SET_UNION),
			Category:      "aggregator",
			Description:   "Bitwise-OR a set field across rows; returns the resolved labels for every bit set in any contributing row.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "rich []string (resolved labels); scalar fallback = popcount of the union mask; the row value is non-scalar, so an overlay scoring it (params.field, else aggregations[0]) is refused PROCESSING_CONFIG reason non_scalar (so is a chain stage carrying it that feeds another)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mask_union", Type: "[]uint64", Description: "Bitwise OR of every contributing row's set mask, as four little-endian 64-bit words (words[0] = bits 0-63). Fixed length at every set rung, so a 256-bit mask is carried whole and a consumer indexes words[bit/64] without knowing the column width."},
				descriptor.ComponentKey{Name: "popcount", Type: "int", Description: "Number of bits set in mask_union."},
				descriptor.ComponentKey{Name: "labels", Type: "[]string", Description: "Resolved dictionary labels for every bit set in mask_union."},
			),
		},
		{
			Name:          string(types.AGG_SET_INTERSECTION),
			Category:      "aggregator",
			Description:   "Bitwise-AND a set field across rows; returns the resolved labels for every bit set in every contributing row.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "rich []string (resolved labels); scalar fallback = popcount of the intersection mask; the row value is non-scalar, so an overlay scoring it (params.field, else aggregations[0]) is refused PROCESSING_CONFIG reason non_scalar (so is a chain stage carrying it that feeds another)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mask_intersection", Type: "[]uint64", Description: "Bitwise AND of every contributing row's set mask, as four little-endian 64-bit words (words[0] = bits 0-63). Fixed length at every set rung, so a 256-bit mask is carried whole and a consumer indexes words[bit/64] without knowing the column width."},
				descriptor.ComponentKey{Name: "popcount", Type: "int", Description: "Number of bits set in mask_intersection."},
				descriptor.ComponentKey{Name: "labels", Type: "[]string", Description: "Resolved dictionary labels for every bit set in mask_intersection."},
			),
		},
		{
			Name:          string(types.AGG_SET_FREQUENCY),
			Category:      "aggregator",
			Description:   "Per-bit row count: how many rows had each set label selected; on a weighted slot the sum of their weights.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "rich map[string]int (label→row count; map[string]float64 of weight sums when weighted); scalar fallback = max single-label frequency; the row value is non-scalar, so an overlay scoring it (params.field, else aggregations[0]) is refused PROCESSING_CONFIG reason non_scalar (so is a chain stage carrying it that feeds another)",
			Streamable:    true,
			// Partial: per_label_count merges across chunks but the
			// map allocation makes it more expensive than a pure
			// fold; orchestrator may stage merge at terminal flush.
			ComponentSchema: aggSchema(descriptor.Partial,
				descriptor.ComponentKey{Name: "total_label_observations", Type: "int", Description: "Sum of popcounts across contributing rows (total label selections seen); weighted slot: the weight-summed selections (a float)."},
				descriptor.ComponentKey{Name: "distinct_labels", Type: "int", Description: "Number of distinct labels observed at least once (weighted slot: with a positive weight sum)."},
				descriptor.ComponentKey{Name: "per_label_count", Type: "map[string]int", Description: "Per-label row count: how many rows had each label selected; weighted slot: each label's sum of weights (float values)."},
			),
		},
		{
			Name:          string(types.AGG_SET_CARDINALITY_SUM),
			Category:      "aggregator",
			Description:   "Sum of popcounts across contributing rows — total selections seen; on a weighted slot sum(w * popcount).",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "scalar int64 (float64 when weighted)",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "sum_cardinality", Type: "int", Description: "Sum of popcounts across contributing rows — total label selections seen; weighted slot: sum(w * popcount) (a float)."},
			),
		},
		{
			Name:          string(types.AGG_SET_CARDINALITY_AVG),
			Category:      "aggregator",
			Description:   "Average popcount per contributing row — typical number of selections; on a weighted slot sum(w * popcount) / sum(w).",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "scalar float64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "sum_cardinality", Type: "int", Description: "Sum of popcounts across contributing rows; weighted slot: sum(w * popcount) (a float)."},
				descriptor.ComponentKey{Name: "avg_cardinality", Type: "float64", Description: "Average popcount per contributing row (sum_cardinality / n; weighted slot: / sum of weights)."},
			),
		},
		{
			Name:          string(types.AGG_SET_DISTINCT_VALUES),
			Category:      "aggregator",
			Description:   "Count of distinct exact mask values seen; each combination is atomic.",
			AcceptsTypes:  setFieldTypes,
			EmitsTypeNote: "scalar int64",
			Streamable:    true,
			ComponentSchema: aggSchema(descriptor.Mergeable,
				descriptor.ComponentKey{Name: "mask_union", Type: "[]uint64", Description: "Bitwise OR of every contributing row's set mask, as four little-endian 64-bit words (words[0] = bits 0-63). Fixed length at every set rung, so a 256-bit mask is carried whole and a consumer indexes words[bit/64] without knowing the column width."},
				descriptor.ComponentKey{Name: "popcount", Type: "int", Description: "Number of bits set in mask_union (count of distinct labels observed)."},
				descriptor.ComponentKey{Name: "labels", Type: "[]string", Description: "Resolved dictionary labels for every bit set in mask_union."},
			),
		},
		vecAggCapability(types.AGG_VEC_MEAN,
			"Per-member mean of a vector (vector or fields, exactly one; never field): one figure per member, in member order — the group's average profile. Missing data is per element: a null member skips that element only. Weighted: sum(w*x) / sum(w) per element. An element no row answered is NaN (null in JSON). Array-valued: refused as a crosstab cell or margin aggregation (unless params.element selects one element), a Sort key, a window or post-test input, the value an overlay scores (request, Compose or whole-chain: params.field where the kind honours it, else aggregations[0] — an unscored one beside it runs) and a chain stage that feeds another. params.expand writes one scalar column per member instead, \"<label>[<element label>]\" — an ordinary nullable f64 column a Sort key, a window, a post-test, a series overlay's params.field or a later chain stage (an f64 input field) reads (an unanswered element is NaN, a null there and a missing host value to an overlay).",
			"rich []float64, one mean per member in member order (an element no row answered is NaN, null in JSON); no scalar figure"),
		vecAggCapability(types.AGG_VEC_SUM,
			"Per-member total of a vector (vector or fields, exactly one; never field): one figure per member, in member order. Missing data is per element: a null member skips that element only. Weighted: sum(w*x) per element. An element no row answered is 0. Array-valued: refused as a crosstab cell or margin aggregation (unless params.element selects one element), a Sort key, a window or post-test input, the value an overlay scores (request, Compose or whole-chain: params.field where the kind honours it, else aggregations[0] — an unscored one beside it runs) and a chain stage that feeds another. params.expand writes one scalar column per member instead, \"<label>[<element label>]\" — an ordinary f64 column a Sort key, a window, a post-test, a series overlay's params.field or a later chain stage (an f64 input field) reads.",
			"rich []float64, one total per member in member order (0 where no row answered); no scalar figure"),
	}
}

// vecAggCapability declares a vector aggregator (AGG_VEC_MEAN /
// AGG_VEC_SUM). Its columns are the slot's vector / fields members;
// AcceptsTypes is the member types a vector carries without coerce
// (packed_bool members need the vector's coerce "binary").
func vecAggCapability(op types.AggregationType, desc, emits string) descriptor.Operator {
	return descriptor.Operator{
		Name:        string(op),
		Category:    "aggregator",
		Description: desc,
		Params: []descriptor.Param{
			{
				Name:        "labels",
				Type:        "list",
				Description: "Display label per member, inline fields only (a named vector carries its own Request.Vectors labels); one per resolved member. Names the elements in labels (components). Default: the member names.",
			},
			{
				Name:        "expand",
				Type:        "bool",
				Description: "true: replace the array with one scalar column per member, \"<label>[<element label>]\" in member order (grouped: per row); the label itself is absent. Predict lists the columns (aggregations[i].columns). Each is an ordinary nullable f64 column: a Sort key, a window's field / order_by / partition_by, a post-test field and a series overlay's params.field (request, Compose or whole-chain host) read it, and so does a later chain stage, as an f64 input field (NaN, an element no row answered, is a null there and a missing host value to an overlay), while the bare label stays refused (PROCESSING_CONFIG, reason array_valued); element labels must be distinct (expand_duplicate_labels). A return path names one with a quoted segment, data[*].\"<label>[<element label>]\", or all with a prefix glob, data[*].<label>*. Default false.",
			},
			{
				Name:        "element",
				Type:        "string",
				Description: "Crosstab cell or margin_aggregations only: the display label of the ONE member the slot reads, making it a scalar — that element's figure (the scalar twin's: AGG_AVERAGE / AGG_SUM of the member, except an unanswered element stays this operator's NaN / 0), its floor {n, n_null} over the member's presence (so a record reaches an auxiliary margin only if its selected member reached a cell) and one-entry components. Fuses and margins like the scalar twin. Refused beside expand (element_with_expand), outside a crosstab (element_outside_crosstab), naming no member label (element_unknown) or a shared one (element_ambiguous).",
			},
		},
		AcceptsTypes:  numericFieldTypesNoDecimal,
		EmitsTypeNote: emits,
		Streamable:    true,
		ComponentSchema: aggSchema(descriptor.Mergeable,
			descriptor.ComponentKey{Name: "labels", Type: "[]string", Description: "Display label per member, in member order: the vector's labels, else params.labels, else the member names. An element slot (params.element) reports its one member, here and in every per-member key."},
			descriptor.ComponentKey{Name: "element_n", Type: "[]int", Description: "Per member: rows whose member answered (raw, like the floor's n; the floor counts a row present when ANY member answered)."},
			descriptor.ComponentKey{Name: "element_sum", Type: "[]float64", Description: "Per member: sum(w*x) over contributing rows (unweighted: the plain sum). Merged per merge block in a fixed tree, bit-identical for any worker count."},
			descriptor.ComponentKey{Name: "element_sum_weights", Type: "[]float64", Description: "Weighted slot only: per member, the sum of contributing rows' weights (valid and > 0) — AGG_VEC_MEAN's denominator.", Optional: true},
		),
	}
}

// modelErrorFieldTypes are the column types a model-error aggregator
// (AGG_MAPE / AGG_RMSE / AGG_MAE) reads on either side: the integer and
// float scalars plus packed_bool (a 0/1 outcome scored against a
// probability). Mirrors processing.modelErrorFieldTypeOK and the
// predict check modelErrorTypeOK; categorical, set, date, datetime and
// decimal128 are refused PROCESSING_CONFIG on both sides.
var modelErrorFieldTypes = []string{
	"f32",
	"f64",
	"packed_bool",
	"u16",
	"u32",
	"u4",
	"u64",
	"u8",
}

// modelErrorCapability declares one model-error aggregator: the shared
// prediction_field param, the shared floor-plus-sums components and
// the operator's own keys.
func modelErrorCapability(op types.AggregationType, desc string, own ...descriptor.ComponentKey) descriptor.Operator {
	keys := []descriptor.ComponentKey{
		{Name: "loss_sum", Type: "float64", Description: "Running sum of the per-row loss over contributing rows (weighted slot: of weight * loss). Additive across partitions."},
		{Name: "weight_sum", Type: "float64", Description: "Running sum of the weights of contributing rows; the contributing-row count on an unweighted slot. Additive across partitions."},
		{Name: "n_scored", Type: "int", Description: "Rows that contributed: actual and prediction both answered (and, weighted, the weight was valid and > 0)."},
		{Name: "n_null_prediction", Type: "int", Description: "Rows whose actual answered but whose prediction is null; skipped. The floor's n / n_null key on the actual (field) only."},
	}
	keys = append(keys, own...)
	return descriptor.Operator{
		Name:        string(op),
		Category:    "aggregator",
		Description: desc,
		Params: []descriptor.Param{
			{
				Name:        "prediction_field",
				Type:        "field",
				Required:    true,
				FieldFilter: "numeric",
				Description: "Column holding the predicted value scored against the actual in field — typically an ATTR_REG_PREDICT output in the same request, or any numeric schema column. A null prediction skips the row (counted in n_null_prediction).",
			},
		},
		AcceptsTypes:    modelErrorFieldTypes,
		EmitsTypeNote:   "scalar float64 (NaN, null in JSON, when no row contributed)",
		Streamable:      true,
		ComponentSchema: aggSchema(descriptor.Mergeable, keys...),
	}
}
