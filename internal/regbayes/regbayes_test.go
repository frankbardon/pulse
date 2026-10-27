package regbayes

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheck pins the shared REG_BAYES_LINEAR rule: the refused
// modifiers, the prior knobs (a per-coefficient prior_precision's length
// and entries included) and the other engines' knobs; every other type
// passes untouched.
func TestCheck(t *testing.T) {
	t.Parallel()

	bayes := func(mod func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"a", "b"}}
		mod(s)
		return s
	}
	cases := []struct {
		name string
		spec *types.RegressionSpec
		want string // "" = accepted, else a message prefix
	}{
		{"nil", nil, ""},
		{"defaults", bayes(func(*types.RegressionSpec) {}), ""},
		{"ols passes", &types.RegressionSpec{Type: types.REG_OLS, Resample: "bootstrap"}, ""},
		{"resample", bayes(func(s *types.RegressionSpec) { s.Resample = "bootstrap" }), "REG_BAYES_LINEAR does not accept Resample"},
		{"selection", bayes(func(s *types.RegressionSpec) { s.Selection = "forward" }), "REG_BAYES_LINEAR does not accept Selection"},
		{"prior enum", bayes(func(s *types.RegressionSpec) { s.Prior = "horseshoe" }), "REG_BAYES_LINEAR Prior must be one of"},
		{"scalar precision", bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionScalar(2) }), ""},
		{"negative scalar precision", bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionScalar(-1) }), "REG_BAYES_LINEAR PriorPrecision must be > 0"},
		{"vector precision", bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{0, 40, 1e-3}) }), ""},
		{"vector all flat", bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{0, 0, 0}) }), ""},
		{"vector short", bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{1, 1}) }), "REG_BAYES_LINEAR PriorPrecision vector length"},
		{"vector empty", bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector(nil) }), "REG_BAYES_LINEAR PriorPrecision vector length"},
		{"vector negative", bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{1, -0.5, 1}) }), "REG_BAYES_LINEAR PriorPrecision entries must be finite"},
		{"shape", bayes(func(s *types.RegressionSpec) { s.PriorShape = -1 }), "REG_BAYES_LINEAR PriorShape"},
		{"rate", bayes(func(s *types.RegressionSpec) { s.PriorRate = -1 }), "REG_BAYES_LINEAR PriorRate"},
		{"credible", bayes(func(s *types.RegressionSpec) { s.CredibleLevel = 1 }), "REG_BAYES_LINEAR CredibleLevel"},
		{"prior_mu length", bayes(func(s *types.RegressionSpec) { s.PriorMu = []float64{1} }), "REG_BAYES_LINEAR PriorMu length"},
		{"penalty", bayes(func(s *types.RegressionSpec) { s.Penalty = "l2" }), "REG_BAYES_LINEAR does not accept Penalty"},
		{"alpha", bayes(func(s *types.RegressionSpec) { s.Alpha = 1 }), "REG_BAYES_LINEAR does not accept Alpha"},
		{"l1_ratio", bayes(func(s *types.RegressionSpec) { s.L1Ratio = 0.5 }), "REG_BAYES_LINEAR does not accept L1Ratio"},
		{"unpenalized", bayes(func(s *types.RegressionSpec) { s.Unpenalized = []string{"a"} }), "REG_BAYES_LINEAR does not accept Unpenalized"},
		{"family", bayes(func(s *types.RegressionSpec) { s.Family = "binomial" }), "REG_BAYES_LINEAR does not accept Family"},
		{"link", bayes(func(s *types.RegressionSpec) { s.Link = "logit" }), "REG_BAYES_LINEAR does not accept Link"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Check(c.spec, nil)
			if c.want == "" {
				if got != nil {
					t.Fatalf("refused %v", got)
				}
				return
			}
			if got == nil || got.Code != errors.PROCESSING_CONFIG || !strings.HasPrefix(got.Message, c.want) {
				t.Fatalf("got %v, want PROCESSING_CONFIG %q…", got, c.want)
			}
		})
	}
	// A negative entry names its coefficient.
	got := Check(bayes(func(s *types.RegressionSpec) { s.PriorPrecision = types.PrecisionVector([]float64{1, 1, -2}) }), nil)
	if got.Details["index"] != 2 || got.Details["coefficient"] != "b" {
		t.Errorf("details %v", got.Details)
	}
}

// TestRemedies: a remedy's nil rendering is the historical literal and
// drops out when the type it names is not offered.
func TestRemedies(t *testing.T) {
	t.Parallel()

	none := func(string) bool { return false }
	for _, r := range Remedies() {
		if r(nil) == "" || r(none) != "" {
			t.Errorf("remedy nil %q, none %q", r(nil), r(none))
		}
	}
	msg := Check(&types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Resample: "jackknife"}, none).Message
	if strings.Contains(msg, "REG_OLS") {
		t.Errorf("hidden REG_OLS named: %q", msg)
	}
}
