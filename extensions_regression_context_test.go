package pulse_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Extension regressions honour the operation's ctx and may refuse
// resample (U62 E2-S2): a buffered model implementing
// extend.ContextRegression is fitted through FitContext — the run, every
// resample refit and the pulse.New probe — so a caller cancel or
// Limits.RequestTimeout reaches a long fit; a registration declaring
// NoResample is refused resample and its knobs with PROCESSING_CONFIG
// {regression, slot: "resample"} at predict and at runtime alike.

const (
	regCtxName      types.RegressionType = "REG_ACME_SLOPE_CTX"
	regNoResample   types.RegressionType = "REG_ACME_SLOPE_NORS"
	probeTargetName                      = "probe_y"
)

// ctxCalls counts how a ContextRegression model was driven.
type ctxCalls struct {
	fit, fitContext, probe atomic.Int64
	// block makes a non-probe FitContext wait for ctx to be done.
	block atomic.Bool
	// started receives once per blocking FitContext entry.
	started chan struct{}
}

// ctxSlope is a buffered slope that implements both Fit and FitContext:
// Fit is never expected (the adapter prefers FitContext) and fails
// loudly; FitContext fits the slope, or — under block, off the probe —
// waits for ctx to be done and returns its error.
type ctxSlope struct {
	acc   *slopeAcc
	calls *ctxCalls
	probe bool
}

func (m *ctxSlope) Fit(extend.Rows) (extend.RegressionOutput, error) {
	m.calls.fit.Add(1)
	return extend.RegressionOutput{}, stderrors.New("Fit called although the model implements FitContext")
}

func (m *ctxSlope) FitContext(ctx context.Context, rows extend.Rows) (extend.RegressionOutput, error) {
	m.calls.fitContext.Add(1)
	if m.probe {
		m.calls.probe.Add(1)
	}
	if m.calls.block.Load() && !m.probe {
		select {
		case m.calls.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return extend.RegressionOutput{}, ctx.Err()
	}
	for i := 0; i < rows.Len(); i++ {
		m.acc.add(rows.At(i), &regCalls{})
	}
	return m.acc.output(false)
}

func ctxExtensions(calls *ctxCalls) pulse.Extensions {
	factory := func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		return &ctxSlope{acc: newSlopeAcc(spec), calls: calls, probe: spec.Target == probeTargetName}, nil
	}
	return pulse.Extensions{Regressions: []pulse.RegressionRegistration{
		{Name: regCtxName, Description: "context-aware buffered slope", Factory: factory, PValues: true, OutputSchema: slopeOutputs},
	}}
}

func openCtxE2E(t *testing.T, calls *ctxCalls, l pulse.Limits) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ctxExtensions(calls), Limits: l})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, &types.Cohort{Filename: "e2e.pulse"}
}

// TestExtensions_RegressionFitContextPreferred: a model implementing
// both forms is probed and run through FitContext, never Fit, and its
// fit matches the plain buffered model's byte for byte (the plain-Fit
// path itself unchanged).
func TestExtensions_RegressionFitContextPreferred(t *testing.T) {
	calls := &ctxCalls{started: make(chan struct{}, 1)}
	p, cohort := openCtxE2E(t, calls, pulse.Limits{})
	if calls.probe.Load() == 0 || calls.fit.Load() != 0 {
		t.Fatalf("probe: FitContext probe calls %d, Fit calls %d; want the probe through FitContext only", calls.probe.Load(), calls.fit.Load())
	}
	before := calls.fitContext.Load()
	got := processE2E(t, p, regressionRequest(cohort, slopeSpec(regCtxName))).Regressions[0]
	if calls.fitContext.Load() != before+1 || calls.fit.Load() != 0 {
		t.Fatalf("run: FitContext calls +%d, Fit calls %d; want one FitContext", calls.fitContext.Load()-before, calls.fit.Load())
	}

	plain, plainCohort := openE2E(t, regressionExtensions(&regCalls{}))
	want := processE2E(t, plain, regressionRequest(plainCohort, slopeSpec(regBufferedName))).Regressions[0]
	got.Type, want.Type = "", ""
	if a, b := mustJSON(t, got), mustJSON(t, want); a != b {
		t.Errorf("FitContext fit differs from the plain Fit model:\n%s\n%s", a, b)
	}
}

