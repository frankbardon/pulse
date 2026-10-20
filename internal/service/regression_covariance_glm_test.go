package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// regression_covariance_glm_test.go pins RegressionSpec.Covariance on
// REG_GLM (binomial / logit, poisson / log, gamma / inverse) and
// REG_BAYES_LINEAR against mv_robust_vcov_glm.json (scripts/reference/
// gen_robust_vcov_glm.R): the GLM sandwich over IRLS scores against
// sandwich::vcovHC / vcovCL / NeweyWest on glm and lmtest::coeftest's
// Wald p-values — dispersion-free, so a gamma fit's robust covariance
// never reads the estimated dispersion its classical SEs do (Wald z, no
// df); the Bayes sandwich posterior against its closed form
// (which R checks equals the lm sandwich at prior precision 0).

// glmRobustRow is one mv_robust_vcov_glm.json row.
type glmRobustRow struct {
	Week   float64 `json:"week"`
	Region string  `json:"region"`
	X1     float64 `json:"x1"`
	X2     float64 `json:"x2"`
	YBin   float64 `json:"y_bin"`
	YCount float64 `json:"y_count"`
	Y      float64 `json:"y"`
	YGamma float64 `json:"y_gamma"`
	WFreq  float64 `json:"w_freq"`
	WProb  float64 `json:"w_prob"`
}

// glmRobustCase is one mv_robust_vcov_glm.json case: robustCase plus
// the family and target.
type glmRobustCase struct {
	robustCase
	Family string `json:"family"`
	Target string `json:"target"`
}

type glmRobustGolden struct {
	Rows  []glmRobustRow  `json:"rows"`
	Cases []glmRobustCase `json:"cases"`
}

// glmRobustTol: Pulse's IRLS stops on a 1e-8 relative step (quadratic
// convergence leaves β ~1e-15 off the MLE R polishes to) and inverts
// XᵀWX through gonum; the Bayes posterior is closed form. Both pass at
// 1e-11 on arm64; 1e-10 (the REG_OLS oracle's) leaves room for amd64.
const robustGLMBayesTol = 1e-10

var glmRobustPredictors = []string{"x1", "x2"}

func loadGLMRobust(t *testing.T) glmRobustGolden {
	t.Helper()
	var g glmRobustGolden
	loadMV(t, "mv_robust_vcov_glm.json", &g)
	if len(g.Rows) != 60 || len(g.Cases) != 77 {
		t.Fatalf("golden has %d rows / %d cases, want 60 / 77", len(g.Rows), len(g.Cases))
	}
	return g
}

