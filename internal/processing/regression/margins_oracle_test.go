package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// margins_oracle_test.go pins the regression marginal effects
// (RegressionSpec.Margins, U65 E3-S1) to R 4.6.1: margins.json
// (scripts/reference/gen_margins.R, make reference) — the analytic
// closed forms (AME = mean of β_j·μ′(η), MEM at the weighted means, the
// delta-method Jacobian, vcov / sandwich HC3 / vcovCL) at the fit's
// fixed point over lm, binomial logit / probit / cloglog, poisson log,
// gamma inverse / log and the negative binomial, plus a
// frequency-weighted pair equal to the rep() expansion; and the external
// cross-check, pinned marginaleffects 1.0.0 avg_slopes /
// slopes(newdata = mean), whose numerical derivatives agree to ~1e-8.
// Pulse runs tol 1e-13.

// marginsOracleTol is the analytic oracle's bound; marginsMETol the
// marginaleffects cross-check's (its numerical slopes and Jacobian).
const (
	marginsOracleTol = 1e-9
	marginsMETol     = 1e-7
)

type marginsCase struct {
	Name         string      `json:"name"`
	Model        string      `json:"model"`
	Family       string      `json:"family"`
	Link         string      `json:"link"`
	Type         string      `json:"type"`
	Scale        string      `json:"scale"`
	Covariance   string      `json:"covariance"`
	Weighted     bool        `json:"weighted"`
	Cluster      bool        `json:"cluster"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	Estimates    []float64   `json:"estimates"`
	StdErrors    []float64   `json:"std_errors"`
	Statistics   []float64   `json:"statistics"`
	PValues      []float64   `json:"p_values"`
	CILower      []float64   `json:"ci_lower"`
	CIUpper      []float64   `json:"ci_upper"`
	DF           []float64   `json:"df"`
	At           []float64   `json:"at"`
	MEEstimates  []float64   `json:"me_estimates"`
	MEStdErrors  []float64   `json:"me_std_errors"`
	// MEBinaryTerms are the 0/1 columns marginaleffects reports as a
	// discrete 1 − 0 contrast, not a slope: no cross-check for them.
	MEBinaryTerms []string `json:"me_binary_terms"`
	// Raw is a derived case's source columns per row ([z, g index]):
	// x1 = log1p(z), x2 = (g == 1) (TestMargins_DerivedPredictorsMatchR).
	Raw [][]float64 `json:"raw"`
}

// loadMargins reads margins.json, checking its golden-hash footer.
func loadMargins(t *testing.T) []marginsCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "margins.json"))
	if err != nil {
		t.Fatalf("read margins.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("margins.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("margins.json: hash mismatch (hand-edited?); regenerate with `make reference`")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []marginsCase     `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode margins.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["MASS"] != "7.3.66" || doc.Packages["sandwich"] != "3.1.3" ||
		doc.Packages["marginaleffects"] != "1.0.0" || doc.Packages["Matrix"] != "1.7.6" || len(doc.Cases) != 18 {
		t.Fatalf("margins.json: R %q, packages %v, %d cases", doc.RVersion, doc.Packages, len(doc.Cases))
	}
	return doc.Cases
}

// records is the case's rows as {x1, x2, y[, g][, f]}.
func (c marginsCase) records() []Record {
	out := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		v := map[string]float64{"x1": r[0], "x2": r[1], "y": r[2]}
		k := 3
		if c.Cluster {
			v["g"] = r[k]
			k++
		}
		if c.Weighted {
			v["f"] = r[k]
		}
		out[i] = mockRecord{values: v}
	}
	return out
}

func (c marginsCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_GLM, Family: c.Family, Link: c.Link, Target: "y", Predictors: []string{"x1", "x2"},
		Tol: 1e-13, MaxIters: 500, Margins: &types.RegressionMargins{Type: c.Type, Scale: c.Scale}}
	if c.Model == "lm" {
		s.Type, s.Family, s.Link, s.Tol, s.MaxIters = types.REG_OLS, "", "", 0, 0
	}
	switch c.Covariance {
	case "hc3":
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3}
	case "cluster":
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
	}
	return s
}

