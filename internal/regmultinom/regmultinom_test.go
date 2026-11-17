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
		// U68 E2-S3: a penalised fit is symmetric, so it names no
		// reference (the penalty knobs themselves are regpenalty's).
		{"reference under penalty", "reference", "unpenalized", func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Reference = "l2", 1, "mid" }},
		{"bounds", "bounds", "unbounded", func(s *types.RegressionSpec) { s.Bounds = &types.RegressionBounds{Nonnegative: []string{"x"}} }},
		{"prior", "prior", "no_prior", func(s *types.RegressionSpec) { s.Prior = "nig" }},
		{"prior_mu", "prior_mu", "no_prior", func(s *types.RegressionSpec) { s.PriorMu = []float64{0, 0} }},
		{"prior_shape", "prior_shape", "no_prior", func(s *types.RegressionSpec) { s.PriorShape = 1 }},
		{"prior_rate", "prior_rate", "no_prior", func(s *types.RegressionSpec) { s.PriorRate = 1 }},
		{"credible_level", "credible_level", "no_prior", func(s *types.RegressionSpec) { s.CredibleLevel = 0.9 }},
		{"solver", "solver", "no_solver", func(s *types.RegressionSpec) { s.Solver = "qr" }},
		{"intercept", "intercept", "intercept", func(s *types.RegressionSpec) { s.Intercept = &off }},
		{"margins", "margins", "no_margins", func(s *types.RegressionSpec) { s.Margins = &types.RegressionMargins{} }},
		{"collinearity", "collinearity", "no_collinearity", func(s *types.RegressionSpec) { s.Collinearity = &types.RegressionCollinearity{} }},
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
	ok.Save = "m.pulse-model.json" // U68 E1-S2: a multinomial fit saves
	ok.Covariance = &types.RegressionCovariance{Type: types.CovarianceClassical}
	// U68 E2-S2: the resample and selection modifiers run.
	ok.Resample, ok.JackknifeBy, ok.BootstrapIters, ok.RNGSeed = "jackknife", "g", 10, 3
	ok.Selection, ok.Criterion = "stepwise", "bic"
	if cerr := Check(ok); cerr != nil {
		t.Fatalf("offered knobs refused: %v", cerr)
	}
	// U68 E2-S3: the penalty knobs run (their coupling is regpenalty's).
	pen := base()
	pen.Penalty, pen.Alpha, pen.L1Ratio, pen.Unpenalized = "elasticnet", 0.1, 0.5, []string{"x"}
	if cerr := Check(pen); cerr != nil {
		t.Fatalf("penalty knobs refused: %v", cerr)
	}
	// U68 E3-S1: a robust covariance passes this rule (every type: hc2 /
	// hc3 / hac and the modifier pairings are regcov's refusals).
	for _, ct := range types.AllCovarianceTypes() {
		rob := base()
		rob.Covariance = &types.RegressionCovariance{Type: ct}
		if cerr := Check(rob); cerr != nil {
			t.Fatalf("covariance %s refused by regmultinom: %v", ct, cerr)
		}
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
