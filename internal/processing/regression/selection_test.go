package regression

import (
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// makeSelectionFixture builds a 5-predictor synthetic where only x1 and
// x3 have signal. Sample size is large enough that AIC reliably picks
// the two signal predictors and rejects the three noise predictors.
func makeSelectionFixture(t *testing.T, n int, seed uint64) []Record {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed^0xFACEFEEDFACEFEED))
	out := make([]Record, n)
	for i := 0; i < n; i++ {
		m := map[string]float64{}
		x1 := rng.NormFloat64()
		x2 := rng.NormFloat64()
		x3 := rng.NormFloat64()
		x4 := rng.NormFloat64()
		x5 := rng.NormFloat64()
		// True model: y = 1 + 2·x1 + 0·x2 − 1.5·x3 + 0·x4 + 0·x5 + N(0, 0.3)
		y := 1.0 + 2.0*x1 - 1.5*x3 + 0.3*rng.NormFloat64()
		m["x1"] = x1
		m["x2"] = x2
		m["x3"] = x3
		m["x4"] = x4
		m["x5"] = x5
		m["y"] = y
		out[i] = mockRecord{values: m}
	}
	return out
}

func setEq(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	set := make(map[string]bool, len(want))
	for _, w := range want {
		set[w] = true
	}
	for _, g := range got {
		if !set[g] {
			return false
		}
	}
	return true
}

// runOLSSelection runs the modifier-driven selection on a fixture and
// returns the result.
func runOLSSelection(t *testing.T, selection, criterion string, records []Record) *types.RegressionResult {
	t.Helper()
	spec := &types.RegressionSpec{
		Type:       types.REG_OLS,
		Target:     "y",
		Predictors: []string{"x1", "x2", "x3", "x4", "x5"},
		Selection:  selection,
		Criterion:  criterion,
	}
	schema := schemaWithFields("y", "x1", "x2", "x3", "x4", "x5")
	eng, err := newOLSEngine(spec, schema)
	if err != nil {
		t.Fatalf("newOLSEngine: %v", err)
	}
	be := eng.(BufferedEngine)
	res, err := be.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	return res
}

// TestRegOLS_ForwardSelection_RecoversTrueFeatures: forward selection
// on a 5-predictor synthetic with x1 + x3 as the signal predictors
// should select exactly {x1, x3}. BIC is used because AIC's lighter
// per-parameter penalty occasionally retains noise predictors whose
// chance correlation with y dips RSS by enough to pass the AIC
// threshold (a well-known limitation of greedy forward AIC at moderate
// n). BIC's log(n) penalty rejects them reliably here.
func TestRegOLS_ForwardSelection_RecoversTrueFeatures(t *testing.T) {
	records := makeSelectionFixture(t, 500, 23)
	res := runOLSSelection(t, "forward", "bic", records)
	if res.Selection != "forward" {
		t.Errorf("Selection echo = %q, want forward", res.Selection)
	}
	if res.Criterion != "bic" {
		t.Errorf("Criterion echo = %q, want bic", res.Criterion)
	}
	if !setEq(res.SelectedFeatures, []string{"x1", "x3"}) {
		t.Errorf("forward selection picked %v, want {x1, x3}", res.SelectedFeatures)
	}
}

// TestRegOLS_BackwardSelection_RecoversTrueFeatures: backward
// elimination starts at the full 5-predictor model and should converge
// to {x1, x3} under BIC.
func TestRegOLS_BackwardSelection_RecoversTrueFeatures(t *testing.T) {
	records := makeSelectionFixture(t, 500, 29)
	res := runOLSSelection(t, "backward", "bic", records)
	if !setEq(res.SelectedFeatures, []string{"x1", "x3"}) {
		t.Errorf("backward selection picked %v, want {x1, x3}", res.SelectedFeatures)
	}
}

// TestRegOLS_Stepwise_RecoversTrueFeatures: bidirectional stepwise on
// the same fixture should converge to {x1, x3} under BIC.
func TestRegOLS_Stepwise_RecoversTrueFeatures(t *testing.T) {
	records := makeSelectionFixture(t, 500, 31)
	res := runOLSSelection(t, "stepwise", "bic", records)
	if !setEq(res.SelectedFeatures, []string{"x1", "x3"}) {
		t.Errorf("stepwise picked %v, want {x1, x3}", res.SelectedFeatures)
	}
}

// TestRegOLS_Selection_AICIncludesSignalPredictors: AIC's lighter
// per-parameter penalty can retain noise predictors when their chance
// correlation with y is non-negligible. The test asserts containment
// (x1 and x3 are always selected) rather than equality, so the AIC
// path is still covered by selection's contract.
func TestRegOLS_Selection_AICIncludesSignalPredictors(t *testing.T) {
	records := makeSelectionFixture(t, 200, 37)
	res := runOLSSelection(t, "forward", "aic", records)
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
		t.Errorf("forward AIC missing signal predictors: selected=%v", res.SelectedFeatures)
	}
}

// TestRegOLS_Selection_PreservesCoefficients: after selection, the
// coefficient map contains entries only for the selected predictors
// plus the intercept.
func TestRegOLS_Selection_PreservesCoefficients(t *testing.T) {
	records := makeSelectionFixture(t, 200, 41)
	res := runOLSSelection(t, "forward", "aic", records)

	for _, name := range res.SelectedFeatures {
		if _, ok := res.Coefficients[name]; !ok {
			t.Errorf("selected feature %s missing from Coefficients map", name)
		}
	}
	for _, name := range []string{"x1", "x2", "x3", "x4", "x5"} {
		inSelected := false
		for _, sel := range res.SelectedFeatures {
			if sel == name {
				inSelected = true
				break
			}
		}
		if !inSelected {
			if _, ok := res.Coefficients[name]; ok {
				t.Errorf("non-selected feature %s leaked into Coefficients map", name)
			}
		}
	}
	// Intercept always populated.
	if _, ok := res.Coefficients[InterceptKey]; !ok {
		t.Errorf("intercept missing from Coefficients map")
	}
}

// TestRegOLS_Selection_ResampleCompose: selection + bootstrap should
// run end-to-end; SE / p-values come from the resample, SelectedFeatures
// from the selection step.
func TestRegOLS_Selection_ResampleCompose(t *testing.T) {
	records := makeSelectionFixture(t, 500, 43)
	spec := &types.RegressionSpec{
		Type:           types.REG_OLS,
		Target:         "y",
		Predictors:     []string{"x1", "x2", "x3", "x4", "x5"},
		Selection:      "forward",
		Criterion:      "bic",
		Resample:       "bootstrap",
		BootstrapIters: 150,
		RNGSeed:        2024,
	}
	schema := schemaWithFields("y", "x1", "x2", "x3", "x4", "x5")
	eng, err := newOLSEngine(spec, schema)
	if err != nil {
		t.Fatalf("newOLSEngine: %v", err)
	}
	be := eng.(BufferedEngine)
	res, err := be.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	if res.Selection != "forward" {
		t.Errorf("Selection echo = %q, want forward", res.Selection)
	}
	if res.Resample != "bootstrap" {
		t.Errorf("Resample echo = %q, want bootstrap", res.Resample)
	}
	if !setEq(res.SelectedFeatures, []string{"x1", "x3"}) {
		t.Errorf("compose picked %v, want {x1, x3}", res.SelectedFeatures)
	}
	for _, name := range res.SelectedFeatures {
		if !(res.StdErrors[name] > 0) {
			t.Errorf("compose SE for %s = %v, want positive", name, res.StdErrors[name])
		}
	}
}
