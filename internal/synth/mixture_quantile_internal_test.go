package synth

import (
	"math"
	"testing"
)

// mixtureFixture returns a parsed two-component mixture for the tests
// below, going through the real parse path so a params-shape change
// cannot leave these asserting against a hand-built struct the code no
// longer produces.
func mixtureFixture(t *testing.T, means, stds, weights []any) mixtureComponents {
	t.Helper()
	mc, err := parseMixtureComponents(FieldSpec{
		Name: "m", Type: "f64", Distribution: DistMixture,
		Params: map[string]any{"means": means, "stds": stds, "weights": weights},
	})
	if err != nil {
		t.Fatalf("parseMixtureComponents: %v", err)
	}
	return mc
}

// TestMixtureMoments_MatchesLawOfTotalVariance checks the analytic
// moments against the same formula written out longhand per case, so a
// refactor of the loop cannot quietly redefine what it computes. fieldMoments' contract is EXACT moments, not approximate
// ones — that is the standard on which DistMixture was admitted to its
// supported set at all — so an arithmetic slip here would quietly
// mis-standardise every modelled shape-fitted field's linear
// prediction, shifting its whole conditional structure with no other
// symptom.
func TestMixtureMoments_MatchesLawOfTotalVariance(t *testing.T) {
	cases := []struct {
		name                    string
		means, stds, weights    []any
		wantMean, wantStdApprox float64
	}{
		{
			name:  "equal weights, well separated",
			means: []any{10.0, 90.0}, stds: []any{1.5, 1.5}, weights: []any{0.5, 0.5},
			wantMean: 50, wantStdApprox: math.Sqrt(0.5*(2.25+100) + 0.5*(2.25+8100) - 2500),
		},
		{
			name:  "unequal weights",
			means: []any{0.0, 20.0}, stds: []any{2.0, 5.0}, weights: []any{3.0, 1.0},
			wantMean: 5, wantStdApprox: math.Sqrt(0.75*(4+0) + 0.25*(25+400) - 25),
		},
		{
			// Unnormalised weights must give the same answer as their
			// normalised form — the parse deliberately keeps them raw
			// for the sampler's sake, so the normalisation has to happen
			// here and be right.
			name:  "weights not summing to one",
			means: []any{-4.0, 4.0}, stds: []any{1.0, 1.0}, weights: []any{7.0, 7.0},
			wantMean: 0, wantStdApprox: math.Sqrt(1 + 16),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc := mixtureFixture(t, tc.means, tc.stds, tc.weights)
			mean, std := mc.moments()
			if math.Abs(mean-tc.wantMean) > 1e-9 {
				t.Errorf("mean = %v, want %v", mean, tc.wantMean)
			}
			if math.Abs(std-tc.wantStdApprox) > 1e-9 {
				t.Errorf("std = %v, want %v", std, tc.wantStdApprox)
			}
		})
	}
}

// TestMixtureQuantile_InvertsTheCDF is the correctness bar for the
// numeric inverse: F(Q(p)) must return p, across the flat valley
// between two well-separated modes (where the density underflows and a
// Newton step would explode) as well as inside each mode.
func TestMixtureQuantile_InvertsTheCDF(t *testing.T) {
	mc := mixtureFixture(t, []any{10.0, 90.0}, []any{1.5, 1.5}, []any{0.5, 0.5})
	lo, hi := mc.bracket()
	for _, p := range []float64{
		0.001, 0.01, 0.1, 0.25, 0.4,
		0.4999, 0.5, 0.5001, // the valley: F is flat here
		0.6, 0.75, 0.9, 0.99, 0.999,
	} {
		x := mc.quantile(p, lo, hi)
		if got := mc.cdf(x); math.Abs(got-p) > 1e-9 {
			t.Errorf("p = %v: cdf(quantile(p)) = %v, want %v (x = %v)", p, got, p, x)
		}
	}
}

// TestMixtureQuantile_IsMonotoneAndBounded pins the two structural
// properties the composed draw relies on. Monotonicity is what makes a
// positive coefficient move the drawn value UP — the direction claim
// E4-S1's acceptance rests on, given the magnitude claim is
// deliberately not made — and the bracket bound is what lets the
// bisection run a fixed number of steps with no expansion loop.
func TestMixtureQuantile_IsMonotoneAndBounded(t *testing.T) {
	mc := mixtureFixture(t, []any{10.0, 90.0}, []any{1.5, 1.5}, []any{0.3, 0.7})
	lo, hi := mc.bracket()

	prev := math.Inf(-1)
	for i := 0; i <= 1000; i++ {
		p := float64(i) / 1000
		x := mc.quantile(p, lo, hi)
		if x < prev {
			t.Fatalf("p = %v: quantile decreased (%v after %v)", p, x, prev)
		}
		if x < lo || x > hi {
			t.Fatalf("p = %v: quantile %v escaped the bracket [%v, %v]", p, x, lo, hi)
		}
		prev = x
	}

	// p outside [0,1] needs no special case — the bisection simply walks
	// to the enclosing endpoint, which is why quantile carries no guard.
	if got := mc.quantile(-1, lo, hi); math.Abs(got-lo) > 1e-6 {
		t.Errorf("quantile(-1) = %v, want the bracket floor %v", got, lo)
	}
	if got := mc.quantile(2, lo, hi); math.Abs(got-hi) > 1e-6 {
		t.Errorf("quantile(2) = %v, want the bracket ceiling %v", got, hi)
	}
}

// TestMixtureQuantile_IsBitIdenticalAcrossCalls is the determinism
// contract at its smallest scale. A fixed-count bisection is a pure
// function of its inputs by construction, and this is the assertion
// that keeps it one: an "iterate until |F(x)-p| < eps" rewrite would
// still pass every accuracy test above while making the answer depend
// on how many steps a given p happened to need.
//
// Bit equality, never a tolerance — a tolerance here would be asserting
// the opposite of the property.
func TestMixtureQuantile_IsBitIdenticalAcrossCalls(t *testing.T) {
	mc := mixtureFixture(t, []any{10.0, 90.0}, []any{1.5, 1.5}, []any{0.5, 0.5})
	lo, hi := mc.bracket()
	for i := 1; i < 200; i++ {
		p := float64(i) / 200
		first := mc.quantile(p, lo, hi)
		for rep := 0; rep < 3; rep++ {
			if got := mc.quantile(p, lo, hi); got != first {
				t.Fatalf("p = %v: repeat %d returned %v, first call returned %v", p, rep, got, first)
			}
		}
	}
}

// TestMixtureBracket_EnclosesTheWholeDistribution asserts the claim
// mixtureQuantileTailStds rests on: at the bracket ends the CDF has
// saturated at exactly 0 and exactly 1, so no p in [0,1] has its root
// outside. If this stopped holding the bisection would silently return
// a clipped answer rather than failing.
func TestMixtureBracket_EnclosesTheWholeDistribution(t *testing.T) {
	// Deliberately lopsided: a tight component beside a wide one, so the
	// bracket is driven by the widest std rather than by a symmetric fit.
	mc := mixtureFixture(t, []any{-100.0, 250.0}, []any{0.25, 40.0}, []any{0.9, 0.1})
	lo, hi := mc.bracket()
	if got := mc.cdf(lo); got != 0 {
		t.Errorf("cdf(lo=%v) = %v, want exactly 0", lo, got)
	}
	if got := mc.cdf(hi); got != 1 {
		t.Errorf("cdf(hi=%v) = %v, want exactly 1", hi, got)
	}
}