func (c marginsCase) fit(t *testing.T) *types.RegressionResult {
	t.Helper()
	spec := c.spec()
	res, err := FitBuffered([]*types.RegressionSpec{spec}, schemaWithFields("x1", "x2", "y", "g", "f"), c.records())
	if err != nil {
		t.Fatalf("%s: FitBuffered: %v", c.Name, err)
	}
	if res[0].Margins == nil {
		t.Fatalf("%s: no margins block", c.Name)
	}
	return res[0]
}

// TestReferenceOracle_MarginsMatchR: every case's coefficients and, per
// term, the effect, its delta-method standard error, statistic, p-value
// and 95% interval (and df where the statistic is a t, the mem point
// where it is mem) agree with the analytic closed forms at 1e-9
// relative; the frequency-weighted pair is the rep()-expanded fit's.
func TestReferenceOracle_MarginsMatchR(t *testing.T) {
	t.Parallel()

	for _, c := range loadMargins(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			w := worst{}
			res := c.fit(t)
			mg := res.Margins
			for i, k := range []string{InterceptKey, "x1", "x2"} {
				w.near(t, "coefficient["+k+"]", res.Coefficients[k], c.Coefficients[i])
			}
			if mg.Type != c.Type || mg.Scale != c.Scale || mg.ConfLevel != 0.95 || len(mg.Terms) != 2 || mg.Terms[0] != "x1" || mg.Terms[1] != "x2" {
				t.Fatalf("echo %+v", mg)
			}
			for i, k := range mg.Terms {
				w.near(t, "estimate["+k+"]", mg.Estimates[k], c.Estimates[i])
				w.near(t, "std_error["+k+"]", mg.StdErrors[k], c.StdErrors[i])
				w.near(t, "statistic["+k+"]", mg.Statistics[k], c.Statistics[i])
				w.near(t, "p_value["+k+"]", mg.PValues[k], c.PValues[i])
				w.near(t, "ci_lower["+k+"]", mg.CILower[k], c.CILower[i])
				w.near(t, "ci_upper["+k+"]", mg.CIUpper[k], c.CIUpper[i])
				if c.Type == types.MarginsMEM {
					w.near(t, "at["+k+"]", mg.At[k], c.At[i])
				}
			}
			switch {
			case len(c.DF) == 1:
				w.near(t, "df", mg.DF, c.DF[0])
				if mg.DF != res.DF {
					t.Errorf("margins df %v, coefficient table df %v", mg.DF, res.DF)
				}
			case mg.DF != 0:
				t.Errorf("Wald z case carries df %v", mg.DF)
			}
			if c.Type != types.MarginsMEM && mg.At != nil {
				t.Errorf("ame carries at %v", mg.At)
			}
			logWorst(t, c.Name, w)
		})
	}
}

// TestReferenceOracle_MarginsMatchMarginaleffects: the effects and
// their standard errors agree with marginaleffects 1.0.0 (avg_slopes /
// slopes at the means, the same vcov) within its numerical-derivative
// accuracy, 1e-7 relative (measured ≤ 7e-9).
func TestReferenceOracle_MarginsMatchMarginaleffects(t *testing.T) {
	t.Parallel()

	for _, c := range loadMargins(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			mg := c.fit(t).Margins
			worstEst, worstSE := 0.0, 0.0
			for i, k := range mg.Terms {
				if slices.Contains(c.MEBinaryTerms, k) {
					continue
				}
				for _, p := range []struct {
					what      string
					got, want float64
					worst     *float64
				}{{"estimate", mg.Estimates[k], c.MEEstimates[i], &worstEst}, {"std_error", mg.StdErrors[k], c.MEStdErrors[i], &worstSE}} {
					e := math.Abs(p.got-p.want) / math.Abs(p.want)
					*p.worst = math.Max(*p.worst, e)
					if e > marginsMETol {
						t.Errorf("%s[%s] = %.17g, marginaleffects %.17g (rel %.3g)", p.what, k, p.got, p.want, e)
					}
				}
			}
			t.Logf("%s vs marginaleffects: estimate %.3g, std_error %.3g", c.Name, worstEst, worstSE)
		})
	}
}
