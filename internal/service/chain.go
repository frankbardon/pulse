package service

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// ProcessChain executes a source-rooted linear chain of Process
// requests. The first stage runs against the cohort identified by
// req.Cohort; each subsequent stage receives the previous stage's
// rows as its input, materialised through an in-memory SliceIterator
// against a synthesised schema.
//
// All stages must pass processing.ChainRefusal (mergeable +
// scalar-emitting aggregators only — internal/mergegate, the rule the
// chain validator shares). The executor returns
// PULSE_CHAIN_NOT_MERGEABLE with the offending stage index in details
// on first failure, allowing callers to fall back to per-stage
// Process; a stage >= 1 carrying Joins is PULSE_CHAIN_STAGE_JOIN. Stage 0's Request.Cohort is replaced with req.Cohort; any
// Request.Cohort on stages >= 1 is ignored.
func (s *Service) ProcessChain(ctx context.Context, req *types.ChainRequest) (*types.ChainResponse, error) {
	ctx, release := s.BoundRequest(ctx)
	defer release()
	resp, err := s.processChain(ctx, req)
	return resp, s.scopeRefusal(MapRequestTimeout(ctx, err))
}

func (s *Service) processChain(ctx context.Context, req *types.ChainRequest) (*types.ChainResponse, error) {
	if req == nil || len(req.Stages) == 0 {
		return nil, errors.NewCodedError(errors.PULSE_CHAIN_EMPTY, "chain request must carry at least one stage")
	}
	if err := limits.CheckChainStages(s.Limits(), len(req.Stages)); err != nil {
		return nil, err
	}
	if req.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "chain request requires Cohort for stage 0")
	}
	for i, st := range req.Stages {
		if st == nil || st.Request == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_EMPTY,
				"chain stage requires a non-nil Request",
				map[string]any{"stage_index": i})
		}
	}
	// Hidden slots — the chain root's own, then every stage's (stage 0
	// included, ahead of the chain gate) — are refused before the cohort
	// opens (details.stage locates a stage's).
	if err := s.slotRefusal(req); err != nil {
		return nil, err
	}
	// A whole-chain overlay over a stage carrying an array-valued
	// (vector) aggregation, before any stage runs (ValidateChain's
	// rule).
	if err := descx.ChainOverlayArrayValuedRefusal(req, s.instance, nil); err != nil {
		return nil, err
	}
	// Then every stage's item for_each expands in place (the expansion
	// ValidateChain runs), before the cohort opens.
	for i, st := range req.Stages {
		if err := s.expandItems(st.Request); err != nil {
			return nil, locate(markLocated(err), "stage", i)
		}
	}
	// Then every stage's `models` (model.CheckChainModels — the rule
	// PredictChain runs) and every model file read, before the cohort
	// opens and any stage runs; each stage gets its own bound set.
	if cerr := model.CheckChainModels(req.Stages); cerr != nil {
		return nil, cerr
	}
	stageSets := make([]*model.Set, len(req.Stages))
	for i, st := range req.Stages {
		set, err := s.loadModels(nil, st.Request.Models)
		if err != nil {
			return nil, descx.RefusalAt(err, "stage", i)
		}
		stageSets[i] = set
	}
	// Every stage's multiplicity blocks resolve before the cohort opens
	// — each stage a standalone Request with its own `request` family,
	// the order ValidateChain reports in. plans is index-aligned with
	// req.Stages: stage 0 re-resolves and folds inside Process
	// (identically), so plans[0] is never folded here; every later
	// stage folds its own plan in runChainStage. No family spans
	// stages.
	plans := make([]*descx.MultiplicityPlan, len(req.Stages))
	rets := make([]*returnplan.Plan, len(req.Stages))
	for i, st := range req.Stages {
		plan, err := s.resolveMultiplicity(ctx, st.Request)
		if err != nil {
			return nil, locate(err, "stage", i)
		}
		plans[i] = plan
		// The stage's `return` block resolves beside it (the pass
		// ValidateChain runs per stage). It is APPLIED by the facade
		// only after the whole chain finished, so a later stage always
		// reads the unshaped rows; stage 0 re-resolves inside Process.
		ret, err := descx.ResolveReturn(st.Request, s.instance)
		if err != nil {
			return nil, descx.RefusalAt(err, "stage", i)
		}
		rets[i] = ret
	}

	// Stage 0 runs against the on-disk cohort.
	stage0 := req.Stages[0].Request
	stage0.Cohort = req.Cohort

	// Observability (no-ops off): the chain's own planning and cohort
	// open are its plan and open phases; each stage is a child operation
	// timed under its own carrier, so the parent's clock skips them.
	ei := execInfoFrom(ctx)
	ei.Lap(observe.PhasePlan)
	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	ei.Lap(observe.PhaseOpen)

	// Stage 0's defaults and chain gate read its input schema — for a
	// joined stage 0 the JOINED one (chainStage0Schema), the schema
	// Process executes it against. Against the bare cohort an untyped
	// aggregation over a right-side field stayed untyped: the gate
	// refused it, and the overlays and the next stage could not name
	// its default TYPE_field label.
	gateSchema := s.chainStage0Schema(ctx, stage0, cohort.Schema())
	s.applyDefaults(stage0, gateSchema)
	// Zones resolve before the chain gate on every stage. A joined
	// stage 0 resolves inside Process against the joined schema
	// instead (its refusal is located below).
	if len(stage0.Joins) == 0 {
		// Validation only: Process re-resolves stage 0 and runs it zoned.
		if _, err := s.resolveZones(stage0, cohort.Schema()); err != nil {
			return nil, locate(err, "stage", 0)
		}
	}
	if err := processing.ChainRefusal(stage0, gateSchema, s.extensions, 0, req.Stages[0].Name); err != nil {
		return nil, err
	}
	if err := mergegate.StageArrayValuedRefusal(stage0, 0, len(req.Stages), req.Stages[0].Name, s.extensions.AggregationKinds()); err != nil {
		return nil, err
	}

	// Capture the post-defaults form of every stage when echo is on so
	// the boundary (CLI / MCP) can publish a normalized request alongside
	// the response. The capture is a snapshot taken *after* applyDefaults
	// so it reflects exactly what the engine ran.
	var normStages []*types.ChainStage
	if s.echoRequest {
		normStages = make([]*types.ChainStage, 0, len(req.Stages))
		normStages = append(normStages, &types.ChainStage{
			Name:    req.Stages[0].Name,
			Request: snapshotRequest(stage0),
		})
	}

	// A chain overlay reads its stages' data and crosstab payload only
	// (never skipped), so it vetoes no stage's `return` skip; stage 0
	// resolves its own compute plan inside Process.
	ei.Lap(observe.PhasePlan)
	stageCtx, end := startChild(withModelsBound(ctx, stageSets[0]), 0, stage0)
	firstResp, err := s.Process(stageCtx, stage0)
	end(err)
	if err != nil {
		return nil, locate(err, "stage", 0)
	}

	out := &types.ChainResponse{Stages: make([]*types.Response, 0, len(req.Stages))}
	out.Stages = append(out.Stages, firstResp)

	// Subsequent stages run against synthesised schemas + in-memory
	// SliceIterators. Each loop iteration: build the prior stage's
	// output schema, materialise records, apply defaults, validate
	// the gate, run processor.
	priorReq := stage0
	priorResp := firstResp
	for i := 1; i < len(req.Stages); i++ {
		stage := req.Stages[i].Request
		stage.Cohort = nil // chain stages >= 1 do not name a cohort
		resp, err := s.runChainStageChild(withModelsBound(ctx, stageSets[i]), req, i, stage, priorReq, priorResp, gateSchema, plans[i], rets[i], &normStages)
		if err != nil {
			return nil, err
		}
		out.Stages = append(out.Stages, resp)
		priorReq = stage
		priorResp = resp
	}
	ei.Mark()

	if len(out.Stages) > 0 {
		out.Final = out.Stages[len(out.Stages)-1]
	}
	if s.echoRequest {
		out.NormalizedRequest = &types.ChainRequest{
			Cohort:   req.Cohort,
			Stages:   normStages,
			Overlays: req.Overlays,
		}
	}
	// Whole-chain overlay barrier. Runs AFTER every stage has finalised
	// and BEFORE the response is returned to the caller. Per-stage
	// `Stages[i].Overlays` (populated via the per-stage
	// `Request.Overlays` slot) is left UNTOUCHED — the whole-chain
	// barrier reads finalised `*Response` objects only and emits its own
	// `ChainResponse.Overlays` slice keyed to `req.Overlays` in matching
	// index order. Empty / nil `req.Overlays` short-circuits with no
	// allocation (byte-identical JSON for overlay-free chains).
	// A whole-chain overlay scoring a categorical code (an input-valued
	// aggregation over a categorical / packed_bool input — the input
	// schema decides, so it is judged here, once every stage ran with
	// its defaults resolved: stage 0 against gateSchema, a later stage
	// against the schema synthesised from the stage before it). The
	// same call ValidateChain makes.
	if err := descx.ChainOverlayArrayValuedRefusal(req, s.instance, gateSchema); err != nil {
		return nil, err
	}
	if err := s.applyChainOverlays(req, out); err != nil {
		return nil, err
	}
	if len(req.Overlays) > 0 {
		ei.Lap(observe.PhaseOverlay)
	}
	return out, nil
}

