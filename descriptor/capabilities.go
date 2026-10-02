package descriptor

import (
	"github.com/frankbardon/pulse/types"
)

// ProcessChainCapability describes the source-rooted linear chain
// endpoint surfaced by pulse.ProcessChain / pulse_process_chain. The
// manifest carries one ProcessChainCapability entry under
// Manifest.ProcessChain so LLM clients can detect the chain gate
// and choose between the chained or per-stage fallback path.
type ProcessChainCapability struct {
	// Name is the canonical entry identifier ("process_chain").
	Name string `json:"name"`

	// MaxStages is the upper bound on Stages length. Zero indicates
	// no compile-time cap; runtime memory is bounded by the largest
	// intermediate response.Data slice.
	MaxStages int `json:"max_stages"`

	// MergeableAggregators lists the aggregator names that pass the
	// chain gate (mergeable + single-scalar emit). Alphabetically
	// sorted, deterministic across calls.
	MergeableAggregators []string `json:"mergeable_aggregators"`

	// MergeableGroupers lists the grouper names that pass the chain
	// gate.
	MergeableGroupers []string `json:"mergeable_groupers"`

	// RowLocalAttributes lists the attribute names that pass the
	// chain gate (row-local only).
	RowLocalAttributes []string `json:"row_local_attributes"`

	// RejectionRules names the operator categories the chain gate
	// rejects today. Intended for LLM-side reasoning and fallback
	// routing; not a strict schema.
	RejectionRules []string `json:"rejection_rules"`

	// OverlayKinds lists the whole-chain overlay catalog entries
	// accepted on ChainRequest.Overlays today. Alphabetically sorted.
	// Minimal additive surface kept for backward compatibility; the
	// richer per-kind sub-block at Overlays carries the full surface
	// (shapes / scopes / ref kinds / buffered / description).
	OverlayKinds []string `json:"overlay_kinds"`

	// Overlays enumerates the per-kind capability rows for every
	// whole-chain overlay the catalog declares. Sourced from
	// descriptor.overlayCapabilityFor() so the chain capability stays
	// byte-equal with the corresponding entries on Manifest.Overlays —
	// single source of truth for kind metadata. Sorted alphabetically by
	// Kind for golden stability. Each row carries:
	//
	//   - Kind        — the OverlayKind constant (SCREAMING_SNAKE).
	//   - Shapes      — every OverlayShape the kind may emit; whole-chain
	//                   kinds inherit the target stage's host shape so
	//                   the catalog declares MATRIX + SCALAR + SERIES.
	//   - Scopes      — the OverlayScope values the kind supports;
	//                   whole-chain kinds decorate the entire chain
	//                   result so Scopes == [total].
	//   - RefKinds    — the OverlayRef union pointer-field names the kind
	//                   consumes; the chain family consumes Stage.
	//   - Buffered    — !types.OverlayStreamable(kind); whole-chain kinds
	//                   stay buffered today (the streamability flag is
	//                   the inverse of the table in
	//                   types/overlay_streamability.go).
	//   - Description — short human-readable summary; mirrors the prose
	//                   in skills/overlay-system.md.
	Overlays []OverlayCapability `json:"overlays"`
}

