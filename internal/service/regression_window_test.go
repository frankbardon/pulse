package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// regression_window_test.go: a regression names a same-request derived
// column end to end (transformed-predictors E1-S1) — a window label over
// the record rows (new), a FEAT_* output and an ATTR_FORMULA hill()
// label (already supported, pinned here) — and predict and Process
// refuse a window label alike where no record carries it.

// derivedRegCohort writes t, x (> 0), y (≈ 2·previous x + 1), ylog
// (1 + 3·log1p x), yhill (2 + 5·hill(x, 3, 2)) and w (a frequency
// weight) as reg.pulse; it returns the service and the file bytes.
func derivedRegCohort(t *testing.T) (*Service, *fs.Config, []byte) {
	t.Helper()
	const n = 48
	rows := make([][]float64, n)
	prev := 0.0
	for i := range rows {
		x := 0.5 + float64(i%9) + 0.25*math.Sin(float64(i))
		y := float64(2*prev) + 1 + 0.05*math.Cos(float64(5*i))
		ylog := 1 + float64(3*math.Log1p(x))
		yhill := 2 + float64(5/(1+math.Pow(3/x, 2)))
		rows[i] = []float64{float64(i), x, y, ylog, yhill, float64(1 + i%3)}
		prev = x
	}
	cfg := fs.NewMemMap()
	writeF64Cohort(t, cfg, "reg.pulse", []string{"t", "x", "y", "ylog", "yhill", "w"}, rows, nil)
	data, err := afero.ReadFile(cfg.Fs(), "reg.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), cfg, data
}

func lagX() *types.Window {
	return &types.Window{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}
}

func onlyRegression(t *testing.T, resp *types.Response) *types.RegressionResult {
	t.Helper()
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		t.Fatalf("regressions = %+v", resp.Regressions)
	}
	return resp.Regressions[0]
}

// TestWindowPredictor_ServiceFitsOnWindowLabel: Process fits REG_OLS on
// a WIN_LAG label (the null first lag dropped listwise) and predict
// accepts the same request.
func TestWindowPredictor_ServiceFitsOnWindowLabel(t *testing.T) {
	t.Parallel()

	svc, _, data := derivedRegCohort(t)
	req := &types.Request{
		Cohort:      &types.Cohort{Filename: "reg.pulse"},
		Windows:     []*types.Window{lagX()},
		Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"lag_x"}}},
	}
	if pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{}); len(pred.Errors) != 0 {
		t.Fatalf("predict refused: %+v", pred.Errors)
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	r := onlyRegression(t, resp)
	if r.NObs != 47 || math.Abs(r.Coefficients["lag_x"]-2) > 0.05 {
		t.Fatalf("n_obs %d, slope %v; want 47, ≈ 2", r.NObs, r.Coefficients["lag_x"])
	}
}

// TestWindowPredictor_PredictRuntimeParity: a window label in an
// aggregated request (PULSE_REGRESSION_WINDOW_UNAVAILABLE) and as a
// slot weight (SERVICE_VALIDATION) are refused by Process and by
// predict with the same code and message.
func TestWindowPredictor_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	svc, _, data := derivedRegCohort(t)
	cases := map[string]struct {
		req  *types.Request
		code errors.Code
	}{
		"aggregated shape": {&types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "s"}},
			Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "s", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "s"}}}},
			Regressions:  []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"lag_x"}}},
		}, errors.PULSE_REGRESSION_WINDOW_UNAVAILABLE},
		"weight": {&types.Request{
			Windows: []*types.Window{lagX()},
			Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"},
				Weight: types.SlotWeightOf(types.WeightSpec{Field: "lag_x", Kind: types.WeightKindFrequency})}},
		}, errors.SERVICE_VALIDATION},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.req.Cohort = &types.Cohort{Filename: "reg.pulse"}
			_, err := svc.Process(context.Background(), c.req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code || !strings.Contains(ce.Message, "window output lag_x") {
				t.Fatalf("Process err = %v, want %s", err, c.code)
			}
			pred := descx.Predict(bytes.NewReader(data), c.req, &descx.PredictOptions{})
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

// TestWindowPredictor_RegressionOnFeatureOutput pins REG_OLS on a
// FEAT_LOG output label end to end: the fit recovers ylog = 1 + 3·log1p x.
func TestWindowPredictor_RegressionOnFeatureOutput(t *testing.T) {
	t.Parallel()

	svc, _, _ := derivedRegCohort(t)
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort:      &types.Cohort{Filename: "reg.pulse"},
		Features:    []*types.Feature{{Type: types.FEAT_LOG, Field: "x"}},
		Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "ylog", Predictors: []string{"LOG_x"}}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	r := onlyRegression(t, resp)
	if r.NObs != 48 || math.Abs(r.Coefficients["LOG_x"]-3) > 1e-9 || math.Abs(r.Coefficients["(intercept)"]-1) > 1e-9 {
		t.Fatalf("n_obs %d, coefficients %v; want 48, slope 3, intercept 1", r.NObs, r.Coefficients)
	}
}

// TestWindowPredictor_RegressionOnFormulaLabel pins REG_OLS on an
// ATTR_FORMULA hill() label end to end: the fit recovers
// yhill = 2 + 5·hill(x, 3, 2).
func TestWindowPredictor_RegressionOnFormulaLabel(t *testing.T) {
	t.Parallel()

	svc, _, _ := derivedRegCohort(t)
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort:      &types.Cohort{Filename: "reg.pulse"},
		Attributes:  []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "sat", Expression: "hill(x, 3, 2)"}},
		Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "yhill", Predictors: []string{"sat"}}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	r := onlyRegression(t, resp)
	if r.NObs != 48 || math.Abs(r.Coefficients["sat"]-5) > 1e-9 || math.Abs(r.Coefficients["(intercept)"]-2) > 1e-9 {
		t.Fatalf("n_obs %d, coefficients %v; want 48, slope 5, intercept 2", r.NObs, r.Coefficients)
	}
}
