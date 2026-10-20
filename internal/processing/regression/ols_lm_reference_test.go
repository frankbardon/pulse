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

// olsLMCase is one ols_lm.json case (scripts/reference/gen_ols_lm.R):
// stock lm() on the rep()-expansion of rows [predictors..., y, w].
type olsLMCase struct {
	Name           string      `json:"name"`
	Predictors     []string    `json:"predictors"`
	Weighted       bool        `json:"weighted"`
	ConfLevel      float64     `json:"conf_level"`
	Rows           [][]float64 `json:"rows"`
	NObs           float64     `json:"n_obs"`
	DF             float64     `json:"df"`
	Coefficients   []float64   `json:"coefficients"`
	StdErrors      []float64   `json:"std_errors"`
	TValues        []float64   `json:"t_values"`
	PValues        []float64   `json:"p_values"`
	CILower        []float64   `json:"ci_lower"`
	CIUpper        []float64   `json:"ci_upper"`
	R2             float64     `json:"r2"`
	AdjR2          float64     `json:"adj_r2"`
	ResidualStdErr float64     `json:"residual_std_err"`
}

func loadOLSLMReference(t *testing.T) []olsLMCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "ols_lm.json"))
	if err != nil {
		t.Fatalf("read ols_lm.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		RVersion string      `json:"r_version"`
		Cases    []olsLMCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode ols_lm.json: %v", err)
	}
	if doc.RVersion == "" || len(doc.Cases) == 0 {
		t.Fatal("ols_lm.json has no R version or no cases")
	}
	return doc.Cases
}

func (c olsLMCase) records() []Record {
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

// refRelClose is the R oracles' strict comparison: relative to the
// larger magnitude, no absolute floor (two zeros agree).
func refRelClose(got, want, tol float64) bool {
	if got == want {
		return true
	}
	return math.Abs(got-want) <= tol*math.Max(math.Abs(got), math.Abs(want))
}

// TestReferenceOracle_RegOLSMatchesLM: unpenalised REG_OLS — plain and
// frequency-weighted — reports stock lm()'s coefficients, standard
// errors, t (as β / SE: Pulse emits no t field), p-values, confint()
// bounds at the case's level, R², adjusted R², σ̂ and residual df, every
// figure within relative 1e-10 (lm solves by QR, Pulse by Cholesky on
// centered moments; they meet to rounding). The near-exact fixture is
// the one TestRegOLS_GoldenRequest used to snapshot from Pulse's own
// output; its p-values (~1e-49 … 1e-70) are compared relatively, which
// the snapshot's 1.0-floored tolerance never did.
func TestReferenceOracle_RegOLSMatchesLM(t *testing.T) {
	const tol = 1e-10
	var sawWeighted, sawPlain bool
	for _, c := range loadOLSLMReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type: types.REG_OLS, Name: c.Name, Target: "y", Predictors: c.Predictors,
				ConfLevel: c.ConfLevel,
			}
			if c.Weighted {
				sawWeighted = true
				spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
			} else {
				sawPlain = true
			}
			res, err := newOLS(t, spec).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			check := func(what string, got, want float64) {
				t.Helper()
				if !refRelClose(got, want, tol) {
					t.Errorf("%s = %.17g, R lm %.17g (rel %.3g)", what, got, want,
						math.Abs(got-want)/math.Max(math.Abs(got), math.Abs(want)))
				}
			}
			for j, key := range coefficientKeys(c.Predictors) {
				check("β["+key+"]", res.Coefficients[key], c.Coefficients[j])
				check("SE["+key+"]", res.StdErrors[key], c.StdErrors[j])
				check("t["+key+"]", res.Coefficients[key]/res.StdErrors[key], c.TValues[j])
				check("p["+key+"]", res.PValues[key], c.PValues[j])
				check("ci_lower["+key+"]", res.CILower[key], c.CILower[j])
				check("ci_upper["+key+"]", res.CIUpper[key], c.CIUpper[j])
			}
			check("r2", res.R2, c.R2)
			check("adj_r2", res.AdjR2, c.AdjR2)
			check("residual_std_err", res.ResidualStdErr, c.ResidualStdErr)
			check("df", res.DF, c.DF)
			check("conf_level", res.ConfLevel, c.ConfLevel)
			// lm sees the expansion: its n is Σw under frequency weights,
			// the row count unweighted.
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
	if !sawWeighted || !sawPlain {
		t.Fatalf("golden must carry plain and weighted cases (plain %v, weighted %v)", sawPlain, sawWeighted)
	}
}
