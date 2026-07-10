package types

import "encoding/json"

// This file defines the request/response types for Pulse as plain Go structs
// with JSON tags. These types are the JSON-serializable shapes used by the
// processing pipeline, CLI --json envelopes, and api predict.
//
// Design note: these are plain Go structs rather than protobuf-generated code.
// This is acceptable for v0.x; the types can be migrated to protobuf later.
// The key requirement is that JSON round-tripping works correctly.

// AggregationType identifies a specific aggregation operation.
type AggregationType string

const (
	AGG_COUNT          AggregationType = "AGG_COUNT"
	AGG_SUM            AggregationType = "AGG_SUM"
	AGG_AVERAGE        AggregationType = "AGG_AVERAGE"
	AGG_MIN            AggregationType = "AGG_MIN"
	AGG_MAX            AggregationType = "AGG_MAX"
	AGG_STDDEV         AggregationType = "AGG_STDDEV"
	AGG_RANGE          AggregationType = "AGG_RANGE"
	AGG_FREQUENCY      AggregationType = "AGG_FREQUENCY"
	AGG_ZSCORE         AggregationType = "AGG_ZSCORE"
	AGG_MEDIAN         AggregationType = "AGG_MEDIAN"
	AGG_VARIANCE       AggregationType = "AGG_VARIANCE"
	AGG_MODE           AggregationType = "AGG_MODE"
	AGG_SKEWNESS       AggregationType = "AGG_SKEWNESS"
	AGG_KURTOSIS       AggregationType = "AGG_KURTOSIS"
	AGG_DISTINCT_COUNT AggregationType = "AGG_DISTINCT_COUNT"
	AGG_PERCENTILE     AggregationType = "AGG_PERCENTILE"

	// AGG_NULL_COUNT counts records where the field is null. Inverse of
	// AGG_COUNT, which counts records where the field is non-null. Stays
	// streamable (O(1) state per row, no buffering).
	AGG_NULL_COUNT AggregationType = "AGG_NULL_COUNT"

	// AGG_WEIGHTED_MEAN computes a weighted arithmetic mean of the
	// target field using a separately-named weight field. Params:
	// `weight_field` (string, required). Emits sum(field * w) / sum(w).
	// Streamable + mergeable via weighted Chan-Welford.
	AGG_WEIGHTED_MEAN AggregationType = "AGG_WEIGHTED_MEAN"

	// AGG_RATIO emits sum(numerator_field) / sum(denominator_field) at
	// finalize. Params: `numerator_field` + `denominator_field`. The
	// Aggregation's own Field is ignored. Streamable + mergeable (two
	// independent sums).
	AGG_RATIO AggregationType = "AGG_RATIO"

	// AGG_CI_LOWER and AGG_CI_UPPER emit the lower / upper bound of a
	// confidence interval for the mean of the named numeric field.
	// Params: `confidence` (float, default 0.95), `method` (string,
	// default "normal"). The "normal" method uses Welford mean + sample
	// variance with z = inv_normal((1+confidence)/2); streamable +
	// mergeable. The "bootstrap" method is reserved for a buffered
	// follow-up — returns PROCESSING_CONFIG today.
	AGG_CI_LOWER AggregationType = "AGG_CI_LOWER"
	AGG_CI_UPPER AggregationType = "AGG_CI_UPPER"

	// AGG_WELFORD emits the streaming Welford-Pébaÿ triple — running
	// mean, running sample variance (n-1 denominator), and observed
	// count — over a numeric field. Returns the running mean through
	// the legacy float64 contract and the typed WelfordTriple via the
	// RichAggregator interface so overlay handlers (OVERLAY_T_CELL /
	// OVERLAY_Z_CELL) can type-switch on the moment payload
	// without re-deriving variance from raw rows. Streamable; mergeable
	// via Chan-Welford. Margin reducibility is recompute (variance does
	// not pool by addition). Field type restricted to the strict scalar
	// numeric family (u8/u16/u32/u64/f32/f64); decimal128 deferred.
	AGG_WELFORD AggregationType = "AGG_WELFORD"

	// Set-typed aggregators. Each consumes a single set_* field whose
	// per-row payload is a fixed-width bitmask over a shared dictionary;
	// bit i ↔ dictionary entry i.

	// AGG_SET_UNION reduces by bitwise OR across rows. The result is a
	// uint64 mask representing the union of every selection observed.
	// Mergeable + streamable; margin-summable for crosstab.
	AGG_SET_UNION AggregationType = "AGG_SET_UNION"

	// AGG_SET_INTERSECTION reduces by bitwise AND across rows. The
	// result mask carries a bit only when every contributing row had
	// that bit set. Mergeable + streamable, but NOT margin-reducible
	// (AND across cells ≠ AND across all rows in general) — crosstab
	// margins recompute from raw rows.
	AGG_SET_INTERSECTION AggregationType = "AGG_SET_INTERSECTION"

	// AGG_SET_FREQUENCY emits a per-element histogram. The result is
	// map[label]int — one entry per dictionary value, counting the rows
	// whose mask had that bit set. Mergeable (bin-by-bin sum) + summable
	// margin. This is the survey-friendly default ("respondents per
	// issuer"). Used as a Crosstab Cell aggregator the result is a
	// map-valued cell payload.
	AGG_SET_FREQUENCY AggregationType = "AGG_SET_FREQUENCY"

	// AGG_SET_CARDINALITY_SUM sums popcount(mask) across rows. Mergeable
	// + streamable + summable margin.
	AGG_SET_CARDINALITY_SUM AggregationType = "AGG_SET_CARDINALITY_SUM"

	// AGG_SET_CARDINALITY_AVG returns mean popcount(mask) per row.
	// Mergeable + streamable; margin = mean-reducible (needs per-cell n).
	AGG_SET_CARDINALITY_AVG AggregationType = "AGG_SET_CARDINALITY_AVG"

	// AGG_SET_DISTINCT_VALUES counts the distinct exact masks observed
	// (treats each combination as atomic). Mergeable via the union of
	// seen-mask sets.
	AGG_SET_DISTINCT_VALUES AggregationType = "AGG_SET_DISTINCT_VALUES"
)

// AllAggregationTypes returns all defined aggregation types.
func AllAggregationTypes() []AggregationType {
	return []AggregationType{
		AGG_COUNT, AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX,
		AGG_STDDEV, AGG_RANGE, AGG_FREQUENCY, AGG_ZSCORE,
		AGG_MEDIAN, AGG_VARIANCE, AGG_MODE, AGG_SKEWNESS, AGG_KURTOSIS,
		AGG_DISTINCT_COUNT, AGG_PERCENTILE,
		AGG_NULL_COUNT,
		AGG_WEIGHTED_MEAN, AGG_RATIO,
		AGG_CI_LOWER, AGG_CI_UPPER,
		AGG_WELFORD,
		AGG_SET_UNION, AGG_SET_INTERSECTION, AGG_SET_FREQUENCY,
		AGG_SET_CARDINALITY_SUM, AGG_SET_CARDINALITY_AVG,
		AGG_SET_DISTINCT_VALUES,
	}
}

// FiltererType identifies a specific filter operation.
type FiltererType string

