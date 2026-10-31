package regsolve

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func ols(mut func(*types.RegressionSpec)) *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b"}}
	if mut != nil {
		mut(s)
	}
	return s
}

func noIntercept(s *types.RegressionSpec) {
	f := false
	s.Intercept = &f
}

func TestCheck(t *testing.T) {
	t.Parallel()

	yes := true
	cases := []struct {
		name string
		spec *types.RegressionSpec
		slot string // details.slot for an intercept refusal, "" for none
		bad  bool
	}{
		{"nil", nil, "", false},
		{"defaults", ols(nil), "", false},
		{"solver qr", ols(func(s *types.RegressionSpec) { s.Solver = SolverQR }), "", false},
		{"solver normal", ols(func(s *types.RegressionSpec) { s.Solver = SolverNormal }), "", false},
		{"intercept true explicit", ols(func(s *types.RegressionSpec) { s.Intercept = &yes }), "", false},
		{"through origin", ols(noIntercept), "", false},
		{"through origin weighted", ols(func(s *types.RegressionSpec) {
			noIntercept(s)
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
		}), "", false},
		{"through origin vcov", ols(func(s *types.RegressionSpec) { noIntercept(s); s.Vcov = true }), "", false},
		{"through origin solver qr", ols(func(s *types.RegressionSpec) { noIntercept(s); s.Solver = SolverQR }), "", false},
		{"extension type passes", &types.RegressionSpec{Type: "REG_ACME_X", Solver: "zz"}, "", false},

		{"unknown solver", ols(func(s *types.RegressionSpec) { s.Solver = "svd" }), "", true},
		{"solver on glm", ols(func(s *types.RegressionSpec) { s.Type, s.Family, s.Solver = types.REG_GLM, "binomial", SolverQR }), "", true},
		{"solver with penalty", ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Solver = "l2", 0.1, SolverQR }), "", true},
		{"solver with bounds", ols(func(s *types.RegressionSpec) {
			s.Bounds, s.Solver = &types.RegressionBounds{Nonnegative: []string{"a"}}, SolverNormal
		}), "", true},
		{"through origin on bayes", ols(func(s *types.RegressionSpec) { noIntercept(s); s.Type = types.REG_BAYES_LINEAR }), "", true},
		{"through origin penalty", ols(func(s *types.RegressionSpec) { noIntercept(s); s.Penalty, s.Alpha = "l1", 0.1 }), "penalty", true},
		{"through origin bounds", ols(func(s *types.RegressionSpec) {
			noIntercept(s)
			s.Bounds = &types.RegressionBounds{Nonnegative: []string{"a"}}
		}), "bounds", true},
		{"through origin selection", ols(func(s *types.RegressionSpec) { noIntercept(s); s.Selection, s.Criterion = "forward", "aic" }), "selection", true},
		{"through origin resample", ols(func(s *types.RegressionSpec) { noIntercept(s); s.Resample = "jackknife" }), "resample", true},
		{"through origin robust", ols(func(s *types.RegressionSpec) {
			noIntercept(s)
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
		}), "covariance", true},
		{"through origin normal", ols(func(s *types.RegressionSpec) { noIntercept(s); s.Solver = SolverNormal }), "solver", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := Check(c.spec)
			if !c.bad {
				if got != nil {
					t.Fatalf("Check = %v, want nil", got)
				}
				return
			}
			if got == nil || got.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("Check = %v, want PROCESSING_CONFIG", got)
			}
			if c.slot != "" && got.Details["slot"] != c.slot {
				t.Errorf("details.slot = %v, want %s", got.Details["slot"], c.slot)
			}
		})
	}
}
