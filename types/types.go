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
)

// AllAggregationTypes returns all defined aggregation types.
func AllAggregationTypes() []AggregationType {
	return []AggregationType{
		AGG_COUNT, AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX,
		AGG_STDDEV, AGG_RANGE, AGG_FREQUENCY, AGG_ZSCORE,
		AGG_MEDIAN, AGG_VARIANCE, AGG_MODE, AGG_SKEWNESS, AGG_KURTOSIS,
		AGG_DISTINCT_COUNT, AGG_PERCENTILE,
	}
}

// FiltererType identifies a specific filter operation.
type FiltererType string

const (
	FILTER_INCLUDE    FiltererType = "FILTER_INCLUDE"
	FILTER_EXCLUDE    FiltererType = "FILTER_EXCLUDE"
	FILTER_RANGE      FiltererType = "FILTER_RANGE"
	FILTER_EXPRESSION FiltererType = "FILTER_EXPRESSION"
)

// AllFiltererTypes returns all defined filterer types.
func AllFiltererTypes() []FiltererType {
	return []FiltererType{
		FILTER_INCLUDE, FILTER_EXCLUDE, FILTER_RANGE, FILTER_EXPRESSION,
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
)

// AllGroupTypes returns all defined group types.
func AllGroupTypes() []GroupType {
	return []GroupType{GROUP_CATEGORY, GROUP_DATE, GROUP_QUANTILE, GROUP_RANGE, GROUP_ROUNDED}
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
