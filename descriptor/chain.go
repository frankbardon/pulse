package descriptor

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ChainValidationResult is the structured output of ValidateChain.
// Mirrors FacetValidationResult: the resolved request echoes back so
// callers can forward it after inspection, and per-stage schemas
// expose the inferred lineage for debugging.
type ChainValidationResult struct {
	// Valid mirrors envelope.Errors emptiness.
	Valid bool `json:"valid"`

	// Request echoes the input chain request unchanged.
	Request *types.ChainRequest `json:"request"`

	// SchemaInfo summarises the source cohort schema used for stage 0.
	SchemaInfo *PredictSchemaInfo `json:"schema_info,omitempty"`

	// StageSchemas exposes the inferred output schema per stage as a
	// list of field names. Index i carries the columns produced by
	// stage i (input to stage i+1). Useful for debugging chain breaks
	// when stage N+1 cannot find a field stage N was expected to emit.
	StageSchemas [][]string `json:"stage_schemas,omitempty"`

	// OverlaysSchemaDivergence lists every rejected (Ref, Target)
	// chain-overlay pair from the ValidateChain overlay walk per
	// kind-catalog-v1 PRD §I-FR-I3. Populated alongside the matching
	// PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT envelope error so LLM
	// planners can budget reshapes without re-parsing envelope details.
	// Empty (and omitted on the wire) when every chain-overlay spec
	// passes the shape-match gate.
	OverlaysSchemaDivergence []ChainOverlaySchemaDivergence `json:"overlays_schema_divergence,omitempty"`
}

// ChainOverlaySchemaDivergence carries one rejected (Ref, Target)
// chain-overlay pair from ValidateChain's overlay walk. Mirrors the
// per-spec envelope-error Details shape so wire consumers can read the
// list directly instead of stitching envelope entries together.
type ChainOverlaySchemaDivergence struct {
	// Index is the position of the offending spec in
	// ChainRequest.Overlays.
	Index int `json:"index"`

	// Kind echoes the spec's whole-chain overlay kind.
	Kind types.OverlayKind `json:"kind"`

	// RefIndex is the resolved stage index of the spec's Ref.
	RefIndex int `json:"ref_index"`

	// RefShape is the inferred OverlayShape of the Ref stage's
	// Request slot.
	RefShape types.OverlayShape `json:"ref_shape"`

	// TargetIndex is the resolved stage index of the spec's Target
	// (defaulting to the latest stage when both Target slots were
	// empty per the runtime resolver contract).
	TargetIndex int `json:"target_index"`

	// TargetShape is the inferred OverlayShape of the Target stage's
	// Request slot.
	TargetShape types.OverlayShape `json:"target_shape"`
}

