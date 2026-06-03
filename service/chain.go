package service

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// ProcessChain executes a source-rooted linear chain of Process
// requests. The first stage runs against the cohort identified by
// req.Cohort; each subsequent stage receives the previous stage's
// rows as its input, materialised through an in-memory SliceIterator
// against a synthesised schema.
//
// All stages must pass processing.CanChainRequest (mergeable +
// scalar-emitting aggregators only). The executor returns
// PULSE_CHAIN_NOT_MERGEABLE with the offending stage index in details
// on first failure, allowing callers to fall back to per-stage
// Process. Stage 0's Request.Cohort is replaced with req.Cohort; any
// Request.Cohort on stages >= 1 is ignored.
func (s *Service) ProcessChain(ctx context.Context, req *types.ChainRequest) (*types.ChainResponse, error) {
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

	// Stage 0 runs against the on-disk cohort.
	stage0 := req.Stages[0].Request
	stage0.Cohort = req.Cohort

	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	s.applyDefaults(stage0, cohort.Schema())
	if !processing.CanChainRequest(stage0, cohort.Schema()) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_NOT_MERGEABLE,
			"chain stage 0 is not mergeable",
			map[string]any{"stage_index": 0, "stage_name": req.Stages[0].Name})
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
		return nil, err
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

		synthSchema, err := processing.ChainOutputSchema(priorReq)
		if err != nil {
			return nil, err
		}
		records, err := processing.RecordsFromChainRows(priorResp.Data, synthSchema)
		if err != nil {
			return nil, err
		}

		s.applyDefaults(stage, synthSchema)
		if !processing.CanChainRequest(stage, synthSchema) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_NOT_MERGEABLE,
				"chain stage is not mergeable",
				map[string]any{"stage_index": i, "stage_name": req.Stages[i].Name})
		}

		if s.echoRequest {
			normStages = append(normStages, &types.ChainStage{
				Name:    req.Stages[i].Name,
				Request: snapshotRequest(stage),
			})
		}

		resp, err := s.runChainStage(ctx, stage, synthSchema, records)
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
			Cohort: req.Cohort,
			Stages: normStages,
		}
	}
	return out, nil
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
func (s *Service) runChainStage(ctx context.Context, req *types.Request, schema *encoding.Schema, records []*processing.Record) (*types.Response, error) {
	iter := processing.NewSliceIterator(records)
	proc := processing.NewProcessorWithExtensions(schema, s.extensions)
	return proc.Process(ctx, req, iter)
}
