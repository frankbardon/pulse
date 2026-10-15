package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// fitsCohort is a 104-week cohort: week, x and y = 3 + 2x + a week
// trend + deterministic noise, so a fit over weeks 0–90 and a fit over
// every week disagree.
const fitsCohort = "fits.pulse"

func fitsSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 4},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 12},
	}}
}

func fitsRecords() [][]uint64 {
	out := make([][]uint64, 104)
	for i := range out {
		x := float64((i*37)%17) + 0.25*float64(i%3)
		y := 3 + 2*x + 0.05*float64(i)*float64(i)/100 + 0.1*float64((i*13)%7-3)
		out[i] = []uint64{uint64(i), math.Float64bits(x), math.Float64bits(y)}
	}
	return out
}

func fitsService(t *testing.T) *Service {
	t.Helper()
	return New(setupTestFS(t, fitsCohort, fitsSchema(), fitsRecords()))
}

// fitRequest is an OLS of y on x over weeks [lo, hi].
func fitRequest(lo, hi string) *types.Request {
	return &types.Request{
		Cohort:      &types.Cohort{Filename: fitsCohort},
		Filterers:   []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{lo, hi}}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}}},
	}
}

func fitsSlot(label, lo, hi string) *types.Request {
	return &types.Request{
		Label:        label,
		Cohort:       &types.Cohort{Filename: fitsCohort},
		Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{lo, hi}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "sum_y"}},
	}
}

// fitsBatch: two fits, three explicit slots and a four-slot sweep.
func fitsBatch() *types.ComposedRequest {
	return &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "train", Request: fitRequest("0", "90")},
			{Name: "all", Request: fitRequest("0", "103")},
		},
		Requests: []*types.Request{
			fitsSlot("train_rows", "0", "90"),
			fitsSlot("holdout_rows", "91", "103"),
			fitsSlot("every_row", "0", "103"),
		},
		Sweep: &types.SweepSpec{
			Axes:  []types.SweepAxis{{Name: "hi", Values: []any{json.Number("20"), json.Number("40"), json.Number("60"), json.Number("80")}}},
			Label: "upto_{{hi}}",
			Request: json.RawMessage(`{"cohort":{"filename":"` + fitsCohort + `"},
				"filterers":[{"type":"FILTER_RANGE","field":"week","values":["0","{{hi}}"]}],
				"aggregations":[{"type":"AGG_SUM","field":"y","label":"sum_y"}]}`),
		},
	}
}

// fitObservers installs the run counter and the per-slot set recorder
// for one test and removes them after.
type fitObservation struct {
	mu    sync.Mutex
	runs  map[string]int
	slots map[string]*model.Set
}

func observeFits(t *testing.T) *fitObservation {
	t.Helper()
	o := &fitObservation{runs: map[string]int{}, slots: map[string]*model.Set{}}
	fitRunObserver = func(name string) {
		o.mu.Lock()
		o.runs[name]++
		o.mu.Unlock()
	}
	slotModelsObserver = func(label string, set *model.Set) {
		o.mu.Lock()
		o.slots[label] = set
		o.mu.Unlock()
	}
	t.Cleanup(func() { fitRunObserver, slotModelsObserver = nil, nil })
	return o
}

func composeBoth(t *testing.T, svc *Service, c *types.ComposedRequest) map[string]*types.ComposedResponse {
	t.Helper()
	out := map[string]*types.ComposedResponse{}
	serial, err := svc.Compose(context.Background(), c)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	out["serial"] = serial
	par, err := svc.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 4, FailFast: true})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	out["parallel"] = par
	return out
}

