package errors

// Code is a typed string representing categorical error codes.
// Each code identifies a specific error category within a domain.
type Code string

// ENCODING domain - Binary format and data encoding operations
const (
	// ENCODING_INVALID indicates invalid data format or structure.
	ENCODING_INVALID Code = "ENCODING_INVALID"

	// ENCODING_IO indicates I/O failures during read/write operations.
	ENCODING_IO Code = "ENCODING_IO"

	// ENCODING_TYPE_MISMATCH indicates type conversion or casting errors.
	ENCODING_TYPE_MISMATCH Code = "ENCODING_TYPE_MISMATCH"

	// ENCODING_INTERNAL indicates unexpected errors in encoding layer.
	ENCODING_INTERNAL Code = "ENCODING_INTERNAL"
)

// PROCESSING domain - Processing engine and pipeline operations
const (
	// PROCESSING_CONFIG indicates component configuration errors.
	PROCESSING_CONFIG Code = "PROCESSING_CONFIG"

	// PROCESSING_STATE indicates context state management errors.
	PROCESSING_STATE Code = "PROCESSING_STATE"

	// PROCESSING_RUNTIME indicates runtime execution errors.
	PROCESSING_RUNTIME Code = "PROCESSING_RUNTIME"

	// PROCESSING_GROUP indicates group-related processing errors.
	PROCESSING_GROUP Code = "PROCESSING_GROUP"

	// PROCESSING_INTERNAL indicates unexpected errors in processing layer.
	PROCESSING_INTERNAL Code = "PROCESSING_INTERNAL"
)

// SERVICE domain - HTTP/API layer and service operations
const (
	// SERVICE_VALIDATION indicates request validation failures.
	SERVICE_VALIDATION Code = "SERVICE_VALIDATION"

	// SERVICE_RESOURCE indicates resource loading or access failures.
	SERVICE_RESOURCE Code = "SERVICE_RESOURCE"

	// SERVICE_REGISTRY indicates registry lookup failures.
	SERVICE_REGISTRY Code = "SERVICE_REGISTRY"

	// SERVICE_INTERNAL indicates unexpected errors in service layer.
	SERVICE_INTERNAL Code = "SERVICE_INTERNAL"
)

// DATA domain - Data file and dataset management operations
const (
	// DATA_FILE indicates file access or format errors.
	DATA_FILE Code = "DATA_FILE"

	// DATA_PARSE indicates data parsing or deserialization errors.
	DATA_PARSE Code = "DATA_PARSE"

	// DATA_CONFIG indicates data configuration errors.
	DATA_CONFIG Code = "DATA_CONFIG"

	// DATA_CALCULATION indicates errors during data field access or calculation.
	DATA_CALCULATION Code = "DATA_CALCULATION"

	// DATA_INTERNAL indicates unexpected errors in data layer.
	DATA_INTERNAL Code = "DATA_INTERNAL"
)

// CLI domain - Command-line interface operations
const (
	// CLI_INPUT indicates command input or argument errors.
	CLI_INPUT Code = "CLI_INPUT"

	// CLI_OUTPUT indicates output generation or file write errors.
	CLI_OUTPUT Code = "CLI_OUTPUT"

	// CLI_COMMAND indicates command execution errors.
	CLI_COMMAND Code = "CLI_COMMAND"

	// CLI_INTERNAL indicates unexpected errors in CLI layer.
	CLI_INTERNAL Code = "CLI_INTERNAL"
)

