package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// glm_penalty_parity_test.go: every penalty-knob refusal (the shared
// rule internal/regpenalty — the coupling, gamma + penalty, vcov on
// lasso / elastic net) is raised by Process and by Predict with the same
// code, message and details, on REG_GLM and REG_OLS alike; a well-formed
// penalised GLM runs, and predict's APPROXIMATE_SE /
// REGULARIZED_SELECTION warnings cover REG_GLM as they cover REG_OLS.

func TestRegPenalty_PredictRuntimeParity(t *testing.T) {
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
	glm := types.RegressionSpec{Name: "fit", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}}
	ols := types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp"}}
	cases := []struct {
		name string
		base types.RegressionSpec
		mut  func(*types.RegressionSpec)
		code errors.Code
	}{
		{"glm gamma + l2", glm, func(s *types.RegressionSpec) { s.Family, s.Target, s.Penalty, s.Alpha = "gamma", "mpg", "l2", 0.1 }, errors.PROCESSING_CONFIG},
		{"glm alpha without penalty", glm, func(s *types.RegressionSpec) { s.Alpha = 0.1 }, errors.PROCESSING_CONFIG},
		{"glm l2 without alpha", glm, func(s *types.RegressionSpec) { s.Penalty = "l2" }, errors.PROCESSING_CONFIG},
		{"glm elasticnet l1_ratio 0", glm, func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "elasticnet", 0.1 }, errors.PROCESSING_CONFIG},
		{"glm l1 vcov", glm, func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Vcov = "l1", 0.01, true }, errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED},
		{"ols alpha without penalty", ols, func(s *types.RegressionSpec) { s.Alpha = 0.1 }, errors.PROCESSING_CONFIG},
		{"ols l1 with l1_ratio", ols, func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "l1", 0.1, 0.5 }, errors.PROCESSING_CONFIG},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := c.base
			c.mut(&spec)
			req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{&spec}}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("Process err = %v, want %s", err, c.code)
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v lack %s %q", pred.Errors, ce.Code, ce.Message)
			}
		})
	}
}

func TestRegPenalty_GLMRunsAndWarns(t *testing.T) {
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
	hasWarning := func(env []*descriptor.EnvelopeEntry, code string) bool {
		for _, w := range env {
			if w.Code == code {
				return true
			}
		}
		return false
	}
	cases := []struct {
		name               string
		mut                func(*types.RegressionSpec)
		approxSE, regSelct bool
	}{
		{"l2", func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Vcov = "l2", 0.05, true }, false, false},
		{"l1", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.01 }, true, false},
		{"elasticnet + jackknife", func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.L1Ratio, s.Resample = "elasticnet", 0.01, 0.5, "jackknife"
		}, false, false},
		{"l2 + selection", func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.Selection, s.Criterion = "l2", 0.05, "backward", "aic"
		}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := types.RegressionSpec{Name: "fit", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}}
			c.mut(&spec)
			req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{&spec}}
			resp, err := New(cfg).Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			r := resp.Regressions[0]
			if r.Error != nil || r.Penalty != spec.Penalty {
				t.Fatalf("result error %+v, penalty echo %q", r.Error, r.Penalty)
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			if len(pred.Errors) != 0 {
				t.Fatalf("predict errors %+v", pred.Errors)
			}
			if got := hasWarning(pred.Warnings, "PROCESSING_REGRESSION_APPROXIMATE_SE"); got != c.approxSE {
				t.Errorf("APPROXIMATE_SE warning %v, want %v", got, c.approxSE)
			}
			if got := hasWarning(pred.Warnings, "PROCESSING_REGRESSION_REGULARIZED_SELECTION"); got != c.regSelct {
				t.Errorf("REGULARIZED_SELECTION warning %v, want %v", got, c.regSelct)
			}
		})
	}
}