const (
	FILTER_INCLUDE    FiltererType = "FILTER_INCLUDE"
	FILTER_EXCLUDE    FiltererType = "FILTER_EXCLUDE"
	FILTER_RANGE      FiltererType = "FILTER_RANGE"
	FILTER_EXPRESSION FiltererType = "FILTER_EXPRESSION"

	// FILTER_NULL keeps or drops records based on null state of a field.
	// Mode is "is_null" (keep null-valued records) or "is_not_null" (keep
	// non-null records). Row-local; streamable.
	FILTER_NULL FiltererType = "FILTER_NULL"

	// FILTER_TRUE keeps records where Field is logically true. Default
	// (strict) mode requires Field to be packed_bool and keeps records
	// whose value is 1. Opt-in JavaScript-style truthiness coercion is
	// enabled with Values=["truthy"]; in that mode any field type is
	// accepted and the same rules JS uses for `Boolean(value)` apply
	// (0, NaN, empty string, null → falsy; everything else → truthy).
	// Row-local; streamable.
	FILTER_TRUE FiltererType = "FILTER_TRUE"

	// FILTER_FALSE keeps records where Field is logically false. Default
	// (strict) mode requires Field to be packed_bool and keeps records
	// whose value is 0. Opt-in JavaScript-style falsiness coercion is
	// enabled with Values=["truthy"]; in that mode any field type is
	// accepted and records whose value would coerce to falsy under
	// `Boolean(value)` are kept (0, NaN, empty string, null → kept).
	// Row-local; streamable.
	FILTER_FALSE FiltererType = "FILTER_FALSE"

	// Set-typed filterers. Each takes a set_* Field plus a Values []string
	// list of dictionary labels; the builder resolves the labels into a
	// query mask at compile time so the per-row check is a single bitwise
	// op. Row-local; streamable.

	// FILTER_SET_CONTAINS_ANY keeps records whose set mask shares at
	// least one bit with the resolved query mask (row & q != 0).
	FILTER_SET_CONTAINS_ANY FiltererType = "FILTER_SET_CONTAINS_ANY"

	// FILTER_SET_CONTAINS_ALL keeps records whose set mask has every bit
	// in the resolved query mask set (row & q == q).
	FILTER_SET_CONTAINS_ALL FiltererType = "FILTER_SET_CONTAINS_ALL"

	// FILTER_SET_CONTAINS_NONE keeps records whose set mask shares no
	// bits with the resolved query mask (row & q == 0).
	FILTER_SET_CONTAINS_NONE FiltererType = "FILTER_SET_CONTAINS_NONE"

	// FILTER_SET_EQUALS keeps records whose set mask exactly equals the
	// resolved query mask.
	FILTER_SET_EQUALS FiltererType = "FILTER_SET_EQUALS"
)

// AllFiltererTypes returns all defined filterer types.
func AllFiltererTypes() []FiltererType {
	return []FiltererType{
		FILTER_EXCLUDE,
		FILTER_EXPRESSION,
		FILTER_FALSE,
		FILTER_INCLUDE,
		FILTER_NULL,
		FILTER_RANGE,
		FILTER_SET_CONTAINS_ALL,
		FILTER_SET_CONTAINS_ANY,
		FILTER_SET_CONTAINS_NONE,
		FILTER_SET_EQUALS,
		FILTER_TRUE,
	}
}

// GroupType identifies a specific grouping operation.
type GroupType string

const (
	GROUP_CATEGORY GroupType = "GROUP_CATEGORY"
	GROUP_ROUNDED  GroupType = "GROUP_ROUNDED"
	GROUP_RANGE    GroupType = "GROUP_RANGE"
	GROUP_QUANTILE GroupType = "GROUP_QUANTILE"
	GROUP_DATE     GroupType = "GROUP_DATE"

	// GROUP_SET_VALUE treats the set mask as an atomic value. Each
	// distinct mask is its own bucket; the bucket key is the sorted
	// pipe-delimited label list (e.g. "AMEX|VISA"). One row → one
	// bucket. Streamable + mergeable.
	GROUP_SET_VALUE GroupType = "GROUP_SET_VALUE"

	// GROUP_SET_PER_ELEMENT explodes the set: each row fans into one
	// bucket per set bit, keyed by the resolved dictionary label. One
	// row → N buckets (where N = popcount(mask)). Cardinality multiplies.
	// Streamable via the multi-key streaming hook (KeysForRow on
	// StreamingGrouper). This is the survey-friendly default
	// ("respondents per option").
	GROUP_SET_PER_ELEMENT GroupType = "GROUP_SET_PER_ELEMENT"
)

// AllGroupTypes returns all defined group types.
func AllGroupTypes() []GroupType {
	return []GroupType{
		GROUP_CATEGORY,
		GROUP_DATE,
		GROUP_QUANTILE,
		GROUP_RANGE,
		GROUP_ROUNDED,
		GROUP_SET_PER_ELEMENT,
		GROUP_SET_VALUE,
	}
}

// AttributeType identifies a specific derived-attribute computation.
type AttributeType string

const (
	ATTR_ZSCORE     AttributeType = "ATTR_ZSCORE"
	ATTR_TSCORE     AttributeType = "ATTR_TSCORE"
	ATTR_NORMALIZED AttributeType = "ATTR_NORMALIZED"
	ATTR_FORMULA    AttributeType = "ATTR_FORMULA"
	ATTR_PERCENTILE AttributeType = "ATTR_PERCENTILE"
	ATTR_DATE_PART  AttributeType = "ATTR_DATE_PART"

	// ATTR_REG_FITTED emits the per-row fitted value ŷᵢ = Xᵢ · β + β₀ for
	// each filter-passing record, using a regression fit computed in a
	// streaming prepass over the same record set. Two-pass: prepass folds
	// the OLS sufficient statistics, finalize freezes the coefficient
	// vector, pass 2 emits ŷᵢ. Carries its own RegressionSpec-shaped
	// fields (Target, Predictors, Penalty, Alpha, L1Ratio); refits
	// internally per attribute (Option A). Accepts any OLS penalty
	// (unpenalized, ridge, lasso, elasticnet); GLM and Bayesian fits are
	// deferred.
	ATTR_REG_FITTED AttributeType = "ATTR_REG_FITTED"

	// ATTR_REG_RESIDUAL emits the per-row residual yᵢ − ŷᵢ for each
	// filter-passing record, using the same OLS prepass machinery as
	// ATTR_REG_FITTED. Sums to ≈ 0 when the fit includes an intercept
	// (always true for unpenalized OLS) and the response is observed for
	// every contributing row.
	ATTR_REG_RESIDUAL AttributeType = "ATTR_REG_RESIDUAL"

	// ATTR_REG_LEVERAGE emits the per-row hat-matrix diagonal
	// hᵢᵢ = xᵢ · (XᵀX)⁻¹ · xᵢᵀ for each filter-passing record. Restricted
	// to unpenalized OLS — penalized leverage uses a different formula
	// (involving the regularized resolvent) and GLM leverage requires the
	// IRLS weight matrix; both deferred to Phase 9. Specs with any
	// Penalty set are rejected at factory time with PROCESSING_CONFIG.
	ATTR_REG_LEVERAGE AttributeType = "ATTR_REG_LEVERAGE"

	// ATTR_SET_POPCOUNT emits popcount(mask) per row as a numeric column.
	// Field must reference a set_* field. Streamable.
	ATTR_SET_POPCOUNT AttributeType = "ATTR_SET_POPCOUNT"

	// ATTR_SET_HAS emits a packed_bool per row indicating whether the
	// set field has a specific dictionary label set. Params.value names
	// the label whose bit is tested. Streamable.
	ATTR_SET_HAS AttributeType = "ATTR_SET_HAS"
)

// AllAttributeTypes returns all defined attribute types.
func AllAttributeTypes() []AttributeType {
	return []AttributeType{
		ATTR_DATE_PART,
		ATTR_FORMULA,
		ATTR_NORMALIZED,
		ATTR_PERCENTILE,
		ATTR_REG_FITTED,
		ATTR_REG_LEVERAGE,
		ATTR_REG_RESIDUAL,
		ATTR_SET_HAS,
		ATTR_SET_POPCOUNT,
		ATTR_TSCORE,
		ATTR_ZSCORE,
	}
}

// WindowType identifies a specific window operation.
type WindowType string

