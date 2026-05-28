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

	// PULSE_EXPORT_FIELD_UNKNOWN indicates an ExportJob.Includes /
	// ConvertJob.Includes entry names a field that does not appear in
	// the source schema. Details carry the offending name plus the
	// list of known field names so the caller can correct the request
	// without re-fetching the schema.
	PULSE_EXPORT_FIELD_UNKNOWN Code = "PULSE_EXPORT_FIELD_UNKNOWN"

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

	// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL is a predict warning emitted
	// when an aggregation has no defined semantics on a decimal128 field
	// (e.g., AGG_MEDIAN, AGG_PERCENTILE in v1).
	PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL Code = "PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL"

	// PULSE_SYNTH_DISTRIBUTION_UNKNOWN indicates a synth spec referenced
	// a distribution kind not registered in the synth package.
	PULSE_SYNTH_DISTRIBUTION_UNKNOWN Code = "PULSE_SYNTH_DISTRIBUTION_UNKNOWN"

	// PULSE_SYNTH_CONSTRAINT_INFEASIBLE indicates that rejection sampling
	// for declared constraints exceeded the allowed rejection rate (50%
	// by default), so the generator gives up rather than produce biased
	// or truncated output.
	PULSE_SYNTH_CONSTRAINT_INFEASIBLE Code = "PULSE_SYNTH_CONSTRAINT_INFEASIBLE"

	// PULSE_PROFILE_FIELD_UNSUPPORTED indicates a field type the profile
	// layer cannot summarize. The field is skipped with a warning rather
	// than failing the whole profile.
	PULSE_PROFILE_FIELD_UNSUPPORTED Code = "PULSE_PROFILE_FIELD_UNSUPPORTED"

	// PULSE_TEST_UNKNOWN_TYPE indicates the request referenced a TestType
	// that is not registered in either the row-test or post-test registry.
	PULSE_TEST_UNKNOWN_TYPE Code = "PULSE_TEST_UNKNOWN_TYPE"

	// PULSE_TEST_FIELD_NOT_NUMERIC indicates a test requires a numeric
	// field but the named field resolves to a categorical or otherwise
	// non-numeric schema type.
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

	// PULSE_ARCHIVE_MAGIC_INVALID indicates a cohort path whose leading
	// bytes match neither the single-file Pulse magic ("PULSE\x00\x00\x00")
	// nor the zip-archive magic (PK\x03\x04). Surfaced by pulse.Open
	// when the file is not a recognised Pulse artifact.
	PULSE_ARCHIVE_MAGIC_INVALID Code = "PULSE_ARCHIVE_MAGIC_INVALID"

	// PULSE_ARCHIVE_CORRUPT indicates a Pulse shard archive whose zip
	// end-of-central-directory record is missing or invalid, or whose
	// central directory cannot be parsed. The archive is unreadable
	// until repaired (typically via re-creation from constituent shards).
	PULSE_ARCHIVE_CORRUPT Code = "PULSE_ARCHIVE_CORRUPT"

	// PULSE_SHARD_MISSING indicates the central directory references an
	// entry that is not addressable inside the archive, or a caller
	// requested a shard by name that does not exist. Distinct from
	// PULSE_ARCHIVE_CORRUPT in that the archive itself is structurally
	// valid; only the named entry is absent.
	PULSE_SHARD_MISSING Code = "PULSE_SHARD_MISSING"

	// PULSE_SHARD_HEADER_INVALID indicates a shard payload inside an
	// archive failed its first-read magic + format-version check. The
	// archive's central directory may be valid while a constituent
	// shard's bytes are not a single-file Pulse cohort.
	PULSE_SHARD_HEADER_INVALID Code = "PULSE_SHARD_HEADER_INVALID"

	// PULSE_SHARD_SCHEMA_MISMATCH indicates an incoming shard's
	// structural schema (field count, per-field name/type/byte_offset/
	// bit_position, categorical width) is not byte-equal to the
	// archive's canonical schema. The insert is rejected; descriptions
	// alone diverging do NOT raise this code (see
	// PULSE_SHARD_DESCRIPTION_DIVERGENCE).
	PULSE_SHARD_SCHEMA_MISMATCH Code = "PULSE_SHARD_SCHEMA_MISMATCH"

	// PULSE_SHARD_DICT_DIVERGENCE indicates an incoming shard's
	// categorical dictionary is not prefix-related to the canonical
	// dictionary on the same field. Pulse permits append-only growth
	// (incoming prefix of canonical, or canonical prefix of incoming
	// — see PULSE_SHARD_DICT_WIDTH_OVERFLOW for the capacity guard),
	// but rejects reorders or new values inserted before existing
	// ones.
	PULSE_SHARD_DICT_DIVERGENCE Code = "PULSE_SHARD_DICT_DIVERGENCE"

	// PULSE_SHARD_DICT_WIDTH_OVERFLOW indicates a categorical
	// dictionary extension that would exceed the declared field
	// width's capacity (256 for u8, 65 536 for u16, 2^32 for u32).
	// The field's width is fixed at folder creation; widening
	// requires rebuilding the archive with a wider categorical type.
	PULSE_SHARD_DICT_WIDTH_OVERFLOW Code = "PULSE_SHARD_DICT_WIDTH_OVERFLOW"

	// PULSE_SHARD_DESCRIPTION_DIVERGENCE is emitted as a WARNING (not
	// an error) when an incoming shard's per-field description differs
	// from the canonical schema's. Descriptions are advisory metadata;
	// the canonical description in `_schema.pulse` wins for any
	// downstream consumer.
	PULSE_SHARD_DESCRIPTION_DIVERGENCE Code = "PULSE_SHARD_DESCRIPTION_DIVERGENCE"

	// PULSE_SHARD_RESERVED_NAME indicates a caller attempted to insert
	// a shard whose basename collides with the reserved canonical
	// schema entry name (`_schema.pulse`). The reserved name is
	// addressable only through the archive's own canonical-schema
	// channel; user shards must pick a different basename.
	PULSE_SHARD_RESERVED_NAME Code = "PULSE_SHARD_RESERVED_NAME"

	// PULSE_SHARD_NAME_COLLISION indicates two shards in the same
	// archive (or two paths handed to `pulse shard create`) share a
	// basename. Zip entry names are flat — basenames must be unique
	// within an archive.
	PULSE_SHARD_NAME_COLLISION Code = "PULSE_SHARD_NAME_COLLISION"

	// PULSE_CHAIN_NOT_MERGEABLE indicates a stage inside a
	// ProcessChain request fails the chain gate. The gate accepts
	// mergeable requests (same set as processing.CanMergeRequest)
	// whose aggregators emit a single scalar per output row. Stages
	// using windows, features, tier-1/tier-2 tests, regressions,
	// two-pass attributes, AGG_FREQUENCY, AGG_MODE, or non-mergeable
	// groupers / aggregators are rejected. The error details carry
	// the offending stage index and name so callers can fall back to
	// per-stage Process calls.
	PULSE_CHAIN_NOT_MERGEABLE Code = "PULSE_CHAIN_NOT_MERGEABLE"

	// PULSE_CHAIN_EMPTY indicates a ProcessChain request with zero
	// stages, or a stage with a nil inner Request. The chain
	// executor needs at least one stage with a real Request to run.
	PULSE_CHAIN_EMPTY Code = "PULSE_CHAIN_EMPTY"

	// PULSE_JOIN_TYPE_MISMATCH indicates an equi-join key pair where
	// the left field's schema type differs from the right field's
	// (e.g. left is u32, right is categorical_u8). Hash join requires
	// type-compatible keys; mismatches surface this code so callers
	// can either cast upstream (during import) or restructure the
	// request.
	PULSE_JOIN_TYPE_MISMATCH Code = "PULSE_JOIN_TYPE_MISMATCH"

	// PULSE_JOIN_KIND_NOT_IMPLEMENTED indicates a join spec whose Kind
	// is reserved but not yet implemented ("left", "outer", "anti").
	// v1 supports "inner" (and empty == "inner"); the remaining kinds
	// land once the null bitmap correctness path is fully wired for
	// outer-join fills.
	PULSE_JOIN_KIND_NOT_IMPLEMENTED Code = "PULSE_JOIN_KIND_NOT_IMPLEMENTED"

	// PULSE_JOIN_FIELD_UNKNOWN indicates an OnPair references a field
	// not present in the corresponding cohort's schema. Surfaced by
	// descriptor.ValidateJoin and at runtime by the join orchestrator.
	PULSE_JOIN_FIELD_UNKNOWN Code = "PULSE_JOIN_FIELD_UNKNOWN"

	// PULSE_JOIN_KEYS_EMPTY indicates a join spec with an empty On
	// slice. At least one equi-join pair is required.
	PULSE_JOIN_KEYS_EMPTY Code = "PULSE_JOIN_KEYS_EMPTY"

	// PULSE_JOIN_TOO_MANY indicates a Request with more than one
	// JoinSpec. v1 supports exactly one join per Request; multi-join
	// chains land when the orchestrator gains a per-join intermediate
	// state machine.
	PULSE_JOIN_TOO_MANY Code = "PULSE_JOIN_TOO_MANY"

	// PULSE_JOIN_FIELD_COLLISION indicates the joined schema would
	// carry two fields with the same name (left field + right field
	// without an As prefix to disambiguate). Set JoinSpec.As to add
	// a per-field prefix on the right side, or rename one side at
	// import time.
	PULSE_JOIN_FIELD_COLLISION Code = "PULSE_JOIN_FIELD_COLLISION"

	// PULSE_LABEL_FIELD_UNKNOWN indicates a LabelBinding references a
	// field name not present in the cohort schema.
	PULSE_LABEL_FIELD_UNKNOWN Code = "PULSE_LABEL_FIELD_UNKNOWN"

	// PULSE_LABEL_FIELD_NOT_CATEGORICAL indicates a LabelBinding
	// references a schema field whose type is not one of
	// categorical_u8 / categorical_u16 / categorical_u32. Labels
	// translate dictionary string values; other types have no
	// dictionary key to translate.
	PULSE_LABEL_FIELD_NOT_CATEGORICAL Code = "PULSE_LABEL_FIELD_NOT_CATEGORICAL"

	// PULSE_LABEL_TABLE_UNKNOWN indicates a LabelBinding references a
	// label-table name that is not registered on the Service. Tables
	// are populated by pulse.Options.Extensions.LabelTables.
	PULSE_LABEL_TABLE_UNKNOWN Code = "PULSE_LABEL_TABLE_UNKNOWN"

	// PULSE_LABEL_FIELD_COLLISION indicates a LabelBinding in augment
	// mode would emit a sibling "<field>_label" column whose name
	// already exists in the request's output schema (an existing
	// schema field or a prior augment binding). The augment suffix is
	// fixed; resolve by renaming one of the colliding sources or
	// switching to replace mode.
	PULSE_LABEL_FIELD_COLLISION Code = "PULSE_LABEL_FIELD_COLLISION"

	// PULSE_LABEL_DUPLICATE_BINDING indicates two LabelBinding
	// entries in the same request target the same Field. Bindings
	// must be unique per field within a request.
	PULSE_LABEL_DUPLICATE_BINDING Code = "PULSE_LABEL_DUPLICATE_BINDING"

	// PULSE_LABEL_TABLE_NOT_ENUMERABLE indicates a reverse lookup
	// (ResolveLabel / pulse_label_resolve) was attempted against a
	// function-driven label table that exposes only a Lookup closure
	// and no static Rows map. Reverse search requires enumerable Rows.
	PULSE_LABEL_TABLE_NOT_ENUMERABLE Code = "PULSE_LABEL_TABLE_NOT_ENUMERABLE"

	// PULSE_LABEL_COLLISION is a warning emitted in replace mode when
	// two distinct source values resolve to the same label string
	// (e.g. legacy and current ISO country codes both mapping to
	// "United States"). The output disambiguates by appending the
	// source value in parentheses ("United States (US)"); the warning
	// names the affected source values so callers can clean the
	// table or switch to augment mode.
	PULSE_LABEL_COLLISION Code = "PULSE_LABEL_COLLISION"

	// PULSE_LABEL_LOOKUP_MISS is a warning emitted when one or more
	// categorical values present in the data have no entry in the
	// label table. The output falls back to the raw resolved
	// categorical value; the warning summarises the count of
	// unresolved values per field so callers can audit gaps.
	PULSE_LABEL_LOOKUP_MISS Code = "PULSE_LABEL_LOOKUP_MISS"
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
	PULSE_EXPORT_FIELD_UNKNOWN,
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
	PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL,
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
	PULSE_ARCHIVE_MAGIC_INVALID,
	PULSE_ARCHIVE_CORRUPT,
	PULSE_SHARD_MISSING,
	PULSE_SHARD_HEADER_INVALID,
	PULSE_SHARD_SCHEMA_MISMATCH,
	PULSE_SHARD_DICT_DIVERGENCE,
	PULSE_SHARD_DICT_WIDTH_OVERFLOW,
	PULSE_SHARD_DESCRIPTION_DIVERGENCE,
	PULSE_SHARD_RESERVED_NAME,
	PULSE_SHARD_NAME_COLLISION,
	PULSE_CHAIN_NOT_MERGEABLE,
	PULSE_CHAIN_EMPTY,
	PULSE_JOIN_TYPE_MISMATCH,
	PULSE_JOIN_KIND_NOT_IMPLEMENTED,
	PULSE_JOIN_FIELD_UNKNOWN,
	PULSE_JOIN_KEYS_EMPTY,
	PULSE_JOIN_TOO_MANY,
	PULSE_JOIN_FIELD_COLLISION,
	PULSE_LABEL_FIELD_UNKNOWN,
	PULSE_LABEL_FIELD_NOT_CATEGORICAL,
	PULSE_LABEL_TABLE_UNKNOWN,
	PULSE_LABEL_FIELD_COLLISION,
	PULSE_LABEL_DUPLICATE_BINDING,
	PULSE_LABEL_TABLE_NOT_ENUMERABLE,
	PULSE_LABEL_COLLISION,
	PULSE_LABEL_LOOKUP_MISS,
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
