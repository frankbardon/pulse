package descriptor

import (
	"bytes"
	"fmt"
	"io"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// FacetValidationResult is the structured ValidateFacet output.
// Mirrors the predict surface: the resolved request is echoed back
// verbatim (callers may want to forward it to FacetSchema after
// inspection) and Warnings carry advisory issues that do not block
// execution.
type FacetValidationResult struct {
	// Valid mirrors envelope.Errors emptiness.
	Valid bool `json:"valid"`

	// Request echoes the input request unchanged.
	Request *types.FacetRequest `json:"request"`

	// SchemaInfo summarises the cohort schema used for validation.
	SchemaInfo *PredictSchemaInfo `json:"schema_info,omitempty"`

	// OverlaysApplied lists every FACET-host overlay spec the validator
	// accepted, in req.Overlays order. Each entry echoes the catalog
	// identity (Name, Kind, Scope) plus the streamability flag harvested
	// from types.OverlayStreamable(kind) so the caller can reason about
	// the buffered/streaming routing decision without re-parsing the
	// spec. Empty when req.Overlays is empty; never nil in JSON output.
	//
	// Per kind-catalog-v1 PRD §I-FR-I3 the FACET-host predict surface
	// mirrors the Request-host predict surface so LLM callers see the
	// same per-spec descriptor shape regardless of host. E5-S10 wires
	// the four FACET-host kinds (OVERLAY_INDEX_VS_POP /
	// OVERLAY_ZSCORE_VS_POP / OVERLAY_CHISQ_VS_POP / OVERLAY_KS_VS_POP)
	// onto this surface; sibling to PredictResult.OverlaysApplied.
	OverlaysApplied []OverlayAppliedDescriptor `json:"overlays_applied"`

	// OverlayCost maps each FACET-host overlay-spec Name to a coarse
	// cost score the routing layer can consult before execution. Sibling
	// to PredictResult.OverlayCost — streamable kinds carry
	// overlayCostStreamable (~5% extra work) and buffered kinds carry
	// overlayCostBuffered (~one extra payload traversal). Per kind-
	// catalog-v1 PRD §I-FR-I3 the value is intentionally coarse — callers
	// budgeting cost across slots sum the map values. Empty when
	// req.Overlays is empty; never nil in JSON output.
	OverlayCost map[string]float64 `json:"overlay_cost"`
}

// ValidateFacet validates a FacetRequest against a .pulse cohort header
// + schema. It never reads record data — predict's "no execution"
// contract applies here too. Errors land in the envelope as
// SERVICE_VALIDATION; advisory issues (percentiles on a non-numeric
// field, a low DiscreteTopK, IncludeHistogram on a discrete-only
// request) become warnings.
//
// Callers can use this to gate a UI before issuing the (potentially
// expensive) FacetSchema call.
func ValidateFacet(fileData io.ReadSeeker, req *types.FacetRequest) *Envelope {
	return ValidateFacetWithExtensions(fileData, req, nil)
}

// ValidateFacetWithExtensions extends ValidateFacet with the
// embedder-registered extension snapshot so label-table references
// can be resolved. Pass nil to opt out (same behavior as ValidateFacet).
func ValidateFacetWithExtensions(fileData io.ReadSeeker, req *types.FacetRequest, snap *ExtensionsSnapshot) *Envelope {
	result := &FacetValidationResult{
		Valid:           true,
		Request:         req,
		OverlaysApplied: []OverlayAppliedDescriptor{},
		OverlayCost:     map[string]float64{},
	}
	env := NewEnvelope(result)

	if req == nil {
		env.AddError(string(errors.SERVICE_VALIDATION), "facet request is required", nil)
		result.Valid = false
		return env
	}

	if err := encoding.ReadHeader(fileData); err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse file header: "+err.Error(), nil)
		result.Valid = false
		return env
	}
	schema, err := encoding.ReadSchema(fileData)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse schema: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	result.SchemaInfo = &PredictSchemaInfo{FieldCount: len(schema.Fields)}
	for _, f := range schema.Fields {
		result.SchemaInfo.Fields = append(result.SchemaInfo.Fields, f.Name)
	}

	if len(req.Fields) == 0 {
		env.AddError(string(errors.SERVICE_VALIDATION), "facet request requires at least one field", nil)
	}
	if req.DiscreteTopK < 0 {
		env.AddError(string(errors.SERVICE_VALIDATION), "discrete_top_k must be >= 0", nil)
	}
	if req.DiscreteTopK > 0 && req.DiscreteTopK < 5 {
		env.AddWarning(string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY),
			fmt.Sprintf("discrete_top_k=%d is unusually small; truncation will hide most distinct values", req.DiscreteTopK),
			map[string]any{"discrete_top_k": req.DiscreteTopK})
	}

	for _, p := range req.NumericPercentiles {
		if math.IsNaN(p) || !(p > 0 && p < 1) {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("numeric_percentiles entry %g must lie in (0, 1)", p),
				map[string]any{"percentile": p})
		}
	}

	if req.IncludeHistogram {
		if req.HistogramRange[0] >= req.HistogramRange[1] {
			env.AddError(string(errors.SERVICE_VALIDATION),
				"histogram_range requires min < max",
				map[string]any{"min": req.HistogramRange[0], "max": req.HistogramRange[1]})
		}
		if req.HistogramBins < 0 {
			env.AddError(string(errors.SERVICE_VALIDATION), "histogram_bins must be >= 0", nil)
		}
		if req.HistogramBins > 256 {
			env.AddError(string(errors.SERVICE_VALIDATION),
				"histogram_bins exceeds cap of 256",
				map[string]any{"requested": req.HistogramBins})
		}
	}

	for _, name := range req.Fields {
		f := schema.Field(name)
		if f == nil {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("facet field %q not found in schema", name),
				map[string]any{"field": name})
			continue
		}
		if len(req.NumericPercentiles) > 0 && !facetIsNumeric(f.Type) {
			env.AddWarning(string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
				fmt.Sprintf("numeric_percentiles ignored for non-numeric field %q (type %s)", name, f.Type.String()),
				map[string]any{"field": name, "type": f.Type.String()})
		}
		if req.IncludeHistogram && !facetIsNumeric(f.Type) {
			env.AddWarning(string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
				fmt.Sprintf("include_histogram ignored for non-numeric field %q (type %s)", name, f.Type.String()),
				map[string]any{"field": name, "type": f.Type.String()})
		}
		// Set-typed fields: numeric stats are not meaningful on bitmasks
		// (mean / percentile / histogram of raw mask values is gibberish).
		// Reject up-front so the caller switches to per-bit value_counts.
		if f.Type.IsSet() && len(req.NumericPercentiles) > 0 {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("numeric_percentiles is not meaningful on set field %q; use the per-bit value_counts that the facet surface already returns", name),
				map[string]any{"field": name, "type": f.Type.String()})
		}
		if f.Type.IsSet() && req.IncludeHistogram {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("include_histogram is not meaningful on set field %q; use the per-bit value_counts that the facet surface already returns", name),
				map[string]any{"field": name, "type": f.Type.String()})
		}
	}

	for _, name := range req.AdditiveFields {
		f := schema.Field(name)
		if f == nil {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("additive field %q not found in schema", name),
				map[string]any{"field": name})
			continue
		}
		for _, fil := range req.Filterers {
			if fil == nil {
				continue
			}
			if fil.Type == types.FILTER_EXPRESSION && filterExpressionMentionsField(fil.Expression, name) {
				env.AddError(string(errors.SERVICE_VALIDATION),
					fmt.Sprintf("additive field %q referenced inside FILTER_EXPRESSION; express the predicate as discrete filterers instead", name),
					map[string]any{"field": name, "expression": fil.Expression})
			}
		}
	}

	for _, fil := range req.Filterers {
		if fil == nil || fil.Field == "" {
			continue
		}
		if schema.Field(fil.Field) == nil {
			env.AddError(string(errors.SERVICE_VALIDATION),
				fmt.Sprintf("filterer references unknown field: %s", fil.Field),
				map[string]any{"field": fil.Field, "filterer": string(fil.Type)})
		}
	}

	// Validate label bindings against the resolved schema. Facet has no
	// projected output columns beyond the requested fields, so the extra
	// set is empty — augment-mode collision detection only checks the
	// schema namespace.
	ValidateLabels(env, req.Labels, schema, snap, nil)

	// Validate FACET-host overlay specs (E5-S6). Per-kind contracts live
	// in descriptor/overlay_facet.go; the validator is no-op when
	// req.Overlays is empty so the no-overlay envelope shape stays
	// byte-identical to the pre-E5 path.
	ValidateFacetOverlays(env, req, schema)

	// Populate the FACET-host predict surface (OverlaysApplied + OverlayCost)
	// per kind-catalog-v1 PRD §I-FR-I3. E5-S10 wires the four FACET-host
	// kinds onto the FacetValidationResult — sibling to the Request-host
	// PredictResult emission. The populator walks every spec regardless of
	// whether ValidateFacetOverlays surfaced errors so LLM callers see the
	// catalog identity of the spec the engine would attempt to dispatch.
	populateFacetOverlayDescriptors(result, req)

	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return env
}

