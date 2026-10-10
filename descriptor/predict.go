package descriptor

import (
	"github.com/frankbardon/pulse/types"
)

// PredictResult holds the validated request and any diagnostics.
type PredictResult struct {
	Valid      bool               `json:"valid"`
	Request    *types.Request     `json:"request"`
	SchemaInfo *PredictSchemaInfo `json:"schema_info,omitempty"`
	// RecordCount reports the cumulative record total across all shards
	// when the cohort is a shard archive, and zero for single-file
	// cohorts (predict reads only headers/schemas, so single-file
	// counts are not computed by this no-execute path). The count is
	// derived by peeking each shard's own header.
	RecordCount int64 `json:"record_count"`
	// Shards mirrors InspectResult.Shards for archive-backed cohorts.
	// Empty (but non-nil) for single-file cohorts. Listed in zip
	// central-directory order.
	Shards []ShardInfo `json:"shards"`
	// Streamable reports whether ProcessStream / process --stream can
	// emit rows without buffering the entire result. False whenever the
	// request uses groups, attributes, windows, decimal fields, or any
	// non-streamable operator. Computed via per-type Streamable() methods
	// plus schema-aware checks.
	//
	// Streamability is a property of the request and the canonical
	// schema, not of the cohort shape — archive-backed cohorts inherit
	// the same streamability as a single-file cohort with the same
	// schema.
	Streamable bool `json:"streamable"`
	// StreamableReasons lists the gates that forced Streamable=false. Empty
	// when Streamable=true. Useful for users debugging why their request
	// is buffering.
	StreamableReasons []string `json:"streamable_reasons,omitempty"`
	// CrosstabFusable reports whether this instance would run the
	// request's crosstab on the fused in-decode arm (O(cells + margins)
	// memory, one decode pass) rather than the buffered arm that holds
	// every filter-passing record. Nil when the request carries no
	// crosstab spec. Computed from the same rule the engine dispatches
	// on (internal/crosstabfuse), on the defaults-resolved request, and
	// instance-aware: Options.DisableCrosstabFusion answers false with a
	// reason. Both arms produce identical output; only memory differs.
	//
	// The one fact predict cannot know is whether a keyable grouper's
	// factory accepts its params (e.g. a GROUP_DATE component the
	// runtime rejects): predict may then say true while the runtime
	// declines and the buffered path refuses the request with a coded
	// error — the request errors on either answer.
	CrosstabFusable *bool `json:"crosstab_fusable,omitempty"`
	// CrosstabFusionReasons lists, in rule order, every reason
	// CrosstabFusable is false — short stable prose. Empty when it is
	// true or nil.
	CrosstabFusionReasons []string `json:"crosstab_fusion_reasons,omitempty"`
	// Suggestions enumerates structured next-actions the caller can apply
	// to repair (or improve) the request. Suggestions fire on validation
	// issues — field-name typos, operator/type mismatches, date misuse,
	// missing required params — and on non-streamable but otherwise valid
	// requests (streamable-substitute hints). May be empty; never nil in
	// JSON output.
	Suggestions []Suggestion `json:"suggestions"`
	// DefaultsApplied lists every operator slot whose Type was inferred
	// from the named field's schema type. Predict computes this on a
	// clone of the request, so the echoed Request reflects exactly what
	// the engine would run; the DefaultsApplied list shows what would
	// have been filled in. Empty when no defaults fire; never nil in
	// JSON output.
	DefaultsApplied []DefaultApplied `json:"defaults_applied"`

	// Aggregations mirrors req.Aggregations in order, attaching the
	// per-slot ComponentSchema the engine will use at runtime (the
	// operator's `descriptor.ComponentSchema` projected from the
	// capabilities table — and via the manifest snapshot for
	// embedder-registered ops) plus the BufferedComponents flag.
	//
	// BufferedComponents is true iff the operator's
	// ComponentSchema.Mergeability is None — meaning the components
	// map cannot be reconstructed from per-chunk partials and the
	// orchestrator emits the Components block only on the terminal
	// buffered flush. The flag is a hint to streaming consumers; it
	// does NOT flip the overall Streamable axis (the data slice still
	// streams). Today the canonical None-mergeability operators are
	// AGG_MEDIAN and AGG_PERCENTILE.
	//
	// Empty when req.Aggregations is empty; never nil in JSON output.
	Aggregations []AggregationPredict `json:"aggregations"`

	// Groups mirrors req.Groups in order, attaching the per-slot
	// ComponentSchema the engine will use at runtime (the operator's
	// `descriptor.ComponentSchema` projected from the capabilities
	// table — same projection ComponentsSchemas.Groupers uses) plus
	// the BufferedComponents flag.
	//
	// BufferedComponents is true iff the operator's
	// ComponentSchema.Mergeability is None — meaning the components
	// map cannot be reconstructed from per-chunk partials and the
	// orchestrator emits the Components block only on the terminal
	// buffered flush. The flag is a hint to streaming consumers; it
	// does NOT flip the overall Streamable axis. Of the seven
	// registered groupers today, GROUP_QUANTILE is the canonical
	// None — quantile cutoffs need the sorted full input.
	//
	// Empty when req.Groups is empty; never nil in JSON output.
	Groups []GroupPredict `json:"groups"`

	// Filterers mirrors req.Filterers in order, attaching the per-slot
	// ComponentSchema the engine will use at runtime (the operator's
	// `descriptor.ComponentSchema` projected from the capabilities
	// table — same projection ComponentsSchemas.Filterers uses) plus
	// the BufferedComponents flag.
	//
	// Filterer components in v1 are uniform across every registered
	// filterer — the orchestrator emits the universal floor
	// {n_in, n_out, n_null_input} from the filter pass's per-record
	// counters. Mergeability is Mergeable for every entry today
	// (counters fold trivially via integer addition), so
	// BufferedComponents is always false. The flag is still surfaced
	// on the predict struct for symmetry with AggregationPredict and
	// GroupPredict so future per-filter specifics (e.g. n_below /
	// n_above for FILTER_RANGE) can downgrade the slot without
	// changing the predict shape.
	//
	// Empty when req.Filterers is empty; never nil in JSON output.
	Filterers []FiltererPredict `json:"filterers"`

	// OverlaysApplied lists every overlay spec the engine accepted, in
	// req.Overlays order. Each entry echoes the catalog identity (Name,
	// Kind, Scope) plus the streamability flag harvested from
	// types.OverlayStreamable(kind) so the caller can reason about the
	// buffered/streaming routing decision without re-parsing the spec.
	// Empty when req.Overlays is empty; never nil in JSON output.
	//
	// Per kind-catalog-v1 PRD §I-FR-I3 the predict surface is
	// `OverlaysApplied + OverlaysSchemaDivergence + OverlayCost`.
	// Predict emits one descriptor per spec in matching order across
	// the full overlay catalog; new kinds appended to
	// types.AllOverlayKinds() inherit the same per-spec emission
	// without re-opening this slot. The streamability-derived
	// dispatch routes streamable kinds to overlayCostStreamable and
	// buffered kinds to overlayCostBuffered; every kind in
	// types.AllOverlayKinds() carries a multiplier, and the
	// multi-ref scaling rule lives on the COMPOSE-host equivalent at
	// ComposeValidationResult.OverlayCost since Targets / MaxPanelTargets
	// only exist on ComposeOverlaySpec.
	OverlaysApplied []OverlayAppliedDescriptor `json:"overlays_applied"`

	// OverlaysSchemaDivergence lists every (left, right) overlay-spec
	// slot pair that produced incompatible result-schema shapes. Empty
	// for the Request-only Predict surface — divergence detection lives
	// on the Compose-host validator (ValidateCompose) — but the field is
	// present so consumers can rely on the shape today. Per PRD §I-FR-I3
	// the placeholder is honoured as "this slot is reserved and shipped
	// empty".
	OverlaysSchemaDivergence []SlotPair `json:"overlays_schema_divergence"`

	// OverlayCost maps each overlay-spec Name to a coarse cost score
	// the routing layer can consult before execution. The score is a
	// rough record-count multiplier per overlay slot — streamable kinds
	// fold inside the existing streaming pass (one extra accumulator per
	// record) and carry overlayCostStreamable; buffered kinds force a
	// post-host re-traversal of the materialised payload and carry
	// overlayCostBuffered. Per kind-catalog-v1 PRD §I-FR-I3 the value is
	// intentionally coarse — callers budgeting cost across slots sum the
	// map values; renderers showing a "this overlay will buffer the
	// host" warning branch on >= overlayCostBuffered. The map key is
	// the overlay spec's Name when set, and the synthesised default
	// (Kind + Scope + Ref) when empty — matching the Name field on
	// OverlayAppliedDescriptor below. Empty when req.Overlays is empty;
	// never nil in JSON output.
	OverlayCost map[string]float64 `json:"overlay_cost"`

	// TimeZones lists the resolved zone of every zone-capable slot
	// (GROUP_DATE, GROUP_DATE_RANGES, FILTER_DATE_RANGES, ATTR_DATE_PART,
	// FEAT_DATE_FEATURES — crosstab axes included) in request order:
	// filterers, features, attributes, groups, crosstab rows, crosstab
	// columns. Resolution is slot `tz` → Request.TimeZone →
	// Options.DefaultTimeZone → UTC, computed by the same function the
	// runtime validates with. Empty when no zone-capable slot is present;
	// never nil in JSON output.
	TimeZones []ResolvedZone `json:"time_zones"`

	// Weights lists how a row weight resolves on every weight-bearing
	// slot (aggregations, the crosstab cell and margin aggregations,
	// tests, post-tests, regressions, attributes, overlays) in that
	// order. Resolution is slot `weight` → Request.Weight →
	// Options.DefaultWeight → none, computed by the same function the
	// runtime validates with. Omitted when the request, its slots and
	// the instance name no weight at all, so an unweighted request's
	// predict output is unchanged.
	Weights []ResolvedWeight `json:"weights,omitempty"`

	// SuggestedWeight echoes inspect's suggested_weight as DATA (never
	// a warning, so strict mode is unaffected) when no slot resolves a
	// weight field. Never applied. Omitted when there is nothing to
	// suggest, a weight resolves, or the instance hides row weighting.
	SuggestedWeight *SuggestedWeight `json:"suggested_weight,omitempty"`

	// PValues counts the inferential p-values the request would emit —
	// one per test and post-test (the Tukey HSD post-test excluded: its
	// p-values are already corrected) plus every p-value an inferential
	// overlay layer carries — and how many of them no multiple-comparison
	// correction reaches. It is the trigger data for the
	// PULSE_ADVISORY_MANY_TESTS advisory: compare Uncorrected with
	// Threshold. Omitted when the request emits no p-value, when its
	// multiplicity blocks are refused, or when the instance hides the
	// multiple-comparison capability.
	PValues *PValueCount `json:"p_values,omitempty"`

	// Advisories lists coded, non-blocking notes that the chosen
	// analysis may not fit the data (PULSE_ADVISORY_* codes), in rule
	// order. Separate from the envelope's warnings: strict mode never
	// escalates one, and none changes execution. Computed from the
	// request, the schema and its dictionaries only. Omitted when no
	// rule fires or every firing code is in Options.SuppressAdvisories.
	Advisories []Advisory `json:"advisories,omitempty"`

	// ResolvedVectors maps each Request.Vectors name to its resolved
	// member list in axis order — literal `fields` entries in the
	// caller's order, glob and `pattern` expansions in schema order —
	// computed by the same resolver the runtime refuses with, against
	// the schema the request executes over. Omitted when the request
	// defines no vector or a vector is refused (the refusal is the
	// predict error).
	ResolvedVectors map[string][]string `json:"resolved_vectors,omitempty"`

	// Matrices mirrors req.Matrices in order once every spec resolves:
	// each matrix's shape, axis, missing mode, estimated accumulator
	// size, streamability and pairwise PSD risk, computed by the same
	// resolver and rules the runtime runs. Omitted when the request
	// carries no matrix or a spec is refused (the refusal is the
	// predict error).
	Matrices []MatrixPredict `json:"matrices,omitempty"`

	// Sizes estimates each top-level Response section the request
	// produces, unshaped and under the effective `return` plan, in
	// Response key order — reported on every request, with or without
	// a `return` block (no effective block: shaped equals full). A
	// section whose size depends on data predict cannot see (a grouper
	// with data-dependent keys, a join's column set, a single file's
	// row-level output with no record count) is omitted; a section the
	// request does not produce is omitted. Omitted entirely when the
	// `return` block is refused. Guidance only — never a byte-accurate
	// figure.
	Sizes []ResponseSectionSize `json:"sizes,omitempty"`

	// LimitFindings lists every instance resource limit (pulse.Limits)
	// the request's predictable figures exceed, in limit-declaration
	// order. A Certain finding is an exact figure the runtime will see —
	// predict adds the PULSE_LIMIT_EXCEEDED error the process pre-flight
	// raises and Valid is false. A Possible finding is an upper bound: a
	// warning only, Valid is untouched. Omitted when nothing exceeds a
	// limit.
	LimitFindings []LimitFinding `json:"limit_findings,omitempty"`

	// Return is the resolved `return` plan — the expanded selection the
	// runtime will apply, computed by the same resolver it calls.
	// Omitted when the request carries no `return` block or the block is
	// refused (the refusal is the predict error).
	Return *ReturnPlan `json:"return,omitempty"`
}

