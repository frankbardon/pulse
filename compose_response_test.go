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

// compose_response_test.go pins response(fit, channel, spend) at the
// facade (U79 G9): over an in-request REG_MMM_HILL fit whose channel is
// a WIN_DISTRIBUTED_LAG column and over an inline model with a declared
// adstock, it equals the hand-written beta·hill(m·spend, k, s) in an
// ATTR_FORMULA and in an optimize objective and constraint, on both
// orchestrators; every static refusal (no adstock record — a control, a
// hand-built channel, an OLS fit — and an unknown fit) is reported by
// PredictCompose exactly as Compose and ComposeParallel return it; and
// the name is reserved against embedders.

// responseMult is the fit's adstock multiplier: geometric decay 0.5 over
// lags 0..3, unnormalised — (1 − 0.5⁴) / (1 − 0.5).
const responseMult = 1.875

// truthMult is the inline truth's: geometric decay 0.6 over lags 0..4,
// unnormalised.
var truthMult = (1 - math.Pow(0.6, 5)) / (1 - 0.6)

// responseInline is the inline truth model: channel x, its adstock
// declared.
const responseInline = `{"model_format_version": 1, "type": "REG_MMM_HILL", "target": "y",
	"predictors": ["x"], "terms": ["x"],
	"coefficients": {"(intercept)": 1, "x": 6, "k:x": 9, "s:x": 1.6},
	"features": ["adstock"],
	"adstock": {"x": {"kernel": "geometric", "decay": 0.6, "max_lag": 4, "normalize": false}}}`

