package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// glm_links_oracle_test.go pins the non-canonical REG_GLM links (U65
// E2-S1) to R 4.6.1: glm_links.json (scripts/reference/gen_glm_links.R,
// make reference) — binomial probit / cloglog, poisson identity / sqrt,
// gamma log / identity, plus a frequency-weighted case per family whose
// figures are the rep()-expanded glm's. Every figure at the MLE (the
// oracle refits glm to its fixed point; Pulse runs tol 1e-13).

// glmLinksTol is the oracle tolerance: relative, absolute floor 1e-14
// (oracleClose). Fisher scoring converges linearly off the canonical
// link, so the fit runs a tight tol; the measured errors sit near 1e-13.
const glmLinksTol = 1e-9

type glmLinksCase struct {
	Name         string      `json:"name"`
	Family       string      `json:"family"`
	Link         string      `json:"link"`
	Weighted     bool        `json:"weighted"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	StdErrors    []float64   `json:"std_errors"`
	Vcov         [][]float64 `json:"vcov"`
	Deviance     []float64   `json:"deviance"`
	NullDeviance []float64   `json:"null_deviance"`
	AIC          []float64   `json:"aic"`
	Dispersion   []float64   `json:"dispersion"`
	DFResidual   []float64   `json:"df_residual"`
	HC0StdErrors []float64   `json:"hc0_std_errors"`
	HC1StdErrors []float64   `json:"hc1_std_errors"`
	HC3StdErrors []float64   `json:"hc3_std_errors"`
	Fitted       []float64   `json:"fitted"`
	Working      []float64   `json:"working"`
	Hat          []float64   `json:"hat"`
	DFFITS       []float64   `json:"dffits"`
}

// loadGLMLinks reads glm_links.json, checking its golden-hash footer.
func loadGLMLinks(t *testing.T) []glmLinksCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_links.json"))
	if err != nil {
		t.Fatalf("read glm_links.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("glm_links.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("glm_links.json: hash mismatch (hand-edited?); regenerate with `make reference`")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []glmLinksCase    `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode glm_links.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["sandwich"] != "3.1.3" || len(doc.Cases) != 9 {
		t.Fatalf("glm_links.json: R %q, sandwich %q, %d cases", doc.RVersion, doc.Packages["sandwich"], len(doc.Cases))
	}
	return doc.Cases
}

// records is the case's rows as {x1, x2, y[, f]}.
func (c glmLinksCase) records() []Record {
	out := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		v := map[string]float64{"x1": r[0], "x2": r[1], "y": r[2]}
		if c.Weighted {
			v["f"] = r[3]
		}
		out[i] = mockRecord{values: v}
	}
	return out
}

func (c glmLinksCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_GLM, Family: c.Family, Link: c.Link, Target: "y", Predictors: []string{"x1", "x2"},
		Vcov: true, Tol: 1e-13, MaxIters: 500}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
	}
	return s
}

// worst tracks one figure's largest relative error across a case.
type worst map[string]float64

func (w worst) near(t *testing.T, what string, got, want float64) {
	t.Helper()
	e := math.Abs(got-want) / math.Max(math.Abs(want), 1e-14/glmLinksTol)
	key := what
	if i := strings.IndexAny(what, "[ "); i > 0 {
		key = what[:i]
	}
	w[key] = math.Max(w[key], e)
	if !oracleClose(got, want, glmLinksTol) {
		t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
	}
}

