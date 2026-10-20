package regression

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// glmGammaCase is one glm_gamma.json case (scripts/reference/gen_glm_gamma.R).
type glmGammaCase struct {
	Name       string   `json:"name"`
	Target     string   `json:"target"`
	Predictors []string `json:"predictors"`
	Terms      []string `json:"terms"`
	NRows      int      `json:"n_rows"`
	Rows       []struct {
		X1 float64 `json:"x1"`
		X2 float64 `json:"x2"`
		Y  float64 `json:"y"`
	} `json:"rows"`
	Coefficients     []float64   `json:"coefficients"`
	StdErrors        []float64   `json:"std_errors"`
	PValues          []float64   `json:"p_values"`
	Dispersion       float64     `json:"dispersion"`
	DFResidual       float64     `json:"df_residual"`
	DispersionOneSEs []float64   `json:"dispersion_one_std_errors"`
	Vcov             [][]float64 `json:"vcov"`
	Intervals        []struct {
		Level float64   `json:"level"`
		Lower []float64 `json:"lower"`
		Upper []float64 `json:"upper"`
	} `json:"intervals"`
	Deviance     float64 `json:"deviance"`
	NullDeviance float64 `json:"null_deviance"`
}

func loadGLMGammaReference(t *testing.T) []glmGammaCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_gamma.json"))
	if err != nil {
		t.Fatalf("read glm_gamma.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		RVersion string         `json:"r_version"`
		Cases    []glmGammaCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode glm_gamma.json: %v", err)
	}
	if doc.RVersion == "" || len(doc.Cases) == 0 {
		t.Fatalf("glm_gamma.json has no R version or no cases")
	}
	return doc.Cases
}

// TestReferenceOracle_GLMGammaMatchesR: a REG_GLM gamma fit estimates its
// dispersion as R's summary.glm does — φ̂ = Pearson χ² / (n − k) — and
// every inference figure follows it: the standard errors √(φ̂·diag
// (XᵀWX)⁻¹), the p-values 2·pt(−|t|, n − k), DF = n − k, the t-based
// intervals at 0.95 and 0.90, and Vcov = φ̂·(XᵀWX)⁻¹ = vcov(glm), all
// within relative 1e-10 of R (glm_gamma.json, figures at the MLE by a
// warm-started refit). The fixed-dispersion figures it replaced
// (summary(dispersion = 1)) differ on every case.
func TestReferenceOracle_GLMGammaMatchesR(t *testing.T) {
	for _, c := range loadGLMGammaReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			recs := make([]Record, len(c.Rows))
			for i, r := range c.Rows {
				recs[i] = mockRecord{values: map[string]float64{"x1": r.X1, "x2": r.X2, c.Target: r.Y}}
			}
			if len(recs) != c.NRows {
				t.Fatalf("%d rows, golden says %d", len(recs), c.NRows)
			}
			for _, iv := range c.Intervals {
				spec := &types.RegressionSpec{
					Type: types.REG_GLM, Family: "gamma", Link: "inverse",
					Target: c.Target, Predictors: c.Predictors, Vcov: true, ConfLevel: iv.Level,
				}
				eng := newGLM(t, spec).(*glmEngine)
				var mu []float64
				eng.fitted = func(_, m []float64) { mu = append([]float64(nil), m...) }
				res, err := eng.FitBuffered(recs)
				if err != nil {
					t.Fatalf("FitBuffered: %v", err)
				}
				near := func(what string, got, want float64) {
					t.Helper()
					if !oracleClose(got, want, 1e-10) {
						t.Errorf("%s = %.17g, R %.17g", what, got, want)
					}
				}
				for i, term := range c.Terms {
					near("β["+term+"]", res.Coefficients[term], c.Coefficients[i])
					near("SE["+term+"]", res.StdErrors[term], c.StdErrors[i])
					near("p["+term+"]", res.PValues[term], c.PValues[i])
					near("ci_lower["+term+"]", res.CILower[term], iv.Lower[i])
					near("ci_upper["+term+"]", res.CIUpper[term], iv.Upper[i])
					if oracleClose(res.StdErrors[term], c.DispersionOneSEs[i], 1e-6) {
						t.Errorf("SE[%s] = %v still equals the dispersion-1 figure", term, res.StdErrors[term])
					}
				}
				if res.DF != c.DFResidual {
					t.Errorf("df = %v, R df.residual %v", res.DF, c.DFResidual)
				}
				if res.ConfLevel != iv.Level {
					t.Errorf("conf_level = %v, want %v", res.ConfLevel, iv.Level)
				}
				near("deviance", res.Deviance, c.Deviance)
				near("null deviance", res.NullDeviance, c.NullDeviance)
				if res.Vcov == nil {
					t.Fatal("vcov absent")
				}
				for i := range c.Terms {
					for j := range c.Terms {
						near("vcov", res.Vcov.Values[i][j], c.Vcov[i][j])
					}
					if se := res.StdErrors[c.Terms[i]]; math.Sqrt(res.Vcov.Values[i][i]) != se {
						t.Errorf("√vcov[%d][%d] = %v, SE %v: not bit-equal", i, i, math.Sqrt(res.Vcov.Values[i][i]), se)
					}
				}
				prior := make([]float64, len(mu))
				for i := range prior {
					prior[i] = 1
				}
				ys := make([]float64, len(c.Rows))
				for i, r := range c.Rows {
					ys[i] = r.Y
				}
				phi, df := eng.estimatedDispersion(ys, mu, prior, float64(len(ys)))
				near("dispersion", phi, c.Dispersion)
				if df != c.DFResidual {
					t.Errorf("dispersion df = %v, R %v", df, c.DFResidual)
				}
			}
		})
	}
}

// TestRegGLM_GammaZeroResidualDF: a gamma fit with as many rows as
// coefficients has no residual df, so its dispersion is undefined, as in
// R (summary.glm's NaN dispersion): every standard error, p-value and
// bound and DF itself are NaN — null on the wire, key kept — while the
// coefficients (an exact fit) and the deviance stay finite. Binomial /
// poisson fix the dispersion and never read the df.
func TestRegGLM_GammaZeroResidualDF(t *testing.T) {
	recs := []Record{
		mockRecord{values: map[string]float64{"x": 0.5, "y": 1.9}},
		mockRecord{values: map[string]float64{"x": 1.5, "y": 0.8}},
	}
	spec := &types.RegressionSpec{Type: types.REG_GLM, Family: "gamma", Target: "y", Predictors: []string{"x"}, Vcov: true}
	res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	if !math.IsNaN(res.DF) {
		t.Errorf("df = %v, want NaN", res.DF)
	}
	for _, k := range []string{InterceptKey, "x"} {
		if v := res.Coefficients[k]; math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("β[%s] = %v, want finite", k, v)
		}
		for what, m := range map[string]map[string]float64{"se": res.StdErrors, "p": res.PValues, "ci_lower": res.CILower, "ci_upper": res.CIUpper} {
			if v, ok := m[k]; !ok || !math.IsNaN(v) {
				t.Errorf("%s[%s] = %v (present %v), want NaN", what, k, v, ok)
			}
		}
	}
	if math.IsNaN(res.Deviance) {
		t.Errorf("deviance NaN")
	}
}
