package descriptor

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/frankbardon/pulse/types"
)

// FeatureKind classifies one row of the feature table. It is the
// vocabulary feature profiles are written in: a profile lists feature
// NAMES, and the kind decides how a name is spelled and which surface it
// gates.
type FeatureKind string

// The four feature kinds. Operators are spelled bare (their registered
// SCREAMING_SNAKE name); every other kind is spelled `<kind>:<name>`.
const (
	// FeatureKindCapability is an engine capability: a facade method
	// family, its CLI leaves and MCP tools, or a request slot.
	FeatureKindCapability FeatureKind = "capability"
	// FeatureKindOperator is a registered operator — every
	// types.All*Types() entry (AGG/ATTR/FILTER/GROUP/WIN/FEAT/TEST/REG/
	// OVERLAY). Extension operators are operators too, but they are not
	// rows of this table: they carry no Since.
	FeatureKindOperator FeatureKind = "operator"
	// FeatureKindIOFormat is one tabular I/O format, gating BOTH its
	// import and its export direction.
	FeatureKindIOFormat FeatureKind = "io_format"
	// FeatureKindMCPExtra is an MCP surface that is neither a tool nor
	// core: a prompt or the cohort-resource enumeration.
	FeatureKindMCPExtra FeatureKind = "mcp_extra"
)

// AllFeatureKinds returns the four kinds in a stable order.
func AllFeatureKinds() []FeatureKind {
	return []FeatureKind{
		FeatureKindCapability,
		FeatureKindOperator,
		FeatureKindIOFormat,
		FeatureKindMCPExtra,
	}
}

// BuiltinFeatureSince is the Since every built-in feature carries: the
// v1.0.0 line is the baseline the feature table starts from.
const BuiltinFeatureSince = "1.0.0"

// Feature is one row of the internal feature table. It is deliberately
// NOT a field on any public struct (descriptor.Operator and friends stay
// untouched), so the manifest and public API goldens do not move.
type Feature struct {
	// Name is the one spelling a profile uses: bare for operators,
	// `<kind>:<name>` for every other kind. There are no aliases.
	Name string
	// Kind classifies the row.
	Kind FeatureKind
	// Since is the Pulse release (major.minor.patch) that introduced the
	// feature.
	Since string
	// DependsOn is the feature's dependency expression: the AND of its
	// groups, each group an any-of set of feature names. A feature is
	// usable only when, for EVERY group, at least one named feature is
	// also present. nil means no dependency. Rows never spell this by
	// hand — withDependencies derives it from the host rules and the
	// hard-edge table below.
	DependsOn [][]string
}

// FeatureName spells a feature of kind k whose bare name is bare: the
// bare name itself for an operator, `<kind>:<bare>` otherwise.
func FeatureName(k FeatureKind, bare string) string {
	if k == FeatureKindOperator {
		return bare
	}
	return string(k) + ":" + bare
}

func capability(bare string) Feature {
	return Feature{Name: FeatureName(FeatureKindCapability, bare), Kind: FeatureKindCapability, Since: BuiltinFeatureSince}
}

func ioFormat(bare string) Feature {
	return Feature{Name: FeatureName(FeatureKindIOFormat, bare), Kind: FeatureKindIOFormat, Since: BuiltinFeatureSince}
}

func mcpExtra(bare string) Feature {
	return Feature{Name: FeatureName(FeatureKindMCPExtra, bare), Kind: FeatureKindMCPExtra, Since: BuiltinFeatureSince}
}

func op(name string) Feature {
	return Feature{Name: name, Kind: FeatureKindOperator, Since: BuiltinFeatureSince}
}

// Capability feature names, referenced by the MCP tool and command
// binding tables and the instance manifest.
var (
	featProcess       = FeatureName(FeatureKindCapability, "process")
	featCompose       = FeatureName(FeatureKindCapability, "compose")
	featComposeSweep  = FeatureName(FeatureKindCapability, "compose_sweep")
	featComposeFits   = FeatureName(FeatureKindCapability, "compose_fits")
	featComposeBanner = FeatureName(FeatureKindCapability, "compose_banner")
	featModelSave     = FeatureModelSave
	featModels        = FeatureModels
	featForEach       = FeatureForEach
	featProcessChain  = FeatureName(FeatureKindCapability, "process_chain")
	featFacet         = FeatureName(FeatureKindCapability, "facet")
	featSample        = FeatureName(FeatureKindCapability, "sample")
	featLookup        = FeatureName(FeatureKindCapability, "lookup")
	featImport        = FeatureName(FeatureKindCapability, "import")
	featDedup         = FeatureName(FeatureKindCapability, "dedup")
	featMaterialize   = FeatureName(FeatureKindCapability, "materialize")
	featRake          = FeatureName(FeatureKindCapability, "rake")
	featRecommend     = FeatureName(FeatureKindCapability, "recommend")
	featExplain       = FeatureName(FeatureKindCapability, "explain")
	featLabels        = FeatureName(FeatureKindCapability, "labels")
	featRangeTables   = FeatureName(FeatureKindCapability, "range_tables")
	featCrosstab      = FeatureName(FeatureKindCapability, "crosstab")
	featCrosstabCells = FeatureCrosstabCells
	featJoins         = FeatureName(FeatureKindCapability, "joins")
	featStream        = FeatureName(FeatureKindCapability, "stream")
	featWatch         = FeatureName(FeatureKindCapability, "watch")
	featFilterToFile  = FeatureName(FeatureKindCapability, "filter_to_file")
	featIndex         = FeatureName(FeatureKindCapability, "index")
	featShard         = FeatureName(FeatureKindCapability, "shard")
	featWiden         = FeatureName(FeatureKindCapability, "widen")
	featSynth         = FeatureName(FeatureKindCapability, "synth")
	featExport        = FeatureName(FeatureKindCapability, "export")
	featWeighting     = FeatureWeighting
	featMultiplicity  = FeatureMultiplicity
	featMatrices      = FeatureName(FeatureKindCapability, "matrices")
)

// FeatureMultiplicity is capability:multiplicity — the multiple-
// comparison correction surface: every `multiplicity` slot (Request,
// Test, OverlaySpec, ComposeOverlaySpec, ComposedRequest) and
// pulse.Options.DefaultMultiplicity. Exported because the root facade
// (DefaultMultiplicity refusal) reads it too.
const FeatureMultiplicity = "capability:multiplicity"

// FeatureCrosstabCells is capability:crosstab_cells — the multi-measure
// crosstab: the `cells` key on CrosstabSpec (several measures as stub
// rows) and the MEASURE-folded payload slots it produces
// (MatrixPayload.cell_labels / measures, CrosstabComponents.measures).
// Exported for the MCP bound schema.
const FeatureCrosstabCells = "capability:crosstab_cells"

// FeatureForEach is capability:for_each — the item fan-out: the
// `for_each` key on Window, Feature and Attribute. Exported because the
// MCP binder (nested for_each properties) reads it.
const FeatureForEach = "capability:for_each"

// FeatureModelSave is capability:model_save — the `save` key on a
// RegressionSpec and a ComposedRequest `fits` entry, which writes the
// fit as a model file. Exported for the MCP bound schema.
const FeatureModelSave = "capability:model_save"

