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

	// PULSE_IMPORT_NULL_PROMOTED is a WARNING-class code emitted when an
	// inferred import (no explicit --schema) encounters a null cell in a
	// column that inference had marked non-nullable — because the null
	// fell outside the bounded inference sample window. The importer
	// promotes the field to nullable and continues rather than failing
	// the row; the code records which fields were promoted so callers can
	// widen the inference sample or supply an explicit schema. Never
	// emitted for explicit-schema imports, where a null in a declared
	// non-nullable field remains a PULSE_IMPORT_ROW_ERROR.
	PULSE_IMPORT_NULL_PROMOTED Code = "PULSE_IMPORT_NULL_PROMOTED"

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

	// PULSE_IMPORT_SET_OVERFLOW indicates the dictionary inferred for a
	// set-typed column exceeds the largest available set width (64
	// entries for set_u64). Surfaced by the importer when a multi-select
	// column's observed vocabulary cannot fit any set tier.
	PULSE_IMPORT_SET_OVERFLOW Code = "PULSE_IMPORT_SET_OVERFLOW"

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

	// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH indicates an extension
	// operator's ComponentsFunc emitted keys that are not a subset of
	// the registration's declared ComponentSchema.Keys (modulo the
	// universal-floor keys the orchestrator owns: n, n_null, total_n,
	// n_in, n_out, n_null_input). The probe rejects the registration at
	// pulse.New() time so the runtime never sees a half-declared
	// components contract. Details carry the offending category, name,
	// the undeclared emitted keys, and the declared key list so callers
	// can either widen the schema or trim the emission. Wired into
	// extensions_probe.verifyComponentKeysSubset.
	PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH Code = "PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH"

	// PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA indicates an extension
	// operator registration declared a ComponentsFunc emitter without a
	// matching ComponentSchema (Schema.Keys is empty), or declared a
	// non-empty ComponentSchema without a ComponentsFunc emitter. Both
	// halves of the contract MUST be present together so the probe can
	// validate the emitter's output against the declared schema; the
	// floor-only path (zero ComponentSchema AND nil ComponentsFunc)
	// remains a valid shape and is NOT subject to this code. Surfaced at
	// pulse.New() time during probe-validation; details carry the
	// category and name of the offending registration. Wired into
	// extensions_probe alongside
	// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH.
	PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA Code = "PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA"

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

	// PULSE_COMPOSE_LABEL_COLLISION indicates that two slots inside a
	// ComposedRequest resolve to the same final Label after the
	// auto-default pass (`request_<index+1>` for empty slots) and
	// caller-supplied values are merged. Compose-only overlay kinds
	// resolve their Reference / Targets by final Label, so duplicate
	// names would make sibling lookups ambiguous. Details carry the
	// offending label string plus the colliding slot indices so
	// callers can rename one side or drop the colliding caller-supplied
	// value.
	PULSE_COMPOSE_LABEL_COLLISION Code = "PULSE_COMPOSE_LABEL_COLLISION"

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

	// PULSE_RANGE_EMPTY indicates a labeled-date-range set
	// (GROUP_DATE_RANGES / FILTER_DATE_RANGES inline ranges, or a named
	// range table) was presented with zero ranges. At least one
	// {label, start, end} range is required.
	PULSE_RANGE_EMPTY Code = "PULSE_RANGE_EMPTY"

	// PULSE_RANGE_INVALID indicates a single labeled date range is
	// malformed: a start or end boundary literal could not be parsed
	// against any known date layout (encoding.DateFormats), or the range
	// is bounded on both sides with start strictly after end. Details
	// carry the offending label and the boundary value(s).
	PULSE_RANGE_INVALID Code = "PULSE_RANGE_INVALID"

	// PULSE_RANGE_DUPLICATE_LABEL indicates two ranges in the same
	// labeled-date-range set share a Label. Labels are the bucket keys a
	// row maps to, so they must be unique within a set. Details carry the
	// duplicated label.
	PULSE_RANGE_DUPLICATE_LABEL Code = "PULSE_RANGE_DUPLICATE_LABEL"

	// PULSE_RANGE_OVERLAP indicates two ranges in the same
	// labeled-date-range set cover an overlapping span of days. Bounds are
	// inclusive on both sides (so a range ending 2024-03-31 and one
	// starting 2024-04-01 are contiguous, not overlapping), and an open
	// (omitted/null) boundary extends to -inf / +inf. A record day landing
	// in an overlap would map ambiguously to two labels, so the set is
	// rejected. Details carry the two offending labels.
	PULSE_RANGE_OVERLAP Code = "PULSE_RANGE_OVERLAP"

	// PULSE_RANGE_SOURCE_AMBIGUOUS indicates a GROUP_DATE_RANGES /
	// FILTER_DATE_RANGES operator did not name exactly one range source.
	// Exactly one of an inline `ranges` array or a named `table` reference
	// is required: supplying both, or neither, is ambiguous and rejected.
	PULSE_RANGE_SOURCE_AMBIGUOUS Code = "PULSE_RANGE_SOURCE_AMBIGUOUS"

	// PULSE_RANGE_TABLE_UNKNOWN indicates a GROUP_DATE_RANGES /
	// FILTER_DATE_RANGES operator referenced a range table by a name that
	// is not registered (via Options.Extensions.RangeTables or the
	// PULSE_RANGE_TABLES_DIR loader). Details carry the offending name.
	PULSE_RANGE_TABLE_UNKNOWN Code = "PULSE_RANGE_TABLE_UNKNOWN"

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

	// PULSE_CROSSTAB_EMPTY_ROWS indicates a Crosstab section was
	// presented with no row-axis groupers. A crosstab requires at
	// least one grouper on each axis; use a plain grouped Process
	// request when only one axis is needed.
	PULSE_CROSSTAB_EMPTY_ROWS Code = "PULSE_CROSSTAB_EMPTY_ROWS"

	// PULSE_CROSSTAB_EMPTY_COLUMNS indicates a Crosstab section was
	// presented with no column-axis groupers. Mirrors PULSE_CROSSTAB_
	// EMPTY_ROWS for the column axis.
	PULSE_CROSSTAB_EMPTY_COLUMNS Code = "PULSE_CROSSTAB_EMPTY_COLUMNS"

	// PULSE_CROSSTAB_MISSING_CELL indicates a Crosstab section was
	// presented without a Cell aggregation. The cell aggregation is
	// the value emitted per (row-tuple, column-tuple) intersection
	// and is required.
	PULSE_CROSSTAB_MISSING_CELL Code = "PULSE_CROSSTAB_MISSING_CELL"

	// PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS indicates a Crosstab
	// section was presented alongside top-level Groups or
	// Aggregations on the same Request. The two surfaces are
	// mutually exclusive; the crosstab section already lowers to a
	// grouped request internally.
	PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS Code = "PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS"

	// PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE indicates a Crosstab
	// section requested a normalization mode whose required margin
	// cannot be computed (e.g. normalize=row on a degenerate request
	// where the row-margin aggregation has no defined finalizer for
	// the chosen aggregator). Default behavior is to leave the
	// affected cells as null and surface the warning; strict mode
	// promotes it to an error.
	PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE Code = "PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE"

	// PULSE_CROSSTAB_AGG_UNCLASSIFIED is an internal guard surfaced
	// when the margin computation encounters an aggregator that has
	// not been classified in AggregationType.MarginReducibility().
	// Reaching this code means a new aggregator was added without
	// updating the reducibility table — a CI-gated repair.
	PULSE_CROSSTAB_AGG_UNCLASSIFIED Code = "PULSE_CROSSTAB_AGG_UNCLASSIFIED"

	// PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE indicates the
	// Crosstab section's normalize_level value falls outside the
	// valid range [0, len(axis)-1] for the axis selected by
	// Normalize (rows when row, columns when column). Valid depths
	// are zero-indexed from the top of the axis.
	PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE Code = "PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE"

	// PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS indicates
	// normalize_level was set on a Crosstab section whose Normalize
	// is "none". The level selector only has meaning when a
	// normalization direction is selected; set Normalize to row or
	// column, or omit normalize_level.
	PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS Code = "PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS"

	// PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE indicates
	// normalize_level was set with normalize=total. Total
	// normalization uses a scalar grand-total denominator with no
	// axis to descend; the level selector applies only to
	// normalize=row or normalize=column.
	PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE Code = "PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE"

	// PULSE_CROSSTAB_NORMALIZE_MAP_VALUED indicates the Crosstab
	// section requested a normalize mode (row / column / total)
	// paired with a cell aggregator whose output is map-valued
	// (AGG_SET_FREQUENCY). Dividing one map by another is undefined;
	// drop the normalize directive or pick a scalar aggregator (e.g.
	// AGG_SET_CARDINALITY_SUM) for normalized output.
	PULSE_CROSSTAB_NORMALIZE_MAP_VALUED Code = "PULSE_CROSSTAB_NORMALIZE_MAP_VALUED"

	// PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE indicates the
	// Crosstab section's normalize_within value falls outside the
	// valid range [0, len(other-axis)-1] for the axis OPPOSITE the
	// one selected by Normalize (columns when normalize=row, rows
	// when normalize=column). Valid depths are zero-indexed from the
	// top of the other axis.
	PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE Code = "PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE"

	// PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS indicates
	// normalize_within was set on a Crosstab section whose Normalize
	// is "none". The cross-axis partition selector only has meaning
	// when a normalization direction is selected; set Normalize to
	// row or column, or omit normalize_within.
	PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS Code = "PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS"

	// PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE indicates
	// normalize_within was set with normalize=total. Total
	// normalization uses a scalar grand-total denominator with no
	// other axis to partition; normalize_within applies only to
	// normalize=row or normalize=column.
	PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE Code = "PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE"

	// PULSE_CROSSTAB_MARGIN_AGG_INVALID indicates an entry in the
	// Crosstab section's margin_aggregations slot is structurally
	// malformed — a null element, or an entry carrying no aggregation
	// type. There is no default auxiliary aggregator, so the entry is
	// refused rather than dropped: silently skipping it would return a
	// margin with the requested figure missing and nothing saying so.
	PULSE_CROSSTAB_MARGIN_AGG_INVALID Code = "PULSE_CROSSTAB_MARGIN_AGG_INVALID"

	// PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL indicates two
	// margin_aggregations entries resolve to the same effective label
	// (Label when set, otherwise TYPE_field), or one collides with the
	// cell aggregation's label. Margin components are keyed by label,
	// so the second figure would overwrite the first.
	PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL Code = "PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL"

	// PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED indicates margin_aggregations
	// were declared on a Crosstab section that DISPLAYS no margin
	// (margins.rows / .columns / .grand all false). Auxiliary
	// aggregations land in the row / column / grand accumulators only
	// and are emitted only where that margin is displayed, so nothing
	// carries them back. A normalize direction does not satisfy it: the
	// margin it requires is a denominator, and an auxiliary is never a
	// denominator — both execution paths still accumulate it in that
	// shape, so this warning is also the only notice that the work is
	// paid for and discarded. Surfaced as a predict WARNING — the
	// request is structurally legal and runs — never as an error.
	PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED Code = "PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED"

	// PULSE_REQUEST_UNKNOWN_FIELD indicates a request JSON carried a
	// top-level key that is not a recognised Request slot. JSON
	// decoding silently ignores unknown keys, so the offending slot
	// is dropped and the request runs as if that operation were
	// absent. The most common cause is reusing a manifest
	// operator-catalog field name ("groupers", "aggregators") as the
	// request key instead of the request slot name ("groups",
	// "aggregations"). Surfaced by the MCP action handlers before
	// execution; details carry the offending key(s), the nearest
	// valid slot, and the full valid-key list.
	PULSE_REQUEST_UNKNOWN_FIELD Code = "PULSE_REQUEST_UNKNOWN_FIELD"

	// PULSE_OVERLAY_KIND_UNKNOWN indicates a Request.Overlays entry
	// referenced an OverlayKind not present in
	// types.AllOverlayKinds(). Surfaced by descriptor.ValidateOverlays
	// at predict time and as a defense-in-depth guard inside
	// processing.ApplyOverlays.
	PULSE_OVERLAY_KIND_UNKNOWN Code = "PULSE_OVERLAY_KIND_UNKNOWN"

	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE indicates an
	// OverlaySpec's Ref does not match the host shape required by the
	// chosen Kind. For OVERLAY_INDEX_VS_MARGIN this fires in three
	// cases: missing Ref.Margin pointer, unknown Ref.Margin.Axis, or
	// non-MATRIX host (Request.Crosstab is nil).
	PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE Code = "PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE"

	// PULSE_OVERLAY_COMPONENTS_REQUIRED indicates an overlay kind that
	// reads Response.Components.Crosstab (per-cell n / weight sums /
	// Welford triples / margin counts) ran against a host built with
	// components disabled. The OVERLAY_PAIRWISE_* family raises this at
	// handler entry: without the components block there is no sample-size
	// leg to divide by. Re-run with components enabled (clear
	// DisableComponents / the per-request override) so the host carries
	// the counters the pairwise test needs.
	PULSE_OVERLAY_COMPONENTS_REQUIRED Code = "PULSE_OVERLAY_COMPONENTS_REQUIRED"

	// PULSE_OVERLAY_SCOPE_UNSUPPORTED indicates an OverlaySpec named a
	// scope that is not yet supported for the chosen Kind.
	// OVERLAY_INDEX_VS_MARGIN currently supports Scope=CELL only.
	PULSE_OVERLAY_SCOPE_UNSUPPORTED Code = "PULSE_OVERLAY_SCOPE_UNSUPPORTED"

	// PULSE_OVERLAY_REF_ZERO is a WARNING-class code emitted by an
	// overlay handler when the referenced margin denominator is zero,
	// absent, or yields a non-finite value (e.g. cell / 0 in
	// OVERLAY_INDEX_VS_MARGIN). The affected cell stays absent on the
	// overlay payload; the warning carries the row / column index and
	// margin axis so callers can audit the failing cells. Surfaced as
	// a Response.Warning, never as an envelope error.
	PULSE_OVERLAY_REF_ZERO Code = "PULSE_OVERLAY_REF_ZERO"

	// PULSE_OVERLAY_EXPECTED_LOW is a WARNING-class code emitted by the
	// inferential overlay family (OVERLAY_CHISQ_MATRIX / CHISQ_ROW /
	// CHISQ_COL / FISHER_EXACT_CELL) when the canonical "expected count
	// is too low for the χ² approximation" heuristic fires. Mirrors
	// PULSE_TEST_EXPECTED_COUNT_TOO_LOW on the TEST_CHISQ surface — the
	// statistic is still emitted alongside but the approximation is
	// flagged as potentially unreliable so renderers can highlight the
	// affected rows / columns / cells. Surfaced as a Response.Warning,
	// never as an envelope error.
	PULSE_OVERLAY_EXPECTED_LOW Code = "PULSE_OVERLAY_EXPECTED_LOW"

	// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE indicates an OverlaySpec's Level
	// selector exceeds the nested-axis depth of the relevant host axis
	// (Row axis depth = len(req.Crosstab.Rows), Column axis depth =
	// len(req.Crosstab.Columns)). Mirrors
	// PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE on the crosstab
	// normalize_level surface. Forward-compat: the code + fixup are
	// registered so the catalog is in place for the deferred level /
	// within overlay family; OverlaySpec carries no Level slot today,
	// so this code is unreachable from runtime until that slot is
	// wired through.
	PULSE_OVERLAY_LEVEL_OUT_OF_RANGE Code = "PULSE_OVERLAY_LEVEL_OUT_OF_RANGE"

	// PULSE_OVERLAY_PARAM_MISSING indicates an OverlaySpec did not supply
	// a Params entry that the chosen Kind requires. OVERLAY_INDEX_VS_ROLLING_MEAN
	// is the canonical example — the window width lives on
	// Params["window"] per the WIN_* operator convention. Missing
	// Params["window"] fires this code at both predict
	// (descriptor.validateOverlayIndexVsRollingMean) and runtime
	// (processing.applyIndexVsRollingMean) with Details carrying the
	// kind and the missing param name. Reused by other windowed kinds
	// (e.g. OVERLAY_ZSCORE_VS_ROLLING) for the missing-window case.
	PULSE_OVERLAY_PARAM_MISSING Code = "PULSE_OVERLAY_PARAM_MISSING"

	// PULSE_OVERLAY_FORMULA_PARSE_ERROR indicates an OVERLAY_FORMULA spec's
	// `Params["formula"]` string failed to parse via `expr-lang/expr` —
	// e.g. unbalanced parentheses, a stray operator, or a typo on a
	// keyword. Surfaced at both predict
	// (`descriptor.validateOverlayFormula`) and runtime
	// (`processing.applyFormula` family) with Details carrying
	// `{formula, parse_error}` so the renderer can surface both the
	// offending input and the underlying parser message. The fixup
	// catalogue (errors/fixup_metadata.go) tracks the per-shape
	// namespace tables in skills/overlay-system.md.
	PULSE_OVERLAY_FORMULA_PARSE_ERROR Code = "PULSE_OVERLAY_FORMULA_PARSE_ERROR"

	// PULSE_OVERLAY_FORMULA_TYPE_MISMATCH indicates an OVERLAY_FORMULA
	// expression returned a value whose type cannot be coerced to a
	// numeric Statistic (float64). The coercion accepts `float64` /
	// `float32` / `int` / `int64` natively, widens `bool` to `0.0 /
	// 1.0`, and rejects everything else (strings, maps, nil, etc.).
	// Surfaced at runtime by `processing.applyFormula` after
	// `expr.Run` returns; Details carry `{returned_type, formula}`.
	// The fixup catalogue lives in errors/fixup_metadata.go.
	PULSE_OVERLAY_FORMULA_TYPE_MISMATCH Code = "PULSE_OVERLAY_FORMULA_TYPE_MISMATCH"

	// PULSE_OVERLAY_FORMULA_INVALID_IDENT indicates an OVERLAY_FORMULA
	// expression references an identifier (variable or function) not
	// in the per-host-shape variable table or the function set built
	// from `pulse.Options.Extensions.ExprFunctions` plus the
	// expr-lang stdlib. Surfaced at predict
	// (`descriptor.validateOverlayFormula` AST walk) with Details
	// carrying `{ident, host_shape, available_vars}`. Embedders that
	// need new variables MUST register a custom kind via
	// `pulse.Options.Extensions.OverlayKinds` — FORMULA cannot widen
	// its variable namespace from outside. The fixup hint
	// (errors/fixup_metadata.go) walks authors through the per-shape
	// namespace table in skills/overlay-system.md FORMULA section.
	PULSE_OVERLAY_FORMULA_INVALID_IDENT Code = "PULSE_OVERLAY_FORMULA_INVALID_IDENT"

	// PULSE_OVERLAY_YOY_FREQUENCY_MISSING indicates an OVERLAY_YOY spec
	// did not supply a `frequency` Param either on the OverlaySpec or on
	// the host's GROUP_DATE grouper. The YoY kind cannot infer the
	// correct prior-period stride from the GROUP_DATE `component` slot
	// alone because the per-component stride for "one year prior" varies
	// by component (annual ⇒ 1 ordinal, quarterly ⇒ 4 ordinals, monthly ⇒
	// 12 ordinals, weekly ⇒ 52 ordinals, daily ⇒ 365-day calendar
	// arithmetic, hourly ⇒ 365×24-hour arithmetic). The handler reads
	// the explicit `frequency` value from `spec.Params["frequency"]`
	// first (the YoY's own override) and falls back to
	// `req.Groups[0].Params["frequency"]` (the canonical GROUP_DATE
	// authoring slot). Surfaced at both predict
	// (descriptor.validateOverlayYoY) and runtime (processing.applyYoY)
	// with Details carrying the kind and the host grouper type.
	PULSE_OVERLAY_YOY_FREQUENCY_MISSING Code = "PULSE_OVERLAY_YOY_FREQUENCY_MISSING"

	// PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY indicates an OVERLAY_YOY
	// spec named a `frequency` Param outside the supported set
	// (`annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly`).
	// The supported set is the minimum frequency catalog needed to cover
	// the GROUP_DATE component family — finer-than-hourly or coarser-
	// than-annual frequencies are explicit non-goals in v1. Surfaced at
	// both predict (descriptor.validateOverlayYoY) and runtime
	// (processing.applyYoY) with Details carrying the offending
	// `frequency` value plus the supported list.
	PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY Code = "PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY"

	// PULSE_OVERLAY_REF_UNKNOWN is a WARNING-class code emitted by the
	// sibling-reference overlay family (OVERLAY_DELTA_VS_SIBLING /
	// OVERLAY_INDEX_VS_SIBLING) when the OverlaySpec.Ref.Sibling
	// (Field, Value) pair does not resolve to a known group on the
	// SERIES host. Two failure modes share the code: (1) the named
	// `Sibling.Field` is not a grouper field on the host's grouper
	// list, or (2) the named `Sibling.Value` is not present among the
	// observed axis-key values for that field. The affected layer
	// surfaces NaN statistics across every present entry; the warning
	// carries the offending `(field, value)` pair plus the kind so
	// callers can audit the failing reference. Surfaced as a
	// Response.Warning, never as an envelope error — analogous to
	// PULSE_OVERLAY_REF_ZERO's "denominator absent" emission shape.
	// Distinct from PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
	// (structural shape mismatch caught at predict time) and from
	// PULSE_OVERLAY_REF_ZERO (the sibling resolved but its value is
	// zero — only meaningful for the INDEX_VS_SIBLING dispatch where
	// division by zero is undefined).
	PULSE_OVERLAY_REF_UNKNOWN Code = "PULSE_OVERLAY_REF_UNKNOWN"

	// PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT indicates a whole-chain
	// overlay spec (OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE)
	// where the resolved target stage and reference stage produce
	// different host result shapes (one is MATRIX, the other SERIES, or
	// one is SCALAR while the other is SERIES, etc). The handler cannot
	// fold per-coordinate arithmetic when target and reference do not
	// agree on a shared coordinate grid; the layer surfaces an empty
	// payload that inherits the target stage's shape and the warning
	// carries the offending pair of shapes plus the originating
	// (target_index, stage_index, stage_name) for the
	// MCP fix-up surface. The canonical chain-overlay companion of
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (structural shape
	// mismatch caught at predict time, but for CHAIN-host kinds the
	// divergence is between two stages, not between a single Ref and
	// the host). Surfaced at both predict (descriptor.ValidateChain
	// shape-divergence gate) and runtime (processing.applyIndexVsStage /
	// processing.applyDeltaVsStage shape-divergence defence).
	// Warning-class — surfaced as a Response.Warning, never as an
	// envelope error.
	PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT Code = "PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT"

	// PULSE_OVERLAY_TARGET_UNKNOWN indicates a whole-chain
	// ChainOverlaySpec named a Target StageRef that does not resolve
	// to a known stage on the chain. Two failure modes share the code:
	// (1) StageRef.Index is non-nil but lands outside
	// [0, len(stages)), or (2) StageRef.Name is non-empty but does not
	// match any ChainStage.Name. The handler returns the coded error
	// without producing any overlay layer; Details carry the offending
	// stage_index / stage_name and the overlay spec index so callers
	// can fix-up the reference. Sibling of
	// PULSE_OVERLAY_REFERENCE_UNKNOWN — the Target arm is distinguished
	// by the `which: "target"` Detail.
	PULSE_OVERLAY_TARGET_UNKNOWN Code = "PULSE_OVERLAY_TARGET_UNKNOWN"

	// PULSE_OVERLAY_REFERENCE_UNKNOWN indicates a whole-chain
	// ChainOverlaySpec named a Ref StageRef that does not resolve to a
	// known stage on the chain (Index out of range OR Name unmatched),
	// or the spec did not populate Ref at all. Sibling of
	// PULSE_OVERLAY_TARGET_UNKNOWN distinguished by `which: "ref"`. The
	// code also covers the missing-reference-cell / missing-reference-
	// row surface on the CHAIN-host DELTA family
	// (OVERLAY_DELTA_VS_STAGE): when a target cell or row keys a
	// reference coordinate that does not exist, the warning rides this
	// code with `ref_missing: true` and the handler folds against an
	// implicit zero reference (so the delta equals the target value
	// verbatim). Surfaced at both predict (descriptor.ValidateChain) and
	// runtime (processing.ApplyChainOverlays + per-kind handlers).
	PULSE_OVERLAY_REFERENCE_UNKNOWN Code = "PULSE_OVERLAY_REFERENCE_UNKNOWN"

	// PULSE_OVERLAY_KEY_SET_DIVERGENT indicates a Compose overlay spec
	// where the resolved reference slot and one or more target slots
	// produce different per-coordinate key sets — matrix
	// (row × column) tuples that exist on one slot but not the other,
	// or series group-keys present on one slot's Data rows but absent
	// on another's. Compose-only overlays require strict cross-Request
	// key alignment so the renderer can fold target values against the
	// reference at byte-equal coordinates; tolerant alignment is an
	// explicit non-goal for v1 (callers needing it pre-align their
	// inputs or fall back to a multi-reference kind). Details carry the
	// `reference` slot label, the offending `target` slot label, the
	// `missing` key-set (keys present on the reference but absent from
	// the target), and the `extra` key-set (keys present on the target
	// but absent from the reference) — all four are populated via
	// encoding/json-friendly types so the envelope serializer renders
	// them verbatim. The check runs once per overlay spec at the
	// post-slot-barrier inside processing.ApplyComposeOverlays, BEFORE
	// the schema-match and dict-drift gates fire — key-set
	// divergence is the cheapest signal so it fails fast. Surfaced at
	// runtime; the descriptor.ValidateComposedRequest predict-time
	// companion is deferred.
	PULSE_OVERLAY_KEY_SET_DIVERGENT Code = "PULSE_OVERLAY_KEY_SET_DIVERGENT"

	// PULSE_OVERLAY_SCHEMA_DIVERGENT indicates a Compose overlay spec
	// where the resolved reference slot and one or more target slots
	// produce structurally divergent schemas across the row / column
	// axes. The structural match is over grouper kinds + types + nested
	// depth — field names are explicitly allowed to differ. Two slots
	// can rename the same categorical_u32 column ("brand" vs "label")
	// and still align; two slots whose row axis differs in grouper kind
	// (GROUP_CATEGORY vs GROUP_RANGE) or in depth (one nested grouper
	// vs two) cannot. Details carry the `reference` slot label, the
	// `target` slot label, and the canonical-string `reference_schema`
	// / `target_schema` (kind tuples joined "|" per axis, axes joined
	// "/") so renderers can diff the two structures verbatim. The
	// check runs once per overlay spec at the post-slot-barrier inside
	// processing.ApplyComposeOverlays AFTER PULSE_OVERLAY_KEY_SET_DIVERGENT
	// and shape gates have passed. Surfaced at runtime; the
	// descriptor.ValidateComposedRequest predict-time companion is
	// deferred.
	PULSE_OVERLAY_SCHEMA_DIVERGENT Code = "PULSE_OVERLAY_SCHEMA_DIVERGENT"

	// PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT indicates a Compose overlay
	// spec where the reference slot and one or more target slots
	// disagree on host result shape (one is MATRIX while the other is
	// SERIES, or one is SCALAR while the other is non-SCALAR). Compose
	// overlays compare across slots cell-for-cell at byte-equal
	// coordinates; without a shared shape there is no coordinate grid
	// to fold. Details carry the offending `target_label`, the
	// `reference_shape`, and the `target_shape`. Sibling of
	// PULSE_OVERLAY_SLOT_NOT_CROSSTAB — both surface shape-level
	// rejections at the same gate, distinguished by whether the
	// rejection is "shapes disagree" (this code) or "the chosen kind
	// requires a specific shape and a target violates it" (the other
	// code). The check runs once per overlay spec at the post-slot-
	// barrier inside processing.ApplyComposeOverlays AFTER
	// PULSE_OVERLAY_KEY_SET_DIVERGENT and BEFORE
	// PULSE_OVERLAY_SCHEMA_DIVERGENT. Surfaced at runtime; the
	// descriptor.ValidateComposedRequest predict-time companion is
	// deferred.
	PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT Code = "PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT"

	// PULSE_OVERLAY_DICT_PREFIX_DRIFT indicates a Compose overlay spec
	// where ComposeOverlaySpec.Options.DictPrefixFast was opted into on a
	// pair of slots whose categorical dictionaries do NOT share a
	// byte-equal common prefix. The fast-path engages direct-index
	// comparison across slots (skipping the by-label decode the default
	// path performs); divergent dictionaries silently produce incorrect
	// cell alignment under that mode so the runtime fails loud rather
	// than degrading silently. Default behaviour is the SAFE by-label
	// join — every cell / group key is decoded via the slot's dictionary
	// before comparison and tolerates arbitrary dict reordering — so
	// this code only fires for callers that explicitly opted into the
	// fast path. Details carry the offending `reference` slot label, the
	// `target_label`, the `field` whose dictionaries disagree, and the
	// shorter `reference_dict_prefix` / `target_dict_prefix` strings
	// joined with "|" so renderers can diff the two dictionaries
	// verbatim. The check runs once per overlay spec at the post-slot
	// barrier inside processing.ApplyComposeOverlays AFTER the key-set,
	// shape, and schema gates have passed and BEFORE the per-kind
	// handler dispatches. Probe-validation at registration time is an
	// explicit non-goal: cohort pairing is a per-request decision
	// rather than a registration-time one, so the probe runs per
	// ApplyComposeOverlays invocation instead. Surfaced at runtime;
	// the descriptor.ValidateComposedRequest predict-time companion
	// is deferred.
	PULSE_OVERLAY_DICT_PREFIX_DRIFT Code = "PULSE_OVERLAY_DICT_PREFIX_DRIFT"

	// PULSE_OVERLAY_SLOT_NOT_CROSSTAB indicates a Compose overlay spec
	// whose Kind requires a MATRIX-shaped (crosstab) host but at least
	// one resolved slot — reference or target — is not a crosstab
	// result. The matrix-required Compose kinds (OVERLAY_RANK and the
	// matrix-shape Compose family) have not yet registered their
	// per-kind shape requirements, so the helper table
	// `kindRequiresMatrix` is a stub and this code stays unreachable
	// at runtime. The catalog row is in place so shape gating can be
	// wired without touching this file again. Details carry the
	// `required_shape: "MATRIX"`, the offending `target_label` (or
	// "reference" when the reference itself is the offender), and the
	// `observed_shape` ("series" / "scalar"). Sibling of
	// PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT — both fire at the same gate;
	// this one fires when the kind dictates MATRIX and a target is
	// non-MATRIX, the other when target / reference shapes disagree
	// regardless of kind. The check runs once per overlay spec at the
	// post-slot-barrier inside processing.ApplyComposeOverlays.
	PULSE_OVERLAY_SLOT_NOT_CROSSTAB Code = "PULSE_OVERLAY_SLOT_NOT_CROSSTAB"

	// PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP indicates a multi-reference
	// COMPOSE-host overlay spec (today: OVERLAY_PROP_Z_PANEL) named more
	// target slots than the per-spec `OverlayOptions.MaxPanelTargets`
	// cap allows. Per the interview risk paragraph "Multi-reference
	// combinatorics", the default cap is 16 — bumping the default
	// requires an interview update; the per-request override surface
	// lives on `ComposeOverlaySpec.Options.MaxPanelTargets` so callers
	// who need a larger panel today can opt in explicitly. Surfaced at
	// runtime by `processing.applyPropZPanel` at handler entry; the
	// descriptor.ValidateComposedRequest predict-time companion is
	// deferred. Details carry `{kind, observed, cap}` so the renderer
	// can surface both the offending size and the cap.
	PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP Code = "PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP"

	// PULSE_INDEX_MISSING indicates a LookupRequest named a field with
	// no sidecar point-lookup index on disk at the path
	// encoding.SidecarIndexPath derives (the same path
	// Service.BuildIndex writes to). Distinct from PULSE_LOOKUP_NOT_FOUND
	// (the index exists but the requested key has no match) and from
	// PULSE_LOOKUP_MISS / PULSE_LOOKUP_TABLE_UNKNOWN (the unrelated
	// expr-runtime lookup-table feature). Details carry the cohort
	// path, the offending field, and the derived index_path so callers
	// can build it directly.
	PULSE_INDEX_MISSING Code = "PULSE_INDEX_MISSING"

	// PULSE_LOOKUP_NOT_FOUND indicates a LookupRequest's key value has
	// no matching entry in the sidecar point-lookup index's resolved
	// hash bucket — the index itself was present and readable
	// (otherwise PULSE_INDEX_MISSING fires first), but no
	// IndexEntry.Key in the bucket is byte-equal to the resolved
	// on-wire key bytes. Details carry the cohort path, field, and
	// literal value that failed to resolve.
	PULSE_LOOKUP_NOT_FOUND Code = "PULSE_LOOKUP_NOT_FOUND"

	// PULSE_LOOKUP_AMBIGUOUS indicates a LookupRequest resolved to a
	// sidecar IndexEntry whose RowIDs carries more than one row-id (a
	// duplicate key value in the source cohort) while the effective
	// LookupRequest.Multiplicity is LookupMultiplicityAssertUnique — the
	// default when the field is left unset. Distinct from
	// PULSE_LOOKUP_NOT_FOUND (zero matches); this is the "too many
	// matches for assert-unique" case. Never fires under
	// LookupMultiplicityFirst / LookupMultiplicityAll — both modes
	// accept a multi-row match by design. Details carry the cohort path,
	// key fields, literal values, and the matched row count.
	PULSE_LOOKUP_AMBIGUOUS Code = "PULSE_LOOKUP_AMBIGUOUS"

	// PULSE_INDEX_STALE indicates the sidecar point-lookup index's
	// embedded content-hash Fingerprint no longer matches the current
	// `.pulse` cohort file's content. The cohort was mutated (re-imported,
	// re-exported, or otherwise rewritten) after the sidecar was built,
	// so the row-ids the sidecar's hash buckets point at may no longer
	// address the same records — serving a lookup against it risks
	// returning silently wrong rows. Service.Lookup hard-errors rather
	// than falling back to a full scan or auto-rebuilding; the sidecar
	// must be rebuilt explicitly via `pulse index build` before lookups
	// against these key fields can resume. Distinct from
	// PULSE_INDEX_MISSING (no sidecar exists at all) and from
	// PULSE_LOOKUP_NOT_FOUND (the sidecar is fresh but the key has no
	// match). Details carry the cohort path, key fields, and the derived
	// index_path.
	PULSE_INDEX_STALE Code = "PULSE_INDEX_STALE"

	// PULSE_INDEX_UNSUPPORTED_SHARDED indicates a point-lookup
	// operation (Service.BuildIndex, Service.Lookup, or
	// Service.VerifyIndex) targeted a shard-archive cohort (first four
	// bytes match encoding.ZipMagic, "PK\x03\x04") rather than a
	// single-file `.pulse` cohort. Sharded cohorts are out of scope for
	// point-lookup v1: row-id addressing (encoding.RecordLocator.Offset)
	// only has meaning for a single contiguous record region, which a
	// multi-shard archive does not present. Detected cheaply — via the
	// same leading-magic-bytes dispatch service.Service.Open already
	// performs to distinguish single-file vs archive cohorts, not an
	// added scan. Details carry the cohort path. Distinct from
	// PULSE_INDEX_MISSING (single-file cohort, no sidecar built yet).
	PULSE_INDEX_UNSUPPORTED_SHARDED Code = "PULSE_INDEX_UNSUPPORTED_SHARDED"

	// PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED is a WARNING-class code
	// emitted by the CSV (and TSV) export adapter when an overlay-bearing
	// Response is exported to a flat tabular format that cannot encode
	// nested overlay payloads. Per
	// research/export-embedding-shape.md § 7 the adapter implements
	// warn-and-skip: the host CSV body is written verbatim (byte-
	// identical to a pre-overlay export) and the overlay layers are
	// dropped on the floor. The warning carries `layer_count`,
	// `layer_names`, and `layer_kinds` so callers can audit which
	// layers were dropped. Surfaced as a Response.Warning / envelope
	// warnings entry, never as an envelope error — the host export
	// proceeds successfully. The TSV adapter shares the CSV writer
	// surface and inherits the warn-and-skip behaviour; the code name
	// stays CSV-flavoured because CSV is the canonical name in the
	// warning text. Fixups call out the Arrow / Parquet / Excel /
	// NDJSON alternatives that DO carry overlays and the
	// IncludeOverlays=false opt-out that suppresses the warning while
	// keeping CSV output.
	PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED Code = "PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED"

	// PULSE_TEMPLATE_NOT_FOUND indicates a render was asked for a
	// template name that is not registered — neither declared
	// programmatically nor loaded from a template directory. Details
	// carry the requested name under DetailTemplate.
	PULSE_TEMPLATE_NOT_FOUND Code = "PULSE_TEMPLATE_NOT_FOUND"

	// PULSE_TEMPLATE_INVALID indicates a template document failed
	// declaration validation: malformed JSON, a missing or empty name, a
	// body that is not a JSON object, a duplicate variable name, or a
	// variable declaration the template layer cannot interpret. Raised
	// when the template is registered, not when it is rendered, so a bad
	// document never reaches a caller's render call. Details carry the
	// offending template under DetailTemplate and, when the fault is
	// variable-scoped, the variable under DetailVariable.
	PULSE_TEMPLATE_INVALID Code = "PULSE_TEMPLATE_INVALID"

	// PULSE_TEMPLATE_TARGET_UNKNOWN indicates a template's `target` is
	// absent or names something other than one of the five public
	// request roots. Targets are spelled lowercase on the wire —
	// "request" (types.Request), "composed" (types.ComposedRequest),
	// "chain" (types.ChainRequest), "facet" (types.FacetRequest),
	// "sample" (types.SampleRequest) — never as the Go type name. The
	// target selects the type the rendered JSON strict-decodes into, so
	// it cannot be inferred. Details carry the template under
	// DetailTemplate and the offending target value.
	//
	// Also raised when a target-specific facade method is handed a
	// template declaring a different one — RenderTemplateRequest returns
	// *types.Request and so accepts only the "request" target. The
	// declared target is valid, just not one that method can produce;
	// details then also carry expected_target, and the message names the
	// general RenderTemplate as the method that handles it.
	PULSE_TEMPLATE_TARGET_UNKNOWN Code = "PULSE_TEMPLATE_TARGET_UNKNOWN"

	// PULSE_TEMPLATE_VAR_MISSING indicates a variable declared
	// `required` resolved to nothing at render: the caller supplied no
	// value and the declaration carries no `default`. Distinct from
	// PULSE_TEMPLATE_UNRESOLVED, which is the render-time marker that
	// survived substitution. Details carry the template under
	// DetailTemplate and the variable under DetailVariable.
	PULSE_TEMPLATE_VAR_MISSING Code = "PULSE_TEMPLATE_VAR_MISSING"

	// PULSE_TEMPLATE_VAR_UNKNOWN indicates the caller supplied a
	// variable the template does not declare. Unknown variables are
	// rejected rather than ignored — silently dropping one produces a
	// request that looks parameterised but is not. Details carry the
	// template under DetailTemplate and the offending name under
	// DetailVariable.
	PULSE_TEMPLATE_VAR_UNKNOWN Code = "PULSE_TEMPLATE_VAR_UNKNOWN"

	// PULSE_TEMPLATE_VAR_TYPE indicates a supplied value — or a
	// declaration's own `default` — does not match the variable's
	// declared type. Details carry the template under DetailTemplate,
	// the variable under DetailVariable, and the declared vs. supplied
	// types.
	PULSE_TEMPLATE_VAR_TYPE Code = "PULSE_TEMPLATE_VAR_TYPE"

	// PULSE_TEMPLATE_VAR_ENUM indicates a supplied value is not a member
	// of an `enum` variable's declared `values` set. Membership is exact
	// and case-sensitive. Details carry the template under
	// DetailTemplate, the variable under DetailVariable, the offending
	// value, and the permitted set.
	PULSE_TEMPLATE_VAR_ENUM Code = "PULSE_TEMPLATE_VAR_ENUM"

	// PULSE_TEMPLATE_UNRESOLVED indicates an unguarded variable marker
	// survived substitution — a `$var` or `{{token}}` in the template
	// body had no value to substitute. Rendering hard-fails rather than
	// emitting a request carrying a literal marker string. Details carry
	// the template under DetailTemplate and the unresolved marker's
	// variable name under DetailVariable.
	PULSE_TEMPLATE_UNRESOLVED Code = "PULSE_TEMPLATE_UNRESOLVED"

	// PULSE_TEMPLATE_RENDER_INVALID indicates substitution succeeded but
	// the rendered JSON failed strict-decode into the template's target
	// request type — typically an unknown field, or a substituted value
	// landing in a slot whose type it does not fit. Details carry the
	// template under DetailTemplate, the target type, and the decode
	// fault.
	PULSE_TEMPLATE_RENDER_INVALID Code = "PULSE_TEMPLATE_RENDER_INVALID"

	// PULSE_SPSS_DICT_INVALID indicates an SPSS `.sav` dictionary is
	// structurally malformed: the file does not open with the `$FL2` /
	// `$FL3` magic, the layout code identifies neither byte order, a
	// record carries an unknown type tag or an out-of-range field, or a
	// record appears where the format does not allow it. Details carry
	// the offending record under DetailSPSSRecord and its byte offset
	// under DetailSPSSOffset.
	PULSE_SPSS_DICT_INVALID Code = "PULSE_SPSS_DICT_INVALID"

	// PULSE_SPSS_DICT_TRUNCATED indicates an SPSS `.sav` file ends part
	// way through the dictionary: a record claims more bytes than remain,
	// or the stream runs out before the record type 999 terminator is
	// reached. The bytes present are well formed as far as they go, which
	// is what distinguishes this from PULSE_SPSS_DICT_INVALID. Details
	// carry the record under DetailSPSSRecord and the byte offset the
	// read ran past under DetailSPSSOffset.
	PULSE_SPSS_DICT_TRUNCATED Code = "PULSE_SPSS_DICT_TRUNCATED"

	// PULSE_SPSS_FILE_EMPTY indicates an SPSS `.sav` source carries no
	// bytes at all: a zero-length file, or an in-memory reader handed a
	// nil or empty buffer. It is deliberately DISTINCT from
	// PULSE_SPSS_DICT_TRUNCATED, which means a real file stops part way
	// through a record — a zero-length file has no first record to be
	// part way through, and the two have completely different causes (a
	// touched-but-never-written path versus an interrupted transfer).
	// Details carry "header" under DetailSPSSRecord and offset 0 under
	// DetailSPSSOffset.
	PULSE_SPSS_FILE_EMPTY Code = "PULSE_SPSS_FILE_EMPTY"

	// PULSE_SPSS_ENDIANNESS_MISMATCH indicates an SPSS `.sav` file states
	// its byte order twice and the two statements contradict each other:
	// the header layout code reads as 2 or 3 in one order while the
	// record 7/3 machine-integer endianness field names the other. It is
	// an ERROR rather than a warning, unlike the sibling charset
	// cross-check: every multi-byte field in the file — every count,
	// every offset, every double — is read in whichever order wins, so
	// choosing wrongly does not lose a label, it produces a whole file of
	// plausible and incorrect numbers. Details carry the extension record
	// under DetailSPSSRecord, the subtype under DetailSPSSSubtype and the
	// record's byte offset under DetailSPSSOffset.
	PULSE_SPSS_ENDIANNESS_MISMATCH Code = "PULSE_SPSS_ENDIANNESS_MISMATCH"

	// PULSE_SPSS_MAGIC_FLAG_MISMATCH indicates an SPSS system file whose
	// 4-byte magic and header compression flag disagree about whether it
	// is a ZSAV: `$FL3` with compression flag 0 or 1, or `$FL2` with
	// compression flag 2. It is a WARNING. The compression flag is the
	// single dispatch point for how the data section is decoded, and it
	// is the more specific statement of the two — the magic is a coarse
	// generation label a re-saving tool can leave stale — so the flag
	// wins and the file still reads. Details carry "header" under
	// DetailSPSSRecord and the compression field's byte offset under
	// DetailSPSSOffset.
	PULSE_SPSS_MAGIC_FLAG_MISMATCH Code = "PULSE_SPSS_MAGIC_FLAG_MISMATCH"

	// PULSE_SPSS_VALUE_LABELS_DROPPED indicates a record 3/4 value-label
	// pair names variables it cannot be bound to — a set mixing variables
	// of different type or width, a set attached to a string wider than
	// the 8 bytes a record 3 value slot can hold, or an element index
	// landing on a string continuation rather than on a variable. It is a
	// WARNING that drops that one label set and imports everything else:
	// a value label is display metadata, so refusing the file would cost
	// the data to save the labels, and binding it anyway would mislabel
	// values silently. Details carry the record under DetailSPSSRecord,
	// the record type 4's byte offset under DetailSPSSOffset and the
	// variable under DetailSPSSVariable where one is named.
	PULSE_SPSS_VALUE_LABELS_DROPPED Code = "PULSE_SPSS_VALUE_LABELS_DROPPED"

	// PULSE_SPSS_EXTENSION_UNKNOWN indicates an SPSS `.sav` dictionary
	// carries a record type 7 extension subtype this reader does not
	// interpret. It is a WARNING, never a parse failure: real SPSS
	// versions emit subtypes the published format description does not
	// list, and refusing such a file would reject data that is otherwise
	// perfectly readable. The record's bytes are retained verbatim so
	// nothing is lost. Details carry the subtype under DetailSPSSSubtype
	// and the record's byte offset under DetailSPSSOffset.
	PULSE_SPSS_EXTENSION_UNKNOWN Code = "PULSE_SPSS_EXTENSION_UNKNOWN"

	// PULSE_SPSS_EXTENSION_INVALID indicates a record type 7 extension
	// subtype this reader does interpret carried a payload that does not
	// match the shape the format defines for it — a declared element size
	// or count the subtype does not allow, a truncated payload, or a
	// field naming a variable the dictionary does not have. It is a
	// WARNING: the record's framing was sound, so the dictionary walk
	// stayed aligned, and the raw bytes are retained. Only the
	// interpretation is dropped. Details carry the subtype under
	// DetailSPSSSubtype and a byte offset under DetailSPSSOffset.
	PULSE_SPSS_EXTENSION_INVALID Code = "PULSE_SPSS_EXTENSION_INVALID"

	// PULSE_SPSS_VERY_LONG_STRING_INVALID indicates the record 7/14 very
	// long string segmentation could not be reassembled: the record names
	// a variable the dictionary does not declare, states a width the
	// scheme cannot express, or the physical variables following the head
	// segment do not have the widths the declared width implies.
	//
	// It is a WARNING. The segmentation record only says how to JOIN
	// physical variables that are already present, so a fold that cannot
	// be performed loses no bytes — the physical segments simply surface
	// as the separate columns the dictionary literally declares, under
	// their own names. That is visibly wrong rather than quietly wrong,
	// which is the whole reason it is not a silent skip. Details carry
	// the subtype under DetailSPSSSubtype, the variable under
	// DetailSPSSVariable where one was named, and a byte offset under
	// DetailSPSSOffset.
	PULSE_SPSS_VERY_LONG_STRING_INVALID Code = "PULSE_SPSS_VERY_LONG_STRING_INVALID"

	// PULSE_SPSS_COMPRESSION_UNSUPPORTED indicates an SPSS `.sav` file
	// whose dictionary parsed cleanly but whose data section declares an
	// encoding this reader cannot decode.
	//
	// All three encodings the format defines are read today —
	// uncompressed (header compression flag 0), bytecode compression
	// (flag 1, the SPSS save default) and ZSAV zlib block compression
	// (flag 2, what a `.zsav` carries) — so this fires only for a
	// compression flag the format does not define. It is retained as
	// the named refusal for a data-section encoding the reader does not
	// implement, because a data section read under the wrong encoding
	// produces plausible-looking garbage and silently wrong numbers are
	// worse than no numbers. Details carry "data" under
	// DetailSPSSRecord and the first byte of the data section under
	// DetailSPSSOffset.
	PULSE_SPSS_COMPRESSION_UNSUPPORTED Code = "PULSE_SPSS_COMPRESSION_UNSUPPORTED"

	// PULSE_SPSS_ZSAV_INVALID indicates an SPSS `.zsav` file whose ZSAV
	// block index does not describe the file it sits in. The index is
	// the 24-byte ZHEADER at the head of the data section plus the
	// ZTRAILER it points at, which carries one 24-byte entry per zlib
	// block: the block's offset and size both compressed and
	// uncompressed. The entries must tile the compressed region exactly
	// — each block starting where the previous one ended, in both
	// coordinate spaces — and the trailer length must match the block
	// count it declares.
	//
	// It is a hard error because the index is the only thing that says
	// where a block begins: an entry that disagrees with its neighbours
	// means the reader would inflate from a byte offset the writer
	// never wrote a stream at. Details carry "data" under
	// DetailSPSSRecord, a byte offset under DetailSPSSOffset, and —
	// where one block is implicated — its 1-based position under
	// DetailSPSSBlock.
	PULSE_SPSS_ZSAV_INVALID Code = "PULSE_SPSS_ZSAV_INVALID"

	// PULSE_SPSS_ZSAV_BLOCK_CORRUPT indicates one zlib block of an SPSS
	// `.zsav` data section could not be inflated, or inflated to a size
	// other than the one its block-index entry declares. The index
	// itself was coherent, which is what distinguishes this from
	// PULSE_SPSS_ZSAV_INVALID: the offsets were right and the bytes at
	// them are damaged.
	//
	// A short or long inflation is as fatal as a zlib failure. The
	// decompressed blocks are concatenated into one command stream, so
	// a block that yields the wrong number of bytes shifts every
	// element after it onto the wrong variable. Details carry "data"
	// under DetailSPSSRecord, the block's compressed byte offset under
	// DetailSPSSOffset and its 1-based position under DetailSPSSBlock.
	PULSE_SPSS_ZSAV_BLOCK_CORRUPT Code = "PULSE_SPSS_ZSAV_BLOCK_CORRUPT"

	// PULSE_SPSS_COMPRESSION_INVALID indicates an SPSS `.sav` file
	// declares bytecode compression and its command stream is corrupt:
	// a command that cannot apply to the element position it fell on
	// (an all-spaces string segment where the dictionary declares a
	// numeric, or the system-missing sentinel where it declares a
	// string), or a compression bias the header states as a value
	// arithmetic cannot use.
	//
	// It is distinct from PULSE_SPSS_DATA_TRUNCATED, which is a stream
	// that ran out of bytes, and from
	// PULSE_SPSS_COMPRESSION_UNSUPPORTED, which is an encoding this
	// reader does not implement. This one is a stream that is present,
	// complete enough to keep reading, and no longer describing the
	// dictionary that precedes it — so continuing would emit numbers
	// rather than an error. Details carry "data" under
	// DetailSPSSRecord and the byte offset of the offending command
	// under DetailSPSSOffset.
	PULSE_SPSS_COMPRESSION_INVALID Code = "PULSE_SPSS_COMPRESSION_INVALID"

	// PULSE_SPSS_DATA_TRUNCATED indicates an SPSS `.sav` data section
	// ends part way through a case: the bytes after the record type 999
	// terminator are not a whole multiple of the case stride the
	// dictionary declares. The dictionary itself parsed cleanly, which is
	// what distinguishes this from PULSE_SPSS_DICT_TRUNCATED. Details
	// carry "data" under DetailSPSSRecord and the byte offset of the
	// incomplete case under DetailSPSSOffset.
	PULSE_SPSS_DATA_TRUNCATED Code = "PULSE_SPSS_DATA_TRUNCATED"

	// PULSE_SPSS_DATA_CASE_COUNT_MISMATCH indicates the number of cases
	// the data section actually contains disagrees with the count the
	// file declares — the header ncases field, or the record 7/16 64-bit
	// count where the file carries one. It is a WARNING: every complete
	// case present is still read, because discarding readable rows to
	// honour a writer's miscount would lose data the file plainly
	// contains. Details carry "data" under DetailSPSSRecord, the byte
	// offset of the data section under DetailSPSSOffset, and the declared
	// and actual counts under "declared" and "actual".
	PULSE_SPSS_DATA_CASE_COUNT_MISMATCH Code = "PULSE_SPSS_DATA_CASE_COUNT_MISMATCH"

	// PULSE_SPSS_CATEGORICAL_OVERFLOW indicates an SPSS variable maps to
	// a Pulse categorical field whose dictionary would need more entries
	// than categorical_u32 can hold. It is a hard error, not a warning:
	// the alternative is dropping values, and a `.pulse` cohort missing
	// rows of a free-text variable is worse than a refused import.
	// Details carry the variable under DetailSPSSVariable and the
	// distinct-value count under DetailSPSSDistinct.
	PULSE_SPSS_CATEGORICAL_OVERFLOW Code = "PULSE_SPSS_CATEGORICAL_OVERFLOW"

	// PULSE_SPSS_CARDINALITY_HIGH indicates an SPSS variable mapped to a
	// Pulse categorical field whose distinct-value count is a large
	// fraction of the case count — the free-text ("other, please
	// specify") signature. It is a WARNING and never blocks an import:
	// the mapping is lossless, and the cost is a large inline dictionary
	// block every read pays for, which is a performance concern rather
	// than a fidelity one. Details carry the variable under
	// DetailSPSSVariable, the distinct count under DetailSPSSDistinct and
	// the case count under DetailSPSSActualCases.
	PULSE_SPSS_CARDINALITY_HIGH Code = "PULSE_SPSS_CARDINALITY_HIGH"

	// PULSE_SPSS_TEMPORAL_PRECISION indicates an SPSS variable carrying a
	// date or time print format holds at least one value the matching
	// Pulse temporal type cannot represent exactly — a fractional second,
	// a non-finite double, or a second count outside the int64 range —
	// so the variable was mapped to `f64` raw SPSS seconds instead. It is
	// a WARNING: the raw seconds are lossless and the original print
	// format is retained for export, so nothing is discarded; only the
	// ergonomics of a typed temporal column are. Details carry the
	// variable under DetailSPSSVariable and the print format type code
	// under DetailSPSSFormat.
	PULSE_SPSS_TEMPORAL_PRECISION Code = "PULSE_SPSS_TEMPORAL_PRECISION"

	// PULSE_SPSS_DATE_WIDENED indicates an SPSS variable carrying a
	// day-resolution print format (DATE / ADATE / EDATE / SDATE / JDATE)
	// was mapped to `datetime` rather than `date`, because at least one
	// of its values carries a time of day the day-resolution type would
	// truncate, or falls before 1970-01-01 — which the unsigned epoch-day
	// `date` representation cannot express. It is a WARNING: `datetime`
	// holds every such value exactly and the date-family groupers accept
	// it by documented day truncation, so the widening costs 4 bytes per
	// record and nothing else. Details carry the variable under
	// DetailSPSSVariable and the print format type code under
	// DetailSPSSFormat.
	PULSE_SPSS_DATE_WIDENED Code = "PULSE_SPSS_DATE_WIDENED"

	// PULSE_SPSS_VALUE_COLLISION indicates two distinct SPSS values of one
	// variable resolve to the same Pulse categorical dictionary entry, so
	// the original-value-to-dictionary-ID mapping is no longer one-to-one
	// and an export cannot tell which value to re-emit. The reachable
	// cause is leading whitespace: the shared import path trims every
	// cell, so " X" and "X" become one entry. It is a WARNING because the
	// file is otherwise readable, and the recorded code-to-label-to-ID
	// triple carries both source values against the shared ID so the
	// collision is visible rather than invisible. Details carry the
	// variable under DetailSPSSVariable.
	PULSE_SPSS_VALUE_COLLISION Code = "PULSE_SPSS_VALUE_COLLISION"

	// PULSE_SPSS_MEASURE_LEVEL_MISMATCH indicates an SPSS variable whose
	// record 7/11 measurement level is `scale` carries value labels and
	// was therefore mapped to a Pulse categorical field, whose smart
	// defaults are AGG_FREQUENCY / GROUP_CATEGORY rather than the
	// AGG_SUM / GROUP_RANGE the declared level implies. It is a WARNING:
	// the mapping is lossless — every code and label is preserved — but
	// the analytic defaults will not be the ones the source file's author
	// declared. Details carry the variable under DetailSPSSVariable.
	PULSE_SPSS_MEASURE_LEVEL_MISMATCH Code = "PULSE_SPSS_MEASURE_LEVEL_MISMATCH"

	// PULSE_SPSS_NULL_TOKEN_COLLISION indicates an SPSS string value or
	// value-label key is one of the import pipeline's null sentinel
	// tokens ("", "NA", "N/A", "NULL", in any case), so cells carrying it
	// import as null and its dictionary entry is unreachable. It is a
	// WARNING: an all-blank string is SPSS's own de facto missing-string
	// convention and reading it as null is intended, but a literal "NA"
	// stored as data is a real value the shared import path collapses,
	// and that collapse must be visible. Details carry the variable under
	// DetailSPSSVariable.
	PULSE_SPSS_NULL_TOKEN_COLLISION Code = "PULSE_SPSS_NULL_TOKEN_COLLISION"

	// PULSE_SPSS_CHARSET_UNSUPPORTED indicates an SPSS `.sav` file
	// declares a character encoding this reader cannot decode with — a
	// name record 7/20 spells that resolves to no known charset, a
	// record 7/3 character code with no charset behind it (EBCDIC, DEC
	// Kanji), or a charset that is not an ASCII superset and therefore
	// cannot carry the format's own space padding and ASCII delimiters.
	//
	// It is a HARD error at dictionary parse rather than a warning that
	// falls back to UTF-8. Decoding bytes with the wrong codepage
	// produces text that is plausible and wrong, which is the single
	// failure this reader exists to prevent; a file that cannot be
	// decoded faithfully must not import at all. Details carry the
	// declared charset under DetailSPSSCharset.
	PULSE_SPSS_CHARSET_UNSUPPORTED Code = "PULSE_SPSS_CHARSET_UNSUPPORTED"

	// PULSE_SPSS_CHARSET_INVALID indicates a byte sequence in an SPSS
	// `.sav` file is not decodable in the charset the file declares —
	// an undefined byte in a single-byte codepage, or invalid UTF-8 in a
	// file declaring UTF-8.
	//
	// It is an error and never a silent substitution. golang.org/x/text
	// decoders default to emitting U+FFFD for undecodable input, which
	// would turn a codepage mismatch into a cohort full of replacement
	// characters that no later stage could distinguish from real data;
	// this reader opts out of that behaviour explicitly. Details carry
	// the declared charset under DetailSPSSCharset and, where the fault
	// is in a variable's own data or label, the variable under
	// DetailSPSSVariable.
	PULSE_SPSS_CHARSET_INVALID Code = "PULSE_SPSS_CHARSET_INVALID"

	// PULSE_SPSS_CHARSET_MISMATCH indicates an SPSS `.sav` file states
	// its character encoding twice and the two statements disagree: the
	// record 7/20 NAME resolves to one charset and the record 7/3
	// character code to another.
	//
	// It is a WARNING, and the 7/20 name wins. The name is strictly more
	// expressive than the number — 7/3 cannot tell ISO-8859-1 from
	// windows-1252 — and writers routinely leave the legacy numeric
	// field at an ASCII default while naming the real charset in 7/20.
	// A 7/3 code of 2 or 3 (ASCII) is therefore NOT a disagreement with
	// any ASCII-superset name and never raises this. Details carry the
	// charset actually used under DetailSPSSCharset.
	//
	// On the WRITE side it carries the same idea in the other direction:
	// the emitted file's record 7/20 declares one charset while some
	// payload it carries is in another. That happens in exactly one
	// place — the records 7/10, 7/17 and 7/18 whose bytes the reader
	// retains VERBATIM and never decodes, re-emitted verbatim into a
	// file the caller asked to be written in a different charset. The
	// bytes are the authoritative record of what the source said, so
	// they are still emitted; the disagreement is reported rather than
	// hidden. Pure-ASCII payloads are not a disagreement, because every
	// charset this package supports encodes ASCII as itself.
	PULSE_SPSS_CHARSET_MISMATCH Code = "PULSE_SPSS_CHARSET_MISMATCH"

	// PULSE_SPSS_CHARSET_UNENCODABLE indicates a string held by a
	// cohort being exported to `.sav` contains a character that has no
	// representation in the charset the emitted file declares.
	//
	// It is the WRITE-side mirror of PULSE_SPSS_CHARSET_INVALID, and it
	// exists for the same reason: golang.org/x/text will happily be
	// asked to substitute — encoding.ReplaceUnsupported and the
	// charmap EncodeRune sentinel 0x1A are both one call away — and a
	// substituted character is indistinguishable from data once written.
	// A `.sav` whose windows-1252 label reads "Z?rich" has lost the
	// name of a city and says nothing about having done so. So the
	// export stops, naming the variable, the offending value and the
	// character.
	//
	// The usual cause is a cohort that has been edited since it was
	// imported: text added by a Pulse operation is UTF-8 and need not be
	// expressible in the legacy codepage the source file declared.
	// Details carry the target charset under DetailSPSSCharset, the
	// variable under DetailSPSSVariable where the fault is inside one,
	// and the offending value under DetailSPSSValue.
	PULSE_SPSS_CHARSET_UNENCODABLE Code = "PULSE_SPSS_CHARSET_UNENCODABLE"

	// PULSE_SPSS_WIDTH_OVERFLOW indicates a string being written to a
	// `.sav` does not fit the fixed-width field the format gives it,
	// after it has been encoded into the emitted file's charset.
	//
	// SPSS widths are BYTE counts, never rune counts, so transcoding
	// changes them: "Zürich" is six bytes in windows-1252 and seven in
	// UTF-8. The writer therefore recomputes every declared width from
	// the ENCODED bytes, and a value variable widens to fit. This code
	// is what is left when widening is not available — a field whose
	// width the format fixes:
	//
	//   - a string variable past the 32767-byte ceiling SPSS puts on one;
	//   - a record type 2 short name past eight bytes;
	//   - a value label past the 255 bytes its one-byte length field can
	//     count;
	//   - the 64-byte header file label, or an 80-byte record type 6
	//     document line.
	//
	// It is an ERROR and never a truncation. Cutting a fixed-width field
	// to fit would silently drop the tail of a value — and, with a
	// multi-byte charset, would leave a half-character on the wire that
	// no reader can decode. Details carry the variable under
	// DetailSPSSVariable where one is at fault, the required byte width
	// under DetailSPSSWidth, the available width under
	// DetailSPSSDeclaredWidth and the target charset under
	// DetailSPSSCharset.
	PULSE_SPSS_WIDTH_OVERFLOW Code = "PULSE_SPSS_WIDTH_OVERFLOW"

	// PULSE_SPSS_NAME_INVALID indicates a name being written into a `.sav`
	// dictionary is not one SPSS can carry: it is empty, it is past the
	// 64-byte ceiling once encoded in the emitted file's charset, it opens
	// with something other than a letter, '@', '#' or '$', it carries a
	// character outside the letters, digits and '.', '_', '$', '#', '@' an
	// SPSS name is drawn from, or it ends with '.'.
	//
	// It is a WRITE-side boundary with no read-side twin, because Pulse
	// names are permissive and SPSS names are not: `.pulse` validates
	// nothing about a field name, so a cohort produced by synth, by a CSV
	// import or by a processing run can carry a name no `.sav` can express,
	// and nothing before this point would have noticed.
	//
	// It is a refusal rather than a rename because every way an illegal
	// name fails is QUIET. Record 7/13 is a tab-separated list of
	// `SHORT=LONG` pairs with no escape, so a name carrying '=' or a tab
	// re-parses as a different, shorter pair list and some other variable
	// silently acquires this one's name; record 7/7 is space-separated over
	// the same namespace, so a space inside a name splits one set member
	// into two that do not exist. Neither produces an unreadable file — both
	// produce a well-formed one that says something the cohort did not.
	//
	// Non-ASCII letters are NOT rejected: SPSS in UTF-8 mode accepts them,
	// and a file this reader has just read back can legitimately hold one.
	// Neither are SPSS's reserved syntax keywords (ALL, BY, TO, WITH, …),
	// which restrict the command language and not the file format. Details
	// carry the offending name under DetailSPSSVariable and, when one
	// column owns it, the cohort field under DetailSPSSField.
	PULSE_SPSS_NAME_INVALID Code = "PULSE_SPSS_NAME_INVALID"

	// PULSE_SPSS_NAME_COLLISION indicates two variables an SPSS export
	// would emit answer to one name. SPSS variable names are unique
	// without regard to case, so `Region` and `REGION` are one name and
	// the second record 7/13 mapping for it is dropped — leaving a column
	// in the file that no name reaches.
	//
	// It also covers the same fault one level down: two variables sharing
	// an eight-byte record type 2 SHORT name, which records 7/5, 7/7, 7/14
	// and 7/19 all key by, so each of those records would name only one of
	// the two.
	//
	// It is distinct from PULSE_SPSS_DERIVED_NAME_COLLISION, which is the
	// IMPORT-side collision between a generated `<var>_missing` sibling and
	// a variable the source file already declares. This one is about the
	// file being written. Details name both sides: the offending name under
	// DetailSPSSVariable and the variable that claimed it first under
	// DetailSPSSCollidesWith.
	PULSE_SPSS_NAME_COLLISION Code = "PULSE_SPSS_NAME_COLLISION"

	// PULSE_SPSS_COLUMN_UNMAPPED indicates an SPSS export found a cohort
	// column that no emitted variable is written from and that the metadata
	// sidecar's derived-column registry does not account for — a column
	// about to leave the export silently, carried in the `.pulse` file and
	// absent from the `.sav`.
	//
	// The registry is what makes the distinction decidable. An import
	// synthesises columns the source dictionary never declared — a
	// `<var>_missing` reason sibling and a multiple-dichotomy `set_*`
	// convenience column — and those are CONSUMED on the way back out
	// rather than emitted. Every one of them is named in the sidecar's
	// `derived` block, so a column that is unbound and not in that block is
	// not a derived column: it is data, and dropping it would be the quiet
	// loss this export path exists to refuse.
	//
	// Details name the cohort field under DetailSPSSField, the cohort under
	// DetailSPSSCohort and the sidecar under DetailSPSSSidecar.
	PULSE_SPSS_COLUMN_UNMAPPED Code = "PULSE_SPSS_COLUMN_UNMAPPED"

	// PULSE_SPSS_DERIVED_UNFOLDABLE indicates the metadata sidecar's
	// derived-column registry describes a column an export cannot fold back
	// into the file it came from.
	//
	// Four shapes reach it. The entry's `kind` is outside the vocabulary
	// this binary knows — a document written by a NEWER import, describing
	// a column whose fold-back is genuinely unknown, where both available
	// guesses (emit it as a variable, or drop it) are silent data faults.
	// The entry is under-populated for its kind: a `numeric_missing` entry
	// without its `reasons` dictionary cannot restore a missing code
	// without re-deriving the mapping and hoping it lands where the import
	// did. The entry names a source column that no emitted variable is
	// written from, so consuming the derived column would discard what it
	// held. Or the entry names a column the export is ALSO emitting as a
	// variable, which is a document disagreeing with itself about whether
	// that column is synthetic.
	//
	// It is a refusal for the reason the registry exists: treating a
	// derived column as real emits a phantom variable the source never had,
	// and treating a real one as derived drops a respondent's data. Neither
	// is visible in the output file. Details name the derived column under
	// DetailSPSSDerived, its source under DetailSPSSVariable where the
	// entry has one, and the sidecar under DetailSPSSSidecar.
	PULSE_SPSS_DERIVED_UNFOLDABLE Code = "PULSE_SPSS_DERIVED_UNFOLDABLE"

	// PULSE_SPSS_EXPORT_UNSUPPORTED indicates something in a cohort has
	// no honest `.sav` representation, so the export stops rather than
	// writing a file that says something the cohort did not.
	//
	// It was minted for a blunter claim — "Pulse cannot write .sav at
	// all" — which stopped being true when E5-S6 mounted the writer. It
	// is REPURPOSED rather than retired, on the same grounds E3-S2 kept
	// PULSE_SPSS_COMPRESSION_UNSUPPORTED: the code is already load-
	// bearing inside the writer, where it is the standing answer to "this
	// column cannot be expressed", and removing a code has a wider blast
	// radius (manifest golden, doc sites, an embedder-visible lookup
	// surface) than re-aiming one.
	//
	// What raises it today:
	//
	//   - A `set_*` column with an empty dictionary: a response set with
	//     no member variable to name (dict_synth.go).
	//   - A value with no writable form — a dictionary ID the plan
	//     records no SPSS code for, or an ID two source values collapsed
	//     onto (data_write.go).
	//   - A rendered ROW stream handed to the `.sav` writer with no
	//     cohort behind it and no schema to rebuild one from. The writer
	//     encodes from a cohort's raw storage, never from cell text; see
	//     io/spss's Writer.
	//
	// Details name the offending column under DetailSPSSVariable where
	// there is one, and carry the requested format under "format" plus
	// the output path under "output_path" on the dispatch arm.
	PULSE_SPSS_EXPORT_UNSUPPORTED Code = "PULSE_SPSS_EXPORT_UNSUPPORTED"

	// PULSE_SPSS_DERIVED_NAME_COLLISION indicates the `<var>_missing`
	// sibling column an SPSS import would generate for a user-missing
	// specification carries the same name as a variable the file already
	// declares.
	//
	// It is a hard error rather than a rename because both alternatives
	// lose: emitting two fields of one name produces a cohort whose
	// columns cannot be addressed unambiguously, and silently renaming
	// one produces a column whose name no consumer — including this
	// package's own export path, which drops derived columns and keeps
	// real ones — can map back to its source. SPSS variable names are
	// case-insensitive, so the comparison is too.
	//
	// Details name both sides: the generated sibling under
	// DetailSPSSDerived, the source variable it was derived from under
	// DetailSPSSVariable, and the colliding real variable under
	// DetailSPSSCollidesWith. Fix it by renaming the real variable in
	// SPSS, or by importing with --spss-missing=null, which suppresses
	// the sibling entirely at the cost of the missing REASON.
	PULSE_SPSS_DERIVED_NAME_COLLISION Code = "PULSE_SPSS_DERIVED_NAME_COLLISION"

	// PULSE_SPSS_MISSING_MODE_INVALID indicates a caller asked for a
	// user-missing handling mode that does not exist — a
	// --spss-missing / format.ReaderOptions.SPSSMissing value other than
	// "auto" or "null".
	//
	// It is a refusal rather than a fall back to the default because the
	// two modes produce different cohorts: "auto" carries a
	// `<var>_missing` sibling per user-missing variable and "null" does
	// not, so silently substituting one for a typo of the other would
	// hand the caller a schema they did not ask for. Details carry the
	// offending value under DetailSPSSMissingMode.
	PULSE_SPSS_MISSING_MODE_INVALID Code = "PULSE_SPSS_MISSING_MODE_INVALID"

	// PULSE_SPSS_CATEGORICAL_USER_MISSING indicates one or more SPSS
	// variables that mapped to a Pulse categorical column declare
	// user-missing codes, and those codes were kept as ORDINARY
	// dictionary entries rather than nulled or moved to a sibling.
	//
	// It is informational and never blocks an import: nothing was lost
	// and nothing was changed. It exists because the loss it prevents is
	// downstream rather than at import time — a percentage base computed
	// over a coded question silently includes its "Refused" category
	// unless the analyst excludes it, and an analyst who cannot see WHICH
	// entry is the refusal cannot write that exclusion. The Pulse
	// dictionary holds the SPSS CODES, so the value to exclude is "9",
	// never "Refused".
	//
	// One diagnostic covers the whole file rather than one per variable:
	// an all-categorical survey can have a missing code on every one of
	// hundreds of variables, and a warning per variable would bury the
	// signal it exists to carry. Details name every flagged variable and
	// its flagged dictionary entries under DetailSPSSMissingCategories,
	// and the count of flagged variables under DetailSPSSDistinct.
	PULSE_SPSS_CATEGORICAL_USER_MISSING Code = "PULSE_SPSS_CATEGORICAL_USER_MISSING"

	// PULSE_SPSS_MR_SET_NOT_DERIVED indicates an SPSS multiple-DICHOTOMY
	// response set could not be given the derived `set_*` convenience
	// column the mapping normally emits for one, and names why.
	//
	// It is a WARNING and never blocks an import, and that is a property
	// of the mapping rather than a tolerance: the derived column is
	// additive. Every constituent variable of the set is imported as its
	// own ordinary column whether or not the set derives, so a set that
	// does not derive costs ergonomics and never fidelity — there is
	// nothing to lose by carrying on, and refusing the file would throw
	// away data to protect a convenience.
	//
	// The reasons are all statements about what a Pulse `set_*` column
	// can hold: more than the 64 constituents a set_u64 bitmask has bits
	// for; a member variable no record type 2 declares; the same variable
	// named twice, which would need one bit to be two; a counted value
	// that will not compare against a numeric member; or a constituent
	// whose Pulse field name cannot survive as a set dictionary entry —
	// one containing the set-token delimiter "|", or one that IS a null
	// sentinel token ("NA", "N/A", "NULL"), either of which would make
	// the cell text ambiguous on the shared import path.
	//
	// Details name the set under DetailSPSSSet (including its leading
	// '$'), the member count under DetailSPSSDistinct, and, where one
	// member is at fault, that member under DetailSPSSVariable.
	PULSE_SPSS_MR_SET_NOT_DERIVED Code = "PULSE_SPSS_MR_SET_NOT_DERIVED"

	// PULSE_SPSS_SIDECAR_ABSENT indicates an SPSS export found no
	// metadata sidecar (`<cohort>.spss.json`) beside the cohort it was
	// asked to write, so it will synthesise a default SPSS dictionary
	// from the `.pulse` schema alone.
	//
	// It is a WARNING and never blocks an export. A cohort that was
	// never SPSS-derived — synth output, a CSV import, a processed
	// result — correctly has no sidecar, and that is the ordinary case
	// rather than an error condition. What is lost is only what the
	// `.pulse` schema cannot restate: value labels, measure levels,
	// print/write formats, missing-value specifications, response-set
	// definitions and the original short names. The columns and their
	// data are unaffected.
	//
	// It is deliberately NOT the same condition as
	// PULSE_SPSS_SIDECAR_STALE. Absent is benign; stale is the single
	// highest-fidelity-risk state available, because applying an
	// out-of-date dictionary to changed data produces a `.sav` that
	// looks authoritative and is wrong. Collapsing the two into one
	// warning is exactly the conflation this pair exists to prevent.
	//
	// Details carry the cohort path under DetailSPSSCohort and the
	// sidecar path that was looked for under DetailSPSSSidecar.
	PULSE_SPSS_SIDECAR_ABSENT Code = "PULSE_SPSS_SIDECAR_ABSENT"

	// PULSE_SPSS_SIDECAR_STALE indicates an SPSS export found a metadata
	// sidecar whose fingerprint no longer matches the cohort it
	// describes: the cohort's byte size or modification time has moved
	// since the sidecar was written, so the two are out of step.
	//
	// It is an ERROR, and the asymmetry with PULSE_SPSS_SIDECAR_ABSENT
	// is the point. A stale sidecar still holds a complete, plausible
	// SPSS dictionary — value codes, labels, missing-value
	// specifications, response-set definitions — and applying it to a
	// cohort whose columns or dictionaries have since changed yields a
	// `.sav` in which `IF q1 EQ 5` addresses a category that is no
	// longer there. The output carries every mark of authority and none
	// of the correctness, and nothing downstream can detect it. Refusing
	// to write is the only safe answer; there is no partial application
	// and no silent fallback to defaults.
	//
	// The read-path check that raises it is the same cheap O(1) size +
	// mtime comparison PULSE_INDEX_STALE uses, chosen for the same
	// reason: hashing a multi-GB cohort on every export would cost more
	// than the export. It has the same residual gap — an in-place edit
	// preserving BOTH size and mtime goes unnoticed — and the same
	// authoritative answer, a full SHA-256 recompute against the
	// fingerprint the sidecar carries.
	//
	// Details carry the cohort path under DetailSPSSCohort, the sidecar
	// path under DetailSPSSSidecar, and the mismatching pair under
	// DetailSPSSExpected / DetailSPSSActual so a caller can see which of
	// size and mtime moved.
	PULSE_SPSS_SIDECAR_STALE Code = "PULSE_SPSS_SIDECAR_STALE"

	// PULSE_SPSS_SIDECAR_INVALID indicates a file exists at the metadata
	// sidecar's path but is not a sidecar this binary can read: it is
	// not valid JSON, its `kind` is not "spss", its `format_version` is
	// not one this binary understands, its fingerprint is not a
	// well-formed digest, or a parallel array inside it violates the
	// length contract its consumers index against.
	//
	// It is an ERROR for the same reason PULSE_SPSS_SIDECAR_STALE is:
	// the file's presence is a statement that this cohort HAS source
	// metadata, and proceeding as though it did not would silently
	// substitute a synthesised default dictionary for the real one. It
	// is held apart from _STALE because the fix is different — a stale
	// sidecar is repaired by re-importing the source, an invalid one by
	// finding out what wrote the file.
	//
	// A `format_version` this binary does not recognise lands here
	// deliberately rather than being read optimistically: a document
	// written by a newer Pulse may have moved a slot this one indexes,
	// and misreading a dictionary is worse than declining it.
	//
	// Details carry the cohort path under DetailSPSSCohort and the
	// sidecar path under DetailSPSSSidecar.
	PULSE_SPSS_SIDECAR_INVALID Code = "PULSE_SPSS_SIDECAR_INVALID"

	// PULSE_SPSS_SIDECAR_IGNORED indicates a metadata sidecar exists
	// beside the cohort and the caller explicitly asked for it not to be
	// read (`--ignore-sidecar` / spss.WriterOptions.IgnoreSidecar), so
	// the export is synthesising a default SPSS dictionary from the
	// `.pulse` schema alone.
	//
	// It is a WARNING: an explicit instruction is not an error
	// condition. It exists as its own code rather than reusing
	// PULSE_SPSS_SIDECAR_ABSENT because the two states are genuinely
	// different — one cohort has no source metadata, the other has some
	// and is not using it — and a diagnostic that said "no sidecar
	// found" about a file sitting right there would be false.
	//
	// It is also the downgrade target for both sidecar refusals: with
	// the flag set, a stale or invalid sidecar produces this warning and
	// the synthesised-default path instead of PULSE_SPSS_SIDECAR_STALE /
	// PULSE_SPSS_SIDECAR_INVALID. That is the escape hatch's whole
	// purpose, which is also why the flag suppresses the READ rather
	// than only the staleness verdict: a caller who has opted out of the
	// sidecar gets the same output whatever state the file is in, and an
	// unreadable one cannot block them.
	//
	// Details carry the cohort path under DetailSPSSCohort and the
	// sidecar path under DetailSPSSSidecar — and deliberately NOT which
	// refusal, if any, was silenced: because the flag skips the READ,
	// nothing on this path knows whether the file was stale, invalid or
	// perfectly healthy, and a detail that could only be guessed at
	// would be worse than an absent one.
	PULSE_SPSS_SIDECAR_IGNORED Code = "PULSE_SPSS_SIDECAR_IGNORED"

	// PULSE_SPSS_NAME_SANITIZED is the WARNING that accompanies
	// --sanitize-names: one or more cohort field names could not be an
	// SPSS variable name and were rewritten so the export could proceed.
	//
	// It exists because the rewrite is opt-in but must never be silent.
	// The default is still the PULSE_SPSS_NAME_INVALID refusal — mangling
	// a caller's column names behind their back is worse than stopping —
	// and this warning is what makes the opt-in honest: every rename is
	// reported, so a consumer reading the emitted file can map its
	// variables back to the cohort's fields.
	//
	// It is raised only on the SYNTHESISED dictionary path. A
	// sidecar-driven export re-emits names that came FROM an SPSS file
	// and are legal by construction, so there is nothing to rewrite.
	//
	// Details carry every rename under DetailSPSSRenames as an ordered
	// list of {"field","name"} objects, in cohort schema order.
	PULSE_SPSS_NAME_SANITIZED Code = "PULSE_SPSS_NAME_SANITIZED"
)