const (
	WIN_LAG         WindowType = "WIN_LAG"
	WIN_LEAD        WindowType = "WIN_LEAD"
	WIN_ROW_NUMBER  WindowType = "WIN_ROW_NUMBER"
	WIN_RANK        WindowType = "WIN_RANK"
	WIN_DENSE_RANK  WindowType = "WIN_DENSE_RANK"
	WIN_RUNNING_SUM WindowType = "WIN_RUNNING_SUM"
	WIN_RUNNING_AVG WindowType = "WIN_RUNNING_AVG"
	WIN_MOVING_AVG  WindowType = "WIN_MOVING_AVG"
	WIN_EWMA        WindowType = "WIN_EWMA"
	WIN_PCT_CHANGE  WindowType = "WIN_PCT_CHANGE"
)

// AllWindowTypes returns all defined window types in alphabetical order.
func AllWindowTypes() []WindowType {
	return []WindowType{
		WIN_DENSE_RANK,
		WIN_EWMA,
		WIN_LAG,
		WIN_LEAD,
		WIN_MOVING_AVG,
		WIN_PCT_CHANGE,
		WIN_RANK,
		WIN_ROW_NUMBER,
		WIN_RUNNING_AVG,
		WIN_RUNNING_SUM,
	}
}

// FeatureType identifies a specific ML feature engineering operator.
// Features run pre-filter and may emit one or more output columns.
type FeatureType string

const (
	FEAT_LOG              FeatureType = "FEAT_LOG"
	FEAT_SQRT             FeatureType = "FEAT_SQRT"
	FEAT_BUCKETIZE        FeatureType = "FEAT_BUCKETIZE"
	FEAT_ONE_HOT          FeatureType = "FEAT_ONE_HOT"
	FEAT_DATE_FEATURES    FeatureType = "FEAT_DATE_FEATURES"
	FEAT_FREQUENCY_ENCODE FeatureType = "FEAT_FREQUENCY_ENCODE"
	FEAT_TARGET_ENCODE    FeatureType = "FEAT_TARGET_ENCODE"
	FEAT_TRAIN_TEST_SPLIT FeatureType = "FEAT_TRAIN_TEST_SPLIT"
	FEAT_POLY             FeatureType = "FEAT_POLY"
)

// AllFeatureTypes returns every defined feature type in alphabetical order.
func AllFeatureTypes() []FeatureType {
	return []FeatureType{
		FEAT_BUCKETIZE,
		FEAT_DATE_FEATURES,
		FEAT_FREQUENCY_ENCODE,
		FEAT_LOG,
		FEAT_ONE_HOT,
		FEAT_POLY,
		FEAT_SQRT,
		FEAT_TARGET_ENCODE,
		FEAT_TRAIN_TEST_SPLIT,
	}
}

// TestType identifies a specific statistical test operation.
//
// Pulse splits statistical testing into two tiers, both expressed via this
// enum and both executed inside a single Process pipeline:
//
//   - Tier 1 (row tests): listed in Request.Tests, evaluated against the
//     raw row stream alongside aggregators. Online-moments tests reuse the
//     running mean/variance/n that aggregators already compute, so they add
//     near-zero cost when their inputs overlap with active aggregations.
//   - Tier 2 (post tests): listed in Request.PostTests, evaluated after the
//     window stage on the materialized result row set. Useful for ANOVA
//     across grouper buckets, post-hoc pairwise comparisons, and trend
//     tests over windowed series.
//
// Per-type semantics, required fields, and streamability are documented in
// skills/statistical-testing.md.
type TestType string

const (
	// TEST_T is a one-sample or two-sample Welch t-test on a numeric field.
	// When SplitBy is set, the test partitions Field by SplitBy and compares
	// the two resulting groups; otherwise it tests Field's mean against the
	// hypothesized value supplied in Params.
	TEST_T TestType = "TEST_T"

	// TEST_WELCH is an explicit two-sample Welch t-test alias used when the
	// caller wants to be unambiguous about the variant. Behaves identically
	// to TEST_T with SplitBy set; provided so requests document intent.
	TEST_WELCH TestType = "TEST_WELCH"

	// TEST_CHISQ is a chi-square independence test on a 2D contingency
	// table built from two categorical fields (Rows × Cols).
	TEST_CHISQ TestType = "TEST_CHISQ"

	// TEST_ANOVA_F is a one-way analysis of variance F-test comparing the
	// means of a numeric Field across k groups defined by a categorical
	// SplitBy field. k must be ≥ 2.
	TEST_ANOVA_F TestType = "TEST_ANOVA_F"

	// TEST_KS is a Kolmogorov-Smirnov two-sample distribution test. Not
	// streamable — requires sorted ECDFs.
	TEST_KS TestType = "TEST_KS"

	// TEST_TUKEY_HSD is a post-hoc pairwise comparison of group means using
	// Tukey's Honestly Significant Difference. Tier-2 only: consumes the
	// per-group means and counts produced by upstream aggregators.
	TEST_TUKEY_HSD TestType = "TEST_TUKEY_HSD"

	// TEST_TREND is a Mann-Kendall trend test over an ordered numeric
	// series. Tier-2 typical: runs over windowed result rows; tier-1
	// possible when the raw field is naturally ordered by an OrderBy key.
	TEST_TREND TestType = "TEST_TREND"

	// TEST_PEARSON_R is the parametric correlation test between two
	// numeric fields. Streams via an extended Welford recurrence that
	// also tracks the running cross-product. p-value via the t-statistic
	// r·√((n−2)/(1−r²)) with df = n − 2.
	TEST_PEARSON_R TestType = "TEST_PEARSON_R"

	// TEST_PAIRED_T is the paired-sample t-test on the per-row difference
	// d = Field − Field2. Streams via the same Welford machinery as the
	// one-sample TEST_T, driven by two-field reads.
	TEST_PAIRED_T TestType = "TEST_PAIRED_T"

	// TEST_PROP_Z is the two-proportion z-test on a categorical SplitBy
	// field where each row counts as a success or failure based on
	// Params.success (the dictionary value treated as a "success" on
	// Field). Streams via per-group success / total counts.
	TEST_PROP_Z TestType = "TEST_PROP_Z"

	// TEST_MANN_WHITNEY_U is the nonparametric two-sample alternative to
	// TEST_T. Buffered: ranks the combined value set under tie correction,
	// sums the ranks in group A → R_A, then computes
	//   U_A = R_A − n_A(n_A+1)/2
	// Two-sided p-value via the normal approximation with tie correction.
	TEST_MANN_WHITNEY_U TestType = "TEST_MANN_WHITNEY_U"

	// TEST_WILCOXON_SR is the Wilcoxon signed-rank test on the per-row
	// difference d = Field − Field2. Buffered: drops zero-diff pairs,
	// ranks |d| under tie correction, sums positive-sign ranks → W.
	// Two-sided p-value via the normal approximation with tie correction.
	TEST_WILCOXON_SR TestType = "TEST_WILCOXON_SR"

	// TEST_KRUSKAL_WALLIS is the nonparametric k-group alternative to
	// TEST_ANOVA_F. Buffered: ranks the combined value set under tie
	// correction, sums ranks per group, then
	//   H = (12/(N(N+1))) · Σ (R_i²/n_i) − 3(N+1)
	// p-value via chi-square survival with k−1 df.
	TEST_KRUSKAL_WALLIS TestType = "TEST_KRUSKAL_WALLIS"

	// TEST_SPEARMAN_R is the rank-based correlation between Field and
	// Field2 (monotonic association). Buffered: mid-ranks each column
	// under tie correction, then runs Pearson on the ranks. p-value via
	//   t = ρ · √((n−2)/(1−ρ²))   df = n − 2
	TEST_SPEARMAN_R TestType = "TEST_SPEARMAN_R"

	// TEST_KENDALL_TAU is concordance-based correlation between Field
	// and Field2. Buffered O(n²) pair count under tie correction. Two-
	// sided p-value via normal approximation with the standard variance
	// adjustment for ties in either column.
	TEST_KENDALL_TAU TestType = "TEST_KENDALL_TAU"

	// TEST_ANOVA_WELCH is the heteroscedasticity-robust one-way ANOVA.
	// Streams via the same per-group Welford buckets as TEST_ANOVA_F.
	// Statistic uses per-group weights w_i = n_i / s²_i, with the
	// Welch-Satterthwaite denominator correction for unequal variances.
	TEST_ANOVA_WELCH TestType = "TEST_ANOVA_WELCH"

	// TEST_ANOVA_RM is the repeated-measures one-way ANOVA. Buffered:
	// requires SubjectField to pivot rows into the wide subject ×
	// condition table. Decomposes SS into between-subjects, treatment,
	// and error components; F = MS_treatment / MS_error.
	TEST_ANOVA_RM TestType = "TEST_ANOVA_RM"

	// TEST_BROWN_FORSYTHE is a homogeneity-of-variance test. Replaces
	// each value with its absolute deviation from the group median, then
	// runs one-way ANOVA on the deviations. Buffered: per-group medians
	// require a sort.
	TEST_BROWN_FORSYTHE TestType = "TEST_BROWN_FORSYTHE"

	// TEST_FISHER_EXACT is the exact two-sided p-value for a 2×2
	// contingency table. The canonical small-sample alternative to
	// TEST_CHISQ when any expected cell count is below 5. Buffered:
	// requires the full contingency table.
	TEST_FISHER_EXACT TestType = "TEST_FISHER_EXACT"

	// TEST_SHAPIRO_WILK is the Shapiro-Wilk normality test on Field.
	// When SplitBy is set the test runs per-group; otherwise it runs
	// over the full filtered set. Buffered: requires the ordered values.
	// Implementation supports n ≤ 5000; n above the bound surfaces a
	// PULSE_TEST_SHAPIRO_N_BOUND warning.
	TEST_SHAPIRO_WILK TestType = "TEST_SHAPIRO_WILK"

	// TEST_Z_TWO_SAMPLE is the two-sample z-test on the means of a
	// numeric Field across two groups defined by a categorical SplitBy.
	// Mathematically identical to TEST_WELCH for the test statistic and
	// standard error, but the p-value is computed from the standard
	// normal CDF Φ rather than the Student-t CDF T_df. The variant
	// matches large-sample survey conventions (e.g. weighted-proportion
	// tests already computed as means with an n denominator) where the
	// Student-t correction is not desired. For small n the divergence
	// from TEST_WELCH is non-trivial; predict surfaces no warning, so
	// callers choose intentionally. Streams via the same per-group
	// Welford buckets as TEST_T / TEST_WELCH.
	TEST_Z_TWO_SAMPLE TestType = "TEST_Z_TWO_SAMPLE"
)

