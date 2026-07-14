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
