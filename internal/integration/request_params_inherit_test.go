package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// request_params_inherit_test.go — top-level params on ComposedRequest
// and ChainRequest (U58 E3-S2): every slot, fit request and stage
// inherits them, its own key winning; the merge is normalization (echo,
// predict ≡ run); the caller's request is never mutated, so a re-run
// with other values never meets a stale merge; a Compose-level
// OVERLAY_FORMULA reads them; capability:params hides the root slot.
//
// Fixture (openParams): wave = i%5+1, x = i, region alternates
// west / east, i in 0..19. Σx over wave 3 = 2+7+12+17 = 38; over wave 5
// = 4+9+14+19 = 46.

const (
	sumWave3 = 38.0
	sumWave5 = 46.0
)

// sumWhere is AGG_SUM x over the rows FILTER_EXPRESSION expr keeps.
func sumWhere(label, expr string, params types.Params) *types.Request {
	return &types.Request{
		Label:        label,
		Cohort:       paramsCohort(),
		Params:       params,
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
	}
}

func inheritCompose(params types.Params) *types.ComposedRequest {
	return &types.ComposedRequest{
		Params: params,
		Requests: []*types.Request{
			sumWhere("a", "wave == params.from_wave", nil),
			sumWhere("b", "wave == params.from_wave", nil),
			sumWhere("c", "wave == params.from_wave", types.Params{"from_wave": json.Number("5")}),
		},
	}
}

// TestParams_ComposeInherits: a top-level from_wave = 3 filters every
// slot by wave 3; a slot declaring from_wave = 5 uses 5 — serial
// Compose and ComposeParallel alike.
func TestParams_ComposeInherits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	run := map[string]func(*types.ComposedRequest) (*types.ComposedResponse, error){
		"serial": func(c *types.ComposedRequest) (*types.ComposedResponse, error) { return p.Compose(ctx, c) },
		"parallel": func(c *types.ComposedRequest) (*types.ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, pulse.ComposeOptions{MaxWorkers: 3})
		},
	}
	for name, fn := range run {
		resp, err := fn(inheritCompose(types.Params{"from_wave": json.Number("3")}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, want := range []float64{sumWave3, sumWave3, sumWave5} {
			if got := paramsFloat(t, resp.Responses[i], "s"); got != want {
				t.Fatalf("%s: slot %d sum = %v, want %v", name, i, got, want)
			}
		}
	}
}

// TestParams_ComposeNoStaleMerge: the caller's slot requests are never
// written, so the same requests re-run under another top-level value
// read the new one.
func TestParams_ComposeNoStaleMerge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{EchoRequest: true})
	c := inheritCompose(types.Params{"from_wave": json.Number("3")})
	resp, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if c.Requests[0].Params != nil || len(c.Requests[2].Params) != 1 {
		t.Fatalf("caller slots mutated: %v / %v", c.Requests[0].Params, c.Requests[2].Params)
	}
	// The echo carries the merged view on every slot.
	echo := resp.NormalizedRequest
	if echo == nil || fmt.Sprint(echo.Requests[0].Params["from_wave"]) != "3" || fmt.Sprint(echo.Requests[2].Params["from_wave"]) != "5" {
		t.Fatalf("echo params = %+v", echo)
	}
	c.Params = types.Params{"from_wave": json.Number("5")}
	resp, err = p.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := paramsFloat(t, resp.Responses[0], "s"); got != sumWave5 {
		t.Fatalf("re-run slot 0 sum = %v, want %v", got, sumWave5)
	}
}