// ValidateChain validates a ChainRequest against a .pulse cohort
// header + schema. It never reads record data — the no-execute
// contract holds. Errors land in the envelope as SERVICE_VALIDATION
// or PULSE_CHAIN_NOT_MERGEABLE / PULSE_CHAIN_EMPTY; each stage's
// inferred output schema is propagated forward so the next stage's
// field references can be checked.
//
// The validator does not import service/processing — predict's
// structural ban applies to the broader descriptor surface in spirit.
func ValidateChain(fileData io.ReadSeeker, req *types.ChainRequest) *Envelope {
	result := &ChainValidationResult{Valid: true, Request: req}
	env := NewEnvelope(result)

	if req == nil {
		env.AddError(string(errors.SERVICE_VALIDATION), "chain request is required", nil)
		result.Valid = false
		return env
	}
	if len(req.Stages) == 0 {
		env.AddError(string(errors.PULSE_CHAIN_EMPTY), "chain request must carry at least one stage", nil)
		result.Valid = false
		return env
	}
	if req.Cohort == nil {
		env.AddError(string(errors.SERVICE_VALIDATION), "chain request requires Cohort for stage 0", nil)
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

	current := schema
	for i, stage := range req.Stages {
		if stage == nil || stage.Request == nil {
			env.AddError(string(errors.PULSE_CHAIN_EMPTY),
				"chain stage requires a non-nil Request",
				map[string]any{"stage_index": i})
			continue
		}
		if !chainGateOK(stage.Request, env, i, stage.Name) {
			continue
		}
		validateChainStageFields(stage.Request, current, env, i, stage.Name)
		next := chainPredictedOutputFields(stage.Request)
		result.StageSchemas = append(result.StageSchemas, next)
		current = synthChainSchema(stage.Request)
	}

	// Whole-chain overlay walk (E6-S7). Runs after the per-stage gate so
	// stage-level errors and overlay-level errors land in the same
	// envelope; the walk is a no-op when req.Overlays is empty.
	validateChainOverlays(env, result, req)

	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return env
}

// ValidateChainFromBytes is a convenience wrapper that creates a
// reader from bytes.
func ValidateChainFromBytes(data []byte, req *types.ChainRequest) *Envelope {
	return ValidateChain(bytes.NewReader(data), req)
}

// chainGateOK checks that a stage's operator set fits the v1 chain
// gate. Adds errors directly into the envelope and returns true iff
// the stage passed; downstream field validation can run only on a
// gate-passing stage.
func chainGateOK(req *types.Request, env *Envelope, idx int, name string) bool {
	details := map[string]any{"stage_index": idx, "stage_name": name}
	if len(req.Aggregations) == 0 {
		env.AddError(string(errors.PULSE_CHAIN_NOT_MERGEABLE),
			"chain stage requires at least one aggregator", details)
		return false
	}
	if len(req.Windows) > 0 || len(req.Features) > 0 ||
		len(req.Regressions) > 0 || len(req.Tests) > 0 ||
		len(req.PostTests) > 0 {
		env.AddError(string(errors.PULSE_CHAIN_NOT_MERGEABLE),
			"chain gate excludes windows, features, tests, post-tests, and regressions",
			details)
		return false
	}
	for _, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		switch attr.Type {
		case types.ATTR_FORMULA, types.ATTR_DATE_PART:
			// row-local
		default:
			env.AddError(string(errors.PULSE_CHAIN_NOT_MERGEABLE),
				"chain gate excludes two-pass attributes (ZSCORE / TSCORE / NORMALIZED / REG_*)",
				details)
			return false
		}
	}
	for _, g := range req.Groups {
		if g != nil && !g.Type.Mergeable() {
			env.AddError(string(errors.PULSE_CHAIN_NOT_MERGEABLE),
				"chain gate requires mergeable grouper (GROUP_CATEGORY or GROUP_RANGE)",
				details)
			return false
		}
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		if !agg.Type.Mergeable() {
			env.AddError(string(errors.PULSE_CHAIN_NOT_MERGEABLE),
				"chain gate requires mergeable aggregators",
				details)
			return false
		}
		if agg.Type == types.AGG_FREQUENCY || agg.Type == types.AGG_MODE {
			env.AddError(string(errors.PULSE_CHAIN_NOT_MERGEABLE),
				"chain gate excludes AGG_FREQUENCY and AGG_MODE (non-scalar emit)",
				details)
			return false
		}
	}
	return true
}

// validateChainStageFields confirms that every field reference in a
// stage resolves against the current input schema. Adds errors for
// unknown fields. Mirrors the validation surface in Predict but
// scoped to chain-relevant slots.
func validateChainStageFields(req *types.Request, in *encoding.Schema, env *Envelope, idx int, name string) {
	check := func(field string) {
		if field == "" || in.Field(field) != nil {
			return
		}
		env.AddError(string(errors.SERVICE_VALIDATION),
			"chain stage references unknown field",
			map[string]any{"stage_index": idx, "stage_name": name, "field": field})
	}
	for _, f := range req.Filterers {
		if f == nil {
			continue
		}
		check(f.Field)
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		check(agg.Field)
	}
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		check(g.Field)
	}
	for _, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		check(attr.Field)
	}
}

// chainPredictedOutputFields names the output columns a chain stage
// produces. Mirrors processing.ChainOutputSchema's layout without
// importing processing.
func chainPredictedOutputFields(req *types.Request) []string {
	out := make([]string, 0, len(req.Groups)+len(req.Aggregations))
	seen := make(map[string]struct{})
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		if _, dup := seen[g.Field]; dup {
			continue
		}
		seen[g.Field] = struct{}{}
		out = append(out, g.Field)
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		label := agg.Label
		if label == "" {
			label = string(agg.Type) + "_" + agg.Field
		}
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return out
}

// synthChainSchema produces the inferred input schema for the next
// stage. Categorical_u32 for grouper keys, f64 for aggregator outputs.
// Empty dictionaries — ValidateChain never reads records.
func synthChainSchema(req *types.Request) *encoding.Schema {
	fields := make([]encoding.Field, 0, len(req.Groups)+len(req.Aggregations))
	seen := make(map[string]struct{})
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		if _, dup := seen[g.Field]; dup {
			continue
		}
		seen[g.Field] = struct{}{}
		fields = append(fields, encoding.Field{
			Name:       g.Field,
			Type:       encoding.FieldTypeCategoricalU32,
			Dictionary: encoding.NewDictionary(),
		})
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		label := agg.Label
		if label == "" {
			label = string(agg.Type) + "_" + agg.Field
		}
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}
		fields = append(fields, encoding.Field{Name: label, Type: encoding.FieldTypeF64})
	}
	return &encoding.Schema{Fields: fields}
}
