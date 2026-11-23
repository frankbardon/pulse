package regpenalty

import (
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheck pins the shared penalty-knob rule on the three types it
// covers; every other type passes untouched.
func TestCheck(t *testing.T) {
	t.Parallel()

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
	mn := func(mod func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Predictors: []string{"a", "b"}}
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
		{"ols ridge unpenalized", ols(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a", "b"}, "l2", 0.1, []string{"b"}
		}), ""},
		{"ols unpenalized without penalty", ols(func(s *types.RegressionSpec) { s.Predictors, s.Unpenalized = []string{"a"}, []string{"a"} }),
			"REG_OLS Unpenalized requires a Penalty (l1, l2 or elasticnet); an unpenalized fit already leaves every coefficient unshrunk"},
		// U69 E1-S1 (#487): lasso and elastic net take penalty factors.
		{"ols lasso unpenalized", ols(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a"}, "l1", 0.1, []string{"a"}
		}), ""},
		{"ols elasticnet unpenalized", ols(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.L1Ratio, s.Unpenalized = []string{"a"}, "elasticnet", 0.1, 0.5, []string{"a"}
		}), ""},
		{"ols lasso unpenalized unknown", ols(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a"}, "l1", 0.1, []string{"z"}
		}), "REG_OLS Unpenalized names z, which is not one of Predictors"},
		{"ols unpenalized unknown", ols(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a"}, "l2", 0.1, []string{"z"}
		}), "REG_OLS Unpenalized names z, which is not one of Predictors"},
		{"ols unpenalized duplicate", ols(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a"}, "l2", 0.1, []string{"a", "a"}
		}), "REG_OLS Unpenalized lists a twice"},
		// REG_MULTINOMIAL (U68 E2-S3): the same coupling; unpenalized
		// on every penalty kind.
		{"multinomial l2", mn(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }), ""},
		{"multinomial alpha alone", mn(func(s *types.RegressionSpec) { s.Alpha = 0.1 }), "REG_MULTINOMIAL Alpha is only valid when Penalty is set (l1, l2, or elasticnet)"},
		{"multinomial elasticnet ratio 1", mn(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.1, 1 }),
			"REG_MULTINOMIAL Penalty=elasticnet requires L1Ratio < 1 (use Penalty=l1 for pure lasso)"},
		{"multinomial lasso unpenalized", mn(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Unpenalized = "l1", 0.1, []string{"a"} }), ""},
		{"multinomial elasticnet unpenalized", mn(func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.L1Ratio, s.Unpenalized = "elasticnet", 0.1, 0.5, []string{"b", "a"}
		}), ""},
		{"multinomial unpenalized without penalty", mn(func(s *types.RegressionSpec) { s.Unpenalized = []string{"a"} }),
			"REG_MULTINOMIAL Unpenalized requires a Penalty (l1, l2 or elasticnet); an unpenalized fit already leaves every coefficient unshrunk"},
		{"multinomial unpenalized unknown", mn(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Unpenalized = "l2", 0.1, []string{"z"} }),
			"REG_MULTINOMIAL Unpenalized names z, which is not one of Predictors"},
		{"multinomial unpenalized duplicate", mn(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Unpenalized = "l1", 0.1, []string{"b", "b"} }),
			"REG_MULTINOMIAL Unpenalized lists b twice"},
		// REG_GLM (U69 E1-S1): every penalty kind takes it.
		{"glm ridge unpenalized", glm(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a"}, "l2", 0.1, []string{"a"}
		}), ""},
		{"glm lasso unpenalized", glm(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a", "b"}, "l1", 0.1, []string{"b"}
		}), ""},
		{"glm elasticnet unpenalized", glm(func(s *types.RegressionSpec) {
			s.Family, s.Predictors, s.Penalty, s.Alpha, s.L1Ratio, s.Unpenalized = "poisson", []string{"a", "b"}, "elasticnet", 0.1, 0.5, []string{"a"}
		}), ""},
		{"glm unpenalized without penalty", glm(func(s *types.RegressionSpec) { s.Predictors, s.Unpenalized = []string{"a"}, []string{"a"} }),
			"REG_GLM Unpenalized requires a Penalty (l1, l2 or elasticnet); an unpenalized fit already leaves every coefficient unshrunk"},
		{"glm unpenalized duplicate", glm(func(s *types.RegressionSpec) {
			s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = []string{"a"}, "l1", 0.1, []string{"a", "a"}
		}), "REG_GLM Unpenalized lists a twice"},
		{"glm gamma penalty with unpenalized stays the family refusal", glm(func(s *types.RegressionSpec) {
			s.Family, s.Predictors, s.Penalty, s.Alpha, s.Unpenalized = "gamma", []string{"a"}, "l2", 0.1, []string{"a"}
		}), "REG_GLM Penalty is defined for the binomial and poisson families only; fit family=gamma unpenalised"},
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

