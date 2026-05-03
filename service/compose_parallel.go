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

	o := opts.resolved()
	n := len(composed.Requests)

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

	for i, req := range composed.Requests {
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
			return nil, fmt.Errorf("compose parallel: request %d: %w", failed[0], firstErr)
		}
		details := map[string]any{"failed_indices": failed, "first_error": firstErr.Error()}
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_INTERNAL,
			fmt.Sprintf("compose parallel: %d/%d requests failed", len(failed), n),
			details)
	}

	return responses, nil
}