// TestComposeFits_RunOncePerFitAcrossSlotsAndSweep: with three explicit
// slots and a four-slot sweep, each fit executes exactly once per
// compose — serial and parallel — and every one of the seven slots
// carries the SAME bound set whose coefficients are the fit's own.
func TestComposeFits_RunOncePerFitAcrossSlotsAndSweep(t *testing.T) {
	svc := fitsService(t)
	for _, mode := range []string{"serial", "parallel"} {
		t.Run(mode, func(t *testing.T) {
			o := observeFits(t)
			var resp *types.ComposedResponse
			var err error
			if mode == "serial" {
				resp, err = svc.Compose(context.Background(), fitsBatch())
			} else {
				resp, err = svc.ComposeParallel(context.Background(), fitsBatch(), ComposeOptions{MaxWorkers: 4, FailFast: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Responses) != 7 {
				t.Fatalf("%d responses, want 7", len(resp.Responses))
			}
			if want := map[string]int{"train": 1, "all": 1}; !reflect.DeepEqual(o.runs, want) {
				t.Fatalf("fit executions = %v, want %v", o.runs, want)
			}
			labels := []string{"train_rows", "holdout_rows", "every_row", "upto_20", "upto_40", "upto_60", "upto_80"}
			var first *model.Set
			for _, l := range labels {
				set := o.slots[l]
				if set == nil {
					t.Fatalf("slot %s carried no bound set (got %v)", l, o.slots)
				}
				if first == nil {
					first = set
				} else if set != first {
					t.Errorf("slot %s carries a different set", l)
				}
			}
			if got := first.Names(); !reflect.DeepEqual(got, []string{"all", "train"}) {
				t.Fatalf("bound names = %v", got)
			}
			for name, res := range resp.Fits {
				b, err := first.Lookup(name)
				if err != nil {
					t.Fatal(err)
				}
				if b.Intercept != res.Coefficients[model.InterceptKey] || b.Coefficients[0] != res.Coefficients["x"] {
					t.Errorf("%s: bound (%v, %v) != result %v", name, b.Intercept, b.Coefficients, res.Coefficients)
				}
				if !reflect.DeepEqual(b.Predictors, []string{"x"}) {
					t.Errorf("%s: predictors %v", name, b.Predictors)
				}
			}
			if len(resp.Fits) != 2 {
				t.Fatalf("fits = %v", resp.Fits)
			}
		})
	}
}

// TestComposeFits_SerialParallelByteIdentical: the whole ComposedResponse
// — slots and fits — marshals to the same bytes serial and parallel.
func TestComposeFits_SerialParallelByteIdentical(t *testing.T) {
	got := composeBoth(t, fitsService(t), fitsBatch())
	sb, err := json.Marshal(got["serial"])
	if err != nil {
		t.Fatal(err)
	}
	pb, err := json.Marshal(got["parallel"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sb, pb) {
		t.Fatalf("serial and parallel differ:\nserial   %s\nparallel %s", sb, pb)
	}
	if !bytes.Contains(sb, []byte(`"fits":{"all":`)) {
		t.Fatalf("no fits on the wire: %s", sb)
	}
}

// TestComposeFits_FitIndependentOfSlot: a fit over weeks 0–90 equals the
// same request run alone, whatever the consuming slot filters (91–103
// here), and differs from a fit over every week — so the fit read its
// own filter, not a slot's.
func TestComposeFits_FitIndependentOfSlot(t *testing.T) {
	svc := fitsService(t)
	alone, err := svc.Process(context.Background(), fitRequest("0", "90"))
	if err != nil {
		t.Fatal(err)
	}
	everyWeek, err := svc.Process(context.Background(), fitRequest("0", "103"))
	if err != nil {
		t.Fatal(err)
	}
	c := &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "train", Request: fitRequest("0", "90")}},
		Requests: []*types.Request{fitsSlot("holdout", "91", "103")},
	}
	for mode, resp := range composeBoth(t, svc, c) {
		got := resp.Fits["train"]
		if got.Vector != nil || !reflect.DeepEqual(got.RegressionResult, alone.Regressions[0]) {
			t.Errorf("%s: fit = %+v, want the standalone fit %+v", mode, got, alone.Regressions[0])
		}
		if reflect.DeepEqual(got.Coefficients, everyWeek.Regressions[0].Coefficients) {
			t.Errorf("%s: vacuous — the 0–90 fit equals the all-weeks fit", mode)
		}
		if n := got.NObs; n != 91 {
			t.Errorf("%s: fit n_obs = %d, want 91", mode, n)
		}
		holdout := resp.Responses[0].Data[0]["sum_y"].(float64)
		if holdout <= 0 {
			t.Errorf("%s: holdout slot sum_y = %v", mode, holdout)
		}
	}
}

// TestComposeFits_NoFitsBindsNothing: a fit-free batch carries no set
// into its slots and no fits on the response.
func TestComposeFits_NoFitsBindsNothing(t *testing.T) {
	o := observeFits(t)
	c := &types.ComposedRequest{Requests: []*types.Request{fitsSlot("a", "0", "10")}}
	for mode, resp := range composeBoth(t, fitsService(t), c) {
		if resp.Fits != nil {
			t.Errorf("%s: fits = %v", mode, resp.Fits)
		}
	}
	if len(o.runs) != 0 || o.slots["a"] != nil {
		t.Fatalf("runs %v, slot set %v", o.runs, o.slots["a"])
	}
}

