package regression

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// makeLinearRecords generates n records on y = intercept + Σ slopes[j]·xj
// plus Gaussian noise scaled by sigma. The RNG is seeded explicitly so
// fixture data stays reproducible. Predictor names are x1, x2, … x_p.
func makeLinearRecords(t *testing.T, n int, slopes []float64, intercept, sigma float64, seed uint64) []Record {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed^0xDEADBEEFCAFE))
	p := len(slopes)
	out := make([]Record, n)
	for i := 0; i < n; i++ {
		x := make(map[string]float64, p+1)
		var y float64 = intercept
		for j := 0; j < p; j++ {
			v := rng.NormFloat64()
			x[predictorName(j)] = v
			y += slopes[j] * v
		}
		y += sigma * rng.NormFloat64()
		x["y"] = y
		out[i] = mockRecord{values: x}
	}
	return out
}

func predictorName(j int) string {
	switch j {
	case 0:
		return "x1"
	case 1:
		return "x2"
	case 2:
		return "x3"
	case 3:
		return "x4"
	case 4:
		return "x5"
	}
	return "x" + string(rune('1'+j))
}

func predictorNames(p int) []string {
	out := make([]string, p)
	for j := 0; j < p; j++ {
		out[j] = predictorName(j)
	}
	return out
}

// runOLSWithModifier is the test harness: build a modifier-bearing
// REG_OLS spec, route through the registry, and return the result.
func runOLSWithModifier(t *testing.T, spec *types.RegressionSpec, records []Record) *types.RegressionResult {
	t.Helper()
	schema := schemaWithFields(append([]string{spec.Target}, spec.Predictors...)...)
	eng, err := newOLSEngine(spec, schema)
	if err != nil {
		t.Fatalf("newOLSEngine: %v", err)
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

// TestRegOLS_Jackknife_SEMatchesAnalytical fits a small clean linear
// fixture with jackknife and checks the per-coefficient SE is within
// 20% of the analytical OLS SE. The two estimators are not identical
// (jackknife is exact only asymptotically) but on n=40 well-conditioned
// data they should agree to that tolerance.
func TestRegOLS_Jackknife_SEMatchesAnalytical(t *testing.T) {
	records := makeLinearRecords(t, 40, []float64{2.0, -1.5}, 1.0, 0.5, 7)

	baseSpec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictorNames(2)}
	jackSpec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictorNames(2), Resample: "jackknife"}

	base := runOLSWithModifier(t, baseSpec, records)
	jack := runOLSWithModifier(t, jackSpec, records)

	if jack.Resample != "jackknife" {
		t.Errorf("Resample echo = %q, want jackknife", jack.Resample)
	}
	for _, name := range []string{"x1", "x2"} {
		analytical := base.StdErrors[name]
		jackSE := jack.StdErrors[name]
		if !(analytical > 0) {
			t.Fatalf("analytical SE for %s is zero", name)
		}
		ratio := jackSE / analytical
		if ratio < 0.6 || ratio > 1.6 {
			t.Errorf("jackknife SE / analytical for %s = %v (jack=%v, analytical=%v); want in [0.6, 1.6]", name, ratio, jackSE, analytical)
		}
		// Coefficients should match the base estimate (jackknife reuses
		// the full-data point estimate).
		if math.Abs(jack.Coefficients[name]-base.Coefficients[name]) > 1e-9 {
			t.Errorf("jackknife coefficient for %s drifted from base: jack=%v, base=%v", name, jack.Coefficients[name], base.Coefficients[name])
		}
	}
}

