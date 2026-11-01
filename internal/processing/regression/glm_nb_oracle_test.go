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

// glm_nb_oracle_test.go pins the REG_GLM negative binomial family (NB2,
// U65 E2-S2) to R 4.6.1 + MASS 7.3-66: glm_nb.json
// (scripts/reference/gen_glm_nb.R, make reference) — θ estimated on the
// log, sqrt and identity links (glm.nb's joint MLE pushed to its fixed
// point), θ fixed (glm(family = negative.binomial(1.7))), a
// frequency-weighted case equal to the rep() expansion and an influence
// case. Pulse runs tol 1e-13 (Fisher scoring off the canonical link and
// the θ alternation converge linearly).

type glmNBCase struct {
	Name                 string      `json:"name"`
	Link                 string      `json:"link"`
	ThetaFixed           bool        `json:"theta_fixed"`
	Weighted             bool        `json:"weighted"`
	Rows                 [][]float64 `json:"rows"`
	Coefficients         []float64   `json:"coefficients"`
	StdErrors            []float64   `json:"std_errors"`
	Vcov                 [][]float64 `json:"vcov"`
	Theta                []float64   `json:"theta"`
	ThetaStdErr          []float64   `json:"theta_std_err"`
	TwoLogLik            []float64   `json:"two_log_lik"`
	AIC                  []float64   `json:"aic"`
	Deviance             []float64   `json:"deviance"`
	NullDeviance         []float64   `json:"null_deviance"`
	DFResidual           []float64   `json:"df_residual"`
	HC0StdErrors         []float64   `json:"hc0_std_errors"`
	HC1StdErrors         []float64   `json:"hc1_std_errors"`
	HC3StdErrors         []float64   `json:"hc3_std_errors"`
	Fitted               []float64   `json:"fitted"`
	Pearson              []float64   `json:"pearson"`
	DevianceResiduals    []float64   `json:"deviance_residuals"`
	Working              []float64   `json:"working"`
	Hat                  []float64   `json:"hat"`
	Standardized         []float64   `json:"standardized"`
	StandardizedDeviance []float64   `json:"standardized_deviance"`
	Studentized          []float64   `json:"studentized"`
	Cooks                []float64   `json:"cooks"`
	DFFITS               []float64   `json:"dffits"`
}

