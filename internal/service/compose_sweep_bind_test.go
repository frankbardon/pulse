package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// compose_sweep_bind_test.go pins the run-time schedule of a
// sweep-bound fit (`fits[].from_sweep`, compose_sweep_bind.go): the
// authored fits, then the sweep slots once, then the bound fit refitted
// on the rank-1 slot, then the other slots — serial and parallel alike.

// sweepBindBatch is a ranked three-slot sweep of OLS y ~ x over weeks
// [0, hi] (hi 80, 40, 60 — so the winner, the smallest window by
// residual_std_err, is not slot 0), an authored fit "train", the fit
// "best" bound from the sweep's regression "m", and an explicit slot
// scoring "best".
func sweepBindBatch() *types.ComposedRequest {
	return &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "train", Request: fitRequest("0", "90")},
			{Name: "best", FromSweep: &types.FitFromSweep{Regression: "m"}},
		},
		Requests: []*types.Request{{
			Label:      "scored",
			Cohort:     &types.Cohort{Filename: fitsCohort},
			Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "yhat", Params: json.RawMessage(`{"fit":"best"}`)}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_AVERAGE, Field: "yhat", Label: "mean_yhat"},
			},
		}},
		Sweep: &types.SweepSpec{
			Axes:  []types.SweepAxis{{Name: "hi", Values: []any{json.Number("80"), json.Number("40"), json.Number("60")}}},
			Label: "upto_{{hi}}",
			Request: json.RawMessage(`{"cohort":{"filename":"` + fitsCohort + `"},
				"filterers":[{"type":"FILTER_RANGE","field":"week","values":["0","{{hi}}"]}],
				"regressions":[{"type":"REG_OLS","name":"m","target":"y","predictors":["x"]}]}`),
			Rank: &types.SweepRank{By: "regressions.m.residual_std_err"},
		},
	}
}

// bindEvents records, in order, every fit execution ("fit:<name>") and
// every slot run ("slot:<label>") with the bound names its set carries.
type bindEvents struct {
	mu     sync.Mutex
	events []string
	names  map[string][]string
}

func observeBind(t *testing.T) *bindEvents {
	t.Helper()
	o := &bindEvents{names: map[string][]string{}}
	fitRunObserver = func(name string) {
		o.mu.Lock()
		o.events = append(o.events, "fit:"+name)
		o.mu.Unlock()
	}
	slotModelsObserver = func(label string, set *model.Set) {
		o.mu.Lock()
		o.events = append(o.events, "slot:"+label)
		o.names[label] = set.Names()
		o.mu.Unlock()
	}
	t.Cleanup(func() { fitRunObserver, slotModelsObserver = nil, nil })
	return o
}

// at is the position of event e (-1 when absent); count its occurrences.
func (o *bindEvents) at(e string) int { return slices.Index(o.events, e) }
func (o *bindEvents) count(e string) int {
	n := 0
	for _, x := range o.events {
		if x == e {
			n++
		}
	}
	return n
}

