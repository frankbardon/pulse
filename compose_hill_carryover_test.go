package pulse

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// compose_hill_carryover_test.go pins adstock carry-over (U71 E2-S4,
// #551) on an in-model adstocked REG_MMM_HILL at the facade, against
// the model written out by hand: the engine holdout and a carried
// ATTR_REG_PREDICT (params.order) each score a held-out week as
// β0 + β·hill(Σ decay^l · x(week − l), k, s) over the WHOLE spend
// series — the train weeks carried in from the fit — never the series
// restarted at the first scored week; a saved model carries the same
// state; params.carryover false on a score reads x as given.

// hillCarrySplit is the first held-out / scored week.
const hillCarrySplit = 90

// hillHand is the hand-written model at res's coefficients: the score
// of week v over the generator's spend, the adstock summed from week
// from on (0: the whole series; hillCarrySplit: restarted at zero).
func hillHand(res *types.RegressionResult, x []float64, v, from int) float64 {
	decay := res.Coefficients["decay:x"]
	a := 0.0
	for l := 0; l <= hillAdstockMaxLag && v-l >= from; l++ {
		a += math.Pow(decay, float64(l)) * x[v-l]
	}
	h := 1 / (1 + math.Pow(res.Coefficients["k:x"]/a, res.Coefficients["s:x"]))
	return res.Coefficients[model.InterceptKey] + res.Coefficients["x"]*h
}

// hillHandHoldout is the held-out weeks' rmse and mae under hillHand.
func hillHandHoldout(res *types.RegressionResult, from int) (rmse, mae float64) {
	x, y := hillAdstockSeries()
	var sq, ab float64
	n := 0
	for v := hillCarrySplit; v < len(x); v++ {
		e := y[v] - hillHand(res, x, v, from)
		sq += e * e
		ab += math.Abs(e)
		n++
	}
	return math.Sqrt(sq / float64(n)), ab / float64(n)
}

func relNear(g, w, tol float64) bool {
	return math.Abs(g-w) <= tol*math.Max(1, math.Abs(w))
}

// TestHillCarryover_HoldoutCarriesTheFittedAdstock: an engine holdout
// on an in-model adstocked fit is accepted by predict and the run, its
// rmse / mae the hand-written model's over the whole spend series (the
// train weeks' adstock carried into the held-out weeks), which is not
// the series restarted at the split; the fit records its state — the
// last max_lag train weeks of raw x.
func TestHillCarryover_HoldoutCarriesTheFittedAdstock(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: hillAdstockFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	req := hillAdstockRequest()
	req.Regressions[0].Holdout = "week >= 90"
	if pr, err := p.Predict(context.Background(), req); err != nil || len(pr.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, pr.Errors)
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	res := resp.Regressions[0]
	if res.Error != nil || res.Holdout == nil {
		t.Fatalf("holdout fit failed: %+v", res)
	}
	if res.Holdout.N != 104-hillCarrySplit {
		t.Errorf("holdout n = %d, want %d", res.Holdout.N, 104-hillCarrySplit)
	}
	rmse, mae := hillHandHoldout(res, 0)
	if !relNear(res.Holdout.RMSE, rmse, 1e-9) || !relNear(res.Holdout.MAE, mae, 1e-9) {
		t.Errorf("holdout rmse %v mae %v, hand-written carried %v %v", res.Holdout.RMSE, res.Holdout.MAE, rmse, mae)
	}
	if restart, _ := hillHandHoldout(res, hillCarrySplit); relNear(restart, rmse, 1e-6) {
		t.Fatalf("restarting at the split gives the same rmse %v: the fixture cannot tell carry-over apart", restart)
	}
	st, ok, err := model.ReadAdstockState(res.Outputs)
	if err != nil || !ok {
		t.Fatalf("outputs.adstock_state: %v %v", ok, err)
	}
	x, _ := hillAdstockSeries()
	s := st.Series[0]
	if st.Field != "week" || st.MaxLag != hillAdstockMaxLag || st.Step != 1 || len(s.Order) != hillAdstockMaxLag ||
		s.Order[len(s.Order)-1] != hillCarrySplit-1 || float64(s.Spend["x"][len(s.Order)-1]) != x[hillCarrySplit-1] {
		t.Errorf("state %+v, want the last %d train weeks of x", st, hillAdstockMaxLag)
	}
}