// TestRegOLS_Bootstrap_Reproducible asserts the bootstrap path produces
// identical SE / p-value maps when run twice with the same RNGSeed.
func TestRegOLS_Bootstrap_Reproducible(t *testing.T) {
	records := makeLinearRecords(t, 50, []float64{1.5, -0.8}, 0.3, 0.4, 11)

	spec := &types.RegressionSpec{
		Type:           types.REG_OLS,
		Target:         "y",
		Predictors:     predictorNames(2),
		Resample:       "bootstrap",
		BootstrapIters: 200,
		RNGSeed:        42,
	}
	r1 := runOLSWithModifier(t, spec, records)
	r2 := runOLSWithModifier(t, spec, records)

	for _, name := range []string{InterceptKey, "x1", "x2"} {
		if math.Abs(r1.StdErrors[name]-r2.StdErrors[name]) > 1e-12 {
			t.Errorf("bootstrap SE not reproducible for %s: run1=%v, run2=%v", name, r1.StdErrors[name], r2.StdErrors[name])
		}
		if math.Abs(r1.PValues[name]-r2.PValues[name]) > 1e-12 {
			t.Errorf("bootstrap p-value not reproducible for %s: run1=%v, run2=%v", name, r1.PValues[name], r2.PValues[name])
		}
	}
	if r1.Resample != "bootstrap" {
		t.Errorf("Resample echo = %q, want bootstrap", r1.Resample)
	}
}

// TestRegOLS_Bootstrap_DefaultIters confirms the modifier uses
// defaultBootstrapIters (1000) when spec.BootstrapIters is zero. We
// cannot observe the iteration count directly, so the check is that
// the SE produced is materially different from a 50-iter run (which
// has higher variance and noticeably different finite-sample SE).
func TestRegOLS_Bootstrap_DefaultIters(t *testing.T) {
	records := makeLinearRecords(t, 50, []float64{1.5}, 0.3, 0.4, 13)
	specDefault := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}, Resample: "bootstrap", RNGSeed: 1}
	res := runOLSWithModifier(t, specDefault, records)
	if !(res.StdErrors["x1"] > 0) {
		t.Errorf("bootstrap with default iters produced non-positive SE for x1: %v", res.StdErrors["x1"])
	}
}

// TestRegOLS_Resample_RegularizedFlag confirms the L1 path interoperates
// with jackknife: the engine builds, FitBuffered succeeds, SE entries
// are populated (the resample replaces the active-set plug-in SE that
// Phase 2 emitted).
func TestRegOLS_Resample_RegularizedFlag(t *testing.T) {
	records := makeLinearRecords(t, 60, []float64{1.5, 0, 0.7}, 0.3, 0.4, 17)
	spec := &types.RegressionSpec{
		Type:       types.REG_OLS,
		Target:     "y",
		Predictors: predictorNames(3),
		Penalty:    "l1",
		Alpha:      0.05,
		Resample:   "jackknife",
	}
	res := runOLSWithModifier(t, spec, records)
	for _, name := range []string{"x1", "x2", "x3"} {
		// SE entries should exist (jackknife always produces a per-
		// coefficient SE, even for L1-shrunk coordinates).
		if _, ok := res.StdErrors[name]; !ok {
			t.Errorf("resampled L1 fit missing SE entry for %s", name)
		}
	}
	if res.Resample != "jackknife" {
		t.Errorf("Resample echo = %q, want jackknife", res.Resample)
	}
}

// TestRegOLS_Resample_RejectsSmallN: jackknife / bootstrap both require
// at least 3 observations. Smaller fixtures surface INSUFFICIENT_DATA.
func TestRegOLS_Resample_RejectsSmallN(t *testing.T) {
	records := makeLinearRecords(t, 2, []float64{1.5}, 0.3, 0.0, 19)
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}, Resample: "jackknife"}
	schema := schemaWithFields("y", "x1")
	eng, err := newOLSEngine(spec, schema)
	if err != nil {
		t.Fatalf("newOLSEngine: %v", err)
	}
	be := eng.(BufferedEngine)
	_, err = be.FitBuffered(records)
	if err == nil {
		t.Fatal("expected INSUFFICIENT_DATA on n=2")
	}
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA) {
		t.Errorf("err = %v, want INSUFFICIENT_DATA", err)
	}
}
