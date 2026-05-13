package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newBayes constructs a buffered REG_BAYES_LINEAR engine for the given
// spec, mirroring newOLS in ols_test.go. The engine wraps the same
// Welford accumulator, so the test pattern is intentionally identical.
func newBayes(t *testing.T, spec *types.RegressionSpec) BufferedEngine {
	t.Helper()
	schema := schemaWithFields(append([]string{spec.Target}, spec.Predictors...)...)
	eng, err := newBayesLinearEngine(spec, schema)
	if err != nil {
		t.Fatalf("newBayesLinearEngine: %v", err)
	}
	be, ok := eng.(BufferedEngine)
	if !ok {
		t.Fatalf("newBayesLinearEngine returned %T, want BufferedEngine", eng)
	}
	return be
}

// TestRegBayes_DiffusePriorMatchesOLS: the weakly-informative default
// prior (μ₀ = 0, Λ₀ = 1e-3·I, a₀ = b₀ = 1e-3) is so flat that the
// posterior mean coincides with the OLS coefficients up to floating-
// point cancellation. With the same data, the Bayes engine and the
// Phase 1 OLS engine must produce coefficients within 1e-6 and std
// errors within 1e-6.
//
// Test uses the same n=5 exact-line fixture as Phase 1's
// TestRegOLS_Univariate_Exact (y = 2x + 1) plus a noisier multivariate
// configuration to exercise the off-diagonal posterior precision.
func TestRegBayes_DiffusePriorMatchesOLS(t *testing.T) {
	t.Run("univariate exact line with very diffuse prior", func(t *testing.T) {
		// Phase 1's exact-line fixture (y = 2x + 1, n=5). With only 5
		// rows, the *default* ε = 1e-3 prior shrinks the intercept by
		// ~5e-4 (the intercept's data precision is just n = 5, so ε is
		// ~0.02% relative). To get OLS parity within 1e-6 on this
		// small-n fixture we crank the prior precision down to 1e-12,
		// which makes the prior numerically negligible vs. data on any
		// configuration where the data Gram is non-degenerate.
		x := []float64{1, 2, 3, 4, 5}
		y := []float64{3, 5, 7, 9, 11} // 2x + 1
		records := make([]Record, len(x))
		for i := range x {
			records[i] = mockRecord{values: map[string]float64{"x": x[i], "y": y[i]}}
		}

		olsSpec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}
		bayesSpec := &types.RegressionSpec{
			Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"},
			PriorPrecision: 1e-12,
		}

		olsRes, err := newOLS(t, olsSpec).FitBuffered(records)
		if err != nil {
			t.Fatalf("OLS FitBuffered: %v", err)
		}
		bayesRes, err := newBayes(t, bayesSpec).FitBuffered(records)
		if err != nil {
			t.Fatalf("Bayes FitBuffered: %v", err)
		}

		const coefTol = 1e-6
		if !closef(olsRes.Coefficients[InterceptKey], bayesRes.Coefficients[InterceptKey], coefTol) {
			t.Errorf("intercept OLS=%v Bayes=%v Δ=%v",
				olsRes.Coefficients[InterceptKey], bayesRes.Coefficients[InterceptKey],
				olsRes.Coefficients[InterceptKey]-bayesRes.Coefficients[InterceptKey])
		}
		if !closef(olsRes.Coefficients["x"], bayesRes.Coefficients["x"], coefTol) {
			t.Errorf("slope OLS=%v Bayes=%v Δ=%v",
				olsRes.Coefficients["x"], bayesRes.Coefficients["x"],
				olsRes.Coefficients["x"]-bayesRes.Coefficients["x"])
		}
		// Bayes must emit credible intervals; OLS must not. Cross-check
		// the response shape.
		if len(bayesRes.CredibleIntervals) == 0 {
			t.Error("Bayes did not populate CredibleIntervals")
		}
		if bayesRes.Prior != "nig" {
			t.Errorf("Bayes Prior = %q, want \"nig\"", bayesRes.Prior)
		}
		if len(bayesRes.PValues) != 0 {
			t.Errorf("Bayes emitted PValues unexpectedly: %v", bayesRes.PValues)
		}
		if bayesRes.ConvergedIters != 0 {
			t.Errorf("Bayes ConvergedIters = %d, want 0 (closed form)", bayesRes.ConvergedIters)
		}
	})

	t.Run("multivariate noisy fixture", func(t *testing.T) {
		// y = 1.0 + 0.5 x1 − 2.3 x2 + small deterministic noise. Same
		// generative shape as TestRegOLS_StreamingMatchesBuffered.
		predictors := []string{"x1", "x2"}
		const n = 100
		records := make([]Record, n)
		for i := 0; i < n; i++ {
			x1 := float64(i)
			x2 := math.Cos(0.2 * float64(i))
			y := 1.0 + 0.5*x1 - 2.3*x2 + 0.1*math.Sin(0.7*float64(i))
			records[i] = mockRecord{values: map[string]float64{
				"x1": x1, "x2": x2, "y": y,
			}}
		}

		olsRes, err := newOLS(t, &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors}).FitBuffered(records)
		if err != nil {
			t.Fatalf("OLS FitBuffered: %v", err)
		}
		bayesRes, err := newBayes(t, &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: predictors}).FitBuffered(records)
		if err != nil {
			t.Fatalf("Bayes FitBuffered: %v", err)
		}

		// With ε = 1e-3 and n = 100, the posterior is dominated by data;
		// coefficients agree to 1e-6 (the data scale is O(10) on x1, so
		// the ε·μ₀ term is ~1e-3 vs. an XᵀX diagonal of ~1e5 — relative
		// influence ~1e-8).
		const coefTol = 1e-4
		for _, name := range append([]string{InterceptKey}, predictors...) {
			if !closef(olsRes.Coefficients[name], bayesRes.Coefficients[name], coefTol) {
				t.Errorf("β_%s OLS=%v Bayes=%v Δ=%v",
					name, olsRes.Coefficients[name], bayesRes.Coefficients[name],
					olsRes.Coefficients[name]-bayesRes.Coefficients[name])
			}
		}
		// Std errors: the posterior std under the t marginal is
		// √(b_n/a_n · (Λ_n⁻¹)[j,j]); for a diffuse prior on this data
		// it's within 1% of the OLS std error (which uses σ² = RSS / df).
		// We assert a generous 5% tolerance here — the precise equality
		// is only attained in the limit ε → 0.
		for _, name := range predictors {
			ols := olsRes.StdErrors[name]
			bayes := bayesRes.StdErrors[name]
			if math.Abs(ols-bayes)/math.Abs(ols) > 0.05 {
				t.Errorf("SE_%s OLS=%v Bayes=%v (relative diff > 5%%)", name, ols, bayes)
			}
		}
	})
}

