package regression

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// makeLogisticFixture generates n records on a logistic model
// P(y=1) = sigmoid(intercept + Σ slopes[j]·xj). Predictors are
// independent standard normals; the response is a Bernoulli draw.
func makeLogisticFixture(t *testing.T, n int, slopes []float64, intercept float64, seed uint64) []Record {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed^0xBEEFDEADBEEFDEAD))
	out := make([]Record, n)
	for i := 0; i < n; i++ {
		m := map[string]float64{}
		eta := intercept
		for j, sl := range slopes {
			v := rng.NormFloat64()
			m[predictorName(j)] = v
			eta += sl * v
		}
		// Sigmoid.
		mu := 1.0 / (1.0 + math.Exp(-eta))
		y := 0.0
		if rng.Float64() < mu {
			y = 1.0
		}
		m["y"] = y
		out[i] = mockRecord{values: m}
	}
	return out
}

// runGLMWithModifier routes through newGLMEngine which delegates to
// modifierEngine when Resample or Selection is non-empty.
func runGLMWithModifier(t *testing.T, spec *types.RegressionSpec, records []Record) *types.RegressionResult {
	t.Helper()
	schema := schemaWithFields(append([]string{spec.Target}, spec.Predictors...)...)
	eng, err := newGLMEngine(spec, schema)
	if err != nil {
		t.Fatalf("newGLMEngine: %v", err)
	}
	be, ok := eng.(BufferedEngine)
	if !ok {
		t.Fatalf("expected BufferedEngine, got %T", eng)
	}
	res, err := be.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	return res
}

// TestRegGLM_Jackknife_Logistic: logistic regression + jackknife
// produces finite, positive SE entries for every coefficient.
func TestRegGLM_Jackknife_Logistic(t *testing.T) {
	records := makeLogisticFixture(t, 60, []float64{1.2, -0.8}, 0.1, 53)
	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     "y",
		Predictors: predictorNames(2),
		Family:     "binomial",
		Link:       "logit",
		Resample:   "jackknife",
	}
	res := runGLMWithModifier(t, spec, records)
	if res.Resample != "jackknife" {
		t.Errorf("Resample echo = %q, want jackknife", res.Resample)
	}
	for _, name := range []string{InterceptKey, "x1", "x2"} {
		se := res.StdErrors[name]
		if math.IsNaN(se) || math.IsInf(se, 0) || !(se > 0) {
			t.Errorf("jackknife SE for %s = %v; want finite positive", name, se)
		}
	}
}

// TestRegGLM_Stepwise_Logistic: stepwise selection on a 4-predictor
// logistic fixture where only x1 and x3 carry signal should pick a
// subset that includes both. The smaller fixture and binary response
// make exact recovery noisier than the OLS case, so we assert
// containment rather than equality.
func TestRegGLM_Stepwise_Logistic(t *testing.T) {
	rng := rand.New(rand.NewPCG(67, 67^0xABCDEF1234567890))
	n := 250
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		m := map[string]float64{}
		x1 := rng.NormFloat64()
		x2 := rng.NormFloat64()
		x3 := rng.NormFloat64()
		x4 := rng.NormFloat64()
		// True model: η = 2·x1 − 1.5·x3.
		eta := 2.0*x1 - 1.5*x3
		mu := 1.0 / (1.0 + math.Exp(-eta))
		y := 0.0
		if rng.Float64() < mu {
			y = 1.0
		}
		m["x1"] = x1
		m["x2"] = x2
		m["x3"] = x3
		m["x4"] = x4
		m["y"] = y
		records[i] = mockRecord{values: m}
	}
	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     "y",
		Predictors: []string{"x1", "x2", "x3", "x4"},
		Family:     "binomial",
		Link:       "logit",
		Selection:  "stepwise",
		Criterion:  "aic",
	}
	schema := schemaWithFields("y", "x1", "x2", "x3", "x4")
	eng, err := newGLMEngine(spec, schema)
	if err != nil {
		t.Fatalf("newGLMEngine: %v", err)
	}
	be := eng.(BufferedEngine)
	res, err := be.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	if res.Selection != "stepwise" {
		t.Errorf("Selection echo = %q, want stepwise", res.Selection)
	}
	hasX1, hasX3 := false, false
	for _, s := range res.SelectedFeatures {
		if s == "x1" {
			hasX1 = true
		}
		if s == "x3" {
			hasX3 = true
		}
	}
	if !hasX1 || !hasX3 {
		t.Errorf("stepwise logistic missing signal predictors: selected=%v", res.SelectedFeatures)
	}
}
