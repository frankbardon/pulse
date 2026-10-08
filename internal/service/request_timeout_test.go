package service

import (
	"context"
	stderrors "errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// requestTimeoutRows is the fixture size: far past
// processing.CtxPollInterval, so every polled loop reaches a poll.
const requestTimeoutRows = 20_000

// waitOutShortDeadlines is the deterministic stand-in for a slow run:
// an entry whose ctx carries a deadline under a minute away blocks until
// that deadline fires, so the run starts with a done ctx and its first
// poll trips. A 1h RequestTimeout (or no deadline) never blocks.
func waitOutShortDeadlines(ctx context.Context) {
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < time.Minute {
		<-ctx.Done()
	}
}

func withRequestTimeout(d time.Duration) limits.Limits {
	l := limits.Defaults()
	l.RequestTimeout = d
	return l
}

func timeoutSum() *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "left.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
	}
}

func timeoutCompose() *types.ComposedRequest {
	return &types.ComposedRequest{Requests: []*types.Request{timeoutSum(), timeoutSum()}}
}

// timeoutEntries runs every RequestTimeout entry once against svc.
func timeoutEntries(svc *Service) map[string]func(ctx context.Context) error {
	return map[string]func(ctx context.Context) error{
		"Process": func(ctx context.Context) error {
			resp, err := svc.Process(ctx, timeoutSum())
			if err != nil && resp != nil {
				return stderrors.New("partial response with an error")
			}
			return err
		},
		"ProcessStream": func(ctx context.Context) error {
			it, err := svc.ProcessStream(ctx, timeoutSum())
			if it != nil {
				_ = it.Close()
			}
			return err
		},
		"Facet": func(ctx context.Context) error {
			_, err := svc.Facet(ctx, "left.pulse", "v")
			return err
		},
		"FacetSchema": func(ctx context.Context) error {
			_, err := svc.FacetSchema(ctx, &types.FacetRequest{Cohort: &types.Cohort{Filename: "left.pulse"}, Fields: []string{"v"}})
			return err
		},
		"Compose": func(ctx context.Context) error {
			_, err := svc.Compose(ctx, timeoutCompose())
			return err
		},
		"ComposeParallel/FailFast": func(ctx context.Context) error {
			_, err := svc.ComposeParallel(ctx, timeoutCompose(), ComposeOptions{FailFast: true})
			return err
		},
		"ComposeParallel/CollectAll": func(ctx context.Context) error {
			_, err := svc.ComposeParallel(ctx, timeoutCompose(), ComposeOptions{})
			return err
		},
		"ProcessChain": func(ctx context.Context) error {
			_, err := svc.ProcessChain(ctx, &types.ChainRequest{
				Cohort: &types.Cohort{Filename: "left.pulse"},
				Stages: []*types.ChainStage{{Request: timeoutSum()}},
			})
			return err
		},
	}
}

func isLimitCode(err error) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) && ce.Code == errors.PULSE_LIMIT_EXCEEDED
}

// TestRequestTimeout_TripsOnEveryEntry: the inner RequestTimeout
// deadline maps to PULSE_LIMIT_EXCEEDED {limit: request_timeout,
// configured, observed: elapsed} on every entry FR-23 names — the whole
// Compose / ComposeParallel (both FailFast modes) / chain call included.
func TestRequestTimeout_TripsOnEveryEntry(t *testing.T) {
	svc := ctxPollFixture(t, requestTimeoutRows, 10)
	const d = time.Millisecond
	svc.SetLimits(withRequestTimeout(d))
	svc.entryHook = waitOutShortDeadlines
	for name, run := range timeoutEntries(svc) {
		t.Run(name, func(t *testing.T) {
			err := run(context.Background())
			// The whole-call trip is the entry's own error, not a slot-
			// or stage-located wrapper around one.
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
				t.Fatalf("err = %#v, want a bare PULSE_LIMIT_EXCEEDED", err)
			}
			if ce.Details["limit"] != string(limits.RequestTimeout) || ce.Details["configured"] != int64(d) {
				t.Fatalf("details = %v, want limit=request_timeout configured=%d", ce.Details, int64(d))
			}
			if obs, _ := ce.Details["observed"].(int64); obs < int64(d) {
				t.Fatalf("observed %d is under the configured %d", obs, int64(d))
			}
			if stderrors.Is(err, context.DeadlineExceeded) {
				t.Fatal("limit error still unwraps to context.DeadlineExceeded")
			}
		})
	}
}

