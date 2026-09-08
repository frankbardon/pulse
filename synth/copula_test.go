package synth_test

import (
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// TestSynth_CopulaPreservesLognormalMarginal is E7-S1's own fidelity-gate
// assertion: correlating a normal field against a LOGNORMAL field via
// Spec.Correlations must (a) realize the requested Pearson correlation
// within the established tolerance (0.03, matching
// TestSynth_CorrelationReconstructionWithinTolerance), AND (b) leave the
// lognormal field's own marginal shape intact — not pulled toward
// Gaussian the way the pre-copula direct mean+std*u override did. Shape
// is checked two ways: sample skewness against the closed-form lognormal
// skewness, and empirical quantiles against the theoretical lognormal
// quantile function Q(p) = exp(mu + sigma*Φ⁻¹(p)), computed here via
// math.Erfinv — independent of synth/copula.go's own phi (which uses
// math.Erf, the forward direction) — so this test cannot pass merely by
// agreeing with the implementation under test.
func TestSynth_CopulaPreservesLognormalMarginal(t *testing.T) {
	const targetRho = 0.8
	const tolerance = 0.03
	// sigma is kept modest (0.25, not e.g. 1.0) deliberately: the
	// Gaussian copula targets Spearman (rank) correlation exactly, but
	// the REALIZED Pearson correlation between a normal field and a
	// lognormal field sharing a copula at rank-rho is attenuated by a
	// factor of sigma/sqrt(exp(sigma^2)-1) — a well-known effect for
	// lognormal marginals, not a bug in this construction (it's a
	// textbook consequence of Pearson correlation not being
	// copula-invariant the way Spearman's is). That factor -> 1 as
	// sigma -> 0 and is comfortably inside the 0.03 tolerance at
	// sigma=0.25 (attenuates rho=0.8 to ~0.7875, a 0.0125 gap) while
	// sigma is still large enough to leave the lognormal field clearly
	// non-Gaussian (theoretical skewness ~0.78, checked below).
	const mu, sigma = 0.0, 0.25

	spec := &synth.Spec{
		RowCount: 20000,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistLogNormal,
				Params: map[string]any{"mu": mu, "sigma": sigma}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: targetRho}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")

	rho := pearsonOf(a, b)
	if math.Abs(rho-targetRho) > tolerance {
		t.Fatalf("reconstructed rho = %.4f, want within %.2f of target %.2f", rho, tolerance, targetRho)
	}

	// (a) sample skewness must land near the closed-form lognormal
	// skewness, and clearly away from 0 — the Gaussian value the old
	// direct-override construction (row[name] = mean + std*u) would
	// have produced for every correlated field regardless of its own
	// declared distribution.
	wantSkew := (math.Exp(sigma*sigma) + 2) * math.Sqrt(math.Exp(sigma*sigma)-1)
	gotSkew := sampleSkewness(b)
	if math.Abs(gotSkew-wantSkew) > 0.3 {
		t.Errorf("lognormal skewness = %.4f, want within 0.3 of theoretical %.4f (marginal shape must not be pulled toward Gaussian)",
			gotSkew, wantSkew)
	}
	if gotSkew < 0.5 {
		t.Errorf("lognormal skewness = %.4f, want clearly non-Gaussian (>0.5) — marginal shape was lost", gotSkew)
	}

	// (b) empirical quantiles must track the theoretical lognormal
	// quantile function.
	sorted := append([]float64(nil), b...)
	sort.Float64s(sorted)
	for _, p := range []float64{0.1, 0.25, 0.5, 0.75, 0.9} {
		want := math.Exp(mu + sigma*normInv(p))
		got := empiricalQuantile(sorted, p)
		if relErr := math.Abs(got-want) / want; relErr > 0.05 {
			t.Errorf("empirical quantile at p=%.2f = %.4f, theoretical lognormal quantile = %.4f (rel err %.3f > 0.05)",
				p, got, want, relErr)
		}
	}
}

// TestSynth_CopulaNormalOnNormalIsExact confirms the acceptance bar
// stated algebraically in E7-S1: for the normal-on-normal case,
// Φ⁻¹(p_i) == u_i exactly by construction (p_i was built AS Φ(u_i)), so
// the copula's normal quantile function Q(p) = mean + std*Φ⁻¹(p) reduces
// to precisely the pre-copula formula mean_i + std_i*u_i — the same
// fixture and tolerance as TestSynth_CorrelationReconstructionWithinTolerance,
// kept as a second, differently-seeded instance of the same anchor
// rather than a restatement of it.
func TestSynth_CopulaNormalOnNormalIsExact(t *testing.T) {
	const targetRho = 0.8
	const tolerance = 0.03

	spec := &synth.Spec{
		RowCount: 20000,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 15.0}},
			{Name: "b", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
		},
		Correlations: []synth.CorrelationSpec{{A: "a", B: "b", Correlation: targetRho}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	a := readF64Field(t, data, "a")
	b := readF64Field(t, data, "b")
	rho := pearsonOf(a, b)
	if math.Abs(rho-targetRho) > tolerance {
		t.Fatalf("reconstructed rho = %.4f, want within %.2f of target %.2f", rho, tolerance, targetRho)
	}
}

// normInv returns Φ⁻¹(p) via math.Erfinv — independent of
// synth/copula.go's own phi (which uses math.Erf, the forward
// direction) — so tests using this cannot pass merely by agreeing with
// the implementation under test.
func normInv(p float64) float64 {
	return math.Sqrt2 * math.Erfinv(2*p-1)
}

func empiricalQuantile(sorted []float64, p float64) float64 {
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

func sampleSkewness(x []float64) float64 {
	n := float64(len(x))
	mean := 0.0
	for _, v := range x {
		mean += v
	}
	mean /= n
	var m2, m3 float64
	for _, v := range x {
		d := v - mean
		m2 += d * d
		m3 += d * d * d
	}
	m2 /= n
	m3 /= n
	return m3 / math.Pow(m2, 1.5)
}
