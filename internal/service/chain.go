package service

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/processing"
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
	resp, err := s.processChain(ctx, req)
	return resp, s.scopeRefusal(err)
}

func (s *Service) processChain(ctx context.Context, req *types.ChainRequest) (*types.ChainResponse, error) {
	if req == nil || len(req.Stages) == 0 {
		return nil, errors.NewCodedError(errors.PULSE_CHAIN_EMPTY, "chain request must carry at least one stage")
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
	// Every stage's multiplicity blocks resolve before the cohort opens
	// — each stage a standalone Request with its own `request` family,
	// the order ValidateChain reports in. plans is index-aligned with
	// req.Stages: stage 0 re-resolves and folds inside Process
	// (identically), so plans[0] is never folded here; every later
	// stage folds its own plan in runChainStage. No family spans
	// stages.
	plans := make([]*descx.MultiplicityPlan, len(req.Stages))
	for i, st := range req.Stages {
		plan, err := s.resolveMultiplicity(ctx, st.Request)
		if err != nil {
			return nil, locate(err, "stage", i)
		}
		plans[i] = plan
	}

	// Stage 0 runs against the on-disk cohort.
	stage0 := req.Stages[0].Request
	stage0.Cohort = req.Cohort

	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	s.applyDefaults(stage0, cohort.Schema())
	// Zones resolve before the chain gate on every stage. A joined
	// stage 0 resolves inside Process against the joined schema
	// instead (its refusal is located below).
	if len(stage0.Joins) == 0 {
		if err := s.resolveZones(stage0, cohort.Schema()); err != nil {
			return nil, locate(err, "stage", 0)
		}
	}
	if err := processing.ChainRefusal(stage0, cohort.Schema(), s.extensions, 0, req.Stages[0].Name); err != nil {
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

	firstResp, err := s.Process(ctx, stage0)
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
		// Only stage 0 may join: a later stage reads the previous
		// stage's rows, so its Joins are refused, not dropped.
		if err := mergegate.StageJoinRefusal(stage, i, req.Stages[i].Name); err != nil {
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
		if err := s.resolveZones(stage, synthSchema); err != nil {
			return nil, locate(err, "stage", i)
		}
		if err := processing.ChainRefusal(stage, synthSchema, s.extensions, i, req.Stages[i].Name); err != nil {
			return nil, err
		}
		if err := s.checkFieldRefs(stage, synthSchema); err != nil {
			return nil, locate(err, "stage", i)
		}

		if s.echoRequest {
			normStages = append(normStages, &types.ChainStage{
				Name:    req.Stages[i].Name,
				Request: snapshotRequest(stage),
			})
		}

		resp, err := s.runChainStage(ctx, stage, plans[i], synthSchema, records)
		if err != nil {
			return nil, err
		}
		out.Stages = append(out.Stages, resp)
		priorReq = stage
		priorResp = resp
	}

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
	if err := s.applyChainOverlays(req, out); err != nil {
		return nil, err
	}
	return out, nil
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
// diverge"): the stub handlers in `processing.ApplyChainOverlays`
// skip the divergence check entirely and the layer inherits the target
// stage's shape; the FIXME comment in
// `internal/processing/overlay_chain_dispatch.go` documents the deferred
// surface.
func (s *Service) applyChainOverlays(req *types.ChainRequest, out *types.ChainResponse) error {
	if len(req.Overlays) == 0 {
		// Byte-identity guarantee for overlay-free chains: the slot
		// stays nil (no make-with-zero-len allocation, no `Overlays:
		// []` empty-array marshalling).
		return nil
	}
	stageNames := make([]string, len(req.Stages))
	for i, st := range req.Stages {
		if st == nil {
			continue
		}
		stageNames[i] = st.Name
	}
	layers, warnings, err := processing.ApplyChainOverlaysWithExtensions(req.Overlays, out.Stages, stageNames, s.extensions)
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
	proc := s.newProcessor(schema, req)
	resp, err := proc.Process(ctx, req, iter)
	if err != nil {
		return nil, err
	}
	if err := foldRequestMultiplicity(plan, resp); err != nil {
		return nil, err
	}
	return resp, nil
}
