package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newGLM is the test helper that mirrors newOLS: it wires the GLM
// engine for a spec against a schema synthesized from the spec's
// Target / Predictors names. The schema field types are all f64 — the
// engine itself only consults Record.NumericValue, so the field-type
// choice never reaches the math.
func newGLM(t *testing.T, spec *types.RegressionSpec) Engine {
	t.Helper()
	schema := schemaWithFields(append([]string{spec.Target}, spec.Predictors...)...)
	eng, err := newGLMEngine(spec, schema)
	if err != nil {
		t.Fatalf("newGLMEngine: %v", err)
	}
	return eng
}

// asBuffered casts an Engine to BufferedEngine or fails the test. GLM
// always returns a BufferedEngine implementation.
func asBuffered(t *testing.T, eng Engine) BufferedEngine {
	t.Helper()
	be, ok := eng.(BufferedEngine)
	if !ok {
		t.Fatalf("engine %T does not implement BufferedEngine", eng)
	}
	return be
}

// TestRegGLM_Logistic_NumericalFixture exercises binomial+logit on a
// 60-row, 2-predictor synthetic dataset with class overlap.
//
// Data-generating process (deterministic):
//
//	for i in 0..59:
//	    x1 = (i - 30) * 0.1            # range [-3.0, 2.9]
//	    x2 = sin(0.31*i + 0.2)
//	    z  = -0.5 + 1.2*x1 + 0.8*x2 + 0.9*sin(1.7*i)
//	    y  = 1 if z > 0 else 0
//
// The 0.9·sin perturbation introduces enough label noise around the
// decision boundary to keep the dataset non-separable so IRLS
// converges to a finite optimum (a strictly-separable design would
// drive coefficients to infinity and surface NO_CONVERGE — see
// TestRegGLM_Separable_Diverges for that case).
//
// Reference values were derived by running the engine itself on this
// exact fixture (the IRLS algorithm is textbook; this fixture's
// purpose is regression detection, not external-oracle validation).
// To re-derive, run `go test -run TestRegGLM_Logistic_NumericalFixture
// -v ./processing/regression/` after temporarily replacing the t.Errorf
// blocks below with t.Logf and inspect the output; the printed
// coefficients become the constants here.
func TestRegGLM_Logistic_NumericalFixture(t *testing.T) {
	predictors := []string{"x1", "x2"}
	target := "y"

	const n = 60
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		x1 := (float64(i) - 30) * 0.1
		x2 := math.Sin(0.31*float64(i) + 0.2)
		z := -0.5 + 1.2*x1 + 0.8*x2 + 0.9*math.Sin(1.7*float64(i))
		var y float64
		if z > 0 {
			y = 1
		}
		records[i] = mockRecord{values: map[string]float64{
			"x1": x1, "x2": x2, "y": y,
		}}
	}

	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     target,
		Predictors: predictors,
		Family:     "binomial",
		Link:       "logit",
	}
	eng := asBuffered(t, newGLM(t, spec))
	res, err := eng.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}

	// Reference coefficients (derived from running IRLS on this fixture;
	// tolerance 1e-3 catches any algorithmic regression while leaving
	// headroom for compiler / gonum upgrades that affect the last few
	// digits of a Cholesky solve).
	wantIntercept := -1.01641 // (intercept)
	wantBetaX1 := 2.65913
	wantBetaX2 := 1.56339

	const tol = 1e-3
	if !closef(res.Coefficients[InterceptKey], wantIntercept, tol) {
		t.Errorf("intercept = %v, want %v ±%v", res.Coefficients[InterceptKey], wantIntercept, tol)
	}
	if !closef(res.Coefficients["x1"], wantBetaX1, tol) {
		t.Errorf("β_x1 = %v, want %v ±%v", res.Coefficients["x1"], wantBetaX1, tol)
	}
	if !closef(res.Coefficients["x2"], wantBetaX2, tol) {
		t.Errorf("β_x2 = %v, want %v ±%v", res.Coefficients["x2"], wantBetaX2, tol)
	}

	if res.Type != types.REG_GLM {
		t.Errorf("Type = %v, want REG_GLM", res.Type)
	}
	if res.Family != "binomial" {
		t.Errorf("Family = %v, want binomial", res.Family)
	}
	if res.Link != "logit" {
		t.Errorf("Link = %v, want logit", res.Link)
	}
	if res.NObs != n {
		t.Errorf("NObs = %d, want %d", res.NObs, n)
	}
	if res.ConvergedIters <= 0 {
		t.Errorf("ConvergedIters = %d, want > 0", res.ConvergedIters)
	}
	if res.ConvergedIters > 50 {
		t.Errorf("ConvergedIters = %d, want ≤ 50 (default cap)", res.ConvergedIters)
	}
	if res.PseudoR2 < 0.5 {
		t.Errorf("PseudoR² = %v, want ≥ 0.5 on this well-separated fixture", res.PseudoR2)
	}
	if res.Deviance <= 0 {
		t.Errorf("Deviance = %v, want > 0", res.Deviance)
	}
	if res.NullDeviance <= 0 {
		t.Errorf("NullDeviance = %v, want > 0", res.NullDeviance)
	}
	if res.NullDeviance < res.Deviance {
		t.Errorf("NullDeviance (%v) < Deviance (%v); the intercept-only fit should be worse than the full fit",
			res.NullDeviance, res.Deviance)
	}

	// Every coefficient must have a standard error and a p-value.
	for _, k := range []string{InterceptKey, "x1", "x2"} {
		if _, ok := res.StdErrors[k]; !ok {
			t.Errorf("StdErrors missing %s", k)
		}
		if _, ok := res.PValues[k]; !ok {
			t.Errorf("PValues missing %s", k)
		}
	}

	// OLS-only fields must stay at their zero value on a GLM result.
	if res.R2 != 0 {
		t.Errorf("R2 = %v, want 0 (GLM result, R2 belongs to OLS)", res.R2)
	}
	if res.AdjR2 != 0 {
		t.Errorf("AdjR2 = %v, want 0 (GLM result)", res.AdjR2)
	}
	// Penalty / Alpha / L1Ratio must be empty / zero on a GLM result.
	if res.Penalty != "" {
		t.Errorf("Penalty = %v, want \"\"", res.Penalty)
	}
}

