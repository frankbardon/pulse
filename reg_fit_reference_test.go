package pulse

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// reg_fit_reference_test.go pins custom-regressions E2-S3 (FR-25):
// params.fit on ATTR_REG_FITTED / ATTR_REG_RESIDUAL scores a bound fit
// — a REG_GLM's fitted mean and response residual, a custom fit through
// its registration's Scorer — instead of the OLS refit. A custom fit
// without a Scorer is no_scorer, an unknown name PULSE_MODEL_UNKNOWN, a
// refit field beside params.fit slot_fields: each refused by predict
// exactly as by the runtime. Absent params.fit the slot is the OLS
// refit, unchanged.

const glmResidualCohort = "glm_residual.pulse"

// glmResidualRows is a two-level Poisson case whose maximum-likelihood
// fit is closed-form: with one predictor taking two values the log-link
// model is saturated in them, so each row's fitted mean is its level's
// mean count — x = 1.5 → (2+3+4+7)/4 = 4, x = 3 → (6+9+12+13)/4 = 10.
var glmResidualRows = []struct {
	x, y, mean float64
}{
	{1.5, 2, 4}, {1.5, 3, 4}, {1.5, 4, 4}, {1.5, 7, 4},
	{3, 6, 10}, {3, 9, 10}, {3, 12, 10}, {3, 13, 10},
}

func glmResidualFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := fitsFacadeFS(t)
	var rows [][]string
	for i, r := range glmResidualRows {
		rows = append(rows, []string{jsonNumber(float64(i)), jsonNumber(r.x), jsonNumber(r.y)})
	}
	createTestPulseFile(t, fsys, glmResidualCohort, []string{"id", "x", "y"}, rows)
	return fsys
}

func glmPoissonFit() *Request {
	return &Request{
		Cohort:      &types.Cohort{Filename: glmResidualCohort},
		Regressions: []*types.RegressionSpec{{Type: types.REG_GLM, Name: "m", Family: "poisson", Target: "y", Predictors: []string{"x"}}},
	}
}

// fitRefSlot is one row per id over cohort, with attrs and the minimum
// of each output label (the row's own value).
func fitRefSlot(cohort, key string, attrs ...*types.Attribute) *Request {
	aggs := []*types.Aggregation{{Type: types.AGG_MIN, Field: "y", Label: "y"}}
	for _, a := range attrs {
		aggs = append(aggs, &types.Aggregation{Type: types.AGG_MIN, Field: a.Label, Label: a.Label})
	}
	return &Request{
		Label:        "s",
		Cohort:       &types.Cohort{Filename: cohort},
		Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: key, Interval: 1}},
		Attributes:   attrs,
		Aggregations: aggs,
	}
}

func fitRefAttr(typ types.AttributeType, label, params string) *types.Attribute {
	return &types.Attribute{Type: typ, Label: label, Params: json.RawMessage(params)}
}

// TestRegFitReference_GLMResidual: params.fit naming a Poisson REG_GLM
// fit gives each row its level's mean count as the fitted value and
// y − that mean as the residual (the closed-form MLE, within the IRLS
// tolerance); the fitted value is the ATTR_REG_PREDICT score to the bit
// and the residual is y minus it exactly.
func TestRegFitReference_GLMResidual(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: glmResidualFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	slot := fitRefSlot(glmResidualCohort, "id",
		fitRefAttr(types.ATTR_REG_FITTED, "f", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_PREDICT, "p", `{"fit":"m"}`),
	)
	out, err := p.Compose(context.Background(), &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "m", Request: glmPoissonFit()}},
		Requests: []*Request{slot},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := out.Responses[0].Data
	if len(data) != len(glmResidualRows) {
		t.Fatalf("%d rows, want %d", len(data), len(glmResidualRows))
	}
	for i, row := range data {
		want := glmResidualRows[i]
		f, r, pr, y := row["f"].(float64), row["r"].(float64), row["p"].(float64), row["y"].(float64)
		if math.Abs(f-want.mean) > 1e-9*want.mean {
			t.Errorf("row %d: fitted %v, want the level mean %v", i, f, want.mean)
		}
		if math.Abs(r-(want.y-want.mean)) > 1e-9*want.mean {
			t.Errorf("row %d: residual %v, want y − mean = %v", i, r, want.y-want.mean)
		}
		if math.Float64bits(f) != math.Float64bits(pr) {
			t.Errorf("row %d: fitted %v is not the ATTR_REG_PREDICT score %v", i, f, pr)
		}
		if math.Float64bits(r) != math.Float64bits(y-pr) {
			t.Errorf("row %d: residual %v is not y − score %v", i, r, y-pr)
		}
	}
}