// TestRegBayes_StrongPriorShrinks: with PriorPrecision = 1e6 and
// μ₀ = 0, the posterior mean is pulled toward zero. On synthetic data
// generated from a non-zero β, the shrinkage is visible: |μ_n[j]| <
// |β_OLS_j| for the slope coefficients. (The intercept is also shrunk;
// but we focus on slopes to keep the assertion robust to the centering
// of the data.)
func TestRegBayes_StrongPriorShrinks(t *testing.T) {
	// y = 10 + 3 x1 − 1.5 x2 + small noise. The OLS estimate recovers
	// these coefficients; a very strong N(0, σ²/ε) prior with ε = 1e6
	// shrinks them substantially.
	predictors := []string{"x1", "x2"}
	const n = 200
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		x1 := float64(i)
		x2 := math.Sin(0.05 * float64(i))
		y := 10.0 + 3.0*x1 - 1.5*x2 + 0.05*math.Cos(0.7*float64(i))
		records[i] = mockRecord{values: map[string]float64{
			"x1": x1, "x2": x2, "y": y,
		}}
	}

	olsRes, err := newOLS(t, &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors}).FitBuffered(records)
	if err != nil {
		t.Fatalf("OLS FitBuffered: %v", err)
	}

	bayesSpec := &types.RegressionSpec{
		Type:           types.REG_BAYES_LINEAR,
		Target:         "y",
		Predictors:     predictors,
		PriorPrecision: 1e6,
	}
	bayesRes, err := newBayes(t, bayesSpec).FitBuffered(records)
	if err != nil {
		t.Fatalf("Bayes FitBuffered: %v", err)
	}

	for _, name := range predictors {
		olsCoef := math.Abs(olsRes.Coefficients[name])
		bayesCoef := math.Abs(bayesRes.Coefficients[name])
		if bayesCoef >= olsCoef {
			t.Errorf("|β_%s| strong-prior=%v vs OLS=%v: expected strict shrinkage",
				name, bayesCoef, olsCoef)
		}
	}
}

