package service

import (
	"context"
	"errors"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Row is a single result row in a processing stream. Aliased so callers
// can write `service.Row` without leaking the underlying map type.
type Row = map[string]any

// RowIter is a pull-based iterator over a processing result. Next reports
// (row, true, nil) for each available row, then (nil, false, nil) on
// exhaustion. Close releases any underlying file handles.
//
// Implementations are NOT goroutine-safe. A single consumer per iterator.
type RowIter interface {
	Next(ctx context.Context) (Row, bool, error)
	Close() error
	// Metadata returns the run metadata (total/filtered row counts,
	// cohort filename). Available after the iterator is exhausted; may
	// return nil before then. Streaming consumers that need metadata
	// before draining should call Process instead.
	Metadata() *types.ResponseMetadata
	// Components returns the per-operator constituent-parts metadata
	// emitted by the run — same shape as Response.Components on a
	// buffered Process call. Available after the iterator is exhausted;
	// may return nil before then or when the run produced no components
	// payload. Streaming consumers that surface per-chunk component
	// deltas read this on terminal flush to expose the buffered-only
	// payload for non-mergeable operators (AGG_MEDIAN, AGG_PERCENTILE).
	Components() *types.ResponseComponents
}

// ProcessStream executes a request and returns a pull-based row iterator
// over the result. The semantics match Process — same gates, same errors,
// same metadata — but the consumer can drain rows incrementally instead
// of receiving the whole []map[string]any up front.
//
// Today the iterator buffers internally (calls Process and walks Data).
// The signature is forward-compatible with a future true-streaming path
// driven by the streaming Processor; consumers that adopt it now will
// pick up that change without a code change on their side.
func (s *Service) ProcessStream(ctx context.Context, req *types.Request) (RowIter, error) {
	if req == nil || req.Cohort == nil {
		return nil, pulseerrors.NewCodedError(pulseerrors.SERVICE_VALIDATION,
			"process_stream request requires a cohort")
	}
	resp, err := s.Process(ctx, req)
	if err != nil {
		return nil, err
	}
	return &bufferedRowIter{rows: resp.Data, meta: resp.Metadata, components: resp.Components}, nil
}

// bufferedRowIter walks a pre-materialized row slice. It is the trivial
// implementation behind ProcessStream until the streaming Processor path
// is wired through.
type bufferedRowIter struct {
	rows       []Row
	idx        int
	meta       *types.ResponseMetadata
	components *types.ResponseComponents
	closed     bool
}

var errIterClosed = errors.New("row iterator closed")

func (it *bufferedRowIter) Next(ctx context.Context) (Row, bool, error) {
	if it.closed {
		return nil, false, errIterClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if it.idx >= len(it.rows) {
		return nil, false, nil
	}
	row := it.rows[it.idx]
	it.idx++
	return row, true, nil
}

func (it *bufferedRowIter) Close() error {
	it.closed = true
	it.rows = nil
	return nil
}

func (it *bufferedRowIter) Metadata() *types.ResponseMetadata {
	return it.meta
}

// Components surfaces the per-operator constituent-parts metadata that
// the buffered Process call attached to the underlying Response. The
// streaming wrapper in pulse.ProcessStreamResult reads this on terminal
// flush so non-mergeable operators (AGG_MEDIAN / AGG_PERCENTILE) reach
// the consumer on the last chunk only.
func (it *bufferedRowIter) Components() *types.ResponseComponents {
	return it.components
}
