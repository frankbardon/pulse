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
	AGG_COUNT     AggregationType = "AGG_COUNT"
	AGG_SUM       AggregationType = "AGG_SUM"
	AGG_AVERAGE   AggregationType = "AGG_AVERAGE"
	AGG_MIN       AggregationType = "AGG_MIN"
	AGG_MAX       AggregationType = "AGG_MAX"
	AGG_STDDEV    AggregationType = "AGG_STDDEV"
	AGG_RANGE     AggregationType = "AGG_RANGE"
	AGG_FREQUENCY AggregationType = "AGG_FREQUENCY"
	AGG_ZSCORE    AggregationType = "AGG_ZSCORE"
	AGG_MEDIAN    AggregationType = "AGG_MEDIAN"
	AGG_VARIANCE  AggregationType = "AGG_VARIANCE"
	AGG_MODE      AggregationType = "AGG_MODE"
	AGG_SKEWNESS       AggregationType = "AGG_SKEWNESS"
	AGG_KURTOSIS       AggregationType = "AGG_KURTOSIS"
	AGG_DISTINCT_COUNT AggregationType = "AGG_DISTINCT_COUNT"
	AGG_PERCENTILE     AggregationType = "AGG_PERCENTILE"

	AGG_GEO_CENTROID AggregationType = "AGG_GEO_CENTROID"
	AGG_GEO_BBOX     AggregationType = "AGG_GEO_BBOX"
)

// AllAggregationTypes returns all defined aggregation types.
func AllAggregationTypes() []AggregationType {
	return []AggregationType{
		AGG_COUNT, AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX,
		AGG_STDDEV, AGG_RANGE, AGG_FREQUENCY, AGG_ZSCORE,
		AGG_MEDIAN, AGG_VARIANCE, AGG_MODE, AGG_SKEWNESS, AGG_KURTOSIS,
		AGG_DISTINCT_COUNT, AGG_PERCENTILE,
		AGG_GEO_CENTROID, AGG_GEO_BBOX,
	}
}

// FiltererType identifies a specific filter operation.
type FiltererType string

const (
	FILTER_INCLUDE    FiltererType = "FILTER_INCLUDE"
	FILTER_EXCLUDE    FiltererType = "FILTER_EXCLUDE"
	FILTER_RANGE      FiltererType = "FILTER_RANGE"
	FILTER_EXPRESSION FiltererType = "FILTER_EXPRESSION"

	FILTER_GEO_WITHIN          FiltererType = "FILTER_GEO_WITHIN"
	FILTER_GEO_WITHIN_RADIUS_M FiltererType = "FILTER_GEO_WITHIN_RADIUS_M"
)

// AllFiltererTypes returns all defined filterer types.
func AllFiltererTypes() []FiltererType {
	return []FiltererType{
		FILTER_EXCLUDE,
		FILTER_EXPRESSION,
		FILTER_GEO_WITHIN,
		FILTER_GEO_WITHIN_RADIUS_M,
		FILTER_INCLUDE,
		FILTER_RANGE,
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
	GROUP_H3_CELL  GroupType = "GROUP_H3_CELL"
)

// AllGroupTypes returns all defined group types.
func AllGroupTypes() []GroupType {
	return []GroupType{GROUP_CATEGORY, GROUP_DATE, GROUP_H3_CELL, GROUP_QUANTILE, GROUP_RANGE, GROUP_ROUNDED}
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
)

// AllAttributeTypes returns all defined attribute types.
func AllAttributeTypes() []AttributeType {
	return []AttributeType{
		ATTR_ZSCORE, ATTR_TSCORE, ATTR_NORMALIZED,
		ATTR_FORMULA, ATTR_PERCENTILE,
		ATTR_DATE_PART,
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
	FEAT_LOG               FeatureType = "FEAT_LOG"
	FEAT_SQRT              FeatureType = "FEAT_SQRT"
	FEAT_BUCKETIZE         FeatureType = "FEAT_BUCKETIZE"
	FEAT_ONE_HOT           FeatureType = "FEAT_ONE_HOT"
	FEAT_DATE_FEATURES     FeatureType = "FEAT_DATE_FEATURES"
	FEAT_FREQUENCY_ENCODE  FeatureType = "FEAT_FREQUENCY_ENCODE"
	FEAT_TARGET_ENCODE     FeatureType = "FEAT_TARGET_ENCODE"
	FEAT_TRAIN_TEST_SPLIT  FeatureType = "FEAT_TRAIN_TEST_SPLIT"
)

// AllFeatureTypes returns every defined feature type in alphabetical order.
func AllFeatureTypes() []FeatureType {
	return []FeatureType{
		FEAT_BUCKETIZE,
		FEAT_DATE_FEATURES,
		FEAT_FREQUENCY_ENCODE,
		FEAT_LOG,
		FEAT_ONE_HOT,
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
)

// AllTestTypes returns every defined statistical test type in alphabetical
// order.
func AllTestTypes() []TestType {
	return []TestType{
		TEST_ANOVA_F,
		TEST_CHISQ,
		TEST_KS,
		TEST_T,
		TEST_TREND,
		TEST_TUKEY_HSD,
		TEST_WELCH,
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

	// SplitBy is a categorical field whose distinct values partition Field
	// into groups. Required for two-sample TEST_T / TEST_WELCH and for
	// TEST_ANOVA_F. Two-sample tests expect exactly 2 groups; ANOVA expects
	// ≥ 2.
	SplitBy string `json:"split_by,omitempty"`

	// Rows is the row-axis categorical field for TEST_CHISQ contingency.
	Rows string `json:"rows,omitempty"`

	// Cols is the column-axis categorical field for TEST_CHISQ contingency.
	Cols string `json:"cols,omitempty"`

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
}

// Attribute defines a derived attribute computation.
type Attribute struct {
	// Type is the attribute computation to perform.
	Type AttributeType `json:"type"`

	// Field is the name of the source data field.
	Field string `json:"field"`

	// Label is the output name for the derived attribute.
	Label string `json:"label,omitempty"`

	// Expression is a runtime expression for ATTR_FORMULA type.
	Expression string `json:"expression,omitempty"`

	// Params holds type-specific configuration as raw JSON.
	// Each attribute type defines its own params schema.
	Params json.RawMessage `json:"params,omitempty"`
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