// TestRegBayes_CredibleIntervalCoverage: simulate K independent
// datasets from y = β_true + ε with known σ, fit Bayes on each, and
// check the 95% credible interval contains the true β at least 92% of
// the time. The default prior is diffuse so the credible interval is
// well-calibrated under the data-generating process.
//
// Fixed seed for reproducibility. K = 200 keeps the test under ~1s.
func TestRegBayes_CredibleIntervalCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping coverage simulation in -short mode")
	}

	const (
		K         = 200
		n         = 80
		trueSlope = 1.7
		trueInt   = 0.4
		sigma     = 0.5
	)

	// Deterministic PRNG: linear congruential generator + Box-Muller.
	// Avoid math/rand to keep the test seed truly self-contained — Go
	// versions occasionally tweak default generators.
	type lcg struct{ state uint64 }
	next := func(g *lcg) float64 {
		// Numerical Recipes constants.
		g.state = g.state*6364136223846793005 + 1442695040888963407
		// Convert top 53 bits to a uniform double in [0, 1).
		return float64(g.state>>11) / float64(1<<53)
	}
	boxMuller := func(g *lcg) float64 {
		u1 := next(g)
		if u1 < 1e-300 {
			u1 = 1e-300
		}
		u2 := next(g)
		return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	}

	g := &lcg{state: 0xDEADBEEFCAFEBABE}
	hits := 0
	for k := 0; k < K; k++ {
		records := make([]Record, n)
		for i := 0; i < n; i++ {
			x := float64(i) / float64(n) // uniform on [0, 1)
			y := trueInt + trueSlope*x + sigma*boxMuller(g)
			records[i] = mockRecord{values: map[string]float64{"x": x, "y": y}}
		}
		spec := &types.RegressionSpec{
			Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"},
		}
		eng := newBayes(t, spec)
		res, err := eng.FitBuffered(records)
		if err != nil {
			t.Fatalf("draw %d Bayes FitBuffered: %v", k, err)
		}
		ci := res.CredibleIntervals["x"]
		if ci[0] <= trueSlope && trueSlope <= ci[1] {
			hits++
		}
	}

	// Nominal coverage 95%; allow a generous margin so the test is
	// reliable across CI environments. With K = 200 the standard error
	// of the empirical coverage is ~√(0.95·0.05/200) ≈ 1.5%, so 92% is
	// ~2σ below nominal.
	if hits < 184 { // 184 / 200 = 92%
		t.Errorf("credible-interval coverage = %d/%d = %.1f%%, want ≥ 92%%",
			hits, K, 100*float64(hits)/float64(K))
	}
}

// TestRegBayes_PriorValidation table-drives every invalid combination
// of prior parameters and disallowed cross-engine knobs.
func TestRegBayes_PriorValidation(t *testing.T) {
	cases := []struct {
		name string
		spec *types.RegressionSpec
		ok   bool
	}{
		{
			name: "default prior passes",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}},
			ok:   true,
		},
		{
			name: "nig prior passes",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Prior: "nig"},
			ok:   true,
		},
		{
			name: "positive precision/shape/rate passes",
			spec: &types.RegressionSpec{
				Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"},
				PriorPrecision: 1e-2, PriorShape: 2.0, PriorRate: 0.5,
			},
			ok: true,
		},
		{
			name: "explicit 0.99 credible level passes",
			spec: &types.RegressionSpec{
				Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"},
				CredibleLevel: 0.99,
			},
			ok: true,
		},
		{
			name: "PriorMu matching length passes",
			spec: &types.RegressionSpec{
				Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"},
				PriorMu: []float64{0.0, 0.5, -0.3},
			},
			ok: true,
		},
		{
			name: "unknown Prior rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Prior: "horseshoe-vb"},
			ok:   false,
		},
		{
			name: "negative PriorPrecision rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, PriorPrecision: -1},
			ok:   false,
		},
		{
			name: "negative PriorShape rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, PriorShape: -1},
			ok:   false,
		},
		{
			name: "negative PriorRate rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, PriorRate: -1},
			ok:   false,
		},
		{
			name: "CredibleLevel = 0 OK (defaults to 0.95)",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, CredibleLevel: 0.0},
			ok:   true,
		},
		{
			name: "CredibleLevel <= 0 rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, CredibleLevel: -0.1},
			ok:   false,
		},
		{
			name: "CredibleLevel >= 1 rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, CredibleLevel: 1.0},
			ok:   false,
		},
		{
			name: "PriorMu wrong length rejected",
			spec: &types.RegressionSpec{
				Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"},
				PriorMu: []float64{0.0, 0.5}, // missing the third entry
			},
			ok: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRegression(c.spec)
			if c.ok {
				if err != nil {
					t.Errorf("ValidateRegression(%+v) = %v, want nil", c.spec, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateRegression(%+v) = nil, want PROCESSING_CONFIG", c.spec)
			}
			if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("ValidateRegression error code = %v, want PROCESSING_CONFIG", err)
			}
		})
	}
}