// ReturnPlan is a resolved `return` block: the canonical path sets the
// selection reduces to and a digest identifying it. Include, Exclude and
// Keep are deduplicated, sorted, and free of paths another one already
// covers, so equivalent spellings resolve to the same plan and digest.
type ReturnPlan struct {
	// Preset is the preset the block resolved from: full, standard,
	// minimal, or custom for explicit include / exclude without one.
	Preset string `json:"preset"`
	// Include is the selected paths (each with its whole subtree).
	Include []string `json:"include"`
	// Exclude is the removed paths; exclusion wins over inclusion.
	Exclude []string `json:"exclude"`
	// Keep is the nested `warnings` slots retained wherever their parent
	// object is emitted (unless excluded).
	Keep []string `json:"keep"`
	// Precision is the wire float significant-digit count; omitted when
	// unlimited.
	Precision int `json:"precision,omitempty"`
	// Identity reports that the plan changes nothing (the whole
	// response, unrounded).
	Identity bool `json:"identity"`
	// Digest identifies the resolved selection ("rp1:" + sha256 hex);
	// the preset name does not enter it.
	Digest string `json:"digest"`
	// UnresolvedIncludes lists the Open include paths (through a map key
	// or an open value) predict cannot resolve without data: at runtime
	// each may match nothing, which a buffered run reports as
	// PULSE_RETURN_PATH_UNMATCHED and a stream never reports. A
	// `data[*].<column>` include predict can match against the
	// request's known output columns is resolved and not listed.
	// Omitted when none.
	UnresolvedIncludes []string `json:"unresolved_includes,omitempty"`
}

