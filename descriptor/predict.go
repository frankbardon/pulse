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
