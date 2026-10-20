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

// olsPenaltyCase is one ols_penalty.json case
// (scripts/reference/gen_ols_penalty.R).
type olsPenaltyCase struct {
	Name         string      `json:"name"`
	Penalty      string      `json:"penalty"`
	Alpha        float64     `json:"alpha"`
	L1Ratio      float64     `json:"l1_ratio"`
	Predictors   []string    `json:"predictors"`
	Weighted     bool        `json:"weighted"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	Zero         []string    `json:"zero"`
	// StdErrors, PValues and DF ride the l2 cases only: the closed-form
	// ridge covariance and its Student-t p-values, R matrix algebra
	// (gen_ols_penalty.R ridge_inference).
	StdErrors []float64 `json:"std_errors"`
	PValues   []float64 `json:"p_values"`
	DF        float64   `json:"df"`
}

func loadOLSPenaltyReference(t *testing.T) []olsPenaltyCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "ols_penalty.json"))
	if err != nil {
		t.Fatalf("read ols_penalty.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []olsPenaltyCase  `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode ols_penalty.json: %v", err)
	}
	if doc.Packages["glmnet"] == "" || len(doc.Cases) == 0 {
		t.Fatal("ols_penalty.json has no glmnet version or no cases")
	}
	return doc.Cases
}

func (c olsPenaltyCase) records() []Record {
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

// TestRegOLS_PenaltyMatchesGlmnet: every REG_OLS penalty — lasso,
// elastic net, ridge, weighted — minimizes glmnet's standardize = FALSE
// objective (the α meaning REG_GLM's penalties share): coefficients
// within relative 1e-10 of the KKT-checked oracle (worst seen 4e-15), the
// oracle's zeros exactly 0.
func TestRegOLS_PenaltyMatchesGlmnet(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range loadOLSPenaltyReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			seen[c.Penalty] = true
			res, err := newOLS(t, c.spec()).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			for j, key := range coefficientKeys(c.Predictors) {
				if got := res.Coefficients[key]; !oracleClose(got, c.Coefficients[j], 1e-10) {
					t.Errorf("β[%s] = %.17g, oracle %.17g", key, got, c.Coefficients[j])
				}
			}
			for _, name := range c.Zero {
				if res.Coefficients[name] != 0 {
					t.Errorf("β[%s] = %v, oracle zeroed it", name, res.Coefficients[name])
				}
			}
		})
	}
	for _, want := range []string{"l1", "l2", "elasticnet"} {
		if !seen[want] {
			t.Errorf("golden lacks a %s case", want)
		}
	}
}

