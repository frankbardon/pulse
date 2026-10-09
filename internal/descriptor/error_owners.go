package descriptor

import (
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// errorOwnerShared is the sentinel owner of a code no feature owns: it
// can arise on a core path (open, inspect, predict, request decode,
// extension registration) or from a request slot every request host
// carries, so it is listed on every instance, the empty one included.
const errorOwnerShared = "shared"

// shared is the owner list of an always-listed code.
var shared = []string{errorOwnerShared}

// own spells an owner list of feature names.
func own(names ...string) []string { return names }

// opsWithPrefix returns every built-in operator feature whose name starts
// with prefix, in table order: the owner list of a code every member of
// one operator family can raise.
func opsWithPrefix(prefix string) []string {
	var out []string
	for _, f := range builtinFeatures {
		if f.Kind == FeatureKindOperator && strings.HasPrefix(f.Name, prefix) {
			out = append(out, f.Name)
		}
	}
	return out
}

// plus concatenates owner lists into a fresh slice.
func plus(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

var (
	ownAllTests    = opsWithPrefix("TEST_")
	ownAllOverlays = opsWithPrefix("OVERLAY_")
	ownAllWindows  = opsWithPrefix("WIN_")
	ownAllRegs     = opsWithPrefix("REG_")
	// ownAllMats: every matrix operator (and capability:matrices, which
	// each depends on) — a code only a `matrices` slot can raise.
	ownAllMats = plus(own(featMatrices), opsWithPrefix("MAT_"))
	// ownImportExport: the tabular import core (inference, row decode,
	// width promotion, overrides) runs under the managed import pool
	// AND under Convert / ImportTransfer, which capability:export gates.
	ownImportExport = own(featImport, featExport)
	// ownExpr: the expr-lang environment (lookup tables, expr functions)
	// is reached from the three expression operators; FilterToFile
	// compiles to FILTER_EXPRESSION, which it hard-depends on.
	ownExpr = own("ATTR_FORMULA", "FILTER_EXPRESSION", "OVERLAY_FORMULA")
	// ownChiSq: the contingency-table checks run in the chi-square /
	// Fisher row tests and in the crosstab chi-square overlays.
	ownChiSq = own("TEST_CHISQ", "TEST_FISHER_EXACT", "OVERLAY_CHISQ_COL", "OVERLAY_CHISQ_MATRIX",
		"OVERLAY_CHISQ_ROW", "OVERLAY_CHISQ_VS_POP", "OVERLAY_CHISQ_VS_REF", "OVERLAY_FISHER_EXACT_CELL")
	ownSPSS       = own(FeatureName(FeatureKindIOFormat, "spss"))
	featTemplates = FeatureName(FeatureKindCapability, "templates")
)

// errorOwners maps every registered error code to the features that own
// it — the code is hidden on an instance iff EVERY owner is hidden — or
// to the shared sentinel. TestErrorOwners_Complete pins it to
// errors.AllCodes() in both directions and resolves every owner.
//
// Classification rule: when a code can arise from a core path or from a
// request slot that is not feature-gated, it is shared. An owner list is
// the union of every feature whose execution can raise the code; a code
// raised by every member of an operator family is owned by the whole
// family (hidden only when the family is). Codes that report an UNKNOWN
// name of a family (PULSE_TEST_UNKNOWN_TYPE, PULSE_OVERLAY_KIND_UNKNOWN,
// SERVICE_REGISTRY) are shared: a hidden name must fail exactly like a
// never-registered one, so they stay reachable when the whole family is
// hidden.
var errorOwners = map[errors.Code][]string{
	// ENCODING / PROCESSING / SERVICE / DATA / CLI — the engine floor.
	errors.ENCODING_INVALID:       shared,
	errors.ENCODING_IO:            shared,
	errors.ENCODING_TYPE_MISMATCH: shared,
	errors.ENCODING_INTERNAL:      shared,
	errors.PROCESSING_CONFIG:      shared,
	errors.PROCESSING_STATE:       shared,
	errors.PROCESSING_RUNTIME:     shared,
	errors.PROCESSING_GROUP:       shared,
	errors.PROCESSING_INTERNAL:    shared,
	// Regression engine: ATTR_REG_* reach it only through REG_OLS (a hard
	// dependency), so the REG_* family covers every path.
	errors.PROCESSING_REGRESSION_NOT_IMPLEMENTED:   ownAllRegs,
	errors.PROCESSING_REGRESSION_RANK_DEFICIENT:    ownAllRegs,
	errors.PROCESSING_REGRESSION_NO_CONVERGE:       ownAllRegs,
	errors.PROCESSING_REGRESSION_SINGULAR_GRAM:     ownAllRegs,
	errors.PROCESSING_REGRESSION_INVALID_FAMILY:    own("REG_GLM"),
	errors.PROCESSING_REGRESSION_INVALID_LINK:      own("REG_GLM"),
	errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA: ownAllRegs,
	errors.SERVICE_VALIDATION:                      shared,
	errors.SERVICE_RESOURCE:                        shared,
	errors.SERVICE_REGISTRY:                        shared,
	errors.SERVICE_INTERNAL:                        shared,
	errors.DATA_FILE:                               shared,
	errors.DATA_PARSE:                              shared,
	errors.DATA_CONFIG:                             shared,
	errors.DATA_CALCULATION:                        shared,
	errors.DATA_INTERNAL:                           shared,
	errors.CLI_INPUT:                               shared,
	errors.CLI_OUTPUT:                              shared,
	errors.CLI_COMMAND:                             shared,
	errors.CLI_INTERNAL:                            shared,

	// Import / export.
	errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS:     ownImportExport,
	errors.PULSE_IMPORT_ROW_ERROR:            ownImportExport,
	errors.PULSE_IMPORT_NULL_PROMOTED:        ownImportExport,
	errors.PULSE_IMPORT_WIDTH_PROMOTED:       ownImportExport,
	errors.PULSE_IMPORT_OVERRIDE_INVALID:     ownImportExport,
	errors.PULSE_IMPORT_DST_AMBIGUOUS:        ownImportExport,
	errors.PULSE_IMPORT_DST_NONEXISTENT:      ownImportExport,
	errors.PULSE_IMPORT_DST_RESOLVED:         ownImportExport,
	errors.PULSE_IMPORT_TIMESTAMP_TRUNCATED:  ownImportExport,
	errors.PULSE_EXPORT_ROW_ERROR:            own(featExport),
	errors.PULSE_EXPORT_FIELD_UNKNOWN:        own(featExport),
	errors.PULSE_IMPORT_CATEGORICAL_OVERFLOW: ownImportExport,
	// The synth writer refuses an over-wide set dictionary with it too.
	errors.PULSE_IMPORT_SET_OVERFLOW:          plus(ownImportExport, own(featSynth)),
	errors.PULSE_IMPORT_CATEGORICAL_UNBOUNDED: ownImportExport,
	// The 1000-byte description cap is a header rule every cohort writer
	// (synth, shard, filter-to-file, widen) enforces.
	errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG: shared,
	errors.PULSE_IMPORT_FORMAT_UNKNOWN:       own(featImport),
	errors.PULSE_IO_FORMAT_UNSUPPORTED:       ownImportExport,
	errors.PULSE_IMPORT_SOURCE_MISSING:       own(featImport),
	errors.PULSE_IMPORT_HANDLE_EXISTS:        own(featImport),
	errors.PULSE_IMPORT_SOURCE_FORBIDDEN:     own(featImport),

	// Parent groups: declared at import / dedup, rewritten by shards and
	// read back by inspect on any grouped cohort — the file format, not
	// one feature.
	errors.PULSE_GROUP_DECLARATION_INVALID: shared,
	errors.PULSE_GROUP_FIELD_UNKNOWN:       shared,
	errors.PULSE_GROUP_FIELD_CONFLICT:      shared,
	errors.PULSE_GROUP_MEMBER_NOT_CONSTANT: shared,
	errors.PULSE_GROUP_ENTRIES_EXHAUSTED:   shared,
	errors.PULSE_GROUP_TOO_NARROW:          shared,
	errors.PULSE_DEDUP_LOW_RATIO:           shared,

	// Any numeric aggregator on a categorical field; predict raises it.
	errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL: shared,
	errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY:      shared,
	errors.PULSE_WINDOW_INVALID:                     ownAllWindows,
	errors.PULSE_FEAT_TARGET_LEAKAGE_RISK:           own("FEAT_TARGET_ENCODE"),
	// decimal128 arithmetic: every decimal aggregation, import and synth.
	errors.PULSE_DECIMAL_OVERFLOW:               shared,
	errors.PULSE_DECIMAL_PRECISION_LOSS:         shared,
	errors.PULSE_DECIMAL_DIVIDE_BY_ZERO:         shared,
	errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL: shared,
	// Predict only: an ATTR_CODE_IN code absent from a categorical dictionary.
	errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY: own("ATTR_CODE_IN"),
	// Predict advisories: the two-group rule fires only on the two
	// two-sample tests; the many-tests rule needs the p-value count,
	// which a hidden capability:multiplicity withholds. The suppression
	// refusal is raised by pulse.New on every instance.
	errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS: own("TEST_T", "TEST_WELCH"),
	errors.PULSE_ADVISORY_MANY_TESTS:                 own(featMultiplicity),
	errors.PULSE_SUPPRESS_ADVISORY_UNKNOWN:           shared,

	// Synth (data-profile capture included).
	errors.PULSE_SYNTH_DISTRIBUTION_UNKNOWN:    own(featSynth),
	errors.PULSE_SYNTH_CONSTRAINT_INFEASIBLE:   own(featSynth),
	errors.PULSE_SYNTH_SOURCE_REQUIRED:         own(featSynth),
	errors.PULSE_SYNTH_OUTPUT_REQUIRED:         own(featSynth),
	errors.PULSE_SYNTH_OUTPUT_COLLISION:        own(featSynth),
	errors.PULSE_SYNTH_ALREADY_TAGGED:          own(featSynth),
	errors.PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH: own(featSynth),
	errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN:      own(featSynth),
	errors.PULSE_SYNTH_RULE_EXPR_INVALID:       own(featSynth),
	errors.PULSE_SYNTH_RULE_VALUE_INVALID:      own(featSynth),
	errors.PULSE_SYNTH_RULE_EMPTY:              own(featSynth),
	errors.PULSE_SYNTH_RULE_CONFLICT:           own(featSynth),
	errors.PULSE_SYNTH_RULE_BLOCK_INVALID:      own(featSynth),
	errors.PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE: own(featSynth),
	errors.PULSE_SYNTH_RULE_OWNERSHIP_INVALID:  own(featSynth),
	errors.PULSE_PROFILE_FIELD_UNSUPPORTED:     own(featSynth),

	// Statistical tests. The unknown-name code is shared (see above);
	// the per-test codes name only the tests that raise them.
	errors.PULSE_TEST_UNKNOWN_TYPE:             shared,
	errors.PULSE_TEST_FIELD_NOT_NUMERIC:        ownAllTests,
	errors.PULSE_TEST_INVALID_ALPHA:            ownAllTests,
	errors.PULSE_TEST_INSUFFICIENT_N:           ownAllTests,
	errors.PULSE_TEST_VARIANCE_ZERO:            ownAllTests,
	errors.PULSE_TEST_SPLIT_GROUPS_LT_2:        ownAllTests,
	errors.PULSE_TEST_CONTINGENCY_DEGENERATE:   ownChiSq,
	errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW:   ownChiSq,
	errors.PULSE_TEST_FIELD2_NOT_NUMERIC:       own("TEST_PEARSON_R", "TEST_SPEARMAN_R", "TEST_KENDALL_TAU", "TEST_PAIRED_T", "TEST_WILCOXON_SR"),
	errors.PULSE_TEST_SUCCESS_VALUE_MISSING:    own("TEST_PROP_Z"),
	errors.PULSE_TEST_CORRELATION_UNDEFINED:    ownAllTests,
	errors.PULSE_TEST_PAIRED_LENGTH_MISMATCH:   own("TEST_PAIRED_T", "TEST_WILCOXON_SR"),
	errors.PULSE_TEST_TIES_DOMINATE:            ownAllTests,
	errors.PULSE_TEST_SUBJECT_MISSING:          own("TEST_ANOVA_RM"),
	errors.PULSE_TEST_BALANCED_DESIGN_REQUIRED: own("TEST_ANOVA_RM"),
	errors.PULSE_TEST_TUKEY_REQUIRES_K_GE_3:    own("TEST_TUKEY_HSD"),
	errors.PULSE_TEST_SHAPIRO_N_BOUND:          own("TEST_SHAPIRO_WILK"),
	errors.PULSE_TEST_FISHER_R_OR_C_GT_2:       own("TEST_FISHER_EXACT"),

	// Extension registration runs at pulse.New on every instance.
	errors.PULSE_EXTENSION_NAME_INVALID:                 shared,
	errors.PULSE_EXTENSION_NAME_RESERVED:                shared,
	errors.PULSE_EXTENSION_NAME_COLLISION:               shared,
	errors.PULSE_EXTENSION_DUPLICATE:                    shared,
	errors.PULSE_EXTENSION_STREAMABLE_MISMATCH:          shared,
	errors.PULSE_EXTENSION_FANOUT_MISMATCH:              shared,
	errors.PULSE_EXTENSION_PURPOSE_INVALID:              shared,
	errors.PULSE_EXTENSION_SKILL_INVALID:                shared,
	errors.PULSE_EXTENSION_SKILL_COLLISION:              shared,
	errors.PULSE_EXTENSION_EXAMPLE_INVALID:              shared,
	errors.PULSE_EXTENSION_EXAMPLE_COLLISION:            shared,
	errors.PULSE_EXTENSION_MERGEABLE_MISMATCH:           shared,
	errors.PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH: shared,
	errors.PULSE_EXTENSION_FACTORY_PANIC:                shared,
	errors.PULSE_EXTENSION_PARAM_INVALID:                shared,
	errors.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH:    shared,
	errors.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA:     shared,
	errors.PULSE_LOOKUP_TABLE_UNKNOWN:                   ownExpr,
	errors.PULSE_LOOKUP_MISS:                            ownExpr,

	// Opening a cohort (single file or archive) is core.
	errors.PULSE_ARCHIVE_MAGIC_INVALID: shared,
	errors.PULSE_ARCHIVE_CORRUPT:       shared,
	errors.PULSE_COHORT_COMPRESSED:     shared,
	errors.PULSE_TRANSFER_INVALID:      own(featExport),
	// Reading an archive (open / inspect / predict / count) raises the
	// structural shard codes; the cohesion, widen and rewrite codes come
	// only from the shard-archive methods.
	errors.PULSE_SHARD_MISSING:                shared,
	errors.PULSE_SHARD_HEADER_INVALID:         shared,
	errors.PULSE_SHARD_SCHEMA_MISMATCH:        own(featShard),
	errors.PULSE_SHARD_DICT_DIVERGENCE:        own(featShard),
	errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW:    own(featShard),
	errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE: own(featShard),
	errors.PULSE_SHARD_SET_WIDENED:            own(featShard),
	errors.PULSE_SHARD_GROUPS_REWRITTEN:       own(featShard),
	errors.PULSE_SHARD_RESERVED_NAME:          shared,
	errors.PULSE_SHARD_NAME_COLLISION:         own(featShard),

	errors.PULSE_CHAIN_NOT_MERGEABLE:     own(featProcessChain),
	errors.PULSE_CHAIN_EMPTY:             own(featProcessChain),
	errors.PULSE_CHAIN_STAGE_JOIN:        own(featProcessChain),
	errors.PULSE_COMPOSE_LABEL_COLLISION: own(featCompose),

	errors.PULSE_JOIN_TYPE_MISMATCH:        own(featJoins),
	errors.PULSE_JOIN_KIND_NOT_IMPLEMENTED: own(featJoins),
	errors.PULSE_JOIN_FIELD_UNKNOWN:        own(featJoins),
	errors.PULSE_JOIN_KEYS_EMPTY:           own(featJoins),
	errors.PULSE_JOIN_TOO_MANY:             own(featJoins),
	errors.PULSE_JOIN_FIELD_COLLISION:      own(featJoins),

	// Range compilation runs for a named range table (loaded under
	// capability:range_tables) and for the inline ranges of the two
	// date-range operators; table resolution only from the operators.
	errors.PULSE_RANGE_EMPTY:            own(featRangeTables, "GROUP_DATE_RANGES", "FILTER_DATE_RANGES"),
	errors.PULSE_RANGE_INVALID:          own(featRangeTables, "GROUP_DATE_RANGES", "FILTER_DATE_RANGES"),
	errors.PULSE_RANGE_DUPLICATE_LABEL:  own(featRangeTables, "GROUP_DATE_RANGES", "FILTER_DATE_RANGES"),
	errors.PULSE_RANGE_OVERLAP:          own(featRangeTables, "GROUP_DATE_RANGES", "FILTER_DATE_RANGES"),
	errors.PULSE_RANGE_SOURCE_AMBIGUOUS: own("GROUP_DATE_RANGES", "FILTER_DATE_RANGES"),
	errors.PULSE_RANGE_TABLE_UNKNOWN:    own("GROUP_DATE_RANGES", "FILTER_DATE_RANGES"),

	// Label bindings ride the request's labels slot (part of every
	// request host, not capability:labels) and sample / facet output, so
	// every binding code is shared; only enumerating a table through the
	// label-resolve surface belongs to capability:labels.
	errors.PULSE_LABEL_FIELD_UNKNOWN:         shared,
	errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL: shared,
	errors.PULSE_LABEL_TABLE_UNKNOWN:         shared,
	errors.PULSE_LABEL_FIELD_COLLISION:       shared,
	errors.PULSE_LABEL_DUPLICATE_BINDING:     shared,
	errors.PULSE_LABEL_TABLE_NOT_ENUMERABLE:  own(featLabels),
	errors.PULSE_LABEL_COLLISION:             shared,
	errors.PULSE_LABEL_LOOKUP_MISS:           shared,

	errors.PULSE_CROSSTAB_EMPTY_ROWS:                          own(featCrosstab),
	errors.PULSE_CROSSTAB_EMPTY_COLUMNS:                       own(featCrosstab),
	errors.PULSE_CROSSTAB_MISSING_CELL:                        own(featCrosstab),
	errors.PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS:               own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE:             own(featCrosstab),
	errors.PULSE_CROSSTAB_AGG_UNCLASSIFIED:                    own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE:        own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS: own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE:        own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_MAP_VALUED:                own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE:       own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS:       own(featCrosstab),
	errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE:       own(featCrosstab),
	errors.PULSE_CROSSTAB_MARGIN_AGG_INVALID:                  own(featCrosstab),
	errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL:          own(featCrosstab),
	errors.PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED:               own(featCrosstab),

	// The hidden-slot refusal itself.
	errors.PULSE_REQUEST_UNKNOWN_FIELD: shared,

	// Overlays. The unknown-kind code is shared; a code every host's
	// handlers can raise is owned by the whole OVERLAY_* family (a
	// per-kind split would hide a code another enabled kind still
	// raises); host-specific codes name their host capability; the few
	// raised outside overlay dispatch (crosstab normalize, facet
	// overlay plumbing) add that capability.
	errors.PULSE_OVERLAY_KIND_UNKNOWN:                  shared,
	errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE:   ownAllOverlays,
	errors.PULSE_OVERLAY_COMPONENTS_REQUIRED:           plus(ownAllOverlays, own(featCrosstab)),
	errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED:             ownAllOverlays,
	errors.PULSE_OVERLAY_REF_ZERO:                      ownAllOverlays,
	errors.PULSE_OVERLAY_EXPECTED_LOW:                  ownAllOverlays,
	errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE:            plus(ownAllOverlays, own(featCrosstab)),
	errors.PULSE_OVERLAY_PARAM_MISSING:                 plus(ownAllOverlays, own(featCrosstab, featFacet)),
	errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR:           own("OVERLAY_FORMULA"),
	errors.PULSE_OVERLAY_FORMULA_TYPE_MISMATCH:         own("OVERLAY_FORMULA"),
	errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT:         own("OVERLAY_FORMULA"),
	errors.PULSE_OVERLAY_REF_UNKNOWN:                   ownAllOverlays,
	errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING:         own("OVERLAY_YOY"),
	errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY:    own("OVERLAY_YOY"),
	errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT:   own(featProcessChain),
	errors.PULSE_OVERLAY_TARGET_UNKNOWN:                own(featCompose, featProcessChain),
	errors.PULSE_OVERLAY_REFERENCE_UNKNOWN:             own(featCompose, featProcessChain),
	errors.PULSE_OVERLAY_KEY_SET_DIVERGENT:             own(featCompose),
	errors.PULSE_OVERLAY_SCHEMA_DIVERGENT:              own(featCompose),
	errors.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT:          own(featCompose, featCrosstab),
	errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB:             own(featCompose),
	errors.PULSE_OVERLAY_DICT_PREFIX_DRIFT:             own(featCompose),
	errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP:        own("OVERLAY_PROP_Z_PANEL", "OVERLAY_PANEL_INDEX_VS_REF"),
	errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED: own(featCrosstab, featCompose),
	errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED:        own(featExport),

	// Point lookup and its sidecar index.
	errors.PULSE_INDEX_MISSING:             own(featIndex, featLookup),
	errors.PULSE_LOOKUP_NOT_FOUND:          own(featLookup),
	errors.PULSE_LOOKUP_AMBIGUOUS:          own(featLookup),
	errors.PULSE_INDEX_STALE:               own(featIndex, featLookup),
	errors.PULSE_INDEX_UNSUPPORTED_SHARDED: own(featIndex, featLookup),
	errors.PULSE_INDEX_MANIFEST_INVALID:    own(featIndex, featLookup),
	errors.PULSE_INDEX_MANIFEST_STALE:      own(featIndex, featLookup),

	errors.PULSE_TEMPLATE_NOT_FOUND:      own(featTemplates),
	errors.PULSE_TEMPLATE_INVALID:        own(featTemplates),
	errors.PULSE_TEMPLATE_TARGET_UNKNOWN: own(featTemplates),
	errors.PULSE_TEMPLATE_VAR_MISSING:    own(featTemplates),
	errors.PULSE_TEMPLATE_VAR_UNKNOWN:    own(featTemplates),
	errors.PULSE_TEMPLATE_VAR_TYPE:       own(featTemplates),
	errors.PULSE_TEMPLATE_VAR_ENUM:       own(featTemplates),
	errors.PULSE_TEMPLATE_UNRESOLVED:     own(featTemplates),
	errors.PULSE_TEMPLATE_RENDER_INVALID: own(featTemplates),

	// SPSS: every code is raised by the .sav / .zsav reader or writer or
	// the SPSS metadata sidecar, all gated by io_format:spss.
	errors.PULSE_SPSS_DICT_INVALID:             ownSPSS,
	errors.PULSE_SPSS_DICT_TRUNCATED:           ownSPSS,
	errors.PULSE_SPSS_FILE_EMPTY:               ownSPSS,
	errors.PULSE_SPSS_ENDIANNESS_MISMATCH:      ownSPSS,
	errors.PULSE_SPSS_MAGIC_FLAG_MISMATCH:      ownSPSS,
	errors.PULSE_SPSS_VALUE_LABELS_DROPPED:     ownSPSS,
	errors.PULSE_SPSS_EXTENSION_UNKNOWN:        ownSPSS,
	errors.PULSE_SPSS_EXTENSION_INVALID:        ownSPSS,
	errors.PULSE_SPSS_VERY_LONG_STRING_INVALID: ownSPSS,
	errors.PULSE_SPSS_COMPRESSION_UNSUPPORTED:  ownSPSS,
	errors.PULSE_SPSS_COMPRESSION_INVALID:      ownSPSS,
	errors.PULSE_SPSS_ZSAV_INVALID:             ownSPSS,
	errors.PULSE_SPSS_ZSAV_BLOCK_CORRUPT:       ownSPSS,
	errors.PULSE_SPSS_DATA_TRUNCATED:           ownSPSS,
	errors.PULSE_SPSS_DATA_CASE_COUNT_MISMATCH: ownSPSS,
	errors.PULSE_SPSS_CATEGORICAL_OVERFLOW:     ownSPSS,
	errors.PULSE_SPSS_CARDINALITY_HIGH:         ownSPSS,
	errors.PULSE_SPSS_TEMPORAL_PRECISION:       ownSPSS,
	errors.PULSE_SPSS_DATE_WIDENED:             ownSPSS,
	errors.PULSE_SPSS_VALUE_COLLISION:          ownSPSS,
	errors.PULSE_SPSS_MEASURE_LEVEL_MISMATCH:   ownSPSS,
	errors.PULSE_SPSS_NULL_TOKEN_COLLISION:     ownSPSS,
	errors.PULSE_SPSS_CHARSET_UNSUPPORTED:      ownSPSS,
	errors.PULSE_SPSS_CHARSET_INVALID:          ownSPSS,
	errors.PULSE_SPSS_CHARSET_MISMATCH:         ownSPSS,
	errors.PULSE_SPSS_CHARSET_UNENCODABLE:      ownSPSS,
	errors.PULSE_SPSS_WIDTH_OVERFLOW:           ownSPSS,
	errors.PULSE_SPSS_NAME_INVALID:             ownSPSS,
	errors.PULSE_SPSS_NAME_COLLISION:           ownSPSS,
	errors.PULSE_SPSS_COLUMN_UNMAPPED:          ownSPSS,
	errors.PULSE_SPSS_DERIVED_UNFOLDABLE:       ownSPSS,
	errors.PULSE_SPSS_EXPORT_UNSUPPORTED:       ownSPSS,
	errors.PULSE_SPSS_DERIVED_NAME_COLLISION:   ownSPSS,
	errors.PULSE_SPSS_MISSING_MODE_INVALID:     ownSPSS,
	errors.PULSE_SPSS_CATEGORICAL_USER_MISSING: ownSPSS,
	errors.PULSE_SPSS_MR_SET_NOT_DERIVED:       ownSPSS,
	errors.PULSE_SPSS_SIDECAR_ABSENT:           ownSPSS,
	errors.PULSE_SPSS_SIDECAR_STALE:            ownSPSS,
	errors.PULSE_SPSS_SIDECAR_INVALID:          ownSPSS,
	errors.PULSE_SPSS_SIDECAR_IGNORED:          ownSPSS,
	errors.PULSE_SPSS_NAME_SANITIZED:           ownSPSS,

	errors.PULSE_TIMEZONE_UNKNOWN: shared,

	// The feature-profile codes stay listed on every instance.
	errors.PULSE_FEATURE_PROFILE_INVALID:    shared,
	errors.PULSE_FEATURE_PROFILE_UNKNOWN:    shared,
	errors.PULSE_FEATURE_PROFILE_DEPENDENCY: shared,

	// Row weighting is capability:weighting. AGG_WEIGHTED_MEAN co-owns
	// the two codes its params.weight_field sugar reaches with the
	// capability hidden (weight_field stays ungated): the invalid-row
	// warning, and the decimal128 refusal. An extension's weight
	// awareness is judged only under a weight in force, which a hidden
	// capability never has (every `weight` key and Options.DefaultWeight
	// are refused).
	errors.PULSE_WEIGHT_INVALID_ROWS:        own(featWeighting, "AGG_WEIGHTED_MEAN"),
	errors.PULSE_WEIGHT_UNSUPPORTED:         own(featWeighting, "AGG_WEIGHTED_MEAN"),
	errors.PULSE_WEIGHT_LOW_NEFF:            own(featWeighting),
	errors.PULSE_EXTENSION_NOT_WEIGHT_AWARE: own(featWeighting),

	// Multiple-comparison correction is capability:multiplicity: with
	// it hidden every `multiplicity` key and
	// Options.DefaultMultiplicity are refused before either code can
	// fire.
	errors.PULSE_MULTIPLICITY_INVALID:  own(featMultiplicity),
	errors.PULSE_MULTIPLICITY_CONFLICT: own(featMultiplicity),

	// The linear-algebra core is reachable from more than one feature
	// (regression, synthetic data, later matrix operators) and from no
	// single request slot, so its codes are always listed.
	errors.PULSE_MATRIX_SINGULAR:       shared,
	errors.PULSE_MATRIX_SHAPE_MISMATCH: shared,

	// Virtual vectors are capability:matrices: with it hidden the
	// `vectors` slot is refused as an unknown field before any vector is
	// resolved, so no PULSE_VECTOR_* code can fire.
	errors.PULSE_VECTOR_INVALID:         own(featMatrices),
	errors.PULSE_VECTOR_EMPTY:           own(featMatrices),
	errors.PULSE_VECTOR_MEMBER_TYPE:     own(featMatrices),
	errors.PULSE_VECTOR_DUPLICATE:       own(featMatrices),
	errors.PULSE_VECTOR_LABELS_MISMATCH: own(featMatrices),
	errors.PULSE_VECTOR_UNKNOWN:         own(featMatrices),
	errors.PULSE_VECTOR_UNREFERENCED:    own(featMatrices),

	// The matrix-source refusals fire only on a request carrying the
	// `matrices` slot, which capability:matrices gates; the MAT_ family
	// raises them alike.
	errors.PULSE_MATRIX_UNSUPPORTED_SOURCE: ownAllMats,
	errors.PULSE_MATRIX_HOST_CONFLICT:      ownAllMats,
	// The per-matrix data-quality warnings ride MatrixResult.Warnings,
	// which only a MAT_ operator emits.
	errors.PULSE_MATRIX_NOT_PSD:             ownAllMats,
	errors.PULSE_MATRIX_LISTWISE_HEAVY_DROP: ownAllMats,
	errors.PULSE_MATRIX_INSUFFICIENT_N:      ownAllMats,
	errors.PULSE_MATRIX_ZERO_VARIANCE:       ownAllMats,

	// Response shaping: `return` is a plain request slot, not a feature.
	errors.PULSE_RETURN_INVALID:        shared,
	errors.PULSE_RETURN_PATH_UNKNOWN:   shared,
	errors.PULSE_RETURN_PATH_UNMATCHED: shared,
	errors.PULSE_LIMIT_INVALID:         shared,
	errors.PULSE_LIMIT_EXCEEDED:        shared,

	// The reference export (Pulse.ExportReference, `pulse docs
	// export`) is an ungated core surface every instance offers.
	errors.PULSE_DOCS_EXPORT_DIR_NOT_EMPTY: shared,
}

// errorCodeVisible reports whether the instance whose offer predicate is
// on lists code c: a shared or unclassified code always, an owned code
// iff at least one owner is offered.
func errorCodeVisible(c errors.Code, on func(string) bool) bool {
	owners, ok := errorOwners[c]
	if !ok {
		return true
	}
	for _, o := range owners {
		if o == errorOwnerShared || on(o) {
			return true
		}
	}
	return false
}