// TestRequestTimeout_CallerDeadlinePassesThrough: a caller deadline
// that fires first — under a 1h RequestTimeout — returns
// context.DeadlineExceeded, never the limit code.
func TestRequestTimeout_CallerDeadlinePassesThrough(t *testing.T) {
	svc := ctxPollFixture(t, requestTimeoutRows, 10)
	svc.SetLimits(withRequestTimeout(time.Hour))
	svc.entryHook = waitOutShortDeadlines
	for name, run := range timeoutEntries(svc) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			defer cancel()
			err := run(ctx)
			if isLimitCode(err) || !stderrors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}

// TestRequestTimeout_CallerCancelPassesThrough: a caller cancel returns
// context.Canceled under a set RequestTimeout.
func TestRequestTimeout_CallerCancelPassesThrough(t *testing.T) {
	svc := ctxPollFixture(t, requestTimeoutRows, 10)
	svc.SetLimits(withRequestTimeout(time.Hour))
	for name, run := range timeoutEntries(svc) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := run(ctx)
			if isLimitCode(err) || !stderrors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}
}

// TestRequestTimeout_DefaultAddsNoDeadline: the default (unlimited)
// RequestTimeout hands every entry the caller's ctx untouched — no
// deadline — and the run succeeds.
func TestRequestTimeout_DefaultAddsNoDeadline(t *testing.T) {
	svc := ctxPollFixture(t, requestTimeoutRows, 10)
	var seen atomic.Int64
	svc.entryHook = func(ctx context.Context) {
		seen.Add(1)
		if _, ok := ctx.Deadline(); ok {
			t.Error("default RequestTimeout installed a deadline")
		}
	}
	for name, run := range timeoutEntries(svc) {
		if err := run(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if seen.Load() == 0 {
		t.Fatal("entry hook never ran")
	}
}

// TestRequestTimeout_NestedEntriesShareOneBudget: the outermost entry
// owns the budget — every nested entry (a Compose slot's Process, a
// chain stage, ProcessStream's Process) sees that call's deadline, not
// a fresh one, and the count proves the outer entry bounded at all.
func TestRequestTimeout_NestedEntriesShareOneBudget(t *testing.T) {
	svc := ctxPollFixture(t, requestTimeoutRows, 10)
	svc.SetLimits(withRequestTimeout(time.Hour))
	entries := timeoutEntries(svc)
	for name, want := range map[string]int{
		"Process": 1, "ProcessStream": 1, "Facet": 1, "FacetSchema": 1,
		"Compose": 3, "ComposeParallel/FailFast": 3, "ComposeParallel/CollectAll": 3,
		"ProcessChain": 2,
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var deadlines []time.Time
			svc.entryHook = func(ctx context.Context) {
				dl, ok := ctx.Deadline()
				if !ok {
					t.Error("a set RequestTimeout left an entry unbounded")
				}
				mu.Lock()
				deadlines = append(deadlines, dl)
				mu.Unlock()
			}
			if err := entries[name](context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(deadlines) != want {
				t.Fatalf("entries = %d, want %d", len(deadlines), want)
			}
			for _, dl := range deadlines[1:] {
				if !dl.Equal(deadlines[0]) {
					t.Fatalf("nested deadline %v differs from the outer %v", dl, deadlines[0])
				}
			}
		})
	}
}

// TestRequestTimeout_PerRequestTimeoutStaysRaw: ComposeOptions'
// per-slot PerRequestTimeout is unchanged — its DeadlineExceeded is not
// relabelled as the limit, and FailFast reports the timed-out slot.
func TestRequestTimeout_PerRequestTimeoutStaysRaw(t *testing.T) {
	svc := ctxPollFixture(t, requestTimeoutRows, 10)
	svc.SetLimits(withRequestTimeout(time.Hour))
	svc.entryHook = waitOutShortDeadlines
	_, err := svc.ComposeParallel(context.Background(), timeoutCompose(),
		ComposeOptions{FailFast: true, PerRequestTimeout: time.Millisecond})
	if isLimitCode(err) || !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the per-slot context.DeadlineExceeded", err)
	}
}