// ResponseSectionSize is the size estimate of one top-level Response
// section (`data`, `metadata`, `tests`, `post_tests`, `regressions`,
// `matrices`, `crosstab`, `overlays`, `components`): rows or cells
// times a bytes-per-value model of its compact JSON form.
type ResponseSectionSize struct {
	// Section is the top-level Response key.
	Section string `json:"section"`
	// FullBytes estimates the section's wire bytes without the
	// selection.
	FullBytes int64 `json:"full_bytes"`
	// ShapedBytes estimates the section's wire bytes under the
	// effective `return` plan — the same model with every node the plan
	// drops removed; 0 when the plan excludes the section, FullBytes
	// when there is no effective plan. Never above FullBytes.
	ShapedBytes int64 `json:"shaped_bytes"`
	// Basis says how the section's row / cell count was derived:
	// "exact" (fixed by the request and schema — an ungrouped
	// aggregation's one row, an ungrouped matrix), "upper_bound" (a
	// schema bound the run never exceeds — grouper dictionary sizes
	// clamped to the record count, crosstab axis dictionaries, the
	// record count of a row-level output) or "heuristic" (a typical-size
	// guess — test details, regression terms, overlay payloads,
	// component operator maps, metadata).
	Basis string `json:"basis"`
}

// MatrixPredict is the per-spec predict surface for one entry of
// req.Matrices.
type MatrixPredict struct {
	// Name is the result name (MatrixSpec.EffectiveName).
	Name string `json:"name"`
	// Type is the matrix operator.
	Type types.MatrixType `json:"type"`
	// Shape is [p, p]: rows × columns of every matrix in the result.
	Shape [2]int `json:"shape"`
	// AxisKeys are the member field names in axis order — the result's
	// row_keys and column_keys.
	AxisKeys []string `json:"axis_keys"`
	// Labels are the members' display labels in axis order when the
	// vector supplies them (the result's labels); omitted otherwise.
	Labels []string `json:"labels,omitempty"`
	// Missing is the missing-data mode, "listwise" or "pairwise".
	Missing string `json:"missing"`
	// Encoding is the effective Values layout.
	Encoding types.MatrixEncoding `json:"encoding"`
	// AccumulatorBytes estimates the payload bytes of one co-moment
	// state over the members (32 + 8·(p + p(p+1)/2) listwise,
	// 32 + 56·p(p+1)/2 pairwise). The run holds one per populated merge
	// block (Manifest.Matrix.merge_block_size rows) until finalize, so
	// its matrix state is this times its block count (grouped: times
	// its buckets too — EstimatedBytes).
	AccumulatorBytes int64 `json:"accumulator_bytes"`
	// BucketBasis says how EstimatedBuckets was derived from the
	// request and the schema: "ungrouped" (1), "dictionary" (the
	// Groups[0] field's dictionary size), "boolean" (2), "include"
	// (Group.Include's distinct reachable keys), "quantile_bins"
	// (GROUP_QUANTILE's bin count) or "unknown" (the keys depend on the
	// data — ranges, dates, numeric categories — and the three estimate
	// fields are omitted).
	BucketBasis string `json:"bucket_basis"`
	// EstimatedBuckets is the upper bound on the results this spec
	// emits — one per non-empty bucket of Groups[0]; a bucket no row
	// reaches is not emitted, so a run returns at most this many.
	EstimatedBuckets *int64 `json:"estimated_buckets,omitempty"`
	// EstimatedCells is EstimatedBuckets × p² — the primary cells the
	// spec's results carry in full encoding.
	EstimatedCells *int64 `json:"estimated_cells,omitempty"`
	// EstimatedBytes is the run's co-moment state: merge blocks ×
	// EstimatedBuckets × AccumulatorBytes, the blocks being
	// ceil(records / merge_block_size) — per shard for an archive, whose
	// block numbering restarts per shard. An upper bound (a bucket holds
	// one state per block its rows touch). Omitted with the bucket count
	// unknown. A buffered spec adds its RowBufferBytes. It is the matrix
	// term of the MaxEstimatedMemory estimate.
	EstimatedBytes *int64 `json:"estimated_bytes,omitempty"`
	// Streamable reports whether this spec folds row by row (its
	// result is emitted at finalize — terminal flush when streamed):
	// the spec-level types.MatrixSpec.Streamable, the type folded with
	// its params (a rank method is buffered). Request-level routing
	// (groupers, two-pass attributes) is PredictResult.Streamable.
	Streamable bool `json:"streamable"`
	// Mergeable reports whether this spec's running state combines
	// across partitions — the spec-level types.MatrixSpec.Mergeable (a
	// rank method is not). A request carrying a non-mergeable spec runs
	// serially: DecodeWorkers / ShardWorkers do not fan it out.
	Mergeable bool `json:"mergeable"`
	// RowBufferBytes estimates the row store a BUFFERED spec (not
	// Streamable — a rank method) keeps until finalize: 8·(p + 1) bytes
	// per admitted row (its member values and weight) times the
	// cohort's record count, an upper bound. Omitted on a streamable
	// spec or with the record count unknown; EstimatedBytes includes it.
	RowBufferBytes *int64 `json:"row_buffer_bytes,omitempty"`
	// PairwisePSDRisk reports whether the result (or a decomposition
	// operator's input) can come back not positive semidefinite —
	// pairwise MAT_COVARIANCE at p ≥ 2, pairwise MAT_CORRELATION and
	// MAT_PARTIAL_CORRELATION at p ≥ 3 (folded columns) — the only
	// shapes the runtime checks for PULSE_MATRIX_NOT_PSD. False
	// guarantees the code never appears; true says it can, depending on
	// the data: a warning, or on MAT_PARTIAL_CORRELATION a refusal
	// unless params.repair is "nearest".
	PairwisePSDRisk bool `json:"pairwise_psd_risk"`
}

