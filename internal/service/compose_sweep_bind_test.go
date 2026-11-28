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
				out, err = svc.ComposeParallel(context.Background(), sweepBindBatch(), ComposeOptions{MaxWorkers: 4})
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
// model file plus the winning axes (feature sweep_axes).
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
	var boundDoc, handDoc map[string]json.RawMessage
	if err := json.Unmarshal(boundFile, &boundDoc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(handFile, &handDoc); err != nil {
		t.Fatal(err)
	}
	var axes map[string]json.RawMessage
	_ = json.Unmarshal(boundDoc["sweep_axes"], &axes)
	if !bytes.Equal(bytes.Join(bytes.Fields(boundDoc["features"]), nil), []byte(`["sweep_axes"]`)) {
		t.Errorf("bound file features %s, want [sweep_axes]", boundDoc["features"])
	}
	if len(axes) != 1 || string(axes["hi"]) != "40" {
		t.Errorf("bound file sweep_axes %s, want {hi: 40}", boundDoc["sweep_axes"])
	}
	delete(boundDoc, "features")
	delete(boundDoc, "sweep_axes")
	for _, part := range []struct {
		name string
		a, b any
	}{
		{"fit", bound.Fits["best"], written.Fits["best"]},
		{"scored", bound.Responses[0].Data, written.Responses[0].Data},
		{"model file less sweep_axes", boundDoc, handDoc},
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

// phaseZeroFailBatch is sweepBindBatch with two explicit slots (0, 1)
// ahead of the three sweep slots (2, 3, 4), the sweep's slots 3 and 4
// failing at run time (a non-numeric FILTER_RANGE bound) — phase 0
// fails before either explicit slot could run.
func phaseZeroFailBatch() *types.ComposedRequest {
	c := sweepBindBatch()
	c.Requests = append(c.Requests, &types.Request{
		Label:        "plain",
		Cohort:       &types.Cohort{Filename: fitsCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	})
	c.Sweep.Axes = []types.SweepAxis{{Name: "hi", Values: []any{json.Number("80"), "bad", "worse"}}}
	return c
}

// TestComposeSweepBind_PhaseZeroFailureAggregate (#704): a non-fail-fast
// ComposeParallel whose phase 0 fails returns the uniform SERVICE_INTERNAL
// aggregate — failed_indices the failed sweep slots, skipped_indices the
// explicit slots that never ran, first_error — while a later-phase
// failure's aggregate carries no skipped_indices key; fail-fast keeps the
// failing slot's own located error, and serial Compose (fail-fast by
// construction) the same located error as its later phases.
func TestComposeSweepBind_PhaseZeroFailureAggregate(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	t.Run("parallel_no_fail_fast", func(t *testing.T) {
		resp, err := svc.ComposeParallel(context.Background(), phaseZeroFailBatch(), ComposeOptions{MaxWorkers: 4, CollectErrors: true})
		var ce *perr.CodedError
		if resp != nil || !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_INTERNAL {
			t.Fatalf("resp %v, err %v", resp, err)
		}
		if !reflect.DeepEqual(ce.Details["failed_indices"], []int{3, 4}) ||
			!reflect.DeepEqual(ce.Details["skipped_indices"], []int{0, 1}) ||
			ce.Details["first_error"] == nil || ce.Details["first_error"] == "" {
			t.Fatalf("details %v", ce.Details)
		}
	})
	t.Run("parallel_later_phase_has_no_skipped", func(t *testing.T) {
		c := sweepBindBatch()
		c.Requests[0].Cohort = &types.Cohort{Filename: "missing.pulse"}
		_, err := svc.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 4, CollectErrors: true})
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_INTERNAL || !reflect.DeepEqual(ce.Details["failed_indices"], []int{0}) {
			t.Fatalf("err %v", err)
		}
		if _, ok := ce.Details["skipped_indices"]; ok {
			t.Fatalf("a later-phase aggregate carries skipped_indices: %v", ce.Details)
		}
	})
	t.Run("fail_fast_located", func(t *testing.T) {
		serial, serr := svc.Compose(context.Background(), phaseZeroFailBatch())
		par, perr2 := svc.ComposeParallel(context.Background(), phaseZeroFailBatch(), ComposeOptions{MaxWorkers: 4})
		for name, run := range map[string]struct {
			resp *types.ComposedResponse
			err  error
		}{"serial": {serial, serr}, "parallel": {par, perr2}} {
			var ce *perr.CodedError
			if run.resp != nil || !stderrors.As(run.err, &ce) || ce.Code == perr.SERVICE_INTERNAL || ce.Details["request"] != 3 {
				t.Fatalf("%s: resp %v, err %v", name, run.resp, run.err)
			}
			if _, ok := ce.Details["skipped_indices"]; ok {
				t.Fatalf("%s: a located error carries skipped_indices: %v", name, ce.Details)
			}
		}
	})
}

// TestComposeSweepBind_PhaseZeroFailureRunsNoExplicitSlot: after a
// failed phase 0 no explicit slot runs and no bound fit is refitted —
// on Compose and on a non-fail-fast ComposeParallel alike.
func TestComposeSweepBind_PhaseZeroFailureRunsNoExplicitSlot(t *testing.T) {
	// serial: swaps the package-level service.fitRunObserver test hook; swaps the package-level service.slotModelsObserver test hook

	svc := fitsService(t)
	for _, mode := range []string{"serial", "parallel"} {
		t.Run(mode, func(t *testing.T) {
			o := observeBind(t)
			var err error
			if mode == "serial" {
				_, err = svc.Compose(context.Background(), phaseZeroFailBatch())
			} else {
				_, err = svc.ComposeParallel(context.Background(), phaseZeroFailBatch(), ComposeOptions{MaxWorkers: 4, CollectErrors: true})
			}
			if err == nil {
				t.Fatal("phase 0 did not fail")
			}
			for _, e := range []string{"slot:scored", "slot:plain", "fit:best"} {
				if o.count(e) != 0 {
					t.Errorf("%s ran after a failed phase 0: %v", e, o.events)
				}
			}
			if o.count("slot:upto_80") != 1 {
				t.Fatalf("vacuous: the sweep slot did not run: %v", o.events)
			}
		})
	}
}

// winnerFilesBody is a sweep body whose regression m (y on p) reads p
// from the model file its `models` entry "prior" loads — the path
// varying by the axis src.
const winnerFilesBody = `{"cohort":{"filename":"` + fitsCohort + `"},
	"models":[{"name":"prior","path":"models/{{src}}` + model.FileSuffix + `"}],
	"filterers":[{"type":"FILTER_RANGE","field":"week","values":["0","{{hi}}"]}],
	"attributes":[{"type":"ATTR_REG_PREDICT","label":"p","params":{"fit":"prior"}}],
	"regressions":[{"type":"REG_OLS","name":"m","target":"y","predictors":["p"]}]}`

// winnerFilesFit is the hand-written twin of the sweep body's slot
// (hi, src) as an authored fit request.
func winnerFilesFit(t *testing.T, hi, src string) *types.Request {
	t.Helper()
	body := strings.NewReplacer("{{hi}}", hi, "{{src}}", src).Replace(winnerFilesBody)
	var r types.Request
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

// TestComposeSweepBind_WinnerModelFiles (#579): when the sweep body's
// `models` paths vary by axis, the from_sweep fit binds the WINNING
// slot's loaded files, not slot 0's (the static request ResolveSweepFits
// reads). The two prior models are fitted over disjoint windows, so p —
// and the bound fit's coefficient on it — differs by file: the bound fit
// equals, to the byte, the hand-written fit of the winner's body (its
// file "late") and differs from slot 0's (file "early"), serial and
// parallel alike.
func TestComposeSweepBind_WinnerModelFiles(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	ctx := context.Background()
	for name, window := range map[string][2]string{"early": {"0", "20"}, "late": {"50", "103"}} {
		req := fitRequest(window[0], window[1])
		req.Regressions[0].Save = "models/" + name + model.FileSuffix
		if _, err := svc.Process(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	batch := func(best types.FitSpec) *types.ComposedRequest {
		c := sweepBindBatch()
		c.Fits[1] = best
		c.Sweep.Mode = types.SweepModeZip
		c.Sweep.Axes = []types.SweepAxis{
			{Name: "hi", Values: []any{json.Number("80"), json.Number("40"), json.Number("60")}},
			{Name: "src", Values: []any{"early", "late", "early"}},
		}
		c.Sweep.Request = json.RawMessage(winnerFilesBody)
		// The scoring slot derives best's predictor p from its own copy
		// of the winner's prior.
		c.Requests[0].Models = []types.ModelRef{{Name: "prior", Path: "models/late" + model.FileSuffix}}
		c.Requests[0].Attributes = append([]*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"prior"}`)}},
			c.Requests[0].Attributes...)
		return c
	}
	marshal := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	winner, err := svc.Compose(ctx, batch(types.FitSpec{Name: "best", Request: winnerFilesFit(t, "40", "late")}))
	if err != nil {
		t.Fatal(err)
	}
	slot0, err := svc.Compose(ctx, batch(types.FitSpec{Name: "best", Request: winnerFilesFit(t, "40", "early")}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(marshal(winner.Fits["best"]), marshal(slot0.Fits["best"])) {
		t.Fatal("vacuous: the two prior files fit the same model")
	}
	for _, mode := range []string{"serial", "parallel"} {
		t.Run(mode, func(t *testing.T) {
			c := batch(types.FitSpec{Name: "best", FromSweep: &types.FitFromSweep{Regression: "m"}})
			var out *types.ComposedResponse
			var err error
			if mode == "serial" {
				out, err = svc.Compose(ctx, c)
			} else {
				out, err = svc.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 4})
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Ranking) != 3 || out.Ranking[0].Slot != 1 || out.Ranking[0].Axes["src"] != "late" {
				t.Fatalf("vacuous: the winner must be slot 1 (src late): %+v", out.Ranking)
			}
			got := out.Fits["best"]
			if got == nil || got.RegressionResult == nil || got.Error != nil {
				t.Fatalf("fits.best = %+v", got)
			}
			if gb, wb := marshal(got), marshal(winner.Fits["best"]); !bytes.Equal(gb, wb) {
				t.Errorf("bound fit %s\n winner's hand-written fit %s", gb, wb)
			}
			if gb, wb := marshal(out.Responses[0].Data), marshal(winner.Responses[0].Data); !bytes.Equal(gb, wb) {
				t.Errorf("scored %s\n winner's hand-written %s", gb, wb)
			}
		})
	}
}