// TestFailFastWinner_RequestTimeout: under a fired RequestTimeout every
// slot error is the limit (the slot's Process maps it) and the winner is
// the lowest index; a PerRequestTimeout slot beats a sibling cancel.
func TestFailFastWinner_RequestTimeout(t *testing.T) {
	c := &requestTimeoutCause{configured: time.Millisecond, start: time.Now()}
	ctx, cancel := context.WithTimeoutCause(context.Background(), time.Nanosecond, c)
	defer cancel()
	<-ctx.Done()
	lim := MapRequestTimeout(ctx, ctx.Err())
	if !isLimitCode(lim) {
		t.Fatalf("mapped = %v, want the limit code", lim)
	}
	if w := failFastWinner(ctx, []error{nil, lim, lim}, []int{1, 2}); w != 1 {
		t.Fatalf("winner = %d, want 1", w)
	}
	live := context.Background()
	errs := []error{context.Canceled, context.DeadlineExceeded}
	if w := failFastWinner(live, errs, []int{0, 1}); w != 1 {
		t.Fatalf("winner = %d, want the PerRequestTimeout slot 1", w)
	}
}

// TestMapRequestTimeout_OnlyInnerCause: a caller's deadline or cancel
// (context.Cause at the caller's error) and a nil err pass through.
func TestMapRequestTimeout_OnlyInnerCause(t *testing.T) {
	past, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := MapRequestTimeout(past, past.Err()); err != context.DeadlineExceeded {
		t.Fatalf("caller deadline mapped to %v", err)
	}
	if err := MapRequestTimeout(context.Background(), nil); err != nil {
		t.Fatalf("nil mapped to %v", err)
	}
	// A caller deadline earlier than RequestTimeout keeps the caller's
	// cause even inside a bounded ctx.
	svc := New(fs.NewMemMap())
	svc.SetLimits(withRequestTimeout(time.Hour))
	b, release := svc.BoundRequest(past)
	defer release()
	if err := MapRequestTimeout(b, b.Err()); err != context.DeadlineExceeded {
		t.Fatalf("bounded caller deadline mapped to %v", err)
	}
}

// TestRequestTimeout_TripsInsideSlots: the deadline fires while the
// slots run (the outer entry starts on a live ctx; every nested entry
// waits it out), so the slots' own errors — the limit, mapped inside
// each slot's Process — reach the Compose / ComposeParallel / chain
// aggregation, which still reports the limit code.
func TestRequestTimeout_TripsInsideSlots(t *testing.T) {
	svc := ctxPollFixture(t, requestTimeoutRows, 10)
	svc.SetLimits(withRequestTimeout(time.Millisecond))
	entries := timeoutEntries(svc)
	for _, name := range []string{"Compose", "ComposeParallel/FailFast", "ComposeParallel/CollectAll", "ProcessChain"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			svc.entryHook = func(ctx context.Context) {
				if calls.Add(1) > 1 {
					waitOutShortDeadlines(ctx)
				}
			}
			err := entries[name](context.Background())
			if calls.Load() < 2 {
				t.Fatalf("no nested entry ran (calls = %d)", calls.Load())
			}
			if !isLimitCode(err) {
				t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED", err)
			}
		})
	}
}

// TestRequestTimeout_TripsInLaterChainStage: a deadline that stage 0
// never polls (an empty cohort folds no row) trips in stage 1, which
// runs outside any Process entry — the chain call maps it itself.
func TestRequestTimeout_TripsInLaterChainStage(t *testing.T) {
	svc := ctxPollFixture(t, 0, 10)
	svc.SetLimits(withRequestTimeout(time.Millisecond))
	svc.entryHook = waitOutShortDeadlines
	_, err := svc.ProcessChain(context.Background(), &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Stages: []*types.ChainStage{
			{Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "v", Label: "n"}}}},
			{Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}}}},
		},
	})
	if _, ok := err.(*errors.CodedError); !ok || !isLimitCode(err) {
		t.Fatalf("err = %#v, want a bare PULSE_LIMIT_EXCEEDED", err)
	}
}