func (c olsPenaltyCase) spec() *types.RegressionSpec {
	spec := &types.RegressionSpec{
		Type: types.REG_OLS, Target: "y", Predictors: c.Predictors,
		Penalty: c.Penalty, Alpha: c.Alpha, Tol: 1e-15, MaxIters: 1000000,
	}
	if c.Penalty == "elasticnet" {
		spec.L1Ratio = c.L1Ratio
	}
	if c.Weighted {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return spec
}

// TestReferenceOracle_RidgeInferenceMatchesClosedForm: a ridge fit's
// standard errors are the closed-form ridge covariance
// σ̂²·A⁻¹·G·A⁻¹ (A = G + N*·α·I, G the weighted centred Gram,
// σ̂² = RSS/(N* − p − 1)) with Var(β₀) = σ̂²/N* + μᵀVμ, and its p-values
// the Student-t on N* − p − 1 df — R matrix algebra
// (gen_ols_penalty.R ridge_inference), unweighted and
// frequency-weighted, every figure within relative 1e-10.
func TestReferenceOracle_RidgeInferenceMatchesClosedForm(t *testing.T) {
	n := 0
	for _, c := range loadOLSPenaltyReference(t) {
		if c.Penalty != "l2" {
			continue
		}
		n++
		t.Run(c.Name, func(t *testing.T) {
			if len(c.StdErrors) != len(c.Predictors)+1 || len(c.PValues) != len(c.StdErrors) {
				t.Fatalf("golden ridge case lacks std_errors / p_values")
			}
			res, err := newOLS(t, c.spec()).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			if !oracleClose(res.DF, c.DF, 1e-10) {
				t.Errorf("df = %.17g, oracle %.17g", res.DF, c.DF)
			}
			for j, key := range coefficientKeys(c.Predictors) {
				if got := res.StdErrors[key]; !oracleClose(got, c.StdErrors[j], 1e-10) {
					t.Errorf("SE[%s] = %.17g, closed form %.17g", key, got, c.StdErrors[j])
				}
				if got := res.PValues[key]; !oracleClose(got, c.PValues[j], 1e-10) {
					t.Errorf("p[%s] = %.17g, closed form %.17g", key, got, c.PValues[j])
				}
			}
			if len(res.Advisories) != 0 {
				t.Errorf("ridge carries advisories %+v", res.Advisories)
			}
		})
	}
	if n < 2 {
		t.Fatalf("golden holds %d ridge cases, want an unweighted and a weighted one", n)
	}
}

// TestRegOLS_LassoElasticNetInferenceNull: an l1 / elasticnet fit
// reports no inference — std error, p-value and both interval bounds
// present and NaN (null on the wire) on EVERY coefficient, the
// intercept and the zeroed predictors included — while the
// coefficients stay glmnet's (TestRegOLS_PenaltyMatchesGlmnet).
func TestRegOLS_LassoElasticNetInferenceNull(t *testing.T) {
	n := 0
	for _, c := range loadOLSPenaltyReference(t) {
		if c.Penalty == "l2" {
			continue
		}
		n++
		t.Run(c.Name, func(t *testing.T) {
			res, err := newOLS(t, c.spec()).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			assertInferenceNull(t, res, coefficientKeys(c.Predictors))
			// On the wire: null, key kept.
			raw, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			for _, slot := range []string{"std_errors", "p_values", "ci_lower", "ci_upper"} {
				var m map[string]any
				if err := json.Unmarshal(wire[slot], &m); err != nil {
					t.Fatalf("wire %s: %v", slot, err)
				}
				for _, key := range coefficientKeys(c.Predictors) {
					if v, ok := m[key]; !ok || v != nil {
						t.Errorf("wire %s[%s] = %v (present %v), want null", slot, key, v, ok)
					}
				}
			}
		})
	}
	if n == 0 {
		t.Fatal("golden holds no l1 / elasticnet case")
	}
}

// assertInferenceNull fails unless every key carries a NaN std error,
// p-value and interval bound.
func assertInferenceNull(t *testing.T, res *types.RegressionResult, keys []string) {
	t.Helper()
	for _, key := range keys {
		for name, m := range map[string]map[string]float64{
			"std_errors": res.StdErrors, "p_values": res.PValues, "ci_lower": res.CILower, "ci_upper": res.CIUpper,
		} {
			v, ok := m[key]
			if !ok || !math.IsNaN(v) {
				t.Errorf("%s[%s] = %v (present %v), want NaN", name, key, v, ok)
			}
		}
	}
}

// TestRegOLS_RidgeEqualsElasticNetAtZeroL1: one α meaning across the
// OLS penalties — the coordinate-descent elastic net at l1_ratio = 0 is
// the closed-form ridge at the same α.
func TestRegOLS_RidgeEqualsElasticNetAtZeroL1(t *testing.T) {
	predictors, records := lassoFixture()
	for _, alpha := range []float64{0.01, 0.5, 3} {
		acc := newOLSAccumulator(len(predictors))
		acc2 := newOLSAccumulator(len(predictors))
		for _, r := range records {
			x := make([]float64, len(predictors))
			for j, name := range predictors {
				x[j], _ = r.NumericValue(name)
			}
			y, _ := r.NumericValue("y")
			acc.UpdateRow(x, y)
			acc2.UpdateRow(x, y)
		}
		ridge, err := solveRidge(acc, alpha)
		if err != nil {
			t.Fatal(err)
		}
		en, _, err := coordinateDescent(acc2, alpha, 0, 1000000, 1e-15)
		if err != nil {
			t.Fatal(err)
		}
		for j := range predictors {
			if !oracleClose(en.Coefficients[j], ridge.Coefficients[j], 1e-10) {
				t.Errorf("α=%v β[%s]: elasticnet(l1_ratio=0) %.17g, ridge %.17g", alpha, predictors[j], en.Coefficients[j], ridge.Coefficients[j])
			}
		}
		if !oracleClose(en.Intercept, ridge.Intercept, 1e-10) {
			t.Errorf("α=%v intercept: elasticnet %.17g, ridge %.17g", alpha, en.Intercept, ridge.Intercept)
		}
	}
}