// FeatureModels is capability:models — the `models` key on a Request
// and a FacetRequest, which loads saved model files by name into the
// bound-model namespace. Exported for the MCP bound schema.
const FeatureModels = "capability:models"

// FeatureWeighting is capability:weighting — the row-weight surface:
// the request-root `weight`, every per-slot `weight`, and
// pulse.Options.DefaultWeight (.claude/reference/weighting.md, Feature
// profile). Exported because the root facade (DefaultWeight refusal)
// and the MCP binder (nested weight properties) read it too.
const FeatureWeighting = "capability:weighting"

// builtinFeatures is THE feature table. Adding an operator, capability,
// I/O format or MCP extra means adding its row here, by hand —
// TestFeaturesHaveSince fails on any registry entry without a row and on
// any row without a registry entry. Synth distributions, field types,
// named tables and expr functions are deliberately absent: they are not
// features (capability:synth gates every distribution).
var builtinFeatures = withDependencies([]Feature{
	// Capabilities.
	capability("process"),        // Process (+ outputs/sort/labels/time_zone request slots)
	capability("stream"),         // ProcessStream, ProcessStreamResult
	capability("watch"),          // Watch*
	capability("compose"),        // Compose, ComposeParallel, ApplySeriesOverlays
	capability("compose_sweep"),  // ComposedRequest.Sweep + ComposedResponse.Ranking
	capability("compose_fits"),   // ComposedRequest.Fits + ComposedResponse.Fits
	capability("compose_banner"), // ComposedRequest.Banner + ComposedResponse.Banner
	capability("model_save"),     // RegressionSpec.Save + FitSpec.Save (model + population files)
	capability("models"),         // Request.Models + FacetRequest.Models + Request.Populations (load saved files)
	capability("for_each"),       // the `for_each` item fan-out on Window / Feature / Attribute
	capability("process_chain"),  // ProcessChain
	capability("facet"),          // Facet, FacetSchema
	capability("sample"),         // Sample, SampleWithRequest
	capability("joins"),          // Request.Joins slot
	capability("crosstab"),       // Request.Crosstab slot
	capability("crosstab_cells"), // CrosstabSpec.Cells (several measures as stub rows)
	capability("lookup"),         // Lookup
	capability("index"),          // BuildIndex, VerifyIndex, ListIndexes, DropIndex
	capability("shard"),          // every shard-archive method
	capability("import"),         // managed import pool
	capability("export"),         // Export, Convert, ImportTransfer, ExportTransfer
	capability("filter_to_file"), // FilterToFile*
	capability("dedup"),          // Dedup
	capability("materialize"),    // pulse_materialize, pulse api materialize (the facade stays ungated)
	capability("rake"),           // pulse_rake, pulse rake: rim / cell weights written to a new cohort (the facade stays ungated)
	capability("widen"),          // WidenSetField
	capability("templates"),      // request templates
	capability("synth"),          // Synth, SynthStream, data-profile capture; gates every synth distribution
	capability("labels"),         // label tables + resolve
	capability("range_tables"),   // range tables
	capability("weighting"),      // Request.Weight + every per-slot weight + Options.DefaultWeight
	capability("multiplicity"),   // every `multiplicity` slot + Options.DefaultMultiplicity
	capability("matrices"),       // Request.Vectors + Request.Matrices
	capability("recommend"),      // pulse_recommend, pulse recommend (the facade stays ungated)
	capability("explain"),        // pulse_explain, pulse explain (the facade stays ungated)

	// I/O formats — io.Formats(). One name gates import AND export.
	ioFormat("csv"),
	ioFormat("tsv"),
	ioFormat("ndjson"),
	ioFormat("jsonarray"),
	ioFormat("parquet"),
	ioFormat("arrow"),
	ioFormat("excel"),
	ioFormat("spss"),

	// MCP extras — the cohort-resource enumeration plus one row per
	// registered prompt (see mcpPromptFeatures).
	mcpExtra("cohort_resources"),
	mcpExtra("prompt_bootstrap"),
	mcpExtra("prompt_author_request"),
	// One prompt per analytic intent (intentRegistry order; see
	// MCPIntentPrompts). Each depends on the tools its script calls.
	mcpExtra("prompt_describe"),
	mcpExtra("prompt_compare_groups"),
	mcpExtra("prompt_relationship"),
	mcpExtra("prompt_drivers"),
	mcpExtra("prompt_change_over_time"),
	mcpExtra("prompt_composition"),
	mcpExtra("prompt_benchmark"),
	mcpExtra("prompt_distribution_shape"),
	mcpExtra("prompt_segment"),
	mcpExtra("prompt_measure_construct"),
	mcpExtra("prompt_flows"),
	mcpExtra("prompt_data_quality"),

	// Aggregators — types.AllAggregationTypes().
	op("AGG_COUNT"),
	op("AGG_SUM"),
	op("AGG_AVERAGE"),
	op("AGG_MIN"),
	op("AGG_MAX"),
	op("AGG_STDDEV"),
	op("AGG_RANGE"),
	op("AGG_MODE_COUNT"),
	op("AGG_ZSCORE"),
	op("AGG_MEDIAN"),
	op("AGG_VARIANCE"),
	op("AGG_MODE"),
	op("AGG_SKEWNESS"),
	op("AGG_KURTOSIS"),
	op("AGG_DISTINCT_COUNT"),
	op("AGG_PERCENTILE"),
	op("AGG_FREQUENCY"),
	op("AGG_NULL_COUNT"),
	op("AGG_WEIGHTED_MEAN"),
	op("AGG_RATIO"),
	op("AGG_DISTINCT_SUM"),
	op("AGG_CI_LOWER"),
	op("AGG_CI_UPPER"),
	op("AGG_WELFORD"),
	op("AGG_MAPE"),
	op("AGG_RMSE"),
	op("AGG_MAE"),
	op("AGG_AUC"),
	op("AGG_SET_UNION"),
	op("AGG_SET_INTERSECTION"),
	op("AGG_SET_FREQUENCY"),
	op("AGG_SET_CARDINALITY_SUM"),
	op("AGG_SET_CARDINALITY_AVG"),
	op("AGG_SET_DISTINCT_VALUES"),
	op("AGG_VEC_MEAN"),
	op("AGG_VEC_SUM"),
	op("AGG_VEC_SD"),
	// Attributes — types.AllAttributeTypes().
	op("ATTR_CODE_IN"),
	op("ATTR_DATE_PART"),
	op("ATTR_FACTOR_SCORE"),
	op("ATTR_FORMULA"),
	op("ATTR_MAHALANOBIS"),
	op("ATTR_NORMALIZED"),
	op("ATTR_PC_SCORE"),
	op("ATTR_PERCENTILE"),
	op("ATTR_PROFILE_SIMILARITY"),
	op("ATTR_REG_FITTED"),
	op("ATTR_REG_LEVERAGE"),
	op("ATTR_REG_PREDICT"),
	op("ATTR_REG_RESIDUAL"),
	op("ATTR_SCALE_SCORE"),
	op("ATTR_SET_HAS"),
	op("ATTR_SET_POPCOUNT"),
	op("ATTR_TSCORE"),
	op("ATTR_VEC_DISTANCE"),
	op("ATTR_ZSCORE"),
	// Filterers — types.AllFiltererTypes().
	op("FILTER_DATE_RANGES"),
	op("FILTER_EXCLUDE"),
	op("FILTER_EXPRESSION"),
	op("FILTER_FALSE"),
	op("FILTER_INCLUDE"),
	op("FILTER_NULL"),
	op("FILTER_RANGE"),
	op("FILTER_SET_CONTAINS_ALL"),
	op("FILTER_SET_CONTAINS_ANY"),
	op("FILTER_SET_CONTAINS_NONE"),
	op("FILTER_SET_EQUALS"),
	op("FILTER_TRUE"),
	// Groupers — types.AllGroupTypes().
	op("GROUP_CATEGORY"),
	op("GROUP_DATE"),
	op("GROUP_DATE_RANGES"),
	op("GROUP_KMEANS"),
	op("GROUP_QUANTILE"),
	op("GROUP_RANGE"),
	op("GROUP_ROUNDED"),
	op("GROUP_SET_PER_ELEMENT"),
	op("GROUP_SET_VALUE"),
	// Windows — types.AllWindowTypes().
	op("WIN_DELTA"),
	op("WIN_DENSE_RANK"),
	op("WIN_EWMA"),
	op("WIN_LAG"),
	op("WIN_LEAD"),
	op("WIN_LIFT"),
	op("WIN_MOVING_AVG"),
	op("WIN_PCT_CHANGE"),
	op("WIN_RANK"),
	op("WIN_ROW_NUMBER"),
	op("WIN_RUNNING_AVG"),
	op("WIN_RUNNING_SUM"),
	op("WIN_SHARE"),
	// Features — types.AllFeatureTypes().
	op("FEAT_BUCKETIZE"),
	op("FEAT_DATE_FEATURES"),
	op("FEAT_FREQUENCY_ENCODE"),
	op("FEAT_LOG"),
	op("FEAT_ONE_HOT"),
	op("FEAT_POLY"),
	op("FEAT_SQRT"),
	op("FEAT_TARGET_ENCODE"),
	op("FEAT_TRAIN_TEST_SPLIT"),
	// Statistical tests — types.AllTestTypes(). One row per test covers BOTH tiers (row test and post test).
	op("TEST_ANOVA_F"),
	op("TEST_ANOVA_RM"),
	op("TEST_ANOVA_WELCH"),
	op("TEST_BARTLETT_SPHERICITY"),
	op("TEST_BOX_M"),
	op("TEST_BROWN_FORSYTHE"),
	op("TEST_CHISQ"),
	op("TEST_FISHER_EXACT"),
	op("TEST_HOTELLING_T2"),
	op("TEST_KENDALL_TAU"),
	op("TEST_KRUSKAL_WALLIS"),
	op("TEST_KS"),
	op("TEST_MANN_WHITNEY_U"),
	op("TEST_MANOVA"),
	op("TEST_MARDIA"),
	op("TEST_PAIRED_T"),
	op("TEST_PEARSON_R"),
	op("TEST_PROP_Z"),
	op("TEST_SHAPIRO_WILK"),
	op("TEST_SPEARMAN_R"),
	op("TEST_T"),
	op("TEST_TREND"),
	op("TEST_TUKEY_HSD"),
	op("TEST_WELCH"),
	op("TEST_WILCOXON_SR"),
	op("TEST_Z_TWO_SAMPLE"),
	// Regressions — types.AllRegressionTypes().
	op("REG_BAYES_LINEAR"),
	op("REG_GLM"),
	op("REG_OLS"),
	// Matrix operators — types.AllMatrixTypes().
	op("MAT_COLLINEARITY"),
	op("MAT_CORRELATION"),
	op("MAT_COVARIANCE"),
	op("MAT_DISTANCE"),
	op("MAT_FACTOR"),
	op("MAT_PARTIAL_CORRELATION"),
	op("MAT_PCA"),
	op("MAT_RELIABILITY"),
	op("MAT_SEM"),
	// Overlay kinds — types.AllOverlayKinds().
	op("OVERLAY_CHISQ_COL"),
	op("OVERLAY_CHISQ_MATRIX"),
	op("OVERLAY_CHISQ_ROW"),
	op("OVERLAY_CHISQ_VS_POP"),
	op("OVERLAY_CHISQ_VS_REF"),
	op("OVERLAY_CORRESPONDENCE"),
	op("OVERLAY_DELTA_VS_BASELINE"),
	op("OVERLAY_DELTA_VS_MARGIN"),
	op("OVERLAY_DELTA_VS_PRIOR"),
	op("OVERLAY_DELTA_VS_REF"),
	op("OVERLAY_DELTA_VS_SIBLING"),
	op("OVERLAY_DELTA_VS_STAGE"),
	op("OVERLAY_FISHER_EXACT_CELL"),
	op("OVERLAY_FORMULA"),
	op("OVERLAY_INDEX_VS_BASELINE"),
	op("OVERLAY_INDEX_VS_MARGIN"),
	op("OVERLAY_INDEX_VS_POP"),
	op("OVERLAY_INDEX_VS_PRIOR"),
	op("OVERLAY_INDEX_VS_REF"),
	op("OVERLAY_INDEX_VS_ROLLING_MEAN"),
	op("OVERLAY_INDEX_VS_SIBLING"),
	op("OVERLAY_INDEX_VS_STAGE"),
	op("OVERLAY_INDEX_VS_TOTAL"),
	op("OVERLAY_KS_VS_POP"),
	op("OVERLAY_MARKOV"),
	op("OVERLAY_MATRIX_GROUP_SUMMARY"),
	op("OVERLAY_PAIRWISE_PROBIT_T"),
	op("OVERLAY_PAIRWISE_PROP_Z"),
	op("OVERLAY_PAIRWISE_TWO_MEANS_Z"),
	op("OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z"),
	op("OVERLAY_PAIRWISE_WELCH_T"),
	op("OVERLAY_PANEL_INDEX_VS_REF"),
	op("OVERLAY_PROP_Z_CELL"),
	op("OVERLAY_PROP_Z_PANEL"),
	op("OVERLAY_PROP_Z_VS_PRIOR"),
	op("OVERLAY_RAKE"),
	op("OVERLAY_RANK"),
	op("OVERLAY_RETENTION"),
	op("OVERLAY_SHARE_OF_COL"),
	op("OVERLAY_SHARE_OF_ROW"),
	op("OVERLAY_SHARE_OF_TOTAL"),
	op("OVERLAY_STD_RESIDUAL"),
	op("OVERLAY_SYMMETRY"),
	op("OVERLAY_T_CELL"),
	op("OVERLAY_T_VS_PRIOR"),
	op("OVERLAY_T_VS_REF"),
	op("OVERLAY_YOY"),
	op("OVERLAY_ZSCORE_VS_MARGIN"),
	op("OVERLAY_ZSCORE_VS_POP"),
	op("OVERLAY_ZSCORE_VS_ROLLING"),
	op("OVERLAY_ZSCORE_VS_TOTAL"),
	op("OVERLAY_Z_CELL"),
	op("OVERLAY_Z_VS_PRIOR"),
	op("OVERLAY_Z_VS_REF"),
})

