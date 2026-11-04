package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_window_scalar_test.go pins a window's scalar-expression param
// reading the sweep winner (G8): a WIN_DISTRIBUTED_LAG decay of
// sweep_axis("best", "tv") in an explicit slot runs, on both
// orchestrators, exactly as the literal winning decay; predict lists it
// under unresolved_scalars; and a sweep slot whose window reads the fit
// its sweep binds is the from_sweep_window_cycle refusal everywhere.

// winnerDecaySrc reads the sweep-bound fit's winning decay axis.
const winnerDecaySrc = `sweep_axis("best", "tv")`

// adstockSlot is an explicit slot over the fits facade cohort: x's
// geometric adstock at decay (a number or an expression), summed.
func adstockSlot(decay string) *Request {
	return &Request{
		Label:  "adstock",
		Cohort: &types.Cohort{Filename: fitsFacadeCohort},
		Windows: []*types.Window{{
			Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "x_ad",
			OrderBy: []types.OrderKey{{Field: "week"}},
			Params:  json.RawMessage(`{"kernel": "geometric", "max_lag": 3, "decay": ` + decay + `}`),
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x_ad", Label: "s"}},
	}
}

// TestComposeWindowScalar_WinnerDecay: the slot's adstock sum at the
// winning decay equals, bit for bit, the same request run with the
// winning tv written as a literal — on Compose and ComposeParallel —
// and predict judges the batch valid with the decay unresolved.
func TestComposeWindowScalar_WinnerDecay(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := sweepAxisBatch(t)
	c.Optimize = nil
	c.Requests = []*Request{adstockSlot(strconv.Quote(winnerDecaySrc))}

	env, err := p.PredictCompose(ctx, c)
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
	res, ok := env.Data.(*ComposePredictResult)
	if !ok {
		t.Fatalf("predict data %T", env.Data)
	}
	want := []descriptor.UnresolvedScalar{{Path: "requests[0].windows[0].params.decay", Expression: winnerDecaySrc, Reads: []string{"sweep_axis"}}}
	if !reflect.DeepEqual(res.UnresolvedScalars, want) {
		t.Fatalf("unresolved_scalars %+v, want %+v", res.UnresolvedScalars, want)
	}

	var sums []float64
	for name, run := range map[string]func() (*ComposedResponse, error){
		"Compose":         func() (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func() (*ComposedResponse, error) { return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2}) },
	} {
		out, err := run()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(out.Ranking) != 2 {
			t.Fatalf("%s: ranking %+v", name, out.Ranking)
		}
		tv, ok := out.Ranking[0].Axes["tv"].(json.Number)
		if !ok {
			t.Fatalf("%s: winning axes %v", name, out.Ranking[0].Axes)
		}
		got, _ := out.Responses[0].Data[0]["s"].(float64)
		lit, err := p.Process(ctx, adstockSlot(tv.String()))
		if err != nil {
			t.Fatal(err)
		}
		wantSum, _ := lit.Data[0]["s"].(float64)
		if math.Float64bits(got) != math.Float64bits(wantSum) || got == 0 {
			t.Errorf("%s: winner-decay sum %v, literal decay %s sum %v", name, got, tv, wantSum)
		}
		sums = append(sums, got)
	}
	if len(sums) == 2 && math.Float64bits(sums[0]) != math.Float64bits(sums[1]) {
		t.Errorf("Compose %v and ComposeParallel %v differ", sums[0], sums[1])
	}
}

// TestComposeWindowScalar_SweepWindowCycle: a sweep body whose window
// reads the fit the sweep binds (its decay sweep_axis("best", "tv")) is
// PULSE_FIT_INVALID from_sweep_window_cycle — the same code, message
// and details in PredictCompose, Compose and ComposeParallel.
func TestComposeWindowScalar_SweepWindowCycle(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	body := json.RawMessage(`{"cohort":{"filename":"` + fitsFacadeCohort + `"},` +
		`"filterers":[{"type":"FILTER_RANGE","field":"week","values":["0","{{hi}}"]}],` +
		`"windows":[{"type":"WIN_LAG","field":"x","label":"x_l","order_by":[{"field":"week"}]},` +
		`{"type":"WIN_DISTRIBUTED_LAG","field":"x","label":"x_ad","order_by":[{"field":"week"}],"params":{"kernel":"geometric","max_lag":3,"decay":"sweep_axis(\"best\", \"tv\")"}}],` +
		`"regressions":[{"type":"REG_OLS","name":"m","target":"y","predictors":["x"]}]}`)
	c := sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")}, body, func(s *types.SweepSpec) {
		s.Mode = types.SweepModeZip
		s.Axes = append(s.Axes, types.SweepAxis{Name: "tv", Values: []any{json.Number("0.4"), json.Number("0.7")}})
		s.Label = "hi_{{hi}}_tv_{{tv}}"
	})
	ctx := context.Background()
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
	}
	pe := env.Errors[0]
	if perr.Code(pe.Code) != perr.PULSE_FIT_INVALID || pe.Details["reason"] != "from_sweep_window_cycle" ||
		pe.Details["slot"] != 0 || pe.Details["window"] != 1 || pe.Details["param"] != "decay" {
		t.Fatalf("predict %s %q %v", pe.Code, pe.Message, pe.Details)
	}
	for name, run := range map[string]func() error{
		"Compose": func() error { _, err := p.Compose(ctx, c); return err },
		"ComposeParallel": func() error {
			_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
			return err
		},
	} {
		var ce *perr.CodedError
		if err := run(); !stderrors.As(err, &ce) {
			t.Fatalf("%s: uncoded %v", name, err)
		}
		if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
			t.Errorf("%s: runtime %s %q %v\n predict %s %q %v", name, ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
		}
	}
}