// glmRobustCohort writes the golden's rows as robust_glm.pulse: week u16
// (the series order), region categorical_u8 (the clusters), the numeric
// columns f64.
func glmRobustCohort(t *testing.T, rows []glmRobustRow) *Service {
	t.Helper()
	dict := encoding.NewDictionary()
	ids := map[string]uint64{}
	for _, r := range rows {
		if _, ok := ids[r.Region]; !ok {
			id, err := dict.Add(r.Region)
			if err != nil {
				t.Fatal(err)
			}
			ids[r.Region] = uint64(id)
		}
	}
	fields := []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
	}
	for _, n := range []string{"x1", "x2", "y_bin", "y_count", "y", "y_gamma", "w_freq", "w_prob"} {
		fields = append(fields, encoding.Field{Name: n, Type: encoding.FieldTypeF64})
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset, fields[i].CsvColumnIdx = off, i
		off += fields[i].Type.ByteSize()
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, &encoding.Schema{Fields: fields}); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		vals := []uint64{uint64(r.Week), ids[r.Region]}
		for _, f := range []float64{r.X1, r.X2, r.YBin, r.YCount, r.Y, r.YGamma, r.WFreq, r.WProb} {
			vals = append(vals, math.Float64bits(f))
		}
		for j, v := range vals {
			if err := encoding.WriteFieldValue(&buf, fields[j].Type, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	memFs := afero.NewMemMapFs()
	if err := afero.WriteFile(memFs, "robust_glm.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

// glmRobustSpec is the REG_GLM / REG_BAYES_LINEAR spec for c (cov nil:
// the classical twin).
func glmRobustSpec(t *testing.T, c glmRobustCase, cov *types.RegressionCovariance) *types.RegressionSpec {
	t.Helper()
	s := &types.RegressionSpec{Name: "fit", Target: c.Target, Predictors: glmRobustPredictors, Vcov: true, Covariance: cov}
	if c.Family == "bayes" {
		s.Type = types.REG_BAYES_LINEAR
	} else {
		s.Type, s.Family = types.REG_GLM, c.Family
	}
	switch c.Weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

func glmRobustCovariance(t *testing.T, c glmRobustCase) *types.RegressionCovariance {
	t.Helper()
	cov := &types.RegressionCovariance{Type: types.CovarianceType(c.Covariance)}
	switch c.Covariance {
	case "cluster":
		cov.Cluster = "region"
	case "hac":
		cov.OrderBy = "week"
		if err := json.Unmarshal(c.Lags, &cov.Lags); err != nil {
			t.Fatalf("lags %s: %v", c.Lags, err)
		}
	}
	return cov
}

func processGLMRobust(t *testing.T, svc *Service, specs ...*types.RegressionSpec) []*types.RegressionResult {
	t.Helper()
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort:      &types.Cohort{Filename: "robust_glm.pulse"},
		Regressions: specs,
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp.Regressions
}

// testRobustGLMBayesOracle (TestRegressionCovariance_MatchesROracle):
// every mv_robust_vcov_glm.json case — coefficients, standard errors,
// Wald p-values (GLM; Bayes reports none), the normal intervals (Bayes:
// credible_intervals too), the full Vcov and the echo; √diag(Vcov) is
// the standard error bit for bit and no df is reported; the
// coefficients equal the classical twin's to the bit.
func testRobustGLMBayesOracle(t *testing.T) {
	g := loadGLMRobust(t)
	svc := glmRobustCohort(t, g.Rows)
	keys := append([]string{"(intercept)"}, glmRobustPredictors...)
	for _, c := range g.Cases {
		t.Run(fmt.Sprintf("%s/%s/%s/%s", c.Family, c.Weight, c.Covariance, c.Lags), func(t *testing.T) {
			tol := robustGLMBayesTol
			res := processGLMRobust(t, svc, glmRobustSpec(t, c, glmRobustCovariance(t, c)), glmRobustSpec(t, c, nil))
			robust, classical := res[0], res[1]
			check := func(what string, got map[string]float64, want []float64) {
				for i, k := range keys {
					if !vcovNear(got[k], want[i], tol) {
						t.Errorf("%s[%s] = %.17g, R = %.17g", what, k, got[k], want[i])
					}
				}
			}
			check("coefficients", robust.Coefficients, c.Coeffs)
			check("std_errors", robust.StdErrors, c.StdErrors)
			check("ci_lower", robust.CILower, c.CILower)
			check("ci_upper", robust.CIUpper, c.CIUpper)
			if c.Family == "bayes" {
				if robust.PValues != nil {
					t.Errorf("bayes reports p-values %v", robust.PValues)
				}
				for _, k := range keys {
					ci := robust.CredibleIntervals[k]
					if ci[0] != robust.CILower[k] || ci[1] != robust.CIUpper[k] {
						t.Errorf("credible_intervals[%s] = %v, ci [%v %v]", k, ci, robust.CILower[k], robust.CIUpper[k])
					}
				}
			} else {
				check("p_values", robust.PValues, c.PValues)
			}
			if robust.DF != 0 {
				t.Errorf("df = %v, want none (normal inference)", robust.DF)
			}
			for _, k := range keys {
				if robust.Coefficients[k] != classical.Coefficients[k] {
					t.Errorf("coefficient[%s] %v moved from the classical %v", k, robust.Coefficients[k], classical.Coefficients[k])
				}
			}
			if robust.Vcov == nil {
				t.Fatal("vcov absent")
			}
			for i := range keys {
				for j := range keys {
					if !vcovNear(robust.Vcov.Values[i][j], c.Vcov[i][j], tol) {
						t.Errorf("vcov[%s][%s] = %.17g, R = %.17g", keys[i], keys[j], robust.Vcov.Values[i][j], c.Vcov[i][j])
					}
					if robust.Vcov.Values[i][j] != robust.Vcov.Values[j][i] {
						t.Errorf("vcov[%s][%s] not exactly symmetric", keys[i], keys[j])
					}
				}
				if math.Sqrt(robust.Vcov.Values[i][i]) != robust.StdErrors[keys[i]] {
					t.Errorf("√vcov[%s] = %.17g, std_error %.17g (must be exact)", keys[i], math.Sqrt(robust.Vcov.Values[i][i]), robust.StdErrors[keys[i]])
				}
			}
			echo := robust.Covariance
			if echo == nil || string(echo.Type) != c.Covariance {
				t.Fatalf("covariance echo %+v, want type %s", echo, c.Covariance)
			}
			switch c.Covariance {
			case "cluster":
				if echo.Cluster != "region" || echo.Clusters != c.Clusters {
					t.Errorf("echo %+v, want cluster region / %d clusters", echo, c.Clusters)
				}
			case "hac":
				if echo.OrderBy != "week" || echo.Lags == nil || c.LagsUsed == nil || *echo.Lags != *c.LagsUsed {
					t.Errorf("echo %+v, want order_by week / lags %v", echo, c.LagsUsed)
				}
			}
		})
	}
}

// TestRegressionCovariance_GLMBayesClassicalByteIdentical: on REG_GLM
// and REG_BAYES_LINEAR, no covariance block and an explicit {type:
// classical} give the same wire bytes (no "covariance" key), with and
// without vcov; a robust type moves the standard errors only.
func TestRegressionCovariance_GLMBayesClassicalByteIdentical(t *testing.T) {
	g := loadGLMRobust(t)
	svc := glmRobustCohort(t, g.Rows)
	for _, fam := range []glmRobustCase{{Family: "binomial", Target: "y_bin"}, {Family: "poisson", Target: "y_count"}, {Family: "gamma", Target: "y_gamma"}, {Family: "bayes", Target: "y"}} {
		for _, vcov := range []bool{false, true} {
			plain := glmRobustSpec(t, fam, nil)
			plain.Vcov = vcov
			classical := *plain
			classical.Covariance = &types.RegressionCovariance{Type: types.CovarianceClassical}
			robust := *plain
			robust.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC0}
			res := processGLMRobust(t, svc, plain, &classical, &robust)
			want, err := json.Marshal(res[0])
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(want, []byte(`"covariance":`)) {
				t.Fatalf("%s classical fit carries a covariance key: %s", fam.Family, want)
			}
			if got, _ := json.Marshal(res[1]); !bytes.Equal(got, want) {
				t.Errorf("%s vcov=%v: explicit classical differs:\n got  %s\n want %s", fam.Family, vcov, got, want)
			}
			if res[2].StdErrors["x1"] == res[0].StdErrors["x1"] || res[2].Coefficients["x1"] != res[0].Coefficients["x1"] {
				t.Errorf("%s hc0 should move the SE only: SE %v vs %v, coef %v vs %v", fam.Family,
					res[2].StdErrors["x1"], res[0].StdErrors["x1"], res[2].Coefficients["x1"], res[0].Coefficients["x1"])
			}
		}
	}
}
