package pulse

// Operation panic suite, part two (U56 E2-S1): the panics the facade's
// recover cannot see because they happen on another goroutine — the
// engine's four worker pools (parallel decode, parallel shards,
// ComposeParallel slots, compose fits) and the two stream producer
// goroutines (SynthStream, ProcessStreamResult). Each must come back as
// PROCESSING_PANIC with the host alive and no goroutine left behind;
// with DisablePanicRecovery each must crash the process, which only a
// child process can observe.

import (
	"context"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

const (
	// poolLargeCohort clears the engine's 100K-record parallel decode
	// threshold (internal/service.parallelDecodeRecordThreshold).
	poolLargeCohort = "large.pulse"
	poolLargeRows   = 100_000 + 4096
	poolArchive     = "archive.pulse"

	// panicChildEnv / panicChildDirEnv drive the re-panic child process
	// (TestPanicRecovery_PoolRepanics): the arm to run and the data
	// directory the parent prepared.
	panicChildEnv    = "PANIC_SUITE_CHILD_ARM"
	panicChildDirEnv = "PANIC_SUITE_CHILD_DIR"
)

// writePoolLargeCohort writes a poolLargeRows-record {id u32, amount
// f64} cohort under dir on the REAL filesystem: parallel decode mmaps
// the file and silently stays serial on a MemMapFs.
func writePoolLargeCohort(t *testing.T, dir string) {
	t.Helper()
	fields := []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
	}
	s := &encoding.Schema{Fields: fields}
	var buf []byte
	w := &panicByteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	stride := s.RecordByteSize()
	rec := make([]byte, stride)
	for r := 0; r < poolLargeRows; r++ {
		v := uint64(r)
		for i := 0; i < 4; i++ {
			rec[i] = byte(v >> (8 * i))
		}
		bits := math.Float64bits(float64(r%50) + 0.5)
		for i := 0; i < 8; i++ {
			rec[4+i] = byte(bits >> (8 * i))
		}
		buf = append(buf, rec...)
	}
	if err := os.WriteFile(filepath.Join(dir, poolLargeCohort), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

type panicByteSink struct{ b *[]byte }

func (s *panicByteSink) Write(p []byte) (int, error) { *s.b = append(*s.b, p...); return len(p), nil }

// poolArm is one goroutine that runs operator (or generator) code off
// the caller's goroutine.
type poolArm struct {
	name string
	// op is details.op: the pool (or stream) that recovered the panic.
	op string
	// frame is a source file the re-panic trace must name: proof the
	// child crashed on that goroutine, not the caller's.
	frame string
	// setup builds the arm's instance from opts (recovery state already
	// applied by enablePanicRecovery) in the prepared data dir.
	setup func(t *testing.T, dir string, opts Options) *Pulse
	// prepare writes the arm's data under dir (parent side).
	prepare func(t *testing.T, dir string)
	// run drives the arm into the panic and returns the operation's
	// error (a stream's terminator error).
	run func(ctx context.Context, t *testing.T, p *Pulse) error
	// cohort is the arm's cohort, for the follow-up call.
	cohort string
}

func poolSumPanicRequest(cohort string) *Request {
	return &Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "s"}},
		// Extension filterers are row-local streamable, so the request
		// stays mergeable and reaches the parallel arms.
		Filterers: []*types.Filterer{{Type: panicFilterName, Field: "amount"}},
	}
}

func poolFitRequest() *Request {
	return &Request{
		Cohort:      &types.Cohort{Filename: obsCohort},
		Filterers:   []*types.Filterer{{Type: panicFilterName, Field: "amount"}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "amount", Predictors: []string{"id"}}},
	}
}

func poolFitsBatch() *ComposedRequest {
	return &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "a", Request: poolFitRequest()}, {Name: "b", Request: poolFitRequest()}},
		Requests: []*Request{obsRequest()},
	}
}

// memSetup builds an instance over the obs fixture on a MemMapFs
// (dir unused).
func memSetup(t *testing.T, _ string, opts Options) *Pulse {
	t.Helper()
	opts.Extensions = panicExtensions()
	p, _ := obsFixture(t, opts)
	return enablePanicRecovery(p, opts)
}

// streamTerm drains sr and returns its terminator, failing on a
// non-StreamErrored end.
func streamTerm(t *testing.T, sr StreamResult[Row]) error {
	t.Helper()
	for range sr.Chunks {
	}
	select {
	case term, ok := <-sr.Done:
		if !ok {
			t.Fatal("Done closed without a terminator")
		}
		if term.Status != StreamErrored {
			t.Fatalf("terminator status = %v (err %v), want StreamErrored", term.Status, term.Error)
		}
		if _, open := <-sr.Done; open {
			t.Fatal("Done delivered a second terminator")
		}
		return term.Error
	case <-time.After(10 * time.Second):
		t.Fatal("no terminator within 10s")
	}
	return nil
}

