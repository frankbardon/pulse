package pulse

// The operation panic suite (U56). This file — with its siblings named
// panic_recovery_*_test.go — is the only place tests may expect
// PROCESSING_PANIC: everywhere else an engine panic must crash the test
// that hit it. TestMain (testmain_test.go) forces recovery off for every
// instance New builds; suite instances re-enable it explicitly through
// enablePanicRecovery. TestPanicRecoverySuiteIsolated
// (panic_recovery_gate_test.go) fails on any other test file naming the
// code.

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// panicSecret is the panic value. It stands in for row data: it must
// never reach an error, a log record, a hook payload or a metric label.
const panicSecret = "zz_panic_row_value_9431"

// panicAgg panics on real rows (the registration probe hands it none).
type panicAgg struct{}

func (panicAgg) Aggregate(rows extend.Rows, _ string) (float64, error) {
	if rows.Len() > 0 {
		panic(panicSecret)
	}
	return 0, nil
}

// panicFilterer panics on a real record: one whose "amount" reads.
type panicFilterer struct{}

func (panicFilterer) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(rec extend.Record) (bool, error) {
		if _, ok := rec.NumericValue("amount"); ok {
			panic(panicSecret)
		}
		return true, nil
	}, nil
}

const (
	panicAggName    = "AGG_ACME_PANIC"
	panicFilterName = "FILTER_ACME_PANIC"
)

func panicExtensions() Extensions {
	return Extensions{
		Aggregators: []AggregatorRegistration{{
			Name: panicAggName,
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return panicAgg{}, nil
			},
		}},
		Filterers: []FiltererRegistration{{
			Name:    panicFilterName,
			Factory: func() extend.FiltererBuilder { return panicFilterer{} },
		}},
	}
}

// enablePanicRecovery undoes TestMain's testForcePanicRecoveryOff for
// one instance: p recovers exactly as a production instance built from
// opts would (recovery on unless opts.DisablePanicRecovery). Every
// panic-suite instance that expects PROCESSING_PANIC goes through it.
func enablePanicRecovery(p *Pulse, opts Options) *Pulse {
	p.disablePanicRecovery = opts.DisablePanicRecovery
	return p
}

// recoveringFixture is obsFixture with recovery re-enabled per opts.
func recoveringFixture(t *testing.T, opts Options) *Pulse {
	t.Helper()
	p, _ := obsFixture(t, opts)
	return enablePanicRecovery(p, opts)
}

// panicFixture is recoveringFixture plus the panicking extensions,
// pinned to the serial arm (worker-pool recovery is a separate story).
func panicFixture(t *testing.T, opts Options) *Pulse {
	t.Helper()
	opts.Extensions = panicExtensions()
	opts.ShardWorkers = 1
	opts.DecodeWorkers = 1
	return recoveringFixture(t, opts)
}

func panicAggRequest() *Request {
	return &Request{
		Cohort:       &types.Cohort{Filename: obsCohort},
		Aggregations: []*types.Aggregation{{Type: panicAggName, Field: "amount"}},
	}
}

func panicFilterRequest() *Request {
	return &Request{
		Cohort:    &types.Cohort{Filename: obsCohort},
		Filterers: []*types.Filterer{{Type: panicFilterName, Field: "amount"}},
	}
}

// panicCall is one observed operation kind that runs operators on the
// caller's goroutine, driven into the panicking operator.
type panicCall struct {
	name string
	kind observe.OperationKind
	call func(ctx context.Context, p *Pulse) error
}

func panicCalls() []panicCall {
	return []panicCall{
		{"Process", observe.OpProcess, func(ctx context.Context, p *Pulse) error {
			_, err := p.Process(ctx, panicAggRequest())
			return err
		}},
		{"ProcessStream", observe.OpProcessStream, func(ctx context.Context, p *Pulse) error {
			it, err := p.ProcessStream(ctx, panicAggRequest())
			if it != nil {
				_ = it.Close()
			}
			return err
		}},
		{"ProcessStreamResult", observe.OpProcessStream, func(ctx context.Context, p *Pulse) error {
			sr, err := p.ProcessStreamResult(ctx, panicAggRequest())
			drainStream(sr)
			return err
		}},
		{"Compose", observe.OpCompose, func(ctx context.Context, p *Pulse) error {
			_, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), panicAggRequest()}})
			return err
		}},
		{"ProcessChain", observe.OpProcessChain, func(ctx context.Context, p *Pulse) error {
			r := panicFilterRequest()
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}}
			_, err := p.ProcessChain(ctx, &ChainRequest{Cohort: r.Cohort, Stages: []*ChainStage{{Request: r}}})
			return err
		}},
		{"Materialize", observe.OpMaterialize, func(ctx context.Context, p *Pulse) error {
			r := panicFilterRequest()
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "amount"}}
			_, err := p.Materialize(ctx, r, "out.pulse", MaterializeOptions{Overwrite: true})
			return err
		}},
		{"FilterToFileWithRequest", observe.OpFilterToFile, func(ctx context.Context, p *Pulse) error {
			_, err := p.FilterToFileWithRequest(ctx, &FilterToFileRequest{
				SourcePath: obsCohort, OutputDir: "filtered",
				Filterers: []*types.Filterer{{Type: panicFilterName, Field: "amount"}},
			})
			return err
		}},
	}
}

