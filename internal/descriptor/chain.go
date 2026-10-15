package descriptor

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
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

	// OverlaysSchemaDivergence lists every shape-divergent (Ref, Target)
	// chain-overlay pair from the ValidateChain overlay walk per
	// kind-catalog-v1 PRD §I-FR-I3. Populated alongside the matching
	// PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT envelope WARNING (the
	// runtime answers that pair with an empty layer) so LLM
	// planners can budget reshapes without re-parsing envelope details.
	// Empty (and omitted on the wire) when every chain-overlay spec
	// passes the shape-match gate.
	OverlaysSchemaDivergence []ChainOverlaySchemaDivergence `json:"overlays_schema_divergence,omitempty"`
}

// ChainOverlaySchemaDivergence carries one shape-divergent (Ref,
// Target) chain-overlay pair from ValidateChain's overlay walk. Mirrors
// the per-spec envelope-warning Details shape so wire consumers can read the
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
// (FieldRefRefusals), weights, then the Process-predict request body
// (validateRequestBodyAt — operators, accepted types, the stage's own
// overlays and labels), each zone, field and body refusal tagged
// details.stage (a joined stage 0: gate, the join-count rule, the
// join-key rule, zones, then field references against the joined
// schema). The gate,
// the field checks and stage-schema propagation run on the defaulted
// stage, as the runtime does. A nil opts is ValidateChain.
//
// opts.Strict promotes every warning the chain collected (stage 0's
// PULSE_VECTOR_METRIC_UNSUITED, a whole-chain overlay's
// PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT) into an error at the chain
// envelope (finishPredictRoot); opts.EchoRequest echoes the normalized
// chain on env.Request (chainEcho).
func ValidateChainWithOptions(fileData io.ReadSeeker, req *types.ChainRequest, opts *PredictOptions) *descriptor.Envelope {
	if opts == nil {
		opts = &PredictOptions{}
	}
	var (
		expanded *types.ChainRequest
		staged   []*types.Request
	)
	env := validateChainWithOptions(fileData, req, opts, &expanded, &staged)
	result := env.Data.(*ChainValidationResult)
	finishPredictRoot(env, opts, func() { result.Valid = false }, func() any {
		if e := chainEcho(req, expanded, staged); e != nil {
			return e
		}
		return nil
	})
	return env
}

// chainEcho is a chain predict's normalized request — the form the
// runtime echoes as ChainResponse.NormalizedRequest: the cohort, every
// stage (name kept) as it executes — item for_each expanded, smart
// defaults resolved against the stage's input schema — wherever
// predict reached it, else as written, and the whole-chain overlays.
// Nil for a nil request.
func chainEcho(req, expanded *types.ChainRequest, staged []*types.Request) *types.ChainRequest {
	if req == nil {
		return nil
	}
	src := req
	if expanded != nil {
		src = expanded
	}
	out := &types.ChainRequest{Cohort: req.Cohort, Overlays: req.Overlays}
	if len(src.Stages) > 0 {
		out.Stages = make([]*types.ChainStage, len(src.Stages))
	}
	for i, st := range src.Stages {
		if st == nil {
			continue
		}
		stage := &types.ChainStage{Name: st.Name, Request: st.Request}
		if i < len(staged) && staged[i] != nil {
			stage.Request = staged[i]
		}
		out.Stages[i] = stage
	}
	return out
}

