package descriptor

import (
	"bytes"
	"io"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// numericAggregations are aggregation types that only make sense on numeric fields.
var numericAggregations = map[types.AggregationType]bool{
	types.AGG_SUM:        true,
	types.AGG_AVERAGE:    true,
	types.AGG_MIN:        true,
	types.AGG_MAX:        true,
	types.AGG_STDDEV:     true,
	types.AGG_RANGE:      true,
	types.AGG_ZSCORE:     true,
	types.AGG_MEDIAN:     true,
	types.AGG_VARIANCE:   true,
	types.AGG_SKEWNESS:   true,
	types.AGG_KURTOSIS:   true,
	types.AGG_PERCENTILE: true,
}

// CategoricalAggregationIssues returns one EnvelopeEntry per
// (Aggregation slot referencing a categorical field, aggregator from
// numericAggregations) pair found in req. Each entry carries the
// PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL code, a human-readable
// message, and the {field, aggregation} details map. Strict-mode
// promotion is the caller's responsibility — the helper itself is
// non-judgmental and returns nil when the request, schema, or
// Aggregations slice is empty / nil.
//
// Used by:
//   - validateRequestFields (predict path) — emits to env.Warnings /
//     env.Errors based on PredictOptions.Strict.
//   - service.Process — wraps the first entry as a coded error when
//     Service is configured strict; non-strict emission flows through
//     the envelope at the CLI boundary.
func CategoricalAggregationIssues(req *types.Request, schema *encoding.Schema) []*EnvelopeEntry {
	if req == nil || schema == nil || len(req.Aggregations) == 0 {
		return nil
	}
	var out []*EnvelopeEntry
	for _, agg := range req.Aggregations {
		f := schema.Field(agg.Field)
		if f == nil {
			continue
		}
		if !f.Type.IsCategorical() || !numericAggregations[agg.Type] {
			continue
		}
		out = append(out, &EnvelopeEntry{
			Code:    string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
			Message: "numeric aggregation " + string(agg.Type) + " is not meaningful for categorical field " + agg.Field,
			Details: map[string]any{"field": agg.Field, "aggregation": string(agg.Type)},
		})
	}
	return out
}

// decimalSupportedAggregations are the v1 set of aggregations defined on
// decimal128 fields. Any aggregation outside this set on a decimal field
// emits PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL.
var decimalSupportedAggregations = map[types.AggregationType]bool{
	types.AGG_SUM:            true,
	types.AGG_AVERAGE:        true,
	types.AGG_MIN:            true,
	types.AGG_MAX:            true,
	types.AGG_VARIANCE:       true,
	types.AGG_STDDEV:         true,
	types.AGG_COUNT:          true,
	types.AGG_DISTINCT_COUNT: true,
}

// PredictOptions controls predict behavior.
type PredictOptions struct {
	// Strict upgrades warnings to errors.
	Strict bool

	// Extensions is the read-only snapshot of embedder-registered
	// operators + expression-side state. Nil takes the built-in-only
	// path. The snapshot adds every custom operator name to the
	// validator's known-types set so predict does not flag
	// embedder-registered ops as unknown, and feeds streamability
	// overrides into computeStreamable.
	Extensions *ExtensionsSnapshot

	// EchoRequest causes Predict to populate envelope.Request with the
	// normalized (post-defaults) request. PredictResult.Request stays
	// the raw input request — the envelope field is the new uniform
	// surface across all envelope-producing endpoints. Off by default.
	EchoRequest bool
}

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
	// the full overlay catalog (E2 ships 10 kinds; future epics
	// append entries to types.AllOverlayKinds() and inherit the same
	// per-spec emission without re-opening this slot).
	// OverlaysSchemaDivergence stays empty until Compose-driven
	// divergence detection lands (E7-S14); OverlayCost is a flat 1.0
	// per kind until per-kind heuristics land (E10-S3).
	OverlaysApplied []OverlayAppliedDescriptor `json:"overlays_applied"`

	// OverlaysSchemaDivergence lists every (left, right) overlay-spec
	// slot pair that produced incompatible result-schema shapes. Empty
	// in E1 — divergence detection lands with Compose wiring in a later
	// epic — but the field is present so consumers can rely on the
	// shape today. Per PRD §I-FR-I3 the placeholder is honoured as
	// "this slot is reserved and shipped empty".
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

	// Streamable mirrors types.OverlayStreamable(kind). False for
	// every E2 kind today (the whole crosstab catalog is buffered);
	// later epics flip kinds to streamable kind-by-kind and the field
	// surfaces the per-kind decision uniformly.
	Streamable bool `json:"streamable"`
}