// TestParams_ComposeSweepAndFitInherit: a sweep-expanded slot and a fit
// request inherit the top-level params too.
func TestParams_ComposeSweepAndFitInherit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	c := &types.ComposedRequest{
		Params: types.Params{"from_wave": json.Number("3")},
		Sweep: &types.SweepSpec{
			Axes:    []types.SweepAxis{{Name: "lab", Values: []any{"s1"}}},
			Label:   "{{lab}}",
			Request: json.RawMessage(`{"cohort":{"filename":"params.pulse"},"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.from_wave"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}]}`),
		},
		Fits: []types.FitSpec{{Name: "m", Request: &types.Request{
			Cohort:      paramsCohort(),
			Filterers:   []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "wave >= params.from_wave"}},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "x", Predictors: []string{"wave"}}},
		}}},
	}
	env, err := p.PredictCompose(ctx, c)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose: %v %+v", err, env.Errors[0])
	}
	resp, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if got := paramsFloat(t, resp.Responses[0], "s"); got != sumWave3 {
		t.Fatalf("sweep slot sum = %v, want %v", got, sumWave3)
	}
	if fr := resp.Fits["m"]; fr == nil || fr.RegressionResult == nil || fr.RegressionResult.Error != nil {
		t.Fatalf("fit = %+v", fr)
	}
}

// inheritChain: stage 0 keeps from_wave and sums x by region; stage 1
// keeps the region sums above min_s and totals them.
func inheritChain(params, own0, own1 types.Params) *types.ChainRequest {
	return &types.ChainRequest{
		Cohort: paramsCohort(),
		Params: params,
		Stages: []*types.ChainStage{
			{Name: "by_region", Request: &types.Request{
				Params:       own0,
				Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "wave == params.from_wave"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
			}},
			{Name: "total", Request: &types.Request{
				Params:       own1,
				Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "s > params.min_s"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "t"}},
			}},
		},
	}
}