// CrosstabCapability describes the cross-tabulation endpoint surfaced via
// Request.Crosstab. The manifest carries one CrosstabCapability entry
// under Manifest.Crosstab so LLM clients can detect the section, learn
// the supported normalize / shape values, and route between the matrix
// and long shapes appropriately.
type CrosstabCapability struct {
	// Name is the canonical entry identifier ("crosstab").
	Name string `json:"name"`

	// NormalizeModes lists every valid CrosstabSpec.Normalize value,
	// sorted alphabetically.
	NormalizeModes []string `json:"normalize_modes"`

	// Shapes lists every valid CrosstabSpec.Shape value, sorted
	// alphabetically.
	Shapes []string `json:"shapes"`

	// SummableAggregators is the alphabetized list of aggregator names
	// whose margin equals the sum of cells. v1 recomputes every margin
	// from raw rows for correctness; the classification is exposed so
	// callers can reason about future fast-path optimizations.
	SummableAggregators []string `json:"summable_aggregators"`
	// MeanReducibleAggregators lists aggregators whose margin is
	// derivable when each cell also carries its count.
	MeanReducibleAggregators []string `json:"mean_reducible_aggregators"`
	// IndependentAggregators lists aggregators that maintain their own
	// margin accumulator — the margin is neither a sum of cells nor a
	// re-scan (distinct_count, distinct_sum). Both crosstab paths feed
	// every record into independent row / column / grand accumulators,
	// so the margin is exact after one pass.
	IndependentAggregators []string `json:"independent_aggregators"`
	// RecomputeAggregators lists aggregators whose margin cannot be
	// derived from cells and must be recomputed over the raw filter-
	// passing rows.
	RecomputeAggregators []string `json:"recompute_aggregators"`

	// StreamingExceptions documents the only request shape that may
	// stream through the standard grouped path: shape=long, no
	// margins, no normalization. Intended for LLM-side reasoning.
	StreamingExceptions []string `json:"streaming_exceptions"`

	// RejectionRules names the structural failures the validator
	// emits. Intended for LLM-side autocomplete; not a strict schema.
	RejectionRules []string `json:"rejection_rules"`

	// SupportsNormalizeLevel reports whether the engine honors
	// CrosstabSpec.NormalizeLevel — the depth-index selector that
	// picks a parent grouper as the 100% denominator on nested row /
	// column axes. Always true in v1; carried so clients can detect
	// the feature without inferring it from CLI version.
	SupportsNormalizeLevel bool `json:"supports_normalize_level"`

	// NormalizeLevelRules names the rejection rules specific to the
	// NormalizeLevel slot. Intended for LLM-side autocomplete.
	NormalizeLevelRules []string `json:"normalize_level_rules"`

	// SupportsNormalizeWithin reports whether the engine honors
	// CrosstabSpec.NormalizeWithin — the cross-axis partition
	// selector that fixes a prefix of the OPPOSITE axis (columns
	// when normalize=row, rows when normalize=column) inside the
	// 100% denominator. Composes independently with NormalizeLevel.
	SupportsNormalizeWithin bool `json:"supports_normalize_within"`

	// NormalizeWithinRules names the rejection rules specific to the
	// NormalizeWithin slot.
	NormalizeWithinRules []string `json:"normalize_within_rules"`

	// SupportsMarginAggregations reports whether the engine honors
	// CrosstabSpec.MarginAggregations — the auxiliary aggregations
	// evaluated into the row / column / grand margin accumulators only
	// and never into a cell. The canonical use is a respondent base
	// riding alongside a weighted metric without a second scan.
	SupportsMarginAggregations bool `json:"supports_margin_aggregations"`

	// MarginAggregationRules names the rejection rules specific to the
	// MarginAggregations slot, plus the advisory that fires when the
	// section emits no margin to carry the auxiliary figures.
	MarginAggregationRules []string `json:"margin_aggregation_rules"`

	// MapValuedCellAggregators is the alphabetized list of aggregator
	// names whose Crosstab Cell output is map-valued (rich payload
	// per cell, e.g. AGG_SET_FREQUENCY's per-label row counts). These
	// aggregators emit map[string]int into MatrixCell.Value and the
	// long-shape cell rows; normalize modes (row / column / total) are
	// incompatible because dividing one map by another is undefined.
	// Derived from types.AggregationType.MapValued() at build time.
	MapValuedCellAggregators []string `json:"map_valued_cell_aggregators"`
}