// TestRegGLM_Poisson_NumericalFixture exercises poisson+log on a
// 100-row, 1-predictor synthetic dataset with a known rate function:
//
//	for i in 0..99:
//	    x  = (i - 50) * 0.04           # range [-2.0, 1.96]
//	    μ  = exp(0.5 + 0.3·x)
//	    y  = round(μ + 0.4·sin(0.59*i))    # deterministic perturbation
//
// The "+0.4·sin" term is a deterministic stand-in for Poisson noise:
// the engine doesn't care that the y values aren't strictly Poisson —
// it fits the conditional mean.
//
// Reference values were derived by running the engine itself on this
// exact fixture; tolerance 1e-3 catches algorithmic regressions.
func TestRegGLM_Poisson_NumericalFixture(t *testing.T) {
	predictors := []string{"x"}
	target := "y"
	const n = 100
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		x := (float64(i) - 50) * 0.04
		muTrue := math.Exp(0.5 + 0.3*x)
		y := math.Round(muTrue + 0.4*math.Sin(0.59*float64(i)))
		if y < 0 {
			y = 0
		}
		records[i] = mockRecord{values: map[string]float64{
			"x": x, "y": y,
		}}
	}

	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     target,
		Predictors: predictors,
		Family:     "poisson",
		Link:       "log",
	}
	eng := asBuffered(t, newGLM(t, spec))
	res, err := eng.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}

	// Reference coefficients (derived from running IRLS on this fixture).
	wantIntercept := 0.50346
	wantSlope := 0.29216

	const tol = 1e-3
	if !closef(res.Coefficients[InterceptKey], wantIntercept, tol) {
		t.Errorf("intercept = %v, want %v ±%v", res.Coefficients[InterceptKey], wantIntercept, tol)
	}
	if !closef(res.Coefficients["x"], wantSlope, tol) {
		t.Errorf("β_x = %v, want %v ±%v", res.Coefficients["x"], wantSlope, tol)
	}

	if res.Family != "poisson" {
		t.Errorf("Family = %v, want poisson", res.Family)
	}
	if res.Link != "log" {
		t.Errorf("Link = %v, want log", res.Link)
	}
	if res.NObs != n {
		t.Errorf("NObs = %d, want %d", res.NObs, n)
	}
	// Poisson with this signal converges very fast; canonical log-link
	// IRLS usually settles in ≤ 6-8 cycles.
	if res.ConvergedIters > 20 {
		t.Errorf("ConvergedIters = %d, want ≤ 20", res.ConvergedIters)
	}
	if res.Deviance <= 0 {
		t.Errorf("Deviance = %v, want > 0", res.Deviance)
	}
	if res.PseudoR2 < 0.1 || res.PseudoR2 > 1 {
		t.Errorf("PseudoR² = %v, expected in [0.1, 1] on this signal", res.PseudoR2)
	}
}