// SlotPair is a generic left-right slot reference placeholder used by
// PredictResult.OverlaysSchemaDivergence. The shape is intentionally
// minimal — two string-typed slot references, deferring the richer
// (path, kind, message) detail to E7-S14 when Compose-driven
// divergence lands. Empty in E1.
type SlotPair struct {
	// Left names the first overlay-spec slot in the divergent pair
	// (e.g. "overlays[0]").
	Left string `json:"left"`

	// Right names the second overlay-spec slot in the divergent pair
	// (e.g. "overlays[1]").
	Right string `json:"right"`
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

// Predict validates a request against a .pulse file without executing it.
// It reads only the header and schema, never record data.
// The returned Envelope contains the PredictResult in Data and any
// errors/warnings encountered.
func Predict(fileData io.ReadSeeker, req *types.Request, opts *PredictOptions) *Envelope {
	if opts == nil {
		opts = &PredictOptions{}
	}

	result := &PredictResult{
		Valid:                    true,
		Request:                  req,
		Shards:                   []ShardInfo{},
		DefaultsApplied:          []DefaultApplied{},
		OverlaysApplied:          []OverlayAppliedDescriptor{},
		OverlaysSchemaDivergence: []SlotPair{},
		OverlayCost:              map[string]float64{},
	}
	env := NewEnvelope(result)

	// Read header only.
	if err := encoding.ReadHeader(fileData); err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse file header: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	// Read schema (still header, no record data).
	schema, err := encoding.ReadSchema(fileData)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse schema: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	result.SchemaInfo = &PredictSchemaInfo{
		FieldCount: len(schema.Fields),
	}
	for _, f := range schema.Fields {
		result.SchemaInfo.Fields = append(result.SchemaInfo.Fields, f.Name)
	}

	// Compute defaults on a clone so the echoed Request is untouched. The
	// rest of validation runs against the resolved clone so a slot that
	// only got its Type from the default rules table isn't flagged as
	// missing a Type by downstream validators.
	resolved := cloneRequestForDefaults(req)
	if applied := ResolveDefaults(resolved, schema); len(applied) > 0 {
		result.DefaultsApplied = applied
	}
	req = resolved

	// EchoRequest publishes the normalized clone on the envelope. The
	// clone is the same struct downstream validators see, so what the
	// caller gets on env.Request is exactly what an engine run would
	// execute. PredictResult.Request remains the raw input (back-compat).
	if opts.EchoRequest {
		env.Request = resolved
	}

	// Validate pre-filter feature operators and compute the post-feature
	// column set so downstream stages can reference derived columns.
	projected := validateFeatures(env, req, schema, opts)

	// Project attribute output labels into the column set too. Attributes
	// inject labels mid-pipeline (after features, before grouping); without
	// this projection, aggregations and sort keys that reference attribute
	// labels would falsely trip the unknown-field check.
	projectAttributeOutputs(req, projected)

	// Validate request fields exist in schema (or in feature outputs).
	validateRequestFields(env, req, schema, projected, opts)

	// Validate window operations (structural checks; no execution).
	validateWindows(env, req, schema, opts)

	// Validate tier-1 and tier-2 statistical tests against the schema +
	// projected column set. Tier-1 catches missing fields and type
	// mismatches; tier-2 catches alpha and unknown-type errors. Field
	// existence for tier-2 columns is deferred to runtime since the
	// projected post-pipeline column set is not yet a settled contract.
	validateTests(env, req, schema, projected, opts)

	// Validate response-level sort keys against the projected output columns.
	validateSort(env, req, schema)

	// Validate the Crosstab section (structural + axis field references +
	// normalization gates). Streamability reasons land via
	// computeStreamable below.
	validateCrosstab(env, req, schema, opts)

	// Validate overlay specs (Request.Overlays). E1 covers OVERLAY_INDEX_VS_MARGIN
	// only — unknown kind, ref-shape compatibility against the MATRIX host,
	// and the per-kind supported scope set. Streamability is gated on the
	// host operator (today: always-buffered crosstab); overlay streamability
	// surfaces via types.OverlayStreamable in later epics.
	ValidateOverlays(env, req, schema, opts)

	// Populate the predict surface from req.Overlays. One descriptor per
	// spec in matching order; OverlayCost is a per-kind multiplier keyed
	// by the spec's renderer-facing name (or a synthesised default when
	// empty) — streamable kinds carry overlayCostStreamable, buffered
	// kinds carry overlayCostBuffered. OverlaysSchemaDivergence stays
	// empty in E1 — Compose wiring lands later.
	populateOverlayDescriptors(result, req)

	// Validate label bindings (display-time categorical translation). Snapshot
	// carries the registered label tables; augment-mode collisions are
	// checked against the projected output column set so a sibling
	// "<field>_label" cannot shadow an aggregation/attribute label.
	ValidateLabels(env, req.Labels, schema, extensionsFromOpts(opts), projected)

	// Check description quality.
	validateDescriptionQuality(env, schema, opts)

	// Compute streamability — per-type Streamable() methods plus schema-aware
	// gates (decimal fields force buffered). Honours
	// the extensions snapshot when present so custom operator overrides land.
	result.Streamable, result.StreamableReasons = computeStreamable(req, schema, opts)

	// Compute autocomplete-style suggestions. Suggestions may surface even
	// when the request is otherwise valid (streamability hints), so this
	// runs unconditionally after every other validator.
	result.Suggestions = computeSuggestions(req, schema, result.Streamable)

	// If any errors were added, mark invalid.
	if len(env.Errors) > 0 {
		result.Valid = false
	}

	return env
}

// computeStreamable reports whether the request can execute via the
// streaming Process path. Mirrors processing.canStream's gates but reads
// from the types.* Streamable() methods instead of constructing operators
// (predict cannot import processing).
//
// Returns (true, nil) when streamable; (false, reasons) listing every
// gate that blocks streaming. The reasons slice is intentionally
// human-readable so it can land in the envelope unchanged.
func computeStreamable(req *types.Request, schema *encoding.Schema, opts *PredictOptions) (bool, []string) {
	var reasons []string

	// Regression streamability defers to RegressionSpec.Streamable():
	// it folds the type-level family check, the modifier downgrade
	// (Resample / Selection force buffered), and the Phase-by-Phase
	// implementation status gate (today only unpenalized REG_OLS streams).
	// Phases 2-5 widen the gate as their engines land — predict and the
	// processor share the same source of truth via this method.
	for _, reg := range req.Regressions {
		if reg == nil {
			continue
		}
		if !reg.Streamable() {
			reasons = append(reasons, "regression "+string(reg.Type)+" requires the buffered path under the current spec")
		}
	}
	// Regression slots do not yet compose with groupers, features,
	// two-pass attributes, or tier-1 row tests in the streaming path.
	// Mirror the runtime gate so predict stays parity-true.
	if len(req.Regressions) > 0 {
		if len(req.Groups) > 0 {
			reasons = append(reasons, "regression with groupers runs via the buffered path")
		}
		if len(req.Features) > 0 {
			reasons = append(reasons, "regression with features runs via the buffered path")
		}
		if len(req.Tests) > 0 {
			reasons = append(reasons, "regression with tier-1 tests runs via the buffered path")
		}
		for _, attr := range req.Attributes {
			if attr.Type == types.ATTR_ZSCORE || attr.Type == types.ATTR_TSCORE || attr.Type == types.ATTR_NORMALIZED {
				reasons = append(reasons, "regression with two-pass attribute "+string(attr.Type)+" runs via the buffered path")
				break
			}
		}
	}

	// Crosstab dispatch overrides the regular streamable gate: when set,
	// crosstabStreamableReasons is the authoritative list (matrix shape,
	// margins, normalization, or nested axes all force buffered).
	if req.Crosstab != nil {
		reasons = append(reasons, crosstabStreamableReasons(req.Crosstab)...)
		// Crosstab does not require the standard "at least one
		// aggregation" gate — the cell aggregation lives on
		// req.Crosstab.Cell, not req.Aggregations.
		return len(reasons) == 0, reasons
	}

	if len(req.Aggregations) == 0 {
		reasons = append(reasons, "no aggregations: streaming path requires at least one OnlineAggregator")
	}
	for _, grp := range req.Groups {
		if !streamableWithOverlay(opts, "grouper", string(grp.Type), grp.Type.Streamable()) {
			reasons = append(reasons, "group "+string(grp.Type)+" requires the buffered path")
		}
	}
	for _, attr := range req.Attributes {
		if !streamableWithOverlay(opts, "attribute", string(attr.Type), attr.Type.Streamable()) {
			reasons = append(reasons, "attribute "+string(attr.Type)+" requires a full pass for population stats")
		}
	}
	if len(req.Windows) > 0 {
		reasons = append(reasons, "windows run over the post-aggregate row set")
	}

	for _, agg := range req.Aggregations {
		if !streamableWithOverlay(opts, "aggregator", string(agg.Type), agg.Type.Streamable()) {
			reasons = append(reasons, "aggregation "+string(agg.Type)+" is not streamable")
			continue
		}
		// Decimal field aggregation routes through AggregateDecimalField to
		// preserve precision; the streaming numeric fold loses it.
		if schema != nil {
			if f := schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				reasons = append(reasons, "aggregation on decimal field "+agg.Field+" forces buffered path")
			}
		}
	}

	for _, feat := range req.Features {
		if !streamableWithOverlay(opts, "feature", string(feat.Type), feat.Type.Streamable()) {
			reasons = append(reasons, "feature "+string(feat.Type)+" is not streamable")
		}
	}

	reasons = append(reasons, streamableTestReasons(req)...)

	return len(reasons) == 0, reasons
}