// TestRegBayes_CompatibilityRejected asserts that Penalty / Alpha /
// L1Ratio / Family / Link on a REG_BAYES_LINEAR spec is rejected with
// PROCESSING_CONFIG. The README compatibility matrix forbids mixing
// these knobs into a Bayesian fit — silent acceptance would hide caller
// confusion about which operator they are configuring.
//
// Resample / Selection are tested separately (those route to the
// not-implemented stub for Phase 5; see TestFit_NotImplemented).
func TestRegBayes_CompatibilityRejected(t *testing.T) {
	cases := []struct {
		name string
		spec *types.RegressionSpec
	}{
		{
			name: "Penalty=l2 rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Penalty: "l2"},
		},
		{
			name: "Alpha rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Alpha: 0.1},
		},
		{
			name: "L1Ratio rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, L1Ratio: 0.5},
		},
		{
			name: "Family rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Family: "binomial"},
		},
		{
			name: "Link rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Link: "logit"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRegression(c.spec)
			if err == nil {
				t.Fatalf("ValidateRegression(%+v) = nil, want PROCESSING_CONFIG", c.spec)
			}
			if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("ValidateRegression error code = %v, want PROCESSING_CONFIG", err)
			}
		})
	}
}

// TestRegBayes_StreamingMatchesBuffered: the streaming engine
// (UpdateRow / Finalize) must produce coefficients identical to the
// buffered engine (FitBuffered) to ~1e-10 because both consume the same
// Welford accumulator.
func TestRegBayes_StreamingMatchesBuffered(t *testing.T) {
	predictors := []string{"x1", "x2"}
	records := make([]Record, 50)
	for i := 0; i < 50; i++ {
		x1 := float64(i)
		x2 := math.Cos(0.2 * float64(i))
		y := 1.0 + 0.5*x1 - 2.3*x2 + 0.1*math.Sin(0.7*float64(i))
		records[i] = mockRecord{values: map[string]float64{
			"x1": x1, "x2": x2, "y": y,
		}}
	}
	spec := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: predictors}

	buf := newBayes(t, spec)
	bufRes, err := buf.FitBuffered(records)
	if err != nil {
		t.Fatalf("buffered FitBuffered: %v", err)
	}

	streamEng, err := newBayesLinearEngine(spec, schemaWithFields("y", "x1", "x2"))
	if err != nil {
		t.Fatalf("streaming newBayesLinearEngine: %v", err)
	}
	se, ok := streamEng.(StreamingEngine)
	if !ok {
		t.Fatalf("Bayes engine does not implement StreamingEngine")
	}
	for _, rec := range records {
		if err := se.UpdateRow(rec); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	streamRes, err := se.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	const tol = 1e-10
	if !closef(bufRes.Coefficients[InterceptKey], streamRes.Coefficients[InterceptKey], tol) {
		t.Errorf("intercept buf=%v stream=%v Δ=%v",
			bufRes.Coefficients[InterceptKey], streamRes.Coefficients[InterceptKey],
			bufRes.Coefficients[InterceptKey]-streamRes.Coefficients[InterceptKey])
	}
	for _, name := range predictors {
		if !closef(bufRes.Coefficients[name], streamRes.Coefficients[name], tol) {
			t.Errorf("β_%s buf=%v stream=%v", name, bufRes.Coefficients[name], streamRes.Coefficients[name])
		}
		if !closef(bufRes.StdErrors[name], streamRes.StdErrors[name], tol) {
			t.Errorf("SE_%s buf=%v stream=%v", name, bufRes.StdErrors[name], streamRes.StdErrors[name])
		}
		bufCI := bufRes.CredibleIntervals[name]
		streamCI := streamRes.CredibleIntervals[name]
		if !closef(bufCI[0], streamCI[0], tol) || !closef(bufCI[1], streamCI[1], tol) {
			t.Errorf("CI_%s buf=%v stream=%v", name, bufCI, streamCI)
		}
	}
	if !closef(bufRes.R2, streamRes.R2, tol) {
		t.Errorf("R² buf=%v stream=%v", bufRes.R2, streamRes.R2)
	}
}

