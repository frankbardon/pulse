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

// bayesRefCase is one bayes_linear.json case
// (scripts/reference/gen_bayes_linear.R): the Normal-Inverse-Gamma
// conjugate posterior on the rep()-expansion of rows [predictors..., y, w],
// computed by R matrix algebra and self-checked there against lm.fit on
// the augmented design.
type bayesRefCase struct {
	Name       string      `json:"name"`
	Predictors []string    `json:"predictors"`
	Weighted   bool        `json:"weighted"`
	Rows       [][]float64 `json:"rows"`
	Prior      struct {
		Mu0       []float64 `json:"mu0"`
		Precision float64   `json:"precision"`
		Shape     float64   `json:"shape"`
		Rate      float64   `json:"rate"`
	} `json:"prior"`
	PriorSet struct {
		Mu0       bool `json:"mu0"`
		Precision bool `json:"precision"`
		Shape     bool `json:"shape"`
		Rate      bool `json:"rate"`
	} `json:"prior_set"`
	CredibleLevel  float64     `json:"credible_level"`
	CredibleSet    bool        `json:"credible_set"`
	ConfLevel      float64     `json:"conf_level"`
	ConfSet        bool        `json:"conf_set"`
	NObs           float64     `json:"n_obs"`
	DF             float64     `json:"df"`
	Coefficients   []float64   `json:"coefficients"`
	StdErrors      []float64   `json:"std_errors"`
	CredibleLower  []float64   `json:"credible_lower"`
	CredibleUpper  []float64   `json:"credible_upper"`
	CILower        []float64   `json:"ci_lower"`
	CIUpper        []float64   `json:"ci_upper"`
	ResidualStdErr float64     `json:"residual_std_err"`
	R2             float64     `json:"r2"`
	AdjR2          float64     `json:"adj_r2"`
	Vcov           [][]float64 `json:"vcov"`
}

func loadBayesReference(t *testing.T) []bayesRefCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "bayes_linear.json"))
	if err != nil {
		t.Fatalf("read bayes_linear.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		RVersion string         `json:"r_version"`
		Cases    []bayesRefCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode bayes_linear.json: %v", err)
	}
	if doc.RVersion == "" || len(doc.Cases) == 0 {
		t.Fatal("bayes_linear.json has no R version or no cases")
	}
	return doc.Cases
}

func (c bayesRefCase) records() []Record {
	p := len(c.Predictors)
	recs := make([]Record, len(c.Rows))
	for i, row := range c.Rows {
		m := map[string]float64{"y": row[p], "w": row[p+1]}
		for j, name := range c.Predictors {
			m[name] = row[j]
		}
		recs[i] = mockRecord{values: m}
	}
	return recs
}

// spec builds the case's RegressionSpec, passing only the knobs the case
// sets explicitly so the unset ones exercise Pulse's defaults.
func (c bayesRefCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{
		Type: types.REG_BAYES_LINEAR, Name: c.Name, Target: "y", Predictors: c.Predictors, Vcov: true,
	}
	if c.PriorSet.Mu0 {
		s.PriorMu = c.Prior.Mu0
	}
	if c.PriorSet.Precision {
		s.PriorPrecision = c.Prior.Precision
	}
	if c.PriorSet.Shape {
		s.PriorShape = c.Prior.Shape
	}
	if c.PriorSet.Rate {
		s.PriorRate = c.Prior.Rate
	}
	if c.CredibleSet {
		s.CredibleLevel = c.CredibleLevel
	}
	if c.ConfSet {
		s.ConfLevel = c.ConfLevel
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return s
}

// TestReferenceOracle_RegBayesLinearMatchesR: REG_BAYES_LINEAR — default
// and informative priors, unweighted and frequency-weighted (the
// posterior on the rep() expansion) — reports R's conjugate NIG posterior:
// the posterior mean, the marginal t scale behind StdErrors, the
// credible interval at credible_level and the CI at conf_level (both on
// t_{2a_n}), DF = 2a_n, ResidualStdErr = √(b_n/a_n), R², adjusted R² and
// Vcov = b_n/(a_n−1)·Λ_n⁻¹, every figure within relative 1e-10. The
// streaming path is held bit-identical to buffered by
// TestRegBayes_StreamingMatchesBuffered; this pins the buffered one to R.
// It replaces the diffuse-limit OLS comparison as the posterior's only
// external check.
func TestReferenceOracle_RegBayesLinearMatchesR(t *testing.T) {
	const tol = 1e-10
	var sawWeighted, sawPlain, sawPrior bool
	for _, c := range loadBayesReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			if c.Weighted {
				sawWeighted = true
			} else {
				sawPlain = true
			}
			if c.PriorSet.Mu0 && c.PriorSet.Precision && c.PriorSet.Shape && c.PriorSet.Rate {
				sawPrior = true
			}
			res, err := newBayes(t, c.spec()).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			check := func(what string, got, want float64) {
				t.Helper()
				if !refRelClose(got, want, tol) {
					t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want,
						math.Abs(got-want)/math.Max(math.Abs(got), math.Abs(want)))
				}
			}
			keys := coefficientKeys(c.Predictors)
			for j, key := range keys {
				check("β["+key+"]", res.Coefficients[key], c.Coefficients[j])
				check("SE["+key+"]", res.StdErrors[key], c.StdErrors[j])
				check("credible_lower["+key+"]", res.CredibleIntervals[key][0], c.CredibleLower[j])
				check("credible_upper["+key+"]", res.CredibleIntervals[key][1], c.CredibleUpper[j])
				check("ci_lower["+key+"]", res.CILower[key], c.CILower[j])
				check("ci_upper["+key+"]", res.CIUpper[key], c.CIUpper[j])
			}
			check("df", res.DF, c.DF)
			check("conf_level", res.ConfLevel, c.ConfLevel)
			check("residual_std_err", res.ResidualStdErr, c.ResidualStdErr)
			check("r2", res.R2, c.R2)
			check("adj_r2", res.AdjR2, c.AdjR2)
			if len(res.PValues) != 0 {
				t.Errorf("p_values %v, want none (Bayesian fit)", res.PValues)
			}
			if res.Vcov == nil || len(res.Vcov.Values) != len(keys) {
				t.Fatalf("vcov %+v, want %d×%d", res.Vcov, len(keys), len(keys))
			}
			for i := range keys {
				for j := range keys {
					check("vcov["+keys[i]+"]["+keys[j]+"]", res.Vcov.Values[i][j], c.Vcov[i][j])
				}
			}
			// The posterior sees the expansion: n is Σw under frequency
			// weights, the row count unweighted.
			if c.Weighted {
				check("sum_weights", res.SumWeights, c.NObs)
				if res.NObs != len(c.Rows) {
					t.Errorf("n_obs = %d, want the %d rows", res.NObs, len(c.Rows))
				}
			} else if float64(res.NObs) != c.NObs {
				t.Errorf("n_obs = %d, R %v", res.NObs, c.NObs)
			}
		})
	}
	if !sawWeighted || !sawPlain || !sawPrior {
		t.Fatalf("golden must carry plain, weighted and fully informative-prior cases (plain %v, weighted %v, prior %v)",
			sawPlain, sawWeighted, sawPrior)
	}
}