// assertPanicError checks err is the recovered PROCESSING_PANIC for
// kind, carrying neither the panic value nor anything but the shared
// detail shape.
func assertPanicError(t *testing.T, err error, kind observe.OperationKind) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_PANIC {
		t.Fatalf("err = %v (%T), want PROCESSING_PANIC", err, err)
	}
	if ce.Details["op"] != string(kind) {
		t.Errorf("details.op = %v, want %s", ce.Details["op"], kind)
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

// TestPanicRecovery_FacadeOpKinds (FR-7, FR-17 serial arm, FR-18): a
// panicking extension operator returns PROCESSING_PANIC from every
// observed op kind that runs operators on the caller's goroutine, on a
// default (unobserved) instance and an observed one; the same *Pulse
// then serves a normal call.
//
// Falsified by removing the defer p.recoverOp line from observed (the
// default-instance Process subtest crashes the binary with the
// panic), and from observe / observedStream likewise.
func TestPanicRecovery_FacadeOpKinds(t *testing.T) {
	ctx := context.Background()
	instances := map[string]func(t *testing.T) *Pulse{
		"default": func(t *testing.T) *Pulse { return panicFixture(t, Options{}) },
		"observed": func(t *testing.T) *Pulse {
			h := &captureHandler{}
			return panicFixture(t, Options{Logger: h.logger(), Hooks: (&hookRecorder{}).hooks(), Metrics: newFakeMetrics()})
		},
	}
	for iname, mk := range instances {
		t.Run(iname, func(t *testing.T) {
			p := mk(t)
			if p.observing() != (iname == "observed") {
				t.Fatalf("fixture observing() = %v", p.observing())
			}
			for _, c := range panicCalls() {
				t.Run(c.name, func(t *testing.T) {
					assertPanicError(t, c.call(ctx, p), c.kind)
					// The instance stays usable.
					if _, err := p.Process(ctx, obsRequest()); err != nil {
						t.Fatalf("follow-up Process on the same *Pulse: %v", err)
					}
				})
			}
		})
	}
}

// TestPanicRecovery_ObservedAgree (FR-8, FR-19): with Logger, Hooks and
// Metrics all set, the failure log (code only — no stack, no value),
// OnOperationEnd.Code and pulse_operations_total all say
// PROCESSING_PANIC, the in-flight gauge returns to zero, and a Compose
// child the panic unwound past still gets its end.
//
// Falsified by dropping the (*run).end call in recoverOp (no end hook,
// no failure log, no metric) and by dropping endChildren (the Compose
// child never ends).
func TestPanicRecovery_ObservedAgree(t *testing.T) {
	ctx := context.Background()
	h := &captureHandler{}
	rec := &hookRecorder{}
	fm := newFakeMetrics()
	p := panicFixture(t, Options{Logger: h.logger(), Hooks: rec.hooks(), Metrics: fm})
	fm.freeze()
	h.take()
	rec.take()

	_, err := p.Process(ctx, panicAggRequest())
	assertPanicError(t, err, observe.OpProcess)

	recs := only(h.take(), logMsgFailed)
	if len(recs) != 1 || recs[0].attrs[logKeyCode] != string(errors.PROCESSING_PANIC) {
		t.Fatalf("failure records = %+v, want one with code PROCESSING_PANIC", recs)
	}
	out := h.output()
	for _, leak := range []string{panicSecret, "stack", "panic_type", ".go:"} {
		if strings.Contains(out, leak) {
			t.Errorf("log output carries %q: %s", leak, out)
		}
	}

	evs := rec.take()
	if len(evs) != 2 || !evs[0].start || evs[1].start {
		t.Fatalf("hook events = %+v, want one start and one end", evs)
	}
	if evs[1].res.Code != string(errors.PROCESSING_PANIC) {
		t.Errorf("OnOperationEnd.Code = %q, want PROCESSING_PANIC", evs[1].res.Code)
	}

	if v := fm.get(t, metricOperations, "op=process", "code=PROCESSING_PANIC", "scope=top").value; v != 1 {
		t.Errorf("operations_total{code=PROCESSING_PANIC} = %v, want 1", v)
	}
	if v := fm.get(t, metricInFlight, "op=process").value; v != 0 {
		t.Errorf("in-flight gauge = %v after a recovered panic, want 0", v)
	}

	// Compose: the panicking second slot's child op ends too.
	_, err = p.Compose(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), panicAggRequest()}})
	assertPanicError(t, err, observe.OpCompose)
	starts, ends := map[uint64]bool{}, map[uint64]string{}
	for _, e := range rec.take() {
		if e.start {
			starts[e.info.ID] = true
		} else {
			ends[e.info.ID] = e.res.Code
		}
	}
	if len(starts) != 3 {
		t.Fatalf("compose started %d operations, want 3 (parent + 2 slots)", len(starts))
	}
	for id := range starts {
		if _, ok := ends[id]; !ok {
			t.Errorf("operation %d started but never ended", id)
		}
	}
}

