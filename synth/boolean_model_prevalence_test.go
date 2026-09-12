package synth_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// FU-24: where a modelled boolean's prevalence drift actually comes
// from.
//
// The motivating cohort's `aware` field carries bernoulli(p =
// 0.7473906704010238), so P(aware == 0) should be 0.2526093295989762.
// Measured over 20 seeds at 40,000 rows it came back at 0.247193 — a
// mean bias of -0.005417, ten standard errors low, so a real bias rather
// than a sampling artefact. The two suspects were bernoulliSampler's own
// draw and toBool's 0.5 rounding on the packed_bool write path. Both are
// EXONERATED: over 200 seeds a single-field bernoulli spec returns
// 0.252655 against 0.252609, a bias of +0.000046 at z = +0.28, and
// removing every model from the same 122-field spec takes the full
// pipeline to +0.000324 at z = +0.69.
//
// The bias is in the COMPOSED MODEL DRAW, and it is a property of the
// construction rather than a defect in any one function. A modelled
// target is drawn as value = Q(Φ(u)) with
//
//	u = (prediction − mean)/std + (residual_std/std)·z
//
// and the marginal is held at the captured p only because Φ(u) is
// uniform — which needs u to be standard NORMAL. At FIT time it is, by
// construction: Var(prediction) = R²·std² and residual_std² =
// (1−R²)·std², so the two terms sum to exactly 1. At GENERATION time the
// predictors are drawn from their own reconstructed marginals, so
// Var(prediction) is whatever THAT distribution produces and the sum is
// no longer 1. Measured on `aware`: Var(prediction)/std² = 0.045270 at
// generation against the captured R² of 0.077878, giving a latent
// variance of 0.970721 instead of 1.003329, and a normal approximation
// of that latent predicts P(aware == 0) = 0.247637 against the 20-seed
// mean of 0.247193. Nine of its 64 predictor terms never fire on a
// generated row, which is where most of the missing variance goes.
//
// The test below is that statement made falsifiable on a fixture small
// enough to compute by hand. It asserts all three claims at once,
// because each is trivially satisfiable by abandoning the others.

// TestSynthModel_BooleanPrevalenceFollowsTheGeneratedLatentVariance
// pins the mechanism:
//
//  1. an UNMODELLED bernoulli field in the same cohort comes back at
//     exactly its declared prevalence (the sampler and the write path
//     are exact);
//  2. a MODELLED one whose spec makes the generated latent standard
//     normal also comes back at exactly its declared prevalence;
//  3. a MODELLED one whose spec leaves the generated latent
//     UNDER-DISPERSED comes back at the prevalence that latent's own
//     variance implies — not at the declared one, and not at some third
//     number.
//
// Claim 3 is the load-bearing one. Without it, claims 1 and 2 are
// satisfied by any implementation that happens to be exact on this
// fixture, and the drift would keep reading as "the boolean marginal is
// slightly biased" rather than as "a modelled target's marginal is
// conditional on its latent being standard normal".
func TestSynthModel_BooleanPrevalenceFollowsTheGeneratedLatentVariance(t *testing.T) {
	const rows = 60000
	const p = 0.7473906704010238
	std := math.Sqrt(p * (1 - p))
	// cut is the latent threshold quantileFor's bernoulli arm applies:
	// value = 1 iff Φ(u) > 1−p, i.e. iff u > Φ⁻¹(1−p).
	cut := math.Sqrt2 * math.Erfinv(2*(1-p)-1)

	// One predictor firing on one of three equally weighted levels, with
	// a captured latent coefficient of exactly 0.6.
	const latentCoef = 0.6
	coef := latentCoef * std
	// Var(m) over the generated rows is latentCoef²·(1/3)(2/3) = 0.08,
	// and E[m] is set to 0 by the intercept, so the latent's variance is
	// entirely decided by residual_std.
	varPred := latentCoef * latentCoef * (1.0 / 3.0) * (2.0 / 3.0)
	intercept := p - (latentCoef/3)*std

	build := func(residualZ float64) *synth.Spec {
		return &synth.Spec{
			RowCount: rows,
			Fields: []synth.FieldSpec{
				{
					Name: "region", Type: "categorical_u8",
					Distribution: synth.DistWeightedCategorical,
					Params: map[string]any{
						"values":  []any{"east", "west", "north"},
						"weights": []any{1.0, 1.0, 1.0},
					},
				},
				{
					Name: "modelled", Type: "packed_bool",
					Distribution: synth.DistBernoulli,
					Params:       map[string]any{"p": p},
				},
				{
					Name: "untouched", Type: "packed_bool",
					Distribution: synth.DistBernoulli,
					Params:       map[string]any{"p": p},
				},
			},
			Models: []synth.FieldModelSpec{{
				Field:       "modelled",
				Intercept:   intercept,
				Predictors:  []synth.ModelPredictorSpec{catLevel("region", "east", coef)},
				ResidualStd: residualZ * std,
			}},
		}
	}

	share1 := func(v []float64) float64 {
		ones := 0
		for _, x := range v {
			if x != 0 {
				ones++
			}
		}
		return float64(ones) / float64(len(v))
	}

	// Case A — the latent is standard normal by construction:
	// Var(m) + residualZ² == 1.
	calibrated := math.Sqrt(1 - varPred)
	dataA, _, err := synth.SynthBytes(build(calibrated), synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("case A SynthBytes: %v", err)
	}
	gotA := share1(readF64Field(t, dataA, "modelled"))
	if math.Abs(gotA-p) > 0.006 {
		t.Errorf("standard-normal latent: modelled prevalence %.4f, want %.4f (declared p) — "+
			"the composed draw must hold the marginal when Var(u) == 1", gotA, p)
	}

	// Claim 1: the unmodelled sibling, drawn by bernoulliSampler and
	// written through toBool, is exact in the SAME cohort. This is what
	// rules the sampler and the write path out as the source of any
	// drift the modelled field shows.
	gotUntouched := share1(readF64Field(t, dataA, "untouched"))
	if math.Abs(gotUntouched-p) > 0.005 {
		t.Errorf("unmodelled sibling prevalence %.4f, want %.4f — bernoulliSampler and the "+
			"packed_bool write path are exact and must stay so", gotUntouched, p)
	}

	// Case B — the SAME captured coefficient and the SAME marginal, with
	// a residual that leaves the latent under-dispersed. The prevalence
	// must follow the latent's own variance, and the predicted value is
	// computed here rather than hard-coded so the assertion states the
	// mechanism instead of a number.
	const underZ = 0.6
	dataB, _, err := synth.SynthBytes(build(underZ), synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("case B SynthBytes: %v", err)
	}
	gotB := share1(readF64Field(t, dataB, "modelled"))
	latentSd := math.Sqrt(varPred + underZ*underZ)
	wantB := 1 - 0.5*(1+math.Erf((cut/latentSd)/math.Sqrt2))
	if math.Abs(gotB-wantB) > 0.008 {
		t.Errorf("under-dispersed latent (sd %.4f): modelled prevalence %.4f, want %.4f — "+
			"the marginal follows the GENERATED latent's variance, not the declared p",
			latentSd, gotB, wantB)
	}
	if gotB-p < 0.05 {
		t.Errorf("under-dispersed latent: prevalence %.4f is within %.4f of the declared p %.4f — "+
			"the drift this test exists to pin is not present, so the assertion above proves nothing",
			gotB, math.Abs(gotB-p), p)
	}
}