// AllTestTypes returns every defined statistical test type in alphabetical
// order.
func AllTestTypes() []TestType {
	return []TestType{
		TEST_ANOVA_F,
		TEST_ANOVA_RM,
		TEST_ANOVA_WELCH,
		TEST_BROWN_FORSYTHE,
		TEST_CHISQ,
		TEST_FISHER_EXACT,
		TEST_KENDALL_TAU,
		TEST_KRUSKAL_WALLIS,
		TEST_KS,
		TEST_MANN_WHITNEY_U,
		TEST_PAIRED_T,
		TEST_PEARSON_R,
		TEST_PROP_Z,
		TEST_SHAPIRO_WILK,
		TEST_SPEARMAN_R,
		TEST_T,
		TEST_TREND,
		TEST_TUKEY_HSD,
		TEST_WELCH,
		TEST_WILCOXON_SR,
		TEST_Z_TWO_SAMPLE,
	}
}

// Test defines a single statistical test to evaluate during a Process run.
// Tests appear in two request slots: Request.Tests (tier 1, row-level) and
// Request.PostTests (tier 2, result-level). The shape is shared because
// most fields are common across tiers; per-test validation rules live in
// the registry and predict layer.
type Test struct {
	// Type is the statistical test to perform.
	Type TestType `json:"type"`

	// Field is the primary numeric field under test. Required for TEST_T,
	// TEST_WELCH, TEST_ANOVA_F, TEST_KS, and TEST_TREND. Omitted for
	// TEST_CHISQ (uses Rows × Cols instead).
	Field string `json:"field,omitempty"`

	// Field2 is the secondary numeric field reference used by paired or
	// bivariate tests (TEST_PEARSON_R, TEST_PAIRED_T). For TEST_PAIRED_T
	// the difference d = Field − Field2 is tested. For TEST_PEARSON_R
	// the correlation between Field and Field2 is reported. Empty for
	// every other test.
	Field2 string `json:"field2,omitempty"`

	// SplitBy is a categorical field whose distinct values partition Field
	// into groups. Required for two-sample TEST_T / TEST_WELCH and for
	// TEST_ANOVA_F. Two-sample tests expect exactly 2 groups; ANOVA expects
	// ≥ 2.
	SplitBy string `json:"split_by,omitempty"`

	// Rows is the row-axis categorical field for TEST_CHISQ contingency.
	Rows string `json:"rows,omitempty"`

	// Cols is the column-axis categorical field for TEST_CHISQ contingency.
	Cols string `json:"cols,omitempty"`

	// SubjectField identifies the within-subject grouping for
	// repeated-measures designs (TEST_ANOVA_RM). Each distinct value
	// represents one subject contributing one observation per condition
	// (SplitBy). Empty for every other test.
	SubjectField string `json:"subject_field,omitempty"`

	// Alpha is the significance level. Defaults to 0.05 when zero.
	// Must lie in the open interval (0, 1).
	Alpha float64 `json:"alpha,omitempty"`

	// Label is the output alias for the test result. When empty, defaults
	// to "<TYPE>" or "<TYPE>_<field>" depending on the operator.
	Label string `json:"label,omitempty"`

	// OrderBy supplies ordering keys for tests that need a sorted series
	// (TEST_TREND, TEST_KS when run as a series test). Tier-2 trend tests
	// typically reference an output column produced by an upstream window
	// or grouper.
	OrderBy []OrderKey `json:"order_by,omitempty"`

	// Params holds operator-specific configuration as raw JSON.
	//   TEST_T (one-sample): {"mu": <hypothesized mean>}
	//   TEST_T / TEST_WELCH (two-sample): {"variant": "welch"} (default)
	//   TEST_KS: {"alternative": "two-sided"|"less"|"greater"}
	//   TEST_TUKEY_HSD: {"ms_within": <f64>, "df_within": <f64>}
	//   TEST_TREND: {"variant": "mann_kendall"} (default)
	Params json.RawMessage `json:"params,omitempty"`
}

// TestResult is the per-test outcome embedded in Response.Tests and
// Response.PostTests. Common statistics live as top-level fields; operator-
// specific payload (contingency tables, pairwise comparisons, effect sizes,
// per-group moments) lives in Details so the result shape stays predictable
// across the catalog.
type TestResult struct {
	// Label echoes the request Label or the operator's synthesized default.
	Label string `json:"label,omitempty"`

	// Type is the test that produced this result.
	Type TestType `json:"type"`

	// Variant names the specific algorithm when a test supports more than
	// one (e.g. "welch_two_sample", "mann_kendall", "independence").
	Variant string `json:"variant,omitempty"`

	// Statistic is the test statistic (t, F, χ², KS D, Mann-Kendall S, etc.).
	Statistic float64 `json:"statistic"`

	// DF is the degrees of freedom. Tests with multiple DF values (e.g.
	// ANOVA's between/within) report the numerator DF here and surface the
	// remainder in Details.
	DF float64 `json:"df,omitempty"`

	// PValue is the two-sided p-value under the null hypothesis.
	PValue float64 `json:"p_value"`

	// Alpha is the significance threshold applied to RejectNull.
	Alpha float64 `json:"alpha"`

	// RejectNull is true when PValue < Alpha.
	RejectNull bool `json:"reject_null"`

	// Details holds operator-specific payload (per-group n/mean/variance,
	// contingency table, pairwise comparisons, confidence intervals,
	// effect-size measures). Marshals naturally as a nested JSON object.
	Details map[string]any `json:"details,omitempty"`

	// Warnings carries per-test diagnostics (low N, near-zero variance,
	// expected-count thresholds, etc.) so the envelope-level Warnings list
	// is not the only signal.
	Warnings []string `json:"warnings,omitempty"`
}

