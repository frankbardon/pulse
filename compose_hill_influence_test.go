package pulse

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// compose_hill_influence_test.go pins leverage and influence over a
// bound REG_MMM_HILL fit (U71 E2-S6, #732) at the facade, against the
// tangent plane written out by hand: g = ∂ŷ/∂θ over (intercept, β, k,
// s, decay) at the fit's coefficients, h = gᵀ(Vcov/σ̂²)g, Cook's
// distance e²·h/(p′·σ̂²·(1 − h)²). A carried slot reads the decay's
// partial Σ_l l·decay^(l−1)·x(week − l) over the whole spend series (the
// train weeks carried in from the fit's state); carryover false reads x
// as given (∂a/∂decay = 0). On the fit's own rows the hat values sum to
// the free parameter count and lie in [0, 1]. The R oracle is
// internal/regmmm's hill_influence_oracle_test.go.

// hillTangentHand is the hand-written tangent at res's coefficients for
// week v: ŷ, and g over (intercept, x, k:x, s:x, decay:x). carried
// reads a as Σ_l decay^l·x(v − l) over the whole series and its decay
// partial; else a = x(v), ∂a/∂decay = 0.
func hillTangentHand(res *types.RegressionResult, x []float64, v int, carried bool) (yhat float64, g []float64) {
	c := res.Coefficients
	decay, b, k, s := c["decay:x"], c["x"], c["k:x"], c["s:x"]
	a, da := x[v], 0.0
	if carried {
		a = 0
		for l := 0; l <= hillAdstockMaxLag && v-l >= 0; l++ {
			a += math.Pow(decay, float64(l)) * x[v-l]
			if l > 0 {
				da += float64(l) * math.Pow(decay, float64(l-1)) * x[v-l]
			}
		}
	}
	h, dhk, dhs, dha := 0.0, 0.0, 0.0, 0.0
	if a > 0 {
		q := math.Pow(k/a, s)
		h = 1 / (1 + q)
		hh := h * (1 - h)
		dha, dhk, dhs = s*hh/a, -s*hh/k, hh*math.Log(a/k)
	}
	return c[model.InterceptKey] + b*h, []float64{1, h, b * dhk, b * dhs, b * dha * da}
}

// hillHatHand is gᵀ(Vcov/σ̂²)g over res's free keys, with p′ (the count
// of keys whose variance is defined).
func hillHatHand(res *types.RegressionResult, g []float64) (h float64, p1 int) {
	keys := []string{model.InterceptKey, "x", "k:x", "s:x", "decay:x"}
	idx := map[string]int{}
	for i, k := range res.Vcov.RowKeys {
		idx[k] = i
	}
	for a, ka := range keys {
		va := res.Vcov.Values[idx[ka]]
		if math.IsNaN(va[idx[ka]]) {
			continue
		}
		p1++
		for b, kb := range keys {
			if v := va[idx[kb]]; !math.IsNaN(v) {
				h += g[a] * v / res.Dispersion * g[b]
			}
		}
	}
	return h, p1
}