// loadGLMNB reads glm_nb.json, checking its golden-hash footer.
func loadGLMNB(t *testing.T) []glmNBCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_nb.json"))
	if err != nil {
		t.Fatalf("read glm_nb.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("glm_nb.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("glm_nb.json: hash mismatch (hand-edited?); regenerate with `make reference`")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []glmNBCase       `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode glm_nb.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["MASS"] != "7.3.66" || doc.Packages["sandwich"] != "3.1.3" || len(doc.Cases) != 6 {
		t.Fatalf("glm_nb.json: R %q, MASS %q, sandwich %q, %d cases", doc.RVersion, doc.Packages["MASS"], doc.Packages["sandwich"], len(doc.Cases))
	}
	return doc.Cases
}

// records is the case's rows as {x1, x2, y[, f]}.
func (c glmNBCase) records() []Record {
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

func (c glmNBCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_GLM, Family: "negative_binomial", Link: c.Link, Target: "y", Predictors: []string{"x1", "x2"},
		Vcov: true, Tol: 1e-13, MaxIters: 500}
	if c.ThetaFixed {
		s.Theta = c.Theta[0]
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
	}
	return s
}

// TestReferenceOracle_GLMNegBinMatchesR: every case's coefficients,
// standard errors and vcov ((XᵀWX)⁻¹ at θ̂, dispersion 1 —
// summary.negbin), θ, SE(θ̂), 2·log-likelihood, AIC (θ counted when
// estimated), deviance and null deviance at θ̂, its HC0 / HC1 / HC3
// sandwich standard errors (sandwich 3.1-3, θ held at θ̂) and every
// row's fitted mean and pearson / deviance / working residual agree with
// R at 1e-9 relative; the frequency-weighted case equals the
// rep()-expanded glm.nb. Leverage and influence over a bound fit:
// TestGLMNegBin_BoundFitMatchesR (root package).
func TestReferenceOracle_GLMNegBinMatchesR(t *testing.T) {
	t.Parallel()

	terms := []string{InterceptKey, "x1", "x2"}
	for _, c := range loadGLMNB(t) {
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
			if res.Family != "negative_binomial" || res.Link != c.Link {
				t.Fatalf("echo %s/%s, want negative_binomial/%s", res.Family, res.Link, c.Link)
			}
			for i, k := range terms {
				w.near(t, "coefficient["+k+"]", res.Coefficients[k], c.Coefficients[i])
				w.near(t, "std_error["+k+"]", res.StdErrors[k], c.StdErrors[i])
				for j := range terms {
					w.near(t, "vcov["+k+"]", res.Vcov.Values[i][j], c.Vcov[i][j])
				}
			}
			w.near(t, "theta", res.Theta, c.Theta[0])
			if c.ThetaFixed {
				if res.ThetaStdErr != 0 {
					t.Errorf("fixed θ carries theta_std_err %v", res.ThetaStdErr)
				}
			} else {
				w.near(t, "theta_std_err", res.ThetaStdErr, c.ThetaStdErr[0])
			}
			w.near(t, "two_log_lik", res.TwoLogLik, c.TwoLogLik[0])
			w.near(t, "aic", res.AIC, c.AIC[0])
			w.near(t, "deviance", res.Deviance, c.Deviance[0])
			w.near(t, "null_deviance", res.NullDeviance, c.NullDeviance[0])
			if res.Dispersion != 0 || res.DF != 0 {
				t.Errorf("dispersion %v, df %v: φ is fixed at 1 (Wald z)", res.Dispersion, res.DF)
			}

			if len(mu) != len(c.Fitted) {
				t.Fatalf("%d fitted rows, R %d", len(mu), len(c.Fitted))
			}
			for i := range mu {
				w.near(t, "fitted", mu[i], c.Fitted[i])
			}
			if c.Weighted {
				logWorst(t, c.Name, w)
				return
			}
			rf, err := NewResidualFamily("negative_binomial", c.Link, res.Theta)
			if err != nil {
				t.Fatal(err)
			}
			for i, r := range c.Rows {
				w.near(t, "pearson", rf.Residual("pearson", r[2], mu[i]), c.Pearson[i])
				w.near(t, "deviance_residual", rf.Residual("deviance", r[2], mu[i]), c.DevianceResiduals[i])
				w.near(t, "working", rf.Residual("working", r[2], mu[i]), c.Working[i])
			}
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
				if hc.Theta != res.Theta {
					t.Errorf("%s fit θ %v, classical %v: the covariance never moves the estimate", typ, hc.Theta, res.Theta)
				}
			}
			logWorst(t, c.Name, w)
		})
	}
}

func logWorst(t *testing.T, name string, w worst) {
	t.Helper()
	for _, k := range sortedKeys(w) {
		t.Logf("%s %s: max rel error %.3g", name, k, w[k])
	}
}

// TestNegBin_DefaultTolNearMLE: at the default tol a negative binomial
// fit stops near the MLE — every coefficient and θ within 1e-6 relative
// of R's fixed point (glm.nb's own default sits ~1e-6 off it).
func TestNegBin_DefaultTolNearMLE(t *testing.T) {
	t.Parallel()

	for _, c := range loadGLMNB(t) {
		spec := c.spec()
		spec.Tol, spec.MaxIters = 0, 0
		res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(c.records())
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		for i, k := range []string{InterceptKey, "x1", "x2"} {
			if !oracleClose(res.Coefficients[k], c.Coefficients[i], 1e-6) {
				t.Errorf("%s default tol: coefficient[%s] = %.17g, R %.17g", c.Name, k, res.Coefficients[k], c.Coefficients[i])
			}
		}
		if !oracleClose(res.Theta, c.Theta[0], 1e-6) {
			t.Errorf("%s default tol: θ = %.17g, R %.17g", c.Name, res.Theta, c.Theta[0])
		}
	}
}

// TestNegBin_SpecialFunctions: the portable digamma / trigamma against
// their closed forms (ψ(1) = −γ, ψ(½) = −γ − 2 log 2, ψ′(1) = π²/6,
// ψ′(½) = π²/2) and the recurrences across the series threshold.
func TestNegBin_SpecialFunctions(t *testing.T) {
	t.Parallel()

	const gamma = 0.57721566490153286061
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"digamma(1)", digamma(1), -gamma},
		{"digamma(0.5)", digamma(0.5), -gamma - 2*math.Ln2},
		{"digamma(2)", digamma(2), 1 - gamma},
		{"trigamma(1)", trigamma(1), math.Pi * math.Pi / 6},
		{"trigamma(0.5)", trigamma(0.5), math.Pi * math.Pi / 2},
		{"trigamma(2)", trigamma(2), math.Pi*math.Pi/6 - 1},
	} {
		if !oracleClose(c.got, c.want, 1e-15) {
			t.Errorf("%s = %.17g, want %.17g", c.name, c.got, c.want)
		}
	}
	for _, x := range []float64{0.01, 0.7, 3.3, 11.5, 12, 40, 1e3, 1e8} {
		if d := digamma(x+1) - digamma(x) - 1/x; math.Abs(d) > 1e-14*math.Max(1, math.Abs(digamma(x))) {
			t.Errorf("ψ(%g + 1) − ψ(%g) − 1/x = %g", x, x, d)
		}
		if d := trigamma(x) - trigamma(x+1) - 1/(x*x); math.Abs(d) > 1e-14*math.Max(1, trigamma(x)) {
			t.Errorf("ψ′(%g) − ψ′(%g + 1) − 1/x² = %g", x, x, d)
		}
	}
}