// TestExtensions_RegressionFitContextRequestTimeout: a FitContext that
// blocks until ctx is done is stopped by Limits.RequestTimeout — the
// request returns the coded PULSE_LIMIT_EXCEEDED (limit
// request_timeout) promptly — on the plain fit and on a resample refit.
func TestExtensions_RegressionFitContextRequestTimeout(t *testing.T) {
	const d = 200 * time.Millisecond
	for _, tc := range []struct {
		name string
		mut  func(*types.RegressionSpec)
	}{
		{"plain", func(*types.RegressionSpec) {}},
		{"resample", func(s *types.RegressionSpec) { s.Resample = "jackknife" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &ctxCalls{started: make(chan struct{}, 1)}
			p, cohort := openCtxE2E(t, calls, pulse.Limits{RequestTimeout: d})
			calls.block.Store(true)
			spec := slopeSpec(regCtxName)
			tc.mut(spec)
			type result struct{ err error }
			done := make(chan result, 1)
			start := time.Now()
			go func() {
				_, err := p.Process(context.Background(), regressionRequest(cohort, spec))
				done <- result{err}
			}()
			select {
			case r := <-done:
				ce := requireCodedError(t, r.err, perr.PULSE_LIMIT_EXCEEDED)
				if ce.Details["limit"] != "request_timeout" {
					t.Errorf("details = %v, want limit request_timeout", ce.Details)
				}
				if el := time.Since(start); el > 5*time.Second {
					t.Errorf("returned after %v, want promptly after the %v timeout", el, d)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("RequestTimeout never reached FitContext: the request is still running")
			}
			if calls.fit.Load() != 0 {
				t.Errorf("Fit called %d times; want FitContext only", calls.fit.Load())
			}
		})
	}
}

// TestExtensions_RegressionFitContextCallerCancel: the caller's cancel
// reaches a blocked FitContext; the request returns context.Canceled.
func TestExtensions_RegressionFitContextCallerCancel(t *testing.T) {
	calls := &ctxCalls{started: make(chan struct{}, 1)}
	p, cohort := openCtxE2E(t, calls, pulse.Limits{RequestTimeout: time.Hour})
	calls.block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.Process(ctx, regressionRequest(cohort, slopeSpec(regCtxName)))
		done <- err
	}()
	select {
	case <-calls.started:
	case <-time.After(10 * time.Second):
		t.Fatal("FitContext never started")
	}
	cancel()
	select {
	case err := <-done:
		if !stderrors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the caller's cancel never reached FitContext")
	}
}

// noResampleExtensions registers the buffered slope twice: opted out of
// resample, and not.
func noResampleExtensions(seen **types.RegressionSpec) pulse.Extensions {
	factory := func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		if spec.Target != probeTargetName {
			*seen = spec
		}
		return &bufferedSlope{acc: newSlopeAcc(spec), calls: &regCalls{}}, nil
	}
	return pulse.Extensions{Regressions: []pulse.RegressionRegistration{
		{Name: regNoResample, Description: "slope that refuses resample", Factory: factory, PValues: true, NoResample: true, OutputSchema: slopeOutputs},
		{Name: regBufferedName, Description: "buffered slope", Factory: factory, PValues: true, OutputSchema: slopeOutputs},
	}}
}

// TestExtensions_RegressionNoResampleRefused: on a NoResample
// registration every opt-out key (resample and each knob, set alone) is
// refused PROCESSING_CONFIG {regression, slot: resample[, key], name} —
// the runtime's error and predict's first error identical — while the
// same request on a registration that does not opt out runs; rng_seed
// still reaches the factory; the manifest row drops the resample slot.
func TestExtensions_RegressionNoResampleRefused(t *testing.T) {
	var seen *types.RegressionSpec
	p, cohort := openE2E(t, noResampleExtensions(&seen))
	optOut := 0
	for key, idx := range regressionSpecKeys() {
		class, slot, ok := descx.RegressionFieldClassOf(key)
		if !ok || class != descx.RegressionFieldOptOut {
			continue
		}
		optOut++
		t.Run(key, func(t *testing.T) {
			if slot != "resample" {
				t.Fatalf("opt-out key %q governed by slot %q, want resample", key, slot)
			}
			spec := slopeSpec(regNoResample)
			f := reflect.ValueOf(spec).Elem().Field(idx)
			f.Set(regSlotValue(t, key, f.Type()))
			req := regressionRequest(cohort, spec)
			_, err := p.Process(context.Background(), req)
			ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
			want := map[string]any{"regression": string(regNoResample), "slot": "resample", "name": "fit"}
			if key != slot {
				want["key"] = key
			}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime details = %v, want %v", ce.Details, want)
			}
			pr, perrv := p.Predict(context.Background(), req)
			if perrv != nil {
				t.Fatalf("Predict: %v", perrv)
			}
			if len(pr.Errors) == 0 {
				t.Fatalf("predict accepted %s on a NoResample model", key)
			}
			if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict first error = %+v, runtime %s %q %v", got, ce.Code, ce.Message, ce.Details)
			}
		})
	}
	if optOut != 4 {
		t.Errorf("opt-out keys = %d, want resample + its three knobs", optOut)
	}

	// The model that does not opt out accepts resample, both sides.
	ok := slopeSpec(regBufferedName)
	ok.Resample = "jackknife"
	okReq := regressionRequest(cohort, ok)
	if res := processE2E(t, p, okReq).Regressions[0]; res.Resample != "jackknife" {
		t.Errorf("resample on an opted-in model: result resample %q", res.Resample)
	}
	if pr, err := p.Predict(context.Background(), okReq); err != nil || len(pr.Errors) != 0 {
		t.Fatalf("predict refused resample on an opted-in model: %v %+v", err, pr.Errors)
	}

	// rng_seed stays the model's own on a NoResample registration.
	seeded := slopeSpec(regNoResample)
	seeded.RNGSeed = 42
	seen = nil
	processE2E(t, p, regressionRequest(cohort, seeded))
	if seen == nil || seen.RNGSeed != 42 {
		t.Errorf("factory saw %+v, want rng_seed 42", seen)
	}

	m := p.Manifest(context.Background())
	slots := map[string][]string{}
	for _, row := range m.Extensions.Regressions {
		for _, prm := range row.Params {
			if prm.Slot {
				slots[row.Name] = append(slots[row.Name], prm.Name)
			}
		}
	}
	if slices.Contains(slots[string(regNoResample)], "resample") {
		t.Errorf("manifest lists resample on the NoResample model: %v", slots[string(regNoResample)])
	}
	if !slices.Contains(slots[string(regBufferedName)], "resample") {
		t.Errorf("manifest drops resample on the opted-in model: %v", slots[string(regBufferedName)])
	}
}