// PredictFromBytes routes data to either the single-file predict path
// or the archive predict path based on the first four bytes. Archive
// detection is by the zip magic PK\x03\x04; single-file (or any other
// prefix) falls through to Predict, which surfaces the standard
// ENCODING_INVALID envelope on malformed input.
//
// For archive-backed cohorts: the canonical schema is read from the
// reserved _schema.pulse entry and used for every validation step
// (field existence, type compatibility, streamability). The cumulative
// record total and per-shard list are added to the PredictResult; the
// request is validated against the canonical schema and inherits the
// same streamability gates that a single-file cohort with the same
// schema would produce.
func PredictFromBytes(data []byte, req *types.Request, opts *PredictOptions) *Envelope {
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04 {
		return predictArchive(data, req, opts)
	}
	return Predict(bytes.NewReader(data), req, opts)
}

// predictArchive runs the predict pipeline against a Pulse shard
// archive. The canonical _schema.pulse entry supplies the schema the
// request validates against; per-shard headers contribute the
// cumulative RecordCount and Shards listing. Streamability is computed
// from the same gates the single-file path uses — a shard archive
// inherits the streamability of its request against the canonical
// schema, not of its shape (see PredictResult.Streamable docs).
func predictArchive(data []byte, req *types.Request, opts *PredictOptions) *Envelope {
	reader := bytes.NewReader(data)
	arch, err := encoding.OpenArchive(reader, int64(len(data)))
	if err != nil {
		result := &PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []ShardInfo{},
			DefaultsApplied:          []DefaultApplied{},
			Suggestions:              []Suggestion{},
			OverlaysApplied:          []OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []SlotPair{},
			OverlayCost:              map[string]float64{},
		}
		env := NewEnvelope(result)
		env.AddError(string(errors.PULSE_ARCHIVE_CORRUPT), "invalid pulse shard archive: "+err.Error(), nil)
		return env
	}

	rc, oerr := arch.Open(encoding.ReservedSchemaName)
	if oerr != nil {
		result := &PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []ShardInfo{},
			DefaultsApplied:          []DefaultApplied{},
			Suggestions:              []Suggestion{},
			OverlaysApplied:          []OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []SlotPair{},
			OverlayCost:              map[string]float64{},
		}
		env := NewEnvelope(result)
		env.AddError(string(errors.PULSE_SHARD_MISSING),
			"archive missing reserved schema entry "+encoding.ReservedSchemaName+": "+oerr.Error(), nil)
		return env
	}

	// Materialize the canonical schema-doc payload so we can feed
	// Predict (which expects a ReadSeeker over header + schema). We
	// only need the leading header + schema block; the trailing SHRD
	// extension on _schema.pulse is harmless to Predict because
	// Predict only reads ReadHeader + ReadSchema, then validates the
	// request against schema-derived state.
	canonical, rerr := io.ReadAll(rc)
	_ = rc.Close()
	if rerr != nil {
		result := &PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []ShardInfo{},
			DefaultsApplied:          []DefaultApplied{},
			Suggestions:              []Suggestion{},
			OverlaysApplied:          []OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []SlotPair{},
			OverlayCost:              map[string]float64{},
		}
		env := NewEnvelope(result)
		env.AddError(string(errors.ENCODING_INVALID),
			"reading canonical schema entry "+encoding.ReservedSchemaName+": "+rerr.Error(), nil)
		return env
	}

	env := Predict(bytes.NewReader(canonical), req, opts)
	result, _ := env.Data.(*PredictResult)
	if result == nil {
		return env
	}

	// Enumerate non-reserved entries in central-directory order and
	// sum per-shard record counts via PeekShardRecordCount.
	var cumulative int64
	shards := make([]ShardInfo, 0)
	for _, entry := range arch.Entries() {
		if entry.Name == encoding.ReservedSchemaName {
			continue
		}
		count, perr := arch.PeekShardRecordCount(entry.Name)
		if perr != nil {
			env.AddError(string(errors.PULSE_SHARD_HEADER_INVALID),
				"peeking record count for shard "+entry.Name+": "+perr.Error(),
				map[string]any{"entry": entry.Name})
			result.Valid = false
			continue
		}
		shards = append(shards, ShardInfo{
			Filename:    entry.Name,
			RecordCount: count,
		})
		cumulative += count
	}
	result.Shards = shards
	result.RecordCount = cumulative
	return env
}

