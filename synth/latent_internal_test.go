package synth

import (
	"math"
	"testing"
)

// TestLatentFor_InvertsQuantileForEveryDistribution is the lockstep gate
// between quantileFor (generation's latent -> value map) and latentFor
// (the fidelity report's value -> latent map) for every distribution
// whose Q is strictly monotone: u -> Q(u, phi(u)) -> latentFor must
// return u.
//
// It no longer covers every distribution fieldMoments admits.
// DistBernoulli is admitted with a STEP Q, which no inverse can undo, and
// is therefore excluded here and covered by
// TestLatentFor_EveryDistributionIsClassified instead — that test is what
// keeps the partition exhaustive, so a new distribution cannot fall out
// of both.
//
// It is a build-failing gate rather than a spot check because BOTH ways
// the two can drift are silent at runtime. A distribution admitted to
// quantileFor with no arm here drops every model targeting it out of the
// recovery section without a word; an arm inverted wrongly reports a
// false attenuation for a generation path that is exactly correct — and
// a false attenuation is worse than no section at all, because it sends
// a reader hunting a generation bug that does not exist.
func TestLatentFor_InvertsQuantileForEveryDistribution(t *testing.T) {
	// The probe points stay inside +/-2.5 sd. Beyond that the mixture's
	// bisection bracket and float64's grip on exp() both start costing
	// digits, and this test is about the ALGEBRA matching, not about the
	// numerical reach of either transform.
	probes := []float64{-2.5, -1.3, -0.4, 0, 0.4, 1.3, 2.5}

	cases := []struct {
		name string
		fs   FieldSpec
		// tol is the round-trip tolerance in latent units. Closed-form
		// arms are exact to floating point; the mixture arm is inverted
		// by a fixed 64-step bisection, so its own forward map is only
		// accurate to the bracket width over 2^64 steps in principle and
		// to about 1e-9 in practice.
		tol float64
	}{
		{
			name: "normal",
			fs: FieldSpec{Name: "n", Distribution: DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 25.0}},
			tol: 1e-12,
		},
		{
			name: "uniform",
			fs: FieldSpec{Name: "u", Distribution: DistUniform,
				Params: map[string]any{"min": -4.0, "max": 12.0}},
			tol: 1e-9,
		},
		{
			name: "lognormal",
			fs: FieldSpec{Name: "l", Distribution: DistLogNormal,
				Params: map[string]any{"mu": 1.5, "sigma": 0.7}},
			tol: 1e-12,
		},
		{
			name: "exponential",
			fs: FieldSpec{Name: "e", Distribution: DistExponential,
				Params: map[string]any{"lambda": 0.25}},
			tol: 1e-9,
		},
		{
			name: "mixture",
			fs: FieldSpec{Name: "m", Distribution: DistMixture,
				Params: map[string]any{
					"means":   []any{10.0, 40.0},
					"stds":    []any{3.0, 8.0},
					"weights": []any{0.6, 0.4},
				}},
			tol: 1e-6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mean, std, _, _, _, err := fieldMoments(tc.fs)
			if err != nil {
				t.Fatalf("fieldMoments: %v", err)
			}
			q, err := quantileFor(tc.fs, mean, std)
			if err != nil {
				t.Fatalf("quantileFor: %v", err)
			}
			inv, err := latentFor(tc.fs, mean, std)
			if err != nil {
				t.Fatalf("latentFor: %v", err)
			}
			for _, u := range probes {
				v := q(u, phi(u))
				got, ok := inv(v)
				if !ok {
					t.Fatalf("u=%v -> value %v: latent inverse reported the value unplaceable", u, v)
				}
				if math.Abs(got-u) > tc.tol {
					t.Errorf("u=%v -> value %v -> latent %v; round-trip error %g exceeds %g",
						u, v, got, math.Abs(got-u), tc.tol)
				}
			}
		})
	}
}

// TestLatentFor_RefusesUnsupportedDistribution pins the defensive half of
// latentFor's refusal: a distribution fieldMoments does not admit at all
// must say so rather than answer with a plausible number. (The other
// refusal — admitted but not invertible — is the step-quantile case, held
// by TestLatentFor_EveryDistributionIsClassified.)
func TestLatentFor_RefusesUnsupportedDistribution(t *testing.T) {
	fs := FieldSpec{Name: "p", Distribution: DistPoisson,
		Params: map[string]any{"lambda": 3.0}}
	if _, err := latentFor(fs, 3, 1.7); err == nil {
		t.Fatal("latentFor accepted poisson; fieldMoments refuses it, so the two have drifted")
	}
}

// TestLatentFor_ReportsUnplaceableValues covers the ok=false contract on
// the two arms whose support does not cover the whole real line. A
// generated value outside it is not a number the inverse may guess at:
// the row must drop out of the refit, exactly as a null would.
func TestLatentFor_ReportsUnplaceableValues(t *testing.T) {
	logn := FieldSpec{Name: "l", Distribution: DistLogNormal,
		Params: map[string]any{"mu": 1.0, "sigma": 0.5}}
	inv, err := latentFor(logn, 3, 1.5)
	if err != nil {
		t.Fatalf("latentFor lognormal: %v", err)
	}
	if _, ok := inv(0); ok {
		t.Error("lognormal inverse placed 0, which has no latent")
	}
	if _, ok := inv(-2); ok {
		t.Error("lognormal inverse placed a negative value, which has no latent")
	}

	expo := FieldSpec{Name: "e", Distribution: DistExponential,
		Params: map[string]any{"lambda": 1.0}}
	inv, err = latentFor(expo, 1, 1)
	if err != nil {
		t.Fatalf("latentFor exponential: %v", err)
	}
	if _, ok := inv(-0.5); ok {
		t.Error("exponential inverse placed a negative value, which has no latent")
	}
}

// TestPhiInv_ClipsRatherThanReturningInfinity pins the clip on the
// probit's tails. An infinite latent from one saturated row would poison
// the Gram matrix of the whole refit it appears in, which is a much
// worse outcome than that row being placed a few thousandths off.
func TestPhiInv_ClipsRatherThanReturningInfinity(t *testing.T) {
	for _, p := range []float64{0, 1, -1, 2, math.SmallestNonzeroFloat64} {
		got := phiInv(p)
		if math.IsInf(got, 0) || math.IsNaN(got) {
			t.Errorf("phiInv(%v) = %v, want a finite clipped value", p, got)
		}
	}
	if phiInv(0.5) != 0 {
		t.Errorf("phiInv(0.5) = %v, want 0", phiInv(0.5))
	}
}