// TestComposeSweepBind_SweepFirstSchedule: the authored fit runs first,
// then each sweep slot exactly once (over a set holding the authored fit
// only), then the bound fit, then the explicit slot over a set holding
// both; responses keep index order; the ranking's first entry is the
// bound slot — serial and parallel.
func TestComposeSweepBind_SweepFirstSchedule(t *testing.T) {
	// serial: swaps the package-level service.fitRunObserver test hook; swaps the package-level service.slotModelsObserver test hook

	svc := fitsService(t)
	sweepSlots := []string{"upto_80", "upto_40", "upto_60"}
	for _, mode := range []string{"serial", "parallel"} {
		t.Run(mode, func(t *testing.T) {
			o := observeBind(t)
			var out *types.ComposedResponse
			var err error
			if mode == "serial" {
				out, err = svc.Compose(context.Background(), sweepBindBatch())
			} else {
				out, err = svc.ComposeParallel(context.Background(), sweepBindBatch(), ComposeOptions{MaxWorkers: 4, FailFast: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, r := range out.Responses {
				labels = append(labels, r.Label)
			}
			if want := append([]string{"scored"}, sweepSlots...); !reflect.DeepEqual(labels, want) {
				t.Fatalf("responses %v, want %v", labels, want)
			}
			train, best, scored := o.at("fit:train"), o.at("fit:best"), o.at("slot:scored")
			for _, e := range []string{"fit:train", "fit:best", "slot:scored", "slot:upto_80", "slot:upto_40", "slot:upto_60"} {
				if o.count(e) != 1 {
					t.Fatalf("%s ran %d times: %v", e, o.count(e), o.events)
				}
			}
			for _, l := range sweepSlots {
				at := o.at("slot:" + l)
				if at < train || at > best {
					t.Errorf("sweep slot %s ran outside [fit:train, fit:best]: %v", l, o.events)
				}
				if !reflect.DeepEqual(o.names[l], []string{"train"}) {
					t.Errorf("sweep slot %s saw %v, want the authored fit only", l, o.names[l])
				}
			}
			if scored < best || !reflect.DeepEqual(o.names["scored"], []string{"best", "train"}) {
				t.Errorf("scored at %d (best at %d) saw %v", scored, best, o.names["scored"])
			}
			if len(out.Ranking) != 3 || out.Ranking[0].Slot == 0 || out.Ranking[0].Label != "upto_40" {
				t.Fatalf("vacuous: the winner must not be slot 0: %+v", out.Ranking)
			}
			if f := out.Fits["best"]; f == nil || f.RegressionResult == nil || f.Error != nil {
				t.Fatalf("fits.best = %+v", out.Fits["best"])
			}
		})
	}
}

// TestComposeSweepBind_EqualsHandWrittenFit: the bound fit's result is,
// to the byte, an authored fit of the winning slot's body; the slot
// scoring it reads the same figures; and its `save` writes the same
// model file.
func TestComposeSweepBind_EqualsHandWrittenFit(t *testing.T) {
	t.Parallel()

	cfg := setupTestFS(t, fitsCohort, fitsSchema(), fitsRecords())
	svc := New(cfg)
	boundBatch := sweepBindBatch()
	boundBatch.Fits[1].Save = "bound.pulse-model.json"
	bound, err := svc.Compose(context.Background(), boundBatch)
	if err != nil {
		t.Fatal(err)
	}
	hand := sweepBindBatch()
	hand.Fits[1] = types.FitSpec{Name: "best", Request: fitRequest("0", "40"), Save: "hand.pulse-model.json"}
	written, err := svc.Compose(context.Background(), hand)
	if err != nil {
		t.Fatal(err)
	}
	boundFile, err := afero.ReadFile(cfg.Fs(), "bound.pulse-model.json")
	if err != nil {
		t.Fatal(err)
	}
	handFile, err := afero.ReadFile(cfg.Fs(), "hand.pulse-model.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []struct {
		name string
		a, b any
	}{
		{"fit", bound.Fits["best"], written.Fits["best"]},
		{"scored", bound.Responses[0].Data, written.Responses[0].Data},
		{"model file", json.RawMessage(boundFile), json.RawMessage(handFile)},
	} {
		ab, _ := json.Marshal(part.a)
		bb, _ := json.Marshal(part.b)
		if !bytes.Equal(ab, bb) {
			t.Errorf("%s: bound %s\n hand-written %s", part.name, ab, bb)
		}
	}
}

// TestComposeSweepBind_SerialParallelByteIdentical: a sweep-bound batch
// marshals to the same bytes on both orchestrators.
func TestComposeSweepBind_SerialParallelByteIdentical(t *testing.T) {
	t.Parallel()

	got := composeBoth(t, fitsService(t), sweepBindBatch())
	sb, _ := json.Marshal(got["serial"])
	pb, _ := json.Marshal(got["parallel"])
	if !bytes.Equal(sb, pb) {
		t.Fatalf("serial %s\nparallel %s", sb, pb)
	}
}

// emptyRankBatch is sweepBindBatch over two-row windows: every slot's
// residual_std_err is undefined (null), so no slot is ranked.
func emptyRankBatch() *types.ComposedRequest {
	c := sweepBindBatch()
	c.Sweep.Mode = types.SweepModeZip
	c.Sweep.Axes = []types.SweepAxis{
		{Name: "lo", Values: []any{json.Number("0"), json.Number("10")}},
		{Name: "hi", Values: []any{json.Number("1"), json.Number("11")}},
	}
	c.Sweep.Request = json.RawMessage(strings.Replace(string(c.Sweep.Request), `"0","{{hi}}"`, `"{{lo}}","{{hi}}"`, 1))
	return c
}

// TestComposeSweepBind_EmptyRankingBindsFailed: with no ranked slot the
// bound fit binds FAILED — its entry carries a PULSE_SWEEP_RANK_PATH
// error (reason empty), a PULSE_SLOT_FAILED warning lifts with
// details.fit, the slot scoring it reads an all-null column with
// PULSE_FIT_UNAVAILABLE — and Strict fails the batch with that code.
func TestComposeSweepBind_EmptyRankingBindsFailed(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	for name, out := range composeBoth(t, svc, emptyRankBatch()) {
		if len(out.Ranking) != 0 {
			t.Fatalf("%s: ranking %+v", name, out.Ranking)
		}
		f := out.Fits["best"]
		if f == nil || f.RegressionResult == nil || f.Error == nil || f.Error.Code != string(perr.PULSE_SWEEP_RANK_PATH) ||
			f.Error.Details["reason"] != "empty" || f.Name != "m" || f.Type != types.REG_OLS {
			t.Fatalf("%s: fits.best = %+v", name, f)
		}
		warned := slices.ContainsFunc(out.Warnings, func(w *types.ResponseWarning) bool {
			return w.Code == string(perr.PULSE_SLOT_FAILED) && w.Details["fit"] == 1 && w.Details["code"] == string(perr.PULSE_SWEEP_RANK_PATH)
		})
		if !warned {
			t.Errorf("%s: no PULSE_SLOT_FAILED with details.fit: %+v", name, out.Warnings)
		}
		// The rank ran once (phase 0): each excluded slot is warned once.
		for _, r := range out.Responses[1:] {
			n := 0
			for _, w := range r.Warnings {
				if w.Code == string(perr.PULSE_SWEEP_RANK_PATH) {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%s: slot %s carries %d rank warnings, want 1", name, r.Label, n)
			}
		}
		scored := out.Responses[0]
		if v, ok := scored.Data[0]["mean_yhat"].(float64); ok && !math.IsNaN(v) {
			t.Errorf("%s: mean over the failed fit's score %v", name, v)
		}
		if !slices.ContainsFunc(scored.Warnings, func(w *types.ResponseWarning) bool { return w.Code == string(perr.PULSE_FIT_UNAVAILABLE) }) {
			t.Errorf("%s: no PULSE_FIT_UNAVAILABLE on the scoring slot: %+v", name, scored.Warnings)
		}
	}
	svc.SetStrict(true)
	defer svc.SetStrict(false)
	_, err := svc.Compose(context.Background(), emptyRankBatch())
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_SWEEP_RANK_PATH || ce.Details["fit"] != 1 {
		t.Fatalf("strict: %v", err)
	}
}

// TestComposeSweepBind_NoBindingKeepsSchedule: without a from_sweep
// entry the fits run before every slot, as always.
func TestComposeSweepBind_NoBindingKeepsSchedule(t *testing.T) {
	// serial: swaps the package-level service.fitRunObserver test hook; swaps the package-level service.slotModelsObserver test hook

	o := observeBind(t)
	c := sweepBindBatch()
	c.Fits = c.Fits[:1]
	c.Requests[0].Attributes[0].Params = json.RawMessage(`{"fit":"train"}`)
	if _, err := fitsService(t).Compose(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if o.at("fit:train") != 0 {
		t.Fatalf("events %v", o.events)
	}
}

// TestComposeSweepBind_RankReadsSlotLocalAdjustedP: phase 0 folds each
// sweep slot's own multiplicity family before ranking, so a binding
// ranks by a regression's p_adjusted exactly as the unbound sweep does
// (the batch's barrier fold has not run yet); a `compose` family, which
// pools slots that have not run, is left to the barrier — its adjusted
// figure is absent in phase 0, so ranking by it is refused, not misread.
func TestComposeSweepBind_RankReadsSlotLocalAdjustedP(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	withFamily := func(family string, bind bool) *types.ComposedRequest {
		c := sweepBindBatch()
		c.Sweep.Request = json.RawMessage(strings.Replace(string(c.Sweep.Request), `"predictors":["x"]`,
			`"predictors":["x","week"],"multiplicity":{"method":"holm","family":"`+family+`"}`, 1))
		c.Sweep.Rank.By = "regressions.m.p_adjusted.week"
		if !bind {
			c.Fits = c.Fits[:1]
			c.Requests[0].Attributes[0].Params = json.RawMessage(`{"fit":"train"}`)
		}
		return c
	}
	bound, err := svc.Compose(context.Background(), withFamily("request", true))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := svc.Compose(context.Background(), withFamily("request", false))
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.Ranking) != 3 || !reflect.DeepEqual(bound.Ranking, plain.Ranking) {
		t.Fatalf("bound ranking %+v\n unbound %+v", bound.Ranking, plain.Ranking)
	}
	if _, err := svc.Compose(context.Background(), withFamily("compose", false)); err != nil {
		t.Fatalf("the unbound compose family ranks at the barrier: %v", err)
	}
	_, err = svc.Compose(context.Background(), withFamily("compose", true))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_SWEEP_RANK_PATH {
		t.Fatalf("compose family: %v", err)
	}
}

// TestComposeSweepAxis_FailedFitUnavailable: with no ranked slot the
// bound fit has no winning axes — a formula reading sweep_axis() of it
// is an all-null column with PULSE_FIT_UNAVAILABLE (as coef() of a
// failed fit is), and a filter reading it is refused with the fit's own
// code plus details.fit.
func TestComposeSweepAxis_FailedFitUnavailable(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	c := emptyRankBatch()
	c.Requests[0].Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "lo", Expression: `sweep_axis("best", "lo") * 2`}}
	c.Requests[0].Aggregations = []*types.Aggregation{{Type: types.AGG_MAX, Field: "lo", Label: "max_lo"}}
	for name, out := range composeBoth(t, svc, c) {
		scored := out.Responses[0]
		if v, ok := scored.Data[0]["max_lo"].(float64); ok && !math.IsNaN(v) {
			t.Errorf("%s: max over the failed fit's axis %v", name, v)
		}
		if !slices.ContainsFunc(scored.Warnings, func(w *types.ResponseWarning) bool {
			return w.Code == string(perr.PULSE_FIT_UNAVAILABLE) && w.Details["fit"] == "best"
		}) {
			t.Errorf("%s: no PULSE_FIT_UNAVAILABLE: %+v", name, scored.Warnings)
		}
	}
	c = emptyRankBatch()
	c.Requests[0].Attributes = nil
	c.Requests[0].Aggregations = c.Requests[0].Aggregations[:1]
	c.Requests[0].Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `week > sweep_axis("best", "lo")`}}
	_, err := svc.Compose(context.Background(), c)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_SWEEP_RANK_PATH || ce.Details["fit"] != "best" {
		t.Fatalf("filter over the failed fit: %v", err)
	}
}
