package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// window_predictor_chain_parity_test.go — parity gate A extended to the
// full media-mix chain (transformed predictors, E2-S2): WIN_DISTRIBUTED_LAG →
// a post-window ATTR_FORMULA hill() → REG_OLS with bounds.nonnegative in
// ONE request, in a Compose fit scored by its slot, and in a sweep over
// the adstock decay — each bit for bit what the multi-step route
// (materialize the windows, then saturate and regress over the
// materialized columns) produces.

// chainHill saturates the adstocked tv spend: half-saturation 900, shape 2.
const chainHill = "hill(tv_ad, 900, 2)"

// chainHillAttr is the post-window saturation attribute (it reads the
// window label tv_ad, so it runs after windows).
func chainHillAttr() *types.Attribute {
	return &types.Attribute{Type: types.ATTR_FORMULA, Label: "tv_sat", Expression: chainHill}
}

// chainDerive is windowParityDerive with the adstock decay set to the
// JSON value decay (a number, or a sweep placeholder).
func chainDerive(t *testing.T, decay string) *Request {
	t.Helper()
	d := windowParityDerive()
	params, err := json.Marshal(map[string]any{"kernel": "geometric", "decay": json.RawMessage(decay), "max_lag": 6})
	if err != nil {
		t.Fatalf("adstock params: %v", err)
	}
	d.Windows[0].Params = params
	return d
}

// chainOLS regresses sales on the saturated adstock, the lagged radio,
// the search EWMA and the trend, the three media terms bounded
// nonnegative (search enters negatively, so its bound binds).
func chainOLS(name string) *types.RegressionSpec {
	return &types.RegressionSpec{Name: name, Type: types.REG_OLS, Target: "sales",
		Predictors: []string{"tv_sat", "radio_lag", "search_ewma", "trend"},
		Bounds:     &types.RegressionBounds{Nonnegative: []string{"tv_sat", "radio_lag", "search_ewma"}}}
}

// chainSingle is the one-request chain: derive, the hill attribute
// after the derive attributes, and the regression.
func chainSingle(t *testing.T, decay, name string) *Request {
	t.Helper()
	r := chainDerive(t, decay)
	r.Attributes = append(r.Attributes, chainHillAttr())
	r.Regressions = []*types.RegressionSpec{chainOLS(name)}
	return r
}

// chainCase is the parity case for one decay: the single route given,
// the reference materializing chainDerive(decay) and saturating +
// regressing (slot regName) over the result.
func chainCase(t *testing.T, name, regName, decay string, single func(t *testing.T, p *Pulse) []*types.RegressionResult) windowParityCase {
	return windowParityCase{
		name:        name,
		Single:      single,
		Materialize: chainDerive(t, decay),
		Reference: func(cohort string) *Request {
			return &Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes:  []*types.Attribute{chainHillAttr()},
				Regressions: []*types.RegressionSpec{chainOLS(regName)}}
		},
		Check: chainFitted,
	}
}

// chainFitted asserts the fit is the intended one: full listwise n, a
// positive saturated-tv slope and the search bound binding.
func chainFitted(t *testing.T, single []*types.RegressionResult) {
	t.Helper()
	r := single[0]
	if r.NObs != windowParityNObs {
		t.Fatalf("n_obs = %d, want %d", r.NObs, windowParityNObs)
	}
	if c := r.Coefficients["tv_sat"]; !(c > 0) {
		t.Fatalf("tv_sat = %v, want a positive slope on the saturated adstock", c)
	}
	if c := r.Coefficients["search_ewma"]; c != 0 {
		t.Fatalf("search_ewma = %v, want 0 (the nonnegative bound binds)", c)
	}
}

// TestWindowPredictor_ParityAdstockHillChain: {WIN_DISTRIBUTED_LAG → ATTR_FORMULA
// hill() → REG_OLS bounds.nonnegative} in one request fits bit for bit
// what materialize{windows} → Process{hill attribute, regression} fits.
func TestWindowPredictor_ParityAdstockHillChain(t *testing.T) {
	p := windowParityPulse(t, Options{})
	runWindowParity(t, p, chainCase(t, "chain", "chain", "0.6", processRoute(chainSingle(t, "0.6", "chain"))))
}