// MultiplicityTriggerThreshold is the uncorrected p-value count at or
// above which correction is worth suggesting: with ten independent
// tests at alpha 0.05 the chance of at least one false positive is
// already about 40%. Echoed on PValueCount.Threshold.
const MultiplicityTriggerThreshold = 10

// How a PValueCount was derived, weakest contribution wins.
const (
	// PValueBasisExact: every contribution is fixed by the request
	// shape (a test, a whole-table overlay).
	PValueBasisExact = "exact"
	// PValueBasisDictionary: at least one overlay's count was read off
	// a crosstab axis's categorical dictionary, assuming every
	// dictionary entry is a populated bucket (an unobserved entry
	// lowers the real count, a null bucket raises it).
	PValueBasisDictionary = "dictionary"
	// PValueBasisLowerBound: at least one overlay's extent could not be
	// derived from the schema (a numeric or multi-field axis); it
	// counted as one p-value, so Total and Uncorrected are floors.
	PValueBasisLowerBound = "lower_bound"
)

// PValueCount is PredictResult.PValues.
type PValueCount struct {
	// Total is the number of inferential p-values the request emits.
	Total int `json:"total"`
	// Uncorrected is how many of Total no correction reaches (their
	// slot's resolved method is none).
	Uncorrected int `json:"uncorrected"`
	// Basis is how the counts were derived: PValueBasisExact,
	// PValueBasisDictionary or PValueBasisLowerBound.
	Basis string `json:"basis"`
	// Threshold is MultiplicityTriggerThreshold.
	Threshold int `json:"threshold"`
}