// panickingRowIter is a RowIter whose Next panics: it stands in for an
// engine bug in the producer goroutine's row shaping.
type panickingRowIter struct{ RowIter }

func (panickingRowIter) Next(context.Context) (Row, bool, error) { panic(panicSecret) }

func poolArms() []poolArm {
	return []poolArm{
		{
			name: "parallel_decode", op: "parallel_decode", frame: "parallel_decode.go", cohort: poolLargeCohort,
			prepare: writePoolLargeCohort,
			setup: func(t *testing.T, dir string, opts Options) *Pulse {
				t.Helper()
				opts.DataDir = dir
				opts.Extensions = panicExtensions()
				opts.DecodeWorkers = 4
				opts.ShardWorkers = 1
				p, err := New(opts)
				if err != nil {
					t.Fatal(err)
				}
				return enablePanicRecovery(p, opts)
			},
			run: func(ctx context.Context, _ *testing.T, p *Pulse) error {
				_, err := p.Process(ctx, poolSumPanicRequest(poolLargeCohort))
				return err
			},
		},
		{
			name: "shard_parallel", op: "shard_parallel", frame: "shard_reduce.go", cohort: poolArchive,
			setup: func(t *testing.T, _ string, opts Options) *Pulse {
				t.Helper()
				fsys := afero.NewMemMapFs()
				shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
				for i, path := range shards {
					createTestPulseFile(t, fsys, path, []string{"id", "region", "amount"}, [][]string{
						{"1", "north", "10.5"}, {"2", "south", "20.25"}, {string(rune('3' + i)), "north", "7"},
					})
				}
				builder, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := builder.CreateShardArchive(context.Background(), poolArchive, shards); err != nil {
					t.Fatalf("CreateShardArchive: %v", err)
				}
				opts.FS = fsys
				opts.Extensions = panicExtensions()
				opts.ShardWorkers = 3
				opts.DecodeWorkers = 1
				p, err := New(opts)
				if err != nil {
					t.Fatal(err)
				}
				return enablePanicRecovery(p, opts)
			},
			run: func(ctx context.Context, _ *testing.T, p *Pulse) error {
				_, err := p.Process(ctx, poolSumPanicRequest(poolArchive))
				return err
			},
		},
		{
			name: "compose_parallel", op: "compose_parallel", frame: "compose_parallel.go", cohort: obsCohort,
			setup: func(t *testing.T, dir string, opts Options) *Pulse {
				opts.ShardWorkers, opts.DecodeWorkers = 1, 1
				return memSetup(t, dir, opts)
			},
			run: func(ctx context.Context, _ *testing.T, p *Pulse) error {
				_, err := p.ComposeParallel(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), panicAggRequest()}},
					ComposeOptions{MaxWorkers: 2, FailFast: true})
				return err
			},
		},
		{
			name: "compose_fits", op: "compose_fits", frame: "compose_fits.go", cohort: obsCohort,
			setup: func(t *testing.T, dir string, opts Options) *Pulse {
				opts.ShardWorkers, opts.DecodeWorkers = 1, 1
				return memSetup(t, dir, opts)
			},
			run: func(ctx context.Context, _ *testing.T, p *Pulse) error {
				_, err := p.ComposeParallel(ctx, poolFitsBatch(), ComposeOptions{MaxWorkers: 2})
				return err
			},
		},
		{
			name: "synth_stream", op: "synth_stream", frame: "stream.go", cohort: obsCohort,
			setup: memSetup,
			run: func(ctx context.Context, t *testing.T, p *Pulse) error {
				sr, err := p.SynthStream(ctx, &SynthSpec{RowCount: 10}, SynthOptions{})
				if err != nil {
					t.Fatalf("SynthStream setup: %v", err)
				}
				return streamTerm(t, sr)
			},
		},
		{
			name: "process_stream", op: "process_stream", frame: "stream.go", cohort: obsCohort,
			setup: func(t *testing.T, dir string, opts Options) *Pulse {
				opts.ShardWorkers, opts.DecodeWorkers = 1, 1
				return memSetup(t, dir, opts)
			},
			run: func(ctx context.Context, t *testing.T, p *Pulse) error {
				sr, err := p.ProcessStreamResult(ctx, obsRequest())
				if err != nil {
					t.Fatalf("ProcessStreamResult setup: %v", err)
				}
				return streamTerm(t, sr)
			},
		},
	}
}