// validateRequestFields checks that all referenced fields exist and that
// numeric aggregations on categorical fields produce warnings. The
// projected column set augments the schema with feature output names so
// downstream stages can address derived columns.
func validateRequestFields(env *Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, opts *PredictOptions) {
	// Numeric aggregation on categorical field — single helper, strict
	// promotion happens here so service.Process can share the helper
	// without owning predict's strict semantics.
	for _, entry := range CategoricalAggregationIssues(req, schema) {
		if opts.Strict {
			env.Errors = append(env.Errors, entry)
		} else {
			env.Warnings = append(env.Warnings, entry)
		}
	}

	// Check aggregation fields.
	for _, agg := range req.Aggregations {
		f := schema.Field(agg.Field)
		if f == nil {
			if projected[agg.Field] {
				continue // derived column from feature stage
			}
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"aggregation references unknown field: "+agg.Field,
				map[string]any{"field": agg.Field, "aggregation": string(agg.Type)},
			)
			continue
		}

		// Decimal field aggregation validity matrix.
		if f.Type.IsDecimal() && !decimalSupportedAggregations[agg.Type] {
			entry := &EnvelopeEntry{
				Code:    string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL),
				Message: "aggregation " + string(agg.Type) + " has no decimal128 implementation; field " + agg.Field + " is decimal128",
				Details: map[string]any{"field": agg.Field, "aggregation": string(agg.Type)},
			}
			if opts.Strict {
				env.Errors = append(env.Errors, entry)
			} else {
				env.Warnings = append(env.Warnings, entry)
			}
		}

	}

	// Check filter fields.
	for _, fil := range req.Filterers {
		if fil.Field == "" && fil.Type == types.FILTER_EXPRESSION {
			continue // expression filters don't require a field
		}
		if fil.Field != "" {
			f := schema.Field(fil.Field)
			if f == nil && !projected[fil.Field] {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"filter references unknown field: "+fil.Field,
					map[string]any{"field": fil.Field, "filter": string(fil.Type)},
				)
				continue
			}
			_ = f
		}
	}

	// Check group fields.
	for _, grp := range req.Groups {
		f := schema.Field(grp.Field)
		if f == nil && !projected[grp.Field] {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"group references unknown field: "+grp.Field,
				map[string]any{"field": grp.Field, "group": string(grp.Type)},
			)
			continue
		}
		_ = f
	}

	// Check regression slots. Phase 0 validates structural shape only
	// (known type, target + predictors named, fields exist); deeper
	// runtime checks (n ≥ p + 1, family/link compatibility) land with
	// the engines in Phases 1–4.
	validateRegressions(env, req, schema, projected)

	// Check attribute fields.
	for _, attr := range req.Attributes {
		// Removed-type sentinel: ATTR_RANK was retired in favor of WIN_RANK.
		// Surface a migration hint instead of the generic registry-miss error.
		if attr.Type == "ATTR_RANK" {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"ATTR_RANK was removed in this release; use WIN_RANK with empty partition_by and a single ASC order_by on the same field",
				map[string]any{"attribute": "ATTR_RANK", "replacement": "WIN_RANK"},
			)
			continue
		}
		// Regression attributes (ATTR_REG_FITTED / RESIDUAL / LEVERAGE) carry
		// their own Target + Predictors instead of a single Field. Validate
		// those references here; the factory rechecks numeric-type
		// compatibility at construction time.
		if attr.Type == types.ATTR_REG_FITTED || attr.Type == types.ATTR_REG_RESIDUAL || attr.Type == types.ATTR_REG_LEVERAGE {
			if attr.Target == "" {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					string(attr.Type)+" requires Target",
					map[string]any{"attribute": string(attr.Type)},
				)
			} else if schema.Field(attr.Target) == nil && !projected[attr.Target] {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					string(attr.Type)+" Target references unknown field: "+attr.Target,
					map[string]any{"field": attr.Target, "attribute": string(attr.Type)},
				)
			}
			if len(attr.Predictors) == 0 {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					string(attr.Type)+" requires at least one predictor",
					map[string]any{"attribute": string(attr.Type)},
				)
			}
			for _, name := range attr.Predictors {
				if schema.Field(name) == nil && !projected[name] {
					env.AddError(
						string(errors.SERVICE_VALIDATION),
						string(attr.Type)+" predictor references unknown field: "+name,
						map[string]any{"field": name, "attribute": string(attr.Type)},
					)
				}
			}
			continue
		}
		f := schema.Field(attr.Field)
		if f == nil && !projected[attr.Field] {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"attribute references unknown field: "+attr.Field,
				map[string]any{"field": attr.Field, "attribute": string(attr.Type)},
			)
		}
	}
}