// Zone-resolution sources reported on ResolvedZone.Source.
const (
	// ZoneSourceSlot: the slot's own `tz`.
	ZoneSourceSlot = "slot"
	// ZoneSourceRequest: the request root's `time_zone`.
	ZoneSourceRequest = "request"
	// ZoneSourceOptions: pulse.Options.DefaultTimeZone.
	ZoneSourceOptions = "options"
	// ZoneSourceDefault: nothing named a zone; UTC.
	ZoneSourceDefault = "default"
)

// ResolvedZone is the resolved time zone of one zone-capable request
// slot.
type ResolvedZone struct {
	// Slot addresses the slot by its JSON path inside the request, e.g.
	// "groups[0]", "filterers[1]", "attributes[2]", "features[0]",
	// "crosstab.rows[0]", "crosstab.columns[1]".
	Slot string `json:"slot"`

	// Operator is the slot's operator type (post smart-defaults).
	Operator string `json:"operator"`

	// FieldType is the schema type of the slot's field ("datetime",
	// "date", ...), or empty when the field is not in the cohort schema
	// (a derived column).
	FieldType string `json:"field_type,omitempty"`

	// TZ is the zone name that applies to the slot, spelled as the
	// caller (or Options.DefaultTimeZone) spelled it; "UTC" when nothing
	// named one. Null when the field is not `datetime` and the zone was
	// inherited: a calendar date carries no instant, so an inherited
	// zone is not applied to it.
	TZ *string `json:"tz"`

	// Source names where TZ came from: "slot", "request", "options" or
	// "default". On a null TZ it names the level the skipped zone was
	// inherited from.
	Source string `json:"source"`
}

// Weight-resolution sources reported on ResolvedWeight.Source.
const (
	// WeightSourceSlot: the slot's own `weight` (a `null` included).
	WeightSourceSlot = "slot"
	// WeightSourceRequest: the request root's `weight`.
	WeightSourceRequest = "request"
	// WeightSourceOptions: pulse.Options.DefaultWeight.
	WeightSourceOptions = "options"
	// WeightSourceNone: nothing named a weight.
	WeightSourceNone = "none"
)

// Weight-resolution outcomes reported on ResolvedWeight.Status.
const (
	// WeightStatusApplied: the slot's operator consumes the resolved
	// weight.
	WeightStatusApplied = "applied"
	// WeightStatusSkippedNotWeightAware: a weight resolved, but the
	// slot's operator does not consume one; the slot runs unweighted.
	WeightStatusSkippedNotWeightAware = "skipped_not_weight_aware"
	// WeightStatusOptedOut: the slot's own `weight` is `null`.
	WeightStatusOptedOut = "opted_out"
	// WeightStatusNone: no weight resolves for the slot.
	WeightStatusNone = "none"
)

// ResolvedWeight is the resolved row weight of one weight-bearing
// request slot.
type ResolvedWeight struct {
	// Slot addresses the slot by its JSON path inside the request, e.g.
	// "aggregations[0]", "crosstab.cell",
	// "crosstab.margin_aggregations[1]", "tests[0]", "overlays[2]".
	Slot string `json:"slot"`

	// Operator is the slot's operator type (post smart-defaults), or
	// the overlay kind on an overlay slot.
	Operator string `json:"operator"`

	// Field is the resolved weight field; empty when Status is
	// "opted_out" or "none".
	Field string `json:"field,omitempty"`

	// Kind is the resolved weight kind ("probability" or "frequency",
	// the default spelled out); empty when Field is.
	Kind string `json:"kind,omitempty"`

	// Status is "applied", "skipped_not_weight_aware", "opted_out" or
	// "none".
	Status string `json:"status"`

	// Source names where the weight came from: "slot", "request",
	// "options" or "none".
	Source string `json:"source"`
}