// armSeams installs the test seams a stream arm needs to panic and
// returns their restore.
func armSeams(arm string) func() {
	switch arm {
	case "synth_stream":
		prev := synthStreamBytes
		synthStreamBytes = func(*SynthSpec, SynthOptions) ([]byte, *SynthResult, error) { panic(panicSecret) }
		return func() { synthStreamBytes = prev }
	case "process_stream":
		testWrapStreamIter = func(it RowIter) RowIter { return panickingRowIter{it} }
		return func() { testWrapStreamIter = nil }
	}
	return func() {}
}

// assertPoolPanic checks err is (or wraps) the PROCESSING_PANIC a pool
// recovered, stamped with that pool's name, carrying no panic value.
func assertPoolPanic(t *testing.T, err error, op string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_PANIC {
		t.Fatalf("err = %v (%T), want PROCESSING_PANIC", err, err)
	}
	if ce.Details["op"] != op {
		t.Errorf("details.op = %v, want %s", ce.Details["op"], op)
	}
	if ce.Details["panic_type"] != "string" {
		t.Errorf("details.panic_type = %v, want string", ce.Details["panic_type"])
	}
	stack, _ := ce.Details["stack"].([]string)
	if len(stack) == 0 || len(stack) > 32 {
		t.Errorf("details.stack has %d frames, want 1..32", len(stack))
	}
	if strings.Contains(err.Error(), panicSecret) {
		t.Errorf("error leaks the panic value: %v", err)
	}
}

// waitGoroutines fails unless the goroutine count falls back to at most
// before within 5s: a recovered pool or stream panic leaves nothing
// running.
func waitGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= before {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("goroutine leak: %d running, %d before\n%s", n, before, buf)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPanicRecovery_PoolArms (FR-12, FR-13, FR-17, FR-18, FR-21): a
// panic on each worker-pool / stream-producer goroutine returns
// PROCESSING_PANIC (details.op the pool), the host survives, the same
// *Pulse serves a normal call, and no goroutine is left behind.
//
// Falsified per arm by deleting its recover: the recoverInto defer in
// parallel_decode.go's g.Go func, shard_reduce.go's shardWorker,
// compose_parallel.go's processSlotRecovered and compose_fits.go's
// runRecovered, and the recover defers of the two stream.go producer
// goroutines — each deletion crashes this test binary with the panic.
func TestPanicRecovery_PoolArms(t *testing.T) {
	ctx := context.Background()
	for _, arm := range poolArms() {
		t.Run(arm.name, func(t *testing.T) {
			dir := t.TempDir()
			if arm.prepare != nil {
				arm.prepare(t, dir)
			}
			p := arm.setup(t, dir, Options{})
			restore := armSeams(arm.name)
			before := runtime.NumGoroutine()
			err := arm.run(ctx, t, p)
			restore()
			assertPoolPanic(t, err, arm.op)
			waitGoroutines(t, before)
			// The instance stays usable.
			if _, err := p.Process(ctx, &Request{
				Cohort:       &types.Cohort{Filename: arm.cohort},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}},
			}); err != nil {
				t.Fatalf("follow-up Process on the same *Pulse: %v", err)
			}
		})
	}
}

// TestPanicRecovery_ComposeFitsSerialMatchesPool: a panicking fit fails
// with the same PROCESSING_PANIC (details.op compose_fits, details.fit)
// on the serial Compose arm as on the ComposeParallel pool, so the
// error does not depend on the worker count. Falsified by calling run
// instead of runRecovered on the serial arm (the facade then recovers
// with details.op compose).
func TestPanicRecovery_ComposeFitsSerialMatchesPool(t *testing.T) {
	ctx := context.Background()
	p := memSetup(t, "", Options{ShardWorkers: 1, DecodeWorkers: 1})
	_, serial := p.Compose(ctx, poolFitsBatch())
	_, pool := p.ComposeParallel(ctx, poolFitsBatch(), ComposeOptions{MaxWorkers: 2})
	for name, err := range map[string]error{"Compose": serial, "ComposeParallel": pool} {
		assertPoolPanic(t, err, "compose_fits")
		var ce *errors.CodedError
		stderrors.As(err, &ce)
		if ce.Details["fit"] != 0 {
			t.Errorf("%s: details.fit = %v, want 0", name, ce.Details["fit"])
		}
	}
}