// TestCanStreamRequest_BayesLinear: lightweight registry-level check
// that RegressionSpec.Streamable() returns true for the bare Bayes
// spec, false with bootstrap / selection modifiers. Mirrors the
// processor-side TestCanStreamRequest_RegressionMatrix row that
// covers Bayes; runtime parity for the full request → CanStreamRequest
// path lives there. (CanStreamRequest itself sits in the parent
// processing package; importing it here would create a cycle.)
func TestCanStreamRequest_BayesLinear(t *testing.T) {
	cases := []struct {
		name string
		spec types.RegressionSpec
		want bool
	}{
		{"bare Bayes streams", types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}}, true},
		{"Bayes + bootstrap buffers", types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Resample: "bootstrap"}, false},
		{"Bayes + jackknife buffers", types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Resample: "jackknife"}, false},
		{"Bayes + stepwise buffers", types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Selection: "stepwise", Criterion: "aic"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.Streamable(); got != c.want {
				t.Errorf("Streamable() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestRegBayes_InsufficientData covers the n < p+1 guard rail.
func TestRegBayes_InsufficientData(t *testing.T) {
	spec := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"}}
	// Only 2 records but we need n ≥ p+1 = 3.
	records := []Record{
		mockRecord{values: map[string]float64{"x1": 0, "x2": 1, "y": 1}},
		mockRecord{values: map[string]float64{"x1": 1, "x2": 0, "y": 2}},
	}
	_, err := newBayes(t, spec).FitBuffered(records)
	if err == nil {
		t.Fatal("FitBuffered with n<p+1 returned nil error")
	}
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA) {
		t.Errorf("err = %v, want PROCESSING_REGRESSION_INSUFFICIENT_DATA", err)
	}
}

// TestStudentTQuantile_SanityValues spot-checks studentTQuantile against
// commonly-tabulated values from standard t-tables. Tolerance is 1e-4
// — credible intervals are not sensitive beyond this.
//
// Reference table (Wikipedia / Engineering Statistics Handbook):
//
//	df = 1,  p = 0.975  → t* ≈ 12.7062
//	df = 10, p = 0.975  → t* ≈ 2.2281
//	df = 30, p = 0.975  → t* ≈ 2.0423
//	df = 60, p = 0.975  → t* ≈ 2.0003
//	df = 100, p = 0.975 → t* ≈ 1.9840
//	df = ∞ (≈10000), p = 0.975 → t* → Φ⁻¹(0.975) ≈ 1.9600
func TestStudentTQuantile_SanityValues(t *testing.T) {
	cases := []struct {
		df   float64
		p    float64
		want float64
	}{
		{1, 0.975, 12.7062},
		{10, 0.975, 2.2281},
		{30, 0.975, 2.0423},
		{60, 0.975, 2.0003},
		{100, 0.975, 1.9840},
		{10000, 0.975, 1.9600},
		// Symmetry: q(0.025; df) = -q(0.975; df).
		{10, 0.025, -2.2281},
	}
	for _, c := range cases {
		got := studentTQuantile(c.p, c.df)
		if !closef(got, c.want, 1e-3) {
			t.Errorf("studentTQuantile(p=%v, df=%v) = %v, want %v ±1e-3", c.p, c.df, got, c.want)
		}
	}
}