// TestParams_ChainInherits: every stage inherits the chain's params; a
// stage declaring a key itself uses its own. Wave 3 by region: west 14,
// east 24; wave 5: west 18, east 28.
func TestParams_ChainInherits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	top := types.Params{"from_wave": json.Number("3"), "min_s": json.Number("0")}
	cases := []struct {
		name       string
		own0, own1 types.Params
		want       float64
	}{
		{"inherit", nil, nil, sumWave3},
		{"stage 0 overrides", types.Params{"from_wave": json.Number("5")}, nil, sumWave5},
		{"stage 1 overrides", nil, types.Params{"min_s": json.Number("20")}, 24},
	}
	for _, c := range cases {
		resp, err := p.ProcessChain(ctx, inheritChain(top, c.own0, c.own1))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := paramsFloat(t, resp.Final, "t"); got != c.want {
			t.Fatalf("%s: total = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestParams_ChainNoStaleMerge: the caller's stage requests never gain
// the inherited keys; a re-run under another value reads it; the echo
// shows the merged params per stage.
func TestParams_ChainNoStaleMerge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{EchoRequest: true})
	req := inheritChain(types.Params{"from_wave": json.Number("3"), "min_s": json.Number("0")}, nil, nil)
	resp, err := p.ProcessChain(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if req.Stages[0].Request.Params != nil || req.Stages[1].Request.Params != nil {
		t.Fatalf("caller stages mutated")
	}
	echo := resp.NormalizedRequest
	if echo == nil || len(echo.Stages) != 2 {
		t.Fatalf("echo = %+v", echo)
	}
	for i, st := range echo.Stages {
		if fmt.Sprint(st.Request.Params["from_wave"]) != "3" || fmt.Sprint(st.Request.Params["min_s"]) != "0" {
			t.Fatalf("echo stage %d params = %v", i, st.Request.Params)
		}
	}
	req.Params = types.Params{"from_wave": json.Number("5"), "min_s": json.Number("0")}
	resp, err = p.ProcessChain(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := paramsFloat(t, resp.Final, "t"); got != sumWave5 {
		t.Fatalf("re-run total = %v, want %v", got, sumWave5)
	}
}

// TestParams_InheritPredictMatchesRuntime: under inheritance an
// undeclared read (slot, fit, stage, Compose overlay formula) and an
// invalid top-level block are refused with the same code and message by
// PredictCompose / PredictChain and at run; an inherited name is never
// refused.
func TestParams_InheritPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	top := types.Params{"from_wave": json.Number("3")}

	undeclaredSlot := inheritCompose(top)
	undeclaredSlot.Requests[1] = sumWhere("b", "wave == params.to_wave", nil)
	invalidTop := inheritCompose(types.Params{"from_wave": nil})
	// Every slot overrides the null name: the top-level block is still
	// judged, once, before any slot.
	for _, r := range invalidTop.Requests {
		r.Params = types.Params{"from_wave": json.Number("3")}
	}
	undeclaredFit := inheritCompose(top)
	undeclaredFit.Fits = []types.FitSpec{{Name: "m", Request: &types.Request{
		Cohort:      paramsCohort(),
		Filterers:   []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "wave >= params.min_wave"}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "x", Predictors: []string{"wave"}}},
	}}}
	overlay := func(formula string) *types.ComposedRequest {
		c := inheritCompose(types.Params{"from_wave": json.Number("3"), "k": json.Number("2")})
		c.Overlays = []types.ComposeOverlaySpec{{Name: "fx", Kind: types.OverlayKindFormula,
			Reference: "a", Targets: []string{"b"}, Params: map[string]any{"formula": formula}}}
		return c
	}
	composeCases := []struct {
		name string
		req  *types.ComposedRequest
		code perr.Code
	}{
		{"slot undeclared", undeclaredSlot, perr.PULSE_PARAMS_UNDECLARED},
		{"top-level invalid", invalidTop, perr.PULSE_PARAMS_INVALID},
		{"fit undeclared", undeclaredFit, perr.PULSE_PARAMS_UNDECLARED},
		{"overlay formula undeclared", overlay("value * params.j"), perr.PULSE_PARAMS_UNDECLARED},
	}
	for _, c := range composeCases {
		t.Run(c.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, c.req)
			if err != nil {
				t.Fatal(err)
			}
			_, runErr := p.Compose(ctx, c.req)
			assertPredictHasRunError(t, env.Errors, requireCodedError(t, runErr, c.code))
			_, parErr := p.ComposeParallel(ctx, c.req, pulse.ComposeOptions{FailFast: true})
			if ce := requireCodedError(t, parErr, c.code); ce.Message != requireCodedError(t, runErr, c.code).Message {
				t.Fatalf("parallel %q, serial %q", ce.Message, requireCodedError(t, runErr, c.code).Message)
			}
		})
	}
	t.Run("overlay formula inherited", func(t *testing.T) {
		c := overlay("value * params.k")
		env, err := p.PredictCompose(ctx, c)
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("PredictCompose: %v %+v", err, env.Errors[0])
		}
		resp, err := p.Compose(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Scalar == nil || *resp.Overlays[0].Payload.Scalar != 2*sumWave3 {
			t.Fatalf("overlay = %+v", resp.Overlays)
		}
	})

	undeclaredStage := inheritChain(types.Params{"from_wave": json.Number("3")}, nil, nil)
	chainCases := []struct {
		name string
		req  *types.ChainRequest
		code perr.Code
	}{
		{"stage undeclared", undeclaredStage, perr.PULSE_PARAMS_UNDECLARED},
		{"top-level invalid", inheritChain(types.Params{"from_wave": nil, "min_s": json.Number("0")}, types.Params{"from_wave": json.Number("3")}, nil), perr.PULSE_PARAMS_INVALID},
	}
	for _, c := range chainCases {
		t.Run("chain "+c.name, func(t *testing.T) {
			env, err := p.PredictChain(ctx, c.req)
			if err != nil {
				t.Fatal(err)
			}
			_, runErr := p.ProcessChain(ctx, c.req)
			assertPredictHasRunError(t, env.Errors, requireCodedError(t, runErr, c.code))
		})
	}
	t.Run("chain inherited", func(t *testing.T) {
		env, err := p.PredictChain(ctx, inheritChain(types.Params{"from_wave": json.Number("3"), "min_s": json.Number("0")}, nil, nil))
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("PredictChain: %v %+v", err, env)
		}
	})
}

