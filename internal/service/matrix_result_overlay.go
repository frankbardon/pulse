package service

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// matrix_result_overlay.go places the MATRIX_RESULT overlay pass
// (processing.ApplyMatrixResultOverlays: Request.Overlays kinds that
// read Response.Matrices, OVERLAY_MATRIX_GROUP_SUMMARY first).
//
// Ordering (U28 E4-S1): the pass runs AFTER the multiplicity fold, so a
// MATRIX_RESULT overlay reads every result exactly as the caller
// receives it — auxiliary.p_adjusted / significant_adjusted and the
// MatrixResult.Multiplicity echo included. On a process request that is
// right after foldRequestMultiplicity in Service.process; a Compose
// slot's correction is the batch's barrier fold (foldComposeMultiplicity),
// so the slot's pass is DEFERRED (deferMatrixResultOverlays) and run by
// both Compose orchestrators right after that fold, before the sweep
// rank. A ProcessChain stage never carries one (descx.ChainStageOverlayRefusal),
// and the FACET / Compose / chain overlay slots refuse the kind outright.

// applyMatrixResultOverlays is the pass itself — a package variable so
// the ordering test can observe what the pass reads.
var applyMatrixResultOverlays = processing.ApplyMatrixResultOverlays

// deferredMatrixResultKey carries a Compose slot's deferred-pass cell.
type deferredMatrixResultKey struct{}

// deferMatrixResultOverlays marks ctx so the Compose slot run under it
// stores its MATRIX_RESULT pass in *cell instead of running it.
func deferMatrixResultOverlays(ctx context.Context, cell *func() error) context.Context {
	return context.WithValue(ctx, deferredMatrixResultKey{}, cell)
}

// matrixResultOverlays runs (or, inside a Compose slot, defers) the
// MATRIX_RESULT pass over resp. A request with no such overlay is a
// no-op, so resp stays byte-identical.
func (s *Service) matrixResultOverlays(ctx context.Context, req *types.Request, resp *types.Response, compute processing.ComputePlan) error {
	if !processing.AppliesMatrixResultOverlays(req, s.extensions, compute) {
		return nil
	}
	exts := s.extensions
	pass := func() error { return applyMatrixResultOverlays(req, resp, exts, compute) }
	if cell, ok := ctx.Value(deferredMatrixResultKey{}).(*func() error); ok && cell != nil {
		*cell = pass
		return nil
	}
	return pass()
}

// runDeferredMatrixResultOverlays runs every Compose slot's deferred
// MATRIX_RESULT pass in slot order; a refusal names its slot like a
// slot's own Process refusal does.
func runDeferredMatrixResultOverlays(deferred []func() error) error {
	for i, pass := range deferred {
		if pass == nil {
			continue
		}
		if err := pass(); err != nil {
			return fmt.Errorf("request %d: %w", i, locate(err, "request", i))
		}
	}
	return nil
}
