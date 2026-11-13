//go:build heavy

package regmmm

import (
	"context"
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
)

// bayes_sbc_heavy_test.go is REG_MMM_BAYES's simulation-based
// calibration harness (U64 E4-S2, #620; Talts et al. 2018). Each
// replicate draws a true θ from the model's own priors, simulates an
// outcome from the model's likelihood at that θ over a fixed design,
// fits it with the operator's sampler path (MAP start, then NUTS) and
// records the rank of the true value among thinned posterior draws. If
// the density, its transforms and Jacobians, the start and the sampler
// are right, every parameter's rank is uniform on 0..sbcKept, whatever
// the prior; a wrong Jacobian, a biased sampler or too little mixing
// bends the histogram (∩, ∪ or a slope).
//
// The priors are REG_MMM_BAYES's defaults (ROI mode: decay Beta(1, 1),
// k TruncNormal(1, 1, 0.02, 10), s LogNormal(0.3, 0.5), ROI
// LogNormal(0.2, 0.9), intercept Normal(0, 2), control Normal(0, 1), σ
// HalfNormal(1)), and per non-geometric adstock kernel the same with
// that kernel's default parameter priors (defaultKernelPriors: delayed's
// decay Beta(1, 1) and peak TruncNormal(1, 2, 0, max_lag); a Weibull
// kernel's shape LogNormal(0, 0.5) and scale LogNormal(1, 0.75)) — every
// kernel is calibrated on its own — and per kernel × the logistic and
// Michaelis–Menten curves the same with that curve's default priors (λ
// LogNormal(0, 1); Michaelis–Menten's k Hill's TruncNormal(1, 1, 0.02,
// 10)), every cell of the kernel × curve matrix. They are stated on the max-scaled outcome, so in a fit
// they scale with the data (max|y|), an empirical-Bayes choice no
// generative prior reproduces; SBC is then run with that scale fixed at
// 1 (sbcModel), which calibrates everything the scale does not touch —
// the whole posterior computation. The spend and control design is
// fixed across replicates (SBC conditions on covariates), so the spend
// and control scalings are constants, as in a fit.
//
// Deterministic and seeded: replicate r draws from PCG(sbcSeed, r) with
// uniform and detmath variates (identical bits on every architecture)
// and samples with seed sbcSeed + r, so the ranks — and the verdict —
// are the same on every run, worker count and machine. The verdict per
// parameter is a chi-square test of the binned ranks against uniform,
// failed below sbcAlpha after a Bonferroni split over the parameters.
const (
	sbcSeed       = 20261102
	sbcReplicates = 200
	sbcWeeks      = 52
	sbcMaxLag     = 4
	sbcWarmup     = 400
	sbcThin       = 5
	sbcKept       = 99 // ranks 0..99: 100 values, sbcBins bins of 10
	sbcBins       = 10
	sbcAlpha      = 0.001
)

// sbcDesign is the fixed design: one channel's weekly spend (bursty,
// dark weeks) and one control.
func sbcDesign() (spend, controls [][]float64) {
	rng := rand.New(rand.NewPCG(sbcSeed, 0x736263))
	x, z := make([]float64, sbcWeeks), make([]float64, sbcWeeks)
	for w := range x {
		if rng.Float64() < 0.7 {
			x[w] = float64(rng.Float64()*0.9) + 0.1
		}
		z[w] = float64(2*rng.Float64()) - 1
	}
	return [][]float64{x}, [][]float64{z}
}

// sbcModel is newBayesModel over y under adstock kernel k and curve c
// with the outcome scale fixed at 1: the scaled outcome is y itself and
// an ROI channel's β conversion uses the raw spend total.
func sbcModel(t *testing.T, k adstockKernel, c saturationCurve, y []float64, spend, controls [][]float64) *bayesModel {
	t.Helper()
	pr := defaultBayesPriors(len(spend))
	if k.Name() != "geometric" {
		pr = defaultKernelPriors(k, len(spend), sbcMaxLag)
	}
	pr.Curve = c
	m, err := newBayesModel(y, spend, controls, sbcMaxLag, pr)
	if err != nil {
		t.Fatal(err)
	}
	m.yScale = 1
	copy(m.ys, y)
	for c := range m.spendShare {
		m.spendShare[c] = m.spendTotal[c]
	}
	return m
}

// draw is one variate from p.
func (p prior) draw(rng *rand.Rand) float64 {
	switch p.Family {
	case familyNormal:
		return p.A + float64(p.B*detmath.NormFloat64(rng))
	case familyHalfNormal:
		return math.Abs(float64(p.B * detmath.NormFloat64(rng)))
	case familyLogNormal:
		return detmath.Exp(p.A + float64(p.B*detmath.NormFloat64(rng)))
	case familyBeta:
		if p.A == 1 && p.B == 1 {
			return rng.Float64()
		}
	case familyTruncNormal:
		for {
			if v := p.A + float64(p.B*detmath.NormFloat64(rng)); v >= p.Lo && v <= p.Hi {
				return v
			}
		}
	}
	panic("sbc: no sampler for prior " + p.Family)
}

// sbcLift is a calibration run's lift test: a change of frac × the
// window spend over rows from..to, measured with standard error se.
type sbcLift struct {
	from, to int
	frac, se float64
}

