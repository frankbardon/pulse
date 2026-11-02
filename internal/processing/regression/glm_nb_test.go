package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/types"
)

// TestNegBin_ProbabilityWeightsConstantAreUnweighted: a probability
// weight that is the same on every row maps onto w* = 1 (w·n_eff/Σw), so
// the negative binomial fit — θ̂, SE(θ̂), β, the vcov and the
// log-likelihood — equals the unweighted one (to the round-off of the
// rescale); a frequency weight of 1 everywhere is the unweighted fit too.
// The probability fit reports no two_log_lik / aic (loglik.go).
func TestNegBin_ProbabilityWeightsConstantAreUnweighted(t *testing.T) {
	t.Parallel()

	c := loadGLMNB(t)[0] // nb_log
	plain, err := asBuffered(t, newGLM(t, c.spec())).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []types.WeightKind{types.WeightKindProbability, types.WeightKindFrequency} {
		w := 2.5
		if kind == types.WeightKindFrequency {
			w = 1
		}
		recs := make([]Record, len(c.Rows))
		for i, r := range c.Rows {
			recs[i] = mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1], "y": r[2], "w": w}}
		}
		spec := c.spec()
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
		res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for _, f := range []struct {
			name      string
			got, want float64
		}{
			{"theta", res.Theta, plain.Theta}, {"theta_std_err", res.ThetaStdErr, plain.ThetaStdErr},
			{"deviance", res.Deviance, plain.Deviance},
			{"x1", res.Coefficients["x1"], plain.Coefficients["x1"]}, {"se x2", res.StdErrors["x2"], plain.StdErrors["x2"]},
		} {
			if !oracleClose(f.got, f.want, 1e-12) {
				t.Errorf("%s %s = %.17g, unweighted %.17g", kind, f.name, f.got, f.want)
			}
		}
		// The likelihood: the expansion's under frequency, absent under
		// probability (a pseudo-likelihood; loglik.go).
		wantLL, wantAIC := plain.TwoLogLik, plain.AIC
		if kind == types.WeightKindProbability {
			wantLL, wantAIC = 0, 0
		}
		if !oracleClose(res.TwoLogLik, wantLL, 1e-12) || !oracleClose(res.AIC, wantAIC, 1e-12) {
			t.Errorf("%s two_log_lik %v aic %v, want %v %v", kind, res.TwoLogLik, res.AIC, wantLL, wantAIC)
		}
	}
}

// TestNegBin_UnderdispersedIsRefused: counts no more variable than a
// poisson (here every y equals its mean, rounded) put θ̂ at infinity: the
// fit is PROCESSING_REGRESSION_NO_CONVERGE reason theta_unbounded (a
// data-dependent refusal, so the slot fails alone), never a huge θ; the
// same rows fit with θ fixed.
func TestNegBin_UnderdispersedIsRefused(t *testing.T) {
	t.Parallel()

	recs := make([]Record, 60)
	for i := range recs {
		x1, x2 := float64(i%10)/5, float64(i%7)/3-1
		recs[i] = mockRecord{values: map[string]float64{"x1": x1, "x2": x2, "y": math.Round(math.Exp(1 + 0.4*x1 - 0.3*x2))}}
	}
	spec := &types.RegressionSpec{Type: types.REG_GLM, Family: reglink.NegativeBinomial, Target: "y", Predictors: []string{"x1", "x2"}}
	_, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_REGRESSION_NO_CONVERGE || ce.Details["reason"] != "theta_unbounded" || ce.Details["family"] != reglink.NegativeBinomial {
		t.Fatalf("err = %v, want NO_CONVERGE theta_unbounded", err)
	}
	spec.Theta = 5
	res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
	if err != nil || res.Theta != 5 || res.ThetaStdErr != 0 {
		t.Fatalf("fixed θ fit: %v, θ %v, SE %v", err, res.Theta, res.ThetaStdErr)
	}
}

// TestNegBin_DoesNotTouchOtherFamilies: the negative binomial's knobs
// stay off the other families' results — no theta or theta_std_err on
// a poisson fit (two_log_lik / aic are every family's since loglik.go).
func TestNegBin_DoesNotTouchOtherFamilies(t *testing.T) {
	t.Parallel()

	c := loadGLMNB(t)[0]
	spec := c.spec()
	spec.Family = "poisson"
	res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	if res.Theta != 0 || res.ThetaStdErr != 0 {
		t.Fatalf("poisson fit carries θ %v, SE %v", res.Theta, res.ThetaStdErr)
	}
}
