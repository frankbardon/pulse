package processing

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// cancelIter wraps a SliceIterator, counts every row it yields (across
// Reset, so a multi-pass loop keeps counting) and cancels its ctx as it
// yields row cancelAt. cancelAt <= 0 never cancels.
type cancelIter struct {
	*SliceIterator
	cancel   context.CancelFunc
	cancelAt int
	yielded  int
}

func (c *cancelIter) Next() bool {
	if !c.SliceIterator.Next() {
		return false
	}
	c.yielded++
	if c.yielded == c.cancelAt {
		c.cancel()
	}
	return true
}

// ctxPollRows is the fixture size: far past the cancel points, so a
// loop that ignores ctx is told apart from one that polls it.
const ctxPollRows = 40_000

// ctxPollFixture: ctxPollRows records over a 4-key categorical cat and
// a positive f64 x.
func ctxPollFixture(t *testing.T) (*encoding.Schema, []*Record) {
	t.Helper()
	schema, _ := limitGroupsFixture(t)
	recs := make([]*Record, ctxPollRows)
	for i := range recs {
		recs[i] = NewRecord(schema, map[string]float64{"cat": float64(i % 4), "x": float64(i%97 + 1), "y": 1})
	}
	return schema, recs
}

// ctxPollCase is one per-record loop: the request that routes to it and
// the row whose yield cancels the ctx. A cancel inside the second scan
// of a multi-pass loop sits past ctxPollRows.
type ctxPollCase struct {
	name     string
	req      func() *types.Request
	fused    bool
	cancelAt int
	path     ProcessPath // the arm an uncancelled run takes
}

func ctxPollCases() []ctxPollCase {
	sum := func() *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}}
	}
	logFeat := []*types.Feature{{Type: types.FEAT_LOG, Field: "x", Label: "lx"}}
	grouped := func() *types.Request {
		r := sum()
		r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
		return r
	}
	twoPass := func() *types.Request {
		r := sum()
		r.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"}}
		return r
	}
	return []ctxPollCase{
		{name: "buffered materialize", req: func() *types.Request {
			r := sum()
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "x", Label: "m"})
			return r
		}, cancelAt: 5_000, path: PathBuffered},
		{name: "streaming ungrouped", req: sum, cancelAt: 5_000, path: PathStreaming},
		{name: "streaming ungrouped feature pre-pass", req: func() *types.Request {
			r := sum()
			r.Features = logFeat
			return r
		}, cancelAt: 5_000, path: PathStreaming},
		{name: "streaming ungrouped after pre-pass", req: func() *types.Request {
			r := sum()
			r.Features = logFeat
			return r
		}, cancelAt: ctxPollRows + 5_000, path: PathStreaming},
		{name: "streaming grouped", req: grouped, cancelAt: 5_000, path: PathStreaming},
		{name: "streaming grouped feature pre-pass", req: func() *types.Request {
			r := grouped()
			r.Features = logFeat
			return r
		}, cancelAt: 5_000, path: PathStreaming},
		{name: "streaming grouped after pre-pass", req: func() *types.Request {
			r := grouped()
			r.Features = logFeat
			return r
		}, cancelAt: ctxPollRows + 5_000, path: PathStreaming},
		{name: "streaming two-pass layer scan", req: twoPass, cancelAt: 5_000, path: PathStreaming},
		{name: "streaming two-pass emission scan", req: twoPass, cancelAt: ctxPollRows + 5_000, path: PathStreaming},
		{name: "fused crosstab", fused: true, req: func() *types.Request {
			return &types.Request{Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
			}}
		}, cancelAt: 5_000},
	}
}

func runCtxPollCase(ctx context.Context, p *Processor, c ctxPollCase, iter RecordIterator) (*types.Response, error) {
	if c.fused {
		return p.RunCrosstabFused(ctx, c.req(), iter)
	}
	return p.Process(ctx, c.req(), iter)
}

// TestCtxPoll_CancelStopsEveryLoop: every serial per-record loop — the
// buffered materialize loop, the streaming ungrouped / grouped / two-
// pass scans (feature pre-pass included) and the fused crosstab loop —
// stops within CtxPollInterval rows of a mid-run cancel and returns the
// caller's ctx error unchanged (never a coded error).
func TestCtxPoll_CancelStopsEveryLoop(t *testing.T) {
	schema, recs := ctxPollFixture(t)
	for _, c := range ctxPollCases() {
		t.Run(c.name, func(t *testing.T) {
			// The arm is the one named: an uncancelled run succeeds there.
			p := NewProcessor(schema)
			if _, err := runCtxPollCase(context.Background(), p, c, NewSliceIterator(recs)); err != nil {
				t.Fatalf("uncancelled run: %v", err)
			}
			if !c.fused && p.LastPath() != c.path {
				t.Fatalf("path = %s, want %s", p.LastPath(), c.path)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			it := &cancelIter{SliceIterator: NewSliceIterator(recs), cancel: cancel, cancelAt: c.cancelAt}
			resp, err := runCtxPollCase(ctx, NewProcessor(schema), c, it)
			if err != context.Canceled {
				t.Fatalf("err = %v (resp %v), want context.Canceled unchanged", err, resp != nil)
			}
			if over := it.yielded - c.cancelAt; over > CtxPollInterval {
				t.Fatalf("loop ran %d rows past the cancel, want <= %d", over, CtxPollInterval)
			}
		})
	}
}

// TestCtxPoll_PreCancelledStopsAtFirstRow: a ctx cancelled before the
// run stops every loop on its first row.
func TestCtxPoll_PreCancelledStopsAtFirstRow(t *testing.T) {
	schema, recs := ctxPollFixture(t)
	for _, c := range ctxPollCases() {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			it := &cancelIter{SliceIterator: NewSliceIterator(recs), cancel: cancel}
			_, err := runCtxPollCase(ctx, NewProcessor(schema), c, it)
			if !stderrors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if it.yielded != 1 {
				t.Fatalf("yielded %d rows, want 1", it.yielded)
			}
		})
	}
}

// TestCtxPoller_Cadence: Poll reads ctx on the first row and on every
// CtxPollInterval-th row after it, never between; a nil ctx never
// cancels.
func TestCtxPoller_Cadence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := NewCtxPoller(ctx)
	for i := range 3*CtxPollInterval + 1 {
		err := p.Poll()
		if want := i%CtxPollInterval == 0; (err != nil) != want {
			t.Fatalf("row %d: err = %v, want error %v", i, err, want)
		}
	}
	//lint:ignore SA1012 a nil ctx is the case under test
	np := NewCtxPoller(nil)
	for range 2 * CtxPollInterval {
		if err := np.Poll(); err != nil {
			t.Fatalf("nil ctx: %v", err)
		}
	}
}

// BenchmarkCtxPoller_Poll is the per-row cost the loops pay.
func BenchmarkCtxPoller_Poll(b *testing.B) {
	p := NewCtxPoller(context.Background())
	for b.Loop() {
		if err := p.Poll(); err != nil {
			b.Fatal(err)
		}
	}
}