// ValidateFacetFromBytes is a convenience wrapper that creates a reader
// from bytes.
func ValidateFacetFromBytes(data []byte, req *types.FacetRequest) *Envelope {
	return ValidateFacet(bytes.NewReader(data), req)
}

// facetIsNumeric is the facet-side numeric predicate. Intentionally
// excludes the bit-packed bool encoding (packed_bool) — a histogram or
// percentile profile over a binary indicator is degenerate (one or two
// buckets), so refusing it is more useful than producing the trivial
// answer. Widen only when a facet operator actually wants 0/1 numeric
// stats. Canonical broader predicate: encoding.FieldType.IsNumericForAnalytics.
func facetIsNumeric(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeU4,
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate,
		encoding.FieldTypeDecimal128:
		return true
	}
	return false
}

// filterExpressionMentionsField returns true when expr references field
// as a standalone identifier. Mirrors the runtime helper inside service
// so descriptor stays free of service / processing imports.
func filterExpressionMentionsField(expr, field string) bool {
	if expr == "" || field == "" {
		return false
	}
	for i := 0; i+len(field) <= len(expr); i++ {
		if expr[i:i+len(field)] != field {
			continue
		}
		left := i == 0 || !facetIdentChar(expr[i-1])
		right := i+len(field) == len(expr) || !facetIdentChar(expr[i+len(field)])
		if left && right {
			return true
		}
	}
	return false
}

func facetIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