// TestPanicRecovery_OffPathNoAllocs (FR-9): the recover adds no
// allocation to the off path of observe, observed or observedStream.
// TestObservabilityOffNoAllocs keeps its per-operation pins; this adds
// observedStream, which that helper subtest does not cover.
//
// Falsified by inlining observedStreamOn into observedStream: the end
// closure's capture moves run to the heap, one allocation per off-path
// call.
func TestPanicRecovery_OffPathNoAllocs(t *testing.T) {
	p, _ := obsFixture(t, Options{})
	ctx := context.Background()
	req := obsRequest()
	allocs := testing.AllocsPerRun(200, func() {
		_ = p.observe(ctx, requestOp(observe.OpProcess, req), func(context.Context) error { return nil })
		_, _ = observed(p, ctx, requestOp(observe.OpProcess, req), func(context.Context) (int, error) { return 1, nil })
		_, _ = observedStream(p, ctx, requestOp(observe.OpProcessStream, req), func(context.Context, func(error)) (int, error) { return 1, nil })
	})
	if allocs != 0 {
		t.Fatalf("off-path helpers allocated %v per run, want 0", allocs)
	}
}

// TestPanicRecovery_HelpersBothPaths: each helper recovers on the off
// and the on path, and on the on path the operation ends with the code.
//
// Falsified by moving observedStream's defer below the observing()
// early return (the off subtest crashes).
func TestPanicRecovery_HelpersBothPaths(t *testing.T) {
	ctx := context.Background()
	boom := func() { panic(panicSecret) }
	helpers := map[string]func(p *Pulse) error{
		"observe": func(p *Pulse) error {
			return p.observe(ctx, opSpec{kind: observe.OpOpen}, func(context.Context) error { boom(); return nil })
		},
		"observed": func(p *Pulse) error {
			v, err := observed(p, ctx, opSpec{kind: observe.OpOpen}, func(context.Context) (*int, error) { boom(); return new(int), nil })
			if v != nil {
				t.Errorf("observed returned %v with the panic, want the zero value", v)
			}
			return err
		},
		"observedStream": func(p *Pulse) error {
			_, err := observedStream(p, ctx, opSpec{kind: observe.OpOpen}, func(context.Context, func(error)) (int, error) { boom(); return 0, nil })
			return err
		},
	}
	for name, h := range helpers {
		t.Run(name+"/off", func(t *testing.T) {
			p := recoveringFixture(t, Options{})
			assertPanicError(t, h(p), observe.OpOpen)
		})
		t.Run(name+"/on", func(t *testing.T) {
			rec := &hookRecorder{}
			p := recoveringFixture(t, Options{Hooks: rec.hooks()})
			assertPanicError(t, h(p), observe.OpOpen)
			evs := rec.take()
			if len(evs) != 2 || evs[1].res.Code != string(errors.PROCESSING_PANIC) {
				t.Fatalf("hook events = %+v, want start then end with PROCESSING_PANIC", evs)
			}
		})
	}
}