// chainStage0Schema is the schema a chain stage 0's smart defaults and
// chain gate read: left, or — when the stage carries exactly one
// JoinSpec whose right side opens (header only, no record) and joins
// without a field collision — the joined schema processWithJoin runs
// it against. Any failure falls back to left, so Process raises the
// join's own refusal where it always did. The join keys are not judged
// here (the joined schema does not depend on them). Predict's twin is
// internal/descriptor.joinedDefaultsSchema.
func (s *Service) chainStage0Schema(ctx context.Context, stage0 *types.Request, left *encoding.Schema) *encoding.Schema {
	if len(stage0.Joins) != 1 || stage0.Joins[0] == nil {
		return left
	}
	right, err := s.Open(ctx, stage0.Joins[0].Right)
	if err != nil {
		return left
	}
	joined, err := processing.JoinedSchema(left, right.Schema(), stage0.Joins[0])
	if err != nil {
		return left
	}
	return joined
}

// requestInputSchema is the schema r executes over: its cohort's, or
// for a request carrying one JoinSpec the joined one
// (chainStage0Schema); nil when r names no cohort or it does not open.
// The Compose categorical-code overlay rule reads it; predict's twin is
// internal/descriptor.composeSlotInputSchema.
func (s *Service) requestInputSchema(ctx context.Context, r *types.Request) *encoding.Schema {
	if r == nil || r.Cohort == nil {
		return nil
	}
	cohort, err := s.Open(ctx, resolveCohortPath(r.Cohort))
	if err != nil {
		return nil
	}
	return s.chainStage0Schema(ctx, r, cohort.Schema())
}