// TestHillInfluence_TangentPlaneFigures: over the in-model adstocked fit
// (vcov: true, the train weeks), a carried ATTR_REG_LEVERAGE /
// ATTR_REG_INFLUENCE / scaled ATTR_REG_RESIDUAL equals the hand-written
// tangent plane on the fit's own rows (Σ h = p′, 0 ≤ h ≤ 1) and on the
// later weeks (carried from the fitted tail); carryover false reads x
// as given. Predict accepts every slot; both orchestrators.
func TestHillInfluence_TangentPlaneFigures(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: hillAdstockFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	const carried = `,"order":{"field":"week"}}`
	frame := func(label, where string) *Request {
		attr := func(typ types.AttributeType, lbl, params string) *types.Attribute {
			return &types.Attribute{Type: typ, Label: lbl, Params: json.RawMessage(params)}
		}
		return &Request{
			Label: label, Cohort: &types.Cohort{Filename: hillAdstockCohort},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: where}},
			Attributes: []*types.Attribute{
				attr(types.ATTR_REG_LEVERAGE, "h", `{"fit":"hill"`+carried),
				attr(types.ATTR_REG_INFLUENCE, "cooks", `{"fit":"hill"`+carried),
				attr(types.ATTR_REG_INFLUENCE, "dffits", `{"fit":"hill","measure":"dffits"`+carried),
				attr(types.ATTR_REG_RESIDUAL, "std", `{"fit":"hill","kind":"standardized"`+carried),
				attr(types.ATTR_REG_RESIDUAL, "stud", `{"fit":"hill","kind":"studentized"`+carried),
				attr(types.ATTR_REG_LEVERAGE, "given", `{"fit":"hill","carryover":false}`),
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "h", Label: "h"},
				{Type: types.AGG_MIN, Field: "h", Label: "h_min"},
				{Type: types.AGG_MAX, Field: "h", Label: "h_max"},
				{Type: types.AGG_SUM, Field: "cooks", Label: "cooks"},
				{Type: types.AGG_SUM, Field: "dffits", Label: "dffits"},
				{Type: types.AGG_SUM, Field: "std", Label: "std"},
				{Type: types.AGG_SUM, Field: "stud", Label: "stud"},
				{Type: types.AGG_SUM, Field: "given", Label: "given"},
			},
		}
	}
	batch := func() *ComposedRequest {
		fit := hillAdstockRequest()
		fit.Regressions[0].Vcov = true
		fit.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "week < 90"}}
		return &ComposedRequest{
			Fits:     []types.FitSpec{{Name: "hill", Request: fit}},
			Requests: []*Request{frame("in", "week < 90"), frame("out", "week >= 90")},
		}
	}
	if env, err := p.PredictCompose(context.Background(), batch()); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	x, y := hillAdstockSeries()
	for name, run := range composeRuns(p) {
		out, err := run(batch())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res := out.Fits["hill"].RegressionResult
		if !(res.Dispersion > 0) || res.Vcov == nil {
			t.Fatalf("%s: the fit carries dispersion %v, vcov %v", name, res.Dispersion, res.Vcov != nil)
		}
		if want := res.Deviance / res.DF; res.Dispersion != want {
			t.Errorf("%s: dispersion %v, SSR/df %v", name, res.Dispersion, want)
		}
		for i, span := range [][2]int{{0, hillCarrySplit}, {hillCarrySplit, len(x)}} {
			var wantH, wantCooks, wantStd, wantGiven float64
			var p1 int
			for v := span[0]; v < span[1]; v++ {
				yhat, g := hillTangentHand(res, x, v, true)
				var h float64
				h, p1 = hillHatHand(res, g)
				e := y[v] - yhat
				wantH += h
				wantCooks += e * e * h / (float64(p1) * res.Dispersion * (1 - h) * (1 - h))
				wantStd += e / math.Sqrt(res.Dispersion*(1-h))
				_, gg := hillTangentHand(res, x, v, false)
				hg, _ := hillHatHand(res, gg)
				wantGiven += hg
			}
			row := out.Responses[i].Data[0]
			num := func(k string) float64 { v, _ := row[k].(float64); return v }
			lbl := name + " " + out.Responses[i].Label
			if !relNear(num("h"), wantH, 1e-9) {
				t.Errorf("%s: Σ carried leverage %v, hand-written tangent %v", lbl, num("h"), wantH)
			}
			if !relNear(num("cooks"), wantCooks, 1e-7) {
				t.Errorf("%s: Σ carried Cook's distance %v, hand-written %v", lbl, num("cooks"), wantCooks)
			}
			if !relNear(num("std"), wantStd, 1e-7) {
				t.Errorf("%s: Σ standardized residual %v, hand-written %v", lbl, num("std"), wantStd)
			}
			if !relNear(num("given"), wantGiven, 1e-9) {
				t.Errorf("%s: Σ carryover-false leverage %v, hand-written (x as given) %v", lbl, num("given"), wantGiven)
			}
			if relNear(num("given"), num("h"), 1e-6) {
				t.Errorf("%s: carryover false equals the carried leverage %v: the fixture cannot tell them apart", lbl, num("h"))
			}
			if math.IsNaN(num("dffits")) || math.IsNaN(num("stud")) {
				t.Errorf("%s: dffits %v / studentized %v undefined", lbl, row["dffits"], row["stud"])
			}
			if i == 0 {
				// The fit's own rows: Σ h = p′ (the fit's Jacobian's hat
				// matrix), each h in [0, 1].
				if p1 != 5 || !relNear(num("h"), float64(p1), 1e-9) {
					t.Errorf("%s: in-sample Σ h = %v, want p′ = %d", lbl, num("h"), p1)
				}
				if num("h_min") < 0 || num("h_max") > 1 {
					t.Errorf("%s: in-sample h spans [%v, %v], want within [0, 1]", lbl, num("h_min"), num("h_max"))
				}
			}
		}
	}
}