// PULSE domain - Pulse-specific error codes for I/O pipelines,
// categorical handling, description validation, and aggregation warnings.
const (
	// PULSE_IMPORT_SCHEMA_AMBIGUOUS indicates type ambiguity during schema inference.
	PULSE_IMPORT_SCHEMA_AMBIGUOUS Code = "PULSE_IMPORT_SCHEMA_AMBIGUOUS"

	// PULSE_IMPORT_ROW_ERROR indicates a per-row import error.
	PULSE_IMPORT_ROW_ERROR Code = "PULSE_IMPORT_ROW_ERROR"

	// PULSE_EXPORT_ROW_ERROR indicates a per-row export error.
	PULSE_EXPORT_ROW_ERROR Code = "PULSE_EXPORT_ROW_ERROR"

	// PULSE_IMPORT_CATEGORICAL_OVERFLOW indicates dictionary exceeds width capacity.
	PULSE_IMPORT_CATEGORICAL_OVERFLOW Code = "PULSE_IMPORT_CATEGORICAL_OVERFLOW"

	// PULSE_IMPORT_CATEGORICAL_UNBOUNDED indicates sample suggests unbounded cardinality.
	PULSE_IMPORT_CATEGORICAL_UNBOUNDED Code = "PULSE_IMPORT_CATEGORICAL_UNBOUNDED"

	// PULSE_IMPORT_DESCRIPTION_TOO_LONG indicates description exceeds 1000 bytes.
	PULSE_IMPORT_DESCRIPTION_TOO_LONG Code = "PULSE_IMPORT_DESCRIPTION_TOO_LONG"

	// PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL indicates a numeric aggregation
	// was requested on a categorical field.
	PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL Code = "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL"

	// PULSE_FIELD_DESCRIPTION_LOW_QUALITY indicates a field description quality warning.
	PULSE_FIELD_DESCRIPTION_LOW_QUALITY Code = "PULSE_FIELD_DESCRIPTION_LOW_QUALITY"

	// PULSE_WINDOW_INVALID indicates a structural validation failure for a
	// window operation: invalid frame matrix, alpha out of bounds,
	// non-orderable order key, label collision, or unsupported window type.
	PULSE_WINDOW_INVALID Code = "PULSE_WINDOW_INVALID"

	// PULSE_FEAT_TARGET_LEAKAGE_RISK indicates that FEAT_TARGET_ENCODE was
	// requested without a prior FEAT_TRAIN_TEST_SPLIT in the same Features
	// list. The encoded values include rows that should belong to the
	// validation/test partitions, leaking target information into the
	// training feature. Mitigation: place a FEAT_TRAIN_TEST_SPLIT operator
	// before any FEAT_TARGET_ENCODE.
	PULSE_FEAT_TARGET_LEAKAGE_RISK Code = "PULSE_FEAT_TARGET_LEAKAGE_RISK"

	// PULSE_DECIMAL_OVERFLOW indicates a decimal arithmetic or aggregation
	// result exceeds the decimal128(38) representable range.
	PULSE_DECIMAL_OVERFLOW Code = "PULSE_DECIMAL_OVERFLOW"

	// PULSE_DECIMAL_PRECISION_LOSS warns that a decimal aggregation fell
	// back to f64 because intermediate state would have overflowed
	// decimal128(38).
	PULSE_DECIMAL_PRECISION_LOSS Code = "PULSE_DECIMAL_PRECISION_LOSS"

	// PULSE_DECIMAL_DIVIDE_BY_ZERO indicates a decimal divide operation
	// with a zero divisor.
	PULSE_DECIMAL_DIVIDE_BY_ZERO Code = "PULSE_DECIMAL_DIVIDE_BY_ZERO"

	// PULSE_GEO_INVALID_POINT indicates a malformed point parse or a
	// latitude/longitude out of range (|lat|>90 or |lon|>180).
	PULSE_GEO_INVALID_POINT Code = "PULSE_GEO_INVALID_POINT"

	// PULSE_GEO_INVALID_POLYGON indicates a WKT POLYGON parse failure or
	// a non-closed ring.
	PULSE_GEO_INVALID_POLYGON Code = "PULSE_GEO_INVALID_POLYGON"

	// PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS indicates an AGG_GEO_BBOX input set
	// that crosses the 180/-180 meridian, where a flat min/max bbox is
	// ambiguous.
	PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS Code = "PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS"

	// PULSE_GEO_INVALID_RESOLUTION indicates an H3 resolution parameter
	// that is out of range (not 0–15) or finer than a cell's native
	// resolution when walking parents.
	PULSE_GEO_INVALID_RESOLUTION Code = "PULSE_GEO_INVALID_RESOLUTION"

	// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL is a predict warning emitted
	// when an aggregation has no defined semantics on a decimal128 field
	// (e.g., AGG_MEDIAN, AGG_PERCENTILE in v1).
	PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL Code = "PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL"

	// PULSE_AGG_NOT_MEANINGFUL_FOR_GEO is a predict warning emitted when
	// a numeric aggregator is requested on a geospatial field type
	// (point_f64 or h3_cell).
	PULSE_AGG_NOT_MEANINGFUL_FOR_GEO Code = "PULSE_AGG_NOT_MEANINGFUL_FOR_GEO"

	// PULSE_SYNTH_DISTRIBUTION_UNKNOWN indicates a synth spec referenced
	// a distribution kind not registered in the synth package.
	PULSE_SYNTH_DISTRIBUTION_UNKNOWN Code = "PULSE_SYNTH_DISTRIBUTION_UNKNOWN"

	// PULSE_SYNTH_CONSTRAINT_INFEASIBLE indicates that rejection sampling
	// for declared constraints exceeded the allowed rejection rate (50%
	// by default), so the generator gives up rather than produce biased
	// or truncated output.
	PULSE_SYNTH_CONSTRAINT_INFEASIBLE Code = "PULSE_SYNTH_CONSTRAINT_INFEASIBLE"

	// PULSE_PROFILE_FIELD_UNSUPPORTED indicates a field type the profile
	// layer cannot summarize (e.g. point_f64 / h3_cell). The field is
	// skipped with a warning rather than failing the whole profile.
	PULSE_PROFILE_FIELD_UNSUPPORTED Code = "PULSE_PROFILE_FIELD_UNSUPPORTED"
)

