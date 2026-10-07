package service

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
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
) (*types.ComposedResponse, error) {
	resp, _, err := s.ComposeParallelResolved(ctx, composed, opts)
	return resp, err
}

// ComposeParallelResolved is ComposeParallel that also returns the slot
// requests the responses ran (see ComposeResolved).
func (s *Service) ComposeParallelResolved(
	ctx context.Context,
	composed *types.ComposedRequest,
	opts ComposeOptions,
) (*types.ComposedResponse, []*types.Request, error) {
	var slots []*types.Request
	resp, err := s.composeParallel(ctx, composed, opts, &slots)
	if err != nil {
		return nil, nil, s.scopeRefusal(err)
	}
	return resp, slots, nil
}

func (s *Service) composeParallel(
	ctx context.Context,
	composed *types.ComposedRequest,
	opts ComposeOptions,
	slots *[]*types.Request,
) (*types.ComposedResponse, error) {
	if composed == nil || len(composed.Requests) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"composed request must contain at least one request")
	}
	// Hidden slots are refused before the worker pool starts, exactly
	// as on the serial path.
	if err := s.slotRefusal(composed); err != nil {
		return nil, err
	}
	multPlan, err := s.resolveComposeMultiplicity(composed)
	if err != nil {
		return nil, err
	}
	// The Compose-level `return`, exactly as on the serial path.
	if _, err := descx.ResolveComposeReturn(composed, s.instance); err != nil {
		return nil, err
	}
	ctx = withinCompose(ctx)
	// A Compose overlay reads its slots' Components after they run, so
	// no slot `return` may skip them (conservative: any overlay keeps
	// every slot's Components).
	if len(composed.Overlays) > 0 {
		ctx = withComponentsVeto(ctx)
	}

	// Synthesize Label auto-defaults + collision-check on a clone of the
	// slot list before the worker pool starts; see applyComposeLabelDefaults
	// for the contract. The caller's *Request pointers are never mutated.
	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		return nil, err
	}
	*slots = requests

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
			// barrier entirely. When ComposeOptions.FailFast is true
			// and any slot fails, overlays are skipped — no
			// applyComposeOverlays call, no partial emission. The
			// failing call returns immediately with the first
			// observed error.
			return nil, fmt.Errorf("compose parallel: request %d: %w", failed[0], locate(firstErr, "request", failed[0]))
		}
		details := map[string]any{"failed_indices": failed, "first_error": firstErr.Error()}
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_INTERNAL,
			fmt.Sprintf("compose parallel: %d/%d requests failed", len(failed), n),
			details)
	}

	// Compose-only overlay barrier. Runs AFTER the worker
	// pool drains and all per-slot results are gathered into the
	// order-preserved `responses` slice. The parallel path only
	// reaches this barrier when every slot succeeded: any slot
	// failure returns above (FailFast=true with the first error,
	// FailFast=false with the aggregated SERVICE_INTERNAL), so the
	// overlay and multiplicity folds never see a partial set of
	// slots. The order of layer emission matches
	// `req.Overlays` spec order regardless of slot dispatch order
	// because `responses` is keyed by slot index, not completion
	// order.
	layers, warnings, err := s.applyComposeOverlays(ctx, composed, requests, responses)
	if err != nil {
		return nil, err
	}

	// Build the ComposedResponse wrapper. Overlay-free composes leave
	// `Overlays == nil` (no make-with-zero-len allocation, no
	// `overlays: []` empty-array marshalling) so the byte-identity
	// contract locked by TestComposedResponse_OverlayFreeByteIdentical
	// (types/types_test.go:1106) is preserved when the caller declared
	// no Compose-only overlays. Warning fold delegated to
	// `distributeComposeWarnings` (internal/service/compose_overlay.go) so the
	// serial `service.Compose` and the parallel path here share the
	// identical layer-warning routing contract.
	out := &types.ComposedResponse{Responses: responses}
	if len(layers) > 0 {
		out.Overlays = distributeComposeWarnings(layers, warnings)
	}
	// The multiplicity barrier fold: every slot and every Compose-host
	// layer has finished, so the `compose` family pools them all and
	// each slot's own families correct inside the slot. Shared with the
	// other orchestrator, so serial and parallel answer identically.
	if err := foldComposeMultiplicity(multPlan, out); err != nil {
		return nil, err
	}

	return out, nil
}