// validateChainWithOptions is ValidateChainWithOptions before the
// Strict and echo pass; expandedOut receives the for_each-expanded
// chain and stagedOut each stage's defaults-resolved request
// (index-aligned).
func validateChainWithOptions(fileData io.ReadSeeker, req *types.ChainRequest, opts *PredictOptions, expandedOut **types.ChainRequest, stagedOut *[]*types.Request) *descriptor.Envelope {
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
	// A slot the instance hides is an unknown field, refused before
	// anything else — the runtime's order.
	if serr := SlotRefusal(req, opts.instance()); serr != nil {
		addCodedError(env, serr)
		result.Valid = false
		return env
	}
	// Then every stage's item for_each expands (the runtime's order,
	// before the cohort opens); a refusal is located by details.stage.
	// Every pass below judges the expanded stages; result.Request stays
	// as written.
	expanded, xerr := ExpandChainItems(req, opts.instance().Limits())
	if xerr != nil {
		addCodedError(env, xerr)
		result.Valid = false
		return env
	}
	req = expanded
	*expandedOut = expanded
	// Then every stage's `models` — the runtime's model.CheckChainModels
	// and its model-file reads, before the cohort opens; nothing past it
	// runs on a refusal.
	stageModels, serr := loadChainModels(req.Stages, opts)
	if serr != nil {
		addCodedError(env, serr)
		result.Valid = false
		return env
	}
	// A MATRIX_RESULT overlay on a stage's own Request.Overlays — the
	// runtime's ChainStageOverlayRefusal, at the same point.
	if cerr := ChainStageOverlayRefusal(req.Stages, opts.instance()); cerr != nil {
		addCodedError(env, cerr)
		result.Valid = false
		return env
	}
	// Multiplicity per stage — each stage is a standalone Request with
	// its own `request` family (ResolveMultiplicity), located by stage.
	for i, st := range req.Stages {
		if st == nil {
			continue
		}
		if _, merr := ResolveMultiplicity(st.Request, opts.DefaultMultiplicity, opts.instance()); merr != nil {
			addCodedError(env, RefusalAt(merr, "stage", i))
			result.Valid = false
		}
		// The stage's `return` block (ResolveReturn), resolved beside
		// its multiplicity before the cohort opens, as the runtime does.
		if _, rerr := ResolveReturn(st.Request, opts.instance()); rerr != nil {
			addCodedError(env, RefusalAt(rerr, "stage", i))
			result.Valid = false
		}
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
	// stagedReqs is every stage as the runtime executes it (defaults
	// resolved), index-aligned with req.Stages; the whole-chain overlay
	// walk resolves its params.field labels against it.
	stagedReqs := make([]*types.Request, len(req.Stages))
	*stagedOut = stagedReqs
	// stage0Schema is the schema stage 0 executes over (the joined one
	// for a joined stage 0) — the whole-chain categorical-code rule's
	// stage-0 input types.
	var stage0Schema *encoding.Schema
	// pendingColumnErr: the column collision in the previous stage's
	// synthesised output (chainStageColumnRefusal), raised at the stage
	// that reads it.
	var pendingColumnErr *errors.CodedError
	for i, stage := range req.Stages {
		if stage == nil || stage.Request == nil {
			env.AddError(string(errors.PULSE_CHAIN_EMPTY),
				"chain stage requires a non-nil Request",
				map[string]any{"stage_index": i})
			continue
		}
		// The runtime applies smart defaults to every stage against
		// its input schema (for a joined stage 0 the JOINED schema,
		// joinedDefaultsSchema — else an untyped aggregation over a
		// right-side field stays untyped) BEFORE the chain gate, so the
		// gate, the field checks, the next stage's synthesised schema
		// (whose aggregator labels embed the Type) and the whole-chain
		// overlays' label resolution all see the defaulted stage. The
		// echoed Request stays as written.
		// Only stage 0 may join (the runtime refuses a later stage's
		// Joins before its defaults, zones and gate).
		if jerr := mergegate.StageJoinRefusal(stage.Request, i, stage.Name); jerr != nil {
			addCodedError(env, jerr)
		}
		// Only stage 0 may carry matrices (the runtime refuses right
		// after the join rule); a refused stage is not judged further.
		stageMatErr := mergegate.StageMatrixRefusal(stage.Request, i, stage.Name)
		if stageMatErr != nil {
			addCodedError(env, stageMatErr)
		}
		// The previous stage's output columns collide: the runtime
		// refuses building this stage's input schema.
		if pendingColumnErr != nil {
			addCodedError(env, pendingColumnErr)
			pendingColumnErr = nil
		}
		// A joined stage 0 resolves zones inside Process — after the
		// gate and the join-count rule; every other stage resolves
		// before the gate.
		joined := i == 0 && len(stage.Request.Joins) > 0
		// The schema the stage's defaults and gate read: its input
		// schema, or for a joined stage 0 the joined one.
		gateSchema := current
		if joined {
			gateSchema = joinedDefaultsSchema(stage.Request, current, opts)
		}
		staged := chainStageDefaulted(stage.Request, gateSchema, opts)
		stagedReqs[i] = staged
		if i == 0 {
			stage0Schema = gateSchema
		}
		// fieldsReached: the runtime gets as far as the field-reference
		// rule (no earlier located refusal on this stage).
		fieldsReached := true
		if !joined {
			if _, zerr := resolveRequestZones(stage.Request, current, opts); zerr != nil {
				addCodedError(env, RefusalAt(zerr, "stage", i))
				fieldsReached = false
			}
		}
		gerr := mergegate.ChainRefusal(staged, gateSchema, snap.mergeFacts(opts.instance()), i, stage.Name)
		if gerr == nil {
			// The runtime's next check: an array-valued aggregation may
			// not feed a later stage.
			gerr = mergegate.StageArrayValuedRefusal(staged, i, len(req.Stages), stage.Name, aggregationKinds(opts.instance(), snap))
		}
		if gerr != nil {
			addCodedError(env, gerr)
		}
		gateOK := gerr == nil && stageMatErr == nil
		// The schema the stage's field references are judged against:
		// its input schema, or for a joined stage 0 the joined schema
		// (nil — no judgement — when the right side is unreadable).
		fieldSchema, fieldReq := current, staged
		if joined {
			fieldSchema = nil
			if jerr := JoinCountRefusal(stage.Request); jerr != nil {
				addCodedError(env, RefusalAt(jerr, "stage", i))
				fieldsReached = false
			} else if merr := mergegate.MatrixRefusal(stage.Request); merr != nil {
				addCodedError(env, RefusalAt(merr, "stage", i))
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
		// Stage 0 runs Process, which warns on an explicit metric that
		// fits its vector's kind poorly — and under Strict refuses the
		// stage with it before any record is read (a later stage runs
		// no such check). Located details.stage; the root's Strict pass
		// promotes it.
		if i == 0 {
			addMetricUnsuitedAt(env, stage.Request, gateSchema, "stage", i)
		}
		// A stage 0 that does not join runs Process, which applies the
		// matrix-host rule (a crosstab carrying matrices) before its
		// field references.
		if i == 0 && !joined && fieldsReached {
			if merr := mergegate.MatrixRefusal(stage.Request); merr != nil {
				addCodedError(env, RefusalAt(merr, "stage", i))
				fieldsReached = false
			}
		}
		if fieldsReached {
			// A later stage's overlays judge a forwarded code column (an
			// upstream code-emitting aggregation's output, f64 here) as
			// the code it is — the runtime's ScopedChainStageFieldRefRefusal.
			codes := aggregationKinds(opts.instance(), snap).ChainCodeColumns(stagedReqs, i, stage0Schema, chainSynth(aggregationKinds(opts.instance(), snap)))
			refs := fieldRefRefusalsCoded(fieldReq, fieldSchema, codes, extensionsFromOpts(opts), opts.instance(), stageModels[i])
			for _, ce := range refs {
				addCodedError(env, RefusalAt(ce, "stage", i))
			}
			// `return` data columns, right after the field references
			// (the runtime's checkFieldRefs order).
			colsOK := true
			if len(refs) == 0 {
				if cerr := ReturnColumnRefusal(fieldReq, fieldSchema, opts.instance()); cerr != nil {
					addCodedError(env, RefusalAt(cerr, "stage", i))
					colsOK = false
				}
			}
			// The runtime resolves the stage's weights right after its
			// field references pass (ResolveWeights).
			if len(refs) == 0 && colsOK {
				if _, werr := ResolveWeights(fieldReq, fieldSchema, opts.DefaultWeight, opts.instance()); werr != nil {
					addCodedError(env, RefusalAt(werr, "stage", i))
				} else if ce := formulaExpressionRefusal(fieldReq, opts); ce != nil {
					// The attribute constructors run next: ATTR_FORMULA's
					// empty expression (params.expression is not read).
					addCodedError(env, RefusalAt(ce, "stage", i))
				} else if fieldSchema != nil {
					// Then the Process-predict request body (#254): the
					// operator construction, accepted types, the stage's
					// own overlays and its label bindings (judged over
					// the stage's input schema — a later stage's labels
					// are applied, as stage 0's are), located by stage.
					validateRequestBodyAt(env, fieldReq, fieldSchema, opts, "stage", i)
				}
			}
		}
		next := chainPredictedOutputFields(staged, gateSchema, aggregationKinds(opts.instance(), snap))
		result.StageSchemas = append(result.StageSchemas, next)
		// The runtime synthesises this stage's output schema only when a
		// later stage reads it, raising a column collision there (after
		// that stage's join and matrix rules).
		pendingColumnErr = chainStageColumnRefusal(staged, gateSchema, aggregationKinds(opts.instance(), snap))
		current = synthChainSchema(staged, gateSchema, aggregationKinds(opts.instance(), snap))
	}

	// Whole-chain overlay walk. Runs after the per-stage gate so
	// stage-level errors and overlay-level errors land in the same
	// envelope; the walk is a no-op when req.Overlays is empty.
	validateChainOverlays(env, result, req, stagedReqs, stage0Schema, opts)

	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return env
}

// loadChainModels is the runtime chain pre-pass's model half:
// model.CheckChainModels, then every stage's model files read (located
// details.stage). It returns each stage's catalog, index-aligned.
func loadChainModels(stages []*types.ChainStage, opts *PredictOptions) ([]model.Catalog, error) {
	if cerr := model.CheckChainModels(stages, opts.populations()); cerr != nil {
		return nil, cerr
	}
	out := make([]model.Catalog, len(stages))
	for i, st := range stages {
		if st == nil || st.Request == nil {
			continue
		}
		refs := model.RequestRefs(st.Request)
		files, cerr := opts.loadModelFiles(refs)
		if cerr != nil {
			return nil, RefusalAt(cerr, "stage", i)
		}
		out[i] = loadedCatalog(opts.populations(), refs, files)
	}
	return out, nil
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

// joinedDefaultsSchema is the schema a joined chain stage 0's smart
// defaults and chain gate read — the runtime's
// Service.chainStage0Schema: the JOINED schema when the stage carries
// exactly one JoinSpec whose right side's schema loads and joins
// without a field collision, else base (Process then raises the join's
// own refusal). The join keys are not judged here: the joined schema
// does not depend on them, and the key refusal surfaces after the gate
// on both sides.
func joinedDefaultsSchema(req *types.Request, base *encoding.Schema, opts *PredictOptions) *encoding.Schema {
	if base == nil || req == nil || len(req.Joins) != 1 || req.Joins[0] == nil || opts == nil || opts.SchemaLoader == nil {
		return base
	}
	right, err := opts.SchemaLoader(req.Joins[0].Right)
	if err != nil {
		return base
	}
	joined, err := encx.JoinedSchema(base, right, req.Joins[0].As)
	if err != nil {
		return base
	}
	return joined
}

// chainPredictedOutputFields names the output columns a chain stage
// produces over its input schema in: the fields of synthChainSchema, in
// order — processing.ChainOutputSchema's layout without importing
// processing; an expanding vector aggregation names its per-element
// columns (vectors.AggregationOutputColumns — the runtime row writer's
// names), which a later stage reads as f64 columns.
func chainPredictedOutputFields(req *types.Request, in *encoding.Schema, kinds vectors.AggregationKinds) []string {
	schema := synthChainSchema(req, in, kinds)
	out := make([]string, len(schema.Fields))
	for i := range schema.Fields {
		out[i] = schema.Fields[i].Name
	}
	return out
}

// synthChainSchema produces the inferred input schema for the stage
// after req, which executes over in: vectors.ChainStageSchema — the
// builder processing.ChainOutputSchema shares (categorical_u32 group
// key columns, one f64 per aggregation output column, an expanding
// vector aggregation's element columns included). A column collision
// is reported by chainStageColumnRefusal; here the column is listed
// once. Empty dictionaries — ValidateChain never reads records.
func synthChainSchema(req *types.Request, in *encoding.Schema, kinds vectors.AggregationKinds) *encoding.Schema {
	schema, _ := vectors.ChainStageSchema(req, in, kinds)
	return schema
}

// chainStageColumnRefusal is the runtime's processing.ChainOutputSchema
// refusal: an aggregation column of req (executing over in) colliding
// with an earlier column of its synthesised output. The runtime raises
// it only when a later stage reads that output.
func chainStageColumnRefusal(req *types.Request, in *encoding.Schema, kinds vectors.AggregationKinds) *errors.CodedError {
	_, cerr := vectors.ChainStageSchema(req, in, kinds)
	return cerr
}

// chainSynth is synthChainSchema under kinds, as the next-schema
// builder vectors.AggregationKinds.ChainCodeColumns threads.
func chainSynth(kinds vectors.AggregationKinds) func(*types.Request, *encoding.Schema) *encoding.Schema {
	return func(r *types.Request, in *encoding.Schema) *encoding.Schema { return synthChainSchema(r, in, kinds) }
}