// projectAttributeOutputs adds each attribute's output label to the
// projected column set. The label rule mirrors processor.go's
// applyAttributes / defaultAttributeLabel: an explicit label wins;
// otherwise the default is "<TYPE>_<field>", with the regression
// attributes (ATTR_REG_FITTED / RESIDUAL / LEVERAGE) substituting
// Target for Field since they do not carry a single source field.
//
// Duplicated locally rather than imported from processing/ because
// descriptor must not import the processing package (predict is a
// no-execute path).
func projectAttributeOutputs(req *types.Request, projected map[string]bool) {
	for _, attr := range req.Attributes {
		label := attr.Label
		if label == "" {
			label = attributeDefaultLabel(attr)
		}
		projected[label] = true
	}
}

// attributeDefaultLabel mirrors processing.defaultAttributeLabel so
// predict produces the same projected column names process would emit.
// The duplication is intentional — descriptor/predict.go must not
// import processing/.
func attributeDefaultLabel(attr *types.Attribute) string {
	switch attr.Type {
	case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
		return string(attr.Type) + "_" + attr.Target
	}
	return string(attr.Type) + "_" + attr.Field
}

// validateDescriptionQuality emits warnings for fields with low-quality descriptions.
func validateDescriptionQuality(env *Envelope, schema *encoding.Schema, opts *PredictOptions) {
	for _, f := range schema.Fields {
		if isLowQualityDescription(f.Description) {
			entry := &EnvelopeEntry{
				Code:    string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY),
				Message: "field " + f.Name + " has a low-quality description",
				Details: map[string]any{"field": f.Name, "description": f.Description},
			}
			if opts.Strict {
				env.Errors = append(env.Errors, entry)
			} else {
				env.Warnings = append(env.Warnings, entry)
			}
		}
	}
}

