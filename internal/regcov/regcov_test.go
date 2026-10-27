package regcov

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheck walks every rule of the shared covariance check: shape
// faults are PROCESSING_CONFIG, robust types the fit cannot honour are
// PROCESSING_REGRESSION_VCOV_UNSUPPORTED naming the blocking setting,
// and a classical block passes beside anything.
func TestCheck(t *testing.T) {
	t.Parallel()

	ols := func(c *types.RegressionCovariance) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Covariance: c}
	}
	hc1 := func() *types.RegressionCovariance { return &types.RegressionCovariance{Type: types.CovarianceHC1} }
	with := func(s *types.RegressionSpec, f func(*types.RegressionSpec)) *types.RegressionSpec { f(s); return s }
	cases := []struct {
		name   string
		spec   *types.RegressionSpec
		code   errors.Code // "" passes
		reason string
	}{
		{"nil spec", nil, "", ""},
		{"absent", ols(nil), "", ""},
		{"classical", ols(&types.RegressionCovariance{Type: types.CovarianceClassical}), "", ""},
		{"classical + ridge", with(ols(&types.RegressionCovariance{Type: types.CovarianceClassical}), func(s *types.RegressionSpec) { s.Penalty = "l2" }), "", ""},
		{"hc0", ols(&types.RegressionCovariance{Type: types.CovarianceHC0}), "", ""},
		{"cluster", ols(&types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}), "", ""},
		{"hac auto", ols(&types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t"}), "", ""},
		{"hac 0", ols(&types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t", Lags: types.LagsOf(0)}), "", ""},
		{"empty type", ols(&types.RegressionCovariance{}), errors.PROCESSING_CONFIG, ""},
		{"unknown type", ols(&types.RegressionCovariance{Type: "hc5"}), errors.PROCESSING_CONFIG, ""},
		{"cluster without field", ols(&types.RegressionCovariance{Type: types.CovarianceCluster}), errors.PROCESSING_CONFIG, ""},
		{"cluster field on hc1", ols(&types.RegressionCovariance{Type: types.CovarianceHC1, Cluster: "g"}), errors.PROCESSING_CONFIG, ""},
		{"hac without order_by", ols(&types.RegressionCovariance{Type: types.CovarianceHAC}), errors.PROCESSING_CONFIG, ""},
		{"order_by on cluster", ols(&types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g", OrderBy: "t"}), errors.PROCESSING_CONFIG, ""},
		{"lags on hc3", ols(&types.RegressionCovariance{Type: types.CovarianceHC3, Lags: types.LagsAuto()}), errors.PROCESSING_CONFIG, ""},
		{"negative lags", ols(&types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t", Lags: types.LagsOf(-2)}), errors.PROCESSING_CONFIG, ""},
		{"glm", with(ols(hc1()), func(s *types.RegressionSpec) { s.Type = types.REG_GLM }), "", ""},
		{"bayes", with(ols(hc1()), func(s *types.RegressionSpec) { s.Type = types.REG_BAYES_LINEAR }), "", ""},
		{"glm cluster", with(ols(&types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}), func(s *types.RegressionSpec) { s.Type = types.REG_GLM }), "", ""},
		{"bayes hac", with(ols(&types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "t"}), func(s *types.RegressionSpec) { s.Type = types.REG_BAYES_LINEAR }), "", ""},
		{"glm resample", with(ols(hc1()), func(s *types.RegressionSpec) { s.Type, s.Resample = types.REG_GLM, "bootstrap" }), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ReasonResample},
		{"bayes selection", with(ols(hc1()), func(s *types.RegressionSpec) { s.Type, s.Selection = types.REG_BAYES_LINEAR, "forward" }), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ReasonSelection},
		{"type without the estimators", with(ols(hc1()), func(s *types.RegressionSpec) { s.Type = "REG_OTHER" }), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ReasonType},
		{"ridge", with(ols(hc1()), func(s *types.RegressionSpec) { s.Penalty = "l2" }), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ReasonPenalty},
		{"resample", with(ols(hc1()), func(s *types.RegressionSpec) { s.Resample = "bootstrap" }), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ReasonResample},
		{"selection", with(ols(hc1()), func(s *types.RegressionSpec) { s.Selection = "forward" }), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ReasonSelection},
		{"bounds", with(ols(hc1()), func(s *types.RegressionSpec) {
			s.Bounds = &types.RegressionBounds{Nonnegative: []string{"x"}}
		}), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ReasonBounds},
		{"bounds empty list", with(ols(hc1()), func(s *types.RegressionSpec) { s.Bounds = &types.RegressionBounds{} }), "", ""},
		{"bounds classical", with(ols(&types.RegressionCovariance{Type: types.CovarianceClassical}), func(s *types.RegressionSpec) {
			s.Bounds = &types.RegressionBounds{Nonnegative: []string{"x"}}
		}), "", ""},
	}
	for _, c := range cases {
		got := Check(c.spec)
		if c.code == "" {
			if got != nil {
				t.Errorf("%s: refused %v", c.name, got)
			}
			continue
		}
		if got == nil || got.Code != c.code {
			t.Errorf("%s: got %v, want %s", c.name, got, c.code)
			continue
		}
		if c.reason != "" && got.Details["reason"] != c.reason {
			t.Errorf("%s: reason %v, want %s", c.name, got.Details["reason"], c.reason)
		}
	}
}

// TestDataRefusals: the engine's two data-dependent refusals carry the
// shared code, their reason and what tripped them.
func TestDataRefusals(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Covariance: &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g", OrderBy: "t"}}
	if e := TooFewClusters(spec, 1); e.Code != errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED || e.Details["reason"] != ReasonClusters || e.Details["clusters"] != 1 {
		t.Errorf("TooFewClusters = %+v", e)
	}
	if e := OrderTies(spec, 7); e.Details["reason"] != ReasonOrderTies || e.Details["value"] != 7.0 || e.Details["order_by"] != "t" {
		t.Errorf("OrderTies = %+v", e)
	}
}