func assertPredictHasRunError(t *testing.T, errs []*descriptor.EnvelopeEntry, ce *perr.CodedError) {
	t.Helper()
	for _, e := range errs {
		if e.Code == string(ce.Code) && e.Message == ce.Message {
			return
		}
	}
	t.Fatalf("predict errors %+v lack the run's %s %q", errs, ce.Code, ce.Message)
}

// TestParams_InheritEchoPredict: PredictCompose / PredictChain echo the
// merged params on every sub-request, as the run's normalized request
// does.
func TestParams_InheritEchoPredict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{EchoRequest: true})
	env, err := p.PredictCompose(ctx, inheritCompose(types.Params{"from_wave": json.Number("3")}))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := env.Request.(*types.ComposedRequest)
	if !ok || fmt.Sprint(c.Requests[0].Params["from_wave"]) != "3" || fmt.Sprint(c.Requests[2].Params["from_wave"]) != "5" {
		t.Fatalf("compose echo = %#v", env.Request)
	}
	env, err = p.PredictChain(ctx, inheritChain(types.Params{"from_wave": json.Number("3"), "min_s": json.Number("0")}, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := env.Request.(*types.ChainRequest)
	if !ok || fmt.Sprint(ch.Stages[1].Request.Params["from_wave"]) != "3" {
		t.Fatalf("chain echo = %#v", env.Request)
	}
}

// TestParams_InheritFeatureProfileGate: without capability:params a
// top-level params on a Compose or chain root is an unknown field of
// that root, and the payload schema's ComposedRequest / ChainRequest
// carry no params key.
func TestParams_InheritFeatureProfileGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	openParams(t, pulse.Options{FS: fsys})
	open := func(features ...string) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{Features: features}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	base := []string{"capability:process", "capability:compose", "capability:process_chain", "FILTER_EXPRESSION", "AGG_SUM", "GROUP_CATEGORY"}
	hidden := open(base...)
	enabled := open(append(base, "capability:params")...)

	c := &types.ComposedRequest{Params: types.Params{"w": 1}, Requests: []*types.Request{sumWhere("a", "wave == 1", nil)}}
	_, err := hidden.Compose(ctx, c)
	if ce := requireCodedError(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD); fmt.Sprint(ce.Details["unknown_keys"]) != "[params]" || ce.Details["request"] != nil {
		t.Fatalf("compose details = %v", ce.Details)
	}
	env, err := hidden.PredictCompose(ctx, c)
	if err != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Fatalf("PredictCompose hidden: %v %+v", err, env)
	}
	ch := inheritChain(types.Params{"from_wave": json.Number("3"), "min_s": json.Number("0")}, nil, nil)
	_, err = hidden.ProcessChain(ctx, ch)
	if ce := requireCodedError(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD); fmt.Sprint(ce.Details["unknown_keys"]) != "[params]" || ce.Details["stage"] != nil {
		t.Fatalf("chain details = %v", ce.Details)
	}
	if _, err := enabled.Compose(ctx, c); err != nil {
		t.Fatalf("enabled compose: %v", err)
	}
	if _, err := enabled.ProcessChain(ctx, ch); err != nil {
		t.Fatalf("enabled chain: %v", err)
	}
	for _, c := range []struct {
		p    *pulse.Pulse
		want bool
	}{{hidden, false}, {enabled, true}} {
		raw, err := c.p.PayloadSchema()
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Defs map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, def := range []string{"ComposedRequest", "ChainRequest"} {
			if _, has := doc.Defs[def].Properties["params"]; has != c.want {
				t.Fatalf("payload schema %s.params present = %v, want %v", def, has, c.want)
			}
		}
	}
}
