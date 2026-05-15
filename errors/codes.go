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

	// PROCESSING_REGRESSION_NOT_IMPLEMENTED indicates the request named a
	// regression operator whose engine has not yet shipped. Phase 0
	// stubs return this for every REG_* spec; later phases retire the
	// code as each engine lands.
	PROCESSING_REGRESSION_NOT_IMPLEMENTED Code = "PROCESSING_REGRESSION_NOT_IMPLEMENTED"

	// PROCESSING_REGRESSION_RANK_DEFICIENT indicates the predictor
	// design matrix has collinear columns; XᵀX is singular and the
	// closed-form OLS solve cannot proceed. Add a regularization
	// penalty or drop the redundant predictor.
	PROCESSING_REGRESSION_RANK_DEFICIENT Code = "PROCESSING_REGRESSION_RANK_DEFICIENT"

	// PROCESSING_REGRESSION_NO_CONVERGE indicates an iterative fit
	// (IRLS for REG_GLM, coordinate descent for regularized REG_OLS)
	// failed to converge within MaxIters.
	PROCESSING_REGRESSION_NO_CONVERGE Code = "PROCESSING_REGRESSION_NO_CONVERGE"

	// PROCESSING_REGRESSION_SINGULAR_GRAM indicates XᵀX remained
	// non-invertible even after regularization. Typically caused by a
	// degenerate predictor (all-zero column) or a vanishingly small
	// Alpha.
	PROCESSING_REGRESSION_SINGULAR_GRAM Code = "PROCESSING_REGRESSION_SINGULAR_GRAM"

	// PROCESSING_REGRESSION_INVALID_FAMILY indicates REG_GLM was
	// requested with a Family outside the supported set
	// ({"binomial", "poisson", "gamma"}).
	PROCESSING_REGRESSION_INVALID_FAMILY Code = "PROCESSING_REGRESSION_INVALID_FAMILY"

	// PROCESSING_REGRESSION_INVALID_LINK indicates the requested Link
	// function is incompatible with the chosen Family.
	PROCESSING_REGRESSION_INVALID_LINK Code = "PROCESSING_REGRESSION_INVALID_LINK"

	// PROCESSING_REGRESSION_INSUFFICIENT_DATA indicates the filtered
	// record set has fewer observations than predictors + 1 (the
	// minimum for an identifiable fit).
	PROCESSING_REGRESSION_INSUFFICIENT_DATA Code = "PROCESSING_REGRESSION_INSUFFICIENT_DATA"
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

	// PULSE_IMPORT_FORMAT_UNKNOWN indicates the source extension was not
	// recognised and no explicit format hint was supplied. Surfaced by
	// the imports.Manager on Open.
	PULSE_IMPORT_FORMAT_UNKNOWN Code = "PULSE_IMPORT_FORMAT_UNKNOWN"

	// PULSE_IMPORT_SOURCE_MISSING indicates the source file referenced
	// by an import or by a managed-import sidecar could not be read.
	PULSE_IMPORT_SOURCE_MISSING Code = "PULSE_IMPORT_SOURCE_MISSING"

	// PULSE_IMPORT_HANDLE_EXISTS indicates a managed-import handle of
	// the requested name already exists and Overwrite was not set.
	PULSE_IMPORT_HANDLE_EXISTS Code = "PULSE_IMPORT_HANDLE_EXISTS"

	// PULSE_IMPORT_SOURCE_FORBIDDEN indicates an absolute source path
	// resolved outside the import jail. The Manager confines absolute
	// reads to a configured root (default: process cwd) so an MCP /
	// CLI invocation cannot reach arbitrary files on the host.
	PULSE_IMPORT_SOURCE_FORBIDDEN Code = "PULSE_IMPORT_SOURCE_FORBIDDEN"

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

	// PULSE_TEST_UNKNOWN_TYPE indicates the request referenced a TestType
	// that is not registered in either the row-test or post-test registry.
	PULSE_TEST_UNKNOWN_TYPE Code = "PULSE_TEST_UNKNOWN_TYPE"

	// PULSE_TEST_FIELD_NOT_NUMERIC indicates a test requires a numeric
	// field but the named field resolves to a categorical, geo, or
	// otherwise non-numeric schema type.
	PULSE_TEST_FIELD_NOT_NUMERIC Code = "PULSE_TEST_FIELD_NOT_NUMERIC"

	// PULSE_TEST_INVALID_ALPHA indicates the request's Alpha value lies
	// outside the open interval (0, 1).
	PULSE_TEST_INVALID_ALPHA Code = "PULSE_TEST_INVALID_ALPHA"

	// PULSE_TEST_INSUFFICIENT_N indicates a test received fewer
	// non-null observations than the minimum needed to compute its
	// statistic (typically n < 2 per group, n < df + 1 for parametric
	// tests).
	PULSE_TEST_INSUFFICIENT_N Code = "PULSE_TEST_INSUFFICIENT_N"

	// PULSE_TEST_VARIANCE_ZERO indicates one or more groups have zero
	// sample variance, making the t- or F-statistic undefined.
	PULSE_TEST_VARIANCE_ZERO Code = "PULSE_TEST_VARIANCE_ZERO"

	// PULSE_TEST_SPLIT_GROUPS_LT_2 indicates a two-sample or ANOVA test
	// observed fewer than the required number of distinct groups in the
	// SplitBy field after filtering.
	PULSE_TEST_SPLIT_GROUPS_LT_2 Code = "PULSE_TEST_SPLIT_GROUPS_LT_2"

	// PULSE_TEST_CONTINGENCY_DEGENERATE indicates a chi-square contingency
	// table is empty or has a single row/column, making the test
	// statistic undefined.
	PULSE_TEST_CONTINGENCY_DEGENERATE Code = "PULSE_TEST_CONTINGENCY_DEGENERATE"

	// PULSE_TEST_EXPECTED_COUNT_TOO_LOW warns that a chi-square cell's
	// expected count is below the conventional threshold of 5, making
	// the asymptotic χ² approximation unreliable.
	PULSE_TEST_EXPECTED_COUNT_TOO_LOW Code = "PULSE_TEST_EXPECTED_COUNT_TOO_LOW"

	// PULSE_TEST_FIELD2_NOT_NUMERIC indicates the secondary Field2
	// reference required by a paired or bivariate test (TEST_PAIRED_T,
	// TEST_PEARSON_R) resolves to a non-numeric schema type.
	PULSE_TEST_FIELD2_NOT_NUMERIC Code = "PULSE_TEST_FIELD2_NOT_NUMERIC"

	// PULSE_TEST_SUCCESS_VALUE_MISSING indicates a two-proportion z-test
	// did not supply Params.success — the dictionary value treated as a
	// success on the primary field. Without it, the test cannot decide
	// which category counts as a positive outcome.
	PULSE_TEST_SUCCESS_VALUE_MISSING Code = "PULSE_TEST_SUCCESS_VALUE_MISSING"

	// PULSE_TEST_CORRELATION_UNDEFINED indicates Pearson r encountered
	// a column with zero variance; r and its t-statistic are
	// mathematically undefined in that case.
	PULSE_TEST_CORRELATION_UNDEFINED Code = "PULSE_TEST_CORRELATION_UNDEFINED"

	// PULSE_TEST_PAIRED_LENGTH_MISMATCH indicates a Wilcoxon signed-rank
	// (or future paired test) encountered rows where one of the paired
	// columns is null while the other is present. Drop-pair semantics
	// apply by default (rows with either value missing are skipped);
	// this code surfaces as a warning so the caller knows the effective
	// pair count differs from the input row count.
	PULSE_TEST_PAIRED_LENGTH_MISMATCH Code = "PULSE_TEST_PAIRED_LENGTH_MISMATCH"

	// PULSE_TEST_TIES_DOMINATE warns that a rank-based test observed
	// ties on ≥ 50 % of the input values. The asymptotic normal / chi-
	// square approximation used for the p-value loses accuracy under
	// heavy ties; consider an exact-permutation variant when the count
	// is small.
	PULSE_TEST_TIES_DOMINATE Code = "PULSE_TEST_TIES_DOMINATE"

	// PULSE_TEST_SUBJECT_MISSING indicates a repeated-measures test
	// found at least one subject missing one or more conditions.
	// Default behavior is to drop the incomplete subject(s) and surface
	// the count as a warning; configurable to error under strict mode.
	PULSE_TEST_SUBJECT_MISSING Code = "PULSE_TEST_SUBJECT_MISSING"

	// PULSE_TEST_BALANCED_DESIGN_REQUIRED indicates a repeated-measures
	// test observed unequal cell counts across the condition × subject
	// grid. Type II / III sums-of-squares decompositions for the
	// unbalanced case are not implemented yet; the test fails rather
	// than reporting a biased balanced-design F.
	PULSE_TEST_BALANCED_DESIGN_REQUIRED Code = "PULSE_TEST_BALANCED_DESIGN_REQUIRED"

	// PULSE_TEST_TUKEY_REQUIRES_K_GE_3 indicates a Tukey HSD request on
	// fewer than 3 groups. A standard t-test or two-proportion z is
	// the appropriate alternative for k = 2.
	PULSE_TEST_TUKEY_REQUIRES_K_GE_3 Code = "PULSE_TEST_TUKEY_REQUIRES_K_GE_3"

	// PULSE_TEST_SHAPIRO_N_BOUND warns that a Shapiro-Wilk request
	// observed n above the supported limit (5000). The asymptotic
	// alternative (D'Agostino's K² or Anderson-Darling) is recommended.
	PULSE_TEST_SHAPIRO_N_BOUND Code = "PULSE_TEST_SHAPIRO_N_BOUND"

	// PULSE_TEST_FISHER_R_OR_C_GT_2 indicates a Fisher exact request on
	// a contingency table larger than 2×2. The v1 implementation
	// supports only 2×2; the network algorithm needed for r×c lands
	// later.
	PULSE_TEST_FISHER_R_OR_C_GT_2 Code = "PULSE_TEST_FISHER_R_OR_C_GT_2"

	// PULSE_QUERY_UNRESOLVED indicates the natural-language query
	// parser (internal/query) could not map one or more tokens to an
	// operator, schema field, or bucket within the configured edit-
	// distance budget. Severity depends on context: a query that
	// produced no parseable structure surfaces this as an error; a
	// query that produced a partial request surfaces it as a warning
	// so the caller can still inspect and repair the partial parse.
	PULSE_QUERY_UNRESOLVED Code = "PULSE_QUERY_UNRESOLVED"

	// PULSE_QUERY_AMBIGUOUS indicates a query token matched multiple
	// schema fields at the same Levenshtein distance, or otherwise
	// produced two candidate requests with similar confidence. The
	// parser picks the lexically first match and proceeds; the
	// warning names every candidate so the caller can disambiguate
	// by editing the resolved request.
	PULSE_QUERY_AMBIGUOUS Code = "PULSE_QUERY_AMBIGUOUS"

	// PULSE_EXTENSION_NAME_INVALID indicates an embedder registration
	// name does not match the required pattern
	// <CATEGORY>_<NAMESPACE>_<NAME> with uppercase ASCII segments.
	PULSE_EXTENSION_NAME_INVALID Code = "PULSE_EXTENSION_NAME_INVALID"

	// PULSE_EXTENSION_NAME_RESERVED indicates an embedder registration
	// uses a namespace segment reserved for Pulse internals
	// (BUILTIN / STANDARD / CORE / PULSE).
	PULSE_EXTENSION_NAME_RESERVED Code = "PULSE_EXTENSION_NAME_RESERVED"

	// PULSE_EXTENSION_NAME_COLLISION indicates an embedder registration
	// name matches a built-in operator name within the same category.
	PULSE_EXTENSION_NAME_COLLISION Code = "PULSE_EXTENSION_NAME_COLLISION"

	// PULSE_EXTENSION_DUPLICATE indicates the same operator name was
	// registered more than once within a single pulse.New call (same
	// category).
	PULSE_EXTENSION_DUPLICATE Code = "PULSE_EXTENSION_DUPLICATE"

	// PULSE_EXTENSION_STREAMABLE_MISMATCH indicates a registration
	// declared Streamable=true but its factory returned an instance
	// that does not implement the required streaming interface, or
	// declared Mode=row_local / Mode=two_pass without the corresponding
	// attribute interface.
	PULSE_EXTENSION_STREAMABLE_MISMATCH Code = "PULSE_EXTENSION_STREAMABLE_MISMATCH"

	// PULSE_EXTENSION_FACTORY_PANIC indicates an embedder factory
	// panicked during probe-validation at registration time.
	PULSE_EXTENSION_FACTORY_PANIC Code = "PULSE_EXTENSION_FACTORY_PANIC"

	// PULSE_EXTENSION_PARAM_INVALID indicates a ParamMeta entry on a
	// registration has missing or contradictory fields (empty name,
	// unknown JSONType, Required=true with a non-nil Default).
	PULSE_EXTENSION_PARAM_INVALID Code = "PULSE_EXTENSION_PARAM_INVALID"

	// PULSE_LOOKUP_TABLE_UNKNOWN indicates a runtime expression
	// referenced a lookup table that is not registered on the
	// Service.
	PULSE_LOOKUP_TABLE_UNKNOWN Code = "PULSE_LOOKUP_TABLE_UNKNOWN"

	// PULSE_LOOKUP_MISS indicates a lookup table call provided a key
	// tuple that is not present in the table.
	PULSE_LOOKUP_MISS Code = "PULSE_LOOKUP_MISS"
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
	PROCESSING_REGRESSION_NOT_IMPLEMENTED,
	PROCESSING_REGRESSION_RANK_DEFICIENT,
	PROCESSING_REGRESSION_NO_CONVERGE,
	PROCESSING_REGRESSION_SINGULAR_GRAM,
	PROCESSING_REGRESSION_INVALID_FAMILY,
	PROCESSING_REGRESSION_INVALID_LINK,
	PROCESSING_REGRESSION_INSUFFICIENT_DATA,
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
	PULSE_IMPORT_FORMAT_UNKNOWN,
	PULSE_IMPORT_SOURCE_MISSING,
	PULSE_IMPORT_HANDLE_EXISTS,
	PULSE_IMPORT_SOURCE_FORBIDDEN,
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
	PULSE_TEST_UNKNOWN_TYPE,
	PULSE_TEST_FIELD_NOT_NUMERIC,
	PULSE_TEST_INVALID_ALPHA,
	PULSE_TEST_INSUFFICIENT_N,
	PULSE_TEST_VARIANCE_ZERO,
	PULSE_TEST_SPLIT_GROUPS_LT_2,
	PULSE_TEST_CONTINGENCY_DEGENERATE,
	PULSE_TEST_EXPECTED_COUNT_TOO_LOW,
	PULSE_TEST_FIELD2_NOT_NUMERIC,
	PULSE_TEST_SUCCESS_VALUE_MISSING,
	PULSE_TEST_CORRELATION_UNDEFINED,
	PULSE_TEST_PAIRED_LENGTH_MISMATCH,
	PULSE_TEST_TIES_DOMINATE,
	PULSE_TEST_SUBJECT_MISSING,
	PULSE_TEST_BALANCED_DESIGN_REQUIRED,
	PULSE_TEST_TUKEY_REQUIRES_K_GE_3,
	PULSE_TEST_SHAPIRO_N_BOUND,
	PULSE_TEST_FISHER_R_OR_C_GT_2,
	PULSE_QUERY_UNRESOLVED,
	PULSE_QUERY_AMBIGUOUS,
	PULSE_EXTENSION_NAME_INVALID,
	PULSE_EXTENSION_NAME_RESERVED,
	PULSE_EXTENSION_NAME_COLLISION,
	PULSE_EXTENSION_DUPLICATE,
	PULSE_EXTENSION_STREAMABLE_MISMATCH,
	PULSE_EXTENSION_FACTORY_PANIC,
	PULSE_EXTENSION_PARAM_INVALID,
	PULSE_LOOKUP_TABLE_UNKNOWN,
	PULSE_LOOKUP_MISS,
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
