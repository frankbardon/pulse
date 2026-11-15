package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// compose_hill_adstock_test.go pins REG_MMM_HILL's in-model adstock
// (params.adstock, U71 E2-S2, #551) at the facade: the fit recovers the
// generator's decay, writes the adstock record response() and a saved
// model read (the fitted decay, unnormalised, no fixed_at), so
// response() equals the hand-written coef()·hill(multiplier·spend)
// algebra on both orchestrators; a Weibull fit (E2-S3) records its
// fitted shape and scale the same way; every refusal (no order, an
// engine holdout, a pre-adstocked channel, an unknown kernel, a Weibull
// kernel without max_lag, a partition) is predict's exactly as it is
// Process's.

const hillAdstockCohort = "hill_adstock.pulse"

// hillAdstockDecay / hillAdstockMaxLag are the generator's kernel.
const (
	hillAdstockDecay  = 0.6
	hillAdstockMaxLag = 6
)

// hillAdstockFS writes 104 weeks of bursty spend x, y = 5 + 8·hill(a,
// 60, 2) + a small deterministic wobble, a x's unnormalised geometric
// adstock at hillAdstockDecay over lags 0..hillAdstockMaxLag.
func hillAdstockFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	const n = 104
	x := make([]float64, n)
	for i := range x {
		if (i*37)%17 > 7 {
			x[i] = float64((i*53)%29)*4 + 10
		}
	}
	var rows [][]string
	for i := range n {
		a := 0.0
		for l := 0; l <= hillAdstockMaxLag && l <= i; l++ {
			a += math.Pow(hillAdstockDecay, float64(l)) * x[i-l]
		}
		h := 0.0
		if a > 0 {
			h = 1 / (1 + math.Pow(60/a, 2))
		}
		y := 5 + 8*h + 0.01*float64((i*13)%7-3)
		rows = append(rows, []string{strconv.Itoa(i), jsonNumber(x[i]), jsonNumber(y), []string{"north", "south"}[i%2]})
	}
	createTestPulseFile(t, fsys, hillAdstockCohort, []string{"week", "x", "y", "region"}, rows)
	return fsys
}

// hillAdstockRequest is REG_MMM_HILL "m" of y on raw x with
// params.adstock geometric over max_lag 6, ordered by week.
func hillAdstockRequest() *Request {
	return &Request{
		Cohort: &types.Cohort{Filename: hillAdstockCohort},
		Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_HILL, Name: "m", Target: "y", Predictors: []string{"x"},
			Params: json.RawMessage(`{"channels": ["x"], "adstock": "geometric", "max_lag": 6}`),
			Order:  &types.RegressionOrder{Field: "week"}}},
	}
}

