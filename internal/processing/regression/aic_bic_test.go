package regression

import (
	"math"
	"testing"
)

// TestAICBICFromOLS_BasicShape verifies the AIC/BIC criterion helpers
// for a degenerate sanity case: two candidate models on the same target,
// one with smaller RSS and the same k. AIC should reward the smaller-
// RSS model (lower score).
func TestAICBICFromOLS_BasicShape(t *testing.T) {
	aicA, bicA, logLA := aicBICFromOLS(100, 50, 2)
	aicB, bicB, logLB := aicBICFromOLS(50, 50, 2)
	if aicB >= aicA {
		t.Errorf("AIC: smaller RSS did not produce smaller score (a=%v, b=%v)", aicA, aicB)
	}
	if bicB >= bicA {
		t.Errorf("BIC: smaller RSS did not produce smaller score (a=%v, b=%v)", bicA, bicB)
	}
	if logLB <= logLA {
		t.Errorf("logL: smaller RSS did not produce larger log-likelihood (a=%v, b=%v)", logLA, logLB)
	}
}

// TestAICBICFromOLS_ParameterPenalty verifies that holding RSS constant
// and increasing k increases both AIC and BIC. BIC's penalty is heavier
// (log(n) > 2 when n > 7), so BIC's increase should exceed AIC's.
func TestAICBICFromOLS_ParameterPenalty(t *testing.T) {
	aic1, bic1, _ := aicBICFromOLS(75, 100, 1)
	aic2, bic2, _ := aicBICFromOLS(75, 100, 5)
	if aic2 <= aic1 {
		t.Errorf("AIC: more parameters did not increase score (k=1: %v, k=5: %v)", aic1, aic2)
	}
	if bic2 <= bic1 {
		t.Errorf("BIC: more parameters did not increase score (k=1: %v, k=5: %v)", bic1, bic2)
	}
	if bic2-bic1 <= aic2-aic1 {
		t.Errorf("BIC penalty should exceed AIC penalty for n=100; got Δaic=%v, Δbic=%v", aic2-aic1, bic2-bic1)
	}
}

// TestAICBICFromGLM_DevianceShift confirms a higher-deviance model
// scores higher (worse) on both AIC and BIC.
func TestAICBICFromGLM_DevianceShift(t *testing.T) {
	aicA, bicA, _ := aicBICFromGLM(120.0, 200, 2)
	aicB, bicB, _ := aicBICFromGLM(80.0, 200, 2)
	if aicB >= aicA {
		t.Errorf("AIC: smaller deviance did not produce smaller score (a=%v, b=%v)", aicA, aicB)
	}
	if bicB >= bicA {
		t.Errorf("BIC: smaller deviance did not produce smaller score (a=%v, b=%v)", bicA, bicB)
	}
}

// TestAICBICFromOLS_DegenerateRSS asserts the perfect-fit short-circuit:
// RSS=0 returns large-negative scores so selection treats it as the
// best candidate.
func TestAICBICFromOLS_DegenerateRSS(t *testing.T) {
	aic, bic, logL := aicBICFromOLS(0, 50, 2)
	if !(aic < -1e10) || !(bic < -1e10) {
		t.Errorf("RSS=0 did not produce dominant criterion values (aic=%v, bic=%v)", aic, bic)
	}
	if !(logL > 1e10) {
		t.Errorf("RSS=0 did not produce large log-likelihood (logL=%v)", logL)
	}
}

// TestAICBICFromOLS_NaNGuard asserts the helper does not panic and
// returns NaN for non-physical inputs.
func TestAICBICFromOLS_NaNGuard(t *testing.T) {
	aic, _, _ := aicBICFromOLS(100, 0, 2)
	if !math.IsNaN(aic) {
		t.Errorf("aicBICFromOLS(100, 0, 2) = %v, want NaN", aic)
	}
}