// TestHillCarryover_ScoreCarriesFromTheFit: a Compose fit over the
// train weeks scores the later weeks through ATTR_REG_PREDICT with
// params.order — the hand-written carried model summed over the scored
// weeks, on both orchestrators — and with params.carryover false x as
// given (hill(x)); the fit's saved model, reloaded, carries the same
// state and scores the same sum bit for bit.
func TestHillCarryover_ScoreCarriesFromTheFit(t *testing.T) {
	t.Parallel()

	fsys := hillAdstockFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	const path = "models/hill_carry.pulse-model.json"
	held := []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "week >= 90"}}
	scoring := func() []*types.Attribute {
		return []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "carried", Params: json.RawMessage(`{"fit":"hill","order":{"field":"week"}}`)},
			{Type: types.ATTR_REG_PREDICT, Label: "given", Params: json.RawMessage(`{"fit":"hill","carryover":false}`)},
		}
	}
	sums := []*types.Aggregation{{Type: types.AGG_SUM, Field: "carried", Label: "carried"}, {Type: types.AGG_SUM, Field: "given", Label: "given"}}
	batch := func() *ComposedRequest {
		fit := hillAdstockRequest()
		fit.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "week < 90"}}
		fit.Regressions[0].Save = path
		return &ComposedRequest{
			Fits: []types.FitSpec{{Name: "hill", Request: fit}},
			Requests: []*Request{{Label: "score", Cohort: &types.Cohort{Filename: hillAdstockCohort}, Filterers: held,
				Attributes: scoring(), Aggregations: sums}},
		}
	}
	if env, err := p.PredictCompose(context.Background(), batch()); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	x, _ := hillAdstockSeries()
	var carriedSum float64
	for name, run := range composeRuns(p) {
		out, err := run(batch())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res := out.Fits["hill"].RegressionResult
		var wantCarried, wantGiven float64
		for v := hillCarrySplit; v < len(x); v++ {
			wantCarried += hillHand(res, x, v, 0)
			h := 1 / (1 + math.Pow(res.Coefficients["k:x"]/x[v], res.Coefficients["s:x"]))
			wantGiven += res.Coefficients[model.InterceptKey] + res.Coefficients["x"]*h
		}
		row := out.Responses[0].Data[0]
		got, _ := row["carried"].(float64)
		given, _ := row["given"].(float64)
		if !relNear(got, wantCarried, 1e-9) {
			t.Errorf("%s: carried score sum %v, hand-written %v", name, got, wantCarried)
		}
		if !relNear(given, wantGiven, 1e-9) {
			t.Errorf("%s: carryover false sum %v, hand-written hill(x) %v", name, given, wantGiven)
		}
		carriedSum = got
	}

	// The saved model holds the state: a plain request scores the same
	// sum bit for bit.
	saved, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: hillAdstockCohort}, Filterers: held,
		Models:     []types.ModelRef{{Name: "hill", Path: path}},
		Attributes: scoring()[:1], Aggregations: sums[:1],
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if back, _ := saved.Data[0]["carried"].(float64); math.Float64bits(back) != math.Float64bits(carriedSum) {
		t.Errorf("carried score over the saved model %v, over the fit %v", saved.Data[0]["carried"], carriedSum)
	}
}

// TestHillCarryover_ResampleSeedIgnoresCarryover: params.carryover
// shapes scoring only, so it never enters the rng_seed 0 derived seed —
// a bootstrap on the in-model adstocked fit draws bit-identical
// replicates (standard errors, p-values, succeeded count) with
// carryover absent, true and false.
func TestHillCarryover_ResampleSeedIgnoresCarryover(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: hillAdstockFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	boot := func(carry string) *types.RegressionResult {
		t.Helper()
		req := hillAdstockRequest()
		s := req.Regressions[0]
		s.Params = json.RawMessage(`{"channels": ["x"], "adstock": "geometric", "max_lag": 6` + carry + `}`)
		s.Resample, s.BootstrapIters = "bootstrap", 30
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		res := resp.Regressions[0]
		if res.Error != nil || res.BootstrapSucceeded == 0 {
			t.Fatalf("carryover %q: bootstrap fit failed: %+v", carry, res.Error)
		}
		return res
	}
	base := boot("")
	for _, carry := range []string{`, "carryover": true`, `, "carryover": false`} {
		got := boot(carry)
		if got.BootstrapSucceeded != base.BootstrapSucceeded {
			t.Errorf("carryover %q: succeeded %d, absent %d", carry, got.BootstrapSucceeded, base.BootstrapSucceeded)
		}
		for name, se := range base.StdErrors {
			if math.Float64bits(got.StdErrors[name]) != math.Float64bits(se) {
				t.Errorf("carryover %q: %s replicate se %v, absent %v", carry, name, got.StdErrors[name], se)
			}
			if math.Float64bits(got.PValues[name]) != math.Float64bits(base.PValues[name]) {
				t.Errorf("carryover %q: %s p %v, absent %v", carry, name, got.PValues[name], base.PValues[name])
			}
		}
	}
}