// TestPanicRecovery_DisableRepanics (FR-10, FR-20 facade half): with
// DisablePanicRecovery the facade re-panics with the ORIGINAL value, on
// the default and the observed instance; the observed one still ends
// the operation (code PROCESSING_PANIC) before the panic propagates.
//
// Falsified by dropping the `if p.disablePanicRecovery { panic(r) }`
// line (Process returns an error instead of panicking).
func TestPanicRecovery_DisableRepanics(t *testing.T) {
	ctx := context.Background()
	for _, observed := range []bool{false, true} {
		name := "default"
		if observed {
			name = "observed"
		}
		t.Run(name, func(t *testing.T) {
			rec := &hookRecorder{}
			opts := Options{DisablePanicRecovery: true}
			if observed {
				opts.Hooks = rec.hooks()
			}
			p := panicFixture(t, opts)
			rec.take()
			var got any
			func() {
				defer func() { got = recover() }()
				_, err := p.Process(ctx, panicAggRequest())
				t.Errorf("Process returned (err %v) instead of panicking", err)
			}()
			if got != panicSecret {
				t.Fatalf("re-panicked with %#v, want the original value %q", got, panicSecret)
			}
			if observed {
				evs := rec.take()
				if len(evs) != 2 || evs[1].res.Code != string(errors.PROCESSING_PANIC) {
					t.Errorf("hook events = %+v, want the operation ended with PROCESSING_PANIC", evs)
				}
			}
		})
	}
}

// TestPanicRecovery_ExprFunctionCoded (FR-14): a panicking embedder
// expr function is already recovered by expr-lang's VM into an error;
// this pins that it surfaces as a CODED error (never uncoded, never a
// crash), on Process with a FILTER_EXPRESSION, and that the coded
// error's text does not carry the panic value.
func TestPanicRecovery_ExprFunctionCoded(t *testing.T) {
	p := recoveringFixture(t, Options{ShardWorkers: 1, DecodeWorkers: 1, Extensions: Extensions{
		ExprFunctions: []ExprFunction{{Name: "acme_boom", Fn: func(x float64) float64 {
			if x > 0 {
				panic(panicSecret)
			}
			return x
		}}},
	}})
	req := &Request{
		Cohort:       &types.Cohort{Filename: obsCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "acme_boom(amount) > 1"}},
	}
	_, err := p.Process(context.Background(), req)
	if err == nil {
		t.Fatal("Process succeeded; want the expr function's panic as an error")
	}
	// Today: expr-lang's VM recovers it into an eval error, which the
	// filter coding wraps as PROCESSING_RUNTIME. That is already coded,
	// so it is not re-coded as PROCESSING_PANIC (one panic, one code:
	// the facade recover never sees it).
	if code := operationCode(err); code != string(errors.PROCESSING_RUNTIME) {
		t.Fatalf("expr-function panic surfaced as %s, want PROCESSING_RUNTIME: %v", code, err)
	}
	if strings.Contains(err.Error(), panicSecret) {
		t.Errorf("error text leaks the panic value: %v", err)
	}
}

// TestPanicRecovery_TestOverrideCrashes (FR-15): under this test binary
// TestMain forces recovery off, so an instance built WITHOUT
// enablePanicRecovery re-panics with the original value — an engine
// panic in an ordinary test crashes it rather than returning
// PROCESSING_PANIC — while the same Options re-enabled recover.
//
// Falsified by deleting the testForcePanicRecoveryOff assignment in
// TestMain (the ordinary instance returns an error), and by dropping
// `|| testForcePanicRecoveryOff` in New likewise.
func TestPanicRecovery_TestOverrideCrashes(t *testing.T) {
	ctx := context.Background()
	if !testForcePanicRecoveryOff {
		t.Fatal("TestMain did not set testForcePanicRecoveryOff")
	}
	opts := Options{Extensions: panicExtensions(), ShardWorkers: 1, DecodeWorkers: 1}
	ordinary, _ := obsFixture(t, opts)
	var got any
	func() {
		defer func() { got = recover() }()
		_, err := ordinary.Process(ctx, panicAggRequest())
		t.Errorf("ordinary instance returned (err %v) instead of crashing", err)
	}()
	if got != panicSecret {
		t.Fatalf("ordinary instance panicked with %#v, want the original value %q", got, panicSecret)
	}
	_, err := panicFixture(t, Options{}).Process(ctx, panicAggRequest())
	assertPanicError(t, err, observe.OpProcess)
}
