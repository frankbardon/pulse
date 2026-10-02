package descriptor

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// JoinValidationResult is the structured output of ValidateJoin.
// Echoes the request unchanged and exposes the inferred output
// schema as a list of field names.
type JoinValidationResult struct {
	Valid        bool                          `json:"valid"`
	Request      *types.Request                `json:"request"`
	LeftSchema   *descriptor.PredictSchemaInfo `json:"left_schema,omitempty"`
	RightSchema  *descriptor.PredictSchemaInfo `json:"right_schema,omitempty"`
	JoinedFields []string                      `json:"joined_fields,omitempty"`
}

// JoinCountRefusal is the v1 join-count rule — exactly one JoinSpec
// per Request — shared by runtime Process (internal/service, checked
// before the crosstab and join dispatch) and every no-execute
// validator (Predict, ValidateJoin, the Compose slots, chain stage 0),
// so both refuse with the same code, message and details: a
// PULSE_JOIN_TOO_MANY {count} for more than one JoinSpec, nil
// otherwise (including no join at all).
func JoinCountRefusal(req *types.Request) error {
	if req == nil || len(req.Joins) <= 1 {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_TOO_MANY,
		"v1 supports exactly one JoinSpec per Request",
		map[string]any{"count": len(req.Joins)})
}

// ValidateJoin validates a Request whose Joins slot carries one
// JoinSpec against the headers + schemas of the left and right
// cohorts. It never reads record data — descriptor's no-execute
// contract applies. Errors land as SERVICE_VALIDATION or
// PULSE_JOIN_*; emits JoinedFields when the join would succeed at
// runtime.
func ValidateJoin(leftData, rightData io.ReadSeeker, req *types.Request) *descriptor.Envelope {
	result := &JoinValidationResult{Valid: true, Request: req}
	env := descriptor.NewEnvelope(result)

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
	if jerr := JoinCountRefusal(req); jerr != nil {
		addCodedError(env, jerr)
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

	// Kind and OnPairs: the one join-key rule the runtime refuses with
	// (internal/encoding.JoinKeysRefusals), every refusal reported.
	for _, ce := range encx.JoinKeysRefusals(leftSchema, rightSchema, spec) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
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
func ValidateJoinFromBytes(left, right []byte, req *types.Request) *descriptor.Envelope {
	return ValidateJoin(bytes.NewReader(left), bytes.NewReader(right), req)
}

func readHeaderSchema(r io.ReadSeeker) (*encoding.Schema, error) {
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	return encoding.ReadSchema(r, pulseVersion)
}

func schemaInfo(s *encoding.Schema) *descriptor.PredictSchemaInfo {
	info := &descriptor.PredictSchemaInfo{FieldCount: len(s.Fields)}
	for _, f := range s.Fields {
		info.Fields = append(info.Fields, f.Name)
	}
	return info
}