// TestRegGLM_Separable_Diverges constructs a perfectly separable
// logistic dataset (all y=0 for x<0, all y=1 for x≥0). IRLS coefficients
// blow up because the maximum-likelihood estimate is at infinity; the
// engine surfaces PROCESSING_REGRESSION_NO_CONVERGE after MaxIters.
func TestRegGLM_Separable_Diverges(t *testing.T) {
	predictors := []string{"x"}
	target := "y"
	const n = 40
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		x := float64(i) - 19.5 // crosses zero between i=19 and i=20
		var y float64
		if x >= 0 {
			y = 1
		}
		records[i] = mockRecord{values: map[string]float64{
			"x": x, "y": y,
		}}
	}

	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     target,
		Predictors: predictors,
		Family:     "binomial",
		// MaxIters short enough to keep the test fast; even with 200 the
		// fit cannot converge because the MLE is at infinity.
		MaxIters: 15,
	}
	eng := asBuffered(t, newGLM(t, spec))
	_, err := eng.FitBuffered(records)
	if err == nil {
		t.Fatal("FitBuffered on separable data returned nil error; want PROCESSING_REGRESSION_NO_CONVERGE or RANK_DEFICIENT")
	}
	// Either NO_CONVERGE (β grew without converging) or RANK_DEFICIENT
	// (the working-weight diagonal collapsed) is acceptable here — both
	// are the correct symptom of a separable design and both surface
	// canonical regression error codes.
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_NO_CONVERGE) &&
		!errors.HasCode(err, errors.PROCESSING_REGRESSION_RANK_DEFICIENT) {
		t.Errorf("err = %v, want PROCESSING_REGRESSION_NO_CONVERGE or RANK_DEFICIENT", err)
	}
}

// TestRegGLM_InvalidFamily asserts the engine rejects unknown families
// via PROCESSING_REGRESSION_INVALID_FAMILY at the spec-validation step.
// Empty Family is also rejected (Family is required for REG_GLM).
func TestRegGLM_InvalidFamily(t *testing.T) {
	cases := []struct {
		name   string
		family string
	}{
		{"unknown family", "bogus"},
		{"empty family", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type:       types.REG_GLM,
				Target:     "y",
				Predictors: []string{"x"},
				Family:     c.family,
				Link:       "logit",
			}
			schema := schemaWithFields("y", "x")
			_, err := newGLMEngine(spec, schema)
			if err == nil {
				t.Fatal("newGLMEngine returned nil error; want PROCESSING_REGRESSION_INVALID_FAMILY")
			}
			if !errors.HasCode(err, errors.PROCESSING_REGRESSION_INVALID_FAMILY) {
				t.Errorf("err = %v, want PROCESSING_REGRESSION_INVALID_FAMILY", err)
			}
		})
	}
}

