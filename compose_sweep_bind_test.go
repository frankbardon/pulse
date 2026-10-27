package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// compose_sweep_bind_test.go pins the static surface of a sweep-bound
// fit (`fits[].from_sweep`) at the facade: every from_sweep refusal is
// the same coded error in PredictCompose, Compose and ComposeParallel;
// a valid binding predicts valid with the fit summarised from the sweep
// body and its name resolvable by ATTR_REG_PREDICT, coef() and an
// optimize block; the batch binds the winner at run time; and an
// instance hiding capability:compose_sweep_bind refuses the key as an
// unknown field.

// sweepBindBody is a sweep body over the fits facade cohort: the rows
// up to week {{hi}} and a regression named "m" of y on predictors.
func sweepBindBody(t *testing.T, predictors string, extra string) json.RawMessage {
	t.Helper()
	body := `{"cohort":{"filename":"` + fitsFacadeCohort + `"},` +
		`"filterers":[{"type":"FILTER_RANGE","field":"week","values":["0","{{hi}}"]}` + extra + `],` +
		`"regressions":[{"type":"REG_OLS","name":"m","target":"y","predictors":` + predictors + `}]}`
	if !json.Valid([]byte(body)) {
		t.Fatalf("sweep body: %s", body)
	}
	return json.RawMessage(body)
}

// sweepBindBatch is a ranked two-slot sweep plus fits; edit adjusts the
// sweep (nil keeps it).
func sweepBindBatch(t *testing.T, fits []types.FitSpec, body json.RawMessage, edit func(*types.SweepSpec)) *ComposedRequest {
	t.Helper()
	sw := &types.SweepSpec{
		Axes:    []types.SweepAxis{{Name: "hi", Values: []any{json.Number("60"), json.Number("90")}}},
		Label:   "hi_{{hi}}",
		Request: body,
		Rank:    &types.SweepRank{By: "regressions.m.residual_std_err"},
	}
	if edit != nil {
		edit(sw)
	}
	return &ComposedRequest{Fits: fits, Sweep: sw, Requests: []*Request{facadeFitSlot("s")}}
}

func sweepBound(name, regression string) types.FitSpec {
	return types.FitSpec{Name: name, FromSweep: &types.FitFromSweep{Regression: regression}}
}