// requestHosts is the any-of group every request-executing feature
// depends on: an operator or request slot is incoherent without at least
// one capability that executes a Request.
var requestHosts = []string{featProcess, featCompose, featProcessChain}

// processOnly lists the capabilities that exist only as a mode of Process
// (streaming, watching, filter-to-file, materialize — which runs Process's
// pre-flight and record-level pipeline) and so depend on it alone.
var processOnly = map[string]bool{
	featStream:       true,
	featWatch:        true,
	featFilterToFile: true,
	featMaterialize:  true,
}

// requestSlotCapabilities are request slots modelled as capabilities;
// like operators they need a request-executing host.
var requestSlotCapabilities = map[string]bool{
	featJoins:     true,
	featCrosstab:  true,
	featWeighting: true,
	featMatrices:  true,
	featForEach:   true,
	featModelSave: true,
}

// modelsHosts is the any-of host group of capability:models: every
// request host, plus capability:facet (FacetRequest.Models feeds the
// facet filterers' coef()).
var modelsHosts = []string{featProcess, featCompose, featProcessChain, featFacet}

// slotTokenSet is the prose vocabulary one slot-owning capability owns:
// the wire tokens that name its request / response slots, and the topic
// a guidance sentence naming one of them must be about.
type slotTokenSet struct {
	// tokens are whole [A-Za-z0-9_] runs. hiddenProseNames adds them
	// when the capability is hidden, so the prose scrub drops every
	// sentence naming one.
	tokens []string
	// topic matches a sentence that is ABOUT the slot once the tokens
	// are cut out of it (the SLOT-TOKEN guidance lint): a sentence that
	// names a token without being about the slot would be over-dropped
	// on an instance hiding the capability. nil only when tokens is
	// empty.
	topic *regexp.Regexp
}