// TestPanicRecovery_ComposeParallelNoFailFast: without FailFast a
// panicking slot is one failed slot among the rest — the existing
// aggregate SERVICE_INTERNAL error names its index — and the pool still
// drains without a leak.
func TestPanicRecovery_ComposeParallelNoFailFast(t *testing.T) {
	ctx := context.Background()
	p := memSetup(t, "", Options{ShardWorkers: 1, DecodeWorkers: 1})
	before := runtime.NumGoroutine()
	_, err := p.ComposeParallel(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), panicAggRequest()}},
		ComposeOptions{MaxWorkers: 2})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_INTERNAL {
		t.Fatalf("err = %v, want the SERVICE_INTERNAL aggregate", err)
	}
	if idx, _ := ce.Details["failed_indices"].([]int); len(idx) != 1 || idx[0] != 1 {
		t.Errorf("failed_indices = %v, want [1]", ce.Details["failed_indices"])
	}
	if strings.Contains(err.Error(), panicSecret) {
		t.Errorf("aggregate leaks the panic value: %v", err)
	}
	waitGoroutines(t, before)
}

// runRepanicChild is the child side of TestPanicRecovery_PoolRepanics:
// it runs arm with DisablePanicRecovery and should never return.
func runRepanicChild(t *testing.T, name, dir string) {
	for _, arm := range poolArms() {
		if arm.name != name {
			continue
		}
		p := arm.setup(t, dir, Options{DisablePanicRecovery: true})
		defer armSeams(name)()
		err := arm.run(context.Background(), t, p)
		// Reaching here means the goroutine recovered instead of
		// re-panicking (the parent fails on this marker).
		os.Stdout.WriteString("CHILD_RETURNED: ")
		if err != nil {
			os.Stdout.WriteString(err.Error())
		}
		os.Stdout.WriteString("\n")
		return
	}
	t.Fatalf("unknown arm %q", name)
}

// TestPanicRecovery_NewWiresServiceKnob (FR-11): New hands
// Options.DisablePanicRecovery to the instance's service — the pools'
// per-instance knob, never a global — and TestMain's override reaches
// it too, so an engine panic on a pool goroutine crashes an ordinary
// test. Falsified by dropping the svc.SetDisablePanicRecovery call in
// New (both production subtests see the service default) or its
// `|| testForcePanicRecoveryOff` (the ordinary instance recovers).
func TestPanicRecovery_NewWiresServiceKnob(t *testing.T) {
	ordinary, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	if !ordinary.svc.PanicRecoveryDisabled() {
		t.Fatal("TestMain's override did not reach the service: pool panics would be recovered in ordinary tests")
	}
	prev := testForcePanicRecoveryOff
	testForcePanicRecoveryOff = false
	defer func() { testForcePanicRecoveryOff = prev }()
	for _, disable := range []bool{true, false} {
		p, err := New(Options{FS: afero.NewMemMapFs(), DisablePanicRecovery: disable})
		if err != nil {
			t.Fatal(err)
		}
		if got := p.svc.PanicRecoveryDisabled(); got != disable {
			t.Errorf("DisablePanicRecovery %v: service knob = %v", disable, got)
		}
		if p.disablePanicRecovery != disable {
			t.Errorf("DisablePanicRecovery %v: facade knob = %v", disable, p.disablePanicRecovery)
		}
	}
}

// panickingCloseIter completes normally, then panics in Close — after
// the producer goroutine already delivered its terminator.
type panickingCloseIter struct{ RowIter }

func (panickingCloseIter) Close() error { panic(panicSecret) }

// TestPanicRecovery_StreamPanicAfterTerminator: a producer-goroutine
// panic AFTER the terminator went out (here in the iterator's Close) is
// swallowed — the consumer keeps the StreamCompleted terminator, Done
// delivers exactly one, and the recover never sends on the closed Done
// channel. Falsified by dropping the `if !terminated` guard (the second
// send panics on the closed channel and crashes the binary).
func TestPanicRecovery_StreamPanicAfterTerminator(t *testing.T) {
	ctx := context.Background()
	p := memSetup(t, "", Options{ShardWorkers: 1, DecodeWorkers: 1})
	testWrapStreamIter = func(it RowIter) RowIter { return panickingCloseIter{it} }
	defer func() { testWrapStreamIter = nil }()
	before := runtime.NumGoroutine()
	sr, err := p.ProcessStreamResult(ctx, obsRequest())
	if err != nil {
		t.Fatal(err)
	}
	for range sr.Chunks {
	}
	term, ok := <-sr.Done
	if !ok || term.Status != StreamCompleted || term.Error != nil {
		t.Fatalf("terminator = %+v (ok %v), want StreamCompleted", term, ok)
	}
	if _, open := <-sr.Done; open {
		t.Fatal("Done delivered a second terminator")
	}
	waitGoroutines(t, before)
}