// chainScoreSlot is the scoring slot attributes: attrs (the derive
// attributes on the single route, none on the reference) then the hill
// attribute, the fit's ATTR_REG_PREDICT p and a coef() rebuild c.
func chainScoreSlot(attrs []*types.Attribute) []*types.Attribute {
	c := `tv_sat == nil || radio_lag == nil || search_ewma == nil ? nil : ` +
		`coef("mmm", "(intercept)") + coef("mmm", "tv_sat") * tv_sat + coef("mmm", "radio_lag") * radio_lag + ` +
		`coef("mmm", "search_ewma") * search_ewma + coef("mmm", "trend") * trend`
	return append(append([]*types.Attribute{}, attrs...), chainHillAttr(),
		scoreRef(`{"fit":"mmm"}`),
		&types.Attribute{Type: types.ATTR_FORMULA, Label: "c", Expression: c})
}

// chainRowKey keys a data row by its market and week.
type chainRowKey struct {
	market string
	week   float64
}

func chainRows(t *testing.T, route string, rows []map[string]any) map[chainRowKey]map[string]any {
	t.Helper()
	out := map[chainRowKey]map[string]any{}
	for i, r := range rows {
		m, _ := r["market"].(string)
		w, ok := numberOf(r["week"])
		if m == "" || !ok {
			t.Fatalf("%s row %d has no market/week key: %v", route, i, r)
		}
		k := chainRowKey{m, w}
		if _, dup := out[k]; dup {
			t.Fatalf("%s: duplicate row %v", route, k)
		}
		out[k] = r
	}
	return out
}

// numberOf widens a decoded row number to float64.
func numberOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case uint64:
		return float64(x), true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case uint16:
		return float64(x), true
	}
	return 0, false
}

// TestWindowPredictor_ParityComposeFitsScoring: a Compose fit carrying
// the windows, the post-window hill and the regression fits bit for bit
// what the same fit over the materialized cohort fits; a slot scoring
// it on its OWN same-request adstock + hill columns — ATTR_REG_PREDICT
// and a coef() rebuild — scores every row bit for bit as the
// materialized route's slot does, null exactly where it is null.
func TestWindowPredictor_ParityComposeFitsScoring(t *testing.T) {
	p := windowParityPulse(t, Options{})
	ctx := context.Background()

	derive := chainDerive(t, "0.6")
	slot := chainDerive(t, "0.6")
	slot.Label = "score"
	slot.Attributes = chainScoreSlot(slot.Attributes)
	single := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mmm", Request: chainSingle(t, "0.6", "mmm")}},
		Requests: []*Request{slot},
	}
	if env, err := p.PredictCompose(ctx, single); err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose refused the single route: %v %+v", err, env)
	}

	const mat = "parity-fits.pulse"
	if _, err := p.Materialize(ctx, derive, mat, MaterializeOptions{Overwrite: true}); err != nil {
		t.Fatalf("reference Materialize: %v", err)
	}
	refFit := &Request{Cohort: &types.Cohort{Filename: mat},
		Attributes:  []*types.Attribute{chainHillAttr()},
		Regressions: []*types.RegressionSpec{chainOLS("mmm")}}
	// A row-local slot emits no data rows: a WIN_ROW_NUMBER (read by
	// nothing) gives the reference slot the same one-row-per-record
	// shape; every scored attribute is row-local, so it computes the
	// same value in either phase.
	refSlot := &Request{Label: "score", Cohort: &types.Cohort{Filename: mat},
		Attributes: chainScoreSlot(nil),
		Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "row", PartitionBy: []string{"market"},
			OrderBy: []types.OrderKey{{Field: "week"}}}}}
	reference := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mmm", Request: refFit}},
		Requests: []*Request{refSlot},
	}

	got, err := p.Compose(ctx, single)
	if err != nil {
		t.Fatalf("single route Compose: %v", err)
	}
	want, err := p.Compose(ctx, reference)
	if err != nil {
		t.Fatalf("reference route Compose: %v", err)
	}
	gf, wf := got.Fits["mmm"], want.Fits["mmm"]
	if gf == nil || wf == nil {
		t.Fatalf("fit mmm missing: single %+v, reference %+v", gf, wf)
	}
	assertRegressionsBitEqual(t, []*types.RegressionResult{gf.RegressionResult}, []*types.RegressionResult{wf.RegressionResult})
	chainFitted(t, []*types.RegressionResult{gf.RegressionResult})

	gRows := chainRows(t, "single", got.Responses[0].Data)
	wRows := chainRows(t, "reference", want.Responses[0].Data)
	if len(gRows) != 2*(windowParityWeeks-2) || len(gRows) != len(wRows) {
		t.Fatalf("scored rows: single %d, reference %d, want %d", len(gRows), len(wRows), 2*(windowParityWeeks-2))
	}
	scored := 0
	for k, g := range gRows {
		w, ok := wRows[k]
		if !ok {
			t.Fatalf("row %v missing on the reference route", k)
		}
		for _, col := range []string{"tv_ad", "tv_sat", "p", "c"} {
			gv, gok := g[col].(float64)
			wv, wok := w[col].(float64)
			if g[col] == nil && w[col] == nil {
				continue
			}
			if !gok || !wok || gv != wv {
				t.Fatalf("row %v %s: single %v (%T), reference %v (%T)", k, col, g[col], g[col], w[col], w[col])
			}
			if col == "p" {
				scored++
			}
		}
	}
	// Each market's leading lag is null: those rows score null.
	if scored != len(gRows)-2 {
		t.Fatalf("scored %d rows, want %d (every row but each market's leading null lag)", scored, len(gRows)-2)
	}
}

