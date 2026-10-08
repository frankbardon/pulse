package pulse

import (
	"context"
	stderrors "errors"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// timeoutStreamPulse builds an instance over a cohort whose grouped
// request yields more rows than the StreamResult chunk buffer, so a
// consumer that never reads leaves the producer blocked mid-stream.
func timeoutStreamPulse(t *testing.T, l Limits) (*Pulse, *Request) {
	t.Helper()
	memFs := afero.NewMemMapFs()
	rows := make([][]string, 0, 3*(streamBuffer+4))
	for i := 0; i < cap(rows); i++ {
		rows = append(rows, []string{strconv.Itoa(i), "n" + strconv.Itoa(i%(streamBuffer+4))})
	}
	createTestPulseFile(t, memFs, "rows.pulse", []string{"age", "name"}, rows)
	p, err := New(Options{FS: memFs, Limits: l})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, &Request{
		Cohort:       &types.Cohort{Filename: "rows.pulse"},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "name"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "s"}},
	}
}

// awaitStalledTerminator waits for the terminator of a stream whose
// chunks are never read: the producer blocks once the buffer fills, so
// only a done ctx can end it.
func awaitStalledTerminator(t *testing.T, res StreamResult[Row]) StreamTerminator {
	t.Helper()
	select {
	case term := <-res.Done:
		return term
	case <-time.After(10 * time.Second):
		t.Fatal("no terminator: the stalled stream was never stopped")
		return StreamTerminator{}
	}
}

// TestProcessStreamResult_RequestTimeoutIsStreamErrored: the
// RequestTimeout deadline firing mid-stream (the run finished; the
// consumer stalled) ends the stream StreamErrored with the coded
// PULSE_LIMIT_EXCEEDED error — not StreamCancelled.
func TestProcessStreamResult_RequestTimeoutIsStreamErrored(t *testing.T) {
	const d = 300 * time.Millisecond
	p, req := timeoutStreamPulse(t, Limits{RequestTimeout: d})
	res, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	term := awaitStalledTerminator(t, res)
	if term.Status != StreamErrored {
		t.Fatalf("status = %v (err %v), want StreamErrored", term.Status, term.Error)
	}
	var ce *errors.CodedError
	if !stderrors.As(term.Error, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("terminator error = %v, want PULSE_LIMIT_EXCEEDED", term.Error)
	}
	if ce.Details["limit"] != "request_timeout" || ce.Details["configured"] != int64(d) {
		t.Fatalf("details = %v", ce.Details)
	}
	for range res.Chunks {
	}
}

// TestProcessStreamResult_CallerDeadlineStaysCancelled: the caller's own
// deadline firing mid-stream under a 1h RequestTimeout stays
// StreamCancelled with context.DeadlineExceeded.
func TestProcessStreamResult_CallerDeadlineStaysCancelled(t *testing.T) {
	p, req := timeoutStreamPulse(t, Limits{RequestTimeout: time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := p.ProcessStreamResult(ctx, req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	term := awaitStalledTerminator(t, res)
	if term.Status != StreamCancelled || !stderrors.Is(term.Error, context.DeadlineExceeded) {
		t.Fatalf("status = %v err = %v, want StreamCancelled + context.DeadlineExceeded", term.Status, term.Error)
	}
	for range res.Chunks {
	}
}

// TestProcessStreamResult_CallerCancelPassesThrough: under a set
// RequestTimeout a caller's cancel stays context.Canceled through the
// facade. (The in-run trip on every entry is internal/service's
// TestRequestTimeout_TripsOnEveryEntry.)
func TestProcessStreamResult_CallerCancelPassesThrough(t *testing.T) {
	p, req := timeoutStreamPulse(t, Limits{RequestTimeout: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.ProcessStreamResult(ctx, req); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := p.Process(ctx, req); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("Process err = %v, want context.Canceled", err)
	}
}
