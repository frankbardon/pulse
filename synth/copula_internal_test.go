package synth

import "testing"

// TestFieldMoments_RefusesUnsupportedDistributions confirms fieldMoments'
// refusal set is unchanged by the E7-S1 copula rewrite: poisson,
// bernoulli, pareto, and mixture still refuse to participate in a
// correlation with an error — only normal/uniform/lognormal/exponential
// ever reach quantileFor.
func TestFieldMoments_RefusesUnsupportedDistributions(t *testing.T) {
	for _, dist := range []string{DistPoisson, DistBernoulli, DistPareto, DistMixture} {
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
// the four distributions fieldMoments accepts each get a working
// quantile function, and an unsupported distribution refuses the same
// way fieldMoments does.
func TestQuantileFor_AcceptsExactlyFieldMomentsSupportedDistributions(t *testing.T) {
	supported := []FieldSpec{
		{Name: "n", Distribution: DistNormal, Params: map[string]any{"mean": 1.0, "std": 2.0}},
		{Name: "u", Distribution: DistUniform, Params: map[string]any{"min": 0.0, "max": 1.0}},
		{Name: "l", Distribution: DistLogNormal, Params: map[string]any{"mu": 0.0, "sigma": 1.0}},
		{Name: "e", Distribution: DistExponential, Params: map[string]any{"lambda": 1.0}},
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
