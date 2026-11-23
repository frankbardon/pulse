package regmargins

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestTerms: the block's terms, or every predictor in order; nil without
// a block.
func TestTerms(t *testing.T) {
	t.Parallel()

	s := &types.RegressionSpec{Predictors: []string{"a", "b"}}
	if Terms(s) != nil || Terms(nil) != nil {
		t.Error("no block, no terms")
	}
	s.Margins = &types.RegressionMargins{}
	if got := Terms(s); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("default terms %v", got)
	}
	s.Margins.Terms = []string{"b"}
	if got := Terms(s); !slices.Equal(got, []string{"b"}) {
		t.Errorf("terms %v", got)
	}
}

// TestCheck_Refusals: each refusal is PROCESSING_CONFIG with its
// details.requires; a well-formed block on a supported fit, no block and
// an extension type pass.
func TestCheck_Refusals(t *testing.T) {
	t.Parallel()

	base := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Family: "binomial", Target: "y", Predictors: []string{"a", "b"}, Margins: &types.RegressionMargins{}}
		mut(s)
		return s
	}
	for _, tc := range []struct {
		name     string
		spec     *types.RegressionSpec
		requires string
	}{
		{"ok", base(func(*types.RegressionSpec) {}), ""},
		{"ok mem link", base(func(s *types.RegressionSpec) { s.Margins.Type, s.Margins.Scale = "mem", "link" }), ""},
		{"ok ols", base(func(s *types.RegressionSpec) { s.Type, s.Family = types.REG_OLS, "" }), ""},
		{"ok multinomial", base(func(s *types.RegressionSpec) { s.Type, s.Family = types.REG_MULTINOMIAL, "" }), ""},
		{"multinomial penalty", base(func(s *types.RegressionSpec) { s.Type, s.Family, s.Penalty = types.REG_MULTINOMIAL, "", "l2" }), "unpenalized"},
		{"no block", base(func(s *types.RegressionSpec) { s.Margins, s.Penalty = nil, "l1" }), ""},
		{"extension", base(func(s *types.RegressionSpec) { s.Type = "REG_ACME_THING" }), ""},
		{"type", base(func(s *types.RegressionSpec) { s.Margins.Type = "x" }), "type ame or mem"},
		{"scale", base(func(s *types.RegressionSpec) { s.Margins.Scale = "x" }), "scale response or link"},
		{"ok binary slope", base(func(s *types.RegressionSpec) { s.Margins.Binary = "slope" }), ""},
		{"ok binary contrast", base(func(s *types.RegressionSpec) { s.Margins.Binary = "contrast" }), ""},
		{"binary", base(func(s *types.RegressionSpec) { s.Margins.Binary = "dummy" }), "binary slope or contrast"},
		{"term", base(func(s *types.RegressionSpec) { s.Margins.Terms = []string{"c"} }), "a predictor"},
		{"repeat", base(func(s *types.RegressionSpec) { s.Margins.Terms = []string{"a", "a"} }), "a predictor"},
		{"bayes", base(func(s *types.RegressionSpec) { s.Type = types.REG_BAYES_LINEAR }), "type REG_OLS, REG_GLM or REG_MULTINOMIAL"},
		{"penalty", base(func(s *types.RegressionSpec) { s.Penalty = "l2" }), "unpenalized"},
		{"selection", base(func(s *types.RegressionSpec) { s.Selection = "forward" }), "no_selection"},
		{"resample", base(func(s *types.RegressionSpec) { s.Resample = "bootstrap" }), "no_resample"},
		{"bounds", base(func(s *types.RegressionSpec) { s.Bounds = &types.RegressionBounds{Nonnegative: []string{"a"}} }), "unbounded"},
	} {
		cerr := Check(tc.spec)
		switch {
		case tc.requires == "" && cerr != nil:
			t.Errorf("%s: refused %v", tc.name, cerr)
		case tc.requires != "" && (cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["requires"] != tc.requires):
			t.Errorf("%s: %v, want PROCESSING_CONFIG requires %q", tc.name, cerr, tc.requires)
		}
	}
}