// isLowQualityDescription checks if a description is empty or too short/generic.
func isLowQualityDescription(desc string) bool {
	if desc == "" {
		return true
	}
	trimmed := strings.TrimSpace(desc)
	if len(trimmed) < 10 {
		return true
	}
	// Check for obviously unhelpful descriptions.
	lower := strings.ToLower(trimmed)
	unhelpful := []string{"n/a", "na", "none", "tbd", "todo", "unknown", "field", "data", "value", "column"}
	return slices.Contains(unhelpful, lower)
}

// overlayCostStreamable is the OverlayCost score assigned to overlay
// kinds whose streamability flag is true — the kind folds inside the
// existing streaming Process pass via a kind-specific accumulator
// carried alongside the per-group reducers, so the marginal cost is
// effectively a few f64 adds per record. The value is a rough record-
// count multiplier per PRD §I-FR-I3; 0.05 ≈ "5% extra work" relative
// to a fresh pass over the source records. The streamable E3 trio
// (INDEX_VS_TOTAL, ZSCORE_VS_TOTAL, the SERIES dispatch of
// SHARE_OF_TOTAL) lands at this value today.
const overlayCostStreamable = 0.05

// overlayCostBuffered is the OverlayCost score assigned to overlay
// kinds whose streamability flag is false — the kind requires the
// fully materialised host payload (the buffered crosstab matrix, the
// finalized per-group SeriesPayload for the sibling family, etc.) and
// folds at the post-host exit, traversing the payload a second time.
// The value is a rough record-count multiplier per PRD §I-FR-I3; 1.0
// ≈ "one extra pass" over the materialised host structure. Every E1 /
// E2 kind plus the SIBLING family (DELTA_VS_SIBLING, INDEX_VS_SIBLING)
// lands at this value today.
const overlayCostBuffered = 1.0