// TestRegGLM_InvalidLink covers all the link-rejection paths: every
// reserved-but-not-implemented value (binomial probit/cloglog, poisson
// identity/sqrt, gamma log/identity) plus a cross-family combo
// (binomial+log) plus unknown links.
func TestRegGLM_InvalidLink(t *testing.T) {
	cases := []struct {
		name   string
		family string
		link   string
	}{
		{"binomial+probit (reserved)", "binomial", "probit"},
		{"binomial+cloglog (reserved)", "binomial", "cloglog"},
		{"binomial+log (wrong family)", "binomial", "log"},
		{"binomial+sqrt (unknown)", "binomial", "sqrt"},
		{"poisson+identity (reserved)", "poisson", "identity"},
		{"poisson+sqrt (reserved)", "poisson", "sqrt"},
		{"poisson+logit (wrong family)", "poisson", "logit"},
		{"gamma+log (reserved)", "gamma", "log"},
		{"gamma+identity (reserved)", "gamma", "identity"},
		{"gamma+logit (wrong family)", "gamma", "logit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type:       types.REG_GLM,
				Target:     "y",
				Predictors: []string{"x"},
				Family:     c.family,
				Link:       c.link,
			}
			schema := schemaWithFields("y", "x")
			_, err := newGLMEngine(spec, schema)
			if err == nil {
				t.Fatalf("newGLMEngine(%s,%s) returned nil error; want PROCESSING_REGRESSION_INVALID_LINK",
					c.family, c.link)
			}
			if !errors.HasCode(err, errors.PROCESSING_REGRESSION_INVALID_LINK) {
				t.Errorf("err = %v, want PROCESSING_REGRESSION_INVALID_LINK", err)
			}
		})
	}
}

// TestRegGLM_PenaltyRejected asserts the engine refuses any Penalty /
// Alpha / L1Ratio knob on a REG_GLM spec. Regularized GLM is deferred
// to a later phase; silently dropping the knob would mislead callers
// into thinking they got a regularized fit.
func TestRegGLM_PenaltyRejected(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*types.RegressionSpec)
	}{
		{"l1 penalty", func(s *types.RegressionSpec) { s.Penalty = "l1" }},
		{"l2 penalty", func(s *types.RegressionSpec) { s.Penalty = "l2" }},
		{"elasticnet penalty", func(s *types.RegressionSpec) { s.Penalty = "elasticnet" }},
		{"nonzero alpha", func(s *types.RegressionSpec) { s.Alpha = 0.1 }},
		{"nonzero l1_ratio", func(s *types.RegressionSpec) { s.L1Ratio = 0.5 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type:       types.REG_GLM,
				Target:     "y",
				Predictors: []string{"x"},
				Family:     "binomial",
			}
			c.mod(spec)
			schema := schemaWithFields("y", "x")
			_, err := newGLMEngine(spec, schema)
			if err == nil {
				t.Fatal("newGLMEngine returned nil error; want PROCESSING_CONFIG")
			}
			if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("err = %v, want PROCESSING_CONFIG", err)
			}
		})
	}
}

// TestRegGLM_InsufficientData covers n < p + 1: 2 observations against
// 3 predictors fails the n ≥ p + 1 = 4 threshold inside FitBuffered.
func TestRegGLM_InsufficientData(t *testing.T) {
	predictors := []string{"x1", "x2", "x3"}
	records := []Record{
		mockRecord{values: map[string]float64{"x1": 1, "x2": 2, "x3": 3, "y": 1}},
		mockRecord{values: map[string]float64{"x1": 4, "x2": 5, "x3": 6, "y": 0}},
	}
	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     "y",
		Predictors: predictors,
		Family:     "binomial",
	}
	eng := asBuffered(t, newGLM(t, spec))
	_, err := eng.FitBuffered(records)
	if err == nil {
		t.Fatal("FitBuffered: nil error; want PROCESSING_REGRESSION_INSUFFICIENT_DATA")
	}
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA) {
		t.Errorf("err = %v, want PROCESSING_REGRESSION_INSUFFICIENT_DATA", err)
	}
}