// TestComposeSweepBind_RefusalsMatchPredict: each from_sweep
// PULSE_FIT_INVALID reason is reported by PredictCompose exactly as
// Compose and ComposeParallel return it — code, message and details
// (details.fit and details.reason included).
func TestComposeSweepBind_RefusalsMatchPredict(t *testing.T) {
	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	withReq := sweepBound("best", "m")
	withReq.Request = facadeFitRequest("0", "90")
	plain := sweepBindBody(t, `["x"]`, "")
	cases := []struct {
		reason string
		batch  *ComposedRequest
	}{
		{"from_sweep_with_request", sweepBindBatch(t, []types.FitSpec{withReq}, plain, nil)},
		{"from_sweep_no_sweep", &ComposedRequest{Fits: []types.FitSpec{sweepBound("best", "m")}, Requests: []*Request{facadeFitSlot("s")}}},
		{"from_sweep_no_rank", sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")}, plain, func(s *types.SweepSpec) { s.Rank = nil })},
		{"from_sweep_regression_unknown", sweepBindBatch(t, []types.FitSpec{sweepBound("best", "nope")}, plain, nil)},
		{"from_sweep_signature_varies", sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")}, sweepBindBody(t, `["{{pred}}"]`, ""), func(s *types.SweepSpec) {
			s.Mode = types.SweepModeZip
			s.Axes = append(s.Axes, types.SweepAxis{Name: "pred", Values: []any{"x", "week"}})
		})},
		{"from_sweep_cycle", sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")},
			sweepBindBody(t, `["x"]`, `,{"type":"FILTER_EXPRESSION","expression":"coef(\"best\", \"x\") > 0"}`), nil)},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, tc.batch)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if perr.Code(pe.Code) != perr.PULSE_FIT_INVALID || pe.Details["reason"] != tc.reason || pe.Details["fit"] != 0 {
				t.Fatalf("predict %s %v", pe.Code, pe.Details)
			}
			runs := map[string]func() error{
				"Compose": func() error { _, err := p.Compose(ctx, tc.batch); return err },
				"ComposeParallel": func() error {
					_, err := p.ComposeParallel(ctx, tc.batch, ComposeOptions{MaxWorkers: 2, FailFast: true})
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

// TestPredictCompose_SweepBoundFit: a valid binding predicts valid, the
// fit summarised from the sweep body's regression (with its from_sweep
// echo), and the bound name resolves for ATTR_REG_PREDICT, coef() in a
// slot filter and an optimize block with no data; a name nothing binds
// is still PULSE_MODEL_UNKNOWN.
func TestPredictCompose_SweepBoundFit(t *testing.T) {
	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	batch := func(fit string) *ComposedRequest {
		c := sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")}, sweepBindBody(t, `["x"]`, ""), nil)
		c.Requests = []*Request{{
			Label:     "scored",
			Cohort:    &types.Cohort{Filename: fitsFacadeCohort},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `y > coef("` + fit + `", "x")`}},
			Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "yhat",
				Params: json.RawMessage(`{"fit":"` + fit + `"}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "yhat", Label: "sum_yhat"}},
		}}
		c.Optimize = []types.OptimizeSpec{{
			Name:      "x_star",
			Variables: []types.OptimizeVariable{{Name: "v", Lower: types.BoundValue(0), Upper: types.BoundValue(10)}},
			Objective: `coef("` + fit + `", "(intercept)") + coef("` + fit + `", "x") * v`,
			Direction: types.OptimizeMaximize,
		}}
		return c
	}
	env, err := p.PredictCompose(context.Background(), batch("best"))
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*ComposePredictResult)
	if len(env.Errors) > 0 || !res.Valid {
		t.Fatalf("valid=%v errors=%+v", res.Valid, env.Errors)
	}
	want := []descriptor.FitSummary{{Name: "best", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"},
		Cohort: fitsFacadeCohort, FromSweep: &types.FitFromSweep{Regression: "m"}}}
	if !reflect.DeepEqual(res.Fits, want) {
		t.Fatalf("fits %+v", res.Fits)
	}
	// The request as written keeps its from_sweep entry request-free.
	if res.Request.Fits[0].Request != nil {
		t.Fatal("the written request echo gained the resolved fit request")
	}
	// The control: a name nothing binds.
	env, err = p.PredictCompose(context.Background(), batch("nope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_MODEL_UNKNOWN) {
		t.Fatalf("unbound name: %+v", env.Errors)
	}
}

// TestComposeSweepBind_BindsWinnerAtFacade: at the facade a valid
// sweep-bound batch runs on both orchestrators — fits.best is the
// rank-1 slot's regression refitted (its coefficients those of an
// authored fit of the winning window) and the explicit slot scores it.
func TestComposeSweepBind_BindsWinnerAtFacade(t *testing.T) {
	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m"), {Name: "w60", Request: facadeFitRequest("0", "60")}, {Name: "w90", Request: facadeFitRequest("0", "90")}},
		sweepBindBody(t, `["x"]`, ""), nil)
	c.Requests[0].Attributes = []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "yhat", Params: json.RawMessage(`{"fit":"best"}`)}}
	c.Requests[0].Aggregations = append(c.Requests[0].Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "yhat", Label: "sum_yhat"})
	ctx := context.Background()
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
		want := map[string]string{"hi_60": "w60", "hi_90": "w90"}[out.Ranking[0].Label]
		best, hand := out.Fits["best"], out.Fits[want]
		if best == nil || hand == nil || best.Error != nil || !reflect.DeepEqual(best.Coefficients, hand.Coefficients) {
			t.Fatalf("%s: fits.best %+v, authored %s %+v", name, best, want, hand)
		}
		if v, ok := out.Responses[0].Data[0]["sum_yhat"].(float64); !ok || v == 0 {
			t.Errorf("%s: the scoring slot read %v", name, out.Responses[0].Data[0]["sum_yhat"])
		}
	}
}

// TestHiddenSweepBind_RefusedAsUnknownField: an instance offering
// Compose, the sweep and fits but not capability:compose_sweep_bind
// refuses a set `from_sweep` as PULSE_REQUEST_UNKNOWN_FIELD on Compose,
// ComposeParallel and PredictCompose; the unprofiled instance judges it.
func TestHiddenSweepBind_RefusedAsUnknownField(t *testing.T) {
	fsys := fitsFacadeFS(t)
	feats := []string{"capability:compose", "capability:compose_sweep", "capability:compose_fits",
		string(types.AGG_SUM), string(types.REG_OLS), string(types.FILTER_RANGE)}
	h, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "no-bind", Features: feats}})
	if err != nil {
		t.Fatal(err)
	}
	hidden := descx.HiddenSlotKeys(&types.FitSpec{}, h.svc.InstanceSnapshot())
	if !slices.Contains(hidden, "from_sweep") {
		t.Fatalf("vacuous: the profile does not hide from_sweep (hidden %v)", hidden)
	}
	control, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if got := descx.HiddenSlotKeys(&types.FitSpec{}, control.svc.InstanceSnapshot()); slices.Contains(got, "from_sweep") {
		t.Fatalf("the unprofiled instance hides from_sweep")
	}
	ctx := context.Background()
	c := sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")}, sweepBindBody(t, `["x"]`, ""), nil)
	if env, err := control.PredictCompose(ctx, c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("default instance: %v %+v", err, env)
	}
	// A profile enabling the capability walks the request-free entry
	// through every slot gate and predicts it valid.
	on, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "bind", Features: append(slices.Clone(feats), "capability:compose_sweep_bind")}})
	if err != nil {
		t.Fatal(err)
	}
	if env, err := on.PredictCompose(ctx, c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("enabling profile: %v %+v", err, env)
	}
	if _, err := on.Compose(ctx, c); err != nil {
		t.Fatalf("enabling profile: %v", err)
	}
	entries := map[string]func(*Pulse) *slotGateOutcome{
		"Compose": func(p *Pulse) *slotGateOutcome { _, err := p.Compose(ctx, c); return slotGateFromErr(err) },
		"ComposeParallel": func(p *Pulse) *slotGateOutcome {
			_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2})
			return slotGateFromErr(err)
		},
		"PredictCompose": func(p *Pulse) *slotGateOutcome {
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			return slotGateFromEnv(env)
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			out := run(h)
			assertSlotRefusal(t, out, "from_sweep", hidden)
			if out.details["fit"] != 0 && out.details["fit"] != float64(0) {
				t.Errorf("not located by details.fit: %v", out.details)
			}
		})
	}
}
