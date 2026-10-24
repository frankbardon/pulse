package synth

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/internal/detmath"
)

// The generation draws and the copula's marginal transforms evaluate
// exp / expm1 / log / pow / erf / erfinv through internal/detmath, whose
// bits are the same on every architecture and CPU, never through math.*,
// whose are not. Each check below sweeps enough inputs that the stdlib
// kernel disagrees with detmath on some of them on a contracting machine
// (arm64: Exp ~5e-4, Expm1 ~1.5e-3, Log ~2e-3, Pow ~4e-2, Erf ~4e-2,
// Erfinv ~4e-1 of inputs), so a revert of any one site to math.* fails
// here locally — the same falsification shape as
// TestShapeFit_RunsOnDetmath. On amd64, where several stdlib kernels are
// the uncontracted pure-Go source, a revert may pass; CI's linux/amd64
// is not where this gate bites, and it does not need to be.

const detmathSweep = 200_000

func sameBits(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

func mustSampler(t *testing.T, fs FieldSpec) sampler {
	t.Helper()
	s, err := buildSampler(fs)
	if err != nil {
		t.Fatalf("buildSampler(%s): %v", fs.Distribution, err)
	}
	return s
}

// TestSynthGeneration_DrawsRunOnDetmath: the lognormal and pareto draws
// and the poisson threshold. A twin RNG on the same seed reproduces each
// draw's underlying variate, so the expected value is the detmath formula
// over the identical input.
func TestSynthGeneration_DrawsRunOnDetmath(t *testing.T) {
	t.Run("lognormal", func(t *testing.T) {
		const mu, sigma = 0.3, 0.7
		s := mustSampler(t, FieldSpec{Name: "x", Type: "f64", Distribution: DistLogNormal,
			Params: map[string]any{"mu": mu, "sigma": sigma}})
		rng, twin := rand.New(rand.NewPCG(5, 6)), rand.New(rand.NewPCG(5, 6))
		for i := range detmathSweep {
			got, _ := s.next(rng)
			want := detmath.Exp(mu + float64(detmath.NormFloat64(twin)*sigma))
			if !sameBits(got.(float64), want) {
				t.Fatalf("draw %d: lognormal = %x, want detmath's %x", i, math.Float64bits(got.(float64)), math.Float64bits(want))
			}
		}
	})
	t.Run("pareto", func(t *testing.T) {
		const xm, alpha = 1.3, 2.1
		s := mustSampler(t, FieldSpec{Name: "x", Type: "f64", Distribution: DistPareto,
			Params: map[string]any{"xm": xm, "alpha": alpha}})
		rng, twin := rand.New(rand.NewPCG(7, 8)), rand.New(rand.NewPCG(7, 8))
		for i := range detmathSweep {
			got, _ := s.next(rng)
			want := xm / detmath.Pow(1-float64(twin.Float64()), 1.0/alpha)
			if !sameBits(got.(float64), want) {
				t.Fatalf("draw %d: pareto = %x, want detmath's %x", i, math.Float64bits(got.(float64)), math.Float64bits(want))
			}
		}
	})
	t.Run("poisson", func(t *testing.T) {
		r := rand.New(rand.NewPCG(9, 10))
		for range detmathSweep {
			lambda := 1e-3 + float64(r.Float64()*29.99)
			s := mustSampler(t, FieldSpec{Name: "x", Type: "u16", Distribution: DistPoisson,
				Params: map[string]any{"lambda": lambda}}).(*poissonSampler)
			if want := detmath.Exp(-lambda); !sameBits(s.l, want) {
				t.Fatalf("lambda %v: threshold e^-lambda = %x, want detmath's %x", lambda, math.Float64bits(s.l), math.Float64bits(want))
			}
		}
	})
}

// TestSynthCopula_MarginalsRunOnDetmath: Φ, Φ⁻¹, the lognormal and
// exponential quantile and latent arms, and the lognormal moments every
// copula, model and vector draw standardises by.
func TestSynthCopula_MarginalsRunOnDetmath(t *testing.T) {
	const mu, sigma, lambda = 0.3, 0.7, 1.7
	logn := FieldSpec{Name: "x", Type: "f64", Distribution: DistLogNormal, Params: map[string]any{"mu": mu, "sigma": sigma}}
	expo := FieldSpec{Name: "y", Type: "f64", Distribution: DistExponential, Params: map[string]any{"lambda": lambda}}
	qLog, err := quantileFor(logn, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	qExp, err := quantileFor(expo, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	lLog, err := latentFor(logn, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	lExp, err := latentFor(expo, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(11, 12))
	for range detmathSweep {
		u := float64(r.Float64()*12) - 6 // a latent normal
		p := r.Float64()                 // a copula probability
		v := float64(r.Float64()*8) + 1e-6
		checks := []struct {
			what      string
			got, want float64
		}{
			{"phi", phi(u), 0.5 * (1 + detmath.Erf(u/math.Sqrt2))},
			{"phiInv", phiInv(p), math.Sqrt2 * detmath.Erfinv(float64(2*min(max(p, 1e-12), 1-1e-12))-1)},
			{"lognormal quantile", qLog(u, p), detmath.Exp(mu + float64(sigma*u))},
			{"exponential quantile", qExp(u, p), -detmath.Log(1-p) / lambda},
			{"lognormal latent", mustLatent(lLog(v)), (detmath.Log(v) - mu) / sigma},
			{"exponential latent", mustLatent(lExp(v)), phiInv(-detmath.Expm1(-lambda * v))},
		}
		for _, c := range checks {
			if !sameBits(c.got, c.want) {
				t.Fatalf("%s (u=%v p=%v v=%v) = %x, want detmath's %x", c.what, u, p, v, math.Float64bits(c.got), math.Float64bits(c.want))
			}
		}
	}
	// fieldMoments' lognormal arm: e^(mu+σ²/2) and the variance factor.
	for range detmathSweep / 10 {
		m := float64(r.Float64()*4) - 2
		s := 0.05 + float64(r.Float64()*1.5)
		mean, std, _, _, _, err := fieldMoments(FieldSpec{Name: "x", Type: "f64", Distribution: DistLogNormal,
			Params: map[string]any{"mu": m, "sigma": s}})
		if err != nil {
			t.Fatal(err)
		}
		sq := float64(s * s)
		wantMean := detmath.Exp(m + float64(sq/2))
		wantStd := math.Sqrt(float64((detmath.Exp(sq) - 1) * detmath.Exp(float64(2*m)+sq)))
		if !sameBits(mean, wantMean) || !sameBits(std, wantStd) {
			t.Fatalf("lognormal(mu=%v, sigma=%v) moments = (%x, %x), want detmath's (%x, %x)",
				m, s, math.Float64bits(mean), math.Float64bits(std), math.Float64bits(wantMean), math.Float64bits(wantStd))
		}
	}
}

func mustLatent(v float64, ok bool) float64 {
	if !ok {
		return math.NaN()
	}
	return v
}
