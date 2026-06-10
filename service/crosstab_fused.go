package service

import (
	"context"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// processCrosstabFused is the streaming arm of processCrosstab. It runs
// the same setup as the buffered path — open cohort, apply defaults,
// resolve labels, install the crosstab projection — and then drives a
// processing.FusedCrosstabState through the streaming iterator one
// record at a time. No record buffering; per-cell state grows
// proportional to the matrix's row × column key cardinality, not the
// cohort row count.
//
// Caller contract: processCrosstab MUST gate this dispatch through
// processing.CanFuseCrosstab(req, cohort.Schema(), s.extensions). The
// caller has already opened the cohort and applied defaults/labels,
// but those steps are cheap and idempotent — this method re-issues
// them defensively only via the iterator-projection pass below so a
// future caller that wants to invoke the fused path standalone (CLI
// fused-only flag, internal benchmarks) still gets the same setup.
//
// Output contract: byte-equal to processCrosstab's buffered RunCrosstab
// output for any request CanFuseCrosstab accepts. The fused path's
// equivalence to the buffered path is enforced by
// TestCrosstabFused_EquivalenceVsBuffered below.
func (s *Service) processCrosstabFused(ctx context.Context, cohort *Cohort, path string, req *types.Request) (*types.Response, error) {
	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	// Install the same projection the buffered crosstab path installs.
	// The retained set is computed from the request via NeededFields
	// (extended for crosstab Rows/Columns/Cell + label fields); the
	// fused path's per-record decode budget depends on this projection
	// to deliver the win on wide cohorts.
	s.applyCrosstabProjection(iter, req, cohort.Schema())

	proc := processing.NewProcessorWithExtensions(cohort.Schema(), s.extensions)
	resp, err := proc.RunCrosstabFused(ctx, req, iter)
	if err != nil {
		return nil, err
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}

	if err := s.buildAndApplyLabels(req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}
