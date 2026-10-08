package pulse

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// observedRowIter is the RowIter ProcessStream returns when
// observability is on. It ends the operation exactly once — on
// exhaustion or a failed Next, on Close, or when the call's ctx is
// cancelled, whichever comes first — and times the scan phase from the
// first Next to that end. It forwards the shaped iterator's Returned
// and MarshalRow, so wrapping never changes what a consumer reads or
// writes.
type observedRowIter struct {
	inner   RowIter
	exec    *service.ExecInfo
	end     func(error)
	stop    func() bool
	started atomic.Bool
	once    sync.Once
}

func newObservedRowIter(ctx context.Context, inner RowIter, end func(error)) *observedRowIter {
	it := &observedRowIter{inner: inner, exec: service.ExecInfoFrom(ctx), end: end}
	it.stop = context.AfterFunc(ctx, func() { it.finish(ctx.Err()) })
	return it
}

// finish ends the operation once: the scan phase closes when a Next
// ever ran, the cancel watch is released, and the end fires.
func (it *observedRowIter) finish(err error) {
	it.once.Do(func() {
		if it.started.Load() {
			it.exec.Lap(observe.PhaseScan)
		}
		it.stop()
		it.end(err)
	})
}

// Next forwards to the inner iterator; exhaustion or an error ends the
// operation.
func (it *observedRowIter) Next(ctx context.Context) (Row, bool, error) {
	if it.started.CompareAndSwap(false, true) {
		it.exec.Mark()
	}
	row, ok, err := it.inner.Next(ctx)
	if err != nil || !ok {
		it.finish(err)
	}
	return row, ok, err
}

// Close closes the inner iterator and ends the operation (a no-op end
// when it already ended).
func (it *observedRowIter) Close() error {
	err := it.inner.Close()
	it.finish(nil)
	return err
}

func (it *observedRowIter) Metadata() *types.ResponseMetadata { return it.inner.Metadata() }

func (it *observedRowIter) Components() *types.ResponseComponents { return it.inner.Components() }

// Returned forwards the shaped iterator's selection marker (nil for an
// unshaped stream, as before wrapping).
func (it *observedRowIter) Returned() *types.ReturnedMarker {
	if m, ok := it.inner.(interface{ Returned() *types.ReturnedMarker }); ok {
		return m.Returned()
	}
	return nil
}

// MarshalRow writes a row exactly as the inner iterator would
// (returnshape.MarshalStreamRow).
func (it *observedRowIter) MarshalRow(row map[string]any) ([]byte, error) {
	return returnshape.MarshalStreamRow(it.inner, row)
}
