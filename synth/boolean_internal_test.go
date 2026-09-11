package synth

import (
	"math"
	"testing"

	mrand "math/rand/v2"
)

// TestToBool_ThresholdsAtHalf pins the writer's reduction of a drawn
// value to the single bit a packed_bool field stores.
//
// The historical threshold was "non-zero", and on a continuous draw that
// is not a rounding choice — it is a near-total collapse. A value clamped
// into [0, 1] is exactly 0 only when the underlying draw fell BELOW zero,
// so every other value, 0.001 included, read as true; a 20%-prevalence
// boolean generated at 69.1%. Rounding to nearest is both the correct
// reduction and the one every other integer arm in the writer already
// uses.
func TestToBool_ThresholdsAtHalf(t *testing.T) {
	cases := []struct {
		in   float64
		want bool
	}{
		{0, false},
		{0.001, false}, // the value the old threshold called true
		{0.25, false},
		{0.49999, false},
		{0.5, true}, // round-half-up, matching Floor(f+0.5) in the u* arms
		{0.75, true},
		{1, true},
		{-3, false},
		{7, true},
	}
	for _, tc := range cases {
		if got := toBool(tc.in); got != tc.want {
			t.Errorf("toBool(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	// The non-float arms are untouched: a real bool passes through, and
	// an int keeps its non-zero reading (there is no fractional int to
	// round, so a threshold would only lose the 1-vs-0 distinction the
	// callers rely on).
	if !toBool(true) || toBool(false) {
		t.Error("bool arm must pass through unchanged")
	}
	if !toBool(1) || toBool(0) {
		t.Error("int arm must keep its non-zero reading")
	}
}

// TestConditionalNumericDraw_BernoulliUsesPrevalence pins that the
// conditional arm draws a DIFFERENT thing for a boolean target rather
// than a rounded version of the same thing. The cell's Mean is a
// prevalence, mom.std is unused, and the output is exactly 0 or 1 — never
// an intermediate the writer would then have to threshold.
func TestConditionalNumericDraw_BernoulliUsesPrevalence(t *testing.T) {
	const n = 40000
	rng := mrand.New(mrand.NewPCG(1, 2))
	mom := categoryMoments{mean: 0.05, std: 999} // std deliberately absurd

	ones := 0
	for i := 0; i < n; i++ {
		v := conditionalNumericDraw(rng, mom, true)
		if v != 0 && v != 1 {
			t.Fatalf("draw %v is neither 0 nor 1; a boolean cell must not emit an intermediate value", v)
		}
		if v == 1 {
			ones++
		}
	}
	got := float64(ones) / n
	se := math.Sqrt(0.05 * 0.95 / n)
	if math.Abs(got-0.05) > 4*se {
		t.Errorf("prevalence %.4f, want 0.05 +/- %.4f — and mom.std must not influence it", got, 4*se)
	}

	// The continuous arm is retained and still continuous, so the flag is
	// a real switch rather than a no-op the compiler could fold away.
	rng2 := mrand.New(mrand.NewPCG(1, 2))
	sawIntermediate := false
	for i := 0; i < 100; i++ {
		v := conditionalNumericDraw(rng2, categoryMoments{mean: 0.5, std: 0.2}, false)
		if v != 0 && v != 1 {
			sawIntermediate = true
			break
		}
	}
	if !sawIntermediate {
		t.Error("the unflagged arm must still draw continuously")
	}
}

// distributionLatentClass is how a distribution relates to the two
// switches fieldMoments and latentFor implement.
type distributionLatentClass int

const (
	// classRefused: fieldMoments does not admit it at all, so it can
	// never reach a compiled model drawer or a copula participant.
	classRefused distributionLatentClass = iota
	// classRoundTrip: admitted, with a strictly monotone Q that latentFor
	// inverts exactly.
	classRoundTrip
	// classStepQuantile: admitted, but Q is a step function, so a
	// generated value does not identify the latent that produced it and
	// latentFor refuses. See latentInvertible.
	classStepQuantile
)

// TestLatentFor_EveryDistributionIsClassified is the gate that forces a
// decision about a NEW distribution instead of letting it acquire one by
// default.
//
// The two switches — fieldMoments (what may be a modelled target or a
// copula participant) and latentFor (what the fidelity report can place
// back on the latent scale) held identical sets until a boolean marginal
// was admitted, and the round-trip gate beside this one encoded that
// identity as an assumption. It is now a three-way partition, and every
// distribution in AllDistributions() must be named in it: a new kind with
// no entry fails here rather than silently landing in whichever arm its
// default behaviour happens to match.
//
// That default-arm failure mode is not hypothetical in this package — a
// `default:` in modelPredictorKind mapped a whole predictor kind to the
// wrong one and disabled roughly 80% of a shipped feature with every test
// green.
func TestLatentFor_EveryDistributionIsClassified(t *testing.T) {
	// params supplies a minimally valid declaration per distribution.
	// Only the admitted ones need usable values; the refused ones are
	// probed for refusal, which happens before any param is read.
	classes := map[string]distributionLatentClass{
		DistNormal:              classRoundTrip,
		DistUniform:             classRoundTrip,
		DistLogNormal:           classRoundTrip,
		DistExponential:         classRoundTrip,
		DistMixture:             classRoundTrip,
		DistBernoulli:           classStepQuantile,
		DistDiscrete:            classStepQuantile,
		DistConstant:            classRefused,
		DistMonotonicFrom:       classRefused,
		DistPareto:              classRefused,
		DistPoisson:             classRefused,
		DistRegex:               classRefused,
		DistSetBernoulli:        classRefused,
		DistUniformDate:         classRefused,
		DistWeightedCategorical: classRefused,
	}
	params := map[string]map[string]any{
		DistNormal:      {"mean": 10.0, "std": 2.0},
		DistUniform:     {"min": 0.0, "max": 4.0},
		DistLogNormal:   {"mu": 1.0, "sigma": 0.5},
		DistExponential: {"lambda": 0.5},
		DistMixture: {
			"means":   []any{10.0, 40.0},
			"stds":    []any{3.0, 8.0},
			"weights": []any{0.6, 0.4},
		},
		DistBernoulli: {"p": 0.3},
		DistDiscrete: {
			"values":  []any{1.0, 2.0, 3.0},
			"weights": []any{5.0, 3.0, 2.0},
		},
	}

	all := AllDistributions()
	if len(all) != len(classes) {
		t.Fatalf("AllDistributions() has %d entries but the classification table has %d — "+
			"a new distribution must be classified as refused, round-trip or step-quantile here",
			len(all), len(classes))
	}

	for _, name := range all {
		class, ok := classes[name]
		if !ok {
			t.Errorf("distribution %q is unclassified; add it to the table above", name)
			continue
		}
		fs := FieldSpec{Name: "f", Distribution: name, Params: params[name]}
		mean, std, _, _, _, momErr := fieldMoments(fs)

		switch class {
		case classRefused:
			if momErr == nil {
				t.Errorf("%q: fieldMoments admitted a distribution classified as refused", name)
			}
			if _, err := latentFor(fs, mean, std); err == nil {
				t.Errorf("%q: latentFor accepted a distribution fieldMoments refuses", name)
			}
		case classRoundTrip:
			if momErr != nil {
				t.Errorf("%q: fieldMoments refused a distribution classified as round-trip: %v", name, momErr)
				continue
			}
			if _, err := quantileFor(fs, mean, std); err != nil {
				t.Errorf("%q: quantileFor refused it: %v", name, err)
			}
			if _, err := latentFor(fs, mean, std); err != nil {
				t.Errorf("%q: latentFor refused a round-trip distribution: %v", name, err)
			}
			if !latentInvertible(name) {
				t.Errorf("%q: latentInvertible says no for a round-trip distribution", name)
			}
		case classStepQuantile:
			if momErr != nil {
				t.Errorf("%q: fieldMoments refused it, so it can never be a modelled target: %v", name, momErr)
				continue
			}
			if _, err := quantileFor(fs, mean, std); err != nil {
				t.Errorf("%q: quantileFor must supply the step Q: %v", name, err)
			}
			if latentInvertible(name) {
				t.Errorf("%q: latentInvertible says yes for a step quantile", name)
			}
			if _, err := latentFor(fs, mean, std); err == nil {
				t.Fatalf("%q: latentFor placed a value a step quantile cannot identify — "+
					"a fabricated latent reports a false attenuation for a correct generation path", name)
			}
		}
	}
}

// TestFieldMoments_Bernoulli pins the closed-form moments and the
// deliberate absence of a floor on std. The degeneracy at p in {0, 1} is
// real and buildModelDrawers is the one place that decides what to do
// about it; flooring it here would hide a constant column behind a
// plausible-looking model.
func TestFieldMoments_Bernoulli(t *testing.T) {
	for _, tc := range []struct{ p, mean, std float64 }{
		{0.5, 0.5, 0.5},
		{0.2, 0.2, 0.4},
		{0.0, 0.0, 0.0},
		{1.0, 1.0, 0.0},
	} {
		fs := FieldSpec{Name: "b", Distribution: DistBernoulli, Params: map[string]any{"p": tc.p}}
		mean, std, _, _, hasClamp, err := fieldMoments(fs)
		if err != nil {
			t.Fatalf("p=%v: %v", tc.p, err)
		}
		if math.Abs(mean-tc.mean) > 1e-12 || math.Abs(std-tc.std) > 1e-12 {
			t.Errorf("p=%v: got (mean %v, std %v), want (%v, %v)", tc.p, mean, std, tc.mean, tc.std)
		}
		if hasClamp {
			t.Errorf("p=%v: bernoulli declared a clamp; Q emits only 0 or 1 so there is nothing to clamp", tc.p)
		}
	}
	// An out-of-range p is a spec error, not a value to silently clip.
	fs := FieldSpec{Name: "b", Distribution: DistBernoulli, Params: map[string]any{"p": 1.5}}
	if _, _, _, _, _, err := fieldMoments(fs); err == nil {
		t.Error("fieldMoments accepted p = 1.5")
	}
}
