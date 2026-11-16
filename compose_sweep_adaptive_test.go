package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/types"
)

// compose_sweep_adaptive_test.go pins the adaptive sweep executor at
// the facade with the deterministic test strategy (sweeptest, mode
// test_fixed: three points a round): the slot list sized to the budget,
// round-major slot indices, serial ≡ parallel, predict's budget /
// adaptive / round-0 labels / seed against the run, the refusals
// predict ≡ runtime, failed points penalised, fail-fast and strict
// aborts, the from_sweep binding of ranking[0] and result() reads.

// adaptiveSweep is an explicit slot "base" then an adaptive test_fixed
// sweep of budget slots over a range axis mu on [10, 60] and a list axis
// rep, ranked by the t statistic against mu (extra spliced into the
// sweep object).
func adaptiveSweep(t *testing.T, budget int, extra string) *ComposedRequest {
	t.Helper()
	return decodeComposedJSON(t, `{"requests":[{"label":"base","cohort":{"filename":"`+parityCohort+`"},
		"aggregations":[{"type":"AGG_AVERAGE","field":"age","label":"v"}]}],
		"sweep":{"mode":"`+string(sweeptest.Mode)+`","budget":`+fmt.Sprint(budget)+`,
		"axes":[{"name":"mu","range":{"min":10,"max":60}},{"name":"rep","values":[1,2]}],
		"label":"mu{{mu}}_r{{rep}}","request":`+rankSweepBody+`,"rank":{"by":"tests.t.statistic","order":"desc"}`+extra+`}}`)
}

// deSweep is adaptiveSweep under mode de (extra spliced into the sweep
// object).
func deSweep(t *testing.T, budget int, extra string) *ComposedRequest {
	t.Helper()
	c := adaptiveSweep(t, budget, extra)
	c.Sweep.Mode = types.SweepModeDE
	return c
}

func sweepLabels(resp *ComposedResponse) []string {
	var out []string
	for _, r := range resp.Responses {
		out = append(out, r.Label)
	}
	return out
}