// runChainStageChild runs chain stage i (i >= 1) over the previous
// stage's rows as a child operation: startChild gives it its own
// observability carrier, which it stamps with the serial arm the stage
// ran and the stage's row counters (no-ops off).
// stage0Schema is the schema stage 0 executed over (its gate schema):
// the root the stage's inherited code columns are judged from.
func (s *Service) runChainStageChild(ctx context.Context, req *types.ChainRequest, i int, stage, priorReq *types.Request, priorResp *types.Response, stage0Schema *encoding.Schema, plan *descx.MultiplicityPlan, ret *returnplan.Plan, normStages *[]*types.ChainStage) (resp *types.Response, err error) {
	ctx, end := startChild(ctx, i, stage)
	defer func() { end(err) }()
	// Only stage 0 may join: a later stage reads the previous
	// stage's rows, so its Joins are refused, not dropped.
	if err := mergegate.StageJoinRefusal(stage, i, req.Stages[i].Name); err != nil {
		return nil, err
	}
	// Likewise only stage 0 may carry matrices: a later stage's rows
	// are the previous stage's output, not the cohort.
	if err := mergegate.StageMatrixRefusal(stage, i, req.Stages[i].Name); err != nil {
		return nil, err
	}

	synthSchema, err := processing.ChainOutputSchema(priorReq)
	if err != nil {
		return nil, err
	}
	records, err := processing.RecordsFromChainRows(priorResp.Data, synthSchema)
	if err != nil {
		return nil, err
	}

	s.applyDefaults(stage, synthSchema)
	zones, err := s.resolveZones(stage, synthSchema)
	if err != nil {
		return nil, locate(err, "stage", i)
	}
	if err := processing.ChainRefusal(stage, synthSchema, s.extensions, i, req.Stages[i].Name); err != nil {
		return nil, err
	}
	if err := mergegate.StageArrayValuedRefusal(stage, i, len(req.Stages), req.Stages[i].Name, s.extensions.AggregationKinds()); err != nil {
		return nil, err
	}
	// The stage's overlays judge a forwarded code column (an upstream
	// code-emitting aggregation's output, f64 in synthSchema) as the
	// code it is — the code columns ValidateChain threads, from the one
	// shared rule over this side's ChainOutputSchema.
	codes := s.extensions.AggregationKinds().ChainCodeColumns(chainStageRequests(req), i, stage0Schema, chainOutputSchemaOrNil)
	if err := s.checkChainStageFieldRefs(ctx, stage, synthSchema, codes); err != nil {
		return nil, locate(err, "stage", i)
	}
	if err := s.limitsPreflight(stage, synthSchema, descx.LimitInputs{
		Records:        int64(len(records)),
		JoinRightRows:  -1,
		Extensions:     s.ExtensionsSnapshot(),
		FusionDisabled: s.disableCrosstabFusion,
	}); err != nil {
		return nil, locate(err, "stage", i)
	}

	if s.echoRequest {
		*normStages = append(*normStages, &types.ChainStage{
			Name:    req.Stages[i].Name,
			Request: snapshotRequest(stage),
		})
	}

	// The stage's own compute plan: what its `return` excludes is
	// never built. Its data — the next stage's input — has no skip.
	ei := execInfoFrom(ctx)
	ei.Lap(observe.PhasePlan)
	stageCtx := withComputePlan(ctx, s.resolveComputePlan(ctx, stage, ret, plan))
	resp, err = s.runChainStage(stageCtx, s.zoned(stage, zones), plan, synthSchema, records)
	if err != nil {
		return nil, err
	}
	ei.addRows(resp)
	return resp, nil
}