// hillHeldFS writes week, x (spend), price and y = 3 + 2·price −
// 0.02·x + a wobble: y FALLS with x, so a nonnegative β on x binds at 0.
func hillHeldFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := range 60 {
		x := float64((i*53)%29)*4 + 10
		price := 5 + float64((i*7)%11)/3
		y := 3 + 2*price - 0.02*x + 0.05*float64((i*13)%7-3)
		rows = append(rows, []string{strconv.Itoa(i), jsonNumber(x), jsonNumber(price), jsonNumber(y)})
	}
	createTestPulseFile(t, fsys, "hill_held.pulse", []string{"week", "x", "price", "y"}, rows)
	return fsys
}

// TestHillInfluence_HeldCurveIsOLSLeverage: a REG_MMM_HILL fit whose
// channel β binds at 0 holds its curve (k, s held with it) — the model
// is linear in its free parameters (intercept, price) — so its
// tangent-plane leverage equals the OLS refit's leverage on the same
// design (y ~ price) row for row.
func TestHillInfluence_HeldCurveIsOLSLeverage(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: hillHeldFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := &ComposedRequest{
		Fits: []types.FitSpec{{Name: "hill", Request: &Request{Cohort: &types.Cohort{Filename: "hill_held.pulse"},
			Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_HILL, Name: "m", Target: "y", Predictors: []string{"x", "price"},
				Params: json.RawMessage(`{"channels": ["x"]}`), Vcov: true}}}}},
		Requests: []*Request{{Label: "s", Cohort: &types.Cohort{Filename: "hill_held.pulse"},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_REG_LEVERAGE, Label: "hill", Params: json.RawMessage(`{"fit":"hill"}`)},
				{Type: types.ATTR_REG_LEVERAGE, Label: "ols", Target: "y", Predictors: []string{"price"}},
				{Type: types.ATTR_FORMULA, Label: "d", Expression: "abs(hill - ols)"},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_MAX, Field: "d", Label: "d"},
				{Type: types.AGG_SUM, Field: "hill", Label: "hill"},
			}}},
	}
	if env, err := p.PredictCompose(context.Background(), c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	out, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	res := out.Fits["hill"].RegressionResult
	if res.Coefficients["x"] != 0 || !math.IsNaN(res.StdErrors["x"]) {
		t.Fatalf("β on x = %v (se %v): the fixture must hold it at its bound", res.Coefficients["x"], res.StdErrors["x"])
	}
	row := out.Responses[0].Data[0]
	if d, _ := row["d"].(float64); !(d <= 1e-12) {
		t.Errorf("max |h_hill − h_ols| = %v, want ≤ 1e-12", row["d"])
	}
	if h, _ := row["hill"].(float64); !relNear(h, 2, 1e-12) {
		t.Errorf("Σ h = %v, want 2 (the free intercept and price)", row["hill"])
	}
}

// TestHillInfluence_SavedModelKeepsTheDispersion: a HILL fit with vcov:
// true saves its dispersion (feature dispersion), so a later request
// loading the model file reads the same carried leverage, bit for bit;
// a fit with params.prior carries none (a penalty) and its leverage is
// refused requires unpenalized.
func TestHillInfluence_SavedModelKeepsTheDispersion(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: hillAdstockFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	const path = "models/hill_influence.pulse-model.json"
	ctx := context.Background()
	lev := []*types.Attribute{{Type: types.ATTR_REG_LEVERAGE, Label: "h", Params: json.RawMessage(`{"fit":"hill","order":{"field":"week"}}`)}}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "h", Label: "h"}}
	fit := hillAdstockRequest()
	fit.Regressions[0].Vcov, fit.Regressions[0].Save = true, path
	out, err := p.Compose(ctx, &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "hill", Request: fit}},
		Requests: []*Request{{Label: "s", Cohort: &types.Cohort{Filename: hillAdstockCohort}, Attributes: lev, Aggregations: sum}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := p.Process(ctx, &Request{Cohort: &types.Cohort{Filename: hillAdstockCohort},
		Models: []types.ModelRef{{Name: "hill", Path: path}}, Attributes: lev, Aggregations: sum})
	if err != nil {
		t.Fatalf("over the saved model: %v", err)
	}
	if a, b := out.Responses[0].Data[0]["h"], loaded.Data[0]["h"]; a != b {
		t.Errorf("Σ leverage over the saved model %v, over the fit %v", b, a)
	}

	prior := hillAdstockRequest()
	prior.Regressions[0].Vcov = true
	prior.Regressions[0].Params = json.RawMessage(`{"channels": ["x"], "adstock": "geometric", "max_lag": 6, "prior": {"channel": "x", "mean": 8, "precision": 1}}`)
	resp, err := p.Process(ctx, prior)
	if err != nil {
		t.Fatal(err)
	}
	if d := resp.Regressions[0].Dispersion; d != 0 {
		t.Errorf("a fit with params.prior carries dispersion %v, want none (its vcov is penalized)", d)
	}
}
