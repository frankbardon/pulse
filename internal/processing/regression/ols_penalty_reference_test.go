package regression

import (
	"encoding/json"
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
// within relative 1e-10 of the KKT-checked oracle (worst seen 4e-15), the oracle's zeros exactly 0
// with no std error or p-value.
func TestRegOLS_PenaltyMatchesGlmnet(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range loadOLSPenaltyReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			seen[c.Penalty] = true
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
			res, err := newOLS(t, spec).FitBuffered(c.records())
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
				// A dropped predictor has neither an SE nor a p-value —
				// never a p of 0.
				_, hasSE := res.StdErrors[name]
				_, hasP := res.PValues[name]
				if hasSE || hasP {
					t.Errorf("zeroed %s carries std error %v / p-value %v", name, hasSE, hasP)
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