// responseHillFit is a REG_MMM_HILL of y on x_ad, x's unnormalised
// geometric adstock (decay 0.5, max_lag 3).
func responseHillFit() *Request {
	return &Request{
		Cohort: &types.Cohort{Filename: fitsFacadeCohort},
		Windows: []*types.Window{{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "x_ad", OrderBy: []types.OrderKey{{Field: "week"}},
			Params: json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 3, "normalize": false}`)}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_HILL, Name: "m", Target: "y", Predictors: []string{"x_ad"},
			Params: json.RawMessage(`{"channels": ["x_ad"]}`)}},
	}
}

// responseBatch fits "hill" (responseHillFit), "raw" (a Hill fit whose
// channel x is a cohort column: no record) and "plain" (OLS), binds the
// inline "truth" in the slot and the block, and reads response() in a
// formula per row and in an optimize block whose budget splits between
// the two models, its cap a response() constraint.
func responseBatch() *ComposedRequest {
	raw := &Request{Cohort: &types.Cohort{Filename: fitsFacadeCohort},
		Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_HILL, Name: "m", Target: "y", Predictors: []string{"x"},
			Params: json.RawMessage(`{"channels": ["x"]}`)}}}
	truth := []types.ModelRef{{Name: "truth", Inline: json.RawMessage(responseInline)}}
	return &ComposedRequest{
		Fits: []types.FitSpec{{Name: "hill", Request: responseHillFit()}, {Name: "raw", Request: raw}, {Name: "plain", Request: facadeFitRequest("0", "90")}},
		Requests: []*Request{{
			Label:  "curve",
			Cohort: &types.Cohort{Filename: fitsFacadeCohort},
			Models: truth,
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "fit_resp", Expression: `response("hill", "x_ad", x)`},
				{Type: types.ATTR_FORMULA, Label: "truth_resp", Expression: `response("truth", "x", x)`},
				// The hand-written algebra response() replaces.
				{Type: types.ATTR_FORMULA, Label: "fit_hand", Expression: `coef("hill", "x_ad") * hill(1.875 * x, coef("hill", "k:x_ad"), coef("hill", "s:x_ad"))`},
				{Type: types.ATTR_FORMULA, Label: "truth_hand", Expression: `coef("truth", "x") * hill((1 - 0.6^5) / (1 - 0.6) * x, coef("truth", "k:x"), coef("truth", "s:x"))`},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "fit_resp", Label: "fit_resp"},
				{Type: types.AGG_SUM, Field: "truth_resp", Label: "truth_resp"},
				{Type: types.AGG_SUM, Field: "fit_hand", Label: "fit_hand"},
				{Type: types.AGG_SUM, Field: "truth_hand", Label: "truth_hand"},
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			},
		}},
		Optimize: []types.OptimizeSpec{{
			Name:   "budget",
			Models: truth,
			Variables: []types.OptimizeVariable{
				{Name: "v", Lower: types.BoundValue(0), Upper: types.BoundValue(10)},
				{Name: "w", Lower: types.BoundValue(0), Upper: types.BoundValue(10)},
			},
			Objective: `response("hill", "x_ad", v) + response("truth", "x", w)`,
			Constraints: []types.OptimizeConstraint{
				{Expr: `10 - v - w`, Type: types.OptimizeConstraintIneq},
				{Expr: `5.5 - response("truth", "x", w)`, Type: types.OptimizeConstraintIneq},
			},
			Direction: types.OptimizeMaximize,
		}},
	}
}

// handResponse is beta·hill(m·spend, k, s) written out.
func handResponse(beta, k, s, m, spend float64) float64 {
	x := m * spend
	return beta * math.Pow(x, s) / (math.Pow(x, s) + math.Pow(k, s))
}

// TestResponse_EqualsHandFormula: summed over the cohort, the formula's
// response() over the in-request fit and the inline truth equals the
// hand-written coef()·hill() algebra it replaces; the
// optimize objective and the response() constraint at the optimum
// equal it at the reported variables; predict accepts the batch.
func TestResponse_EqualsHandFormula(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	if env, err := p.PredictCompose(context.Background(), responseBatch()); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
	within := func(got, want float64) bool { return math.Abs(got-want) <= 1e-12*math.Max(1, math.Abs(want)) }
	for name, run := range composeRuns(p) {
		out, err := run(responseBatch())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c := out.Fits["hill"].RegressionResult.Coefficients
		beta, k, s := c["x_ad"], c["k:x_ad"], c["s:x_ad"]
		if !(beta != 0 && k > 0 && s > 0) {
			t.Fatalf("%s: fit coefficients %v", name, c)
		}
		row := out.Responses[0].Data[0]
		if n, _ := row["n"].(float64); n != 104 {
			t.Fatalf("%s: %v rows", name, row["n"])
		}
		for _, pair := range [][2]string{{"fit_resp", "fit_hand"}, {"truth_resp", "truth_hand"}} {
			got, ok1 := row[pair[0]].(float64)
			want, ok2 := row[pair[1]].(float64)
			if !ok1 || !ok2 || !(want > 0) || !within(got, want) {
				t.Errorf("%s: %s %v, hand-written %v", name, pair[0], row[pair[0]], row[pair[1]])
			}
		}
		if len(out.Optimizations) != 1 || out.Optimizations[0].Error != nil {
			t.Fatalf("%s: optimizations %+v", name, out.Optimizations)
		}
		o := out.Optimizations[0]
		v, w := o.Variables[0].Value, o.Variables[1].Value
		if want := handResponse(beta, k, s, responseMult, v) + handResponse(6, 9, 1.6, truthMult, w); !within(o.Objective, want) {
			t.Errorf("%s: objective %v at v=%v w=%v, hand %v", name, o.Objective, v, w, want)
		}
		if want := 5.5 - handResponse(6, 9, 1.6, truthMult, w); len(o.Constraints) != 2 || math.Abs(o.Constraints[1].Value-want) > 1e-9 {
			t.Errorf("%s: constraint %+v, hand %v", name, o.Constraints, want)
		}
	}
}

// TestResponse_RefusalsMatchPredict: each static response() refusal is
// the same coded error in predict and on both orchestrators.
func TestResponse_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	formula := func(src string) func(*ComposedRequest) {
		return func(c *ComposedRequest) { c.Requests[0].Attributes[0].Expression = src }
	}
	objective := func(src string) func(*ComposedRequest) {
		return func(c *ComposedRequest) { c.Optimize[0].Objective = src }
	}
	cases := []struct {
		name     string
		edit     func(*ComposedRequest)
		code     perr.Code
		channels []string
	}{
		{"unknown fit", formula(`response("nope", "x_ad", x)`), perr.PULSE_MODEL_UNKNOWN, nil},
		{"hand-built channel", formula(`response("raw", "x", x)`), perr.PULSE_MODEL_TERM_UNKNOWN, []string{}},
		{"not a channel", formula(`response("hill", "y", x)`), perr.PULSE_MODEL_TERM_UNKNOWN, []string{"x_ad"}},
		{"ols fit in a filter", func(c *ComposedRequest) {
			c.Requests[0].Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `response("plain", "x", x) > 0`}}
		}, perr.PULSE_MODEL_TERM_UNKNOWN, []string{}},
		{"inline without the channel in optimize", objective(`response("truth", "z", w) + v`), perr.PULSE_MODEL_TERM_UNKNOWN, []string{"x"}},
		{"hand-built channel in a constraint", func(c *ComposedRequest) {
			c.Optimize[0].Constraints[1].Expr = `5 - response("raw", "x", w)`
		}, perr.PULSE_MODEL_TERM_UNKNOWN, []string{}},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := responseBatch()
			tc.edit(c)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if perr.Code(pe.Code) != tc.code {
				t.Fatalf("predict %s %v, want %s", pe.Code, pe.Details, tc.code)
			}
			if tc.channels != nil {
				if pe.Details["reason"] != "adstock_unknown" || pe.Details["channel"] == nil {
					t.Fatalf("predict details %v", pe.Details)
				}
				if got, _ := json.Marshal(pe.Details["channels"]); string(got) != mustJSONString(t, tc.channels) {
					t.Fatalf("predict details.channels %s, want %v", got, tc.channels)
				}
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

// mustJSONString is v's JSON.
func mustJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestResponse_NameReserved: an embedder may not register an expression
// function named response.
func TestResponse_NameReserved(t *testing.T) {
	t.Parallel()

	_, err := New(Options{FS: fitsFacadeFS(t), Extensions: Extensions{
		ExprFunctions: []ExprFunction{{Name: "response", Fn: func(a, b string, x float64) float64 { return 0 }}},
	}})
	if !perr.HasCode(err, perr.PULSE_EXTENSION_NAME_RESERVED) {
		t.Fatalf("embedder response(): %v, want PULSE_EXTENSION_NAME_RESERVED", err)
	}
}

// TestResponse_SavedModelLoads: the in-request fit saved with its
// adstock record and loaded by path in a later batch reads the same
// response() as the live fit; predict and both orchestrators refuse a
// channel the loaded file has no record for identically.
func TestResponse_SavedModelLoads(t *testing.T) {
	t.Parallel()

	const path = "models/response_hill.pulse-model.json"
	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	live := responseBatch()
	live.Fits[0].Request.Regressions[0].Save = path
	first, err := p.Compose(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	loaded := func(src string) *ComposedRequest {
		return &ComposedRequest{Requests: []*Request{{
			Label:        "loaded",
			Cohort:       &types.Cohort{Filename: fitsFacadeCohort},
			Models:       []types.ModelRef{{Name: "saved", Path: path}},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "r", Expression: src}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "r", Label: "fit_resp"}},
		}}}
	}
	c := loaded(`response("saved", "x_ad", x)`)
	if env, err := p.PredictCompose(ctx, c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
	for name, run := range composeRuns(p) {
		out, err := run(loaded(`response("saved", "x_ad", x)`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, want := out.Responses[0].Data[0]["fit_resp"], first.Responses[0].Data[0]["fit_resp"]; got != want {
			t.Errorf("%s: loaded %v, live %v", name, got, want)
		}
	}
	bad := loaded(`response("saved", "y", x)`)
	env, err := p.PredictCompose(ctx, bad)
	if err != nil || len(env.Errors) != 1 || env.Errors[0].Details["reason"] != "adstock_unknown" {
		t.Fatalf("predict: %v %+v", err, env)
	}
	for name, run := range map[string]func(*ComposedRequest) (*ComposedResponse, error){
		"Compose": func(c *ComposedRequest) (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *ComposedRequest) (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
		},
	} {
		var ce *perr.CodedError
		if _, err := run(bad); !stderrors.As(err, &ce) || string(ce.Code) != env.Errors[0].Code ||
			ce.Message != env.Errors[0].Message || !reflect.DeepEqual(ce.Details, env.Errors[0].Details) {
			t.Errorf("%s: runtime %v, predict %+v", name, err, env.Errors[0])
		}
	}
}

// TestResponse_SweepBoundFitJudgedAtRuntime: a fit bound from a sweep's
// winner records its adstock from the winning request, which only the
// run knows, so predict leaves a literal response() on it statically
// unresolved (no refusal) and the runtime judges it: the recorded
// channel reads, any other is refused adstock_unknown.
func TestResponse_SweepBoundFitJudgedAtRuntime(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	batch := func(channel string) *ComposedRequest {
		c := hillLineageBatch(t)
		c.Requests = []*Request{{
			Label:        "curve",
			Cohort:       &types.Cohort{Filename: fitsFacadeCohort},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "r", Expression: `response("best", "` + channel + `", x)`}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "r", Label: "r"}},
		}}
		return c
	}
	for _, ch := range []string{"x_ad", "y"} {
		if env, err := p.PredictCompose(ctx, batch(ch)); err != nil || len(env.Errors) > 0 {
			t.Fatalf("predict %s: %v %+v", ch, err, env)
		}
	}
	out, err := p.Compose(ctx, batch("x_ad"))
	if err != nil {
		t.Fatal(err)
	}
	var curve *types.Response
	for _, r := range out.Responses {
		if r != nil && r.Label == "curve" {
			curve = r
		}
	}
	if curve == nil {
		t.Fatal("no curve response")
	}
	if r, ok := curve.Data[0]["r"].(float64); !ok || !(r > 0) {
		t.Fatalf("recorded channel: %v", curve.Data[0])
	}
	_, err = p.Compose(ctx, batch("y"))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_TERM_UNKNOWN || ce.Details["reason"] != "adstock_unknown" {
		t.Fatalf("unrecorded channel: %v", err)
	}
}