// TestComposeSweepAdaptive_RunsBudgetInRounds: budget 7 runs exactly 7
// sweep slots after the explicit one, each labelled sample=<slot>_…;
// serial and parallel run the same slots and rank them identically
// (the next round reads the finished one, never completion order); the
// ranking covers every slot in rank order; the seed is echoed. Predict
// reports expanded_count 7, adaptive, round 0's labels exactly as the
// run gives them and null for the rest, and the run's seed.
func TestComposeSweepAdaptive_RunsBudgetInRounds(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	var first *ComposedResponse
	for name, run := range composeRuns(p) {
		resp, err := run(adaptiveSweep(t, 7, ""))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		labels := sweepLabels(resp)
		if len(labels) != 8 || labels[0] != "base" {
			t.Fatalf("%s: labels %v", name, labels)
		}
		for i, l := range labels[1:] {
			if !strings.HasPrefix(l, sweep.AdaptiveLabelPrefix(i)+"mu") {
				t.Errorf("%s: slot %d label %q", name, i, l)
			}
		}
		if len(resp.Ranking) != 7 || resp.SweepSeed == nil {
			t.Fatalf("%s: ranking %d entries, seed %v", name, len(resp.Ranking), resp.SweepSeed)
		}
		for i, e := range resp.Ranking {
			if e.Label != labels[1+e.Slot] || (i > 0 && e.Value > resp.Ranking[i-1].Value) || e.Axes["mu"] == nil {
				t.Errorf("%s: entry %d %+v out of order or unlabelled", name, i, e)
			}
		}
		if first == nil {
			first = resp
			continue
		}
		if !reflect.DeepEqual(sweepLabels(first), labels) || !reflect.DeepEqual(first.Ranking, resp.Ranking) {
			t.Fatalf("serial and parallel differ:\n%v\n%v", sweepLabels(first), labels)
		}
	}

	env, err := p.PredictCompose(context.Background(), adaptiveSweep(t, 7, ""))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	s := env.Data.(*ComposePredictResult).Sweep
	if s == nil || !s.Adaptive || s.ExpandedCount != 7 || s.Mode != sweeptest.Mode || s.RNGSeed == nil || *s.RNGSeed != *first.SweepSeed {
		t.Fatalf("predict summary %+v", s)
	}
	want := append(sweepLabels(first)[1:1+sweeptest.Batch], "", "", "", "")
	if !reflect.DeepEqual(s.Labels, want) {
		t.Fatalf("predict labels %q, want %q", s.Labels, want)
	}
	if s.Axes[0].Count != 7 || s.Axes[0].Range == nil || s.Axes[1].Count != 2 {
		t.Fatalf("predict axes %+v", s.Axes)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"adaptive":true`) || !strings.Contains(string(raw), `,null,null,null,null]`) {
		t.Fatalf("predict summary wire %s", raw)
	}
}

// TestComposeSweepAdaptive_RefusalParity: each adaptive refusal — no
// budget, no rank, a budget under a non-adaptive mode, a budget over
// MaxComposeSlots, a result() read of a later round's slot, each mode
// de knob out of range, `de` under another mode, a list value whose
// body does not decode (no round 0 slot drew it) — is predict ≡ both
// orchestrators: code, message and details.
func TestComposeSweepAdaptive_RefusalParity(t *testing.T) {
	t.Parallel()

	read := func(label string) string {
		return `,"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":0,"upper":10}],` +
			`"objective":"(x - 3) * (x - 3) + 0 * result(\"` + label + `\", \"v\")"}]`
	}
	withOpt := func(c *ComposedRequest, extra string) *ComposedRequest {
		raw, _ := json.Marshal(c)
		return decodeComposedJSON(t, strings.TrimSuffix(string(raw), "}")+extra+"}")
	}
	cases := []struct {
		name   string
		limits Limits
		c      *ComposedRequest
		code   perr.Code
		reason string
	}{
		{"no budget", Limits{}, adaptiveSweep(t, 0, ""), perr.PULSE_SWEEP_INVALID, sweep.ReasonAdaptiveRequiresBudget},
		{"no rank", Limits{}, func() *ComposedRequest { c := adaptiveSweep(t, 4, ""); c.Sweep.Rank = nil; return c }(), perr.PULSE_SWEEP_INVALID, sweep.ReasonAdaptiveRequiresRank},
		{"budget under grid", Limits{}, decodeComposedJSON(t, `{"sweep":{"budget":3,"axes":[{"name":"mu","values":[1,2]}],"request":`+rankSweepBody+`}}`),
			perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonBudgetWithoutAdaptive},
		{"budget over the slot limit", Limits{MaxComposeSlots: 5}, adaptiveSweep(t, 5, ""), perr.PULSE_LIMIT_EXCEEDED, ""},
		{"read of a later round", Limits{}, withOpt(adaptiveSweep(t, 5, ""), read(sweep.PendingLabel(4))), perr.PULSE_RESULT_INVALID, "slot_unknown"},
		// Mode de's knobs (E3-S3).
		{"de population below 4", Limits{}, deSweep(t, 10, `,"de":{"population":3}`), perr.PULSE_SWEEP_INVALID, sweep.ReasonDEPopulationInvalid},
		{"de population over budget", Limits{}, deSweep(t, 10, `,"de":{"population":11}`), perr.PULSE_SWEEP_INVALID, sweep.ReasonDEPopulationOverBudget},
		{"de f out of range", Limits{}, deSweep(t, 10, `,"de":{"f":2.5}`), perr.PULSE_SWEEP_INVALID, sweep.ReasonDEFInvalid},
		{"de cr out of range", Limits{}, deSweep(t, 10, `,"de":{"cr":-0.5}`), perr.PULSE_SWEEP_INVALID, sweep.ReasonDECRInvalid},
		{"de under test_fixed", Limits{}, adaptiveSweep(t, 5, `,"de":{"f":0.5}`), perr.PULSE_SWEEP_MODE_MISMATCH, sweep.ReasonDEWithoutDE},
		// A list value no round 0 slot drew, refused up front (E3-S3).
		{"undecodable list value", Limits{}, decodeComposedJSON(t, `{"sweep":{"mode":"`+string(sweeptest.Mode)+`","budget":7,"rng_seed":3,
			"axes":[{"name":"mu","range":{"min":10,"max":60}},{"name":"flag","values":[true,false,true,false,true,false,true,false,true,"nope"]}],
			"request":{"cohort":{"filename":"`+parityCohort+`"},"disable_components":{"$var":"flag"},
			"tests":[{"type":"TEST_T","field":"age","label":"t","params":{"mu":{"$var":"mu"}}}]},"rank":{"by":"tests.t.statistic"}}}`),
			perr.PULSE_SWEEP_INVALID, sweep.ReasonRequestDecode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := sweepPulse(t, Options{Limits: tc.limits})
			env, err := p.PredictCompose(context.Background(), tc.c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) || (tc.reason != "" && env.Errors[0].Details["reason"] != tc.reason) {
				t.Fatalf("predict errors %+v, want %s %s", env.Errors, tc.code, tc.reason)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(tc.c)
				ce := requireCoded(t, rerr, tc.code)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestComposeSweepAdaptive_ResultReadAfterPhaseZero: an optimize block
// reading a round-0 slot by its label passes predict and runs on both
// orchestrators, the slot run once (in phase 0), its value the one the
// block reads.
func TestComposeSweepAdaptive_ResultReadAfterPhaseZero(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	probe, err := p.Compose(context.Background(), adaptiveSweep(t, 5, ""))
	if err != nil {
		t.Fatal(err)
	}
	label := probe.Responses[1].Label // sweep slot 0: round 0
	c := adaptiveSweep(t, 5, "")
	c.Optimize = []types.OptimizeSpec{{Name: "o", Direction: types.OptimizeMinimize,
		Variables: []types.OptimizeVariable{{Name: "x", Lower: types.BoundExpr(`result("` + label + `", "v") - 1`), Upper: types.BoundExpr("100")}},
		Objective: "x * x"}}
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		v, _ := toFloat(resp.Responses[1].Data[0]["v"])
		if got := oneOptimum(t, name, resp).Variables[0].Value; got != v-1 || resp.Responses[1].Label != label {
			t.Errorf("%s: x = %v, want %v (slot %q)", name, got, v-1, resp.Responses[1].Label)
		}
	}
}

// TestComposeSweepAdaptive_HiddenParity: an instance offering the
// sampling sweep but not capability:compose_sweep_adaptive refuses an
// adaptive sweep — its `budget` as PULSE_REQUEST_UNKNOWN_FIELD, a bare
// adaptive mode as the unknown-mode PULSE_SWEEP_INVALID naming the
// other modes only — with the same code and details on Compose,
// ComposeParallel and PredictCompose; the unprofiled control runs it.
func TestComposeSweepAdaptive_HiddenParity(t *testing.T) {
	t.Parallel()

	fsys := parityFS(t)
	control := newParityHost(t, fsys, "")
	feats := []string{"capability:compose", "capability:compose_sweep", "capability:compose_sweep_sampling",
		string(types.AGG_AVERAGE), string(types.TEST_T), string(types.GROUP_CATEGORY)}
	h := newParityHostWith(t, fsys, "sweep-no-adaptive", parityHostConfig{
		profiles: map[string][]string{"sweep-no-adaptive": feats},
	})
	control.base = h.base
	ctx := context.Background()
	cases := map[string]struct {
		edit func(*ComposedRequest)
		code perr.Code
	}{
		"budget": {nil, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		"de knobs": {func(c *ComposedRequest) {
			c.Sweep.Mode, c.Sweep.DE = types.SweepModeDE, &types.SweepDE{CR: &[]float64{0.5}[0]}
		}, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		"mode de": {func(c *ComposedRequest) { c.Sweep.Mode, c.Sweep.Budget = types.SweepModeDE, 0 }, perr.PULSE_SWEEP_INVALID},
		"mode":    {func(c *ComposedRequest) { c.Sweep.Budget = 0 }, perr.PULSE_SWEEP_INVALID},
	}
	build := func(edit func(*ComposedRequest)) *ComposedRequest {
		c := adaptiveSweep(t, 4, "")
		if edit != nil {
			edit(c)
		}
		return c
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := h.p.Compose(ctx, build(c.edit))
			serial := slotGateFromErr(err)
			if serial == nil || serial.code != c.code {
				t.Fatalf("Compose: want %s, got %+v (%v)", c.code, serial, err)
			}
			if c.code == perr.PULSE_SWEEP_INVALID {
				if serial.details["reason"] != "mode_unknown" || !reflect.DeepEqual(toStrings(serial.details["valid"]), []string{"grid", "zip", "random", "lhs"}) {
					t.Fatalf("mode refusal details %v", serial.details)
				}
			}
			_, err = h.p.ComposeParallel(ctx, build(c.edit), ComposeOptions{MaxWorkers: 2})
			if par := slotGateFromErr(err); par == nil || par.code != serial.code || !reflect.DeepEqual(par.details, serial.details) {
				t.Fatalf("ComposeParallel %+v, Compose %+v", par, serial)
			}
			env, err := h.p.PredictCompose(ctx, build(c.edit))
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			pred := slotGateFromEnv(env)
			if pred == nil || pred.code != serial.code || !reflect.DeepEqual(toStrings(pred.details["unknown_keys"]), toStrings(serial.details["unknown_keys"])) ||
				pred.details["reason"] != serial.details["reason"] || !reflect.DeepEqual(toStrings(pred.details["valid"]), toStrings(serial.details["valid"])) {
				t.Fatalf("PredictCompose %+v, Compose %+v", pred, serial)
			}
		})
	}
	if out, err := control.p.Compose(ctx, build(nil)); err != nil || len(out.Responses) != 5 {
		t.Fatalf("control adaptive sweep: %v", err)
	}
}

// TestComposeSweepDE_RunsInGenerations: mode de with population 4 and
// budget 10 runs the population, a generation and a partial one (4, 4,
// 2 slots); serial and parallel run the same slots and rank them
// identically; predict reports the budget, round 0's labels and the
// run's seed.
func TestComposeSweepDE_RunsInGenerations(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	var first *ComposedResponse
	for name, run := range composeRuns(p) {
		resp, err := run(deSweep(t, 10, `,"de":{"population":4,"f":0.7,"cr":0.5}`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.Responses) != 11 || len(resp.Ranking) != 10 || resp.SweepSeed == nil {
			t.Fatalf("%s: %d responses, %d ranked", name, len(resp.Responses), len(resp.Ranking))
		}
		if first == nil {
			first = resp
			continue
		}
		if !reflect.DeepEqual(sweepLabels(first), sweepLabels(resp)) || !reflect.DeepEqual(first.Ranking, resp.Ranking) {
			t.Fatalf("serial and parallel differ:\n%v\n%v", sweepLabels(first), sweepLabels(resp))
		}
	}
	env, err := p.PredictCompose(context.Background(), deSweep(t, 10, `,"de":{"population":4,"f":0.7,"cr":0.5}`))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	s := env.Data.(*ComposePredictResult).Sweep
	want := append(sweepLabels(first)[1:5], "", "", "", "", "", "")
	if s == nil || !s.Adaptive || s.ExpandedCount != 10 || s.Mode != types.SweepModeDE || *s.RNGSeed != *first.SweepSeed || !reflect.DeepEqual(s.Labels, want) {
		t.Fatalf("predict summary %+v, labels want %q", s, want)
	}
}
