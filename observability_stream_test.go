package pulse

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// E2-S2 (PRD FR-12): a streaming operation (ProcessStream,
// ProcessStreamResult, SynthStream) starts at the call and ends exactly
// once — on drain, Close or ctx cancel, whichever comes first — and its
// scan phase runs to that end, consumer think-time included. A leaked
// iterator (never drained, closed or cancelled) never ends; that is a
// documented caller bug, not tested here.

// streamRecorder counts top-level ends per operation ID and keeps the
// last end's result and the phases each operation reported.
type streamRecorder struct {
	mu     sync.Mutex
	ends   map[uint64]int
	last   observe.OperationResult
	phases map[uint64][]observe.PhaseTiming
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{ends: map[uint64]int{}, phases: map[uint64][]observe.PhaseTiming{}}
}

func (r *streamRecorder) hooks() *observe.Hooks {
	return &observe.Hooks{
		OnPhase: func(_ context.Context, info observe.OperationInfo, ph observe.PhaseTiming) {
			r.mu.Lock()
			r.phases[info.ID] = append(r.phases[info.ID], ph)
			r.mu.Unlock()
		},
		OnOperationEnd: func(_ context.Context, info observe.OperationInfo, res observe.OperationResult) {
			r.mu.Lock()
			r.ends[info.ID]++
			r.last = res
			r.mu.Unlock()
		},
	}
}

// totalEnds is the number of end calls across every operation, and
// distinct the number of operations that ended.
func (r *streamRecorder) totalEnds() (total, distinct int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.ends {
		total += n
	}
	return total, len(r.ends)
}

func (r *streamRecorder) scan() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	var d time.Duration
	for _, phs := range r.phases {
		for _, ph := range phs {
			if ph.Phase == observe.PhaseScan {
				d += ph.Duration
			}
		}
	}
	return d
}

// waitEnds polls until n ends were recorded (a ctx-cancel end fires on
// its own goroutine) or a deadline passes.
func (r *streamRecorder) waitEnds(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if total, _ := r.totalEnds(); total >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("operation never ended")
}

func (r *streamRecorder) assertOnce(t *testing.T) {
	t.Helper()
	// Give a stray second end (a racing cancel) the chance to land.
	time.Sleep(10 * time.Millisecond)
	if total, distinct := r.totalEnds(); total != 1 || distinct != 1 {
		t.Fatalf("%d ends over %d operations, want exactly 1", total, distinct)
	}
}

const thinkTime = 25 * time.Millisecond