// TestComposeFits_Refusals: every structural refusal is coded, located
// by details.fit, raised before any fit runs, serial and parallel alike.
func TestComposeFits_Refusals(t *testing.T) {
	twoRegs := fitRequest("0", "90")
	twoRegs.Regressions = append(twoRegs.Regressions, &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"week"}})
	noRegs := fitRequest("0", "90")
	noRegs.Regressions = nil
	grouped := fitRequest("0", "90")
	grouped.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 52}}
	cases := []struct {
		name   string
		fits   []types.FitSpec
		code   perr.Code
		reason string
		fit    int
	}{
		{"zero regressions", []types.FitSpec{{Name: "a", Request: noRegs}}, perr.PULSE_FIT_INVALID, model.ReasonRegressionCount, 0},
		{"two regressions", []types.FitSpec{{Name: "a", Request: fitRequest("0", "9")}, {Name: "b", Request: twoRegs}}, perr.PULSE_FIT_INVALID, model.ReasonRegressionCount, 1},
		{"grouped", []types.FitSpec{{Name: "a", Request: grouped}}, perr.PULSE_FIT_INVALID, model.ReasonGrouped, 0},
		{"no name", []types.FitSpec{{Request: fitRequest("0", "9")}}, perr.PULSE_FIT_INVALID, model.ReasonNameMissing, 0},
		{"no request", []types.FitSpec{{Name: "a"}}, perr.PULSE_FIT_INVALID, model.ReasonRequestMissing, 0},
		{"duplicate name", []types.FitSpec{{Name: "a", Request: fitRequest("0", "9")}, {Name: "a", Request: fitRequest("0", "19")}}, perr.PULSE_MODEL_NAME_COLLISION, "", 1},
	}
	svc := fitsService(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := observeFits(t)
			c := &types.ComposedRequest{Fits: tc.fits, Requests: []*types.Request{fitsSlot("s", "0", "9")}}
			runs := map[string]func() error{
				"serial": func() error { _, err := svc.Compose(context.Background(), c); return err },
				"parallel": func() error {
					_, err := svc.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 2, FailFast: true})
					return err
				},
			}
			for mode, run := range runs {
				var ce *perr.CodedError
				if err := run(); !stderrors.As(err, &ce) || ce.Code != tc.code {
					t.Fatalf("%s: want %s, got %v", mode, tc.code, err)
				}
				if ce.Details["fit"] != tc.fit {
					t.Errorf("%s: details.fit = %v, want %d", mode, ce.Details["fit"], tc.fit)
				}
				if tc.reason != "" && ce.Details["reason"] != tc.reason {
					t.Errorf("%s: reason = %v, want %s", mode, ce.Details["reason"], tc.reason)
				}
			}
			if len(o.runs) != 0 {
				t.Fatalf("a fit ran before the refusal: %v", o.runs)
			}
		})
	}
}

// TestComposeFits_InnerRefusalKeepsCode: a fit whose own request is
// refused keeps that refusal's code and gains details.fit; with two
// failing fits the lower index wins, serial and parallel.
func TestComposeFits_InnerRefusalKeepsCode(t *testing.T) {
	bad := func() *types.Request {
		r := fitRequest("0", "90")
		r.Regressions[0].Predictors = []string{"nope"}
		return r
	}
	c := &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "ok", Request: fitRequest("0", "90")},
			{Name: "bad1", Request: bad()},
			{Name: "bad2", Request: bad()},
		},
		Requests: []*types.Request{fitsSlot("s", "0", "9")},
	}
	svc := fitsService(t)
	alone, aloneErr := svc.Process(context.Background(), bad())
	if aloneErr == nil {
		t.Fatalf("vacuous: the bad fit request runs alone (%v)", alone)
	}
	var want *perr.CodedError
	if !stderrors.As(aloneErr, &want) {
		t.Fatalf("standalone refusal is uncoded: %v", aloneErr)
	}
	runs := map[string]func() error{
		"serial": func() error { _, err := svc.Compose(context.Background(), c); return err },
		"parallel": func() error {
			_, err := svc.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 3, FailFast: true})
			return err
		},
	}
	for mode, run := range runs {
		var ce *perr.CodedError
		if err := run(); !stderrors.As(err, &ce) {
			t.Fatalf("%s: uncoded %v", mode, err)
		}
		if ce.Code != want.Code || ce.Message != want.Message {
			t.Errorf("%s: got %s %q, want %s %q", mode, ce.Code, ce.Message, want.Code, want.Message)
		}
		if ce.Details["fit"] != 1 {
			t.Errorf("%s: details.fit = %v, want 1", mode, ce.Details["fit"])
		}
	}
}

// TestComposeFits_CallerRequestUntouched: running fits never mutates the
// caller's fit request (its `return` is cleared on a clone only).
func TestComposeFits_CallerRequestUntouched(t *testing.T) {
	r := fitRequest("0", "90")
	r.Return = &types.Return{Exclude: []string{"regressions"}}
	c := &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "train", Request: r}},
		Requests: []*types.Request{fitsSlot("s", "0", "9")},
	}
	resp, err := fitsService(t).Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fits["train"] == nil {
		t.Fatal("a fit request's `return` skipped the fit")
	}
	if r.Return == nil || len(r.Return.Exclude) != 1 {
		t.Fatal("caller's fit request was mutated")
	}
}