// TestHillCarryover_FittedResidualCarryFromTheFit (U71 E2-S5): a
// params.fit ATTR_REG_FITTED / ATTR_REG_RESIDUAL over the in-model
// adstocked fit carries its adstock exactly as a carried
// ATTR_REG_PREDICT does. Scored over the fit's own rows (weeks < 90)
// the carried fitted values are the hand-written model over the whole
// series and the squared carried residuals sum to the fit's own SSR
// (its deviance) — the fit's own in-sample figures; over the later
// weeks they carry from the fitted tail. params.carryover false reads x
// as given (hill(x)), the pre-change value. Both orchestrators.
func TestHillCarryover_FittedResidualCarryFromTheFit(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: hillAdstockFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	frame := func(label, where string) *Request {
		return &Request{
			Label: label, Cohort: &types.Cohort{Filename: hillAdstockCohort},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: where}},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_REG_FITTED, Label: "fit", Params: json.RawMessage(`{"fit":"hill","order":{"field":"week"}}`)},
				{Type: types.ATTR_REG_RESIDUAL, Label: "res", Params: json.RawMessage(`{"fit":"hill","order":{"field":"week"}}`)},
				{Type: types.ATTR_FORMULA, Label: "res2", Expression: "res * res"},
				{Type: types.ATTR_REG_FITTED, Label: "given", Params: json.RawMessage(`{"fit":"hill","carryover":false}`)},
				{Type: types.ATTR_REG_RESIDUAL, Label: "given_res", Params: json.RawMessage(`{"fit":"hill","carryover":false}`)},
				{Type: types.ATTR_REG_PREDICT, Label: "given_score", Params: json.RawMessage(`{"fit":"hill","carryover":false}`)},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "fit", Label: "fit"},
				{Type: types.AGG_SUM, Field: "res", Label: "res"},
				{Type: types.AGG_SUM, Field: "res2", Label: "res2"},
				{Type: types.AGG_SUM, Field: "given", Label: "given"},
				{Type: types.AGG_SUM, Field: "given_res", Label: "given_res"},
				{Type: types.AGG_SUM, Field: "given_score", Label: "given_score"},
			},
		}
	}
	batch := func() *ComposedRequest {
		fit := hillAdstockRequest()
		fit.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "week < 90"}}
		return &ComposedRequest{
			Fits:     []types.FitSpec{{Name: "hill", Request: fit}},
			Requests: []*Request{frame("in", "week < 90"), frame("out", "week >= 90")},
		}
	}
	if env, err := p.PredictCompose(context.Background(), batch()); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors[0])
	}
	x, y := hillAdstockSeries()
	for name, run := range composeRuns(p) {
		out, err := run(batch())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res := out.Fits["hill"].RegressionResult
		for i, span := range [][2]int{{0, hillCarrySplit}, {hillCarrySplit, len(x)}} {
			var wantFit, wantRes, wantGiven, wantGivenRes float64
			for v := span[0]; v < span[1]; v++ {
				f := hillHand(res, x, v, 0)
				wantFit += f
				wantRes += y[v] - f
				h := 1 / (1 + math.Pow(res.Coefficients["k:x"]/x[v], res.Coefficients["s:x"]))
				g := res.Coefficients[model.InterceptKey] + res.Coefficients["x"]*h
				if x[v] == 0 {
					g = res.Coefficients[model.InterceptKey]
				}
				wantGiven += g
				wantGivenRes += y[v] - g
			}
			row := out.Responses[i].Data[0]
			num := func(k string) float64 { v, _ := row[k].(float64); return v }
			if !relNear(num("fit"), wantFit, 1e-9) {
				t.Errorf("%s %s: carried fitted sum %v, hand-written %v", name, out.Responses[i].Label, num("fit"), wantFit)
			}
			if !relNear(num("res"), wantRes, 1e-7) {
				t.Errorf("%s %s: carried residual sum %v, hand-written %v", name, out.Responses[i].Label, num("res"), wantRes)
			}
			if !relNear(num("given"), wantGiven, 1e-9) || !relNear(num("given_res"), wantGivenRes, 1e-7) {
				t.Errorf("%s %s: carryover false sums %v / %v, hand-written hill(x) %v / %v", name, out.Responses[i].Label,
					num("given"), num("given_res"), wantGiven, wantGivenRes)
			}
			// carryover false is the pre-change row-local path: the
			// score of the channel columns as given, bit for bit.
			if math.Float64bits(num("given")) != math.Float64bits(num("given_score")) {
				t.Errorf("%s %s: carryover false fitted sum %v, row-local score %v", name, out.Responses[i].Label, num("given"), num("given_score"))
			}
			if i == 0 && !relNear(num("res2"), res.Deviance, 1e-9) {
				t.Errorf("%s: in-sample Σ carried residual² %v, the fit's own SSR %v", name, num("res2"), res.Deviance)
			}
		}
	}
}
