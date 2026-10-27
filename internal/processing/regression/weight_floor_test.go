package regression

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// U36 E1-S7: under kind frequency the regressions' n ≥ p + 1 guard
// reads Σw (weighting.Basis.FloorN), so a fit over fewer raw rows than
// p + 1 whose Σw reaches it equals the unweighted fit on the expanded
// rows: the penalised and Bayesian fits (a penalty / prior makes them
// solvable on any design) run, and the plain OLS / GLM fits answer with
// the expansion's own rank-deficiency refusal. Probability weights keep
// the raw-row floor.

// fitAnyErr fits spec over rows (kind "" unweighted) and returns the
// fit's error instead of failing.
func fitAnyErr(t *testing.T, spec types.RegressionSpec, rows []wRegRow, kind types.WeightKind) (*types.RegressionResult, error) {
	t.Helper()
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	var be BufferedEngine
	switch spec.Type {
	case types.REG_BAYES_LINEAR:
		be = newBayes(t, &spec)
	case types.REG_GLM:
		be = asBuffered(t, newGLM(t, &spec))
	default:
		be = newOLS(t, &spec)
	}
	return be.FitBuffered(wRegRecords(rows))
}

// sameRegFigure: equal ±Inf / NaN class, else relative 1e-10.
func sameRegFigure(got, want float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	return relClose(got, want, 1e-10)
}

func TestWeightedRegression_FrequencyFloorIsExpansion(t *testing.T) {
	t.Parallel()

	// one: a single raw row standing for three (p = 1); two: two raw
	// rows standing for four (p = 2). Both sit below the raw p + 1.
	one := []wRegRow{{x1: 2, x2: 0, y: 3, w: 3}}
	two := []wRegRow{{x1: 1.5, x2: -0.5, y: 2.25, w: 2}, {x1: 3.0, x2: 1.25, y: 4.5, w: 2}}
	p1 := types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}}
	p2 := types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}}
	with := func(s types.RegressionSpec, mod func(*types.RegressionSpec)) types.RegressionSpec {
		mod(&s)
		return s
	}
	ridge := func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.3 }
	lasso := func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Tol = "l1", 0.05, 1e-12 }
	enet := func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio, s.Tol = "elasticnet", 0.05, 0.5, 1e-12 }
	bayes := func(s *types.RegressionSpec) { s.Type = types.REG_BAYES_LINEAR }
	glm := func(s *types.RegressionSpec) {
		s.Type, s.Family, s.Tol, s.MaxIters = types.REG_GLM, "poisson", 1e-14, 100
	}
	for _, c := range []struct {
		name string
		spec types.RegressionSpec
		rows []wRegRow
		runs bool
	}{
		{"ols_p1", p1, one, false},
		{"glm_p1", with(p1, glm), one, false},
		{"ridge_p1", with(p1, ridge), one, true},
		{"lasso_p1", with(p1, lasso), one, false},
		{"elasticnet_p1", with(p1, enet), one, false},
		{"bayes_p1", with(p1, bayes), one, true},
		{"ridge_p2", with(p2, ridge), two, true},
		{"lasso_p2", with(p2, lasso), two, true},
		{"elasticnet_p2", with(p2, enet), two, true},
		{"bayes_p2", with(p2, bayes), two, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, gotErr := fitAnyErr(t, c.spec, c.rows, types.WeightKindFrequency)
			want, wantErr := fitAnyErr(t, c.spec, expand(c.rows), "")
			if c.runs != (wantErr == nil) {
				t.Fatalf("expansion err %v, runs %v", wantErr, c.runs)
			}
			if wantErr != nil {
				var g, w *errors.CodedError
				if !stderrors.As(gotErr, &g) || !stderrors.As(wantErr, &w) || g.Code != w.Code {
					t.Fatalf("weighted err %v, want the expansion's %v", gotErr, wantErr)
				}
				return
			}
			if gotErr != nil {
				t.Fatalf("weighted refuses (%v) where the expansion runs", gotErr)
			}
			if got.NObs != len(c.rows) || got.SumWeights != float64(len(expand(c.rows))) {
				t.Fatalf("n_obs %d sum_weights %v", got.NObs, got.SumWeights)
			}
			for _, m := range [][2]map[string]float64{{got.Coefficients, want.Coefficients}, {got.StdErrors, want.StdErrors}, {got.PValues, want.PValues}} {
				if len(m[0]) != len(m[1]) {
					t.Fatalf("keys %v, want %v", m[0], m[1])
				}
				for k, w := range m[1] {
					if !sameRegFigure(m[0][k], w) {
						t.Errorf("%s = %.17g, want the expansion's %.17g", k, m[0][k], w)
					}
				}
			}
			if !sameRegFigure(got.R2, want.R2) || !sameRegFigure(got.ResidualStdErr, want.ResidualStdErr) {
				t.Errorf("r2 / rse %v %v, want %v %v", got.R2, got.ResidualStdErr, want.R2, want.ResidualStdErr)
			}
			if c.spec.Type == types.REG_OLS {
				if _, err := fitAnyErr(t, c.spec, c.rows, types.WeightKindProbability); err == nil ||
					!stderrors.As(err, new(*errors.CodedError)) || err.(*errors.CodedError).Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA {
					t.Errorf("probability: err %v, want the raw-row floor's PROCESSING_REGRESSION_INSUFFICIENT_DATA", err)
				}
			}
		})
	}
}

// TestWeightedRefit_FrequencyFloorIsFullFit: the weighted (JK1)
// jackknife's replicate refits keep the full fit's guard — Σw under
// frequency — so a refit over two raw rows of Σw = 4 runs a ridge and
// reproduces the full engine fit, and a poisson GLM refit answers with
// the rank check, never the raw-row floor.
func TestWeightedRefit_FrequencyFloorIsFullFit(t *testing.T) {
	t.Parallel()

	two := []wRegRow{{x1: 1.5, x2: -0.5, y: 2.25, w: 2}, {x1: 3.0, x2: 1.25, y: 4.5, w: 2}}
	spec := types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}, Penalty: "l2", Alpha: 0.3,
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})}
	refit, err := olsFitter(wRegRecords(two), "y", spec.Predictors, &spec)
	if err != nil {
		t.Fatalf("ridge refit: %v", err)
	}
	full, err := fitAnyErr(t, spec, two, "")
	if err != nil {
		t.Fatalf("full fit: %v", err)
	}
	if !relClose(refit.Intercept, full.Coefficients["(intercept)"], 1e-12) || !relClose(refit.Slopes[0], full.Coefficients["x1"], 1e-12) {
		t.Errorf("refit %v %v, full fit %v", refit.Intercept, refit.Slopes, full.Coefficients)
	}
	one := []wRegRow{{x1: 2, y: 3, w: 3}}
	knobs := types.RegressionSpec{Type: types.REG_GLM, Family: "poisson", Target: "y", Predictors: []string{"x1"},
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})}
	_, err = glmFitter(wRegRecords(one), "y", knobs.Predictors, poissonLog, 50, 1e-12, &knobs)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_RANK_DEFICIENT {
		t.Errorf("poisson refit over one raw row of Σw 3: err %v, want the rank check's PROCESSING_REGRESSION_RANK_DEFICIENT", err)
	}
}
