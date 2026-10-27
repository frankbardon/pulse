package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_sweep_axis_test.go pins sweep_axis(fit, axis) at the facade:
// a sweep-bound fit's winning axis values read from an explicit slot's
// formula and filter and from an optimize objective, on both
// orchestrators; and every static refusal (unknown fit, a fit not bound
// from the sweep, an unknown axis — in a slot and in an optimize block)
// reported by PredictCompose exactly as Compose and ComposeParallel
// return it.

// multiplierSrc is the geometric steady-state multiplier Σ_{l<4} d^l in
// closed form over the winning decay.
const multiplierSrc = `(1 - sweep_axis("best", "tv") ^ 4) / (1 - sweep_axis("best", "tv"))`

// sweepAxisBatch is a ranked zip sweep over the window hi ∈ {60, 90}
// paired with a decay tv ∈ {0.4, 0.7} (named in the label only), the fit
// "best" bound from its regression, and one explicit slot whose rows
// are week < the winning hi and whose formula is multiplierSrc.
func sweepAxisBatch(t *testing.T) *ComposedRequest {
	t.Helper()
	c := sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")}, sweepBindBody(t, `["x"]`, ""), func(s *types.SweepSpec) {
		s.Mode = types.SweepModeZip
		s.Axes = append(s.Axes, types.SweepAxis{Name: "tv", Values: []any{json.Number("0.4"), json.Number("0.7")}})
		s.Label = "hi_{{hi}}_tv_{{tv}}"
	})
	c.Requests = []*Request{{
		Label:      "readout",
		Cohort:     &types.Cohort{Filename: fitsFacadeCohort},
		Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `week < sweep_axis("best", "hi")`}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "mult", Expression: multiplierSrc}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "week", Label: "n"},
			{Type: types.AGG_MAX, Field: "mult", Label: "mult"},
		},
	}}
	c.Optimize = []types.OptimizeSpec{{
		Name:      "spend",
		Variables: []types.OptimizeVariable{{Name: "v", Lower: types.BoundValue(0), Upper: types.BoundValue(10)}},
		Objective: multiplierSrc + ` * v`,
		Direction: types.OptimizeMaximize,
	}}
	return c
}

// TestComposeSweepAxis_ReadsWinningAxes: the slot's filter keeps exactly
// the winning hi rows, its formula and the optimize objective equal the
// multiplier hand-computed from ranking[0].axes.tv, on both
// orchestrators, and predict judges the batch valid.
func TestComposeSweepAxis_ReadsWinningAxes(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := sweepAxisBatch(t)
	if env, err := p.PredictCompose(ctx, c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
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
		win := out.Ranking[0].Axes
		hi, err1 := win["hi"].(json.Number).Float64()
		d, err2 := win["tv"].(json.Number).Float64()
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: winning axes %v", name, win)
		}
		want := (1 - d*d*d*d) / (1 - d)
		row := out.Responses[0].Data[0]
		if n, _ := row["n"].(float64); n != hi {
			t.Errorf("%s: filter kept %v rows, want week < %v", name, row["n"], hi)
		}
		got, _ := row["mult"].(float64)
		if math.Abs(got-want) > 1e-12*want {
			t.Errorf("%s: formula multiplier %v, hand-computed %v (tv %v)", name, got, want, d)
		}
		if len(out.Optimizations) != 1 || math.Abs(out.Optimizations[0].Objective-10*want) > 1e-9*want {
			t.Errorf("%s: optimize %+v, want objective %v", name, out.Optimizations, 10*want)
		}
	}
}