// overlayCostForKind returns the OverlayCost multiplier for a given
// overlay kind. The dispatch reads types.OverlayStreamable(kind) —
// streamable kinds get overlayCostStreamable, buffered kinds get
// overlayCostBuffered. The static streamability table in
// types/overlay_streamability.go is the single source of truth, so a
// kind that flips streamable automatically flips its cost here.
// Unknown kinds (absent from the table) fall through to the buffered
// score — the validator already surfaces PULSE_OVERLAY_KIND_UNKNOWN
// for the same condition; the cost score stays maximally conservative.
func overlayCostForKind(kind types.OverlayKind) float64 {
	streamable, known := types.OverlayStreamable(kind)
	if !known || !streamable {
		return overlayCostBuffered
	}
	return overlayCostStreamable
}

// populateOverlayDescriptors fills PredictResult.OverlaysApplied and
// PredictResult.OverlayCost from req.Overlays. Per kind-catalog-v1 PRD
// §I-FR-I3 the predict surface is `OverlaysApplied +
// OverlaysSchemaDivergence + OverlayCost`; E1 emits one
// OverlayAppliedDescriptor per spec (Name + Kind + Scope + Streamable)
// and a per-kind OverlayCost multiplier keyed by the spec name. The
// divergence slot stays empty in E1.
//
// The streamability echo and the cost score both consult
// types.OverlayStreamable(kind) — the static table in
// types/overlay_streamability.go is the single source of truth, so a
// kind that flips streamable automatically flips here.
func populateOverlayDescriptors(result *PredictResult, req *types.Request) {
	if result == nil || req == nil || len(req.Overlays) == 0 {
		return
	}
	result.OverlaysApplied, result.OverlayCost = appendOverlayDescriptors(
		result.OverlaysApplied, result.OverlayCost, req.Overlays,
	)
}

