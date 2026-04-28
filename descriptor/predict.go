package descriptor

import (
	"bytes"
	"io"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// numericAggregations are aggregation types that only make sense on numeric fields.
var numericAggregations = map[types.AggregationType]bool{
	types.AGG_SUM:     true,
	types.AGG_AVERAGE: true,
	types.AGG_MIN:     true,
	types.AGG_MAX:     true,
	types.AGG_STDDEV:  true,
	types.AGG_RANGE:   true,
	types.AGG_ZSCORE:  true,
	types.AGG_MEDIAN:    true,
	types.AGG_VARIANCE:  true,
	types.AGG_SKEWNESS:  true,
	types.AGG_KURTOSIS:   true,
	types.AGG_PERCENTILE: true,
}

// PredictOptions controls predict behavior.
type PredictOptions struct {
	// Strict upgrades warnings to errors.
	Strict bool
}

// PredictResult holds the validated request and any diagnostics.
type PredictResult struct {
	Valid       bool              `json:"valid"`
	Request     *types.Request    `json:"request"`
	SchemaInfo  *PredictSchemaInfo `json:"schema_info,omitempty"`
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
		Valid:   true,
		Request: req,
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

	// Validate request fields exist in schema.
	validateRequestFields(env, req, schema, opts)

	// Check description quality.
	validateDescriptionQuality(env, schema, opts)

	// If any errors were added, mark invalid.
	if len(env.Errors) > 0 {
		result.Valid = false
	}

	return env
}

// PredictFromBytes is a convenience wrapper that creates a reader from bytes.
func PredictFromBytes(data []byte, req *types.Request, opts *PredictOptions) *Envelope {
	return Predict(bytes.NewReader(data), req, opts)
}

// validateRequestFields checks that all referenced fields exist and that
// numeric aggregations on categorical fields produce warnings.
func validateRequestFields(env *Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	// Check aggregation fields.
	for _, agg := range req.Aggregations {
		f := schema.Field(agg.Field)
		if f == nil {
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
	}

	// Check filter fields.
	for _, fil := range req.Filterers {
		if fil.Field == "" && fil.Type == types.FILTER_EXPRESSION {
			continue // expression filters don't require a field
		}
		if fil.Field != "" {
			f := schema.Field(fil.Field)
			if f == nil {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"filter references unknown field: "+fil.Field,
					map[string]any{"field": fil.Field, "filter": string(fil.Type)},
				)
			}
		}
	}

	// Check group fields.
	for _, grp := range req.Groups {
		f := schema.Field(grp.Field)
		if f == nil {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"group references unknown field: "+grp.Field,
				map[string]any{"field": grp.Field, "group": string(grp.Type)},
			)
		}
	}

	// Check attribute fields.
	for _, attr := range req.Attributes {
		f := schema.Field(attr.Field)
		if f == nil {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"attribute references unknown field: "+attr.Field,
				map[string]any{"field": attr.Field, "attribute": string(attr.Type)},
			)
		}
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
	for _, u := range unhelpful {
		if lower == u {
			return true
		}
	}
	return false
}
