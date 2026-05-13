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

// geoAggregations are aggregations meaningful on point_f64 / h3_cell.
var geoAggregations = map[types.AggregationType]bool{
	types.AGG_GEO_CENTROID: true,
	types.AGG_GEO_BBOX:     true,
	types.AGG_COUNT:        true,
}

// PredictOptions controls predict behavior.
type PredictOptions struct {
	// Strict upgrades warnings to errors.
	Strict bool
}

// PredictResult holds the validated request and any diagnostics.
type PredictResult struct {
	Valid      bool               `json:"valid"`
	Request    *types.Request     `json:"request"`
	SchemaInfo *PredictSchemaInfo `json:"schema_info,omitempty"`
	// Streamable reports whether ProcessStream / process --stream can
	// emit rows without buffering the entire result. False whenever the
	// request uses groups, attributes, windows, geo aggregations, decimal
	// fields, or any non-streamable operator. Computed via per-type
	// Streamable() methods plus schema-aware checks.
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
		Valid:           true,
		Request:         req,
		DefaultsApplied: []DefaultApplied{},
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

	// Check description quality.
	validateDescriptionQuality(env, schema, opts)

	// Compute streamability — per-type Streamable() methods plus schema-aware
	// gates (decimal fields force buffered, geo aggs force buffered).
	result.Streamable, result.StreamableReasons = computeStreamable(req, schema)

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
func computeStreamable(req *types.Request, schema *encoding.Schema) (bool, []string) {
	var reasons []string

	// Phase 0: any regression slot forces the buffered path. The
	// streaming sufficient-statistics path lands in Phase 1; the
	// spec-level RegressionSpec.Streamable check (modifier downgrade)
	// is still surfaced per-spec for forward compatibility.
	for _, reg := range req.Regressions {
		if reg == nil {
			continue
		}
		if !reg.Streamable() {
			reasons = append(reasons, "regression "+string(reg.Type)+" requires the buffered path under the current spec (modifier or family forces non-streaming)")
			continue
		}
		reasons = append(reasons, "regression "+string(reg.Type)+" runs via the buffered path in Phase 0")
	}

	if len(req.Aggregations) == 0 {
		reasons = append(reasons, "no aggregations: streaming path requires at least one OnlineAggregator")
	}
	for _, grp := range req.Groups {
		if !grp.Type.Streamable() {
			reasons = append(reasons, "group "+string(grp.Type)+" requires the buffered path")
		}
	}
	for _, attr := range req.Attributes {
		if !attr.Type.Streamable() {
			reasons = append(reasons, "attribute "+string(attr.Type)+" requires a full pass for population stats")
		}
	}
	if len(req.Windows) > 0 {
		reasons = append(reasons, "windows run over the post-aggregate row set")
	}

	for _, agg := range req.Aggregations {
		if !agg.Type.Streamable() {
			reasons = append(reasons, "aggregation "+string(agg.Type)+" is not streamable")
			continue
		}
		// Geo aggregations dispatch through buffered AggregateGeoField even
		// when their type would otherwise be online.
		if geoAggregations[agg.Type] && agg.Type != types.AGG_COUNT {
			reasons = append(reasons, "aggregation "+string(agg.Type)+" uses the buffered geo path")
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
		if !feat.Type.Streamable() {
			reasons = append(reasons, "feature "+string(feat.Type)+" is not streamable")
		}
	}

	reasons = append(reasons, streamableTestReasons(req)...)

	return len(reasons) == 0, reasons
}

// PredictFromBytes is a convenience wrapper that creates a reader from bytes.
func PredictFromBytes(data []byte, req *types.Request, opts *PredictOptions) *Envelope {
	return Predict(bytes.NewReader(data), req, opts)
}

// validateRequestFields checks that all referenced fields exist and that
// numeric aggregations on categorical fields produce warnings. The
// projected column set augments the schema with feature output names so
// downstream stages can address derived columns.
func validateRequestFields(env *Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, opts *PredictOptions) {
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

		// Warn if numeric aggregation is applied to categorical field.
		if f.Type.IsCategorical() && numericAggregations[agg.Type] {
			entry := &EnvelopeEntry{
				Code:    string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
				Message: "numeric aggregation " + string(agg.Type) + " is not meaningful for categorical field " + agg.Field,
				Details: map[string]any{"field": agg.Field, "aggregation": string(agg.Type)},
			}
			if opts.Strict {
				env.Errors = append(env.Errors, entry)
			} else {
				env.Warnings = append(env.Warnings, entry)
			}
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

		// Geo field aggregation validity matrix.
		if f.Type.IsGeo() && !geoAggregations[agg.Type] {
			entry := &EnvelopeEntry{
				Code:    string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_GEO),
				Message: "aggregation " + string(agg.Type) + " is not defined on geospatial field " + agg.Field,
				Details: map[string]any{"field": agg.Field, "aggregation": string(agg.Type), "type": f.Type.String()},
			}
			if opts.Strict {
				env.Errors = append(env.Errors, entry)
			} else {
				env.Warnings = append(env.Warnings, entry)
			}
		}

		// AGG_GEO_CENTROID / AGG_GEO_BBOX must target point_f64 only.
		if (agg.Type == types.AGG_GEO_CENTROID || agg.Type == types.AGG_GEO_BBOX) && f.Type != encoding.FieldTypePointF64 {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				string(agg.Type)+" requires a point_f64 field; got "+f.Type.String(),
				map[string]any{"field": agg.Field, "aggregation": string(agg.Type), "type": f.Type.String()},
			)
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
			// Geo filterers require point_f64 fields.
			if (fil.Type == types.FILTER_GEO_WITHIN || fil.Type == types.FILTER_GEO_WITHIN_RADIUS_M) && f != nil && f.Type != encoding.FieldTypePointF64 {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					string(fil.Type)+" requires a point_f64 field; got "+f.Type.String(),
					map[string]any{"field": fil.Field, "filter": string(fil.Type), "type": f.Type.String()},
				)
			}
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
		// GROUP_H3_CELL accepts point_f64 (resolution required) or h3_cell (resolution optional).
		if grp.Type == types.GROUP_H3_CELL && f != nil {
			if f.Type != encoding.FieldTypePointF64 && f.Type != encoding.FieldTypeH3Cell {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"GROUP_H3_CELL requires point_f64 or h3_cell field; got "+f.Type.String(),
					map[string]any{"field": grp.Field, "group": string(grp.Type), "type": f.Type.String()},
				)
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
// applyAttributes: an explicit label wins; otherwise the default is
// "<TYPE>_<field>" so aggregations referencing the implicit label
// resolve under predict the same way they do under process.
func projectAttributeOutputs(req *types.Request, projected map[string]bool) {
	for _, attr := range req.Attributes {
		label := attr.Label
		if label == "" {
			label = string(attr.Type) + "_" + attr.Field
		}
		projected[label] = true
	}
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
