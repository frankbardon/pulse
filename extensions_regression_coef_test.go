package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression_coef_test.go pins custom-regressions decision
// #6: coef(fit, name[, stat]) resolves every coefficient a registered
// model's fit reports — the names the model gives its own parameters
// (here `level` / `gain`, neither a predictor) included — with its
// point, se and the engine's conf_level interval. An unknown name is
// PULSE_MODEL_TERM_UNKNOWN: a loaded model file names its coefficients,
// so predict refuses it exactly as the runtime does; a Compose `fits`
// entry's coefficients exist only once it runs, so predict accepts a
// model-named term on an extension fit and the runtime refuses an
// unknown one.

const regCoefName types.RegressionType = "REG_ACME_CENTREDSE"

// centredSEFit is centredFit reporting a standard error on each of its
// own coefficients, so the engine writes conf_level intervals.
type centredSEFit struct{ centredFit }

var centredSE = map[string]float64{"level": 0.5, "gain": 0.125}

func (c centredSEFit) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out, err := c.centredFit.Fit(rows)
	for i := range out.Coefficients {
		out.Coefficients[i].StdErr = extend.Figure(centredSE[out.Coefficients[i].Name])
	}
	return out, err
}

func centredSERegistration() RegressionRegistration {
	reg := centredRegistration(centredScorerFactory)
	reg.Name = regCoefName
	reg.Factory = func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		return centredSEFit{centredFit{spec: spec}}, nil
	}
	return reg
}

func coefFitRequest() *Request {
	req := centredFitRequest()
	req.Regressions[0].Type = regCoefName
	req.Regressions[0].ConfLevel = 0.9
	return req
}

// coefSlot evaluates each expression as an ATTR_FORMULA once (one row
// per region), binding models.
func coefSlot(exprs map[string]string, models ...types.ModelRef) *Request {
	req := &Request{
		Label:  "coef",
		Cohort: &types.Cohort{Filename: fitsFacadeCohort},
		Models: models,
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
	}
	for label, expr := range exprs {
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Label: label, Expression: expr})
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MIN, Field: label, Label: label})
	}
	return req
}

func TestExtensions_RegressionCoefModelNamed(t *testing.T) {
	fsys := fitsFacadeFS(t)
	p := centredInstance(t, fsys, centredSERegistration())
	ctx := context.Background()
	const save = "models/centredse" + model.FileSuffix
	exprs := map[string]string{
		"gain":     `coef("m", "gain")`,
		"level":    `coef("m", "level")`,
		"gain_lo":  `coef("m", "gain", "lower")`,
		"gain_hi":  `coef("m", "gain", "upper")`,
		"level_lo": `coef("m", "level", "lower")`,
		"gain_se":  `coef("m", "gain", "se")`,
	}
	out, err := p.Compose(ctx, &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "m", Request: coefFitRequest(), Save: save}},
		Requests: []*Request{coefSlot(exprs)},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.Fits["m"].RegressionResult
	if res.ConfLevel != 0.9 {
		t.Fatalf("fit conf_level %v, want 0.9", res.ConfLevel)
	}
	want := map[string]float64{
		"gain": res.Coefficients["gain"], "level": res.Coefficients["level"],
		"gain_lo": res.CILower["gain"], "gain_hi": res.CIUpper["gain"],
		"level_lo": res.CILower["level"], "gain_se": res.StdErrors["gain"],
	}
	for k, v := range want {
		if math.IsNaN(v) {
			t.Fatalf("fit reports %s as NaN", k)
		}
	}
	checkCoefRow := func(t *testing.T, data []map[string]any) {
		t.Helper()
		if len(data) == 0 {
			t.Fatal("no rows")
		}
		for label, w := range want {
			got, ok := data[0][label].(float64)
			if !ok || math.Float64bits(got) != math.Float64bits(w) {
				t.Errorf("%s = %v, want %v", label, data[0][label], w)
			}
		}
	}
	checkCoefRow(t, out.Responses[0].Data)

	// The saved model file reads the same figures on a fresh instance.
	fresh := centredInstance(t, fsys, centredSERegistration())
	loaded, err := fresh.Process(ctx, coefSlot(exprs, types.ModelRef{Name: "m", Path: save}))
	if err != nil {
		t.Fatal(err)
	}
	checkCoefRow(t, loaded.Data)

	cohort, err := afero.ReadFile(fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("loaded model: predict ≡ runtime", func(t *testing.T) {
		ok := coefSlot(exprs, types.ModelRef{Name: "m", Path: save})
		if env, err := fresh.PredictBytes(ctx, cohort, ok); err != nil || len(env.Errors) != 0 {
			t.Fatalf("predict on model-named terms: %v %+v", err, env.Errors)
		}
		bad := coefSlot(map[string]string{"n": `coef("m", "nope")`}, types.ModelRef{Name: "m", Path: save})
		_, rerr := fresh.Process(ctx, bad)
		var ce *perr.CodedError
		if !asCoded(rerr, &ce) || ce.Code != perr.PULSE_MODEL_TERM_UNKNOWN {
			t.Fatalf("runtime: %v, want PULSE_MODEL_TERM_UNKNOWN", rerr)
		}
		wantAvail := []string{model.InterceptKey, "x", "gain", "level"}
		if !reflect.DeepEqual(ce.Details["available"], wantAvail) {
			t.Errorf("available = %v, want %v", ce.Details["available"], wantAvail)
		}
		env, err := fresh.PredictBytes(ctx, cohort, bad)
		if err != nil || len(env.Errors) != 1 {
			t.Fatalf("predict: %v %+v, want the one refusal", err, env.Errors)
		}
		e := env.Errors[0]
		pd, _ := json.Marshal(e.Details)
		rd, _ := json.Marshal(ce.Details)
		if e.Code != string(ce.Code) || e.Message != ce.Message || string(pd) != string(rd) {
			t.Fatalf("predict %s %q %s, runtime %s %q %s", e.Code, e.Message, pd, ce.Code, ce.Message, rd)
		}
	})

	t.Run("compose fit: model-named accepted at predict, unknown refused at runtime", func(t *testing.T) {
		good := &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: coefFitRequest()}}, Requests: []*Request{coefSlot(exprs)}}
		if env, err := p.PredictCompose(ctx, good); err != nil || len(env.Errors) != 0 {
			t.Fatalf("predict-compose on model-named terms: %v %+v", err, env.Errors)
		}
		bad := &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: coefFitRequest()}}, Requests: []*Request{coefSlot(map[string]string{"n": `coef("m", "nope")`})}}
		if env, err := p.PredictCompose(ctx, bad); err != nil || len(env.Errors) != 0 {
			t.Fatalf("predict-compose cannot know an extension fit's coefficients, so it accepts: %v %+v", err, env.Errors)
		}
		_, rerr := p.Compose(ctx, bad)
		var ce *perr.CodedError
		if !asCoded(rerr, &ce) || ce.Code != perr.PULSE_MODEL_TERM_UNKNOWN {
			t.Fatalf("runtime: %v, want PULSE_MODEL_TERM_UNKNOWN", rerr)
		}
	})

	t.Run("built-in fit: unknown term refused at predict too", func(t *testing.T) {
		builtin := facadeFitRequest("0", "79")
		bad := &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: builtin}}, Requests: []*Request{coefSlot(map[string]string{"n": `coef("m", "gain")`})}}
		env, err := p.PredictCompose(ctx, bad)
		if err != nil || len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_MODEL_TERM_UNKNOWN) {
			t.Fatalf("predict-compose on a REG_OLS fit: %v %+v, want PULSE_MODEL_TERM_UNKNOWN", err, env.Errors)
		}
	})
}