// Feature defines a feature engineering operation. Features run pre-filter
// (before any FILTER_* predicate) and may produce one or more derived
// columns. Global-pass features (TARGET_ENCODE, FREQUENCY_ENCODE) require a
// stats sweep before per-row write; per-row features compute one row at a
// time.
type Feature struct {
	// Type is the feature operator to perform.
	Type FeatureType `json:"type"`

	// Field is the source field name. Required by every operator except
	// FEAT_TRAIN_TEST_SPLIT (which reads no field by default — params may
	// optionally name a stratify field).
	Field string `json:"field,omitempty"`

	// Label is an output column name (single-output operators) or output
	// column prefix (multi-output operators). When empty, the operator
	// derives a default — typically "<TYPE>_<field>".
	Label string `json:"label,omitempty"`

	// Params holds operator-specific parameters as raw JSON. See the
	// feature-engineering skill for the per-operator schema.
	Params json.RawMessage `json:"params,omitempty"`
}

// OrderKey specifies an ordering key for a window's ORDER BY clause.
type OrderKey struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// FrameSpec specifies the window frame bounds.
// Mode is "rows" — only frame mode supported in v1.
// Preceding nil means UNBOUNDED PRECEDING; Following nil means UNBOUNDED FOLLOWING.
// Following==0 with Preceding==0 selects the current row only.
type FrameSpec struct {
	Mode      string `json:"mode"`
	Preceding *int   `json:"preceding,omitempty"`
	Following *int   `json:"following,omitempty"`
}

// Window defines a window operation.
type Window struct {
	// Type is the window operation to perform.
	Type WindowType `json:"type"`

	// Field is the source field name. Required for all operators except
	// ROW_NUMBER, RANK, and DENSE_RANK.
	Field string `json:"field,omitempty"`

	// Label is the output column name. When empty, defaults to "<TYPE>_<field>"
	// or "<TYPE>" for ROW_NUMBER/RANK/DENSE_RANK.
	Label string `json:"label,omitempty"`

	// PartitionBy is the list of fields whose distinct values define
	// independent partitions for window evaluation. Empty means a single
	// partition over all rows.
	PartitionBy []string `json:"partition_by,omitempty"`

	// OrderBy is the list of order keys. Required (≥1) for every window operator.
	OrderBy []OrderKey `json:"order_by"`

	// Frame is the window frame specification. Required for RUNNING_*, MOVING_AVG,
	// and EWMA. Rejected for LAG, LEAD, ROW_NUMBER, RANK, DENSE_RANK, PCT_CHANGE.
	Frame *FrameSpec `json:"frame,omitempty"`

	// Params holds operator-specific parameters as raw JSON.
	// LAG/LEAD: {"offset": 1, "default": null}
	// EWMA: {"alpha": 0.5}
	// PCT_CHANGE: {"periods": 1}
	Params json.RawMessage `json:"params,omitempty"`
}

// Aggregation defines a single aggregation operation to apply to a field.
type Aggregation struct {
	// Type is the aggregation operation to perform.
	Type AggregationType `json:"type"`

	// Field is the name of the data field to aggregate.
	Field string `json:"field"`

	// Label is an optional output label for the aggregation result.
	Label string `json:"label,omitempty"`

	// Params holds type-specific configuration as raw JSON.
	// Used by aggregation types that require additional parameters (e.g., AGG_PERCENTILE).
	Params json.RawMessage `json:"params,omitempty"`
}

// Filterer defines a filter to apply to records before processing.
type Filterer struct {
	// Type is the filter operation to perform.
	Type FiltererType `json:"type"`

	// Field is the name of the data field to filter on.
	// Not required for FILTER_EXPRESSION.
	Field string `json:"field,omitempty"`

	// Values is a list of values for include/exclude/range filters.
	Values []string `json:"values,omitempty"`

	// Expression is a runtime expression for FILTER_EXPRESSION type.
	Expression string `json:"expression,omitempty"`
}

// Group defines a grouping operation to partition results.
type Group struct {
	// Type is the grouping operation to perform.
	Type GroupType `json:"type"`

	// Field is the name of the data field to group by.
	Field string `json:"field"`

	// Interval is used by GROUP_ROUNDED and GROUP_RANGE to define the bucket width.
	Interval float64 `json:"interval,omitempty"`

	// Params holds type-specific configuration as raw JSON.
	// Used by group types that require additional parameters (e.g., GROUP_DATE).
	Params json.RawMessage `json:"params,omitempty"`

	// Include optionally restricts the grouper to a fixed set of bucket
	// keys. Rows whose computed key (or, for per-element groupers, each
	// individual key produced by a single row) is not present in this
	// list are skipped — identical to the null-key handling path, no
	// bucket is created for the rejected key. Supported by:
	//
	//   - GROUP_CATEGORY        — matches the categorical label string.
	//   - GROUP_SET_VALUE       — matches the sorted, pipe-joined
	//                             composite bucket key (e.g. "MC|VISA").
	//   - GROUP_SET_PER_ELEMENT — matches each fan-out label
	//                             independently; the row contributes
	//                             only to surviving labels.
	//
	// A non-empty Include is order-significant: output buckets emit in
	// the order values are listed here — across both the plain grouped
	// payload (Response.Data rows and Response.Components buckets) and
	// each crosstab axis independently (an axis without Include keeps
	// its prior alphabetical / dict-index order). Include values that
	// match zero records are still dropped, never emitted as empty
	// buckets.
	//
	// Empty / nil Include means "no inclusion filter" and preserves the
	// prior alphabetical bucket order (dict-index order for
	// GROUP_SET_PER_ELEMENT) — byte-identical to the pre-Include output.
	// Other grouper types ignore Include — predict / runtime gates flag
	// misuse.
	Include []string `json:"include,omitempty"`
}

// Attribute defines a derived attribute computation.
type Attribute struct {
	// Type is the attribute computation to perform.
	Type AttributeType `json:"type"`

	// Field is the name of the source data field. Optional for the
	// regression attribute family (ATTR_REG_FITTED / RESIDUAL / LEVERAGE)
	// which read Target + Predictors instead; when empty the output Label
	// defaults to "<TYPE>_<Target>".
	Field string `json:"field"`

	// Label is the output name for the derived attribute.
	Label string `json:"label,omitempty"`

	// Expression is a runtime expression for ATTR_FORMULA type.
	Expression string `json:"expression,omitempty"`

	// Params holds type-specific configuration as raw JSON.
	// Each attribute type defines its own params schema.
	Params json.RawMessage `json:"params,omitempty"`

	// --- Regression-attribute fields (Option A: each ATTR_REG_* carries
	// its own RegressionSpec-shaped slice and refits internally during
	// the prepass). Ignored by every non-regression attribute type.

	// Target is the dependent variable for ATTR_REG_FITTED / RESIDUAL /
	// LEVERAGE. Required for those types.
	Target string `json:"target,omitempty"`

	// Predictors lists the independent variables for the regression-
	// attribute family. At least one predictor is required.
	Predictors []string `json:"predictors,omitempty"`

	// Penalty selects the OLS regularization scheme for ATTR_REG_FITTED /
	// ATTR_REG_RESIDUAL. One of "" (unpenalized), "l1" (lasso), "l2"
	// (ridge), or "elasticnet". ATTR_REG_LEVERAGE rejects any non-empty
	// Penalty (penalized leverage and GLM leverage are deferred to a
	// later phase).
	Penalty string `json:"penalty,omitempty"`

	// Alpha is the regularization strength for the regression-attribute
	// family when Penalty is non-empty. Must be > 0 in that case.
	Alpha float64 `json:"alpha,omitempty"`

	// L1Ratio is the elastic-net mixing parameter for the regression-
	// attribute family when Penalty == "elasticnet" (0 < L1Ratio < 1).
	L1Ratio float64 `json:"l1_ratio,omitempty"`
}