// appendOverlayDescriptors is the shared per-spec emitter used by both
// the Request-host predict surface (PredictResult) and the FACET-host
// predict surface (FacetValidationResult). Each spec produces one
// OverlayAppliedDescriptor entry plus one entry in the cost map keyed by
// the spec's renderer-facing name (or a synthesised default when empty).
// Streamability + cost both consult types.OverlayStreamable(kind) so the
// static streamability table stays the single source of truth.
//
// Returns the (possibly grown) descriptor slice and cost map. The cost
// map is mutated in place when non-nil; callers must seed an empty map
// on the destination struct before invoking this helper so the JSON
// output stays empty-but-not-nil (matching the PredictResult contract).
func appendOverlayDescriptors(
	descriptors []OverlayAppliedDescriptor,
	costs map[string]float64,
	specs []types.OverlaySpec,
) ([]OverlayAppliedDescriptor, map[string]float64) {
	for i := range specs {
		spec := &specs[i]
		name := overlayDescriptorName(spec)
		streamable, _ := types.OverlayStreamable(spec.Kind)
		descriptors = append(descriptors, OverlayAppliedDescriptor{
			Name:       name,
			Kind:       spec.Kind,
			Scope:      spec.Scope,
			Streamable: streamable,
		})
		// Per-kind cost multiplier — streamable kinds carry
		// overlayCostStreamable (~5% extra work) because they fold
		// inside the streaming Process pass; buffered kinds carry
		// overlayCostBuffered (~one extra payload traversal) because
		// they re-walk the materialised host structure at the post-
		// host exit.
		if costs != nil {
			costs[name] = overlayCostForKind(spec.Kind)
		}
	}
	return descriptors, costs
}

// populateFacetOverlayDescriptors fills FacetValidationResult.OverlaysApplied
// and FacetValidationResult.OverlayCost from req.Overlays. Sibling to
// populateOverlayDescriptors — the FACET-host predict surface mirrors the
// Request-host predict surface (kind-catalog-v1 PRD §I-FR-I3) so LLM
// callers see the same per-spec descriptor shape (Name + Kind + Scope +
// Streamable) and the same per-kind cost map regardless of host shape.
//
// Per-kind cost dispatch: all four FACET-host kinds route through the
// same overlayCostForKind helper. The streamability table at
// types/overlay_streamability.go declares OVERLAY_INDEX_VS_POP and
// OVERLAY_ZSCORE_VS_POP as streamable (cost ~0.05) and OVERLAY_CHISQ_VS_POP
// and OVERLAY_KS_VS_POP as buffered (cost ~1.0) per PRD §2 Non-Goals
// ("Streaming overlay path for inferential kinds"). Cost flips
// automatically when a kind's streamability flag flips.
func populateFacetOverlayDescriptors(result *FacetValidationResult, req *types.FacetRequest) {
	if result == nil || req == nil || len(req.Overlays) == 0 {
		return
	}
	result.OverlaysApplied, result.OverlayCost = appendOverlayDescriptors(
		result.OverlaysApplied, result.OverlayCost, req.Overlays,
	)
}

// overlayDescriptorName returns the renderer-facing label for an
// overlay spec. When the caller set OverlaySpec.Name explicitly that
// string wins; otherwise we synthesise a deterministic default from
// Kind + Scope + the populated Ref family pointer so the cost map and
// descriptor stay keyed consistently.
//
// The processing layer synthesises its own default at execution time
// (see types/overlay.go's OverlaySpec doc). Predict's synthesis must
// stay aligned with that runtime default for the cost-map key to
// match the response-side OverlayLayer.Name. Until the processing
// helper is exported the synthesis here mirrors the documented
// "Kind|Scope|Ref" recipe.
func overlayDescriptorName(spec *types.OverlaySpec) string {
	if spec == nil {
		return ""
	}
	if spec.Name != "" {
		return spec.Name
	}
	name := string(spec.Kind) + "|" + string(spec.Scope)
	if spec.Ref.Margin != nil {
		name += "|margin:" + string(spec.Ref.Margin.Axis)
	}
	return name
}