// ExportFormatCapability describes the per-format export envelope
// surfaced on the manifest. One entry per format the engine's export
// dispatch supports, carrying the format identifier plus the per-format
// overlay-embedding strategy LLM planners need to decide whether a
// Response.Overlays slice will round-trip through an ExportJob to the
// chosen target.
//
// OverlaySupport is the canonical embedding-shape label declared by
// research/export-embedding-shape.md:
//
//   - "sidecar"        — the format carries a top-level
//     LIST<STRUCT> column-family appended to the
//     host stream (Arrow / Parquet). The host
//     record stream is byte-identical to the
//     overlay-free shape; readers that ignore the
//     column family see the unchanged host.
//   - "sheets"         — one workbook sheet per overlay layer named
//     "__overlay_<layer_name>" (Excel). The host
//     workbook sheet is unchanged from the overlay-
//     free shape.
//   - "trailing_block" — a single trailing line `{"_overlays": [...]}`
//     after the last host record (NDJSON). The
//     host stream is byte-identical to the overlay-
//     free shape until the trailer.
//   - "warn_and_skip"  — the format cannot embed overlays at all (CSV /
//     TSV / SPSS). The dispatcher emits one
//     PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning
//     and writes the host body verbatim; no overlay
//     output lands. Setting IncludeOverlays=false
//     suppresses the warning while keeping the same
//     body.
type ExportFormatCapability struct {
	// Name is the canonical format identifier matching the
	// io.Format constants (csv / tsv / ndjson / jsonarray /
	// parquet / arrow / excel). Stable across format-version 1.0;
	// new formats land additively.
	Name string `json:"name"`

	// OverlaySupport names the per-format overlay-embedding strategy
	// the export dispatcher follows when ExportJob.IncludeOverlays
	// resolves to true. See ExportFormatCapability godoc for the
	// canonical label vocabulary.
	OverlaySupport string `json:"overlay_support"`
}

// ExportCapability is the cross-format export envelope. Carries the
// alphabetised per-format slice so LLM planners can pick a target
// format aware of its overlay-embedding shape without inspecting the
// io/ packages.
type ExportCapability struct {
	// Formats enumerates every format the export dispatcher supports,
	// sorted alphabetically by Name for deterministic golden output.
	// One entry per format. Empty slice never appears — Pulse always
	// ships at least the canonical seven adapters.
	Formats []ExportFormatCapability `json:"formats"`
}

// ImportFormatCapability describes one tabular source format the import
// dispatch (the io.NewReader factory) accepts, surfaced on the manifest so an
// LLM planner can answer "can Pulse read this file, and will the
// resulting cohort's types be the source's or a guess?" without
// crawling io/.
//
// SchemaSource is the load-bearing slot and carries exactly two values:
//
//   - "inferred"      — the adapter yields rows of text and the shared
//     inference pass (internal/io/infer.go) samples them and
//     votes on a type per column. Correct in the
//     common case, but it is a guess: a categorical
//     column's dictionary is built in first-seen
//     order, and a type can change with the sample
//     window.
//   - "authoritative" — the adapter implements io.SchemaAwareReader and
//     hands over a schema its own source dictionary
//     DECLARES. Inference is skipped entirely, so
//     types, nullability and categorical dictionary
//     ORDER come from the file rather than from its
//     cell text.
//
// Export reports whether the SAME format can also be written today. It
// is deliberately not inferable from this block's presence: a format
// being readable says nothing about whether a writer exists. Every
// format Pulse reads it can also write as of E5-S6, which mounted the
// `.sav` writer and flipped the last false here to true — the slot stays
// because the two halves are independent and a future read-only format
// would need it again. Cross-reference ExportCapability.Formats for the
// per-format overlay-embedding shape.
type ImportFormatCapability struct {
	// Name is the canonical format identifier matching the io.Format
	// package constants (csv / tsv / ndjson / jsonarray / parquet /
	// arrow / excel / spss).
	Name string `json:"name"`

	// Extensions lists the lowercase file extensions (leading dot
	// included) that io.FormatFromPath resolves to this format, in the
	// dispatch's own order. Present so a planner can answer "what will
	// this path be detected as" without a round trip.
	Extensions []string `json:"extensions"`

	// SchemaSource is "authoritative" when the adapter implements
	// io.SchemaAwareReader and the source's own dictionary becomes the
	// .pulse schema, "inferred" when the shared sample-and-vote pass
	// decides the types. See ImportFormatCapability godoc.
	SchemaSource string `json:"schema_source"`

	// Export reports whether Pulse can also WRITE this format today.
	// False for import-only formats; cross-reference
	// ExportCapability.Formats for the writable set's overlay shapes.
	Export bool `json:"export"`
}

// ImportCapability is the cross-format import envelope — the read-side
// peer of ExportCapability. Carries the alphabetised per-format slice so
// LLM planners can route a source file to `pulse import` (or
// pulse_import) knowing both that the format is readable and whether the
// cohort's types will be the source's own.
type ImportCapability struct {
	// Formats enumerates every format the import dispatch supports,
	// sorted alphabetically by Name for deterministic golden output.
	// The native "pulse" format is excluded: it needs no tabular
	// reader and passes through untouched.
	Formats []ImportFormatCapability `json:"formats"`
}