// Output configures how processing results are formatted.
type Output struct {
	// Format is the output format (e.g., "json", "csv").
	Format string `json:"format"`

	// Filename is an optional output file path.
	Filename string `json:"filename,omitempty"`

	// Pretty enables indented/formatted output.
	Pretty bool `json:"pretty,omitempty"`

	// IncludeNil controls whether nil/null values appear in output.
	IncludeNil bool `json:"include_nil,omitempty"`
}

// Cohort identifies a .pulse data file for processing.
type Cohort struct {
	// Filename is the name of the .pulse file.
	Filename string `json:"filename"`

	// DataDir is the directory containing the cohort file.
	DataDir string `json:"data_dir,omitempty"`
}

// Request is the primary processing request type.
// It specifies a cohort, filters, aggregations, attributes, groups, and output config.
type Request struct {
	// Label is an optional caller-supplied alias used by Compose-only
	// overlay kinds to resolve sibling references across slots in a
	// ComposedRequest (e.g. ComposeOverlaySpec.Reference / Targets).
	// Empty is the zero value and the additive contract holds — the
	// `omitempty` tag keeps the JSON wire shape byte-identical to
	// pre-Label callers, including the CanonicalHash output (see
	// TestCanonicalHash_RequestLabelEmptyByteIdentical).
	//
	// Inside a Compose batch the engine synthesizes `request_<index+1>`
	// (1-based) for every slot that arrives with Label empty, before
	// dispatching the slot — the synthesis happens against an in-memory
	// clone so the caller's *Request pointer is not mutated. Two slots
	// resolving to the same final Label are rejected with
	// PULSE_COMPOSE_LABEL_COLLISION at the same validate hook.
	//
	// Outside Compose (a standalone pulse.Process call) Label is
	// retained verbatim but has no behavioural effect today; future
	// stories may light it up for downstream reference resolution.
	Label string `json:"label,omitempty"`

	// Cohort identifies the .pulse file to process.
	Cohort *Cohort `json:"cohort,omitempty"`

	// Filterers is the list of filters to apply before processing.
	Filterers []*Filterer `json:"filterers,omitempty"`

	// Aggregations is the list of aggregation operations.
	Aggregations []*Aggregation `json:"aggregations,omitempty"`

	// Attributes is the list of derived attribute computations.
	Attributes []*Attribute `json:"attributes,omitempty"`

	// Groups is the list of grouping operations.
	Groups []*Group `json:"groups,omitempty"`

	// Outputs configures result formatting.
	Outputs []*Output `json:"outputs,omitempty"`

	// Windows is the list of window operations evaluated after aggregation.
	Windows []*Window `json:"windows,omitempty"`

	// Features is the list of pre-filter feature engineering operators.
	// Each operator may add one or more derived columns to the working
	// schema before filters and downstream stages run.
	Features []*Feature `json:"features,omitempty"`

	// Sort orders response rows by the listed keys. Applied last in the
	// pipeline (after windows). Each key field must reference a schema
	// field, an aggregation/attribute/group/window output label, or any
	// column produced by upstream stages. Stable sort; nulls last
	// regardless of direction.
	Sort []OrderKey `json:"sort,omitempty"`

	// Tests is the list of tier-1 statistical tests evaluated alongside
	// aggregators against the raw row stream. Online-moments tests
	// (TEST_T, TEST_WELCH, TEST_CHISQ, TEST_ANOVA_F) execute in the
	// streaming Process path; sort-required tests (TEST_KS) force the
	// buffered path. Results land in Response.Tests in the same order.
	Tests []*Test `json:"tests,omitempty"`

	// PostTests is the list of tier-2 statistical tests evaluated after
	// the window stage on the materialized result row set. Typical uses:
	// ANOVA across grouper buckets, Tukey HSD post-hoc on per-group
	// means, trend tests over windowed series. Always buffered (the
	// result row set must be materialized before tier-2 runs). Results
	// land in Response.PostTests in the same order.
	PostTests []*Test `json:"post_tests,omitempty"`

	// Regressions is the list of regression-modeling operators (REG_OLS,
	// REG_GLM, REG_BAYES_LINEAR) evaluated against the filtered record
	// set. Each spec produces one RegressionResult in Response.Regressions
	// in matching order. Streamability follows RegressionSpec.Streamable
	// — closed-form OLS/Bayes stream over sufficient statistics; GLM,
	// Resample, and Selection variants force the buffered path.
	Regressions []*RegressionSpec `json:"regressions,omitempty"`

	// Joins describes pushdown hash-join legs attached to the primary
	// cohort. v1 supports exactly one inner join per Request; multi-
	// join chains and the left/outer/anti kinds land in a follow-up.
	// When set, the orchestrator opens each Right cohort, builds a
	// hash table over the smaller side's join keys, and streams the
	// larger side as the probe — joined records flow through the
	// standard filter / attribute / group / aggregator pipeline.
	Joins []*JoinSpec `json:"joins,omitempty"`

	// Labels rewrites or augments categorical output values using
	// embedder-registered label tables. Each binding pairs a
	// categorical field with a label-table name and a mode (replace
	// or augment). Labels are display-only: filters, formulas, sort
	// keys, and group keys still see raw values. Tables are sourced
	// from pulse.Options.Extensions.LabelTables.
	Labels []*LabelBinding `json:"labels,omitempty"`

	// Crosstab is an optional cross-tabulation directive. When set,
	// the engine composes the cell aggregation across the row × column
	// grouper grid, computes the requested margins via sibling Compose
	// requests, applies the configured normalization, and returns the
	// result either as a structured matrix on Response.Crosstab
	// (shape=matrix, default) or as long-form rows on Response.Data
	// (shape=long). Mutually exclusive with top-level Groups /
	// Aggregations — see PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS.
	Crosstab *CrosstabSpec `json:"crosstab,omitempty"`

	// Overlays is the list of overlay-layer specifications that
	// decorate the primary result with derived projections — index-
	// vs-margin scores, sibling comparisons, baseline lifts, etc.
	// Each spec produces one OverlayLayer in Response.Overlays in
	// matching order. Per-kind semantics, required Ref fields, and
	// scope compatibility live in the overlay catalog (see
	// types/overlay.go).
	Overlays []OverlaySpec `json:"overlays,omitempty"`

	// DisableComponents overrides the engine-level
	// pulse.Options.DisableComponents setting for this single request.
	// nil (the default) inherits the engine setting. Explicit `true`
	// suppresses Response.Components regardless of the engine default;
	// explicit `false` forces components on regardless of the engine
	// default. Use a pointer so the unset state is distinguishable from
	// `false` — the standard tri-state override pattern.
	//
	// When the resolved decision is "disabled", Response.Components stays
	// nil and the wire form is byte-identical to the pre-Components
	// baseline; format_version is not bumped. The gate sits at the
	// processor's attach helpers, so the MetaAggregator.Components and
	// MetaGrouper.Components construction work is skipped — not
	// built-then-discarded.
	DisableComponents *bool `json:"disable_components,omitempty"`
}