// AggregationPredict is the per-slot predict surface for one entry of
// req.Aggregations. Carries the operator identity echoed back to the
// caller plus the static ComponentSchema declared in the capabilities
// table — so callers can budget the runtime Components block shape
// before the engine runs and so streaming consumers can branch on the
// BufferedComponents hint without consulting the manifest.
//
// Predict stays no-execute: every field on this struct is sourced from
// the descriptor capability projection (or the embedder snapshot for
// extension operators) — never from a constructed operator. Custom
// operators registered via pulse.Options.Extensions that do not (yet)
// supply a ComponentSchema produce an empty ComponentSchema and
// BufferedComponents=false; the universal-floor populator at
// orchestrator emission time still attaches {"n", "n_null"} at
// runtime.
type AggregationPredict struct {
	// Type echoes the aggregation type from the request slot, in the
	// canonical SCREAMING_SNAKE form (e.g. "AGG_MEDIAN").
	Type types.AggregationType `json:"type"`

	// Field echoes the field referenced by the request slot. Empty
	// when the slot was authored without a field (defaults may have
	// filled it; the resolved post-defaults clone is reflected here
	// when applicable).
	Field string `json:"field,omitempty"`

	// Label echoes the response-side label the caller assigned to the
	// slot (or the engine-default label rule fallback when empty).
	// Predict echoes the raw label only — label fallback synthesis is
	// the orchestrator's responsibility.
	Label string `json:"label,omitempty"`

	// ComponentSchema is the static schema for the operator the engine
	// will use at runtime, projected from the capabilities table
	// (capabilities_aggregators.go for built-ins; the embedder snapshot
	// for extensions). Empty Keys + empty Mergeability means the slot
	// references an extension operator that has not (yet) declared a
	// ComponentSchema — the universal-floor populator still attaches
	// {"n", "n_null"} at runtime.
	ComponentSchema ComponentSchema `json:"component_schema"`

	// BufferedComponents is true iff ComponentSchema.Mergeability is
	// descriptor.None. The flag advertises that the slot's Components
	// block will arrive only on the terminal buffered flush in a
	// streaming run; it does NOT flip the overall Streamable axis (the
	// data slice still streams). AGG_MEDIAN and AGG_PERCENTILE are the
	// canonical Nones today.
	BufferedComponents bool `json:"buffered_components,omitempty"`
}

// GroupPredict is the per-slot predict surface for one entry of
// req.Groups. Carries the grouper identity echoed back to the caller
// plus the static ComponentSchema declared in the capabilities table —
// so callers can budget the runtime Components.Groupers block shape
// before the engine runs and so streaming consumers can branch on the
// BufferedComponents hint without consulting the manifest.
//
// Predict stays no-execute: every field on this struct is sourced from
// the descriptor capability projection — never from a constructed
// grouper. Custom groupers registered via pulse.Options.Extensions
// that do not (yet) supply a ComponentSchema produce an empty
// ComponentSchema and BufferedComponents=false; the universal-floor
// populator at orchestrator emission time still attaches
// {"total_n", "n_null"} at runtime.
type GroupPredict struct {
	// Type echoes the grouper type from the request slot, in the
	// canonical SCREAMING_SNAKE form (e.g. "GROUP_QUANTILE").
	Type types.GroupType `json:"type"`

	// Field echoes the field referenced by the request slot. Empty
	// when the slot was authored without a field (defaults may have
	// filled it; the resolved post-defaults clone is reflected here
	// when applicable).
	Field string `json:"field,omitempty"`

	// ComponentSchema is the static schema for the grouper the engine
	// will use at runtime, projected from the capabilities table
	// (capabilities_groupers.go). Empty Keys + empty Mergeability
	// means the slot references an extension grouper that has not
	// (yet) declared a ComponentSchema — the universal-floor
	// populator still attaches {"total_n", "n_null"} at runtime.
	ComponentSchema ComponentSchema `json:"component_schema"`

	// BufferedComponents is true iff ComponentSchema.Mergeability is
	// descriptor.None. The flag advertises that the slot's Components
	// block will arrive only on the terminal buffered flush in a
	// streaming run; it does NOT flip the overall Streamable axis (the
	// data slice still streams). GROUP_QUANTILE is the canonical
	// None today.
	BufferedComponents bool `json:"buffered_components,omitempty"`
}