// TestRegFitReference_CustomFit: params.fit naming a custom fit whose
// registration supplies a Scorer scores through it (residual = y − the
// model's own formula, to the bit); without a Scorer the reference is
// refused no_scorer — predict exactly as Compose — naming the slot.
func TestRegFitReference_CustomFit(t *testing.T) {
	t.Parallel()

	fsys := fitsFacadeFS(t)
	ctx := context.Background()
	scored := centredInstance(t, fsys, centredRegistration(centredScorerFactory))
	slot := fitRefSlot(fitsFacadeCohort, "week",
		fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_FITTED, "f", `{"fit":"m"}`),
	)
	slot.Aggregations = append(slot.Aggregations, &types.Aggregation{Type: types.AGG_MIN, Field: "x", Label: "x"})
	c := &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: centredFitRequest()}}, Requests: []*Request{slot}}
	out, err := scored.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	res := out.Fits["m"].RegressionResult
	shift := res.Outputs["shift"].(float64)
	data := out.Responses[0].Data
	if len(data) != 104 {
		t.Fatalf("%d rows", len(data))
	}
	for _, row := range data {
		score := centredScore(res.Coefficients["level"], res.Coefficients["gain"], shift, row["x"].(float64))
		if got := row["f"].(float64); math.Float64bits(got) != math.Float64bits(score) {
			t.Fatalf("week %v: fitted %v, want %v", row["week"], got, score)
		}
		if got, want := row["r"].(float64), row["y"].(float64)-score; math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("week %v: residual %v, want %v", row["week"], got, want)
		}
	}

	plain := centredInstance(t, fsys, centredRegistration(nil))
	for _, typ := range []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL} {
		t.Run(string(typ), func(t *testing.T) {
			c := &ComposedRequest{
				Fits:     []types.FitSpec{{Name: "m", Request: centredFitRequest()}},
				Requests: []*Request{fitRefSlot(fitsFacadeCohort, "week", fitRefAttr(typ, "o", `{"fit":"m"}`))},
			}
			env, err := plain.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, perr.PULSE_MODEL_SCORE_INVALID)
			if pe.details["reason"] != model.ReasonNoScorer || pe.details["attribute"] != string(typ) {
				t.Fatalf("predict details %v, want reason %s on %s", pe.details, model.ReasonNoScorer, typ)
			}
			_, rerr := plain.Compose(ctx, c)
			assertSameRefusal(t, string(typ), rerr, pe)
		})
	}
}

// TestRegFitReference_PredictRuntimeParity: an unknown fit name, a refit
// field beside params.fit, an empty name and a params shape that is not
// {fit} are refused by PredictCompose exactly as Compose refuses them;
// outside a compose nothing is bound, and Predict refuses the name
// exactly as Process does.
func TestRegFitReference_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	fsys := glmResidualFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	withTarget := fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m"}`)
	withTarget.Target = "y"
	cases := []struct {
		name   string
		attr   *types.Attribute
		code   perr.Code
		reason string
	}{
		{"unknown fit", fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"nope"}`), perr.PULSE_MODEL_UNKNOWN, ""},
		{"unknown fit (fitted)", fitRefAttr(types.ATTR_REG_FITTED, "f", `{"fit":"nope"}`), perr.PULSE_MODEL_UNKNOWN, ""},
		{"target beside fit", withTarget, perr.PULSE_MODEL_SCORE_INVALID, model.ReasonSlotFields},
		{"empty fit name", fitRefAttr(types.ATTR_REG_FITTED, "f", `{"fit":""}`), perr.PULSE_MODEL_SCORE_INVALID, model.ReasonFitMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposedRequest{
				Fits:     []types.FitSpec{{Name: "m", Request: glmPoissonFit()}},
				Requests: []*Request{fitRefSlot(glmResidualCohort, "id", tc.attr)},
			}
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, tc.code)
			if tc.reason != "" && pe.details["reason"] != tc.reason {
				t.Fatalf("predict details %v, want reason %s", pe.details, tc.reason)
			}
			_, rerr := p.Compose(ctx, c)
			assertSameRefusal(t, tc.name, rerr, pe)
		})
	}

	t.Run("plain request", func(t *testing.T) {
		req := fitRefSlot(glmResidualCohort, "id", fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m"}`))
		data, err := afero.ReadFile(fsys, glmResidualCohort)
		if err != nil {
			t.Fatal(err)
		}
		env, err := p.PredictBytes(ctx, data, req)
		if err != nil {
			t.Fatal(err)
		}
		pe := findEnvError(t, env, perr.PULSE_MODEL_UNKNOWN)
		_, rerr := p.Process(ctx, req)
		assertSameRefusal(t, "plain", rerr, pe)
	})
}