// ResponseMetadata holds metadata about a processing result.
type ResponseMetadata struct {
	// TotalRows is the total number of records in the cohort.
	TotalRows int64 `json:"total_rows"`

	// FilteredRows is the number of records after filtering.
	FilteredRows int64 `json:"filtered_rows"`

	// CohortFile is the filename of the processed cohort.
	CohortFile string `json:"cohort_file,omitempty"`
}

// ResponseWarning is the envelope-ready projection of a cross-cutting
// diagnostic emitted during a processing run (today: label-display
// resolver warnings). Codes match the errors.Code namespace so a
// CLI / MCP envelope wrapper can promote them verbatim.
type ResponseWarning struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Response is the processing result type.
type Response struct {
	// Data holds the result rows as key-value maps.
	Data []map[string]any `json:"data,omitempty"`

	// Metadata holds information about the processing run.
	Metadata *ResponseMetadata `json:"metadata,omitempty"`

	// Tests holds tier-1 statistical test results, one per entry in
	// Request.Tests and in the same order.
	Tests []*TestResult `json:"tests,omitempty"`

	// PostTests holds tier-2 statistical test results, one per entry in
	// Request.PostTests and in the same order.
	PostTests []*TestResult `json:"post_tests,omitempty"`

	// Regressions holds the per-spec regression fits, one entry per
	// Request.Regressions in the same order. Engines never partially
	// populate a result on failure; a failed fit surfaces as a
	// PROCESSING_REGRESSION_* error on the envelope instead.
	Regressions []*RegressionResult `json:"regressions,omitempty"`

	// Warnings carries cross-cutting diagnostics surfaced after the
	// processor finishes. Today this slot is populated by the label-
	// display resolver (PULSE_LABEL_COLLISION, PULSE_LABEL_LOOKUP_MISS);
	// future runtime overlays attach here too. CLI / MCP envelope
	// wrappers promote each entry into the envelope's Warnings array.
	Warnings []*ResponseWarning `json:"warnings,omitempty"`

	// Crosstab carries the structured matrix payload when the originating
	// Request set Crosstab and Shape=matrix (the default). Nil otherwise.
	// For Shape=long the cell rows land on Data verbatim — the existing
	// grouped-tuple format — so any consumer of long-form rows keeps
	// working unchanged.
	Crosstab *CrosstabResult `json:"crosstab,omitempty"`

	// Overlays carries the executed overlay layers, one entry per
	// Request.Overlays spec in matching order. Each layer holds its
	// derived payload (scalar / series / matrix) plus optional
	// renderer-friendly summary metadata. Omitted entirely when the
	// originating Request had no overlays.
	Overlays []OverlayLayer `json:"overlays,omitempty"`

	// Components carries the constituent-parts metadata emitted by each
	// operator during a processing run — per-aggregation N / NNull,
	// per-grouper bucket inventory, per-filterer N-in / N-out, crosstab
	// cell components, and run-wide totals.
	//
	// Naming-collision note: Response.Metadata (above) holds runtime
	// facts about the orchestrator pass — total / filtered row counts,
	// cohort filename — that are produced by service-layer wiring and
	// are independent of any specific operator. Response.Components
	// holds the computational components emitted BY the operators
	// themselves (the AGG_*, GROUP_*, FILTER_*, crosstab pipeline) and
	// keyed back to their slot identity. The two are deliberately
	// distinct slots: Metadata is "what the run did", Components is
	// "what each operator contributed". Consumers wanting both pull
	// from both slots; renderer-friendly summaries (cell components for
	// the parity overlays, etc.) live on Components.
	//
	// Additive omitempty contract: the slot is `*ResponseComponents` so
	// an unpopulated run marshals to no `components` key at all —
	// byte-identical to the pre-Components wire form. Every nested
	// slice / pointer / map is `omitempty` for the same reason; an
	// AGG-only run with no groupers omits `groupers`, a no-filter run
	// omits `filterers`, etc. format_version stays at "1.0" because
	// the slot is additive.
	Components *ResponseComponents `json:"components,omitempty"`
}

// ResponseComponents carries the constituent-parts metadata emitted by
// each operator slot during a processing run. Every nested field is
// `omitempty` so partially populated runs (an AGG-only request, a
// filter-free request, etc.) marshal to byte-identical wire output
// against the pre-Components baseline. See Response.Components for the
// runtime-facts vs computational-components naming-collision note.
//
// Field-level semantics:
//   - Aggregations: one entry per Request.Aggregations spec in matching
//     declared order. Slot identity rides on Label (mirrors
//     Aggregation.Label). The universal floor (N, NNull) is a typed
//     field on every entry; per-operator keys ride inside Operator.
//   - Groupers: one entry per Request.Groups spec in matching declared
//     order. Slot identity rides on Field. Per-grouper schema (bucket
//     edges, dict mappings, etc.) rides inside Operator.
//   - Crosstab: populated when the originating Request set Crosstab.
//   - Filterers: one entry per Request.Filterers spec in matching
//     declared order. Slot identity rides on Label.
//   - Run: optional run-wide totals (cohort record count, filtered
//     count, null count, shard count, partial-cohort reason). Distinct
//     from Response.Metadata which holds orchestrator-pass facts.
type ResponseComponents struct {
	// Aggregations carries one AggregationComponents entry per
	// Request.Aggregations slot in matching declared order.
	Aggregations []AggregationComponents `json:"aggregations,omitempty"`

	// Groupers carries one GrouperComponents entry per Request.Groups
	// slot in matching declared order.
	Groupers []GrouperComponents `json:"groupers,omitempty"`

	// Crosstab carries the crosstab-level components (cell matrix +
	// margins). Populated only when the originating Request set
	// Crosstab; nil otherwise.
	Crosstab *CrosstabComponents `json:"crosstab,omitempty"`

	// Filterers carries one FiltererComponents entry per
	// Request.Filterers slot in matching declared order.
	Filterers []FiltererComponents `json:"filterers,omitempty"`

	// Run carries run-wide totals — cohort record count, filtered
	// count, null count, shard count, partial-cohort reason. Distinct
	// from Response.Metadata which holds orchestrator-pass facts about
	// the request execution (file name, etc.).
	Run *RunComponents `json:"run,omitempty"`
}

// AggregationComponents carries per-aggregator constituent-parts
// metadata. The universal floor — N (records observed) and NNull
// (records skipped due to null input) — is a typed field on every
// entry; per-operator keys (mean / variance / mode_count / range_min,
// etc.) ride inside Operator. Slot identity rides on Label, which
// mirrors the originating Aggregation.Label so consumers can join the
// components entry back to the request slot without index arithmetic.
type AggregationComponents struct {
	// Label mirrors the originating Aggregation.Label so callers can
	// join the components entry back to its request slot.
	Label string `json:"label,omitempty"`

	// N is the number of input records observed by the aggregator
	// (post-filter, pre-null-skip). Universal floor — emitted by every
	// aggregator.
	N int `json:"n"`

	// NNull is the number of records the aggregator skipped because
	// the source field was null. Universal floor — emitted by every
	// aggregator.
	NNull int `json:"n_null"`

	// Operator carries the per-aggregator schema-declared keys. Key
	// set is governed by the operator's ComponentSchema declaration in
	// descriptor/capabilities_aggregators.go. Values are JSON-compatible
	// scalars or nested maps.
	Operator map[string]any `json:"operator,omitempty"`
}