// FiltererPredict is the per-slot predict surface for one entry of
// req.Filterers. Carries the operator identity echoed back to the
// caller plus the static ComponentSchema declared in the capabilities
// table — so callers can budget the runtime Components.Filterers block
// shape before the engine runs and so streaming consumers can branch
// on the BufferedComponents hint without consulting the manifest.
//
// Predict stays no-execute: every field on this struct is sourced from
// the descriptor capability projection — never from a constructed
// filterer. Custom filterers registered via pulse.Options.Extensions
// that do not (yet) supply a ComponentSchema produce an empty
// ComponentSchema and BufferedComponents=false; the universal-floor
// populator at orchestrator emission time still attaches
// {"n_in", "n_out", "n_null_input"} at runtime.
//
// Components are uniform across every built-in filterer in v1: the
// universal floor {n_in, n_out, n_null_input} is the entire schema and
// Mergeability is Mergeable (counters fold trivially via integer
// addition). The struct mirrors AggregationPredict / GroupPredict so
// future per-filter specifics (e.g. n_below / n_above for FILTER_RANGE)
// can downgrade the slot to Partial / None without changing the
// predict shape.
type FiltererPredict struct {
	// Type echoes the filterer type from the request slot, in the
	// canonical SCREAMING_SNAKE form (e.g. "FILTER_RANGE").
	Type types.FiltererType `json:"type"`

	// Field echoes the field referenced by the request slot. Empty
	// when the slot was authored without a field (FILTER_EXPRESSION
	// reads any record field through its expression body).
	Field string `json:"field,omitempty"`

	// ComponentSchema is the static schema for the filterer the engine
	// will use at runtime, projected from the capabilities table
	// (capabilities_filterers.go). Empty Keys + empty Mergeability
	// means the slot references an extension filterer that has not
	// (yet) declared a ComponentSchema — the universal-floor populator
	// still attaches {"n_in", "n_out", "n_null_input"} at runtime.
	ComponentSchema ComponentSchema `json:"component_schema"`

	// BufferedComponents is true iff ComponentSchema.Mergeability is
	// descriptor.None. The flag advertises that the slot's Components
	// block will arrive only on the terminal buffered flush in a
	// streaming run; it does NOT flip the overall Streamable axis. In
	// v1 every built-in filterer is Mergeable so this flag is always
	// false; the surface exists for future per-filter specifics that
	// may downgrade individual entries.
	BufferedComponents bool `json:"buffered_components,omitempty"`
}

// OverlayAppliedDescriptor describes one accepted overlay spec the
// predict path observed. Mirrors DefaultApplied / Suggestion in shape:
// JSON-serialisable, omitempty-free for slice-friendly emit, populated
// from the catalog entry rather than the raw spec so the caller cannot
// confuse the surface with the raw OverlaySpec it submitted.
//
// Streamable echoes types.OverlayStreamable(kind). Note that a kind
// can be streamable in isolation but force buffered when its host
// (today: crosstab) is itself buffered — predict reports the kind's
// own streamability, not the composed host-overlay decision. Callers
// that need the composed decision should consult
// PredictResult.Streamable + StreamableReasons.
type OverlayAppliedDescriptor struct {
	// Name echoes the renderer-facing label — either the spec's own
	// Name or a synthesised default (Kind + Scope + Ref) when empty.
	Name string `json:"name"`

	// Kind is the on-wire SCREAMING_SNAKE OverlayKind value.
	Kind types.OverlayKind `json:"kind"`

	// Scope echoes the spec's scope.
	Scope types.OverlayScope `json:"scope"`

	// Shape echoes the resolved OverlayShape the layer will carry —
	// derived from (Kind, Scope) via the capability catalog
	// (descriptor.overlayCapabilityFor). For single-shape kinds the
	// shape is unambiguous; for dual-shape kinds (OVERLAY_INDEX_VS_REF
	// / OVERLAY_DELTA_VS_REF / OVERLAY_PANEL_INDEX_VS_REF) the resolver
	// picks the shape matching the spec's scope (CELL ⇒ matrix, GROUP
	// ⇒ series); for whole-chain kinds (OVERLAY_INDEX_VS_STAGE /
	// OVERLAY_DELTA_VS_STAGE) the catalog declares every shape the
	// kind may emit (scalar / series / matrix) and the descriptor
	// leaves Shape empty — the realised shape depends on the target
	// stage's host shape, which Predict cannot determine without
	// inspecting the chain. Empty when the kind is unknown (the
	// validator surfaces PULSE_OVERLAY_KIND_UNKNOWN for the same
	// condition) or when no single shape can be resolved from the
	// spec at predict time.
	Shape types.OverlayShape `json:"shape,omitempty"`

	// Ref echoes the resolved OverlayRef discriminated union variant
	// as a renderer-friendly string. Format: "<family>:<discriminator>"
	// when the family carries an on-wire discriminator, "<family>" for
	// marker-only families, and "" for implicit-margin kinds (every
	// CHISQ_* / FISHER_EXACT_CELL / INDEX_VS_TOTAL / FORMULA) and the
	// COMPOSE-only catalog (Reference / Targets resolve via slot
	// labels, not via the OverlayRef discriminated union). Concrete
	// shapes:
	//
	//   - "margin:row" / "margin:column" / "margin:grand" — Ref.Margin
	//   - "sibling:<field>=<value>"                        — Ref.Sibling
	//   - "baseline_index:<position>"                      — Ref.BaselineIndex
	//   - "prior:<lag>"                                    — Ref.Prior
	//   - "rolling_mean"                                   — Ref.RollingMean (marker)
	//   - "yoy"                                            — Ref.YoY (marker)
	//   - "population:<cohort>"                            — Ref.Population
	//   - "stage:index:<index>" / "stage:name:<name>"      — Ref.Stage
	//   - "slot:<label>"                                   — Ref.Slot
	Ref string `json:"ref,omitempty"`

	// Streamable mirrors types.OverlayStreamable(kind). False for
	// the buffered crosstab catalog; streamable kinds surface the
	// per-kind decision uniformly.
	Streamable bool `json:"streamable"`
}