// applyChainOverlays is the post-stage-loop barrier hook. Invokes
// processing.ApplyChainOverlays against the materialised stage
// responses and populates `out.Overlays` in spec-order. Returns nil
// when `req.Overlays` is empty so the wire form stays byte-identical
// to the overlay-free ChainResponse shape.
//
// Per FR-F1 ("whole-chain overlay fold operates exclusively on
// already-materialised *Response objects; no record re-traversal"):
// the hook reads only the already-finalised `*Response` objects this
// function received from the stage loop; nothing reaches back to the
// per-stage iterators or the source cohort.
//
// Per FR-F2 ("PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT raises at
// runtime here when target stage and reference stage host shapes
// diverge"): processing.ApplyChainOverlaysWithRequests judges each
// stage's host shape off its AUTHORED request (the shape predict-chain
// reads) before the handler runs, and the handlers keep their own
// Response-shape check behind it. A divergence is an empty layer that
// inherits the target stage's shape plus ONE warning whose Details
// carry predict's keys (index, kind, target/ref index + shape,
// stage_index, stage_name) and overlay_index.
func (s *Service) applyChainOverlays(req *types.ChainRequest, out *types.ChainResponse) error {
	if len(req.Overlays) == 0 {
		// Byte-identity guarantee for overlay-free chains: the slot
		// stays nil (no make-with-zero-len allocation, no `Overlays:
		// []` empty-array marshalling).
		return nil
	}
	stageNames := make([]string, len(req.Stages))
	// Each stage's Request as it ran (smart defaults resolved in place
	// by the stage loop): a SERIES stage keys on its group columns and
	// scores the aggregation params.field names.
	stageReqs := make([]*types.Request, len(req.Stages))
	for i, st := range req.Stages {
		if st == nil {
			continue
		}
		stageNames[i] = st.Name
		stageReqs[i] = st.Request
	}
	layers, warnings, err := processing.ApplyChainOverlaysWithRequests(req.Overlays, out.Stages, stageNames, stageReqs, s.extensions)
	if err != nil {
		return err
	}
	if len(layers) == 0 {
		// Defense in depth: a non-empty spec slice that produced no
		// layers (e.g. the handler returned an empty slice for some
		// future kind) leaves the slot nil rather than allocating an
		// empty slice. Today's stub always produces one layer per
		// spec, so this branch is unreachable in v1.
		return nil
	}
	out.Overlays = make([]*types.OverlayLayer, len(layers))
	copy(out.Overlays, layers)
	// Distribute the flat warning slice into the matching layer's
	// `Warnings` slot. The dispatcher (processing.ApplyChainOverlays)
	// stamps `Details["overlay_index"]` on every warning it appends so
	// routing is a single lookup per warning; warnings missing the key
	// (defensive — should never happen with the current dispatcher) fall
	// back to layer 0 so they are still surfaced rather than silently
	// dropped. Layers with no warnings keep `Warnings == nil` (no empty
	// slice allocation) so the `omitempty` JSON tag preserves byte
	// identity for the overlay-free path. The Compose-host barrier
	// mirrors this convention so a shared helper can factor both paths
	// later.
	for i := range warnings {
		layerIdx := 0
		if warnings[i].Details != nil {
			if v, ok := warnings[i].Details["overlay_index"]; ok {
				if idx, ok := v.(int); ok && idx >= 0 && idx < len(out.Overlays) {
					layerIdx = idx
				}
			}
		}
		layer := out.Overlays[layerIdx]
		if layer == nil {
			continue
		}
		layer.Warnings = append(layer.Warnings, warnings[i])
	}
	return nil
}

