package descriptor

import (
	"bytes"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// numericAggregations are aggregation types that only make sense on numeric fields.
var numericAggregations = map[types.AggregationType]bool{
	types.AGG_SUM: true,
	// AGG_DISTINCT_SUM sums its own Field once per distinct key; the
	// summed value is numeric even though the key named by
	// params.distinct_by is frequently categorical. Only Field is
	// checked here, so a categorical VALUE column is the meaningless
	// case this flags — the same reason AGG_SUM is listed.
	types.AGG_DISTINCT_SUM: true,
	types.AGG_AVERAGE:      true,
	types.AGG_MIN:          true,
	types.AGG_MAX:          true,
	types.AGG_STDDEV:       true,
	types.AGG_RANGE:        true,
	types.AGG_ZSCORE:       true,
	types.AGG_MEDIAN:       true,
	types.AGG_VARIANCE:     true,
	types.AGG_SKEWNESS:     true,
	types.AGG_KURTOSIS:     true,
	types.AGG_PERCENTILE:   true,
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
func CategoricalAggregationIssues(req *types.Request, schema *encoding.Schema) []*descriptor.EnvelopeEntry {
	if req == nil || schema == nil || len(req.Aggregations) == 0 {
		return nil
	}
	var out []*descriptor.EnvelopeEntry
	for _, agg := range req.Aggregations {
		f := schema.Field(agg.Field)
		if f == nil {
			continue
		}
		if !f.Type.IsCategorical() || !numericAggregations[agg.Type] {
			continue
		}
		out = append(out, &descriptor.EnvelopeEntry{
			Code:    string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
			Message: "numeric aggregation " + string(agg.Type) + " is not meaningful for categorical field " + agg.Field,
			Details: map[string]any{"field": agg.Field, "aggregation": string(agg.Type)},
		})
	}
	return out
}

// decimalSupportedAggregations are the v1 set of BUILT-IN aggregations
// defined on decimal128 fields. Any other built-in on a decimal field
// emits PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL; a registered extension
// aggregator is exempt (see decimalAggregationRefused).
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

// decimalAggregationRefused reports whether agg on a decimal128 field
// draws PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL. A registered extension
// aggregator is never refused: it owns its decimal semantics
// (extend.Record.DecimalValue) and the runtime dispatches it to its own
// factory, mirroring the processing-side exemption without importing
// processing.
func decimalAggregationRefused(agg types.AggregationType, snap *ExtensionsSnapshot) bool {
	return !decimalSupportedAggregations[agg] && !snap.HasAggregator(string(agg))
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

	// DefaultTimeZone is pulse.Options.DefaultTimeZone — the zone a
	// zone-capable slot inherits when neither its own `tz` nor the
	// request's `time_zone` names one. Empty means UTC.
	DefaultTimeZone string

	// ZoneLoader resolves zone names (nil: temporal.LoadZone). The
	// facade passes its per-instance cache so predict and the runtime
	// resolve through the same loader.
	ZoneLoader ZoneLoader

	// DisableDefaults is pulse.Options.DisableDefaults. When set,
	// predict validates the request WITHOUT smart defaults — exactly
	// what the runtime executes — so a slot left with an empty Type is
	// reported the way the runtime reports it. DefaultsApplied is still
	// computed: it then lists the defaults that WOULD apply.
	DisableDefaults bool

	// SchemaLoader reads the header + schema of the cohort at a path
	// (no record data). The facade passes the runtime's own cohort
	// opener. Predict uses it for Request.Joins: the request is
	// validated against the joined schema (internal/encoding.
	// JoinedSchema, the rule the runtime builds its join over), so a
	// right-side field is resolved by its real type. Nil leaves a join
	// unresolved and validation runs against the left schema alone.
	SchemaLoader func(path string) (*encoding.Schema, error)
}

// Predict validates a request against a .pulse file without executing it.
// It reads only the header and schema, never record data.
// The returned Envelope contains the PredictResult in Data and any
// errors/warnings encountered.
//
// Detection is by the first four bytes at the reader's position, as in
// Inspect: zip magic PK\x03\x04 routes to the shard-archive path, where
// the canonical _schema.pulse entry supplies the schema the request
// validates against and per-shard headers contribute the cumulative
// RecordCount and Shards listing (streamability is computed from the
// same gates the single-file path uses — an archive inherits the
// streamability of its request against the canonical schema, not of its
// shape). Any other prefix takes the single-file path, which surfaces
// the standard ENCODING_INVALID envelope on malformed input.
func Predict(fileData io.ReadSeeker, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	if data, ok := sniffArchive(fileData); ok {
		return predictArchive(data, req, opts)
	}
	if opts == nil {
		opts = &PredictOptions{}
	}

	result := &descriptor.PredictResult{
		Valid:                    true,
		Request:                  req,
		Shards:                   []descriptor.ShardInfo{},
		DefaultsApplied:          []descriptor.DefaultApplied{},
		Aggregations:             []descriptor.AggregationPredict{},
		Groups:                   []descriptor.GroupPredict{},
		Filterers:                []descriptor.FiltererPredict{},
		OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
		OverlaysSchemaDivergence: []descriptor.SlotPair{},
		OverlayCost:              map[string]float64{},
		TimeZones:                []descriptor.ResolvedZone{},
	}
	env := descriptor.NewEnvelope(result)

	// Read header only.
	pulseVersion, err := encoding.ReadHeader(fileData)
	if err != nil {
		env.AddError(string(headerErrorCode(err)), "invalid pulse file header: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	// Read schema (still header, no record data).
	schema, err := encoding.ReadSchema(fileData, pulseVersion)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse schema: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	result.SchemaInfo = &descriptor.PredictSchemaInfo{
		FieldCount: len(schema.Fields),
	}
	for _, f := range schema.Fields {
		result.SchemaInfo.Fields = append(result.SchemaInfo.Fields, f.Name)
	}

	// More than one JoinSpec: the runtime refuses before it opens a
	// cohort or applies a default, so predict reports it first.
	if jerr := JoinCountRefusal(req); jerr != nil {
		addCodedError(env, jerr)
	}

	// A join executes over the joined schema; validate against it.
	// SchemaInfo above stays the cohort's own schema.
	cohortSchema := schema
	schema = predictJoinedSchema(env, req, schema, opts)

	// Compute defaults on a clone so the echoed Request is untouched. The
	// rest of validation runs against the resolved clone so a slot that
	// only got its Type from the default rules table isn't flagged as
	// missing a Type by downstream validators. Under DisableDefaults the
	// runtime executes the request as written, so validation does too;
	// DefaultsApplied still reports what the rules table would infer.
	resolved := cloneRequestForDefaults(req)
	if opts.DisableDefaults {
		if applied := ResolveDefaults(cloneRequestForDefaults(req), schema); len(applied) > 0 {
			result.DefaultsApplied = applied
		}
	} else if applied := ResolveDefaults(resolved, schema); len(applied) > 0 {
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

	// Zone resolution — the same single pass the runtime runs before
	// executing (ResolveZones). A refusal is a predict error carrying
	// the runtime's own code and details.
	if zones, zerr := ResolveZones(req, schema, opts.DefaultTimeZone, opts.ZoneLoader); zerr != nil {
		addCodedError(env, zerr)
	} else {
		result.TimeZones = zones
	}

	// Field references — the one rule the runtime refuses with at the
	// same point (after defaults and zones, against the schema the
	// request executes over). Every unknown name is reported.
	for _, ce := range FieldRefRefusals(req, schema, extensionsFromOpts(opts)) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	}

	// A slot still without an operator Type is refused by the runtime's
	// operator construction; report it with the runtime's code and
	// message.
	validateOperatorTypes(env, req)

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

	// Validate overlay specs (Request.Overlays). Surfaces unknown kind,
	// ref-shape compatibility against the host, and the per-kind
	// supported scope set. Streamability is gated on the host operator;
	// overlay streamability surfaces via types.OverlayStreamable.
	ValidateOverlays(env, req, schema, opts)

	// Populate the predict surface from req.Overlays. One descriptor per
	// spec in matching order; OverlayCost is a per-kind multiplier keyed
	// by the spec's renderer-facing name (or a synthesised default when
	// empty) — streamable kinds carry overlayCostStreamable, buffered
	// kinds carry overlayCostBuffered. OverlaysSchemaDivergence stays
	// empty on Predict — Compose wiring exposes divergence on
	// ValidateCompose.
	populateOverlayDescriptors(result, req)

	// Populate the per-aggregation predict surface: one
	// AggregationPredict descriptor per req.Aggregations slot in
	// matching order. ComponentSchema is projected from the static
	// capability table; BufferedComponents flags slots whose
	// Mergeability is None so streaming consumers know the Components
	// block arrives only on terminal flush. Predict stays no-execute
	// — the schema is looked up via aggregatorComponentSchemaIndex,
	// never via constructed operators.
	populateAggregationPredicts(result, req, opts)

	// Populate the per-grouper predict surface: one GroupPredict
	// descriptor per req.Groups slot in matching order.
	// ComponentSchema is projected from the static grouper capability
	// table; BufferedComponents flags slots whose Mergeability is None
	// (GROUP_QUANTILE today). Predict stays no-execute — the schema is
	// looked up via grouperComponentSchemaIndex, never via constructed
	// operators.
	populateGroupPredicts(result, req, opts)

	// Populate the per-filterer predict surface: one FiltererPredict
	// descriptor per req.Filterers slot in matching order.
	// ComponentSchema is projected from the static filterer
	// capability table; BufferedComponents flags slots whose
	// Mergeability is None. In v1 every built-in filterer is Mergeable
	// (counters fold trivially), so BufferedComponents is always false
	// — the field is still surfaced for future per-filter specifics
	// that may downgrade individual entries. Predict stays no-execute
	// — the schema is looked up via filtererComponentSchemaIndex,
	// never via constructed operators.
	populateFiltererPredicts(result, req, opts)

	// Validate label bindings (display-time categorical translation). Snapshot
	// carries the registered label tables; augment-mode collisions are
	// checked against the projected output column set so a sibling
	// "<field>_label" cannot shadow an aggregation/attribute label.
	ValidateLabels(env, req.Labels, schema, extensionsFromOpts(opts), projected)

	// Check description quality (the cohort's own fields only).
	validateDescriptionQuality(env, cohortSchema, opts)

	// Compute streamability — per-type Streamable() methods plus schema-aware
	// gates (decimal fields force buffered). Honours
	// the extensions snapshot when present so custom operator overrides land.
	result.Streamable, result.StreamableReasons = computeStreamable(req, schema, opts)

	// Compute autocomplete-style suggestions. Suggestions may surface even
	// when the request is otherwise valid (streamability hints), so this
	// runs unconditionally after every other validator.
	result.Suggestions = computeSuggestions(req, schema, result.Streamable, opts.Extensions)

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
		if tp := firstTwoPassAttribute(req, opts); tp != "" {
			reasons = append(reasons, "regression with two-pass attribute "+string(tp)+" runs via the buffered path")
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
	// Two-pass attributes do not yet compose with grouped or feature
	// streaming (mirrors processing.canStream's combination gate).
	if tp := firstTwoPassAttribute(req, opts); tp != "" && (len(req.Groups) > 0 || len(req.Features) > 0) {
		reasons = append(reasons, "two-pass attribute "+string(tp)+" with groupers or features runs via the buffered path")
	}
	if len(req.Windows) > 0 {
		reasons = append(reasons, "windows run over the post-aggregate row set")
	}

	for _, agg := range req.Aggregations {
		if !streamableWithOverlay(opts, "aggregator", string(agg.Type), agg.Type.Streamable()) {
			reasons = append(reasons, "aggregation "+string(agg.Type)+" is not streamable")
			continue
		}
		// Built-in decimal field aggregation routes through
		// AggregateDecimalField to preserve precision; the streaming
		// numeric fold loses it. An extension aggregator reads the
		// decimal itself (DecimalValue), so its declared Streamable flag
		// (checked above) decides — mirrors processing.canStream.
		if schema != nil && !extensionsFromOpts(opts).HasAggregator(string(agg.Type)) {
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

	reasons = append(reasons, streamableTestReasons(req, opts)...)

	return len(reasons) == 0, reasons
}

// predictFromBytes is the in-package byte-slice convenience over
// Predict. The public byte-level entry point is the instance method
// (*pulse.Pulse).PredictBytes, which fills PredictOptions from the
// instance.
func predictFromBytes(data []byte, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	return Predict(bytes.NewReader(data), req, opts)
}

// predictArchive runs the predict pipeline against a Pulse shard
// archive. The canonical _schema.pulse entry supplies the schema the
// request validates against; per-shard headers contribute the
// cumulative RecordCount and Shards listing. Streamability is computed
// from the same gates the single-file path uses — a shard archive
// inherits the streamability of its request against the canonical
// schema, not of its shape (see PredictResult.Streamable docs).
func predictArchive(data []byte, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	reader := bytes.NewReader(data)
	arch, err := encx.OpenArchive(reader, int64(len(data)))
	if err != nil {
		result := &descriptor.PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []descriptor.ShardInfo{},
			DefaultsApplied:          []descriptor.DefaultApplied{},
			Aggregations:             []descriptor.AggregationPredict{},
			Groups:                   []descriptor.GroupPredict{},
			Filterers:                []descriptor.FiltererPredict{},
			Suggestions:              []descriptor.Suggestion{},
			OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []descriptor.SlotPair{},
			OverlayCost:              map[string]float64{},
			TimeZones:                []descriptor.ResolvedZone{},
		}
		env := descriptor.NewEnvelope(result)
		env.AddError(string(errors.PULSE_ARCHIVE_CORRUPT), "invalid pulse shard archive: "+err.Error(), nil)
		return env
	}

	rc, oerr := arch.Open(encx.ReservedSchemaName)
	if oerr != nil {
		result := &descriptor.PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []descriptor.ShardInfo{},
			DefaultsApplied:          []descriptor.DefaultApplied{},
			Aggregations:             []descriptor.AggregationPredict{},
			Groups:                   []descriptor.GroupPredict{},
			Filterers:                []descriptor.FiltererPredict{},
			Suggestions:              []descriptor.Suggestion{},
			OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []descriptor.SlotPair{},
			OverlayCost:              map[string]float64{},
			TimeZones:                []descriptor.ResolvedZone{},
		}
		env := descriptor.NewEnvelope(result)
		env.AddError(string(errors.PULSE_SHARD_MISSING),
			"archive missing reserved schema entry "+encx.ReservedSchemaName+": "+oerr.Error(), nil)
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
		result := &descriptor.PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []descriptor.ShardInfo{},
			DefaultsApplied:          []descriptor.DefaultApplied{},
			Aggregations:             []descriptor.AggregationPredict{},
			Groups:                   []descriptor.GroupPredict{},
			Filterers:                []descriptor.FiltererPredict{},
			Suggestions:              []descriptor.Suggestion{},
			OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []descriptor.SlotPair{},
			OverlayCost:              map[string]float64{},
			TimeZones:                []descriptor.ResolvedZone{},
		}
		env := descriptor.NewEnvelope(result)
		env.AddError(string(errors.ENCODING_INVALID),
			"reading canonical schema entry "+encx.ReservedSchemaName+": "+rerr.Error(), nil)
		return env
	}

	env := Predict(bytes.NewReader(canonical), req, opts)
	result, _ := env.Data.(*descriptor.PredictResult)
	if result == nil {
		return env
	}

	// Enumerate non-reserved entries in central-directory order and
	// sum per-shard record counts via PeekShardRecordCount.
	var cumulative int64
	shards := make([]descriptor.ShardInfo, 0)
	for _, entry := range arch.Entries() {
		if entry.Name == encx.ReservedSchemaName {
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
		shards = append(shards, descriptor.ShardInfo{
			Filename:    entry.Name,
			RecordCount: count,
		})
		cumulative += count
	}
	result.Shards = shards
	result.RecordCount = cumulative
	return env
}

// validateRequestFields reports the schema-typed checks on the request's
// slots (numeric aggregations on categorical fields, decimal
// aggregations, regression and attribute shape). Whether a referenced
// name exists at all is FieldRefRefusals' judgement, not this one's.
func validateRequestFields(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, opts *PredictOptions) {
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

	// Unknown names are the field-reference rule's refusals
	// (FieldRefRefusals, reported right after zone resolution); the
	// checks below judge only fields the schema carries.

	// Decimal field aggregation validity matrix.
	for _, agg := range req.Aggregations {
		f := schema.Field(agg.Field)
		if f == nil {
			continue
		}
		if f.Type.IsDecimal() && decimalAggregationRefused(agg.Type, opts.Extensions) {
			entry := &descriptor.EnvelopeEntry{
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
			}
			if len(attr.Predictors) == 0 {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					string(attr.Type)+" requires at least one predictor",
					map[string]any{"attribute": string(attr.Type)},
				)
			}
			continue
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
// Duplicated locally rather than imported from internal/processing/ because
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
// import internal/processing/.
func attributeDefaultLabel(attr *types.Attribute) string {
	switch attr.Type {
	case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
		return string(attr.Type) + "_" + attr.Target
	}
	return string(attr.Type) + "_" + attr.Field
}

// validateDescriptionQuality emits warnings for fields with low-quality descriptions.
func validateDescriptionQuality(env *descriptor.Envelope, schema *encoding.Schema, opts *PredictOptions) {
	for _, f := range schema.Fields {
		if isLowQualityDescription(f.Description) {
			entry := &descriptor.EnvelopeEntry{
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
// to a fresh pass over the source records. The streamable SERIES-host
// trio (INDEX_VS_TOTAL, ZSCORE_VS_TOTAL, the SERIES dispatch of
// SHARE_OF_TOTAL) lands at this value today.
const overlayCostStreamable = 0.05

// overlayCostBuffered is the OverlayCost score assigned to overlay
// kinds whose streamability flag is false — the kind requires the
// fully materialised host payload (the buffered crosstab matrix, the
// finalized per-group SeriesPayload for the sibling family, etc.) and
// folds at the post-host exit, traversing the payload a second time.
// The value is a rough record-count multiplier per PRD §I-FR-I3; 1.0
// ≈ "one extra pass" over the materialised host structure. The
// MATRIX-host catalog plus the SIBLING family (DELTA_VS_SIBLING,
// INDEX_VS_SIBLING) lands at this value today.
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
// OverlaysSchemaDivergence + OverlayCost`. It emits one
// OverlayAppliedDescriptor per spec (Name + Kind + Scope + Streamable)
// and a per-kind OverlayCost multiplier keyed by the spec name. The
// divergence slot stays empty on the Request-only Predict surface.
//
// The streamability echo and the cost score both consult
// types.OverlayStreamable(kind) — the static table in
// types/overlay_streamability.go is the single source of truth, so a
// kind that flips streamable automatically flips here.
func populateOverlayDescriptors(result *descriptor.PredictResult, req *types.Request) {
	if result == nil || req == nil || len(req.Overlays) == 0 {
		return
	}
	result.OverlaysApplied, result.OverlayCost = appendOverlayDescriptors(
		result.OverlaysApplied, result.OverlayCost, req.Overlays,
	)
}

// aggregatorComponentSchemaIndex returns a {operator name → ComponentSchema}
// lookup over the built-in aggregator capability table. The map is the
// authoritative no-execute source predict reads at slot-population time
// — the universal-floor keys are already prepended inside aggSchema, so
// every entry mirrors the same shape the manifest projects under
// Manifest.ComponentsSchemas.Aggregators.
//
// When opts carries an ExtensionsSnapshot, extension aggregator entries
// from snap.ComponentSchemas are merged in afterwards so a slot that
// references an extension operator sees the embedder-declared schema
// instead of the empty default. Built-ins are not overwritten — the
// built-in capability table wins on collision (which should never
// happen because extension names cannot collide with built-ins, per
// validateExtensions).
func aggregatorComponentSchemaIndex(opts *PredictOptions) map[string]descriptor.ComponentSchema {
	caps := aggregatorCapabilities()
	out := make(map[string]descriptor.ComponentSchema, len(caps))
	for _, op := range caps {
		out[op.Name] = op.ComponentSchema
	}
	snap := extensionsFromOpts(opts)
	if snap != nil && len(snap.ComponentSchemas) > 0 {
		for _, m := range snap.Aggregators {
			if _, dup := out[m.Name]; dup {
				continue
			}
			if schema, ok := snap.ComponentSchemas[m.Name]; ok {
				out[m.Name] = schema
			}
		}
	}
	return out
}

// grouperComponentSchemaIndex returns a {operator name → ComponentSchema}
// lookup over the built-in grouper capability table. The map is the
// authoritative no-execute source predict reads at slot-population time
// — the universal-floor keys ({total_n, n_null}) are already prepended
// inside groupSchema, so every entry mirrors the same shape the manifest
// projects under Manifest.ComponentsSchemas.Groupers.
//
// Extension groupers from opts.Extensions.ComponentSchemas are merged
// in afterwards on the same contract as aggregatorComponentSchemaIndex.
func grouperComponentSchemaIndex(opts *PredictOptions) map[string]descriptor.ComponentSchema {
	caps := grouperCapabilities()
	out := make(map[string]descriptor.ComponentSchema, len(caps))
	for _, op := range caps {
		out[op.Name] = op.ComponentSchema
	}
	snap := extensionsFromOpts(opts)
	if snap != nil && len(snap.ComponentSchemas) > 0 {
		for _, m := range snap.Groupers {
			if _, dup := out[m.Name]; dup {
				continue
			}
			if schema, ok := snap.ComponentSchemas[m.Name]; ok {
				out[m.Name] = schema
			}
		}
	}
	return out
}

// populateGroupPredicts fills PredictResult.Groups with one GroupPredict
// descriptor per req.Groups slot, in matching order. The per-slot
// ComponentSchema is looked up from the built-in grouper capability
// table — the manifest snapshot's single source of truth (same
// projection ComponentsSchemas.Groupers uses). Extension groupers that
// have not yet declared a ComponentSchema produce an empty
// ComponentSchema and BufferedComponents=false; the universal-floor
// populator still attaches {"total_n", "n_null"} at runtime.
//
// BufferedComponents is true iff the resolved ComponentSchema's
// Mergeability is descriptor.None. Of the seven registered groupers
// today, GROUP_QUANTILE is the canonical None — quantile cutoffs need
// the sorted full input. The flag advertises streaming behaviour but
// does NOT flip PredictResult.Streamable.
//
// Predict stays no-execute: this helper reads only the static
// capabilities table — no `internal/service/` or `internal/processing/` imports.
func populateGroupPredicts(result *descriptor.PredictResult, req *types.Request, opts *PredictOptions) {
	if result == nil || req == nil {
		return
	}
	if len(req.Groups) == 0 {
		// Empty-but-not-nil — keep JSON shape stable.
		result.Groups = []descriptor.GroupPredict{}
		return
	}
	idx := grouperComponentSchemaIndex(opts)
	out := make([]descriptor.GroupPredict, 0, len(req.Groups))
	for _, grp := range req.Groups {
		if grp == nil {
			continue
		}
		schema := idx[string(grp.Type)]
		out = append(out, descriptor.GroupPredict{
			Type:               grp.Type,
			Field:              grp.Field,
			ComponentSchema:    schema,
			BufferedComponents: schema.Mergeability == descriptor.None,
		})
	}
	result.Groups = out
}

// populateAggregationPredicts fills PredictResult.Aggregations with one
// AggregationPredict descriptor per req.Aggregations slot, in matching
// order. The per-slot ComponentSchema is looked up from the built-in
// aggregator capability table (the manifest snapshot's single source of
// truth — same projection ComponentsSchemas.Aggregators uses). When the
// slot references an extension operator that the snapshot has not yet
// declared a schema for, the descriptor surfaces an empty
// ComponentSchema and BufferedComponents=false; the universal-floor
// populator still attaches {"n", "n_null"} at runtime.
//
// BufferedComponents is true iff the resolved ComponentSchema's
// Mergeability is descriptor.None — meaning the components map cannot
// be reconstructed from per-chunk partials and the orchestrator emits
// the Components block only on the terminal buffered flush. The flag
// advertises streaming behaviour but does NOT flip PredictResult.Streamable
// (the data slice still streams).
//
// Predict stays no-execute: this helper reads only the static
// capabilities table — no `internal/service/` or `internal/processing/` imports.
func populateAggregationPredicts(result *descriptor.PredictResult, req *types.Request, opts *PredictOptions) {
	if result == nil || req == nil {
		return
	}
	if len(req.Aggregations) == 0 {
		// Empty-but-not-nil — keep JSON shape stable.
		result.Aggregations = []descriptor.AggregationPredict{}
		return
	}
	idx := aggregatorComponentSchemaIndex(opts)
	out := make([]descriptor.AggregationPredict, 0, len(req.Aggregations))
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		schema := idx[string(agg.Type)]
		out = append(out, descriptor.AggregationPredict{
			Type:               agg.Type,
			Field:              agg.Field,
			Label:              agg.Label,
			ComponentSchema:    schema,
			BufferedComponents: schema.Mergeability == descriptor.None,
		})
	}
	result.Aggregations = out
}

// filtererComponentSchemaIndex returns a {operator name → ComponentSchema}
// lookup over the built-in filterer capability table. The map is the
// authoritative no-execute source predict reads at slot-population
// time — the universal-floor keys ({n_in, n_out, n_null_input}) are
// already prepended inside filterSchema, so every entry mirrors the
// same shape the manifest projects under
// Manifest.ComponentsSchemas.Filterers.
//
// Extension filterers from opts.Extensions.ComponentSchemas are merged
// in afterwards on the same contract as aggregatorComponentSchemaIndex.
func filtererComponentSchemaIndex(opts *PredictOptions) map[string]descriptor.ComponentSchema {
	caps := filtererCapabilities()
	out := make(map[string]descriptor.ComponentSchema, len(caps))
	for _, op := range caps {
		out[op.Name] = op.ComponentSchema
	}
	snap := extensionsFromOpts(opts)
	if snap != nil && len(snap.ComponentSchemas) > 0 {
		for _, m := range snap.Filterers {
			if _, dup := out[m.Name]; dup {
				continue
			}
			if schema, ok := snap.ComponentSchemas[m.Name]; ok {
				out[m.Name] = schema
			}
		}
	}
	return out
}

// populateFiltererPredicts fills PredictResult.Filterers with one
// FiltererPredict descriptor per req.Filterers slot, in matching order.
// The per-slot ComponentSchema is looked up from the built-in filterer
// capability table — the manifest snapshot's single source of truth
// (same projection ComponentsSchemas.Filterers uses). Extension
// filterers that have not yet declared a ComponentSchema produce an
// empty ComponentSchema and BufferedComponents=false; the universal-
// floor populator still attaches {n_in, n_out, n_null_input} at
// runtime.
//
// BufferedComponents is true iff the resolved ComponentSchema's
// Mergeability is descriptor.None. In v1 every built-in filterer is
// Mergeable (counter triples fold trivially via integer addition), so
// the flag is always false for built-ins; the surface exists for
// future per-filter specifics that may downgrade individual entries.
// The flag advertises streaming behaviour but does NOT flip
// PredictResult.Streamable.
//
// Predict stays no-execute: this helper reads only the static
// capabilities table — no `internal/service/` or `internal/processing/` imports.
func populateFiltererPredicts(result *descriptor.PredictResult, req *types.Request, opts *PredictOptions) {
	if result == nil || req == nil {
		return
	}
	if len(req.Filterers) == 0 {
		// Empty-but-not-nil — keep JSON shape stable.
		result.Filterers = []descriptor.FiltererPredict{}
		return
	}
	idx := filtererComponentSchemaIndex(opts)
	out := make([]descriptor.FiltererPredict, 0, len(req.Filterers))
	for _, fil := range req.Filterers {
		if fil == nil {
			continue
		}
		schema := idx[string(fil.Type)]
		out = append(out, descriptor.FiltererPredict{
			Type:               fil.Type,
			Field:              fil.Field,
			ComponentSchema:    schema,
			BufferedComponents: schema.Mergeability == descriptor.None,
		})
	}
	result.Filterers = out
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
	descriptors []descriptor.OverlayAppliedDescriptor, costs map[string]float64,
	specs []types.OverlaySpec,
) ([]descriptor.OverlayAppliedDescriptor, map[string]float64) {
	for i := range specs {
		spec := &specs[i]
		name := overlayDescriptorName(spec)
		streamable, _ := types.OverlayStreamable(spec.Kind)
		descriptors = append(descriptors, descriptor.OverlayAppliedDescriptor{
			Name:       name,
			Kind:       spec.Kind,
			Scope:      spec.Scope,
			Shape:      resolveOverlayShape(spec.Kind, spec.Scope),
			Ref:        resolveOverlayRef(spec),
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

// resolveOverlayShape returns the OverlayShape the layer will carry
// for the given (Kind, Scope) pair. Reads the per-kind capability
// table (overlayCapabilityFor) as the single source of truth.
//
// Resolution rules:
//
//   - Single-shape kinds (the bulk of the catalog) return the kind's
//     only declared shape directly.
//   - Dual-shape kinds (OVERLAY_INDEX_VS_REF / OVERLAY_DELTA_VS_REF /
//     OVERLAY_PANEL_INDEX_VS_REF on Compose hosts) disambiguate via
//     Scope: OverlayScopeCell ⇒ matrix, OverlayScopeGroup ⇒ series,
//     OverlayScopeTotal ⇒ scalar.
//   - OVERLAY_FORMULA is a tri-shape kind whose shape is selected by
//     Scope (cell ⇒ matrix, group ⇒ series, total ⇒ scalar) per the
//     per-shape FORMULA binder.
//   - Whole-chain kinds (OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE)
//     inherit the target stage's host shape at runtime — Predict cannot
//     resolve a single shape without inspecting the chain, so the
//     resolver returns "" (the descriptor surfaces an empty Shape
//     field via the omitempty rule).
//   - Unknown kinds (absent from the catalog) return "" — the
//     validator surfaces PULSE_OVERLAY_KIND_UNKNOWN for the same
//     condition.
func resolveOverlayShape(kind types.OverlayKind, scope types.OverlayScope) types.OverlayShape {
	cap := overlayCapabilityFor(kind)
	if len(cap.Shapes) == 0 {
		return ""
	}
	if len(cap.Shapes) == 1 {
		return cap.Shapes[0]
	}
	// Multi-shape kind — disambiguate via Scope. The capability
	// table declares every shape the kind may emit; the scope-to-
	// shape mapping is uniform across the catalog:
	//   cell  ⇒ matrix
	//   group ⇒ series
	//   total ⇒ scalar
	//   row   ⇒ series
	//   column⇒ series
	//   matrix⇒ matrix
	scopeShape := scopeToShape(scope)
	if scopeShape == "" {
		return ""
	}
	for _, s := range cap.Shapes {
		if s == scopeShape {
			return s
		}
	}
	// Scope incompatible with any declared shape — the per-kind
	// validator (PULSE_OVERLAY_SCOPE_UNSUPPORTED) catches this; the
	// descriptor surfaces "" so renderers can branch on the gap.
	return ""
}

// scopeToShape maps an OverlayScope to its canonical OverlayShape per
// the catalog convention. Returns "" for unknown scopes.
func scopeToShape(scope types.OverlayScope) types.OverlayShape {
	switch scope {
	case types.OverlayScopeCell:
		return types.OverlayShapeMatrix
	case types.OverlayScopeMatrix:
		return types.OverlayShapeMatrix
	case types.OverlayScopeRow, types.OverlayScopeColumn, types.OverlayScopeGroup:
		return types.OverlayShapeSeries
	case types.OverlayScopeTotal:
		return types.OverlayShapeScalar
	}
	return ""
}

// resolveOverlayRef returns a renderer-friendly string describing the
// resolved OverlayRef discriminated union variant. Format follows the
// per-family conventions documented on OverlayAppliedDescriptor.Ref.
//
// Returns "" for:
//   - Implicit-margin kinds (CHISQ_*, FISHER_EXACT_CELL, INDEX_VS_TOTAL,
//     ZSCORE_VS_TOTAL, SHARE_OF_TOTAL, FORMULA) that leave Ref empty.
//   - COMPOSE-only kinds (OVERLAY_INDEX_VS_REF / OVERLAY_DELTA_VS_REF /
//     OVERLAY_PROP_Z_CELL / OVERLAY_PROP_Z_PANEL / OVERLAY_PANEL_INDEX_VS_REF
//     / OVERLAY_T_CELL / OVERLAY_T_VS_REF / OVERLAY_CHISQ_VS_REF /
//     OVERLAY_RANK) whose reference + target slots ride on the
//     ComposeOverlaySpec slot-label pair, not on Ref.
func resolveOverlayRef(spec *types.OverlaySpec) string {
	if spec == nil {
		return ""
	}
	ref := spec.Ref
	switch {
	case ref.Margin != nil:
		return "margin:" + string(ref.Margin.Axis)
	case ref.Sibling != nil:
		return "sibling:" + ref.Sibling.Field + "=" + ref.Sibling.Value
	case ref.BaselineIndex != nil:
		return "baseline_index:" + itoa(ref.BaselineIndex.Position)
	case ref.Prior != nil:
		return "prior:" + itoa(ref.Prior.Lag)
	case ref.RollingMean != nil:
		return "rolling_mean"
	case ref.YoY != nil:
		return "yoy"
	case ref.Population != nil:
		return "population:" + ref.Population.Cohort
	case ref.Stage != nil:
		if ref.Stage.Index != nil {
			return "stage:index:" + itoa(*ref.Stage.Index)
		}
		return "stage:name:" + ref.Stage.Name
	case ref.Slot != nil:
		return "slot:" + ref.Slot.Name
	}
	return ""
}

// itoa renders an integer for the Ref string surface. Uses
// strconv.Itoa via the small wrapper to keep the call site readable
// (the surrounding function reads cleanly as a switch on the union).
func itoa(n int) string {
	return strconv.Itoa(n)
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

// predictJoinedSchema returns the schema a Request executes over: for a
// single JoinSpec whose right cohort opts.SchemaLoader can read, the
// joined schema (internal/encoding.JoinedSchema — the runtime's own
// rule); otherwise schema unchanged. A right cohort that cannot be read,
// a join-key refusal (internal/encoding.JoinKeysRefusals) or a
// joined-field collision is a predict error under its own code, as the
// runtime refuses each.
func predictJoinedSchema(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) *encoding.Schema {
	if req == nil || len(req.Joins) != 1 || req.Joins[0] == nil || opts == nil || opts.SchemaLoader == nil {
		return schema
	}
	spec := req.Joins[0]
	right, err := opts.SchemaLoader(spec.Right)
	if err != nil {
		addCodedError(env, err)
		return schema
	}
	// Kind and OnPairs: the one join-key rule the runtime refuses with
	// before it decodes the right side. The joined schema below does
	// not depend on the keys, so validation continues against it.
	for _, ce := range encx.JoinKeysRefusals(schema, right, spec) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	}
	joined, err := encx.JoinedSchema(schema, right, spec.As)
	if err != nil {
		addCodedError(env, err)
		return schema
	}
	return joined
}

// validateOperatorTypes reports every top-level slot whose operator
// Type is still empty once defaults have (or, under DisableDefaults,
// have not) run. The runtime refuses each one at operator construction
// with PROCESSING_CONFIG "unknown <family> type: "; predict mirrors
// code and message, adding the slot path.
func validateOperatorTypes(env *descriptor.Envelope, req *types.Request) {
	if req == nil {
		return
	}
	report := func(family, slot string) {
		env.AddError(string(errors.PROCESSING_CONFIG), "unknown "+family+" type: ", map[string]any{"slot": slot})
	}
	for i, a := range req.Aggregations {
		if a != nil && a.Type == "" {
			report("aggregation", "aggregations["+strconv.Itoa(i)+"]")
		}
	}
	for i, f := range req.Filterers {
		if f != nil && f.Type == "" {
			report("filter", "filterers["+strconv.Itoa(i)+"]")
		}
	}
	for i, f := range req.Features {
		if f != nil && f.Type == "" {
			report("feature", "features["+strconv.Itoa(i)+"]")
		}
	}
	for i, a := range req.Attributes {
		if a != nil && a.Type == "" {
			report("attribute", "attributes["+strconv.Itoa(i)+"]")
		}
	}
	for i, g := range req.Groups {
		if g != nil && g.Type == "" {
			report("group", "groups["+strconv.Itoa(i)+"]")
		}
	}
}
