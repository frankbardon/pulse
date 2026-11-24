package regression

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/types"
)

// glm_nb_penalty_oracle_test.go pins the penalized REG_GLM negative
// binomial (U69 E3-S1, #638) to R 4.6.1 + mpath 0.4-2.28:
// glm_nb_penalty.json (scripts/reference/gen_glm_nb_penalty.R) —
// mpath::glmregNB with θ re-estimated (theta.ml) around the penalized
// fit, or fixed, under glmnet's standardize = FALSE objective; l2 / l1 /
// elasticnet, unpenalized predictors, frequency weights.

type glmNBPenaltyCase struct {
	Name         string      `json:"name"`
	Penalty      string      `json:"penalty"`
	Alpha        float64     `json:"alpha"`
	L1Ratio      float64     `json:"l1_ratio"`
	ThetaFixed   float64     `json:"theta_fixed"`
	Predictors   []string    `json:"predictors"`
	Weighted     bool        `json:"weighted"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	StdErrors    []*float64  `json:"std_errors"`
	Zero         []string    `json:"zero"`
	Theta        float64     `json:"theta"`
	ThetaStdErr  *float64    `json:"theta_std_err"`
	Deviance     float64     `json:"deviance"`
	Hat          []float64   `json:"hat"`
	Unpenalized  []string    `json:"unpenalized"`
}

func loadGLMNBPenaltyReference(t *testing.T) []glmNBPenaltyCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_nb_penalty.json"))
	if err != nil {
		t.Fatalf("read glm_nb_penalty.json: %v (regenerate with scripts/reference/gen_glm_nb_penalty.R)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		Packages map[string]string  `json:"packages"`
		Cases    []glmNBPenaltyCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode glm_nb_penalty.json: %v", err)
	}
	if doc.Packages["mpath"] == "" || len(doc.Cases) == 0 {
		t.Fatalf("glm_nb_penalty.json has no mpath version or no cases")
	}
	return doc.Cases
}

func (c glmNBPenaltyCase) records() []Record {
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

func (c glmNBPenaltyCase) spec() *types.RegressionSpec {
	spec := &types.RegressionSpec{
		Type: types.REG_GLM, Target: "y", Predictors: c.Predictors, Family: reglink.NegativeBinomial,
		Penalty: c.Penalty, Alpha: c.Alpha, Tol: 1e-13, MaxIters: 500,
		Unpenalized: c.Unpenalized, Theta: c.ThetaFixed,
	}
	if c.Penalty == "elasticnet" {
		spec.L1Ratio = c.L1Ratio
	}
	if c.Weighted {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return spec
}

// glmNBPenaltyTol: mpath stops its θ alternation on |Δℓ|/d1 + |Δθ| <
// 1e-8 and Pulse at relative 1e-13, so the two fixed points agree to
// mpath's stopping error (worst seen 1.1e-9 relative, on θ).
const glmNBPenaltyTol = 1e-8

// TestRegGLM_NegBinPenaltyMatchesMpath: every penalized negative
// binomial case — l2, l1, elasticnet, unpenalized predictors, frequency
// weights, θ estimated and fixed — reaches mpath::glmregNB's
// coefficients (a predictor mpath zeroes is exactly 0) and θ, the l2
// penalized-Fisher sandwich standard errors and SE(θ̂) at θ̂ (l1 /
// elasticnet: every SE and p-value NaN, SE(θ̂) too; a fixed θ carries
// no SE(θ̂)) and the NB deviance at the estimate.
func TestRegGLM_NegBinPenaltyMatchesMpath(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, c := range loadGLMNBPenaltyReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			seen[c.Penalty] = true
			if c.ThetaFixed > 0 {
				seen["fixed"] = true
			}
			if len(c.Unpenalized) > 0 {
				seen["unpenalized"] = true
			}
			if c.Weighted {
				seen["weighted"] = true
			}
			if len(c.Zero) > 0 {
				seen["zeroed"] = true
			}
			res, err := asBuffered(t, newGLM(t, c.spec())).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			if res.Penalty != c.Penalty || res.Family != reglink.NegativeBinomial {
				t.Errorf("echo penalty=%q family=%q", res.Penalty, res.Family)
			}
			if !oracleClose(res.Theta, c.Theta, glmNBPenaltyTol) {
				t.Errorf("θ = %.17g, mpath %.17g", res.Theta, c.Theta)
			}
			for j, key := range coefficientKeys(c.Predictors) {
				if got := res.Coefficients[key]; !oracleClose(got, c.Coefficients[j], glmNBPenaltyTol) {
					t.Errorf("β[%s] = %.17g, mpath %.17g", key, got, c.Coefficients[j])
				}
				if c.Penalty != "l2" {
					continue
				}
				se := res.StdErrors[key]
				if c.StdErrors[j] == nil || !oracleClose(se, *c.StdErrors[j], glmNBPenaltyTol) {
					t.Errorf("SE[%s] = %.17g, sandwich %v", key, se, c.StdErrors[j])
				}
			}
			for _, name := range c.Zero {
				if res.Coefficients[name] != 0 {
					t.Errorf("β[%s] = %v, mpath zeroed it", name, res.Coefficients[name])
				}
			}
			if c.Penalty != "l2" {
				assertInferenceNull(t, res, coefficientKeys(c.Predictors))
			}
			switch {
			case c.ThetaStdErr != nil:
				if !oracleClose(res.ThetaStdErr, *c.ThetaStdErr, glmNBPenaltyTol) {
					t.Errorf("SE(θ̂) = %.17g, oracle %.17g", res.ThetaStdErr, *c.ThetaStdErr)
				}
			case c.ThetaFixed > 0:
				if res.ThetaStdErr != 0 {
					t.Errorf("fixed θ carries SE(θ̂) %v", res.ThetaStdErr)
				}
			default:
				if !math.IsNaN(res.ThetaStdErr) {
					t.Errorf("l1 / elasticnet SE(θ̂) = %v, want NaN (no inference)", res.ThetaStdErr)
				}
			}
			if !oracleClose(res.Deviance, c.Deviance, glmNBPenaltyTol) {
				t.Errorf("deviance = %.17g, oracle %.17g", res.Deviance, c.Deviance)
			}
		})
	}
	for _, want := range []string{"l1", "l2", "elasticnet", "fixed", "unpenalized", "weighted", "zeroed"} {
		if !seen[want] {
			t.Errorf("golden lacks a %s case", want)
		}
	}
}

// TestRegGLM_NegBinPenaltyLeverage: a penalized negative binomial's GLM
// refit leverage (ATTR_REG_LEVERAGE params.family negative_binomial +
// penalty, lifted with the fit) is the hat of the penalized IRLS's final
// weighted least squares at θ̂ — the oracle's W·x̃ᵀH_S⁻¹x̃ — and a bound
// fit of the same spec reads the very same design (bit-equal).
func TestRegGLM_NegBinPenaltyLeverage(t *testing.T) {
	t.Parallel()

	n := 0
	for _, c := range loadGLMNBPenaltyReference(t) {
		if c.Hat == nil {
			continue
		}
		n++
		t.Run(c.Name, func(t *testing.T) {
			recs, spec := c.records(), c.spec()
			res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
			if err != nil {
				t.Fatal(err)
			}
			d := boundLeverage(t, spec, res)
			fit, err := FitForAttribute(spec, recs, true)
			if err != nil {
				t.Fatalf("GLM refit: %v", err)
			}
			p := len(c.Predictors)
			for i, row := range c.Rows {
				hb, ok := d.Leverage(rowOf(c.Predictors, row[:p]))
				if !ok {
					t.Fatalf("row %d: no bound leverage", i)
				}
				if !oracleClose(hb, c.Hat[i], glmNBPenaltyTol) {
					t.Errorf("row %d: h = %.17g, oracle %.17g", i, hb, c.Hat[i])
				}
				if hr := LeverageForRow(fit, row[:p], 1); hr != hb {
					t.Errorf("row %d: refit h = %.17g ≠ bound h = %.17g", i, hr, hb)
				}
			}
		})
	}
	if n == 0 {
		t.Fatal("golden carries no hat case")
	}
}