// slotTokens maps every capability that owns a request slot (a
// gatedSlots key whose visibility it decides) to the wire tokens the
// prose scrub removes when the capability is hidden. An entry may be
// empty; a missing one fails TestSlotTokens_EveryRequestSlotCapability.
// Tokens are wire-specific: a slot key that is also an everyday English
// word ("joins", "weight", "crosstab" as a noun several hosts share) is
// left out, since dropping every sentence using the word would
// over-drop. Request.Return and the other ungated slots have no owning
// capability, so no entry.
var slotTokens = map[string]slotTokenSet{
	featMultiplicity: {
		tokens: []string{"multiplicity", "p_adjusted", "significant_adjusted"},
		topic:  regexp.MustCompile(`(?i)\b(?:adjust\w*|correct\w*|multiple\s+comparisons?|family|family-wise|false\s+discovery|bonferroni|holm)\b`),
	},
	featWeighting: {
		// `weight` itself is English (body weight, AGG_WEIGHTED_MEAN's
		// weight_field); sum_weights / n_eff are AGG_WEIGHTED_MEAN's own
		// component keys too, so they stay visible without the slot.
		tokens: []string{"n_weight_invalid", "weight_aware"},
		topic:  regexp.MustCompile(`(?i)weight`),
	},
	featMatrices: {
		tokens: []string{"vectors", "matrices", "n_listwise_dropped", "min_pair_n", "max_pair_n"},
		topic:  regexp.MustCompile(`(?i)\b(?:matri\w*|vectors?|MAT_[A-Z_]+|correlation|covariance)\b`),
	},
	featCrosstab: {
		// `crosstab` names the MATRIX host other capabilities' overlays
		// describe too (OVERLAY_SHARE_OF_TOTAL runs on a series host).
		tokens: []string{"margin_aggregations"},
		topic:  regexp.MustCompile(`(?i)\b(?:margins?|crosstab)\b`),
	},
	// `joins` is an English verb ("joins the family").
	featJoins: {},
	// The overlay hosts' slot is `overlays`, shared by all four hosts;
	// the overlay kinds a hidden host runs are operator names the scrub
	// already removes.
	featCompose:      {},
	featProcessChain: {},
	featFacet:        {},
	// `sweep` and `ranking` are everyday English; no guidance sentence
	// names the slot yet.
	featComposeSweep: {},
	// `fits` is everyday English; no guidance sentence names the slot.
	featComposeFits: {},
	// `banner` names the slot only; no guidance sentence names it yet.
	featComposeBanner: {},
	// `save` is everyday English; no guidance sentence names the slot.
	featModelSave: {},
	// `models` is everyday English; no guidance sentence names the slot.
	featModels: {},
	// `for_each` names the item key only; no guidance sentence names it.
	featForEach: {},
	// pulse_explain's `sample` root slot: "sample" is everyday English
	// (a sample of rows, sample size).
	featSample: {},
	// `cells` and `measures` are everyday English, and no guidance
	// sentence names the `cell_labels` payload slot yet.
	featCrosstabCells: {},
}

// slotTokenHomonyms lists, per slot token, the tokens that mark a
// sentence as naming a DIFFERENT slot spelled the same way: such a
// sentence is never a hit for that token. LookupRequest.multiplicity is
// pulse_lookup's duplicate-key mode, not a correction block.
// pulse_import's `vectors` argument declares native vec_f32 / vec_f64
// FIELDS — a format feature no profile gates — not the request's
// virtual-vector slot capability:matrices owns, so an import sentence
// (one naming a field type, `vector_kinds` or the import refusal) keeps
// its prose under a matrices-free profile.
var slotTokenHomonyms = map[string][]string{
	"multiplicity": {"assert_unique", "LookupMultiplicity", "PULSE_LOOKUP_AMBIGUOUS"},
	"vectors":      {"vec_f32", "vec_f64", "vector_kinds", "PULSE_IMPORT_VECTOR_INVALID"},
}

// SlotTokenHomonymMarkers returns the marker tokens (a fresh slice,
// possibly empty) that make a sentence naming slot token tok name a
// different, same-spelled surface (slotTokenHomonyms).
func SlotTokenHomonymMarkers(tok string) []string {
	return append([]string(nil), slotTokenHomonyms[tok]...)
}

