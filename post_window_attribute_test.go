package pulse

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// post_window_attribute_test.go: a bound model fitted on a window label
// is scored in a Compose slot that declares the same window —
// ATTR_REG_PREDICT and coef() run in the post-window phase
// (transformed-predictors E2-S1) — and PredictCompose admits the batch
// the runtime runs.

func facadeLagX() *types.Window {
	return &types.Window{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "week"}}}
}

// TestWindowPredictor_PostWindowScoresWindowTermModel: the fit m is
// REG_OLS y on lag_x; a slot declaring lag_x scores m (ATTR_REG_PREDICT
// p) and rebuilds the score from coef() (ATTR_FORMULA c) on every row.
// Both are null where the lag is, agree on every other row, and equal
// the fit's own coefficients applied to the row's lag.
func TestWindowPredictor_PostWindowScoresWindowTermModel(t *testing.T) {
	t.Parallel()

	p, _ := scoringParityPulse(t)
	ctx := context.Background()
	fit := &Request{
		Cohort:      &types.Cohort{Filename: fitsFacadeCohort},
		Windows:     []*types.Window{facadeLagX()},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"lag_x"}}},
	}
	slot := &Request{
		Label:   "score",
		Cohort:  &types.Cohort{Filename: fitsFacadeCohort},
		Windows: []*types.Window{facadeLagX()},
		Attributes: []*types.Attribute{
			scoreRef(`{"fit":"m"}`),
			{Type: types.ATTR_FORMULA, Label: "c", Expression: `lag_x == nil ? nil : coef("m", "(intercept)") + coef("m", "lag_x") * lag_x`},
		},
	}
	c := &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: fit}}, Requests: []*Request{slot}}
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("PredictCompose refused: %+v", env.Errors)
	}
	runs := map[string]func() (*ComposedResponse, error){
		"Compose": func() (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func() (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
		},
	}
	for name, run := range runs {
		out, err := run()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m := out.Fits["m"]
		if m == nil || m.Error != nil {
			t.Fatalf("%s: fit m = %+v", name, m)
		}
		b0, b1 := m.Coefficients["(intercept)"], m.Coefficients["lag_x"]
		rows := out.Responses[0].Data
		if len(rows) != 104 {
			t.Fatalf("%s: %d rows, want 104", name, len(rows))
		}
		for i, row := range rows {
			lag, has := row["lag_x"].(float64)
			if !has {
				if row["p"] != nil || row["c"] != nil {
					t.Fatalf("%s row %d: scores %v / %v over a null lag", name, i, row["p"], row["c"])
				}
				continue
			}
			want := b0 + b1*lag
			for _, k := range []string{"p", "c"} {
				got, ok := row[k].(float64)
				if !ok || math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
					t.Fatalf("%s row %d: %s = %v, want %v", name, i, k, row[k], want)
				}
			}
		}
	}
}