// FacetCapability describes the rich-facet endpoint surfaced by
// pulse.FacetSchema / pulse_facet_schema. The manifest carries one
// FacetCapability entry under Manifest.Facet so LLM clients can detect
// the endpoint's feature set without inspecting the source.
type FacetCapability struct {
	// Name is the canonical entry identifier ("facet_schema").
	Name string `json:"name"`

	// SupportsDiscrete reports whether per-value count summaries are
	// available for categorical / boolean / geo fields.
	SupportsDiscrete bool `json:"supports_discrete"`

	// SupportsNumeric reports whether streaming statistics
	// (count, sum, min, max, mean, stddev) are produced for numeric
	// fields.
	SupportsNumeric bool `json:"supports_numeric"`

	// SupportsPercentiles reports whether NumericPercentiles are
	// computed via a buffered second-stage sort.
	SupportsPercentiles bool `json:"supports_percentiles"`

	// SupportsHistogram reports whether IncludeHistogram +
	// HistogramRange + HistogramBins are honoured.
	SupportsHistogram bool `json:"supports_histogram"`

	// SupportsAdditive reports whether AdditiveFields contribution
	// counts are computed.
	SupportsAdditive bool `json:"supports_additive"`

	// SupportsOverlays reports whether FacetRequest.Overlays is
	// honoured. When true, the SupportedOverlayKinds slice enumerates
	// the FACET-host overlay catalog kinds the endpoint dispatches.
	// Four FACET-host kinds wire into FacetSchema's buffered exit:
	// OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP / OVERLAY_CHISQ_VS_POP
	// / OVERLAY_KS_VS_POP.
	SupportsOverlays bool `json:"supports_overlays,omitempty"`

	// SupportedOverlayKinds enumerates the FACET-host overlay kinds the
	// endpoint dispatches. Populated only when SupportsOverlays is true;
	// each entry is a canonical OverlayKind constant string. Stable
	// alphabetical order so LLM clients see a deterministic enum.
	SupportedOverlayKinds []string `json:"supported_overlay_kinds,omitempty"`

	// StreamableConditions lists the human-readable rules that govern
	// which requests run in a single pass vs. force the buffered
	// secondary sort. The order is stable and intended for LLM-side
	// reasoning, not strict parsing.
	StreamableConditions []string `json:"streamable_conditions"`
}

// JoinCapability describes the pushdown hash-join endpoint surfaced
// by Request.Joins. The manifest carries one JoinCapability entry
// under Manifest.Join so LLM clients can detect the v1 envelope
// (inner-only, single-join) without inspecting the source.
type JoinCapability struct {
	// Name is the canonical identifier ("hash_join").
	Name string `json:"name"`

	// MaxJoinsPerRequest is the upper bound on Request.Joins length.
	// 1 today; lifts when multi-join chains land.
	MaxJoinsPerRequest int `json:"max_joins_per_request"`

	// Kinds lists supported JoinSpec.Kind values. "inner" today;
	// "left", "outer", "anti" land once the null-bitmap correctness
	// path is fully wired.
	Kinds []string `json:"kinds"`

	// SpillBytes reports the in-memory threshold beyond which the
	// build side would spill. Zero today (no spill — the build side
	// is always materialised in RAM); set when the spill path lands.
	SpillBytes int64 `json:"spill_bytes"`

	// SpillEnv names the environment variable that overrides the
	// spill threshold. Set when the spill path lands.
	SpillEnv string `json:"spill_env,omitempty"`

	// Limitations lists the v1 envelope notes for LLM clients.
	Limitations []string `json:"limitations"`
}