// chainSweepDecays are the swept adstock decays.
var chainSweepDecays = []string{"0.3", "0.6", "0.85"}

// chainSweep is a Compose holding only a sweep over the adstock decay:
// the body is the full chain with decay = {"$var":"decay"}.
func chainSweep(t *testing.T) *ComposedRequest {
	t.Helper()
	body, err := json.Marshal(chainSingle(t, `{"$var":"decay"}`, "mmm"))
	if err != nil {
		t.Fatalf("sweep body: %v", err)
	}
	values := make([]any, len(chainSweepDecays))
	for i, d := range chainSweepDecays {
		values[i] = json.Number(d)
	}
	return &ComposedRequest{Sweep: &types.SweepSpec{
		Axes:    []types.SweepAxis{{Name: "decay", Values: values}},
		Label:   "decay_{{decay}}",
		Request: body,
	}}
}

// TestWindowPredictor_ParitySweepAdstockDecay: a sweep with decay =
// {"$var":"decay"} in the WIN_DISTRIBUTED_LAG params runs each slot's own
// adstock — every slot equals ITS OWN materialized reference bit for
// bit, and the slots' coefficients differ (the decay reached the
// window), serial and parallel alike.
func TestWindowPredictor_ParitySweepAdstockDecay(t *testing.T) {
	p := windowParityPulse(t, Options{})
	ctx := context.Background()
	serial, err := p.Compose(ctx, chainSweep(t))
	if err != nil {
		t.Fatalf("Compose sweep: %v", err)
	}
	parallel, err := p.ComposeParallel(ctx, chainSweep(t), ComposeOptions{MaxWorkers: 3, FailFast: true})
	if err != nil {
		t.Fatalf("ComposeParallel sweep: %v", err)
	}
	if len(serial.Responses) != len(chainSweepDecays) || len(parallel.Responses) != len(chainSweepDecays) {
		t.Fatalf("sweep slots: serial %d, parallel %d, want %d", len(serial.Responses), len(parallel.Responses), len(chainSweepDecays))
	}
	slopes := map[float64]string{}
	for i, d := range chainSweepDecays {
		resp := serial.Responses[i]
		if resp.Label != "decay_"+d {
			t.Fatalf("slot %d label %q, want decay_%s", i, resp.Label, d)
		}
		assertRegressionsBitEqual(t, parallel.Responses[i].Regressions, resp.Regressions)
		t.Run("decay="+d, func(t *testing.T) {
			runWindowParity(t, p, chainCase(t, "sweep-"+d, "mmm", d,
				func(*testing.T, *Pulse) []*types.RegressionResult { return resp.Regressions }))
		})
		slope := resp.Regressions[0].Coefficients["tv_sat"]
		if prev, dup := slopes[slope]; dup {
			t.Fatalf("decay %s and %s fit the same tv_sat slope %v: the swept decay never reached the adstock", prev, d, slope)
		}
		slopes[slope] = d
	}
}
