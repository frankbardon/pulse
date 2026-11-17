package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestRegressionSelectionCriterion_PredictRuntimeParity: a REG_OLS or
// REG_GLM selection without a criterion is refused by Process with
// predict's code, message and details — SERVICE_VALIDATION
// {selection}, the one rule regresample.CheckSelectionCriterion that
// ValidateRegression applies to every built-in (U68 decision I13; the
// runtime once refused it PROCESSING_CONFIG from inside the search).
func TestRegressionSelectionCriterion_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

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
	cases := []types.RegressionSpec{
		{Name: "ols", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp"}, Selection: "backward"},
		{Name: "glm", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}, Selection: "forward"},
		{Name: "glm-penalized", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}, Selection: "stepwise", Penalty: "l2", Alpha: 0.05},
	}
	for _, spec := range cases {
		t.Run(spec.Name, func(t *testing.T) {
			req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{&spec}}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
				t.Fatalf("Process err = %v, want SERVICE_VALIDATION", err)
			}
			if got := ce.Details["selection"]; got != spec.Selection {
				t.Errorf("details.selection = %v, want %s", got, spec.Selection)
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