// TestRegFitReference_RowLocal: a params.fit slot holds its fit before
// the scan, so predict calls it streamable beside a grouper (the refit
// twin is not), and ProcessStream yields Process's rows.
func TestRegFitReference_RowLocal(t *testing.T) {
	t.Parallel()

	fsys := glmResidualFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Bind the fit through a loaded model so a plain request scores it.
	fit := glmPoissonFit()
	fit.Regressions[0].Save = "models/glm" + model.FileSuffix
	if _, err := p.Process(ctx, fit); err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(fsys, glmResidualCohort)
	if err != nil {
		t.Fatal(err)
	}
	scored := fitRefSlot(glmResidualCohort, "id", fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m"}`))
	scored.Models = []types.ModelRef{{Name: "m", Path: fit.Regressions[0].Save}}
	refit := fitRefSlot(glmResidualCohort, "id", &types.Attribute{Type: types.ATTR_REG_RESIDUAL, Label: "r", Target: "y", Predictors: []string{"x"}})
	for _, tc := range []struct {
		name string
		req  *Request
		want bool
	}{{"params.fit", scored, true}, {"refit", refit, false}} {
		env, err := p.PredictBytes(ctx, data, tc.req)
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("%s: predict %v %+v", tc.name, err, env.Errors)
		}
		if got := env.Data.(*descriptor.PredictResult).Streamable; got != tc.want {
			t.Errorf("%s: predict streamable %v, want %v", tc.name, got, tc.want)
		}
	}
	whole, err := p.Process(ctx, scored)
	if err != nil {
		t.Fatal(err)
	}
	streamed := collectStreamRows(t, p, scored)
	if a, b := centredJSON(t, whole.Data), centredJSON(t, streamed); a != b {
		t.Fatalf("ProcessStream rows differ from Process:\n%s\n%s", b, a)
	}
}

// TestRegFitReference_AbsentIsRefit: without params.fit the slot is the
// OLS refit, byte for byte — params absent, null or {} give one
// response, and its default label stays <TYPE>_<target>; with it the
// default label is <TYPE>_<fit>.
func TestRegFitReference_AbsentIsRefit(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: glmResidualFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := func(params string) string {
		a := &types.Attribute{Type: types.ATTR_REG_RESIDUAL, Target: "y", Predictors: []string{"x"}, Params: json.RawMessage(params)}
		req := &Request{
			Cohort:       &types.Cohort{Filename: glmResidualCohort},
			Attributes:   []*types.Attribute{a},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "ATTR_REG_RESIDUAL_y", Label: "s"}, {Type: types.AGG_STDDEV, Field: "ATTR_REG_RESIDUAL_y", Label: "sd"}},
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("params %q: %v", params, err)
		}
		return centredJSON(t, resp)
	}
	base := run("")
	for _, params := range []string{"null", "{}"} {
		if got := run(params); got != base {
			t.Errorf("params %s changed the refit:\n%s\n%s", params, got, base)
		}
	}
	slot := fitRefSlot(glmResidualCohort, "id", &types.Attribute{Type: types.ATTR_REG_FITTED, Params: json.RawMessage(`{"fit":"m"}`)})
	slot.Aggregations = []*types.Aggregation{{Type: types.AGG_MIN, Field: "ATTR_REG_FITTED_m", Label: "f"}}
	out, err := p.Compose(ctx, &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: glmPoissonFit()}}, Requests: []*Request{slot}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := out.Responses[0].Data[0]["f"].(float64); !ok || math.Abs(got-4) > 1e-9 {
		t.Fatalf("default label ATTR_REG_FITTED_m not scored: %v", out.Responses[0].Data[0])
	}
}

// collectStreamRows drains ProcessStream over req.
func collectStreamRows(t *testing.T, p *Pulse, req *Request) []map[string]any {
	t.Helper()
	ctx := context.Background()
	it, err := p.ProcessStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = it.Close() }()
	var rows []map[string]any
	for {
		row, ok, err := it.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return rows
		}
		rows = append(rows, row)
	}
}