// TestHillAdstock_ResponseAndSaveReadTheFittedDecay: the compose fit
// recovers the decay with a finite standard error, response() equals
// coef()·hill(Σ decay^l · spend, k, s) at the fitted decay summed over
// the cohort, and the saved model's adstock record is the fitted decay
// bit for bit (geometric, max_lag 6, unnormalised, no fixed_at); predict
// accepts the batch.
func TestHillAdstock_ResponseAndSaveReadTheFittedDecay(t *testing.T) {
	t.Parallel()

	fsys := hillAdstockFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	batch := func() *ComposedRequest {
		fit := hillAdstockRequest()
		fit.Regressions[0].Save = "models/hill_adstock.pulse-model.json"
		return &ComposedRequest{
			Fits: []types.FitSpec{{Name: "hill", Request: fit}},
			Requests: []*Request{{
				Label:  "curve",
				Cohort: &types.Cohort{Filename: hillAdstockCohort},
				Attributes: []*types.Attribute{
					{Type: types.ATTR_FORMULA, Label: "resp", Expression: `response("hill", "x", x)`},
					{Type: types.ATTR_FORMULA, Label: "hand", Expression: `coef("hill", "x") * hill((1 - coef("hill", "decay:x") ^ 7) / (1 - coef("hill", "decay:x")) * x, coef("hill", "k:x"), coef("hill", "s:x"))`},
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "resp", Label: "resp"},
					{Type: types.AGG_SUM, Field: "hand", Label: "hand"},
				},
			}},
		}
	}
	if env, err := p.PredictCompose(context.Background(), batch()); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors[0])
	}
	for name, run := range composeRuns(p) {
		out, err := run(batch())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res := out.Fits["hill"].RegressionResult
		decay := res.Coefficients["decay:x"]
		if math.Abs(decay-hillAdstockDecay) > 0.03 || !(res.StdErrors["decay:x"] > 0) {
			t.Fatalf("%s: decay:x = %v (se %v), generator %v", name, decay, res.StdErrors["decay:x"], hillAdstockDecay)
		}
		if len(res.Advisories) != 0 {
			t.Errorf("%s: a well-identified fit carries advisories %+v", name, res.Advisories)
		}
		row := out.Responses[0].Data[0]
		got, ok1 := row["resp"].(float64)
		want, ok2 := row["hand"].(float64)
		if !ok1 || !ok2 || !(want > 0) || math.Abs(got-want) > 1e-9*want {
			t.Errorf("%s: response() sum %v, hand-written %v", name, row["resp"], row["hand"])
		}
		data, err := afero.ReadFile(fsys, "models/hill_adstock.pulse-model.json")
		if err != nil {
			t.Fatal(err)
		}
		var f struct {
			Adstock map[string]model.Adstock `json:"adstock"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		a, ok := f.Adstock["x"]
		if !ok || a.Kernel != "geometric" || a.Decay == nil || math.Float64bits(*a.Decay) != math.Float64bits(decay) ||
			a.MaxLag != hillAdstockMaxLag || a.Normalize || a.FixedAt != "" || a.Peak != nil {
			t.Errorf("%s: saved record %+v, want geometric decay %v max_lag 6 unnormalised", name, a, decay)
		}
		// The saved file loads back: response() over it is the fit's.
		saved, err := p.Process(context.Background(), &Request{
			Cohort:       &types.Cohort{Filename: hillAdstockCohort},
			Models:       []types.ModelRef{{Name: "saved", Path: "models/hill_adstock.pulse-model.json"}},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "resp", Expression: `response("saved", "x", x)`}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "resp", Label: "resp"}},
		})
		if err != nil {
			t.Fatalf("%s: reload: %v", name, err)
		}
		if back, _ := saved.Data[0]["resp"].(float64); math.Float64bits(back) != math.Float64bits(got) {
			t.Errorf("%s: response() over the saved model %v, over the fit %v", name, saved.Data[0]["resp"], got)
		}
	}
}

// TestHillAdstock_WeibullSaveRecordsShapeAndScale: each Weibull kernel
// fits through Process (predict accepting it), its shape:x / scale:x
// carry finite standard errors, and the saved model's adstock record is
// that kernel at the fitted shape and scale bit for bit (unnormalised,
// no fixed_at), so response() over the reloaded model is the fit's.
func TestHillAdstock_WeibullSaveRecordsShapeAndScale(t *testing.T) {
	t.Parallel()

	fsys := hillAdstockFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	for _, kernel := range []string{"weibull_cdf", "weibull_pdf"} {
		path := "models/hill_" + kernel + ".pulse-model.json"
		req := hillAdstockRequest()
		req.Regressions[0].Params = json.RawMessage(`{"channels": ["x"], "adstock": "` + kernel + `", "max_lag": 6}`)
		req.Regressions[0].Save = path
		if pr, err := p.Predict(context.Background(), req); err != nil || len(pr.Errors) > 0 {
			t.Fatalf("%s: predict: %v %+v", kernel, err, pr.Errors)
		}
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", kernel, err)
		}
		res := resp.Regressions[0]
		if res.Error != nil {
			t.Fatalf("%s: slot failed: %v", kernel, res.Error)
		}
		shape, scale := res.Coefficients["shape:x"], res.Coefficients["scale:x"]
		if !(res.StdErrors["shape:x"] > 0) || !(res.StdErrors["scale:x"] > 0) {
			t.Fatalf("%s: shape:x %v (se %v), scale:x %v (se %v)", kernel, shape, res.StdErrors["shape:x"], scale, res.StdErrors["scale:x"])
		}
		data, err := afero.ReadFile(fsys, path)
		if err != nil {
			t.Fatal(err)
		}
		var f struct {
			Adstock map[string]model.Adstock `json:"adstock"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		a, ok := f.Adstock["x"]
		if !ok || a.Kernel != kernel || a.Shape == nil || a.Scale == nil || math.Float64bits(*a.Shape) != math.Float64bits(shape) ||
			math.Float64bits(*a.Scale) != math.Float64bits(scale) || a.MaxLag != hillAdstockMaxLag || a.Normalize || a.FixedAt != "" || a.Decay != nil {
			t.Errorf("%s: saved record %+v, want shape %v scale %v max_lag 6 unnormalised", kernel, a, shape, scale)
		}
		// response() over the reload is coef()·hill(Σw · spend, k, s),
		// Σw the recorded kernel's weights summed.
		w, err := a.Weights()
		if err != nil {
			t.Fatal(err)
		}
		mult := 0.0
		for _, v := range w {
			mult += v
		}
		saved, err := p.Process(context.Background(), &Request{
			Cohort: &types.Cohort{Filename: hillAdstockCohort},
			Models: []types.ModelRef{{Name: "saved", Path: path}},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "resp", Expression: `response("saved", "x", x)`},
				{Type: types.ATTR_FORMULA, Label: "hand", Expression: `coef("saved", "x") * hill(` + strconv.FormatFloat(mult, 'g', -1, 64) + ` * x, coef("saved", "k:x"), coef("saved", "s:x"))`},
			},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "resp", Label: "resp"}, {Type: types.AGG_SUM, Field: "hand", Label: "hand"}},
		})
		if err != nil {
			t.Fatalf("%s: reload: %v", kernel, err)
		}
		got, ok1 := saved.Data[0]["resp"].(float64)
		want, ok2 := saved.Data[0]["hand"].(float64)
		if !ok1 || !ok2 || !(want > 0) || math.Abs(got-want) > 1e-9*want {
			t.Errorf("%s: response() over the saved model %v, hand-written %v", kernel, saved.Data[0]["resp"], saved.Data[0]["hand"])
		}
	}
}

