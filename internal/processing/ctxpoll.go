package processing

import "context"

// CtxPollInterval is how many rows a per-record loop folds between two
// cooperative context checks. A power of two, so the check is a
// counter mask rather than a modulo; a cancelled ctx stops a loop
// within this many rows.
const CtxPollInterval = 4096

// CtxPoller is the per-record loops' cooperative cancellation check:
// Poll reads ctx.Err() on the first row and then once every
// CtxPollInterval rows, so a pre-cancelled ctx stops a loop before its
// first fold and a mid-run cancel within CtxPollInterval rows. The
// error is the caller's ctx error unchanged — never a coded error; the
// caller of the loop maps an inner deadline, not this type.
//
// A value type with no allocation; build one per loop with
// NewCtxPoller.
type CtxPoller struct {
	ctx context.Context
	n   uint32
}

// NewCtxPoller returns a poller over ctx. A nil ctx never cancels.
func NewCtxPoller(ctx context.Context) CtxPoller {
	if ctx == nil {
		ctx = context.Background()
	}
	return CtxPoller{ctx: ctx}
}

// Poll counts one row and, on every CtxPollInterval-th row (the first
// included), returns ctx.Err(). Small enough to inline into the loop.
func (c *CtxPoller) Poll() error {
	n := c.n
	c.n++
	if n&(CtxPollInterval-1) != 0 {
		return nil
	}
	return c.ctx.Err()
}