// TestCheckDependent: a predictor derived from another predictor of the
// same regression — directly (x² from x) or through other derived
// columns — is refused independent_predictors with the path; a derived
// predictor reading only non-predictors (a dummy, a log of another
// column) passes, as do no block, no derived columns and a non-OLS / GLM
// type.
func TestCheckDependent(t *testing.T) {
	t.Parallel()

	reads := map[string][]string{
		"x_pow2":  {"x"},
		"x_log":   {"z"},
		"is_a":    {"channel"},
		"x2_lag":  {"x_pow2", "t"},
		"w_scale": {"w_raw"},
	}
	s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x_log", "is_a", "w_scale"}, Margins: &types.RegressionMargins{}}
	if cerr := CheckDependent(s, reads); cerr != nil {
		t.Errorf("independent derived predictors refused: %v", cerr)
	}
	for _, tc := range []struct {
		predictors []string
		p, q       string
		path       []string
	}{
		{[]string{"x", "x_pow2"}, "x_pow2", "x", []string{"x_pow2", "x"}},
		{[]string{"x2_lag", "x"}, "x2_lag", "x", []string{"x2_lag", "x_pow2", "x"}},
		{[]string{"x2_lag", "x_pow2"}, "x2_lag", "x_pow2", []string{"x2_lag", "x_pow2"}},
	} {
		s.Predictors = tc.predictors
		cerr := CheckDependent(s, reads)
		if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["requires"] != "independent_predictors" ||
			cerr.Details["predictor"] != tc.p || cerr.Details["depends_on"] != tc.q || !slices.Equal(cerr.Details["path"].([]string), tc.path) {
			t.Errorf("%v: %v (details %v)", tc.predictors, cerr, cerr.Details)
		}
	}
	s.Predictors = []string{"x", "x_pow2"}
	if CheckDependent(s, nil) != nil {
		t.Error("no derived columns judged")
	}
	s.Type = types.REG_BAYES_LINEAR
	if CheckDependent(s, reads) != nil {
		t.Error("REG_BAYES_LINEAR judged (Check refuses it first)")
	}
	// U68 E4-S2: REG_MULTINOMIAL reports margins, so it is judged.
	s.Type = types.REG_MULTINOMIAL
	if cerr := CheckDependent(s, reads); cerr == nil || cerr.Details["requires"] != "independent_predictors" {
		t.Errorf("REG_MULTINOMIAL not judged: %v", cerr)
	}
	// A model or extension regression declaring margins (its caller —
	// the field-reference walk — skips an undeclared one) is judged.
	s.Type = types.REG_ORDINAL
	if cerr := CheckDependent(s, reads); cerr == nil || cerr.Details["requires"] != "independent_predictors" {
		t.Errorf("REG_ORDINAL not judged: %v", cerr)
	}
	s.Type, s.Margins = types.REG_OLS, nil
	if CheckDependent(s, reads) != nil {
		t.Error("no block judged")
	}
}

// TestCheckDeclared: a declared extension or model regression's block
// meets Check's shape and fit refusals with the same details — but no
// type rule — and a well-formed block or none passes.
func TestCheckDeclared(t *testing.T) {
	t.Parallel()

	base := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_ORDINAL, Target: "y", Predictors: []string{"a", "b"}, Margins: &types.RegressionMargins{}}
		mut(s)
		return s
	}
	for _, tc := range []struct {
		name     string
		spec     *types.RegressionSpec
		requires string
	}{
		{"ok", base(func(*types.RegressionSpec) {}), ""},
		{"ok mem link", base(func(s *types.RegressionSpec) { s.Margins.Type, s.Margins.Scale = "mem", "link" }), ""},
		{"ok extension", base(func(s *types.RegressionSpec) { s.Type = "REG_ACME_THING" }), ""},
		{"no block", base(func(s *types.RegressionSpec) { s.Margins, s.Resample = nil, "bootstrap" }), ""},
		{"type", base(func(s *types.RegressionSpec) { s.Margins.Type = "x" }), "type ame or mem"},
		{"scale", base(func(s *types.RegressionSpec) { s.Margins.Scale = "x" }), "scale response or link"},
		{"ok binary contrast", base(func(s *types.RegressionSpec) { s.Margins.Binary = "contrast" }), ""},
		{"binary", base(func(s *types.RegressionSpec) { s.Margins.Binary = "Contrast" }), "binary slope or contrast"},
		{"term", base(func(s *types.RegressionSpec) { s.Margins.Terms = []string{"c"} }), "a predictor"},
		{"penalty", base(func(s *types.RegressionSpec) { s.Penalty = "l2" }), "unpenalized"},
		{"selection", base(func(s *types.RegressionSpec) { s.Selection = "forward" }), "no_selection"},
		{"resample", base(func(s *types.RegressionSpec) { s.Resample = "jackknife" }), "no_resample"},
		{"bounds", base(func(s *types.RegressionSpec) { s.Bounds = &types.RegressionBounds{Nonnegative: []string{"a"}} }), "unbounded"},
	} {
		cerr := CheckDeclared(tc.spec)
		switch {
		case tc.requires == "" && cerr != nil:
			t.Errorf("%s: refused %v", tc.name, cerr)
		case tc.requires != "" && (cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["requires"] != tc.requires ||
			cerr.Details["slot"] != "margins"):
			t.Errorf("%s: %v, want PROCESSING_CONFIG slot margins requires %q", tc.name, cerr, tc.requires)
		}
	}
}

// TestResultKeys: per term without outcomes; outcome-major
// "<outcome>:<term>" with them.
func TestResultKeys(t *testing.T) {
	t.Parallel()

	mg := &types.RegressionMarginsResult{Terms: []string{"x", "z"}}
	if got := ResultKeys(mg); !slices.Equal(got, []string{"x", "z"}) {
		t.Errorf("keys %v", got)
	}
	mg.Outcomes = []string{"lo", "hi"}
	if got := ResultKeys(mg); !slices.Equal(got, []string{"lo:x", "lo:z", "hi:x", "hi:z"}) {
		t.Errorf("per-outcome keys %v", got)
	}
	if ResultKeys(nil) != nil || Key("", "x") != "x" || Key("a", "x") != "a:x" {
		t.Error("Key / nil")
	}
}
