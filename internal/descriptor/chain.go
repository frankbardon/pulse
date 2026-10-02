package descriptor

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mergegate"
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
	SchemaInfo *descriptor.PredictSchemaInfo `json:"schema_info,omitempty"`

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
// The validator does not import internal/service or processing — predict's
// structural ban applies to the broader descriptor surface in spirit.
//
// ValidateChain knows built-in operators only: it is
// ValidateChainWithExtensions with a nil snapshot, so every
// embedder-registered name fails the chain gate.
func ValidateChain(fileData io.ReadSeeker, req *types.ChainRequest) *descriptor.Envelope {
	return ValidateChainWithExtensions(fileData, req, nil)
}

// ValidateChainWithExtensions is ValidateChain against an
// ExtensionsSnapshot, the predict-side twin of the runtime gate
// processing.CanChainRequestWithExtensions: an extension aggregator or
// grouper passes the chain gate on its DECLARED Mergeable flag
// (OperatorMeta.Mergeable), a row_local extension attribute as a
// row-local operator. The snapshot carries the declarations, so the
// no-execute ban holds (TestPredictNoExecutionImports). A nil snap is
// exactly ValidateChain.
func ValidateChainWithExtensions(fileData io.ReadSeeker, req *types.ChainRequest, snap *ExtensionsSnapshot) *descriptor.Envelope {
	return ValidateChainWithOptions(fileData, req, &PredictOptions{Extensions: snap})
}

// ValidateChainWithOptions is ValidateChain with the predict options in
// reach: opts.Extensions is the chain-gate snapshot, and
// opts.DefaultTimeZone / ZoneLoader / DisableDefaults / SchemaLoader
// drive the per-stage zone resolution — the runtime's order: defaults,
// zones, the chain gate, then the field-reference rule
// (FieldRefRefusals), each zone and field refusal tagged details.stage
// (a joined stage 0: gate, the join-count rule, the join-key rule,
// zones, then field references against the joined schema). The gate,
// the field checks and stage-schema propagation run on the defaulted
// stage, as the runtime does. A nil opts is ValidateChain.
func ValidateChainWithOptions(fileData io.ReadSeeker, req *types.ChainRequest, opts *PredictOptions) *descriptor.Envelope {
	if opts == nil {
		opts = &PredictOptions{}
	}
	snap := opts.Extensions
	result := &ChainValidationResult{Valid: true, Request: req}
	env := descriptor.NewEnvelope(result)

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

	pulseVersion, err := encoding.ReadHeader(fileData)
	if err != nil {
		env.AddError(string(headerErrorCode(err)), "invalid pulse file header: "+err.Error(), nil)
		result.Valid = false
		return env
	}
	schema, err := encoding.ReadSchema(fileData, pulseVersion)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse schema: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	result.SchemaInfo = &descriptor.PredictSchemaInfo{FieldCount: len(schema.Fields)}
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
		// The runtime applies smart defaults to every stage against
		// its input schema (the cohort's for stage 0, even when it
		// joins) BEFORE the chain gate, so the gate, the field checks
		// and the next stage's synthesised schema (whose aggregator
		// labels embed the Type) all see the defaulted stage. The
		// echoed Request stays as written.
		// Only stage 0 may join (the runtime refuses a later stage's
		// Joins before its defaults, zones and gate).
		if jerr := mergegate.StageJoinRefusal(stage.Request, i, stage.Name); jerr != nil {
			addCodedError(env, jerr)
		}
		staged := chainStageDefaulted(stage.Request, current, opts)
		// A joined stage 0 resolves zones inside Process — after the
		// gate and the join-count rule; every other stage resolves
		// before the gate.
		joined := i == 0 && len(stage.Request.Joins) > 0
		// fieldsReached: the runtime gets as far as the field-reference
		// rule (no earlier located refusal on this stage).
		fieldsReached := true
		if !joined {
			if _, zerr := resolveRequestZones(stage.Request, current, opts); zerr != nil {
				addCodedError(env, RefusalAt(zerr, "stage", i))
				fieldsReached = false
			}
		}
		gerr := mergegate.ChainRefusal(staged, current, snap.mergeFacts(opts.instance()), i, stage.Name)
		if gerr != nil {
			addCodedError(env, gerr)
		}
		gateOK := gerr == nil
		// The schema the stage's field references are judged against:
		// its input schema, or for a joined stage 0 the joined schema
		// (nil — no judgement — when the right side is unreadable).
		fieldSchema, fieldReq := current, staged
		if joined {
			fieldSchema = nil
			if jerr := JoinCountRefusal(stage.Request); jerr != nil {
				addCodedError(env, RefusalAt(jerr, "stage", i))
				fieldsReached = false
			} else if js, keyRefusals := validatorRequestSchema(stage.Request, current, opts); len(keyRefusals) > 0 {
				for _, ce := range keyRefusals {
					addCodedError(env, RefusalAt(ce, "stage", i))
				}
				fieldsReached = false
			} else if _, zerr := resolveRequestZones(stage.Request, js, opts); zerr != nil {
				addCodedError(env, RefusalAt(zerr, "stage", i))
				fieldsReached = false
			} else if js != nil {
				// Process re-applies defaults against the joined schema.
				fieldSchema, fieldReq = js, chainStageDefaulted(staged, js, opts)
			}
		}
		if !gateOK {
			continue
		}
		if fieldsReached {
			for _, ce := range fieldRefRefusals(fieldReq, fieldSchema, extensionsFromOpts(opts), opts.instance()) {
				addCodedError(env, RefusalAt(ce, "stage", i))
			}
		}
		next := chainPredictedOutputFields(staged)
		result.StageSchemas = append(result.StageSchemas, next)
		current = synthChainSchema(staged)
	}

	// Whole-chain overlay walk. Runs after the per-stage gate so
	// stage-level errors and overlay-level errors land in the same
	// envelope; the walk is a no-op when req.Overlays is empty.
	validateChainOverlays(env, result, req, opts)

	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return env
}

// ValidateChainFromBytes is a convenience wrapper that creates a
// reader from bytes.
func ValidateChainFromBytes(data []byte, req *types.ChainRequest) *descriptor.Envelope {
	return ValidateChain(bytes.NewReader(data), req)
}

// chainStageDefaulted is the stage the runtime gates and executes: a
// clone with the shared smart-defaults pass (ResolveDefaults — the
// one the runtime's applyDefaults calls) run against the stage's input
// schema, unless opts.DisableDefaults. The caller's request is never
// mutated.
func chainStageDefaulted(req *types.Request, in *encoding.Schema, opts *PredictOptions) *types.Request {
	clone := cloneRequestForDefaults(req)
	if !opts.DisableDefaults && in != nil {
		ResolveDefaults(clone, in, opts.Instance)
	}
	return clone
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