// TestObservabilityStreamingEndOnce covers every end trigger.
//
// Falsified by removing both once guards — opRun.end's and
// observedRowIter.finish's; either alone holds — (Close after drain
// ends twice), or the context.AfterFunc in newObservedRowIter (cancel
// never ends).
func TestObservabilityStreamingEndOnce(t *testing.T) {
	setup := func(t *testing.T) (*Pulse, *streamRecorder) {
		rec := newStreamRecorder()
		p, _ := obsFixture(t, Options{Hooks: rec.hooks()})
		return p, rec
	}
	grouped := func() *Request {
		r := obsRequest()
		r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
		return r
	}

	t.Run("ProcessStream/drain then Close", func(t *testing.T) {
		p, rec := setup(t)
		it, err := p.ProcessStream(context.Background(), grouped())
		if err != nil {
			t.Fatal(err)
		}
		if total, _ := rec.totalEnds(); total != 0 {
			t.Fatal("ended before the stream was consumed")
		}
		for {
			_, ok, err := it.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			time.Sleep(thinkTime) // consumer think-time
		}
		if total, _ := rec.totalEnds(); total != 1 {
			t.Fatalf("drain: %d ends, want 1", total)
		}
		_ = it.Close()
		rec.assertOnce(t)
		if rec.last.Code != observe.CodeOK {
			t.Errorf("code %q, want ok", rec.last.Code)
		}
		if got := rec.scan(); got < 2*thinkTime {
			t.Errorf("scan %v does not include the consumer's think-time (≥ %v)", got, 2*thinkTime)
		}
	})

	t.Run("ProcessStream/Close before drain", func(t *testing.T) {
		p, rec := setup(t)
		it, err := p.ProcessStream(context.Background(), grouped())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := it.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = it.Close()
		_ = it.Close()
		rec.assertOnce(t)
	})

	t.Run("ProcessStream/ctx cancel", func(t *testing.T) {
		p, rec := setup(t)
		ctx, cancel := context.WithCancel(context.Background())
		it, err := p.ProcessStream(ctx, grouped())
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		rec.waitEnds(t, 1)
		_ = it.Close()
		rec.assertOnce(t)
		if rec.last.Code != observe.CodeUncoded {
			t.Errorf("cancelled stream code %q, want %q", rec.last.Code, observe.CodeUncoded)
		}
	})

	t.Run("ProcessStreamResult/drain", func(t *testing.T) {
		p, rec := setup(t)
		sr, err := p.ProcessStreamResult(context.Background(), grouped())
		if err != nil {
			t.Fatal(err)
		}
		for range sr.Chunks {
			time.Sleep(thinkTime)
		}
		term := <-sr.Done
		if term.Status != StreamCompleted {
			t.Fatalf("status %v", term.Status)
		}
		// The end fires before the terminator is delivered.
		if total, _ := rec.totalEnds(); total != 1 {
			t.Fatalf("%d ends at Done, want 1", total)
		}
		rec.assertOnce(t)
		if got := rec.scan(); got <= 0 {
			t.Errorf("scan %v, want the producer's read-to-terminator time", got)
		}
	})

	t.Run("ProcessStreamResult/ctx cancel", func(t *testing.T) {
		p, rec := setup(t)
		ctx, cancel := context.WithCancel(context.Background())
		sr, err := p.ProcessStreamResult(ctx, grouped())
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		drainStream(sr)
		rec.assertOnce(t)
	})

	t.Run("SynthStream/drain", func(t *testing.T) {
		p, rec := setup(t)
		sr, err := p.SynthStream(context.Background(), fidelitySmallSpec(5), SynthOptions{Seed: 1})
		if err != nil {
			t.Fatal(err)
		}
		drainStream(sr)
		rec.assertOnce(t)
		if rec.last.Code != observe.CodeOK {
			t.Errorf("code %q, want ok", rec.last.Code)
		}
	})

	t.Run("SynthStream/ctx cancel", func(t *testing.T) {
		p, rec := setup(t)
		ctx, cancel := context.WithCancel(context.Background())
		sr, err := p.SynthStream(ctx, fidelitySmallSpec(500), SynthOptions{Seed: 1})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		drainStream(sr)
		rec.assertOnce(t)
	})

	t.Run("failed call ends at once", func(t *testing.T) {
		p, rec := setup(t)
		bad := obsRequest()
		bad.Cohort = &types.Cohort{Filename: "missing.pulse"}
		if _, err := p.ProcessStream(context.Background(), bad); err == nil {
			t.Fatal("want an error")
		}
		rec.assertOnce(t)
	})
}

// TestObservedRowIterForwardsShaping: the observed wrapper is
// transparent — Returned and the precision-aware row encoder behave as
// on the unobserved, shaped iterator.
func TestObservedRowIterForwardsShaping(t *testing.T) {
	ctx := context.Background()
	req := func() *Request {
		r := obsRequest()
		r.Aggregations = []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "amount", Label: "avg"}}
		r.Return = &types.Return{Preset: types.ReturnPresetStandard, Precision: 2}
		return r
	}
	drain := func(p *Pulse) ([]string, *types.ReturnedMarker) {
		it, err := p.ProcessStream(ctx, req())
		if err != nil {
			t.Fatal(err)
		}
		defer it.Close()
		var rows []string
		for {
			row, ok, err := it.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			b, err := returnshape.MarshalStreamRow(it, row)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, string(b))
		}
		marker, _ := it.(interface{ Returned() *types.ReturnedMarker })
		if marker == nil {
			t.Fatal("iterator lost Returned()")
		}
		return rows, marker.Returned()
	}
	plain, _ := obsFixture(t, Options{})
	observed, _ := obsFixture(t, Options{Hooks: newStreamRecorder().hooks()})
	wantRows, wantMarker := drain(plain)
	gotRows, gotMarker := drain(observed)
	if len(wantRows) == 0 || wantMarker == nil {
		t.Fatal("fixture: the unobserved stream produced no shaped rows / marker")
	}
	if len(gotRows) != len(wantRows) || gotRows[0] != wantRows[0] {
		t.Errorf("observed rows %v, want %v", gotRows, wantRows)
	}
	if gotMarker == nil || *gotMarker != *wantMarker {
		t.Errorf("observed marker %+v, want %+v", gotMarker, wantMarker)
	}
}