// snapshotRequest returns a value-level copy of req with the same
// aggregator and grouper deep-clone the predict path uses. Used by
// ProcessChain to capture per-stage normalized requests without holding
// a reference to the live (still-executing) Request struct.
func snapshotRequest(req *types.Request) *types.Request {
	if req == nil {
		return nil
	}
	clone := *req
	if len(req.Aggregations) > 0 {
		clone.Aggregations = make([]*types.Aggregation, len(req.Aggregations))
		for i, a := range req.Aggregations {
			if a == nil {
				continue
			}
			ac := *a
			clone.Aggregations[i] = &ac
		}
	}
	if len(req.Groups) > 0 {
		clone.Groups = make([]*types.Group, len(req.Groups))
		for i, g := range req.Groups {
			if g == nil {
				continue
			}
			gc := *g
			clone.Groups[i] = &gc
		}
	}
	return &clone
}

// runChainStage runs the processor against an in-memory record set
// constructed from the prior stage's output. The synthesised schema
// has no on-wire byte layout — it exists purely to satisfy field
// lookups and dictionary resolution in operators downstream.
//
// A later stage bypasses Service.Process, so it carries the
// multiplicity post-hook itself: plan (the stage's own resolution) is
// folded into the stage's response once the processor has applied the
// stage's own Request.Overlays — a per-stage `request` family exactly
// as a standalone Process of the stage request would correct it. A
// nil or inactive plan is a no-op (byte-identical output).
func (s *Service) runChainStage(ctx context.Context, req *types.Request, plan *descx.MultiplicityPlan, schema *encoding.Schema, records []*processing.Record) (*types.Response, error) {
	iter := processing.NewSliceIterator(records)
	proc := s.newProcessor(ctx, schema, req)
	resp, err := proc.Process(ctx, req, iter)
	if err != nil {
		return nil, err
	}
	execInfoFrom(ctx).setPlan(pathArm(proc.LastPath() == processing.PathStreaming), 1, 0, 0)
	if err := foldRequestMultiplicity(plan, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// chainStageRequests is req's stages' requests, index-aligned (as
// executed: the stage loop resolves each stage's defaults in place).
func chainStageRequests(req *types.ChainRequest) []*types.Request {
	out := make([]*types.Request, len(req.Stages))
	for i, st := range req.Stages {
		if st != nil {
			out[i] = st.Request
		}
	}
	return out
}

// chainOutputSchemaOrNil is processing.ChainOutputSchema with a refusal
// as nil (no code judgement past it; the stage loop raises the refusal
// itself).
func chainOutputSchemaOrNil(r *types.Request) *encoding.Schema {
	out, err := processing.ChainOutputSchema(r)
	if err != nil {
		return nil
	}
	return out
}