// allCodes is the authoritative registry of every defined error code.
// Update this slice whenever a new code is added.
var allCodes = []Code{
	// ENCODING
	ENCODING_INVALID,
	ENCODING_IO,
	ENCODING_TYPE_MISMATCH,
	ENCODING_INTERNAL,
	// PROCESSING
	PROCESSING_CONFIG,
	PROCESSING_STATE,
	PROCESSING_RUNTIME,
	PROCESSING_GROUP,
	PROCESSING_INTERNAL,
	// SERVICE
	SERVICE_VALIDATION,
	SERVICE_RESOURCE,
	SERVICE_REGISTRY,
	SERVICE_INTERNAL,
	// DATA
	DATA_FILE,
	DATA_PARSE,
	DATA_CONFIG,
	DATA_CALCULATION,
	DATA_INTERNAL,
	// CLI
	CLI_INPUT,
	CLI_OUTPUT,
	CLI_COMMAND,
	CLI_INTERNAL,
	// PULSE
	PULSE_IMPORT_SCHEMA_AMBIGUOUS,
	PULSE_IMPORT_ROW_ERROR,
	PULSE_EXPORT_ROW_ERROR,
	PULSE_IMPORT_CATEGORICAL_OVERFLOW,
	PULSE_IMPORT_CATEGORICAL_UNBOUNDED,
	PULSE_IMPORT_DESCRIPTION_TOO_LONG,
	PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL,
	PULSE_FIELD_DESCRIPTION_LOW_QUALITY,
	PULSE_WINDOW_INVALID,
	PULSE_FEAT_TARGET_LEAKAGE_RISK,
	PULSE_DECIMAL_OVERFLOW,
	PULSE_DECIMAL_PRECISION_LOSS,
	PULSE_DECIMAL_DIVIDE_BY_ZERO,
	PULSE_GEO_INVALID_POINT,
	PULSE_GEO_INVALID_POLYGON,
	PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS,
	PULSE_GEO_INVALID_RESOLUTION,
	PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL,
	PULSE_AGG_NOT_MEANINGFUL_FOR_GEO,
	PULSE_SYNTH_DISTRIBUTION_UNKNOWN,
	PULSE_SYNTH_CONSTRAINT_INFEASIBLE,
	PULSE_PROFILE_FIELD_UNSUPPORTED,
}

// codeIndex is a lookup table for fast string→Code parsing.
var codeIndex map[string]Code

func init() {
	codeIndex = make(map[string]Code, len(allCodes))
	for _, c := range allCodes {
		codeIndex[string(c)] = c
	}
}

// AllCodes returns a copy of all defined error codes.
func AllCodes() []Code {
	out := make([]Code, len(allCodes))
	copy(out, allCodes)
	return out
}

// ParseCode attempts to parse a string into a known Code.
// Returns the Code and true if found, or the zero value and false otherwise.
func ParseCode(s string) (Code, bool) {
	c, ok := codeIndex[s]
	return c, ok
}
