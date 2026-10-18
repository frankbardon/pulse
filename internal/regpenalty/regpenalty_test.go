package regpenalty

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheck pins the shared penalty-knob rule on both types it covers;
// every other type passes untouched.
func TestCheck(t *testing.T) {
	glm := func(mod func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Family: "binomial"}
		mod(s)
		return s
	}
	ols := func(mod func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_OLS}
		mod(s)
		return s
	}
	cases := []struct {
		name string
		spec *types.RegressionSpec
		want string // "" = accepted, else the message
	}{
		{"nil", nil, ""},
		{"glm none", glm(func(*types.RegressionSpec) {}), ""},
		{"glm l2", glm(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }), ""},
		{"glm poisson elasticnet", glm(func(s *types.RegressionSpec) {
			s.Family, s.Penalty, s.Alpha, s.L1Ratio = "poisson", "elasticnet", 0.1, 0.4
		}), ""},
		{"glm gamma l1", glm(func(s *types.RegressionSpec) { s.Family, s.Penalty, s.Alpha = "gamma", "l1", 0.1 }),
			"REG_GLM Penalty is defined for the binomial and poisson families only; fit family=gamma unpenalised"},
		{"glm gamma unpenalised", glm(func(s *types.RegressionSpec) { s.Family = "gamma" }), ""},
		{"glm unknown family left to the family check", glm(func(s *types.RegressionSpec) { s.Family, s.Penalty, s.Alpha = "tweedie", "l2", 0.1 }), ""},
		{"glm alpha alone", glm(func(s *types.RegressionSpec) { s.Alpha = 0.1 }), "REG_GLM Alpha is only valid when Penalty is set (l1, l2, or elasticnet)"},
		{"glm l1_ratio alone", glm(func(s *types.RegressionSpec) { s.L1Ratio = 0.1 }), "REG_GLM L1Ratio is only valid when Penalty is elasticnet"},
		{"ols l2 no alpha", ols(func(s *types.RegressionSpec) { s.Penalty = "l2" }), "REG_OLS Penalty=l2 requires Alpha > 0"},
		{"ols l1 with ratio", ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "l1", 0.1, 0.2 }),
			"REG_OLS L1Ratio is only valid when Penalty=elasticnet; use Penalty=elasticnet to mix l1/l2"},
		{"ols elasticnet no alpha", ols(func(s *types.RegressionSpec) { s.Penalty, s.L1Ratio = "elasticnet", 0.5 }), "REG_OLS Penalty=elasticnet requires Alpha > 0"},
		{"ols elasticnet ratio 0", ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "elasticnet", 0.1 }),
			"REG_OLS Penalty=elasticnet requires L1Ratio > 0 (use Penalty=l2 for pure ridge)"},
		{"ols elasticnet ratio 1", ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.1, 1 }),
			"REG_OLS Penalty=elasticnet requires L1Ratio < 1 (use Penalty=l1 for pure lasso)"},
		{"ols unknown", ols(func(s *types.RegressionSpec) { s.Penalty = "l3" }), "REG_OLS Penalty must be one of \"\", \"l1\", \"l2\", \"elasticnet\"; got l3"},
		{"bayes passes", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Penalty: "l2"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Check(c.spec)
			if c.want == "" {
				if got != nil {
					t.Fatalf("refused %v", got)
				}
				return
			}
			if got == nil || got.Code != errors.PROCESSING_CONFIG || got.Message != c.want {
				t.Fatalf("got %v, want PROCESSING_CONFIG %q", got, c.want)
			}
		})
	}
}

// TestVcovRefusal: vcov is refused on l1 / elasticnet on both types and
// nowhere else.
func TestVcovRefusal(t *testing.T) {
	for _, c := range []struct {
		spec   types.RegressionSpec
		refuse bool
	}{
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "l1", Vcov: true}, true},
		{types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Vcov: true}, true},
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "l1"}, false},
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "l2", Vcov: true}, false},
		{types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Penalty: "l1", Vcov: true}, false},
	} {
		got := VcovRefusal(&c.spec)
		if (got != nil) != c.refuse {
			t.Errorf("%+v: refusal %v, want %v", c.spec, got, c.refuse)
		}
		if got != nil && (got.Code != errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED || got.Details["reason"] != "penalty") {
			t.Errorf("%+v: %v, want VCOV_UNSUPPORTED reason penalty", c.spec, got)
		}
	}
}
