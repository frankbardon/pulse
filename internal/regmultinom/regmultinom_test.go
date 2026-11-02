package regmultinom

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func schemaOf(t *testing.T) *encoding.Schema {
	t.Helper()
	d := encoding.NewDictionary()
	for _, l := range []string{"lo", "mid", "hi"} {
		if _, err := d.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
}

func base() *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Name: "m", Target: "c", Predictors: []string{"x"}}
}

// TestCheck_Refusals: every knob REG_MULTINOMIAL does not offer is
// PROCESSING_CONFIG naming its slot and what to change; `reference` on
// another regression too; the bare spec and its own knobs pass.
func TestCheck_Refusals(t *testing.T) {
	t.Parallel()

	off := false
	cases := []struct {
		name, slot, requires string
		mut                  func(*types.RegressionSpec)
	}{
		{"family", "family", "no_family", func(s *types.RegressionSpec) { s.Family = "binomial" }},
		{"link", "link", "no_family", func(s *types.RegressionSpec) { s.Link = "logit" }},
		{"theta", "theta", "no_family", func(s *types.RegressionSpec) { s.Theta = 2 }},
		{"dispersion", "dispersion", "no_family", func(s *types.RegressionSpec) { s.Dispersion = "estimated" }},
		{"penalty", "penalty", "unpenalized", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 1 }},
		{"alpha", "alpha", "unpenalized", func(s *types.RegressionSpec) { s.Alpha = 1 }},
		{"l1_ratio", "l1_ratio", "unpenalized", func(s *types.RegressionSpec) { s.L1Ratio = 0.5 }},
		{"unpenalized", "unpenalized", "unpenalized", func(s *types.RegressionSpec) { s.Unpenalized = []string{"x"} }},
		{"bounds", "bounds", "unbounded", func(s *types.RegressionSpec) { s.Bounds = &types.RegressionBounds{Nonnegative: []string{"x"}} }},
		{"selection", "selection", "no_selection", func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }},
		{"criterion", "criterion", "no_selection", func(s *types.RegressionSpec) { s.Criterion = "aic" }},
		{"resample", "resample", "no_resample", func(s *types.RegressionSpec) { s.Resample = "jackknife" }},
		{"jackknife_groups", "jackknife_groups", "no_resample", func(s *types.RegressionSpec) { s.JackknifeGroups = 3 }},
		{"jackknife_by", "jackknife_by", "no_resample", func(s *types.RegressionSpec) { s.JackknifeBy = "x" }},
		{"bootstrap_iters", "bootstrap_iters", "no_resample", func(s *types.RegressionSpec) { s.BootstrapIters = 10 }},
		{"rng_seed", "rng_seed", "no_resample", func(s *types.RegressionSpec) { s.RNGSeed = 3 }},
		{"prior", "prior", "no_prior", func(s *types.RegressionSpec) { s.Prior = "nig" }},
		{"prior_mu", "prior_mu", "no_prior", func(s *types.RegressionSpec) { s.PriorMu = []float64{0, 0} }},
		{"prior_shape", "prior_shape", "no_prior", func(s *types.RegressionSpec) { s.PriorShape = 1 }},
		{"prior_rate", "prior_rate", "no_prior", func(s *types.RegressionSpec) { s.PriorRate = 1 }},
		{"credible_level", "credible_level", "no_prior", func(s *types.RegressionSpec) { s.CredibleLevel = 0.9 }},
		{"solver", "solver", "no_solver", func(s *types.RegressionSpec) { s.Solver = "qr" }},
		{"intercept", "intercept", "intercept", func(s *types.RegressionSpec) { s.Intercept = &off }},
		{"covariance", "covariance", "classical_covariance", func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
		}},
		{"margins", "margins", "no_margins", func(s *types.RegressionSpec) { s.Margins = &types.RegressionMargins{} }},
		{"collinearity", "collinearity", "no_collinearity", func(s *types.RegressionSpec) { s.Collinearity = &types.RegressionCollinearity{} }},
		{"save", "save", "no_save", func(s *types.RegressionSpec) { s.Save = "m.pulse-model.json" }},
		{"negative max_iters", "max_iters", "max_iters ≥ 0", func(s *types.RegressionSpec) { s.MaxIters = -1 }},
		{"negative tol", "tol", "tol ≥ 0", func(s *types.RegressionSpec) { s.Tol = -1e-6 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := base()
			tc.mut(s)
			cerr := Check(s)
			if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["slot"] != tc.slot || cerr.Details["requires"] != tc.requires || cerr.Details["name"] != "m" {
				t.Fatalf("Check = %v, want PROCESSING_CONFIG slot %s requires %s", cerr, tc.slot, tc.requires)
			}
		})
	}
	ok := base()
	ok.Reference, ok.MaxIters, ok.Tol, ok.ConfLevel, ok.Vcov = "mid", 10, 1e-9, 0.9, true
	ok.Covariance = &types.RegressionCovariance{Type: types.CovarianceClassical}
	if cerr := Check(ok); cerr != nil {
		t.Fatalf("offered knobs refused: %v", cerr)
	}
	ols := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Reference: "lo"}
	if cerr := Check(ols); cerr == nil || cerr.Details["slot"] != "reference" || cerr.Details["requires"] != "type REG_MULTINOMIAL" {
		t.Fatalf("reference on REG_OLS: %v", cerr)
	}
	ols.Reference = ""
	ols.Family = "binomial" // another engine's knob is not this rule's
	if cerr := Check(ols); cerr != nil {
		t.Fatalf("REG_OLS without reference: %v", cerr)
	}
}

// TestCheckTarget: the target must be a cohort categorical (a derived
// name is refused, a numeric field left to the accepted-types table) and
// a reference one of its labels.
func TestCheckTarget(t *testing.T) {
	t.Parallel()

	schema := schemaOf(t)
	s := base()
	if cerr := CheckTarget(s, schema); cerr != nil {
		t.Fatalf("bare: %v", cerr)
	}
	s.Reference = "hi"
	if cerr := CheckTarget(s, schema); cerr != nil {
		t.Fatalf("a label: %v", cerr)
	}
	s.Reference = "top"
	if cerr := CheckTarget(s, schema); cerr == nil || cerr.Details["requires"] != "a level of the target" || cerr.Details["slot"] != "reference" {
		t.Fatalf("unknown label: %v", cerr)
	}
	s.Reference, s.Target = "", "derived"
	if cerr := CheckTarget(s, schema); cerr == nil || cerr.Details["requires"] != "categorical_target" {
		t.Fatalf("derived target: %v", cerr)
	}
	s.Target = "x"
	if cerr := CheckTarget(s, schema); cerr != nil {
		t.Fatalf("numeric target is fieldaccept's: %v", cerr)
	}
	if levels, ok := Levels(schema, "c"); !ok || len(levels) != 3 || levels[0] != "lo" || levels[2] != "hi" {
		t.Fatalf("Levels = %v %v", levels, ok)
	}
	if Key("mid", "(intercept)") != "mid:(intercept)" {
		t.Fatal("Key")
	}
}
