package synth

import "testing"

// TestFieldMoments_RefusesUnsupportedDistributions confirms
// fieldMoments' refusal set: poisson, bernoulli and pareto still refuse
// with an error rather than being approximated.
//
// DistMixture LEFT this list at E4-S1 — it gained exact moments and a
// numerically inverted quantile so a --fit-shape field could carry a
// linear model — and is asserted in the supported set below instead.
// The refusal set is what keeps fieldMoments' "exact or nothing" rule
// honest; the widening was allowed only because a mixture meets it.
func TestFieldMoments_RefusesUnsupportedDistributions(t *testing.T) {
	for _, dist := range []string{DistPoisson, DistBernoulli, DistPareto} {
		t.Run(dist, func(t *testing.T) {
			fs := FieldSpec{Name: "b", Type: "f64", Distribution: dist}
			if _, _, _, _, _, err := fieldMoments(fs); err == nil {
				t.Fatalf("distribution %q: expected fieldMoments refusal, got success", dist)
			}
		})
	}
}

// TestQuantileFor_AcceptsExactlyFieldMomentsSupportedDistributions
// confirms quantileFor's supported set matches fieldMoments' exactly:
// the five distributions fieldMoments accepts each get a working
// quantile function, and an unsupported distribution refuses the same
// way fieldMoments does. A MALFORMED mixture still refuses on both
// halves — the widening admitted the distribution, not bad params.
func TestQuantileFor_AcceptsExactlyFieldMomentsSupportedDistributions(t *testing.T) {
	supported := []FieldSpec{
		{Name: "n", Distribution: DistNormal, Params: map[string]any{"mean": 1.0, "std": 2.0}},
		{Name: "u", Distribution: DistUniform, Params: map[string]any{"min": 0.0, "max": 1.0}},
		{Name: "l", Distribution: DistLogNormal, Params: map[string]any{"mu": 0.0, "sigma": 1.0}},
		{Name: "e", Distribution: DistExponential, Params: map[string]any{"lambda": 1.0}},
		{Name: "m", Distribution: DistMixture, Params: map[string]any{
			"means": []any{-5.0, 5.0}, "stds": []any{1.0, 1.0}, "weights": []any{0.5, 0.5},
		}},
	}
	for _, fs := range supported {
		t.Run(fs.Distribution, func(t *testing.T) {
			mean, std, _, _, _, err := fieldMoments(fs)
			if err != nil {
				t.Fatalf("fieldMoments: %v", err)
			}
			if _, err := quantileFor(fs, mean, std); err != nil {
				t.Fatalf("quantileFor: %v", err)
			}
		})
	}

	unsupported := FieldSpec{Name: "b", Distribution: DistPoisson}
	if _, err := quantileFor(unsupported, 0, 1); err == nil {
		t.Fatalf("quantileFor: expected refusal for unsupported distribution %q, got success", DistPoisson)
	}

	malformed := FieldSpec{Name: "m", Distribution: DistMixture,
		Params: map[string]any{"means": []any{1.0}, "stds": []any{1.0}}}
	if _, _, _, _, _, err := fieldMoments(malformed); err == nil {
		t.Fatal("fieldMoments: expected refusal for a one-component mixture, got success")
	}
	if _, err := quantileFor(malformed, 0, 1); err == nil {
		t.Fatal("quantileFor: expected refusal for a one-component mixture, got success")
	}
}

// TestQuantileFor_NormalReducesExactlyToMeanPlusStdTimesU is E7-S1's
// algebraic correctness anchor: Q_normal(p) = mean + std*Φ⁻¹(p), and
// Φ⁻¹(p) == u exactly by construction whenever p was built as Φ(u) — so
// the copula's normal branch must reduce to precisely the pre-copula
// formula mean + std*u, byte-identical, not merely "close".
func TestQuantileFor_NormalReducesExactlyToMeanPlusStdTimesU(t *testing.T) {
	fs := FieldSpec{Name: "n", Distribution: DistNormal, Params: map[string]any{"mean": 5.0, "std": 2.0}}
	mean, std, _, _, _, err := fieldMoments(fs)
	if err != nil {
		t.Fatalf("fieldMoments: %v", err)
	}
	q, err := quantileFor(fs, mean, std)
	if err != nil {
		t.Fatalf("quantileFor: %v", err)
	}
	const u = 0.37
	got := q(u, phi(u))
	want := mean + std*u
	if got != want {
		t.Fatalf("Q(u, phi(u)) = %v, want exactly %v (mean + std*u)", got, want)
	}
}

// TestPhi_KnownValues sanity-checks phi (the standard normal CDF, via
// math.Erf) against textbook values.
func TestPhi_KnownValues(t *testing.T) {
	cases := []struct {
		x, want float64
		tol     float64
	}{
		{0, 0.5, 1e-9},
		{1.959963985, 0.975, 1e-6},
		{-1.959963985, 0.025, 1e-6},
	}
	for _, c := range cases {
		got := phi(c.x)
		if diff := got - c.want; diff > c.tol || diff < -c.tol {
			t.Errorf("phi(%v) = %v, want %v (tol %v)", c.x, got, c.want, c.tol)
		}
	}
}