// Detail map keys shared by the PULSE_TEMPLATE_* family. Every template
// error that can name a template does so under DetailTemplate; every one
// that can name a variable does so under DetailVariable. Callers key off
// these constants rather than re-spelling the strings.
const (
	// DetailTemplate is the CodedError.Details key carrying the name of
	// the template a PULSE_TEMPLATE_* error refers to.
	DetailTemplate = "template"

	// DetailVariable is the CodedError.Details key carrying the name of
	// the offending variable on the variable-scoped members of the
	// PULSE_TEMPLATE_* family.
	DetailVariable = "variable"
)

// Detail map keys shared by the PULSE_SPSS_* family. Every SPSS parse
// error names the record it was reading and the byte offset it was
// reading at, so a caller can point at the exact spot in the file.
const (
	// DetailSPSSRecord is the CodedError.Details key carrying the SPSS
	// record a PULSE_SPSS_* error was reading: "header", a decimal record
	// type such as "2" or "999", or "unknown" when the tag itself is the
	// problem.
	DetailSPSSRecord = "record_type"

	// DetailSPSSOffset is the CodedError.Details key carrying the 0-based
	// byte offset into the `.sav` file at which a PULSE_SPSS_* error was
	// detected.
	DetailSPSSOffset = "offset"

	// DetailSPSSSubtype is the CodedError.Details key carrying the record
	// type 7 extension subtype a PULSE_SPSS_EXTENSION_* diagnostic refers
	// to, as an int32.
	DetailSPSSSubtype = "subtype"

	// DetailSPSSBlock is the CodedError.Details key carrying the position
	// of the ZSAV zlib block a PULSE_SPSS_ZSAV_* diagnostic refers to.
	//
	// It is 1-BASED, matching the number the message spells and the rest
	// of the PULSE_SPSS_* family's "item N of M" prose. Subtract one to
	// index the block-index entries themselves.
	DetailSPSSBlock = "block"

	// DetailSPSSDeclaredCases is the CodedError.Details key carrying the
	// case count an SPSS `.sav` file DECLARES — the header ncases field,
	// or the record 7/16 64-bit count where one is present.
	DetailSPSSDeclaredCases = "declared"

	// DetailSPSSActualCases is the CodedError.Details key carrying the
	// number of whole cases an SPSS `.sav` data section actually holds.
	// The schema-mapping diagnostics reuse it for the case count a
	// distinct-value count is measured against.
	DetailSPSSActualCases = "actual"

	// DetailSPSSVariable is the CodedError.Details key carrying the name
	// of the SPSS variable a schema-mapping diagnostic refers to. It
	// deliberately shares the "variable" spelling with DetailVariable —
	// the two families never appear in the same Details map, and a
	// caller reading either finds the name where it expects it.
	DetailSPSSVariable = "variable"

	// DetailSPSSField is the CodedError.Details key carrying the name of
	// the `.pulse` COHORT FIELD a write-side diagnostic came from.
	//
	// It is held apart from DetailSPSSVariable because on the export side
	// the two are not always the same string: a multiple-dichotomy set
	// member's variable name is a dictionary ENTRY of the `set_*` column it
	// was expanded from, so naming only the variable would not say which
	// cohort column to rename.
	DetailSPSSField = "cohort_field"

	// DetailSPSSDistinct is the CodedError.Details key carrying the
	// number of distinct values a variable contributes to a Pulse
	// categorical dictionary.
	DetailSPSSDistinct = "distinct"

	// DetailSPSSFormat is the CodedError.Details key carrying the SPSS
	// print format TYPE CODE (5 = F, 20 = DATE, 22 = DATETIME, and so on)
	// a schema-mapping diagnostic refers to.
	DetailSPSSFormat = "format_code"

	// DetailSPSSCharset is the CodedError.Details key carrying the
	// character encoding a PULSE_SPSS_CHARSET_* diagnostic refers to:
	// the canonical name the reader resolved where it resolved one, and
	// the file's own spelling where it did not.
	DetailSPSSCharset = "charset"

	// DetailSPSSValue is the CodedError.Details key carrying the offending
	// VALUE a PULSE_SPSS_CHARSET_UNENCODABLE diagnostic refers to: the
	// whole string that could not be written, so the caller can find the
	// row that holds it. It is held apart from DetailSPSSVariable, which
	// names the column the value sits in.
	DetailSPSSValue = "value"

	// DetailSPSSWidth is the CodedError.Details key carrying the byte
	// width a value REQUIRES after it has been encoded into the emitted
	// file's charset. Reported with DetailSPSSDeclaredWidth, which
	// carries what is available: a single number could not say which of
	// the two moved.
	DetailSPSSWidth = "width"

	// DetailSPSSDeclaredWidth is the CodedError.Details key carrying the
	// byte width a `.sav` field DECLARES or the format fixes it at, the
	// companion of DetailSPSSWidth.
	DetailSPSSDeclaredWidth = "declared_width"

	// DetailSPSSDerived is the CodedError.Details key carrying the name
	// of a DERIVED column — one the import synthesised rather than read
	// from the file, such as a `<var>_missing` user-missing reason
	// sibling.
	DetailSPSSDerived = "derived"

	// DetailSPSSCollidesWith is the CodedError.Details key carrying the
	// name of the existing variable a derived column's generated name
	// collides with. It is held apart from DetailSPSSVariable, which
	// names the variable the derived column was derived FROM: in a
	// collision those are two different variables and reporting only one
	// of them would not say what to rename.
	DetailSPSSCollidesWith = "collides_with"

	// DetailSPSSMissingMode is the CodedError.Details key carrying the
	// user-missing handling mode a PULSE_SPSS_MISSING_MODE_INVALID
	// refers to, exactly as the caller spelled it.
	DetailSPSSMissingMode = "missing_mode"

	// DetailSPSSMissingCategories is the CodedError.Details key carrying
	// the user-missing dictionary entries of every CATEGORICAL column a
	// PULSE_SPSS_CATEGORICAL_USER_MISSING diagnostic covers: a
	// map[string][]string of Pulse field name to the dictionary entry
	// texts that are missing-coded, in dictionary ID order.
	//
	// The entry texts are what an exclusion filter takes verbatim —
	// FILTER_EXCLUDE resolves its Values through the field's dictionary,
	// which holds the SPSS CODES — so this is the value to exclude and
	// not the label to read.
	DetailSPSSMissingCategories = "missing_categories"

	// DetailSPSSSet is the CodedError.Details key carrying the name of
	// the SPSS multiple-response set a diagnostic refers to, INCLUDING
	// its leading '$'.
	//
	// The '$' is kept because the set name is the identity of the
	// definition in the source file and in the metadata sidecar's
	// multiple_response_sets block. The DERIVED Pulse column drops it —
	// a leading '$' is not a legal expr-lang identifier, so a field named
	// "$media" would be unreachable from ATTR_FORMULA — and that name
	// travels under DetailSPSSDerived instead. Reporting only one of the
	// two would leave a reader unable to connect the column to its
	// declaration.
	DetailSPSSSet = "response_set"

	// DetailSPSSCohort is the CodedError.Details key carrying the path
	// of the `.pulse` COHORT a PULSE_SPSS_SIDECAR_* diagnostic refers
	// to. It is held apart from DetailSPSSSidecar because a sidecar
	// diagnostic is always about a RELATIONSHIP between two files, and
	// naming only one of them would not say what to look at.
	DetailSPSSCohort = "cohort"

	// DetailSPSSSidecar is the CodedError.Details key carrying the path
	// of the metadata SIDECAR a PULSE_SPSS_SIDECAR_* diagnostic refers
	// to — derived from the cohort path by SidecarPath, and reported
	// even for PULSE_SPSS_SIDECAR_ABSENT, where it is the path that was
	// looked for and found empty.
	DetailSPSSSidecar = "sidecar"

	// DetailSPSSExpected is the CodedError.Details key carrying the
	// fingerprint values a metadata sidecar RECORDED for its cohort: a
	// map of "size" and "mod_time" as they stood when the sidecar was
	// written.
	DetailSPSSExpected = "expected_fingerprint"

	// DetailSPSSActual is the CodedError.Details key carrying the
	// fingerprint values the cohort presents NOW, in the same shape as
	// DetailSPSSExpected. The pair is reported rather than a single
	// boolean verdict so a caller can see WHICH of size and mtime moved,
	// which is what distinguishes a rewritten cohort from a touched one.
	DetailSPSSActual = "actual_fingerprint"

	// DetailSPSSRenames is the CodedError.Details key carrying every
	// variable rename --sanitize-names performed, as an ordered list of
	// {"field": <cohort field name>, "name": <emitted SPSS name>}
	// objects in cohort schema order.
	//
	// It is a LIST of pairs rather than a field->name map because the
	// prose caps how many it names and the details must not: a cohort
	// whose every column carries a space would otherwise report a
	// truncated list as if it were the whole of it.
	DetailSPSSRenames = "renames"
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
	PULSE_IMPORT_NULL_PROMOTED,
	PULSE_EXPORT_ROW_ERROR,
	PULSE_EXPORT_FIELD_UNKNOWN,
	PULSE_IMPORT_CATEGORICAL_OVERFLOW,
	PULSE_IMPORT_SET_OVERFLOW,
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
	PULSE_EXTENSION_NAME_INVALID,
	PULSE_EXTENSION_NAME_RESERVED,
	PULSE_EXTENSION_NAME_COLLISION,
	PULSE_EXTENSION_DUPLICATE,
	PULSE_EXTENSION_STREAMABLE_MISMATCH,
	PULSE_EXTENSION_FACTORY_PANIC,
	PULSE_EXTENSION_PARAM_INVALID,
	PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH,
	PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA,
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
	PULSE_COMPOSE_LABEL_COLLISION,
	PULSE_JOIN_TYPE_MISMATCH,
	PULSE_JOIN_KIND_NOT_IMPLEMENTED,
	PULSE_JOIN_FIELD_UNKNOWN,
	PULSE_JOIN_KEYS_EMPTY,
	PULSE_JOIN_TOO_MANY,
	PULSE_JOIN_FIELD_COLLISION,
	PULSE_RANGE_EMPTY,
	PULSE_RANGE_INVALID,
	PULSE_RANGE_DUPLICATE_LABEL,
	PULSE_RANGE_OVERLAP,
	PULSE_RANGE_SOURCE_AMBIGUOUS,
	PULSE_RANGE_TABLE_UNKNOWN,
	PULSE_LABEL_FIELD_UNKNOWN,
	PULSE_LABEL_FIELD_NOT_CATEGORICAL,
	PULSE_LABEL_TABLE_UNKNOWN,
	PULSE_LABEL_FIELD_COLLISION,
	PULSE_LABEL_DUPLICATE_BINDING,
	PULSE_LABEL_TABLE_NOT_ENUMERABLE,
	PULSE_LABEL_COLLISION,
	PULSE_LABEL_LOOKUP_MISS,
	PULSE_CROSSTAB_EMPTY_ROWS,
	PULSE_CROSSTAB_EMPTY_COLUMNS,
	PULSE_CROSSTAB_MISSING_CELL,
	PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS,
	PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE,
	PULSE_CROSSTAB_AGG_UNCLASSIFIED,
	PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE,
	PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS,
	PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE,
	PULSE_CROSSTAB_NORMALIZE_MAP_VALUED,
	PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE,
	PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS,
	PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE,
	PULSE_CROSSTAB_MARGIN_AGG_INVALID,
	PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL,
	PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED,
	PULSE_REQUEST_UNKNOWN_FIELD,
	PULSE_OVERLAY_KIND_UNKNOWN,
	PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
	PULSE_OVERLAY_COMPONENTS_REQUIRED,
	PULSE_OVERLAY_SCOPE_UNSUPPORTED,
	PULSE_OVERLAY_REF_ZERO,
	PULSE_OVERLAY_EXPECTED_LOW,
	PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
	PULSE_OVERLAY_PARAM_MISSING,
	PULSE_OVERLAY_FORMULA_PARSE_ERROR,
	PULSE_OVERLAY_FORMULA_TYPE_MISMATCH,
	PULSE_OVERLAY_FORMULA_INVALID_IDENT,
	PULSE_OVERLAY_REF_UNKNOWN,
	PULSE_OVERLAY_YOY_FREQUENCY_MISSING,
	PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY,
	PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT,
	PULSE_OVERLAY_TARGET_UNKNOWN,
	PULSE_OVERLAY_REFERENCE_UNKNOWN,
	PULSE_OVERLAY_KEY_SET_DIVERGENT,
	PULSE_OVERLAY_SCHEMA_DIVERGENT,
	PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT,
	PULSE_OVERLAY_SLOT_NOT_CROSSTAB,
	PULSE_OVERLAY_DICT_PREFIX_DRIFT,
	PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP,
	PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED,
	PULSE_INDEX_MISSING,
	PULSE_LOOKUP_NOT_FOUND,
	PULSE_LOOKUP_AMBIGUOUS,
	PULSE_INDEX_STALE,
	PULSE_INDEX_UNSUPPORTED_SHARDED,
	PULSE_TEMPLATE_NOT_FOUND,
	PULSE_TEMPLATE_INVALID,
	PULSE_TEMPLATE_TARGET_UNKNOWN,
	PULSE_TEMPLATE_VAR_MISSING,
	PULSE_TEMPLATE_VAR_UNKNOWN,
	PULSE_TEMPLATE_VAR_TYPE,
	PULSE_TEMPLATE_VAR_ENUM,
	PULSE_TEMPLATE_UNRESOLVED,
	PULSE_TEMPLATE_RENDER_INVALID,
	PULSE_SPSS_DICT_INVALID,
	PULSE_SPSS_DICT_TRUNCATED,
	PULSE_SPSS_FILE_EMPTY,
	PULSE_SPSS_ENDIANNESS_MISMATCH,
	PULSE_SPSS_MAGIC_FLAG_MISMATCH,
	PULSE_SPSS_VALUE_LABELS_DROPPED,
	PULSE_SPSS_EXTENSION_UNKNOWN,
	PULSE_SPSS_EXTENSION_INVALID,
	PULSE_SPSS_VERY_LONG_STRING_INVALID,
	PULSE_SPSS_COMPRESSION_UNSUPPORTED,
	PULSE_SPSS_COMPRESSION_INVALID,
	PULSE_SPSS_ZSAV_INVALID,
	PULSE_SPSS_ZSAV_BLOCK_CORRUPT,
	PULSE_SPSS_DATA_TRUNCATED,
	PULSE_SPSS_DATA_CASE_COUNT_MISMATCH,
	PULSE_SPSS_CATEGORICAL_OVERFLOW,
	PULSE_SPSS_CARDINALITY_HIGH,
	PULSE_SPSS_TEMPORAL_PRECISION,
	PULSE_SPSS_DATE_WIDENED,
	PULSE_SPSS_VALUE_COLLISION,
	PULSE_SPSS_MEASURE_LEVEL_MISMATCH,
	PULSE_SPSS_NULL_TOKEN_COLLISION,
	PULSE_SPSS_CHARSET_UNSUPPORTED,
	PULSE_SPSS_CHARSET_INVALID,
	PULSE_SPSS_CHARSET_MISMATCH,
	PULSE_SPSS_CHARSET_UNENCODABLE,
	PULSE_SPSS_WIDTH_OVERFLOW,
	PULSE_SPSS_NAME_INVALID,
	PULSE_SPSS_NAME_COLLISION,
	PULSE_SPSS_COLUMN_UNMAPPED,
	PULSE_SPSS_DERIVED_UNFOLDABLE,
	PULSE_SPSS_EXPORT_UNSUPPORTED,
	PULSE_SPSS_DERIVED_NAME_COLLISION,
	PULSE_SPSS_MISSING_MODE_INVALID,
	PULSE_SPSS_CATEGORICAL_USER_MISSING,
	PULSE_SPSS_MR_SET_NOT_DERIVED,
	PULSE_SPSS_SIDECAR_ABSENT,
	PULSE_SPSS_SIDECAR_STALE,
	PULSE_SPSS_SIDECAR_INVALID,
	PULSE_SPSS_SIDECAR_IGNORED,
	PULSE_SPSS_NAME_SANITIZED,
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