// GrouperComponents carries per-grouper constituent-parts metadata —
// bucket inventory, edge values, dictionary mappings. The universal
// floor — TotalN (records partitioned, post-filter) and NNull (records
// that landed in a null / skip path) — is a typed field on every
// entry; per-operator keys (bucket arrays, edges, dict size,
// granularity, etc.) ride inside Operator. Slot identity rides on
// Field, which mirrors the originating Group.Field, plus Label, which
// mirrors the originating Group.Label when present so consumers can
// join the components entry back to its request slot without index
// arithmetic.
//
// For single-key groupers (CATEGORY / RANGE / ROUNDED / QUANTILE /
// DATE / SET_VALUE), TotalN equals the sum of bucket counts. For
// multi-key streaming groupers (GROUP_SET_PER_ELEMENT), a single
// record may contribute to multiple buckets, so the sum of bucket
// counts will exceed TotalN — TotalN remains row count, not emission
// count.
type GrouperComponents struct {
	// Field mirrors the originating Group.Field so callers can join
	// the components entry back to its request slot.
	Field string `json:"field,omitempty"`

	// TotalN is the number of records partitioned by the grouper
	// (post-filter). Universal floor — emitted by every grouper.
	// Zero-valued ints stay absent from JSON via omitempty so a
	// grouper entry with only operator keys does not leak a noisy
	// `total_n: 0` onto the wire.
	TotalN int `json:"total_n,omitempty"`

	// NNull is the number of records that landed in a null / skip
	// path (null field value, empty set, missing key). Universal
	// floor — emitted by every grouper. Zero-valued ints stay absent
	// from JSON via omitempty.
	NNull int `json:"n_null,omitempty"`

	// Operator carries the per-grouper schema-declared keys (bucket
	// edges, dict mappings, range_min / range_max, etc.). Key set is
	// governed by the operator's ComponentSchema declaration in
	// descriptor/capabilities_groupers.go.
	Operator map[string]any `json:"operator,omitempty"`

	// Label mirrors the originating Group.Label so callers can join
	// the components entry back to its request slot when multiple
	// groupers share the same Field.
	Label string `json:"label,omitempty"`
}

// CrosstabComponents is declared in types/crosstab.go alongside
// MatrixPayload. The struct carries the per-cell components matrix,
// per-axis margin counts + components, grand-total counts +
// components, axis-key grouper components, and include / exclude
// record sanity counters — mirroring MatrixPayload coordinate-for-
// coordinate so consumers can index components by the same (r, c).

// FiltererComponents carries per-filterer constituent-parts metadata —
// records in (post-prior-stage), records out (post-this-filter), and
// records dropped due to null input on the filter field. Slot identity
// rides on Label, which mirrors the originating Filterer.Label so
// consumers can join the components entry back to its request slot.
type FiltererComponents struct {
	// Label mirrors the originating Filterer.Label so callers can join
	// the components entry back to its request slot.
	Label string `json:"label,omitempty"`

	// NIn is the number of records the filterer observed (post-prior-
	// stage, pre-this-filter).
	NIn int `json:"n_in"`

	// NOut is the number of records the filterer admitted to the next
	// stage (post-this-filter).
	NOut int `json:"n_out"`

	// NNullInput is the number of records the filterer dropped because
	// the source field was null.
	NNullInput int `json:"n_null_input"`
}

// RunComponents carries run-wide constituent-parts metadata — cohort
// totals, filtered counts, null counts, shard count, partial-cohort
// reason. Distinct from Response.Metadata which holds orchestrator-
// pass facts (cohort filename, etc.); RunComponents is the
// computational-component view of the same run.
//
// Metadata-vs-Run coexistence (see CLAUDE.md Output Format Contract):
// Response.Metadata.TotalRows and Components.Run.TotalRecords carry the
// same value by construction at every orchestrator exit. Metadata
// retains the non-numerical run facts (cohort filename); Run carries
// the typed counters consumers compute against (joins, ratios,
// partial-cohort diagnostics). Both slots are populated on every
// successful Process call so consumers can rely on either side.
type RunComponents struct {
	// TotalRecords is the total number of records in the underlying
	// cohort (pre-filter).
	TotalRecords int64 `json:"total_records"`

	// FilteredRecords is the number of records that survived the
	// filter chain.
	FilteredRecords int64 `json:"filtered_records"`

	// NullRecords is the number of records dropped due to null input
	// somewhere in the pipeline.
	NullRecords int64 `json:"null_records"`

	// ShardCount is the number of shards processed when the cohort
	// resolved to a shard archive. Zero for single-file cohorts.
	ShardCount int `json:"shard_count,omitempty"`

	// PartialCohortReason carries a free-form diagnostic explaining
	// why the run consumed less than the full cohort (e.g. a shard
	// failed to open). Empty when the run consumed the full cohort.
	PartialCohortReason string `json:"partial_cohort_reason,omitempty"`
}

// FileRequest identifies a file for operations like inspect.
type FileRequest struct {
	// Filename is the name of the file.
	Filename string `json:"filename"`

	// DataDir is the directory containing the file.
	DataDir string `json:"data_dir,omitempty"`
}

// FileResponse describes a file's metadata.
type FileResponse struct {
	// Filename is the name of the file.
	Filename string `json:"filename"`

	// RecordCount is the number of records in the file.
	RecordCount int64 `json:"record_count"`

	// Fields is the list of field names in the file.
	Fields []string `json:"fields,omitempty"`
}

// ComposedRequest bundles multiple requests for batch execution.
type ComposedRequest struct {
	// Requests is the list of individual requests to execute.
	Requests []*Request `json:"requests"`

	// Overlays is the optional list of Compose-only overlay layer
	// specifications. Each spec is a cross-Request overlay that
	// decorates one slot's result with a comparison against another
	// slot's result inside the same ComposedRequest — the slots are
	// addressed by their per-Request Label field (with empty Labels
	// auto-defaulted to `request_<index+1>` before reference lookup,
	// before reference lookup). Produces one OverlayLayer in
	// ComposedResponse.Overlays in matching index order.
	//
	// Forward-compat: a ComposedRequest without overlays produces
	// byte-identical JSON to the same request with `Overlays: nil`
	// because the slot is `omitempty` — nil / empty slices marshal to
	// no key at all. The canonical-hash routine inherits that contract
	// via the data-driven JSON walk in types/hash.go (see
	// TestComposedRequest_OverlayFreeByteIdentity in
	// types/hash_test.go).
	Overlays []ComposeOverlaySpec `json:"overlays,omitempty"`
}

// ComposedResponse is the structured response shape for ComposedRequest
// execution. It carries the per-slot Response objects emitted by
// service.Compose / service.ComposeParallel alongside the
// Compose-level Overlays slice — one OverlayLayer per
// ComposedRequest.Overlays spec in matching index order.
//
// Scope note: the type lives here so the request-side
// ComposeOverlaySpec catalog has a typed response sibling to write
// against. The pulse.Pulse.Compose facade now returns
// *ComposedResponse and the runtime populates the Overlays slot here
// at the post-slot barrier.
//
// Forward-compat: every ComposedResponse marshalled before any overlay
// landed produces byte-identical JSON to the same shape with
// `Overlays: nil` because the slot is `omitempty` — nil / empty slices
// marshal to no key at all. See TestComposedResponse_OverlayFreeByteIdentical
// in types/types_test.go for the lock.
type ComposedResponse struct {
	// Responses is the per-slot list of Response objects, one entry per
	// ComposedRequest.Requests slot in matching order.
	Responses []*Response `json:"responses"`

	// Overlays carries the executed Compose-level overlay layers, one
	// entry per ComposedRequest.Overlays spec in matching order. Each
	// layer holds its derived payload (scalar / series / matrix) plus
	// optional renderer-friendly summary metadata — reuses the
	// OverlayLayer shape so renderers handle Compose layers and
	// per-Request layers with the same machinery. Omitted entirely when
	// the originating ComposedRequest had no Overlays.
	Overlays []OverlayLayer `json:"overlays,omitempty"`
}

// VersionResponse provides build and version information.
type VersionResponse struct {
	// Version is the semantic version string.
	Version string `json:"version"`

	// BuildDate is the build timestamp.
	BuildDate string `json:"build_date,omitempty"`

	// GoVersion is the Go compiler version used.
	GoVersion string `json:"go_version,omitempty"`
}
