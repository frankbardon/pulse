package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/observe"
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
	// RequestTimeout bounds the whole call; PerRequestTimeout stays the
	// per-slot knob, and its raw DeadlineExceeded passes through.
	ctx, release := s.BoundRequest(ctx)
	defer release()
	var slots []*types.Request
	resp, err := s.composeParallel(ctx, composed, opts, &slots)
	if err != nil {
		return nil, nil, s.scopeRefusal(MapRequestTimeout(ctx, err))
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
	if err := s.composeSlotsPreflight(composed); err != nil {
		return nil, err
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
	composeRet, err := descx.ResolveComposeReturn(composed, s.instance)
	if err != nil {
		return nil, err
	}
	foldOverlays := composeOverlaysComputed(composed, composeRet, multPlan)
	ctx = withinCompose(ctx)

	// Synthesize Label auto-defaults + collision-check on a clone of the
	// slot list before the worker pool starts; see applyComposeLabelDefaults
	// for the contract. The caller's *Request pointers are never mutated.
	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		return nil, err
	}
	*slots = requests
	// Per-slot Components veto, exactly as on the serial path.
	var vetoes []bool
	if foldOverlays {
		vetoes = composeSlotVetoes(composed.Overlays, requests)
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

	// Observability (no-ops off): the batch's planning is its plan
	// phase; each slot is a child operation timed under its own
	// carrier, so the parent's clock skips the pool (Mark).
	ei := execInfoFrom(ctx)
	ei.Lap(observe.PhasePlan)

	sem := make(chan struct{}, o.MaxWorkers)
	var wg sync.WaitGroup

	launched := 0
	for i, req := range requests {
		// Bail before launching when ctx is already cancelled.
		if runCtx.Err() != nil {
			break
		}
		launched++
		i, req := i, req
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			reqCtx := composeSlotContext(runCtx, vetoes, multPlan, i)
			if o.PerRequestTimeout > 0 {
				var reqCancel context.CancelFunc
				reqCtx, reqCancel = context.WithTimeout(reqCtx, o.PerRequestTimeout)
				defer reqCancel()
			}

			reqCtx, end := startChild(reqCtx, i, req)
			resp, err := s.Process(reqCtx, req)
			end(err)
			if err != nil {
				errs[i] = err
				triggerFailFast()
				return
			}
			responses[i] = resp
		}()
	}
	wg.Wait()
	ei.Mark()

	// Aggregate errors if any. FailFast surfaces the lowest-index error
	// that is not a sibling cancellation (failFastWinner); non-FailFast
	// wraps every error with its slot index so callers see the full
	// picture.
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
	// A done ctx that stopped the launch loop before every slot ran,
	// with no slot error to report, is the ctx's own error — never a
	// "successful" response with unrun (nil) slots.
	if firstErr == nil && launched < n {
		return nil, runCtx.Err()
	}
	if firstErr != nil {
		if o.FailFast {
			// FailFast=true + any-slot-failed: SKIP the overlay
			// barrier entirely. When ComposeOptions.FailFast is true
			// and any slot fails, overlays are skipped — no
			// applyComposeOverlays call, no partial emission. The
			// failing call returns immediately with the error that
			// tripped FailFast.
			w := failFastWinner(ctx, errs, failed)
			return nil, fmt.Errorf("compose parallel: request %d: %w", w, locate(errs[w], "request", w))
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
	var (
		layers   []types.OverlayLayer
		warnings []types.OverlayWarning
	)
	if foldOverlays {
		layers, warnings, err = s.applyComposeOverlays(ctx, composed, requests, responses)
		if err != nil {
			return nil, err
		}
		ei.Lap(observe.PhaseOverlay)
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

// failFastWinner picks the slot whose error a FailFast Compose reports:
// the lowest-index failure that is not a sibling cancellation. A slot
// that was still running when another slot's error cancelled runCtx
// stops with context.Canceled (the serial loops poll ctx); reporting
// that would hide the real failure behind its own side effect. When
// the CALLER's ctx is done every cancellation is genuine, so the
// lowest-index failure wins as before. failed is non-empty.
func failFastWinner(ctx context.Context, errs []error, failed []int) int {
	if ctx.Err() == nil {
		for _, i := range failed {
			if !stderrors.Is(errs[i], context.Canceled) {
				return i
			}
		}
	}
	return failed[0]
}