// SlotTokenCapabilities returns the capabilities with a slot-token
// entry, sorted.
func SlotTokenCapabilities() []string {
	out := make([]string, 0, len(slotTokens))
	for c := range slotTokens {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// SlotTokensOf returns the wire tokens capability owns (a fresh slice,
// possibly empty) and whether it has an entry.
func SlotTokensOf(capability string) ([]string, bool) {
	s, ok := slotTokens[capability]
	return append([]string(nil), s.tokens...), ok
}

// overlayHostKinds lists, per host capability, the overlay kinds that
// host can run. It mirrors the engine's per-host overlay handler maps
// one for one, and TestProfileDependenciesComplete (internal/processing)
// asserts equality in both directions:
//
//   - capability:crosstab      ← overlayHandlers (the MATRIX host a
//     crosstab Request produces, under any request-executing host)
//   - capability:compose       ← composeOverlayHandlers +
//     composeOverlayMultiLayerHandlers + seriesOverlayHandlers (the
//     series host is reached only through ApplySeriesOverlays, which the
//     compose capability gates)
//   - capability:process_chain ← chainOverlayHandlers
//   - capability:facet         ← facetOverlayHandlers
//   - capability:matrices      ← matrixResultOverlayHandlers (the
//     MATRIX_RESULT host a Request.Matrices request produces)
//
// An overlay kind depends on the any-of set of every host listing it.
var overlayHostKinds = map[string][]string{
	featCrosstab: {
		"OVERLAY_CHISQ_COL",
		"OVERLAY_CHISQ_MATRIX",
		"OVERLAY_CHISQ_ROW",
		"OVERLAY_CORRESPONDENCE",
		"OVERLAY_DELTA_VS_MARGIN",
		"OVERLAY_FISHER_EXACT_CELL",
		"OVERLAY_FORMULA",
		"OVERLAY_INDEX_VS_MARGIN",
		"OVERLAY_MARKOV",
		"OVERLAY_PAIRWISE_PROBIT_T",
		"OVERLAY_PAIRWISE_PROP_Z",
		"OVERLAY_PAIRWISE_TWO_MEANS_Z",
		"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z",
		"OVERLAY_PAIRWISE_WELCH_T",
		"OVERLAY_PROP_Z_VS_PRIOR",
		"OVERLAY_RAKE",
		"OVERLAY_RETENTION",
		"OVERLAY_SHARE_OF_COL",
		"OVERLAY_SHARE_OF_ROW",
		"OVERLAY_SHARE_OF_TOTAL",
		"OVERLAY_STD_RESIDUAL",
		"OVERLAY_SYMMETRY",
		"OVERLAY_T_VS_PRIOR",
		"OVERLAY_ZSCORE_VS_MARGIN",
		"OVERLAY_Z_VS_PRIOR",
	},
	featCompose: {
		// composeOverlayHandlers.
		"OVERLAY_CHISQ_VS_REF",
		"OVERLAY_DELTA_VS_REF",
		"OVERLAY_FORMULA",
		"OVERLAY_INDEX_VS_REF",
		"OVERLAY_PROP_Z_CELL",
		"OVERLAY_PROP_Z_PANEL",
		"OVERLAY_RANK",
		"OVERLAY_T_CELL",
		"OVERLAY_T_VS_REF",
		"OVERLAY_Z_CELL",
		"OVERLAY_Z_VS_REF",
		// composeOverlayMultiLayerHandlers.
		"OVERLAY_PANEL_INDEX_VS_REF",
		// seriesOverlayHandlers.
		"OVERLAY_DELTA_VS_BASELINE",
		"OVERLAY_DELTA_VS_PRIOR",
		"OVERLAY_DELTA_VS_SIBLING",
		"OVERLAY_INDEX_VS_BASELINE",
		"OVERLAY_INDEX_VS_PRIOR",
		"OVERLAY_INDEX_VS_ROLLING_MEAN",
		"OVERLAY_INDEX_VS_SIBLING",
		"OVERLAY_INDEX_VS_TOTAL",
		"OVERLAY_PROP_Z_VS_PRIOR",
		"OVERLAY_SHARE_OF_TOTAL",
		"OVERLAY_T_VS_PRIOR",
		"OVERLAY_YOY",
		"OVERLAY_ZSCORE_VS_ROLLING",
		"OVERLAY_ZSCORE_VS_TOTAL",
		"OVERLAY_Z_VS_PRIOR",
	},
	featMatrices: {
		// matrixResultOverlayHandlers: the MATRIX_RESULT host reads
		// Response.Matrices, which capability:matrices gates.
		"OVERLAY_MATRIX_GROUP_SUMMARY",
	},
	featProcessChain: {
		"OVERLAY_DELTA_VS_STAGE",
		"OVERLAY_INDEX_VS_STAGE",
	},
	featFacet: {
		"OVERLAY_CHISQ_VS_POP",
		"OVERLAY_INDEX_VS_POP",
		"OVERLAY_KS_VS_POP",
		"OVERLAY_ZSCORE_VS_POP",
	},
}

// hardEdges are the component- and result-reading dependencies: each
// value is ONE any-of group ANDed after the host group (most name a
// single operator). They are the edges the engine enforces at run time
// today:
//
//   - The Welford-reading overlays consume the {mean, variance, n} triple
//     only AGG_WELFORD emits on the host's cell/row components (the
//     prior-period T / Z kinds also read an AGG_VARIANCE / AGG_STDDEV
//     measure's; OVERLAY_PROP_Z_VS_PRIOR an AGG_AVERAGE share).
//   - OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z reads the weighted moments
//     AGG_WEIGHTED_MEAN or a weighted AGG_AVERAGE cell emits — either
//     operator satisfies it.
//   - ATTR_REG_* fit an OLS model through the regression engine.
//   - OVERLAY_YOY refuses any series host whose first grouper is not
//     GROUP_DATE (it reads the date grouper's frequency).
//   - capability:filter_to_file compiles every filterer of the request
//     into one engine-internal FILTER_EXPRESSION, so hiding
//     FILTER_EXPRESSION would break FilterToFile for any request.
//   - every MAT_* operator rides the Request.Matrices slot, which
//     capability:matrices gates.
//   - capability:model_save writes a fitted regression or a vector
//     fit, so it needs any-of the REG_* operators, the fitted vector
//     attributes and capability:matrices.
//   - capability:models loads a model only a scoring reference reads
//     (ATTR_REG_PREDICT, or coef() in ATTR_FORMULA / FILTER_EXPRESSION)
//     and a population only a params.fit reads (the fitted vector
//     attributes, ATTR_VEC_DISTANCE, capability:matrices).
//
// TEST_TUKEY_HSD after TEST_ANOVA_F is deliberately absent: its inputs
// are plain numeric params, so the pairing is advice, not a dependency.
var hardEdges = map[string][]string{
	"OVERLAY_T_CELL":               {"AGG_WELFORD"},
	"OVERLAY_Z_CELL":               {"AGG_WELFORD"},
	"OVERLAY_T_VS_REF":             {"AGG_WELFORD"},
	"OVERLAY_Z_VS_REF":             {"AGG_WELFORD"},
	"OVERLAY_PAIRWISE_WELCH_T":     {"AGG_WELFORD"},
	"OVERLAY_PAIRWISE_TWO_MEANS_Z": {"AGG_WELFORD"},
	// The prior-period kinds read a measure's moments: the Welford triple
	// (any of the three built-ins carrying it) or an AGG_AVERAGE share.
	"OVERLAY_T_VS_PRIOR":                    {"AGG_STDDEV", "AGG_VARIANCE", "AGG_WELFORD"},
	"OVERLAY_Z_VS_PRIOR":                    {"AGG_STDDEV", "AGG_VARIANCE", "AGG_WELFORD"},
	"OVERLAY_PROP_Z_VS_PRIOR":               {"AGG_AVERAGE"},
	"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": {"AGG_AVERAGE", "AGG_WEIGHTED_MEAN"},
	"ATTR_REG_FITTED":                       {"REG_OLS"},
	"ATTR_REG_LEVERAGE":                     {"REG_OLS"},
	// Scores a bound fit of any scorable regression type.
	"ATTR_REG_PREDICT":        {"REG_BAYES_LINEAR", "REG_GLM", "REG_OLS"},
	"ATTR_REG_RESIDUAL":       {"REG_OLS"},
	"OVERLAY_YOY":             {"GROUP_DATE"},
	featFilterToFile:          {"FILTER_EXPRESSION"},
	"MAT_COLLINEARITY":        {featMatrices},
	"MAT_CORRELATION":         {featMatrices},
	"MAT_COVARIANCE":          {featMatrices},
	"MAT_DISTANCE":            {featMatrices},
	"MAT_FACTOR":              {featMatrices},
	"MAT_PARTIAL_CORRELATION": {featMatrices},
	"MAT_PCA":                 {featMatrices},
	"MAT_RELIABILITY":         {featMatrices},
	"MAT_SEM":                 {featMatrices},

	// A sweep expands into Compose slots: without Compose there is
	// nothing to expand into.
	featComposeSweep: {featCompose},
	// The measures are a crosstab's cells: without the crosstab host
	// there is no table to fold them into.
	featCrosstabCells: {featCrosstab},
	// A save writes a fitted regression (a model file) or a vector fit
	// (a population file): without a regression or a vector battery
	// there is nothing to save.
	featModelSave: {string(types.ATTR_FACTOR_SCORE), string(types.ATTR_MAHALANOBIS), string(types.ATTR_PC_SCORE),
		string(types.REG_BAYES_LINEAR), string(types.REG_GLM), string(types.REG_OLS), featMatrices},
	// A loaded model or population is read only by a reference: a
	// scoring reference (ATTR_REG_PREDICT, coef()) or a params.fit (a
	// fitted vector attribute, a MAT_* slot).
	featModels: {string(types.ATTR_FACTOR_SCORE), "ATTR_FORMULA", string(types.ATTR_MAHALANOBIS), string(types.ATTR_PC_SCORE),
		"ATTR_REG_PREDICT", string(types.ATTR_VEC_DISTANCE), "FILTER_EXPRESSION", featMatrices},
}

// groupEdges are the dependencies needing MORE than one any-of group
// (hardEdges holds one): each group is ANDed after the host group.
//
//   - capability:compose_fits runs its fits before Compose slots and a
//     fit is one regression or one vector battery (a matrix or a fitted
//     vector attribute): it needs Compose AND any-of the REG_*
//     operators, capability:matrices and the fitted attributes.
var groupEdges = map[string][][]string{
	// A banner expands into Compose slots, each a crosstab: without
	// Compose there is nothing to expand into, without the crosstab host
	// there is no table to stitch.
	featComposeBanner: {{featCompose}, {featCrosstab}},
	featComposeFits: {
		{featCompose},
		{string(types.ATTR_FACTOR_SCORE), string(types.ATTR_MAHALANOBIS), string(types.ATTR_PC_SCORE),
			string(types.REG_BAYES_LINEAR), string(types.REG_GLM), string(types.REG_OLS), featMatrices},
	},
}

// RequestHostCapabilities returns the request-executing host
// capabilities, in a fresh slice: the any-of group every non-overlay
// operator depends on. The profile validator gives extension operators
// the same group, so an extension operator is never enabled without a
// host to run it.
func RequestHostCapabilities() []string {
	return append([]string(nil), requestHosts...)
}

// OverlayHostCapabilities returns the host capabilities an overlay kind
// can run under, sorted. The engine-side gate compares it to handler-map
// membership; nil means the kind is listed under no host.
func OverlayHostCapabilities(kind string) []string {
	var out []string
	for host, kinds := range overlayHostKinds {
		for _, k := range kinds {
			if k == kind {
				out = append(out, host)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// withDependencies fills every row's DependsOn from the rules:
//
//   - non-overlay operators and the request-slot capabilities depend on
//     any-of the request-executing hosts;
//   - Process-only modes depend on Process;
//   - overlay kinds depend on any-of the hosts whose handler map lists
//     them;
//   - an intent prompt depends on each feature-bound tool its script
//     calls (intentPromptDependencies);
//   - then a hard edge appends its any-of group.
func withDependencies(rows []Feature) []Feature {
	for i := range rows {
		f := &rows[i]
		var groups [][]string
		switch {
		case f.Kind == FeatureKindOperator && strings.HasPrefix(f.Name, "OVERLAY_"):
			if hosts := OverlayHostCapabilities(f.Name); len(hosts) > 0 {
				groups = append(groups, hosts)
			}
		case f.Name == featModels:
			groups = append(groups, append([]string(nil), modelsHosts...))
		case f.Kind == FeatureKindOperator, requestSlotCapabilities[f.Name]:
			groups = append(groups, append([]string(nil), requestHosts...))
		case processOnly[f.Name]:
			groups = append(groups, []string{featProcess})
		case isIntentPromptFeature(f.Name):
			groups = append(groups, cloneGroups(intentPromptDependencies)...)
		}
		if anyOf := hardEdges[f.Name]; len(anyOf) > 0 {
			groups = append(groups, append([]string(nil), anyOf...))
		}
		groups = append(groups, cloneGroups(groupEdges[f.Name])...)
		f.DependsOn = groups
	}
	return rows
}

func cloneGroups(groups [][]string) [][]string {
	if groups == nil {
		return nil
	}
	out := make([][]string, len(groups))
	for i, g := range groups {
		out[i] = append([]string(nil), g...)
	}
	return out
}

// Core surfaces are ALWAYS present: they are not features, cannot be
// hidden, and a profile that lists one is refused as an unknown feature.
// Kept as data so the profile validator can name them in its refusal.
var coreSurfaces = []string{
	CoreOpen,
	CoreInspect,
	CorePredict,
	CoreCountRecords,
	CoreManifest,
	CorePayloadSchema,
	CoreSkills,
	CoreExamples,
	CoreErrorsLookup,
	CoreCohortArtifacts,
}

// The always-present core surfaces.
const (
	CoreOpen            = "open"             // Open
	CoreInspect         = "inspect"          // Inspect*, pulse_inspect
	CorePredict         = "predict"          // Predict*, pulse_predict
	CoreCountRecords    = "count_records"    // CountRecords
	CoreManifest        = "manifest"         // Manifest, pulse_manifest
	CorePayloadSchema   = "payload_schema"   // payload JSON Schema, pulse://schema
	CoreSkills          = "skills"           // skills list/get
	CoreExamples        = "examples"         // examples search/get
	CoreErrorsLookup    = "errors_lookup"    // errors lookup
	CoreCohortArtifacts = "cohort_artifacts" // CohortArtifacts
)

// MCPToolBinding records which feature (or core surface) owns one MCP
// tool. Exactly one of Feature and Core is set. Data only today; the
// MCP registration filter consumes it in a later unit.
type MCPToolBinding struct {
	Tool    string
	Feature string
	Core    string
}

// mcpToolBindings binds every registered MCP tool (toolmeta.Names()).
// String literals, not toolmeta constants, so the table stays pure data;
// TestFeaturesHaveSince pins it to toolmeta in both directions.
var mcpToolBindings = []MCPToolBinding{
	{Tool: "pulse_inspect", Core: CoreInspect},
	{Tool: "pulse_predict", Core: CorePredict},
	{Tool: "pulse_manifest", Core: CoreManifest},
	{Tool: "pulse_skills_list", Core: CoreSkills},
	{Tool: "pulse_skills_get", Core: CoreSkills},
	{Tool: "pulse_examples_search", Core: CoreExamples},
	{Tool: "pulse_examples_get", Core: CoreExamples},
	{Tool: "pulse_errors_lookup", Core: CoreErrorsLookup},
	{Tool: "pulse_process", Feature: featProcess},
	{Tool: "pulse_compose", Feature: featCompose},
	{Tool: "pulse_process_chain", Feature: featProcessChain},
	{Tool: "pulse_facet", Feature: featFacet},
	{Tool: "pulse_facet_schema", Feature: featFacet},
	{Tool: "pulse_sample", Feature: featSample},
	{Tool: "pulse_lookup", Feature: featLookup},
	{Tool: "pulse_import", Feature: featImport},
	{Tool: "pulse_imports_list", Feature: featImport},
	{Tool: "pulse_drop", Feature: featImport},
	{Tool: "pulse_dedup", Feature: featDedup},
	{Tool: "pulse_materialize", Feature: featMaterialize},
	{Tool: "pulse_rake", Feature: featRake},
	{Tool: "pulse_label_tables", Feature: featLabels},
	{Tool: "pulse_label_resolve", Feature: featLabels},
	{Tool: "pulse_range_tables", Feature: featRangeTables},
	{Tool: "pulse_recommend", Feature: featRecommend},
	{Tool: "pulse_explain", Feature: featExplain},
}

// CommandBinding records which feature (or core surface) owns one
// manifest command (a CLI leaf, Manifest.Commands) or library-only
// operation (Manifest.Operations). Exactly one of Feature, Core and
// Ungated is set. Ungated marks a process-level leaf that describes the
// binary rather than the instance (the MCP server, the build version):
// it is neither a feature nor a core surface and is always listed.
type CommandBinding struct {
	Command string
	Feature string
	Core    string
	Ungated bool
}

// commandBindings binds every manifest command and operation. The
// instance manifest lists an entry iff its binding is core, ungated or
// an enabled feature. TestCommandBindings_Complete pins it to
// commands() / operations() in both directions.
var commandBindings = []CommandBinding{
	// Manifest.Commands.
	{Command: "process", Feature: featProcess},
	{Command: "compose", Feature: featCompose},
	{Command: "process-chain", Feature: featProcessChain},
	{Command: "sample", Feature: featSample},
	{Command: "facet", Feature: featFacet},
	{Command: "lookup", Feature: featLookup},
	{Command: "inspect", Core: CoreInspect},
	{Command: "predict", Core: CorePredict},
	// The non-Request predict leaves follow the capability owning the
	// request shape they validate (pulse_predict's alternative roots
	// are gated the same way).
	{Command: "predict-compose", Feature: featCompose},
	{Command: "predict-facet", Feature: featFacet},
	{Command: "predict-chain", Feature: featProcessChain},
	{Command: "manifest", Core: CoreManifest},
	{Command: "schema", Core: CorePayloadSchema},
	{Command: "mcp", Ungated: true},
	{Command: "synth", Feature: featSynth},
	{Command: "profile", Feature: featSynth},
	{Command: "shard create", Feature: featShard},
	{Command: "shard add", Feature: featShard},
	{Command: "shard remove", Feature: featShard},
	{Command: "shard list", Feature: featShard},
	{Command: "shard compact", Feature: featShard},
	{Command: "shard verify", Feature: featShard},
	{Command: "shard extract", Feature: featShard},
	{Command: "index build", Feature: featIndex},
	{Command: "index list", Feature: featIndex},
	{Command: "index verify", Feature: featIndex},
	{Command: "index drop", Feature: featIndex},
	{Command: "widen", Feature: featWiden},
	{Command: "dedup", Feature: featDedup},
	{Command: "materialize", Feature: featMaterialize},
	{Command: "rake", Feature: featRake},
	{Command: "recommend", Feature: featRecommend},
	{Command: "explain", Feature: featExplain},
	// The feature-profile tooling describes the binary, not an instance.
	{Command: "features init", Ungated: true},
	{Command: "features check", Ungated: true},
	{Command: "features diff", Ungated: true},
	{Command: "features show", Ungated: true},
	// The reference export builds its own in-memory instance (scoped by
	// --feature-profile), so it describes the binary, not this instance.
	{Command: "docs export", Ungated: true},
	{Command: "version", Ungated: true},
	// Manifest.Operations.
	{Command: "filter_to_file", Feature: featFilterToFile},
	{Command: "process_stream", Feature: featStream},
	{Command: "synth_stream", Feature: featSynth},
	{Command: "watch", Feature: featWatch},
}

// CommandBindings returns the manifest command / operation → feature
// table in a stable order.
func CommandBindings() []CommandBinding {
	return append([]CommandBinding(nil), commandBindings...)
}

// CommandBindingOf returns the binding for one manifest command or
// operation name.
func CommandBindingOf(command string) (CommandBinding, bool) {
	for _, b := range commandBindings {
		if b.Command == command {
			return b, true
		}
	}
	return CommandBinding{}, false
}

// mcpPromptFeatures binds every registered MCP prompt
// (gosdk.RegisteredPrompts()) to its mcp_extra feature: the two
// hand-written prompts plus one generated prompt per analytic intent.
var mcpPromptFeatures = buildMCPPromptFeatures()

func buildMCPPromptFeatures() map[string]string {
	out := map[string]string{
		"pulse-bootstrap":      FeatureName(FeatureKindMCPExtra, "prompt_bootstrap"),
		"pulse-author-request": FeatureName(FeatureKindMCPExtra, "prompt_author_request"),
	}
	for _, ip := range MCPIntentPrompts() {
		out[ip.Prompt] = ip.Feature
	}
	return out
}

// intentPromptDependencies is the dependency expression of every
// intent prompt: the script calls pulse_recommend, pulse_explain and
// pulse_process, so each owning feature is its own AND group. The
// script's pulse_inspect and pulse_errors_lookup steps are core
// surfaces, always mounted, so they need no edge.
var intentPromptDependencies = [][]string{{featRecommend}, {featExplain}, {featProcess}}

// MCPIntentPrompt is one generated per-intent MCP prompt: its prompt
// name, the analytic intent it scripts, and its mcp_extra feature.
type MCPIntentPrompt struct {
	Prompt  string
	Intent  string
	Feature string
}

// IntentPromptName spells the MCP prompt of an intent:
// `pulse-<intent-kebab>` (compare_groups → pulse-compare-groups).
func IntentPromptName(intentID string) string {
	return "pulse-" + strings.ReplaceAll(intentID, "_", "-")
}

// MCPIntentPrompts returns one generated prompt per ANALYTIC intent, in
// intent-registry order. The tooling intents (prepare, simulate,
// lookup) route to tools, never to a prompt. A prompt mounts iff its
// feature is enabled AND its intent node survives the instance's
// ontology prune (mcp/gosdk scope.go).
func MCPIntentPrompts() []MCPIntentPrompt {
	var out []MCPIntentPrompt
	for _, in := range intentRegistry {
		if !in.Analytic {
			continue
		}
		out = append(out, MCPIntentPrompt{
			Prompt:  IntentPromptName(in.ID),
			Intent:  in.ID,
			Feature: FeatureName(FeatureKindMCPExtra, "prompt_"+in.ID),
		})
	}
	return out
}

// isIntentPromptFeature reports whether name is a generated intent
// prompt's mcp_extra feature.
func isIntentPromptFeature(name string) bool {
	bare, ok := strings.CutPrefix(name, string(FeatureKindMCPExtra)+":prompt_")
	if !ok {
		return false
	}
	for _, in := range intentRegistry {
		if in.Analytic && in.ID == bare {
			return true
		}
	}
	return false
}

var (
	featureIndexOnce sync.Once
	featureIndex     map[string]Feature
	coreIndex        map[string]bool
)

func buildFeatureIndex() {
	featureIndex = make(map[string]Feature, len(builtinFeatures))
	for _, f := range builtinFeatures {
		if _, dup := featureIndex[f.Name]; !dup {
			featureIndex[f.Name] = f
		}
	}
	coreIndex = make(map[string]bool, len(coreSurfaces))
	for _, c := range coreSurfaces {
		coreIndex[c] = true
	}
}

// Features returns every built-in feature row in table order. The slice
// is a fresh copy; duplicates (a table bug the gate refuses) are kept so
// the gate can see them.
func Features() []Feature {
	out := append([]Feature(nil), builtinFeatures...)
	for i := range out {
		out[i].DependsOn = cloneGroups(out[i].DependsOn)
	}
	return out
}

// FeatureDependencies returns a copy of the dependency groups (AND of
// any-of sets) of the built-in feature spelled name.
func FeatureDependencies(name string) ([][]string, bool) {
	f, ok := LookupFeature(name)
	return cloneGroups(f.DependsOn), ok
}

// FeatureNames returns every built-in feature name in table order.
func FeatureNames() []string {
	out := make([]string, len(builtinFeatures))
	for i, f := range builtinFeatures {
		out[i] = f.Name
	}
	return out
}

// LookupFeature returns the built-in row spelled exactly name.
func LookupFeature(name string) (Feature, bool) {
	featureIndexOnce.Do(buildFeatureIndex)
	f, ok := featureIndex[name]
	f.DependsOn = cloneGroups(f.DependsOn)
	return f, ok
}

// FeatureKindOf returns the kind of the built-in feature spelled name.
func FeatureKindOf(name string) (FeatureKind, bool) {
	f, ok := LookupFeature(name)
	return f.Kind, ok
}

// CoreSurfaces returns the always-present core surface names in a stable
// order. They are not features.
func CoreSurfaces() []string {
	return append([]string(nil), coreSurfaces...)
}

// IsCoreSurface reports whether name is an always-present core surface.
func IsCoreSurface(name string) bool {
	featureIndexOnce.Do(buildFeatureIndex)
	return coreIndex[name]
}

// MCPToolBindings returns the MCP tool → feature/core table in a stable
// order.
func MCPToolBindings() []MCPToolBinding {
	return append([]MCPToolBinding(nil), mcpToolBindings...)
}

// MCPToolBindingOf returns the binding for one MCP tool name.
func MCPToolBindingOf(tool string) (MCPToolBinding, bool) {
	for _, b := range mcpToolBindings {
		if b.Tool == tool {
			return b, true
		}
	}
	return MCPToolBinding{}, false
}

// MCPPromptFeatures returns the MCP prompt name → mcp_extra feature map
// as a fresh copy.
func MCPPromptFeatures() map[string]string {
	out := make(map[string]string, len(mcpPromptFeatures))
	for k, v := range mcpPromptFeatures {
		out[k] = v
	}
	return out
}

// ParseSince parses a feature Since: exactly `major.minor.patch`, each a
// non-negative decimal integer with no sign, prefix or suffix. A
// pre-release or build suffix is refused (ok == false) — a Since names a
// release line, never a build.
func ParseSince(s string) (major, minor, patch int, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var out [3]int
	for i, p := range parts {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return 0, 0, 0, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, false
		}
		out[i] = n
	}
	return out[0], out[1], out[2], true
}

// SinceReached reports whether a feature introduced in since is
// available in the running build version. The running version is
// compared on its major.minor.patch core only, so a pre-release or
// git-describe build of a release line offers that line's features
// (1.0.0-alpha.2 offers Since 1.0.0). A running version with no usable
// core is treated as newest. An unparseable since (a table bug
// TestFeaturesHaveSince refuses) is never reached.
func SinceReached(since, running string) bool {
	sMaj, sMin, sPatch, ok := ParseSince(since)
	if !ok {
		return false
	}
	rMaj, rMin, rPatch, newest := runningVersionCore(running)
	if newest {
		return true
	}
	if rMaj != sMaj {
		return rMaj > sMaj
	}
	if rMin != sMin {
		return rMin > sMin
	}
	return rPatch >= sPatch
}

// runningVersionCore extracts the major.minor.patch core of a running
// build version: a leading "v" is dropped and everything from the first
// "-" or "+" (pre-release, build metadata, git-describe suffix) is cut.
// newest is true for a version with no comparable core: "devel" and
// "devel+<sha>", a bare commit SHA, anything unparseable, and a 0.0.0
// core (the Go pseudo-version of an untagged build), which no release
// carries.
func runningVersionCore(v string) (major, minor, patch int, newest bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	major, minor, patch, ok := ParseSince(v)
	if !ok || (major == 0 && minor == 0 && patch == 0) {
		return 0, 0, 0, true
	}
	return major, minor, patch, false
}

// ReachedFeatureNames returns every built-in feature name whose Since
// the running build version has reached, in table order: the enabled
// set of a profile-free instance with no extensions.
func ReachedFeatureNames(running string) []string {
	out := make([]string, 0, len(builtinFeatures))
	for _, f := range builtinFeatures {
		if SinceReached(f.Since, running) {
			out = append(out, f.Name)
		}
	}
	return out
}

// ProfileWrittenWith returns the `written_with` stamp a generated feature
// profile carries when produced by the running build version: the
// release core (`major.minor.patch`, leading "v" and any pre-release or
// build suffix cut), the same reduction SinceReached applies. A running
// version with no usable core (`devel`, a bare SHA, a 0.0.0
// pseudo-version) is stamped with the newest Since in the feature table
// — the newest release line the build knows — so the stamp is always a
// release a later `Since` can be compared against.
func ProfileWrittenWith(running string) string {
	if major, minor, patch, newest := runningVersionCore(running); !newest {
		return strconv.Itoa(major) + "." + strconv.Itoa(minor) + "." + strconv.Itoa(patch)
	}
	best, bMaj, bMin, bPatch := BuiltinFeatureSince, -1, -1, -1
	for _, f := range builtinFeatures {
		maj, minor, patch, ok := ParseSince(f.Since)
		if !ok {
			continue
		}
		if maj > bMaj || (maj == bMaj && (minor > bMin || (minor == bMin && patch > bPatch))) {
			best, bMaj, bMin, bPatch = f.Since, maj, minor, patch
		}
	}
	return best
}
