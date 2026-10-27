package service

// The engine half of the operation panic suite (U56): the only service
// tests that may expect PROCESSING_PANIC (TestPanicRecoverySuiteIsolated).
// The per-arm, extension-driven suite lives in the root package's
// panic_recovery_pools_test.go.

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
)

const servicePanicValue = "svc_panic_row_value_5521"

// TestPanicRecovery_ServiceOverride (FR-15, FR-11): TestMain's override
// makes every New-built Service re-panic, and SetDisablePanicRecovery
// is the per-instance knob. Falsified by deleting the TestMain
// assignment or the New seeding.
func TestPanicRecovery_ServiceOverride(t *testing.T) {
	t.Parallel()

	if !New(nil).PanicRecoveryDisabled() {
		t.Fatal("TestMain's override did not reach New: pool panics would be recovered in ordinary tests")
	}
	a, b := New(nil), New(nil)
	a.SetDisablePanicRecovery(false)
	if a.PanicRecoveryDisabled() || !b.PanicRecoveryDisabled() {
		t.Fatal("SetDisablePanicRecovery is not per instance")
	}
}

// TestPanicRecovery_RecoverInto: the shared worker recover converts a
// panic into PROCESSING_PANIC stamped with the pool name, and with
// rethrow re-panics with the original value. Falsified by dropping the
// rethrow branch (the second half sees an error, not a panic).
func TestPanicRecovery_RecoverInto(t *testing.T) {
	t.Parallel()

	run := func(rethrow bool) (err error) {
		defer recoverInto(panicOpShardParallel, rethrow, &err)
		panic(servicePanicValue)
	}
	var ce *errors.CodedError
	if err := run(false); !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_PANIC || ce.Details["op"] != panicOpShardParallel {
		t.Fatalf("recovered err = %v, want PROCESSING_PANIC with op %s", err, panicOpShardParallel)
	}
	var got any
	func() {
		defer func() { got = recover() }()
		_ = run(true)
	}()
	if got != servicePanicValue {
		t.Fatalf("rethrow re-panicked with %#v, want the original value", got)
	}
}

// TestPanicRecovery_ParallelDecodeWorker (FR-12, FR-21): a panicking
// per-row callback on one decode worker comes back from
// parallelDecodeMmap as PROCESSING_PANIC; the worker group cancels the
// siblings (each stops at its ctx poll) and every worker goroutine
// exits. Falsified by deleting the recoverInto defer in the g.Go func
// (the binary crashes).
func TestPanicRecovery_ParallelDecodeWorker(t *testing.T) {
	// serial: its runtime.NumGoroutine leak check counts every concurrent test's goroutines

	const workers, records = 4, 4 * 8192
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "v", Type: encoding.FieldTypeU32}}}
	stride := schema.RecordByteSize()
	pctx := &parallelDecodeContext{
		schema:       schema,
		mmapBytes:    make([]byte, records*stride),
		stride:       stride,
		totalRecords: records,
		binding:      recordBindingFor(schema, nil, nil),
	}
	before := runtime.NumGoroutine()
	err := parallelDecodeMmap(context.Background(), pctx, workers, func(idx, _, _ int) DecodeCallback {
		return func(*processing.Record) error {
			if idx == 1 {
				panic(servicePanicValue)
			}
			return nil
		}
	})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_PANIC || ce.Details["op"] != panicOpParallelDecode {
		t.Fatalf("err = %v, want PROCESSING_PANIC with op %s", err, panicOpParallelDecode)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: %d running, %d before", runtime.NumGoroutine(), before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPanicRecovery_JoinOnReturnSkipsJoinOnPanic: a worker that panics
// out of joinOnReturn (DisablePanicRecovery's re-panic) never calls
// wg.Done, so the pool's caller stays in wg.Wait until the crash and
// never returns a partial "success" first — the race
// TestPanicRecovery_PoolRepanics hit in CI with a deferred wg.Done.
// Falsified by deferring the wg.Done in joinOnReturn (Wait returns).
func TestPanicRecovery_JoinOnReturnSkipsJoinOnPanic(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	wg.Add(1)
	func() {
		defer func() { _ = recover() }()
		joinOnReturn(&wg, func() { panic(servicePanicValue) })
	}()
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("wg.Wait returned although the worker panicked: a re-panic is observable as a return")
	case <-time.After(50 * time.Millisecond):
	}
	wg.Done() // release the probe goroutine
	<-waited

	// A normal return joins.
	wg.Add(1)
	joinOnReturn(&wg, func() {})
	wg.Wait()
}

// TestPanicRecovery_WorkerGroup: workerGroup keeps errgroup's contract
// — the first worker error is Wait's, it cancels the group context for
// the siblings, and Wait cancels it on a clean finish too. Falsified by
// dropping the cancel in Go (the sibling never sees ctx done) or the
// once (a later error overwrites the first).
func TestPanicRecovery_WorkerGroup(t *testing.T) {
	t.Parallel()

	first := stderrors.New("first")
	g, gctx := newWorkerGroup(context.Background())
	failed := make(chan struct{})
	g.Go(func() error { defer close(failed); return first })
	g.Go(func() error {
		<-failed
		select {
		case <-gctx.Done():
		case <-time.After(5 * time.Second):
			return stderrors.New("sibling never cancelled")
		}
		return stderrors.New("second")
	})
	if err := g.Wait(); err != first {
		t.Fatalf("Wait = %v, want the first worker error", err)
	}

	g, gctx = newWorkerGroup(context.Background())
	g.Go(func() error { return nil })
	if err := g.Wait(); err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
	if gctx.Err() == nil {
		t.Fatal("Wait left the group context live")
	}
}

// TestPanicRecovery_PoolsNeverDeferJoin: no service source defers a
// pool join or uses errgroup (whose Go defers its own): either releases
// the caller's Wait while a worker re-panics under DisablePanicRecovery,
// so the caller returns before the crash. Pools join via joinOnReturn /
// workerGroup. Falsified by restoring `defer wg.Done()` in any pool.
func TestPanicRecovery_PoolsNeverDeferJoin(t *testing.T) {
	t.Parallel()

	deferredJoin := regexp.MustCompile(`defer\s+[\w.]+\.Done\(\)`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := deferredJoin.Find(src); loc != nil {
			t.Errorf("%s: %q — a deferred join releases Wait during a re-panic; use joinOnReturn", f, loc)
		}
		if strings.Contains(string(src), `"golang.org/x/sync/errgroup"`) {
			t.Errorf("%s imports errgroup, whose Go defers its join; use workerGroup", f)
		}
	}
}
