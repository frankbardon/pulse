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
// precedes the outer one, so context.WithTimeoutCause keeps the outer
// deadline and cause — the outermost call owns the budget.
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
	c := &requestTimeoutCause{configured: d, start: time.Now()}
	return context.WithTimeoutCause(ctx, d, c)
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