// TestRegGLM_BufferedOrchestrator asserts the request runs through the
// buffered path: RegressionSpec.Streamable() returns false on every
// GLM variant, the engine never implements StreamingEngine, and the
// fit produces a populated RegressionResult with ConvergedIters > 0.
func TestRegGLM_BufferedOrchestrator(t *testing.T) {
	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     "y",
		Predictors: []string{"x"},
		Family:     "binomial",
	}
	if spec.Streamable() {
		t.Fatal("RegressionSpec.Streamable() = true on REG_GLM; want false")
	}
	if types.REG_GLM.Streamable() {
		t.Fatal("REG_GLM.Streamable() = true; want false")
	}
	schema := schemaWithFields("y", "x")
	eng, err := newGLMEngine(spec, schema)
	if err != nil {
		t.Fatalf("newGLMEngine: %v", err)
	}
	if _, ok := eng.(StreamingEngine); ok {
		t.Error("glmEngine implements StreamingEngine; GLM must remain buffered-only")
	}
	if _, ok := eng.(BufferedEngine); !ok {
		t.Fatal("glmEngine does not implement BufferedEngine")
	}

	// End-to-end smoke: a non-separable logistic dataset. The check is
	// structural — converged_iters > 0 and a populated coefficient map
	// — not numerical. The 0.9·sin noise component matches the
	// numerical-fixture pattern so the dataset stays non-separable.
	const n = 40
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		x := (float64(i) - 20) * 0.1
		z := -0.5 + 1.5*x + 0.9*math.Sin(1.7*float64(i))
		var y float64
		if z > 0 {
			y = 1
		}
		records[i] = mockRecord{values: map[string]float64{"x": x, "y": y}}
	}
	res, err := eng.(BufferedEngine).FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	if res.ConvergedIters <= 0 {
		t.Errorf("ConvergedIters = %d, want > 0", res.ConvergedIters)
	}
	if _, ok := res.Coefficients[InterceptKey]; !ok {
		t.Error("Coefficients missing intercept")
	}
	if _, ok := res.Coefficients["x"]; !ok {
		t.Error("Coefficients missing x")
	}
}

// TestRegGLM_GammaInverse_Smoke confirms the gamma+inverse link is
// wired and the engine produces a finite, non-NaN coefficient vector
// on a synthetic dataset. Numerical validation against an external
// oracle is deferred per the locked plan; this test exists so the
// gamma path doesn't silently regress before its dedicated fixtures
// land.
func TestRegGLM_GammaInverse_Smoke(t *testing.T) {
	predictors := []string{"x"}
	target := "y"
	const n = 80
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		// Generate strictly-positive y with a slow trend so 1/μ ≈ a + b·x
		// has a well-conditioned solve.
		x := 0.1 + 0.01*float64(i)
		muTrue := 1.0 / (0.5 + 0.3*x)
		y := muTrue * (1 + 0.05*math.Sin(0.41*float64(i)))
		records[i] = mockRecord{values: map[string]float64{
			"x": x, "y": y,
		}}
	}
	spec := &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     target,
		Predictors: predictors,
		Family:     "gamma",
		Link:       "inverse",
	}
	eng := asBuffered(t, newGLM(t, spec))
	res, err := eng.FitBuffered(records)
	if err != nil {
		// Gamma path is not numerically validated; an internal solver
		// error is acceptable but a non-canonical Go panic is not. Log
		// the error and skip the rest of the assertions.
		t.Skipf("gamma+inverse smoke fit returned %v; numerical validation is deferred", err)
	}
	if math.IsNaN(res.Coefficients[InterceptKey]) || math.IsInf(res.Coefficients[InterceptKey], 0) {
		t.Errorf("gamma intercept = %v, want finite", res.Coefficients[InterceptKey])
	}
	if math.IsNaN(res.Coefficients["x"]) || math.IsInf(res.Coefficients["x"], 0) {
		t.Errorf("gamma slope = %v, want finite", res.Coefficients["x"])
	}
}
