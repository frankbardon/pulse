package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// regression_shrinkage_parity_test.go: the per-coefficient shrinkage
// controls — REG_BAYES_LINEAR's prior_precision vector (the shared rule
// internal/regbayes) and REG_OLS ridge `unpenalized` (internal/regpenalty)
// — and REG_BAYES_LINEAR's refused resample / selection modifiers are
// refused by Process and by Predict with the same code, message and
// details. Before regbayes, predict accepted a Bayes fit with resample or
// selection that the runtime refused.

func TestRegShrinkage_PredictRuntimeParity(t *testing.T) {
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	cohort := filepath.Join(dir, "mtcars.pulse")
	data, err := os.ReadFile(cohort)
	if err != nil {
		t.Fatal(err)
	}
	bayes := types.RegressionSpec{Name: "fit", Type: types.REG_BAYES_LINEAR, Target: "mpg", Predictors: []string{"wt", "hp"}}
	ridge := types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp"}, Penalty: "l2", Alpha: 0.1}
	glm := types.RegressionSpec{Name: "fit", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}}
	cases := []struct {
		name string
		base types.RegressionSpec
		mut  func(*types.RegressionSpec)
	}{
		{"bayes resample", bayes, func(s *types.RegressionSpec) { s.Resample = "bootstrap" }},
		{"bayes jackknife", bayes, func(s *types.RegressionSpec) { s.Resample = "jackknife" }},
		{"bayes selection", bayes, func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }},
		{"bayes precision vector short", bayes, func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{1, 1}) }},
		{"bayes precision vector long", bayes, func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{1, 1, 1, 1}) }},
		{"bayes precision negative entry", bayes, func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{0, -1, 1}) }},
		{"bayes precision negative scalar", bayes, func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionScalar(-2) }},
		{"bayes penalty", bayes, func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }},
		{"bayes unpenalized", bayes, func(s *types.RegressionSpec) { s.Unpenalized = []string{"wt"} }},
		{"bayes family", bayes, func(s *types.RegressionSpec) { s.Family = "binomial" }},
		{"ridge unpenalized unknown", ridge, func(s *types.RegressionSpec) { s.Unpenalized = []string{"cyl"} }},
		{"ridge unpenalized twice", ridge, func(s *types.RegressionSpec) { s.Unpenalized = []string{"wt", "wt"} }},
		{"lasso unpenalized", ridge, func(s *types.RegressionSpec) { s.Penalty, s.Unpenalized = "l1", []string{"wt"} }},
		{"elasticnet unpenalized", ridge, func(s *types.RegressionSpec) {
			s.Penalty, s.L1Ratio, s.Unpenalized = "elasticnet", 0.5, []string{"wt"}
		}},
		{"ols unpenalized without penalty", ridge, func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Unpenalized = "", 0, []string{"wt"} }},
		{"glm unpenalized", glm, func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Unpenalized = "l2", 0.1, []string{"wt"} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := c.base
			c.mut(&spec)
			req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{&spec}}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("Process err = %v, want PROCESSING_CONFIG", err)
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message && reflect.DeepEqual(e.Details, ce.Details) {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v lack %s %q %v", pred.Errors, ce.Code, ce.Message, ce.Details)
			}
		})
	}
}

// TestRegShrinkage_WireRequestsRun: the two controls arrive through a
// strict JSON request — prior_precision as an array, unpenalized as a
// list — predict raises nothing and the fits run: a flat-intercept,
// strongly-pinned hp prior holds β(hp) on its prior mean, and an
// unpenalized wt escapes the ridge shrinkage the fully penalised fit
// applies.
func TestRegShrinkage_WireRequestsRun(t *testing.T) {
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	cohort := filepath.Join(dir, "mtcars.pulse")
	data, err := os.ReadFile(cohort)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"cohort":{"filename":` + jsonString(cohort) + `},"regressions":[
		{"name":"bayes","type":"REG_BAYES_LINEAR","target":"mpg","predictors":["wt","hp"],
		 "prior_mu":[0,0,-0.03],"prior_precision":[0,0.001,1e12]},
		{"name":"ridge_free_wt","type":"REG_OLS","target":"mpg","predictors":["wt","hp"],"penalty":"l2","alpha":2,"unpenalized":["wt"]},
		{"name":"ridge","type":"REG_OLS","target":"mpg","predictors":["wt","hp"],"penalty":"l2","alpha":2}]}`
	var req types.Request
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if pred := descx.Predict(bytes.NewReader(data), &req, &descx.PredictOptions{}); len(pred.Errors) != 0 {
		t.Fatalf("predict errors %+v", pred.Errors)
	}
	resp, err := New(cfg).Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	byName := map[string]*types.RegressionResult{}
	for _, r := range resp.Regressions {
		byName[r.Name] = r
	}
	if got := byName["bayes"].Coefficients["hp"]; got < -0.03-1e-9 || got > -0.03+1e-9 {
		t.Errorf("pinned β(hp) = %.17g, want its prior mean -0.03", got)
	}
	free, full := byName["ridge_free_wt"].Coefficients["wt"], byName["ridge"].Coefficients["wt"]
	if !(free < full && full < 0) {
		t.Errorf("β(wt): unpenalized %v should sit further from 0 than the penalised %v", free, full)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