// TestHillAdstock_RefusalsPredictMatchRuntime: every in-model adstock
// refusal is predict's only error and Process's, code, message and
// details identical.
func TestHillAdstock_RefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: hillAdstockFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	edit := func(f func(*Request)) *Request { r := hillAdstockRequest(); f(r); return r }
	cases := []struct {
		name   string
		req    *Request
		reason string
		// more: predict lists a second, independent fault after the
		// runtime's (the run stops at the first).
		more bool
	}{
		{"order_required", edit(func(r *Request) { r.Regressions[0].Order = nil }), "order_required", false},
		{"adstock_holdout", edit(func(r *Request) { r.Regressions[0].Holdout = "week >= 90" }), model.ReasonAdstockHoldout, false},
		{"kernel_unknown", edit(func(r *Request) {
			r.Regressions[0].Params = json.RawMessage(`{"channels": ["x"], "adstock": "hyperbolic", "max_lag": 6}`)
		}), "kernel_unknown", false},
		{"weibull_max_lag_required", edit(func(r *Request) {
			r.Regressions[0].Params = json.RawMessage(`{"channels": ["x"], "adstock": "weibull_cdf"}`)
		}), "max_lag_required", false},
		{"order_partition", edit(func(r *Request) { r.Regressions[0].Order.Partition = "region" }), "order_partition_unsupported", false},
		// Two faults at once: both sides report the same first one.
		{"unordered_weibull", edit(func(r *Request) {
			r.Regressions[0].Order = nil
			r.Regressions[0].Params = json.RawMessage(`{"channels": ["x"], "adstock": "weibull_pdf"}`)
		}), "order_required", true},
		{"channel_preadstocked", edit(func(r *Request) {
			r.Windows = []*types.Window{{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "x_ad", OrderBy: []types.OrderKey{{Field: "week"}},
				Params: json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 3, "normalize": false}`)}}
			r.Regressions[0].Predictors = []string{"x_ad"}
			r.Regressions[0].Params = json.RawMessage(`{"channels": ["x_ad"], "adstock": "geometric"}`)
		}), model.ReasonChannelPreadstocked, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := p.Process(context.Background(), tc.req)
			var ce *perr.CodedError
			if !asCoded(err, &ce) || ce.Code != perr.PROCESSING_CONFIG || ce.Details["reason"] != tc.reason {
				t.Fatalf("runtime = %v, want PROCESSING_CONFIG reason %s", err, tc.reason)
			}
			pr, perrv := p.Predict(context.Background(), tc.req)
			if perrv != nil {
				t.Fatalf("Predict: %v", perrv)
			}
			if len(pr.Errors) == 0 || (len(pr.Errors) != 1 && !tc.more) {
				t.Fatalf("predict errors = %+v, want exactly the runtime's %s %q", pr.Errors, ce.Code, ce.Message)
			}
			if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict = %+v, runtime %s %q %v", got, ce.Code, ce.Message, ce.Details)
			}
		})
	}
}

// TestHillAdstock_IllConditionedAdvisory: a second channel that
// duplicates the first leaves the joint correlation matrix nearly
// singular, so the fit succeeds — its standard errors kept, never
// nulled — and carries PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED with
// its condition number above the threshold; Strict never escalates it,
// and Options.SuppressAdvisories drops the note but no figure.
func TestHillAdstock_IllConditionedAdvisory(t *testing.T) {
	t.Parallel()

	req := func() *Request {
		r := hillAdstockRequest()
		r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x2", Expression: "x * 1.0000001"}}
		r.Regressions[0].Predictors = []string{"x", "x2"}
		r.Regressions[0].Params = json.RawMessage(`{"channels": ["x", "x2"], "adstock": "geometric", "max_lag": 6}`)
		return r
	}
	fsys := hillAdstockFS(t)
	for _, c := range []struct {
		name string
		opts Options
		want bool
	}{
		{"default", Options{FS: fsys}, true},
		{"strict", Options{FS: fsys, Strict: true}, true},
		{"suppressed", Options{FS: fsys, SuppressAdvisories: []string{string(perr.PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED)}}, false},
	} {
		p, err := New(c.opts)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(context.Background(), req())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		res := resp.Regressions[0]
		if res.Error != nil {
			t.Fatalf("%s: slot failed: %v", c.name, res.Error)
		}
		cond, _ := res.Outputs[model.MMMConditionKey].(float64)
		if !(cond > model.MMMConditionThreshold) || !(res.StdErrors["decay:x"] > 0) {
			t.Fatalf("%s: condition_number %v, se(decay:x) %v", c.name, res.Outputs[model.MMMConditionKey], res.StdErrors["decay:x"])
		}
		fired := len(res.Advisories) == 1 && res.Advisories[0].Code == string(perr.PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED) &&
			res.Advisories[0].Details[model.MMMConditionKey] == cond
		if fired != c.want || (!c.want && len(res.Advisories) != 0) {
			t.Errorf("%s: advisories %+v, want fired=%v", c.name, res.Advisories, c.want)
		}
		if hasWarningCode(resp.Warnings, string(perr.PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED)) {
			t.Errorf("%s: the advisory reached the warnings", c.name)
		}
	}
}

// hasWarningCode reports whether ws carries code.
func hasWarningCode(ws []*types.ResponseWarning, code string) bool {
	for _, w := range ws {
		if w != nil && w.Code == code {
			return true
		}
	}
	return false
}
