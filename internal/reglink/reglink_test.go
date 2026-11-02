package reglink

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheck_Pairs: every family accepts exactly its three links (and an
// empty link, the canonical default); a link of another family or an
// unknown one is PROCESSING_REGRESSION_INVALID_LINK naming the allowed
// set; an unknown family and a non-GLM spec are not this rule's.
func TestCheck_Pairs(t *testing.T) {
	t.Parallel()

	for _, fam := range Families() {
		for _, link := range append(Links(fam), "") {
			if cerr := CheckPair(fam, link); cerr != nil {
				t.Errorf("%s/%q refused: %v", fam, link, cerr)
			}
		}
		for _, link := range append(All(), "probit_x") {
			if contains(Links(fam), link) {
				continue
			}
			cerr := Check(&types.RegressionSpec{Type: types.REG_GLM, Family: fam, Link: link})
			if cerr == nil || cerr.Code != errors.PROCESSING_REGRESSION_INVALID_LINK {
				t.Fatalf("%s/%s: %v, want INVALID_LINK", fam, link, cerr)
			}
			if !reflect.DeepEqual(cerr.Details["allowed"], Links(fam)) || cerr.Details["link"] != link || cerr.Details["family"] != fam {
				t.Errorf("%s/%s details %v", fam, link, cerr.Details)
			}
		}
	}
	if cerr := CheckPair("tweedie", "log"); cerr != nil {
		t.Errorf("unknown family judged here: %v", cerr)
	}
	if cerr := Check(&types.RegressionSpec{Type: types.REG_OLS, Family: "binomial", Link: "log"}); cerr != nil {
		t.Errorf("REG_OLS judged here: %v", cerr)
	}
}

// TestTable: canonical links first, the sorted union is the enum, and
// the tables are copies.
func TestTable(t *testing.T) {
	t.Parallel()

	for fam, want := range map[string]string{"binomial": "logit", "poisson": "log", "gamma": "inverse", NegativeBinomial: "log"} {
		if Canonical(fam) != want || Resolve(fam, "") != want || Resolve(fam, "identity") != "identity" {
			t.Errorf("%s canonical %q", fam, Canonical(fam))
		}
	}
	if got := All(); !reflect.DeepEqual(got, []string{"cloglog", "identity", "inverse", "log", "logit", "probit", "sqrt"}) {
		t.Errorf("All() = %v", got)
	}
	b := ByFamily()
	b["binomial"][0] = "x"
	if Links("binomial")[0] != "logit" {
		t.Error("ByFamily shares its slices")
	}
}

// TestCheckTheta: theta is the negative binomial's knob only, finite and
// > 0 there (absent estimates it); the family refuses a penalty, a
// selection and a resample; each refusal is PROCESSING_CONFIG naming
// details.requires. An extension type is the slot table's, never this
// rule's.
func TestCheckTheta(t *testing.T) {
	t.Parallel()

	nb := func(edit func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Family: NegativeBinomial}
		edit(s)
		return s
	}
	for name, s := range map[string]*types.RegressionSpec{
		"estimated":    nb(func(*types.RegressionSpec) {}),
		"fixed":        nb(func(s *types.RegressionSpec) { s.Theta = 0.3 }),
		"fixed φ = 1":  nb(func(s *types.RegressionSpec) { s.Theta, s.Dispersion = 0.3, types.NegBinDispersionFixed }),
		"φ estimated":  nb(func(s *types.RegressionSpec) { s.Theta, s.Dispersion = 0.3, types.NegBinDispersionEstimated }),
		"robust":       nb(func(s *types.RegressionSpec) { s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC0} }),
		"poisson":      {Type: types.REG_GLM, Family: "poisson", Penalty: "l2", Selection: "forward", Resample: "bootstrap"},
		"extension":    {Type: "REG_X_CUSTOM", Theta: 2},
		"model regr.":  {Type: types.REG_MMM_HILL, Theta: 2},
		"nil-safe ols": {Type: types.REG_OLS},
	} {
		if cerr := CheckTheta(s); cerr != nil {
			t.Errorf("%s refused: %v", name, cerr)
		}
	}
	if CheckTheta(nil) != nil {
		t.Error("nil spec refused")
	}
	for _, tc := range []struct {
		name     string
		spec     *types.RegressionSpec
		requires string
	}{
		{"theta on poisson", &types.RegressionSpec{Type: types.REG_GLM, Family: "poisson", Theta: 2}, "family negative_binomial"},
		{"theta on REG_OLS", &types.RegressionSpec{Type: types.REG_OLS, Theta: 2}, "family negative_binomial"},
		{"theta on REG_BAYES_LINEAR", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Theta: 2}, "family negative_binomial"},
		{"negative theta", nb(func(s *types.RegressionSpec) { s.Theta = -1 }), "theta > 0"},
		{"infinite theta", nb(func(s *types.RegressionSpec) { s.Theta = math.Inf(1) }), "theta > 0"},
		{"NaN theta", nb(func(s *types.RegressionSpec) { s.Theta = math.NaN() }), "theta > 0"},
		{"penalty", nb(func(s *types.RegressionSpec) { s.Penalty = "l1" }), "unpenalized"},
		{"selection", nb(func(s *types.RegressionSpec) { s.Selection = "backward" }), "no_selection"},
		{"resample", nb(func(s *types.RegressionSpec) { s.Resample = "bootstrap"; s.Theta = 2 }), "no_resample"},
		{"dispersion on gamma", &types.RegressionSpec{Type: types.REG_GLM, Family: "gamma", Dispersion: types.NegBinDispersionEstimated}, "family negative_binomial"},
		{"dispersion on REG_OLS", &types.RegressionSpec{Type: types.REG_OLS, Dispersion: types.NegBinDispersionFixed}, "family negative_binomial"},
		{"dispersion with θ estimated", nb(func(s *types.RegressionSpec) { s.Dispersion = types.NegBinDispersionEstimated }), "theta"},
		{"fixed dispersion with θ estimated", nb(func(s *types.RegressionSpec) { s.Dispersion = types.NegBinDispersionFixed }), "theta"},
		{"unknown dispersion", nb(func(s *types.RegressionSpec) { s.Theta, s.Dispersion = 2, "quasi" }), "dispersion fixed or estimated"},
	} {
		cerr := CheckTheta(tc.spec)
		if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["requires"] != tc.requires {
			t.Errorf("%s: %v, want PROCESSING_CONFIG requires %q", tc.name, cerr, tc.requires)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
