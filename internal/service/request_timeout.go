package service

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/frankbardon/pulse/internal/limits"
)

// requestTimeoutCause is the cause the RequestTimeout deadline carries
// (context.WithTimeoutCause). Only this cause maps to
// PULSE_LIMIT_EXCEEDED: a caller's own deadline or cancel leaves
// context.Cause at the caller's error, which passes through unchanged.
type requestTimeoutCause struct {
	configured time.Duration
	start      time.Time
}

func (c *requestTimeoutCause) Error() string {
	return "pulse: request timeout " + c.configured.String() + " elapsed"
}

// BoundRequest bounds ctx by the instance's RequestTimeout for one
// top-level call. It returns ctx unchanged (and a no-op release) when
// RequestTimeout is unlimited — the default. The caller defers release
// and maps its error through MapRequestTimeout(ctx, err) with the
// RETURNED ctx. A nested entry (a Compose slot's Process, chain stage
// 0, the facade's ProcessStreamResult around ProcessStream) bounds
// again with the same duration, starting later: its deadline never
// precedes the outer one, so it keeps the outer deadline and cause —
// the outermost call owns the budget (boundAt).
func (s *Service) BoundRequest(ctx context.Context) (context.Context, context.CancelFunc) {
	bounded, release := s.boundRequest(ctx)
	if s.entryHook != nil {
		s.entryHook(bounded)
	}
	return bounded, release
}

func (s *Service) boundRequest(ctx context.Context) (context.Context, context.CancelFunc) {
	d := s.Limits().RequestTimeout
	if limits.IsUnlimited(int64(d)) {
		return ctx, func() {}
	}
	now := time.Now
	if s.clock != nil {
		now = s.clock
	}
	return boundAt(ctx, d, now())
}

// boundAt bounds ctx by d counted from start. A parent deadline NO LATER
// than start+d — a tie included — keeps the parent's deadline and cause:
// the child is a plain cancel ctx (its release still stops the entry's
// own work) with no timer of its own.
//
// The tie is why this is not context.WithDeadlineCause, which keeps the
// parent only when the parent's deadline is strictly EARLIER. A coarse
// monotonic clock (Windows reads interrupt time, which advances once
// per timer tick) gives a nested entry started in the outer entry's
// tick the identical deadline. WithDeadlineCause then installs a second
// timer for the same instant, and the two fire in either order: when
// the slot's fires first, the slot fails with the limit while the outer
// ctx has no cause yet, so the outer MapRequestTimeout passed the
// aggregate through (SERVICE_INTERNAL "compose parallel: 2/2 requests
// failed", CI run 43264135980) instead of PULSE_LIMIT_EXCEEDED
// (TODO-746).
func boundAt(ctx context.Context, d time.Duration, start time.Time) (context.Context, context.CancelFunc) {
	dl := start.Add(d)
	if cur, ok := ctx.Deadline(); ok && !cur.After(dl) {
		return context.WithCancel(ctx)
	}
	return context.WithDeadlineCause(ctx, dl, &requestTimeoutCause{configured: d, start: start})
}

// MapRequestTimeout turns err into PULSE_LIMIT_EXCEEDED {limit:
// request_timeout, configured, observed: elapsed ns} when ctx's
// RequestTimeout deadline fired; any other error — a caller's
// context.DeadlineExceeded / context.Canceled included — is returned
// unchanged. A nil err stays nil: a call that finished is a success
// even if the deadline passed after its last poll.
func MapRequestTimeout(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var c *requestTimeoutCause
	if !stderrors.As(context.Cause(ctx), &c) {
		return err
	}
	return limits.Exceeded(limits.RequestTimeout, int64(c.configured), int64(time.Since(c.start)))
}
