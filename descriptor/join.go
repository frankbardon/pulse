package descriptor

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// JoinValidationResult is the structured output of ValidateJoin.
// Echoes the request unchanged and exposes the inferred output
// schema as a list of field names.
type JoinValidationResult struct {
	Valid       bool                `json:"valid"`
	Request     *types.Request      `json:"request"`
	LeftSchema  *PredictSchemaInfo  `json:"left_schema,omitempty"`
	RightSchema *PredictSchemaInfo  `json:"right_schema,omitempty"`
	JoinedFields []string           `json:"joined_fields,omitempty"`
}

// ValidateJoin validates a Request whose Joins slot carries one
// JoinSpec against the headers + schemas of the left and right
// cohorts. It never reads record data — descriptor's no-execute
// contract applies. Errors land as SERVICE_VALIDATION or
// PULSE_JOIN_*; emits JoinedFields when the join would succeed at
// runtime.
func ValidateJoin(leftData, rightData io.ReadSeeker, req *types.Request) *Envelope {
	result := &JoinValidationResult{Valid: true, Request: req}
	env := NewEnvelope(result)

	if req == nil {
		env.AddError(string(errors.SERVICE_VALIDATION), "request is required", nil)
		result.Valid = false
		return env
	}
	if len(req.Joins) == 0 {
		env.AddError(string(errors.SERVICE_VALIDATION), "request.joins is empty", nil)
		result.Valid = false
		return env
	}
	if len(req.Joins) > 1 {
		env.AddError(string(errors.PULSE_JOIN_TOO_MANY),
			"v1 supports exactly one JoinSpec per Request",
			map[string]any{"count": len(req.Joins)})
	}
	spec := req.Joins[0]
	if spec == nil {
		env.AddError(string(errors.SERVICE_VALIDATION), "JoinSpec is nil", nil)
		result.Valid = false
		return env
	}

	leftSchema, err := readHeaderSchema(leftData)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid left pulse file: "+err.Error(), nil)
		result.Valid = false
		return env
	}
	rightSchema, err := readHeaderSchema(rightData)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid right pulse file: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	result.LeftSchema = schemaInfo(leftSchema)
	result.RightSchema = schemaInfo(rightSchema)

	kind := spec.Kind
	if kind == "" {
		kind = "inner"
	}
	if kind != "inner" {
		env.AddError(string(errors.PULSE_JOIN_KIND_NOT_IMPLEMENTED),
			"only inner join is implemented in v1",
			map[string]any{"kind": kind})
	}

	if len(spec.On) == 0 {
		env.AddError(string(errors.PULSE_JOIN_KEYS_EMPTY),
			"JoinSpec.On is empty",
			nil)
	}

	for i, pair := range spec.On {
		if pair.LeftField == "" || pair.RightField == "" {
			env.AddError(string(errors.PULSE_JOIN_KEYS_EMPTY),
				"OnPair requires both LeftField and RightField",
				map[string]any{"index": i})
			continue
		}
		lf := leftSchema.Field(pair.LeftField)
		rf := rightSchema.Field(pair.RightField)
		if lf == nil {
			env.AddError(string(errors.PULSE_JOIN_FIELD_UNKNOWN),
				"OnPair.LeftField not found in left schema",
				map[string]any{"field": pair.LeftField, "index": i})
			continue
		}
		if rf == nil {
			env.AddError(string(errors.PULSE_JOIN_FIELD_UNKNOWN),
				"OnPair.RightField not found in right schema",
				map[string]any{"field": pair.RightField, "index": i})
			continue
		}
		if !joinTypesCompatible(lf.Type, rf.Type) {
			env.AddError(string(errors.PULSE_JOIN_TYPE_MISMATCH),
				"join key types are not compatible",
				map[string]any{
					"left_field":  pair.LeftField,
					"left_type":   lf.Type.String(),
					"right_field": pair.RightField,
					"right_type":  rf.Type.String(),
					"index":       i,
				})
		}
	}

	// Field-collision check.
	seen := make(map[string]struct{}, len(leftSchema.Fields)+len(rightSchema.Fields))
	for _, f := range leftSchema.Fields {
		seen[f.Name] = struct{}{}
	}
	joined := make([]string, 0, len(leftSchema.Fields)+len(rightSchema.Fields))
	for _, f := range leftSchema.Fields {
		joined = append(joined, f.Name)
	}
	for _, f := range rightSchema.Fields {
		name := f.Name
		if spec.As != "" {
			name = spec.As + f.Name
		}
		if _, dup := seen[name]; dup {
			env.AddError(string(errors.PULSE_JOIN_FIELD_COLLISION),
				"joined schema field collides between left and right",
				map[string]any{"field": name, "as": spec.As})
			continue
		}
		seen[name] = struct{}{}
		joined = append(joined, name)
	}
	result.JoinedFields = joined

	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return env
}

// ValidateJoinFromBytes is a convenience wrapper around byte buffers.
func ValidateJoinFromBytes(left, right []byte, req *types.Request) *Envelope {
	return ValidateJoin(bytes.NewReader(left), bytes.NewReader(right), req)
}

func readHeaderSchema(r io.ReadSeeker) (*encoding.Schema, error) {
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}
	return encoding.ReadSchema(r)
}

func schemaInfo(s *encoding.Schema) *PredictSchemaInfo {
	info := &PredictSchemaInfo{FieldCount: len(s.Fields)}
	for _, f := range s.Fields {
		info.Fields = append(info.Fields, f.Name)
	}
	return info
}

// joinTypesCompatible mirrors processing.typesCompatibleForJoin —
// kept here so descriptor stays free of processing imports.
func joinTypesCompatible(a, b encoding.FieldType) bool {
	if a == b {
		return true
	}
	if a.IsCategorical() && b.IsCategorical() {
		return true
	}
	if joinNumericFamily(a) && joinNumericFamily(b) {
		return true
	}
	return false
}

func joinNumericFamily(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeU4,
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate:
		return true
	}
	return false
}