// TestComposeSweepAxis_RefusalsMatchPredict: each static sweep_axis()
// refusal — mirroring coef()'s — is the same coded error in predict and
// on both orchestrators, located the same way.
func TestComposeSweepAxis_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	formula := func(src string) func(*ComposedRequest) {
		return func(c *ComposedRequest) { c.Requests[0].Attributes[0].Expression = src }
	}
	cases := []struct {
		name   string
		edit   func(*ComposedRequest)
		code   perr.Code
		reason string
		axes   []any
	}{
		{"unknown fit", formula(`sweep_axis("nope", "tv")`), perr.PULSE_MODEL_UNKNOWN, "", nil},
		{"not sweep bound", formula(`sweep_axis("plain", "tv")`), perr.PULSE_MODEL_TERM_UNKNOWN, "not_sweep_bound", nil},
		{"unknown axis in a formula", formula(`sweep_axis("best", "decay")`), perr.PULSE_MODEL_TERM_UNKNOWN, "axis_unknown", []any{"hi", "tv"}},
		{"unknown axis in a filter", func(c *ComposedRequest) {
			c.Requests[0].Filterers[0].Expression = `week < sweep_axis("best", "lo")`
		}, perr.PULSE_MODEL_TERM_UNKNOWN, "axis_unknown", []any{"hi", "tv"}},
		{"unknown axis in optimize", func(c *ComposedRequest) {
			c.Optimize[0].Objective = `sweep_axis("best", "decay") * v`
		}, perr.PULSE_MODEL_TERM_UNKNOWN, "axis_unknown", []any{"hi", "tv"}},
		{"not sweep bound in optimize", func(c *ComposedRequest) {
			c.Optimize[0].Objective = `sweep_axis("plain", "tv") * v`
		}, perr.PULSE_MODEL_TERM_UNKNOWN, "not_sweep_bound", nil},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := sweepAxisBatch(t)
			c.Fits = append(c.Fits, types.FitSpec{Name: "plain", Request: facadeFitRequest("0", "90")})
			tc.edit(c)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if perr.Code(pe.Code) != tc.code || (tc.reason != "" && pe.Details["reason"] != tc.reason) {
				t.Fatalf("predict %s %v, want %s %s", pe.Code, pe.Details, tc.code, tc.reason)
			}
			if tc.axes != nil && !reflect.DeepEqual(pe.Details["axes"], tc.axes) && !reflect.DeepEqual(pe.Details["axes"], []string{"hi", "tv"}) {
				t.Fatalf("predict details.axes %v", pe.Details["axes"])
			}
			runs := map[string]func() error{
				"Compose": func() error { _, err := p.Compose(ctx, c); return err },
				"ComposeParallel": func() error {
					_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
					return err
				},
			}
			for name, run := range runs {
				var ce *perr.CodedError
				if err := run(); !stderrors.As(err, &ce) {
					t.Fatalf("%s: uncoded %v", name, err)
				}
				if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
					t.Errorf("%s: runtime %s %q %v\n predict %s %q %v", name, ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
				}
			}
		})
	}
}

// TestComposeSweepAxis_NameReserved: an embedder may not register an
// expression function named sweep_axis.
func TestComposeSweepAxis_NameReserved(t *testing.T) {
	t.Parallel()

	_, err := New(Options{FS: fitsFacadeFS(t), Extensions: Extensions{
		ExprFunctions: []ExprFunction{{Name: "sweep_axis", Fn: func(a, b string) float64 { return 0 }}},
	}})
	if !perr.HasCode(err, perr.PULSE_EXTENSION_NAME_RESERVED) {
		t.Fatalf("embedder sweep_axis(): %v, want PULSE_EXTENSION_NAME_RESERVED", err)
	}
}

// TestComposeSweepAxis_PostWindowFormula: sweep_axis() in a formula that
// reads a window label of the same explicit slot (a post-window
// attribute: an adstock rebuilt from lag columns at the winning decay)
// resolves per row, on both orchestrators.
func TestComposeSweepAxis_PostWindowFormula(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := sweepAxisBatch(t)
	c.Optimize = nil
	c.Requests = []*Request{{
		Label:  "adstock",
		Cohort: &types.Cohort{Filename: fitsFacadeCohort},
		Windows: []*types.Window{{
			Type: types.WIN_LAG, Field: "x", Label: "x_l1",
			OrderBy: []types.OrderKey{{Field: "week"}},
			Params:  json.RawMessage(`{"offset": 1, "default": 0}`),
		}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "ad", Expression: `x + sweep_axis("best", "tv") * x_l1`}},
	}}
	if env, err := p.PredictCompose(ctx, c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
	for name, run := range map[string]func() (*ComposedResponse, error){
		"Compose":         func() (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func() (*ComposedResponse, error) { return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2}) },
	} {
		out, err := run()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d, err := out.Ranking[0].Axes["tv"].(json.Number).Float64()
		if err != nil {
			t.Fatal(err)
		}
		rows := out.Responses[0].Data
		if len(rows) < 2 {
			t.Fatalf("%s: %d rows", name, len(rows))
		}
		for i, row := range rows {
			x, _ := row["x"].(float64)
			lag, _ := row["x_l1"].(float64)
			got, ok := row["ad"].(float64)
			if want := x + d*lag; !ok || math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
				t.Fatalf("%s: row %d ad %v, want x + %v·x_l1 = %v", name, i, row["ad"], d, want)
			}
		}
	}
}
