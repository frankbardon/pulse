package regmargins

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
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
		{"no block", base(func(s *types.RegressionSpec) { s.Margins, s.Penalty = nil, "l1" }), ""},
		{"extension", base(func(s *types.RegressionSpec) { s.Type = "REG_ACME_THING" }), ""},
		{"type", base(func(s *types.RegressionSpec) { s.Margins.Type = "x" }), "type ame or mem"},
		{"scale", base(func(s *types.RegressionSpec) { s.Margins.Scale = "x" }), "scale response or link"},
		{"term", base(func(s *types.RegressionSpec) { s.Margins.Terms = []string{"c"} }), "a predictor"},
		{"repeat", base(func(s *types.RegressionSpec) { s.Margins.Terms = []string{"a", "a"} }), "a predictor"},
		{"bayes", base(func(s *types.RegressionSpec) { s.Type = types.REG_BAYES_LINEAR }), "type REG_OLS or REG_GLM"},
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

// TestCheckDerived: a predictor the schema lacks (a derived column) is
// refused schema_predictors; cohort fields, a nil schema, no block and
// a non-OLS / GLM type pass.
func TestCheckDerived(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "y", Type: encoding.FieldTypeF64}, {Name: "a", Type: encoding.FieldTypeF64}}}
	s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a"}, Margins: &types.RegressionMargins{}}
	if cerr := CheckDerived(s, schema); cerr != nil {
		t.Errorf("cohort predictor refused: %v", cerr)
	}
	s.Predictors = []string{"a", "a_log"}
	cerr := CheckDerived(s, schema)
	if cerr == nil || cerr.Details["requires"] != "schema_predictors" || cerr.Details["predictor"] != "a_log" {
		t.Errorf("derived predictor: %v", cerr)
	}
	if CheckDerived(s, nil) != nil {
		t.Error("nil schema judged")
	}
	s.Type = types.REG_BAYES_LINEAR
	if CheckDerived(s, schema) != nil {
		t.Error("REG_BAYES_LINEAR judged (Check refuses it first)")
	}
	s.Type, s.Margins = types.REG_OLS, nil
	if CheckDerived(s, schema) != nil {
		t.Error("no block judged")
	}
}