// TestVcovRefusal: vcov is refused on l1 / elasticnet REG_OLS /
// REG_GLM and on any penalised REG_MULTINOMIAL, nowhere else.
func TestVcovRefusal(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		spec   types.RegressionSpec
		refuse bool
	}{
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "l1", Vcov: true}, true},
		{types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Vcov: true}, true},
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "l1"}, false},
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "l2", Vcov: true}, false},
		{types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Penalty: "l1", Vcov: true}, false},
		// A penalised multinomial fit has no coefficient covariance on
		// any kind (U68 E2-S3), under a modifier too.
		{types.RegressionSpec{Type: types.REG_MULTINOMIAL, Penalty: "l2", Vcov: true}, true},
		{types.RegressionSpec{Type: types.REG_MULTINOMIAL, Penalty: "l2", Vcov: true, Resample: "bootstrap"}, true},
		{types.RegressionSpec{Type: types.REG_MULTINOMIAL, Vcov: true}, false},
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

// TestInferenceUndefined: l1 / elasticnet REG_OLS / REG_GLM and any
// penalised REG_MULTINOMIAL without Resample — and nothing else — has
// undefined inference, and its advisory names the slot, operator and
// penalty.
func TestInferenceUndefined(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		spec types.RegressionSpec
		want bool
	}{
		{types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1"}, true},
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "elasticnet", Selection: "forward"}, true},
		{types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1", Resample: "bootstrap"}, false},
		{types.RegressionSpec{Type: types.REG_GLM, Penalty: "l1", Resample: "jackknife"}, false},
		{types.RegressionSpec{Type: types.REG_OLS, Penalty: "l2"}, false},
		{types.RegressionSpec{Type: types.REG_OLS}, false},
		{types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Penalty: "l1"}, false},
		// Every penalised multinomial kind, ridge included (U68 E2-S3).
		{types.RegressionSpec{Type: types.REG_MULTINOMIAL, Penalty: "l2"}, true},
		{types.RegressionSpec{Type: types.REG_MULTINOMIAL, Penalty: "l1", Selection: "forward"}, true},
		{types.RegressionSpec{Type: types.REG_MULTINOMIAL, Penalty: "l2", Resample: "jackknife"}, false},
		{types.RegressionSpec{Type: types.REG_MULTINOMIAL}, false},
	} {
		if got := InferenceUndefined(&c.spec); got != c.want {
			t.Errorf("%+v: %v, want %v", c.spec, got, c.want)
		}
	}
	if InferenceUndefined(nil) {
		t.Error("nil spec")
	}
	msg, d := InferenceAdvisory(&types.RegressionSpec{Type: types.REG_GLM, Penalty: "l1"}, "regressions[2]")
	if d["slot"] != "regressions[2]" || d["operator"] != "REG_GLM" || d["penalty"] != "l1" || msg == "" {
		t.Errorf("advisory %q %v", msg, d)
	}
	msg, d = InferenceAdvisory(&types.RegressionSpec{Type: types.REG_MULTINOMIAL, Penalty: "l2"}, "regressions[0]")
	if d["operator"] != "REG_MULTINOMIAL" || d["penalty"] != "l2" || !strings.Contains(msg, "symmetric") {
		t.Errorf("multinomial advisory %q %v", msg, d)
	}
	if InferenceAdvisoryCode != errors.PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED {
		t.Errorf("code %s", InferenceAdvisoryCode)
	}
}

// TestUnpenalizedMask: the mask follows the given predictor order by
// name and is nil without a list.
func TestUnpenalizedMask(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Unpenalized: []string{"b"}}
	got := UnpenalizedMask(spec, []string{"c", "b", "a"})
	if len(got) != 3 || got[0] || !got[1] || got[2] {
		t.Errorf("mask %v", got)
	}
	if UnpenalizedMask(&types.RegressionSpec{}, []string{"a"}) != nil || UnpenalizedMask(nil, nil) != nil {
		t.Error("mask without a list is not nil")
	}
}

// TestPenalizedTerms: the per-term mask — intercept never penalized,
// an Unpenalized name never, every other predictor always; matched by
// name over the given (possibly subset) predictors.
func TestPenalizedTerms(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_GLM, Penalty: "l1", Predictors: []string{"a", "b", "c"}, Unpenalized: []string{"b"}}
	if got := PenalizedTerms(spec, spec.Predictors); !slices.Equal(got, []bool{false, true, false, true}) {
		t.Errorf("full design = %v", got)
	}
	if got := PenalizedTerms(spec, []string{"c", "b"}); !slices.Equal(got, []bool{false, true, false}) {
		t.Errorf("subset design = %v", got)
	}
	spec.Unpenalized = nil
	if got := PenalizedTerms(spec, spec.Predictors); !slices.Equal(got, []bool{false, true, true, true}) {
		t.Errorf("no unpenalized = %v", got)
	}
}