// SlotPair carries one rejected (Reference, Target) Compose-overlay
// slot pair from internal/descriptor.ValidateCompose's overlay walk. Mirrors
// ChainOverlaySchemaDivergence in spirit but flattens to a string-typed
// (ref label, target label, machine-readable reason) tuple so the
// PredictResult.OverlaysSchemaDivergence slot and the
// ComposeValidationResult.OverlaysSchemaDivergence slot can share one
// renderer-side shape.
//
// PRD §I-FR-I3: the predict surface for Compose overlays is
// `OverlaysApplied + OverlaysSchemaDivergence + OverlayCost`. The
// divergence slot carries the per-spec failure reason alongside the
// envelope error entry so LLM planners can budget reshapes without
// stitching envelope details together.
//
// Reason values are stable identifiers the renderer can branch on:
//
//   - "kind-unknown"          — spec.Kind absent from the overlay catalog.
//   - "reference-unknown"     — Reference label does not resolve to a slot.
//   - "target-unknown"        — Targets[j] does not resolve to a slot.
//   - "slot-shape-divergent"  — ref and target host shapes differ.
//   - "slot-not-crosstab"     — kind requires MATRIX but a slot isn't.
//   - "schema-divergent"      — per-axis grouper-kind tuples disagree.
//   - "panel-targets-over-cap" — multi-ref target count exceeds the cap.
type SlotPair struct {
	// ReferenceLabel is the slot label the spec's Reference field
	// resolves to. May be empty when the failure is the reference
	// itself being unknown / unresolvable.
	ReferenceLabel string `json:"reference_label"`

	// TargetLabel is the slot label the spec's Targets[j] resolves
	// to. May be empty when the failure is on the Reference arm.
	TargetLabel string `json:"target_label"`

	// Reason is a stable, machine-readable identifier for the failure
	// class. See the SlotPair doc for the enum.
	Reason string `json:"reason"`
}

// Suggestion is a structured next-action attached to PredictResult.
// Predict computes suggestions inline so callers can repair a request
// without an additional inspect round-trip.
//
// Path points at the offending request location using JSON-style
// segments — e.g. ["Aggregations", "0", "Field"] addresses the Field
// of the first aggregation.
//
// Proposed is a ranked list of candidate values. Empty when no
// concrete proposal applies (e.g. ATTR_PERCENTILE has no streamable
// peer); the caller should treat empty Proposed as advisory.
//
// Confidence is a static heuristic in [0, 1]: 0.9 for high-certainty
// single-candidate swaps and Levenshtein distance 1; 0.7 for distance
// 2; 0.6 for multi-candidate type-class swaps; 0.5 for missing-param
// fallbacks that hand the user a list to pick from; 0.8 for
// streamability substitutes.
type Suggestion struct {
	Path       []string `json:"path"`
	Reason     string   `json:"reason"`
	Current    any      `json:"current,omitempty"`
	Proposed   []any    `json:"proposed,omitempty"`
	Confidence float64  `json:"confidence"`
}

// PredictSchemaInfo summarizes the schema used for prediction.
type PredictSchemaInfo struct {
	FieldCount int      `json:"field_count"`
	Fields     []string `json:"fields"`
}

// DefaultApplied describes a single defaulted operator slot. Returned by
// ResolveDefaults so callers (predict, service, MCP transcripts) can echo
// exactly which slots had their Type inferred from the schema. The shape
// is JSON-serializable and lands on PredictResult.DefaultsApplied.
//
// Path uses JSON-style segments e.g. ["Aggregations", "0", "Type"] so the
// caller can address the slot in the original request. Field names the
// schema column that drove inference; Type is the operator string that
// was filled in; Category is "aggregation" or "grouper"; Reason carries a
// short human-readable rule trace, e.g. "f64 → AGG_SUM (rule: numeric
// default)".
type DefaultApplied struct {
	Path     []string `json:"path"`
	Field    string   `json:"field"`
	Type     string   `json:"type"`
	Category string   `json:"category"`
	Reason   string   `json:"reason"`
}

// LimitGrade is how sure a LimitFinding is that the run would breach
// the limit.
type LimitGrade string

const (
	// LimitGradeCertain: the estimate is the exact figure the runtime
	// sees; the request is refused (Valid false, PULSE_LIMIT_EXCEEDED).
	LimitGradeCertain LimitGrade = "certain"
	// LimitGradePossible: the estimate is an upper bound; the request
	// may still run.
	LimitGradePossible LimitGrade = "possible"
)

// LimitFinding is one instance resource limit a request's predicted
// figure exceeds. Limit is the snake_case limit name (the `limit` detail
// of PULSE_LIMIT_EXCEEDED), Configured the instance's effective value
// and Estimated predict's figure in the same unit.
type LimitFinding struct {
	Limit      string     `json:"limit"`
	Configured int64      `json:"configured"`
	Estimated  int64      `json:"estimated"`
	Grade      LimitGrade `json:"grade"`
}