// OverlayCapability is the per-kind manifest entry describing one
// registered overlay catalog entry. The manifest carries one
// OverlayCapability per types.AllOverlayKinds() entry under
// Manifest.Overlays.
//
// Field shape mirrors the kind-catalog-v1 PRD §I-FR-I2 contract:
// kind × supported shapes × supported scopes × valid ref kinds ×
// buffered flag × description.
type OverlayCapability struct {
	// Kind is the on-wire SCREAMING_SNAKE OverlayKind value.
	Kind types.OverlayKind `json:"kind"`

	// Shapes lists every OverlayShape this kind may emit. Sorted
	// alphabetically for golden stability.
	Shapes []types.OverlayShape `json:"shapes"`

	// Scopes lists every OverlayScope this kind supports. Sorted
	// alphabetically for golden stability.
	Scopes []types.OverlayScope `json:"scopes"`

	// RefKinds lists the OverlayRef union pointer-field names this
	// kind consumes (e.g. "Margin"). Sorted alphabetically for golden
	// stability. These are Go field-name strings — the on-wire
	// discriminator (MarginAxis, sibling field, etc.) lives one level
	// deeper inside the chosen pointer.
	RefKinds []string `json:"ref_kinds"`

	// Buffered reports whether the orchestrator must materialise
	// records before evaluating this kind. Derived from
	// types.OverlayStreamable(kind) — Buffered is !streamable.
	Buffered bool `json:"buffered"`

	// Fields lists the OverlaySpec slot names beyond Kind / Scope /
	// Ref / Params that the kind's runtime honours. Sorted
	// alphabetically for golden stability. "level" and "within" are
	// honoured by the share / index / delta / zscore family (prefix-axis
	// denominator dispatch) while the χ² / Fisher inferential family
	// leaves the list empty (Level / Within must stay zero; non-zero
	// values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE). SHARE_OF_TOTAL
	// declares the fields for renderer-facing parity with the rest of
	// the share family even though the grand-axis denominator makes the
	// slots inert at runtime — the predict gate still accepts in-range
	// values without behavior change.
	Fields []string `json:"fields,omitempty"`

	// Description is a short human-readable summary of what the kind
	// computes. Mirrors the prose in the matching skill.
	Description string `json:"description"`

	// Zone declares the kind's time-zone participation. "following"
	// means the kind carries no zone of its own and inherits the
	// resolved zone of its host grouper (OVERLAY_YOY follows its host
	// GROUP_DATE). Empty (omitted) means the kind is zone-agnostic.
	Zone string `json:"zone,omitempty"`
}

// RegressionMeta describes a registered REG_* operator in the manifest.
// The shape mirrors Operator and TestMeta enough that LLM clients can
// reuse the same authoring path, but regression-specific knobs (family,
// link, penalty, modifier slots) live alongside the shared name +
// streamable + params trio so the catalog stays one fetch deep.
type RegressionMeta struct {
	// Name is the operator identifier (REG_OLS, REG_GLM,
	// REG_BAYES_LINEAR).
	Name string `json:"name"`

	// Description is a one-sentence prose summary for LLM-side
	// operator selection.
	Description string `json:"description"`

	// AcceptsTypes lists the schema field types valid as Target /
	// Predictors entries (numeric only in v1).
	AcceptsTypes []string `json:"accepts_types"`

	// EmitsTypeNote describes the structured output (RegressionResult)
	// since no scalar field type captures the fit summary.
	EmitsTypeNote string `json:"emits_type_note"`

	// Streamable mirrors types.RegressionType.Streamable(): true for
	// closed-form fits (REG_OLS, REG_BAYES_LINEAR), false for iterative
	// fits (REG_GLM). Spec-level modifiers (Resample, Selection) can
	// downgrade this further per request — see RegressionSpec.Streamable.
	Streamable bool `json:"streamable"`

	// StreamableHint flags the modifier downgrade rule so clients know
	// non-empty Resample / Selection forces the buffered path even on
	// streamable operators.
	StreamableHint string `json:"streamable_hint,omitempty"`

	// Params lists the per-operator parameter schema, including the
	// regularization, family, prior, resample, and selection knobs that
	// modify the underlying fit.
	Params []Param `json:"params"`

	// Modifiers names the spec-level orthogonal wrappers any regression
	// type supports. Each entry is a top-level RegressionSpec field
	// (Resample, Selection) plus its enum values; the field is
	// duplicated under Params for completeness but exposed at the top
	// level here so request authors can discover the composition story
	// without parsing the full param list.
	Modifiers []RegressionModifier `json:"modifiers,omitempty"`
}

// RegressionModifier describes a spec-level wrapper (Resample,
// Selection) that composes with any regression operator. Each modifier
// downgrades streamability when set; clients combine its EnumValues
// with their chosen Type to author the request.
type RegressionModifier struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	EnumValues  []string `json:"enum_values"`
}
