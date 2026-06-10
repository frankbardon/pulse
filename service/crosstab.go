package service

import (
	"context"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// processCrosstab is the dispatch arm of Process for requests carrying a
// Crosstab section. The orchestrator opens the cohort, applies smart
// defaults to the cell aggregation (so omitting the cell Type still
// works when the field's schema type fixes the choice), then either
// hands off to the fused streaming accumulator (when
// processing.CanFuseCrosstab accepts the request) or drains every
// filter-passing record into memory and runs the buffered
// processing.Processor.RunCrosstab pipeline.
//
// Fused vs buffered: the fused path holds O(cells + margins) memory and
// decodes the cohort exactly once; the buffered path materialises every
// filter-passing record before partitioning. Both produce byte-equal
// MatrixPayload / long-shape output for any request the gate accepts,
// asserted by TestCrosstabFused_EquivalenceVsBuffered. The gate rejects
// requests that require buffer-only finalize (AGG_MEDIAN cell, tier-1
// tests, two-pass attributes, etc.); those still take the buffered
// path here.
//
// Crosstabs are inherently buffered (matrix shape, any margin, any
// normalization → buffered) except for the degenerate "long + no
// margins + normalize=none" case, which descriptor/predict.go flags as
// streamable. That degenerate case still routes through here because
// the existing process path does not yet support multi-grouper
// composite keys for nested axes; the RunCrosstab orchestrator is the
// only place those keys are materialised today.
func (s *Service) processCrosstab(ctx context.Context, req *types.Request) (*types.Response, error) {
	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	s.applyDefaults(req, cohort.Schema())

	// Validate / inject label bindings exactly as Process does so a
	// labelled crosstab matches a labelled plain Process request.
	s.applyAutoLabels(&req.Labels, cohort.Schema(), collectOutputLabels(req), nil)
	if err := s.validateProcessLabels(req, cohort.Schema()); err != nil {
		return nil, err
	}

	// Dispatch to the fused streaming path when the gate accepts.
	// The gate is the load-bearing exclusion check — features, tier-1
	// tests, two-pass attributes, non-streamable groupers, non-online
	// cell aggregators, expression-runtime filters/attributes, and
	// opaque extension operators all fall back to the buffered path.
	if !s.disableCrosstabFusion {
		if ok, _ := processing.CanFuseCrosstab(req, cohort.Schema(), s.extensions); ok {
			return s.processCrosstabFused(ctx, cohort, path, req)
		}
	}

	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	// Crosstab always materializes the filter-passing record set. On
	// wide cohorts that materialization is the dominant memory cost.
	// Project the iterator to only the fields the request actually
	// references so each Record's value/null/wide maps allocate at
	// retained-field width instead of full schema width. Forced on
	// independent of opts.ProjectBufferedFields because the crosstab
	// path has no streamable alternative — the savings are load-bearing
	// on cohorts beyond ~50 fields.
	s.applyCrosstabProjection(iter, req, cohort.Schema())

	// Materialize every record once. Crosstab buffers by construction
	// (recursive Grouper partitioning needs the full set), so the
	// streaming iterator is consumed up-front.
	records, err := materializeRecords(iter)
	if err != nil {
		return nil, err
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}

	proc := processing.NewProcessorWithExtensions(cohort.Schema(), s.extensions)
	resp, err := proc.RunCrosstab(ctx, req, records)
	if err != nil {
		return nil, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}

	if err := s.buildAndApplyLabels(req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// materializeRecords drains an iterator into a slice. Crosstab forces
// buffered execution, so this materialization is unavoidable. Pulled out
// of processCrosstab so future variants (streamable long-shape passthrough)
// can share the helper.
func materializeRecords(iter scanIterator) ([]*processing.Record, error) {
	var records []*processing.Record
	for iter.Next() {
		records = append(records, iter.Record())
	}
	return records, nil
}