// TestReferenceOracle_GLMLinksMatchR: every non-canonical link's
// coefficients, standard errors, vcov (φ·(XᵀWX)⁻¹, the expected
// information), deviance, null deviance, AIC, dispersion and residual
// df, its HC0 / HC1 / HC3 sandwich standard errors (sandwich 3.1-3) and
// every row's fitted mean and working residual (y − μ)·g′(μ) agree with
// R glm at 1e-9 relative; the frequency-weighted cases equal the
// rep()-expanded glm. The per-row leverage and DFFITS over a bound fit:
// TestGLMLinks_BoundFitMatchesR (root package).
func TestReferenceOracle_GLMLinksMatchR(t *testing.T) {
	t.Parallel()

	terms := []string{InterceptKey, "x1", "x2"}
	for _, c := range loadGLMLinks(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			w := worst{}
			recs := c.records()
			eng := newGLM(t, c.spec()).(*glmEngine)
			var mu []float64
			eng.fitted = func(_, m []float64) { mu = append([]float64(nil), m...) }
			res, err := eng.FitBuffered(recs)
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			if res.Family != c.Family || res.Link != c.Link {
				t.Fatalf("echo %s/%s, want %s/%s", res.Family, res.Link, c.Family, c.Link)
			}
			for i, k := range terms {
				w.near(t, "coefficient["+k+"]", res.Coefficients[k], c.Coefficients[i])
				w.near(t, "std_error["+k+"]", res.StdErrors[k], c.StdErrors[i])
				for j := range terms {
					w.near(t, "vcov["+k+"]", res.Vcov.Values[i][j], c.Vcov[i][j])
				}
			}
			w.near(t, "deviance", res.Deviance, c.Deviance[0])
			w.near(t, "null_deviance", res.NullDeviance, c.NullDeviance[0])

			ys := make([]float64, len(c.Rows))
			prior := make([]float64, len(c.Rows))
			for i, r := range c.Rows {
				ys[i], prior[i] = r[2], 1
				if c.Weighted {
					prior[i] = r[3]
				}
			}
			if c.Family == "gamma" {
				w.near(t, "dispersion", res.Dispersion, c.Dispersion[0])
				w.near(t, "df_residual", res.DF, c.DFResidual[0])
				// selection's gamma score is R's AIC(glm) (aicBICFromGamma).
				aic, _, _ := aicBICFromGamma(ys, mu, prior, res.Deviance, 2)
				w.near(t, "aic", aic, c.AIC[0])
			} else {
				if res.Dispersion != 0 || c.Dispersion[0] != 1 {
					t.Errorf("dispersion %v (R %v): φ is fixed at 1", res.Dispersion, c.Dispersion[0])
				}
				// selection's fixed-dispersion score is deviance + 2k, R's
				// AIC less the saturated log-likelihood, a function of y
				// alone (0 for a 0/1 binomial response).
				aic, _, _ := aicBICFromGLM(res.Deviance, len(ys), 2)
				w.near(t, "aic", aic-2*saturatedLogLik(c.Family, ys, prior), c.AIC[0])
			}

			// Per row: the fitted mean and the working residual (each
			// distinct row once under a frequency weight).
			if len(mu) != len(c.Fitted) || len(c.Working) != len(c.Fitted) {
				t.Fatalf("%d fitted rows, R %d / %d", len(mu), len(c.Fitted), len(c.Working))
			}
			rf, err := NewResidualFamily(c.Family, c.Link, 0)
			if err != nil {
				t.Fatal(err)
			}
			for i := range mu {
				w.near(t, "fitted", mu[i], c.Fitted[i])
				w.near(t, "working", rf.Residual("working", ys[i], mu[i]), c.Working[i])
			}

			if !c.Weighted {
				for typ, want := range map[types.CovarianceType][]float64{
					types.CovarianceHC0: c.HC0StdErrors, types.CovarianceHC1: c.HC1StdErrors, types.CovarianceHC3: c.HC3StdErrors,
				} {
					spec := c.spec()
					spec.Vcov = false
					spec.Covariance = &types.RegressionCovariance{Type: typ}
					hc, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
					if err != nil {
						t.Fatalf("%s fit: %v", typ, err)
					}
					for i, k := range terms {
						w.near(t, string(typ)+"_std_error["+k+"]", hc.StdErrors[k], want[i])
					}
				}
			}
			for _, k := range sortedKeys(w) {
				t.Logf("%s %s: max rel error %.3g", c.Name, k, w[k])
			}

			// At the default tol (1e-8 on the relative step) a
			// non-canonical fit stops a linearly converging Fisher
			// scoring early: its coefficients sit within ~3e-8 of the
			// MLE (measured), R glm's own default stops ~1e-9 off it.
			spec := c.spec()
			spec.Tol, spec.MaxIters = 0, 0
			def, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
			if err != nil {
				t.Fatalf("default-tol fit: %v", err)
			}
			for i, k := range terms {
				if !oracleClose(def.Coefficients[k], c.Coefficients[i], 1e-7) {
					t.Errorf("default tol: coefficient[%s] = %.17g, R %.17g", k, def.Coefficients[k], c.Coefficients[i])
				}
			}
		})
	}
}

// saturatedLogLik is Σ w·log f(y; μ = y): the log-likelihood of the
// saturated model, which R's AIC carries and the deviance does not.
func saturatedLogLik(family string, y, w []float64) float64 {
	if family != "poisson" {
		return 0 // binomial with a 0/1 response
	}
	s := 0.0
	for i, v := range y {
		lg, _ := math.Lgamma(v + 1)
		term := -v - lg
		if v > 0 {
			term += v * math.Log(v)
		}
		s += w[i] * term
	}
	return s
}
