package service

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ComposeOptions controls parallel execution of a ComposedRequest.
//
// Order of responses is always preserved — slot-by-index — regardless of
// MaxWorkers or completion order.
type ComposeOptions struct {
	// MaxWorkers caps concurrent in-flight Process calls. Zero means
	// runtime.GOMAXPROCS(0). Negative values are clamped to 1.
	MaxWorkers int

	// PerRequestTimeout, if positive, derives a context.WithTimeout for
	// each request. Zero means no per-request timeout (the parent ctx's
	// deadline still applies).
	PerRequestTimeout time.Duration

	// FailFast cancels in-flight siblings on the first request error.
	// Default is true: surface errors quickly. Set false to collect every
	// request's outcome (errors aggregated into a single CodedError with
	// per-index detail).
	FailFast bool
}

// resolvedOptions returns a copy with defaults applied.
func (o ComposeOptions) resolved() ComposeOptions {
	out := o
	if out.MaxWorkers == 0 {
		out.MaxWorkers = runtime.GOMAXPROCS(0)
	}
	if out.MaxWorkers < 1 {
		out.MaxWorkers = 1
	}
	return out
}

// ComposeParallel runs every request in composed concurrently across a
// bounded worker pool. Responses are returned in the same order as
// composed.Requests; per-request errors are surfaced according to opts.
//
// Registry factories return fresh stateful instances per request, so
// concurrent Process calls do not share aggregator/attribute state. Geo
// and decimal aggregators dispatch through buffered code paths that are
// also safe for concurrent invocation (no shared mutable state).
func (s *Service) ComposeParallel(
	ctx context.Context,
	composed *types.ComposedRequest,
	opts ComposeOptions,
) ([]*types.Response, error) {
	if composed == nil || len(composed.Requests) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"composed request must contain at least one request")
	}

	// Synthesize Label auto-defaults + collision-check on a clone of the
	// slot list before the worker pool starts; see applyComposeLabelDefaults
	// for the contract. The caller's *Request pointers are never mutated.
	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		return nil, err
	}

	o := opts.resolved()
	n := len(requests)

	// Per-slot result and error storage; never shared across slots so no
	// inter-slot synchronization is required beyond the slot itself.
	responses := make([]*types.Response, n)
	errs := make([]error, n)

	// Cancellation context: derived once so FailFast can fan out cancellation
	// to every in-flight goroutine via cancel().
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// failOnce ensures FailFast triggers cancel() exactly once, even if
	// multiple goroutines fail concurrently.
	var failOnce sync.Once
	triggerFailFast := func() {
		if o.FailFast {
			failOnce.Do(cancel)
		}
	}

	sem := make(chan struct{}, o.MaxWorkers)
	var wg sync.WaitGroup

	for i, req := range requests {
		// Bail before launching when ctx is already cancelled.
		if runCtx.Err() != nil {
			break
		}
		i, req := i, req
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			reqCtx := runCtx
			if o.PerRequestTimeout > 0 {
				var reqCancel context.CancelFunc
				reqCtx, reqCancel = context.WithTimeout(runCtx, o.PerRequestTimeout)
				defer reqCancel()
			}

			resp, err := s.Process(reqCtx, req)
			if err != nil {
				errs[i] = err
				triggerFailFast()
				return
			}
			responses[i] = resp
		}()
	}
	wg.Wait()

	// Aggregate errors if any. FailFast surfaces the first observed error
	// (lowest-index winner); non-FailFast wraps every error with its slot
	// index so callers see the full picture.
	var firstErr error
	var failed []int
	for i, e := range errs {
		if e != nil {
			failed = append(failed, i)
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	if firstErr != nil {
		if o.FailFast {
			// FailFast=true + any-slot-failed: SKIP the overlay
			// barrier entirely (per E7-S4 acceptance "when
			// ComposeOptions.FailFast = true [...] and any slot
			// fails, overlays are skipped entirely — no
			// applyComposeOverlays call, no partial emission"). The
			// failing call returns immediately with the first
			// observed error.
			return nil, fmt.Errorf("compose parallel: request %d: %w", failed[0], firstErr)
		}
		details := map[string]any{"failed_indices": failed, "first_error": firstErr.Error()}
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_INTERNAL,
			fmt.Sprintf("compose parallel: %d/%d requests failed", len(failed), n),
			details)
	}

	// Compose-only overlay barrier (E7-S4). Runs AFTER the worker
	// pool drains and all per-slot results are gathered into the
	// order-preserved `responses` slice. The parallel path here only
	// reaches this barrier when EITHER every slot succeeded (FailFast
	// = true or false; firstErr is nil) OR FailFast=false collected
	// some slot failures and the orchestrator chose to surface them
	// as aggregated details (the early-return branch above caught
	// the FailFast=true case). The order of layer emission matches
	// `req.Overlays` spec order regardless of slot dispatch order
	// because `responses` is keyed by slot index, not completion
	// order.
	//
	// Facade rewire deferred to E7-S15: the layers + warnings are
	// computed (so the hook surface is exercised end-to-end) but the
	// facade still returns []*Response, so the values are discarded.
	// E7-S15 lifts the return type to *ComposedResponse{Responses,
	// Overlays} and persists both slots.
	if _, _, err := s.applyComposeOverlays(ctx, composed, requests, responses); err != nil {
		return nil, err
	}

	return responses, nil
}