// sbcReplicate draws a truth, simulates, fits and returns each θ
// coordinate's rank (0..sbcKept) with the run's divergence count. With
// a lift, the measured lift is simulated too — the model's Δ at the
// truth plus se·ε — and the fit carries the test.
func sbcReplicate(t *testing.T, r int, spend, controls [][]float64, ref *bayesModel, lift *sbcLift) ([]int, int) {
	rng := rand.New(rand.NewPCG(sbcSeed, uint64(r)))
	dim := ref.Dim()
	truth := make([]float64, dim) // constrained, on the scaled data
	for i := range truth {
		truth[i] = ref.priors[i].draw(rng)
	}
	// The outcome at the truth: the model's own mean (yScale 1) plus
	// σ·ε.
	theta := ref.toTheta(truth)
	mu := ref.predict(theta, spend, controls, 0)
	y := make([]float64, sbcWeeks)
	for w := range y {
		y[w] = mu[w] + float64(truth[ref.iSigma()]*detmath.NormFloat64(rng))
	}
	m := sbcModel(t, ref.kernel, ref.sat, y, spend, controls)
	if lift != nil {
		win := 0.0
		for w := lift.from; w <= lift.to; w++ {
			win += spend[0][w]
		}
		lt, err := m.newLiftTest(0, lift.from, lift.to, float64(lift.frac*win), 0, lift.se)
		if err != nil {
			t.Fatal(err)
		}
		m.lifts = []liftTest{lt}
		m.lifts[0].lift = m.liftPrediction(theta, 0) + float64(lift.se*detmath.NormFloat64(rng))
	}
	init, _ := m.mapStart(mapMaxIter)
	res, err := mcmc.NUTS{TargetAccept: defaultBayesTargetAccept}.Sample(context.Background(), m, uint64(sbcSeed+r),
		mcmc.Options{Chains: 1, Warmup: sbcWarmup, Draws: sbcKept * sbcThin, Init: init})
	if err != nil {
		t.Errorf("replicate %d: %v", r, err)
		return nil, 0
	}
	ranks := make([]int, dim)
	for i := range dim {
		// The rank is transform-invariant, so it is taken on the
		// unconstrained scale the draws are stored on.
		for d := sbcThin - 1; d < len(res.Draws[0][i]); d += sbcThin {
			if res.Draws[0][i][d] < theta[i] {
				ranks[i]++
			}
		}
	}
	return ranks, res.Diagnostics.Chains[0].Divergences
}

// TestMMMBayes_SimulationBasedCalibration: on REG_MMM_BAYES's default
// priors, under every adstock kernel × saturation curve, every
// parameter's SBC rank histogram is uniform.
func TestMMMBayes_SimulationBasedCalibration(t *testing.T) {
	t.Parallel()

	for _, kernel := range kernelNames() {
		for _, curve := range curveNames() {
			name := kernel
			if curve != "hill" {
				name += "_" + curve
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				sbcKernel(t, kernelByName(kernel), curveByName(curve), nil)
			})
		}
	}
}

// TestMMMBayes_SimulationBasedCalibrationLift: with a lift test in the
// likelihood (a switch-off over eight weeks, se 0.5 on the unscaled
// outcome — informative against the default priors) every parameter's
// SBC rank histogram stays uniform, on the default cell and on a cell
// with a two-parameter kernel and a one-parameter curve — so the lift
// term's density and gradient are those of the generative model.
func TestMMMBayes_SimulationBasedCalibrationLift(t *testing.T) {
	t.Parallel()

	lift := &sbcLift{from: 20, to: 27, frac: -1, se: 0.5}
	for _, cell := range [][2]string{{"geometric", "hill"}, {"delayed", "logistic"}} {
		t.Run(cell[0]+"_"+cell[1], func(t *testing.T) {
			t.Parallel()
			sbcKernel(t, kernelByName(cell[0]), curveByName(cell[1]), lift)
		})
	}
}

// sbcKernel is one kernel × curve's calibration run, with lift's lift
// test in every replicate when non-nil.
func sbcKernel(t *testing.T, k adstockKernel, c saturationCurve, lift *sbcLift) {
	spend, controls := sbcDesign()
	// The reference model reads only the design (its outcome, all ones,
	// is unused): the priors, the control centring and the spend scale.
	ones := make([]float64, sbcWeeks)
	for i := range ones {
		ones[i] = 1
	}
	ref := sbcModel(t, k, c, ones, spend, controls)
	dim := ref.Dim()
	names := []string{"intercept", "control"}
	for _, kp := range k.Params() {
		names = append(names, kp.key)
	}
	for _, cq := range c.Params() {
		names = append(names, cq.key)
	}
	names = append(names, "roi", "sigma")
	if len(names) != dim {
		t.Fatalf("dim %d, names %v", dim, names)
	}

	ranks := make([][]int, sbcReplicates)
	divs := make([]int, sbcReplicates)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for r := range jobs {
				ranks[r], divs[r] = sbcReplicate(t, r, spend, controls, ref, lift)
			}
		})
	}
	for r := range sbcReplicates {
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	if t.Failed() {
		return
	}

	totalDiv := 0
	for _, d := range divs {
		totalDiv += d
	}
	alpha := sbcAlpha / float64(dim)
	expected := float64(sbcReplicates) / sbcBins
	for i, name := range names {
		hist := make([]int, sbcBins)
		for _, rk := range ranks {
			hist[rk[i]*sbcBins/(sbcKept+1)]++
		}
		chi := 0.0
		for _, h := range hist {
			d := float64(h) - expected
			chi += d * d / expected
		}
		p := statdist.ChiSquareSurvival(chi, sbcBins-1)
		t.Logf("%-9s ranks %v  χ²(%d) = %5.2f  p = %.3f", name, hist, sbcBins-1, chi, p)
		if !(p >= alpha) {
			t.Errorf("%s: SBC rank histogram %v is not uniform (χ² = %.2f, p = %.2g < %.2g)", name, hist, chi, p, alpha)
		}
	}
	t.Logf("%d replicates × %d kept draws (thin %d after %d warmup); %d post-warmup divergences in all", sbcReplicates, sbcKept, sbcThin, sbcWarmup, totalDiv)
}
