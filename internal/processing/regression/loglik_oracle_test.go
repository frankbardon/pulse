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

// loglik_oracle_test.go pins the log-likelihood and AIC every plain
// REG_OLS / REG_GLM fit reports (loglik.go) to R 4.6.1's logLik() /
// AIC(): loglik.json (scripts/reference/gen_loglik.R, make reference) —
// lm with and without an intercept, binomial-logit, poisson-log and
// gamma-inverse glm, each unweighted and frequency-weighted against the
// rep() expansion. The non-canonical links and the negative binomial
// are pinned by glm_links.json and glm_nb.json
// (TestReferenceOracle_GLMLinksMatchR, TestReferenceOracle_GLMNegBinMatchesR).

type logLikCase struct {
	Name         string      `json:"name"`
	Family       string      `json:"family"`
	Intercept    bool        `json:"intercept"`
	Weighted     bool        `json:"weighted"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	TwoLogLik    []float64   `json:"two_log_lik"`
	AIC          []float64   `json:"aic"`
	DF           []float64   `json:"df"`
}

func loadLogLik(t *testing.T) []logLikCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "loglik.json"))
	if err != nil {
		t.Fatalf("read loglik.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("loglik.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("loglik.json: hash mismatch (hand-edited?); regenerate with `make reference`")
	}
	var doc struct {
		RVersion string       `json:"r_version"`
		Cases    []logLikCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode loglik.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || len(doc.Cases) != 9 {
		t.Fatalf("loglik.json: R %q, %d cases", doc.RVersion, len(doc.Cases))
	}
	return doc.Cases
}

func (c logLikCase) records() []Record {
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

func (c logLikCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}}
	if c.Family != "gaussian" {
		s.Type, s.Family, s.Tol, s.MaxIters = types.REG_GLM, c.Family, 1e-13, 200
	}
	if !c.Intercept {
		s.Intercept = new(bool)
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
	}
	return s
}

func (c logLikCase) fit(t *testing.T) *types.RegressionResult {
	t.Helper()
	var eng BufferedEngine
	if c.Family == "gaussian" {
		eng = newOLS(t, c.spec())
	} else {
		eng = asBuffered(t, newGLM(t, c.spec()))
	}
	res, err := eng.FitBuffered(c.records())
	if err != nil {
		t.Fatalf("%s: FitBuffered: %v", c.Name, err)
	}
	return res
}

// TestReferenceOracle_LogLikMatchesR: every case's two_log_lik
// (2·logLik) and aic (AIC, logLik's df counted: σ on lm, φ on gamma)
// agree with R at 1e-10 relative, the coefficients at 1e-9, and the
// frequency-weighted cases equal the rep()-expanded fit.
func TestReferenceOracle_LogLikMatchesR(t *testing.T) {
	t.Parallel()

	for _, c := range loadLogLik(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			w := worst{}
			res := c.fit(t)
			for i, k := range []string{InterceptKey, "x1", "x2"} {
				if !c.Intercept && k == InterceptKey {
					continue
				}
				w.near(t, "coefficient["+k+"]", res.Coefficients[k], c.Coefficients[i])
			}
			if !oracleClose(res.TwoLogLik, c.TwoLogLik[0], 1e-10) || !oracleClose(res.AIC, c.AIC[0], 1e-10) {
				t.Errorf("two_log_lik %.17g aic %.17g, R %.17g %.17g", res.TwoLogLik, res.AIC, c.TwoLogLik[0], c.AIC[0])
			}
			w.near(t, "two_log_lik", res.TwoLogLik, c.TwoLogLik[0])
			w.near(t, "aic", res.AIC, c.AIC[0])
			// AIC − (−2ℓ) is R's df, exactly.
			if df := (res.AIC + res.TwoLogLik) / 2; math.Abs(df-c.DF[0]) > 1e-9 {
				t.Errorf("parameter count %v, R logLik df %v", df, c.DF[0])
			}
			logWorst(t, c.Name, w)
		})
	}
}

// TestLogLik_OLSStreamedEqualsBuffered: the OLS likelihood reads the
// accumulator only, so the streamed fit (UpdateRow / Finalize) reports
// the buffered fit's two_log_lik and aic bit for bit.
func TestLogLik_OLSStreamedEqualsBuffered(t *testing.T) {
	t.Parallel()

	for _, c := range loadLogLik(t) {
		if c.Family != "gaussian" {
			continue
		}
		buf := c.fit(t)
		eng := newOLS(t, c.spec()).(StreamingEngine)
		for _, r := range c.records() {
			if err := eng.UpdateRow(r); err != nil {
				t.Fatal(err)
			}
		}
		str, err := eng.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(str.TwoLogLik) != math.Float64bits(buf.TwoLogLik) || math.Float64bits(str.AIC) != math.Float64bits(buf.AIC) {
			t.Errorf("%s: streamed %v / %v, buffered %v / %v", c.Name, str.TwoLogLik, str.AIC, buf.TwoLogLik, buf.AIC)
		}
	}
}

// TestLogLik_AbsentWhereUndefined: no two_log_lik / aic on a penalized
// OLS or GLM fit (no maximised likelihood), an OLS fit whose bound
// binds, or a probability-weighted fit (a pseudo-likelihood); a poisson
// fit of a non-count response reports R's −Inf / +Inf (null on the
// wire).
func TestLogLik_AbsentWhereUndefined(t *testing.T) {
	t.Parallel()

	cases := loadLogLik(t)
	byName := map[string]logLikCase{}
	for _, c := range cases {
		byName[c.Name] = c
	}
	ols, pois := byName["ols"], byName["poisson"]
	for name, edit := range map[string]struct {
		c    logLikCase
		edit func(*types.RegressionSpec)
	}{
		"ols l2":      {ols, func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }},
		"glm l2":      {pois, func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }},
		"ols binding": {ols, func(s *types.RegressionSpec) { s.Bounds = &types.RegressionBounds{Nonnegative: []string{"x2"}} }},
		"ols weighted": {ols, func(s *types.RegressionSpec) {
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "x1", Kind: types.WeightKindProbability})
		}},
		"glm weighted": {pois, func(s *types.RegressionSpec) {
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "x1", Kind: types.WeightKindProbability})
		}},
	} {
		spec := edit.c.spec()
		edit.edit(spec)
		var eng BufferedEngine
		if spec.Type == types.REG_OLS {
			eng = newOLS(t, spec)
		} else {
			eng = asBuffered(t, newGLM(t, spec))
		}
		res, err := eng.FitBuffered(edit.c.records())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "ols binding" && len(res.ActiveBounds) == 0 {
			t.Fatalf("%s: the bound does not bind; the case tests nothing", name)
		}
		if res.TwoLogLik != 0 || res.AIC != 0 {
			t.Errorf("%s: two_log_lik %v, aic %v; want absent", name, res.TwoLogLik, res.AIC)
		}
	}
	// A bounds fit no bound binds is the plain fit, likelihood included.
	spec := ols.spec()
	spec.Bounds = &types.RegressionBounds{Nonnegative: []string{"x1"}}
	res, err := newOLS(t, spec).FitBuffered(ols.records())
	if err != nil || len(res.ActiveBounds) != 0 || res.AIC != ols.fit(t).AIC {
		t.Errorf("non-binding bounds: err %v, active %v, aic %v, plain %v", err, res.ActiveBounds, res.AIC, ols.fit(t).AIC)
	}
	// A non-count poisson response: dpois is 0, ℓ = −Inf.
	recs := pois.records()
	recs[0] = mockRecord{values: map[string]float64{"x1": pois.Rows[0][0], "x2": pois.Rows[0][1], "y": pois.Rows[0][2] + 0.5}}
	res, err = asBuffered(t, newGLM(t, pois.spec())).FitBuffered(recs)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(res.TwoLogLik, -1) || !math.IsInf(res.AIC, 1) {
		t.Errorf("non-count poisson: two_log_lik %v, aic %v; want −Inf / +Inf", res.TwoLogLik, res.AIC)
	}
}
