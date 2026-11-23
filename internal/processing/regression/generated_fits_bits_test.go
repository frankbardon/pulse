package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRegression_GeneratedFitsBitsPinned: the regression engine is
// FMA-free end to end (docs/src/library/linalg.md, "What is covered
// across architectures": OLS, GLM, ridge, lasso / elastic net, the
// resample and selection modifiers on them), and this package's test
// generators draw with detmath.NormFloat64 / detmath.Exp behind float64()
// barriers, so a fit of a generated fixture is the same bits on arm64
// and amd64. Each case pins the SHA-256 of the fit's wire form
// (types.MarshalFinite: every float in its shortest round-trip decimal,
// so one moved bit moves the hash; a NaN is null, so no sign bit is
// read). The fixtures are the ones the package's tolerance and
// structure tests already run on.
//
// A moved hash means an engine product lost its float64() barrier, a
// stdlib transcendental crept onto a result path, or a generator lost
// its detmath draw. Re-pin only for a deliberate figure change, after
// confirming the new hash on linux/amd64 (CI) as well as arm64.
func TestRegression_GeneratedFitsBitsPinned(t *testing.T) {
	t.Parallel()

	ols := func(preds []string, extra func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: preds}
		if extra != nil {
			extra(s)
		}
		return s
	}
	glm := func(preds []string, extra func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Family: "binomial", Link: "logit", Target: "y", Predictors: preds}
		if extra != nil {
			extra(s)
		}
		return s
	}
	genRecords := func() []Record { r, _, _, _ := genDataset(1, 60, 0.7, 1.4, -0.9); return r }

	cases := []struct {
		name    string
		spec    *types.RegressionSpec
		records func() []Record
		want    string
	}{
		{"ols_qr", ols([]string{"x1", "x2"}, func(s *types.RegressionSpec) { s.Vcov = true }), genRecords,
			"3d35e793641503eafb6f60706806043f8b9913654fc6540dba7f8116ee8b3ced"},
		{"ols_hc3", ols([]string{"x1", "x2"}, func(s *types.RegressionSpec) { s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3} }), genRecords,
			"28ee1a7069c22f678518e26832bab5d3bda25a030c8ac048a0b2b42e83c3bff4"},
		{"ols_ridge", ols([]string{"x1", "x2"}, func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.5 }), genRecords,
			"454531fa6848b89b8f46f146c893ef33d82c419ac4cf21e53d3e402e3ee3eeaa"},
		{"ols_jackknife", ols(predictorNames(2), func(s *types.RegressionSpec) { s.Resample = "jackknife" }),
			func() []Record { return makeLinearRecords(t, 40, []float64{2.0, -1.5}, 1.0, 0.5, 7) },
			"ad69aefebf39028115caf5a82ebab02b8c7a6d7277342b736524d9ca4e267fa9"},
		{"ols_bootstrap", ols(predictorNames(2), func(s *types.RegressionSpec) {
			s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 200, 42
		}),
			func() []Record { return makeLinearRecords(t, 50, []float64{1.5, -0.8}, 0.3, 0.4, 11) },
			"1a0d47327047d8009e8900c34393f6b65259af0d841ce7f14fe0592e6e0d1048"},
		{"ols_lasso_jackknife", ols(predictorNames(3), func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.Resample = "l1", 0.05, "jackknife"
		}),
			func() []Record { return makeLinearRecords(t, 60, []float64{1.5, 0, 0.7}, 0.3, 0.4, 17) },
			"25844c06c2312629952b4e3c1c1acb9e172274b8ba781da9987dcc10971a2364"},
		{"ols_forward_bic", ols([]string{"x1", "x2", "x3", "x4", "x5"}, func(s *types.RegressionSpec) {
			s.Selection, s.Criterion = "forward", "bic"
		}),
			func() []Record { return makeSelectionFixture(t, 500, 23) },
			"e5ff812bddb87dc0baf02704c7645745978dc9ce9c05c43aec6f57a30a228f3b"},
		{"glm_logit_jackknife", glm(predictorNames(2), func(s *types.RegressionSpec) { s.Resample = "jackknife" }),
			func() []Record { return makeLogisticFixture(t, 60, []float64{1.2, -0.8}, 0.1, 53) },
			"1e772727c517a526d7e29bee30b80b1cce091ba5fbdf6ed4a43fef580968a51b"},
		{"glm_logit_stepwise_aic", glm([]string{"x1", "x2", "x3", "x4"}, func(s *types.RegressionSpec) {
			s.Selection, s.Criterion = "stepwise", "aic"
		}),
			func() []Record { return makeStepwiseLogisticRecords(250, 67) },
			"1beb3664dfaf3764c8cb5e9d48cb526a849cc755fe99878808b3d0ecc537cf55"},
		{"glm_logit_jackknife_groups", glm([]string{"x1", "x2"}, func(s *types.RegressionSpec) {
			s.Resample, s.JackknifeGroups, s.RNGSeed, s.Vcov = "jackknife", 7, 5, true
		}),
			func() []Record { return glmJackknifeRecords(400, 11) },
			"1dd74969aa0d9f8dcaf19c0a216e15bbaca0a0eb4709e2da2a37b810874e7659"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recs := c.records()
			fields := append([]string{c.spec.Target}, c.spec.Predictors...)
			var (
				eng Engine
				err error
			)
			if c.spec.Type == types.REG_GLM {
				eng, err = newGLMEngine(c.spec, schemaWithFields(fields...))
			} else {
				eng, err = newOLSEngine(c.spec, schemaWithFields(fields...))
			}
			if err != nil {
				t.Fatalf("engine: %v", err)
			}
			res, err := eng.(BufferedEngine).FitBuffered(recs)
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			body, err := types.MarshalFinite(res)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			if got := hex.EncodeToString(sum[:]); got != c.want {
				t.Errorf("fit bits %s, want %s\n%s", got, c.want, body)
			}
		})
	}
}
