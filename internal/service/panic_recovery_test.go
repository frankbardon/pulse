package service

// The engine half of the operation panic suite (U56): the only service
// tests that may expect PROCESSING_PANIC (TestPanicRecoverySuiteIsolated).
// The per-arm, extension-driven suite lives in the root package's
// panic_recovery_pools_test.go.

import (
	"context"
	stderrors "errors"
	"runtime"
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
// parallelDecodeMmap as PROCESSING_PANIC; the errgroup cancels the
// siblings (each stops at its ctx poll) and every worker goroutine
// exits. Falsified by deleting the recoverInto defer in the g.Go func
// (the binary crashes).
func TestPanicRecovery_ParallelDecodeWorker(t *testing.T) {
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
