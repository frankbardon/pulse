package mcmc_test

import (
	"context"
	"errors"
	"math"
	"runtime"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/mcmc"
)

var _ mcmc.Sampler = mcmc.HMC{}

func sampleHMC(t *testing.T, s mcmc.HMC, m mcmc.Model, seed uint64, opts mcmc.Options) mcmc.Result {
	t.Helper()
	res, err := s.Sample(context.Background(), m, seed, opts)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	return res
}

// checkTreeless asserts every chain reports no tree (TreeDepth nil,
// TreeDepthSaturated 0), one Divergent mark per draw, and Divergences
// equal to the marks.
func checkTreeless(t *testing.T, res mcmc.Result, draws int) {
	t.Helper()
	for c, st := range res.Diagnostics.Chains {
		if st.TreeDepth != nil || st.TreeDepthSaturated != 0 {
			t.Errorf("chain %d: HMC reports a tree (TreeDepth len %d, saturated %d)", c, len(st.TreeDepth), st.TreeDepthSaturated)
		}
		if len(st.Divergent) != draws {
			t.Errorf("chain %d: %d Divergent marks, want %d", c, len(st.Divergent), draws)
		}
		n := 0
		for _, dv := range st.Divergent {
			if dv {
				n++
			}
		}
		if n != st.Divergences {
			t.Errorf("chain %d: Divergences %d, Divergent marks %d", c, st.Divergences, n)
		}
	}
}

// TestHMC_StandardNormalRecovery: the zero-value HMC on a 10-dimensional
// standard normal (4 × 1000 × 1000) recovers its moments, mixes, adapts
// its step size near the acceptance target, never diverges and reports
// no tree.
func TestHMC_StandardNormalRecovery(t *testing.T) {
	t.Parallel()

	res := sampleHMC(t, mcmc.HMC{}, normal{d: 10}, 21, mcmc.Options{})
	if len(res.Draws) != mcmc.DefaultChains || len(res.Draws[0]) != 10 || len(res.Draws[0][0]) != mcmc.DefaultDraws {
		t.Fatalf("draws shape %d×%d×%d", len(res.Draws), len(res.Draws[0]), len(res.Draws[0][0]))
	}
	want := make([]float64, 10)
	for i := range want {
		want[i] = 1
	}
	checkMoments(t, res, want)
	checkTreeless(t, res, mcmc.DefaultDraws)
	for c, st := range res.Diagnostics.Chains {
		if st.Divergences != 0 {
			t.Errorf("chain %d: %d divergences", c, st.Divergences)
		}
		if a := st.MeanAcceptStat; a < mcmc.DefaultTargetAccept-0.05 || a > 0.97 {
			t.Errorf("chain %d: mean accept stat %.3f, want in [%.2f, 0.97]", c, a, mcmc.DefaultTargetAccept-0.05)
		}
		if st.StepSize <= 0 || len(st.Metric) != 10 || st.GradientEvals <= mcmc.DefaultDraws {
			t.Errorf("chain %d: stats %+v", c, st)
		}
	}
}

// TestHMC_ShiftedMeanRecovery: independent normals with known non-zero
// means and scales spanning two orders of magnitude recover their
// posterior means and variances.
func TestHMC_ShiftedMeanRecovery(t *testing.T) {
	t.Parallel()

	m := shifted{mu: []float64{3, -12.5, 0.25, 40}, sd: []float64{0.5, 2, 0.05, 5}}
	res := sampleHMC(t, mcmc.HMC{}, m, 22, mcmc.Options{})
	vars := make([]float64, len(m.sd))
	for i, s := range m.sd {
		vars[i] = s * s
	}
	checkMomentsAt(t, res, m.mu, vars)
}

// TestHMC_CorrelatedGaussianRecovery: an AR(1) Gaussian with neighbour
// correlation 0.9 recovers its marginal moments and lag-1 correlation.
// The correlation a diagonal metric cannot remove wants a longer path
// than the quarter period DefaultPathLength gives a unit-scale target.
func TestHMC_CorrelatedGaussianRecovery(t *testing.T) {
	t.Parallel()

	const d, rho = 5, 0.9
	res := sampleHMC(t, mcmc.HMC{PathLength: 3}, gaussian{d: d, prec: ar1Precision(d, rho)}, 23, mcmc.Options{})
	checkMoments(t, res, []float64{1, 1, 1, 1, 1})
	var s float64
	n := 0
	for _, ch := range res.Draws {
		for i := range ch[0] {
			s += float64(ch[1][i] * ch[2][i])
			n++
		}
	}
	if c := s / float64(n); math.Abs(c-rho) > 0.05 {
		t.Errorf("corr(x1, x2) %.3f, want %.2f", c, rho)
	}
}

// TestHMC_MetricAdaptation: the adapted inverse metric tracks the
// variances of normals whose scales span three orders of magnitude.
func TestHMC_MetricAdaptation(t *testing.T) {
	t.Parallel()

	sd := []float64{0.1, 1, 10, 100}
	res := sampleHMC(t, mcmc.HMC{}, normal{d: 4, sd: sd}, 24, mcmc.Options{Chains: 2})
	for c, st := range res.Diagnostics.Chains {
		for i, m := range st.Metric {
			if r := m / float64(sd[i]*sd[i]); r < 0.6 || r > 1.6 {
				t.Errorf("chain %d: metric[%d] = %.4g, variance %.4g (ratio %.2f)", c, i, m, sd[i]*sd[i], r)
			}
		}
	}
}

// TestHMC_FixedStepSize: a non-zero StepSize is used as given and fixes
// the trajectory at ⌈PathLength/StepSize⌉ leapfrog steps (here
// ⌈1.05/0.1⌉ = 11, where a floor would give 10), so the gradient count
// is exact: one at the start, then 11 per iteration.
func TestHMC_FixedStepSize(t *testing.T) {
	t.Parallel()

	const warmup, draws = 40, 30
	for _, tc := range []struct {
		step, path float64
		steps      int
	}{
		{0.1, 1.05, 11},
		{0.25, 1, 4},
		{0.5, 0.2, 1}, // a path shorter than one step still takes one
	} {
		res := sampleHMC(t, mcmc.HMC{StepSize: tc.step, PathLength: tc.path}, normal{d: 2}, 25,
			mcmc.Options{Chains: 2, Warmup: warmup, Draws: draws})
		for c, st := range res.Diagnostics.Chains {
			if st.StepSize != tc.step {
				t.Errorf("step %g: chain %d StepSize %g", tc.step, c, st.StepSize)
			}
			if want := 1 + (warmup+draws)*tc.steps; st.GradientEvals != want {
				t.Errorf("step %g path %g: chain %d GradientEvals %d, want %d", tc.step, tc.path, c, st.GradientEvals, want)
			}
		}
		checkTreeless(t, res, draws)
	}
}

// TestHMC_DefaultPathLength: PathLength 0 is DefaultPathLength.
func TestHMC_DefaultPathLength(t *testing.T) {
	t.Parallel()

	opts := mcmc.Options{Chains: 2, Warmup: 30, Draws: 20}
	a := drawHash(sampleHMC(t, mcmc.HMC{StepSize: 0.3}, normal{d: 2}, 26, opts))
	b := drawHash(sampleHMC(t, mcmc.HMC{StepSize: 0.3, PathLength: mcmc.DefaultPathLength}, normal{d: 2}, 26, opts))
	if a != b {
		t.Errorf("PathLength 0 and DefaultPathLength differ")
	}
	if c := drawHash(sampleHMC(t, mcmc.HMC{StepSize: 0.3, PathLength: 3}, normal{d: 2}, 26, opts)); c == a {
		t.Errorf("PathLength 3 reproduced the default's draws")
	}
}

// TestHMC_TargetAccept: with an adapted step size, a higher target gives
// a smaller step size and a higher realised acceptance statistic.
func TestHMC_TargetAccept(t *testing.T) {
	t.Parallel()

	m := gaussian{d: 5, prec: ar1Precision(5, 0.9)}
	mean := func(res mcmc.Result) (eps, acc float64) {
		for _, st := range res.Diagnostics.Chains {
			eps += st.StepSize
			acc += st.MeanAcceptStat
		}
		n := float64(len(res.Diagnostics.Chains))
		return eps / n, acc / n
	}
	opts := mcmc.Options{Chains: 2, Warmup: 500, Draws: 500}
	loEps, loAcc := mean(sampleHMC(t, mcmc.HMC{TargetAccept: 0.6}, m, 27, opts))
	hiEps, hiAcc := mean(sampleHMC(t, mcmc.HMC{TargetAccept: 0.95}, m, 27, opts))
	if !(hiEps < 0.8*loEps) {
		t.Errorf("mean step size %.4f at δ=0.95, %.4f at δ=0.6", hiEps, loEps)
	}
	if !(hiAcc > 0.9 && hiAcc > loAcc) {
		t.Errorf("mean accept stat %.3f at δ=0.95, %.3f at δ=0.6", hiAcc, loAcc)
	}
}

// TestHMC_Divergences: a fixed step size far too large for Neal's funnel
// diverges; every divergence is marked per draw, counted, and rejected
// (the chain stays put, so a divergent draw repeats its predecessor).
func TestHMC_Divergences(t *testing.T) {
	t.Parallel()

	res := sampleHMC(t, mcmc.HMC{StepSize: 1, PathLength: 3}, funnel{d: 10}, 28,
		mcmc.Options{Chains: 2, Warmup: 100, Draws: 200})
	checkTreeless(t, res, 200)
	total := 0
	for c, st := range res.Diagnostics.Chains {
		total += st.Divergences
		for i := 1; i < len(st.Divergent); i++ {
			if !st.Divergent[i] {
				continue
			}
			for j := range res.Draws[c] {
				if res.Draws[c][j][i] != res.Draws[c][j][i-1] {
					t.Fatalf("chain %d draw %d diverged but moved param %d", c, i, j)
				}
			}
		}
	}
	if total == 0 {
		t.Error("no divergence on the funnel at step size 1")
	}
}

// hmcGoldenRuns are pinned like NUTS's goldenRuns: an adapted run on the
// diverging funnel (short-warmup windows), a fixed-step correlated
// Gaussian from a supplied Init, and the full 75/25/50 schedule.
var hmcGoldenRuns = []struct {
	name  string
	model mcmc.Model
	hmc   mcmc.HMC
	seed  uint64
	opts  mcmc.Options
	hash  string
}{
	{"funnel", funnel{d: 4}, mcmc.HMC{}, 101,
		mcmc.Options{Chains: 3, Warmup: 120, Draws: 100},
		"0409e16d2cbe34b41c97eba249760bbca5a375356d3d88a70d03c4f665b7c35b"},
	{"ar1_fixed", gaussian{d: 3, prec: ar1Precision(3, 0.8)}, mcmc.HMC{StepSize: 0.2, PathLength: 1.3}, 202,
		mcmc.Options{Chains: 2, Warmup: 80, Draws: 60, Init: []float64{0.5, -1, 1.5}},
		"75bb93d53a17b0ade2508626df63ebcdd1c2ce2d8fc9fe5b9eef629786a035be"},
	{"normal_full_schedule", normal{d: 2, sd: []float64{0.5, 3}}, mcmc.HMC{TargetAccept: 0.9, PathLength: 2}, 303,
		mcmc.Options{Chains: 2, Warmup: 1000, Draws: 50},
		"96cd18afffa63ce5a25c8c8a51969e7769c864c3ff7542047c1dac130bb6b612"},
}

// TestHMC_GoldenHash pins the exact bits of each golden run. The hashes
// were computed on darwin/arm64; every architecture must reproduce them
// (TestMCMC_NoFMAContraction holds the barriers that make that so), so a
// contraction or a platform transcendental fails here on amd64 CI.
func TestHMC_GoldenHash(t *testing.T) {
	t.Parallel()

	for _, g := range hmcGoldenRuns {
		got := drawHash(sampleHMC(t, g.hmc, g.model, g.seed, g.opts))
		if got != g.hash {
			t.Errorf("%s: draw hash %s, golden %s (GOARCH %s)", g.name, got, g.hash, runtime.GOARCH)
		}
	}
}

// TestHMC_Deterministic: the same seed gives the same bits across runs,
// worker counts and GOMAXPROCS; another seed gives other bits.
func TestHMC_Deterministic(t *testing.T) {
	// serial: sets the process-wide runtime.GOMAXPROCS

	m := funnel{d: 4}
	opts := mcmc.Options{Chains: 5, Warmup: 150, Draws: 100}
	base := drawHash(sampleHMC(t, mcmc.HMC{}, m, 7, opts))
	for _, w := range []int{1, 2, 3, 5, 16} {
		if got := drawHash(sampleHMC(t, mcmc.HMC{Workers: w}, m, 7, opts)); got != base {
			t.Errorf("Workers=%d: hash %s, want %s", w, got, base)
		}
	}
	prev := runtime.GOMAXPROCS(1)
	one := drawHash(sampleHMC(t, mcmc.HMC{}, m, 7, opts))
	runtime.GOMAXPROCS(prev)
	if one != base {
		t.Errorf("GOMAXPROCS=1: hash %s, want %s", one, base)
	}
	if got := drawHash(sampleHMC(t, mcmc.HMC{}, m, 8, opts)); got == base {
		t.Error("seed 8 reproduced seed 7's draws")
	}
	res := sampleHMC(t, mcmc.HMC{}, m, 7, opts)
	for a := range res.Draws {
		for b := a + 1; b < len(res.Draws); b++ {
			if res.Draws[a][0][0] == res.Draws[b][0][0] {
				t.Errorf("chains %d and %d start their draws identically", a, b)
			}
		}
	}
}

// TestHMC_ContextCancel: a context cancelled mid-run returns ctx.Err()
// within one transition per chain; an already-cancelled context returns
// before any gradient.
func TestHMC_ContextCancel(t *testing.T) {
	// serial: wall-clock bound; CPU contention from parallel tests would make it flaky

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &cancelAfter{Model: normal{d: 3}, n: 500, cancel: cancel}
	const chains, steps = 4, 10
	start := time.Now()
	_, err := mcmc.HMC{StepSize: 0.1, PathLength: 1}.Sample(ctx, m, 1, mcmc.Options{Chains: chains, Warmup: 100000, Draws: 100000})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
	if after := m.after.Load(); after > chains*steps {
		t.Errorf("%d gradients after cancel; at most one transition per chain (%d) may finish", after, chains*steps)
	}
	if el := time.Since(start); el > 10*time.Second {
		t.Errorf("cancel took %v", el)
	}

	pre, preCancel := context.WithCancel(context.Background())
	preCancel()
	m2 := &cancelAfter{Model: normal{d: 3}, n: -1, cancel: func() {}}
	if _, err := (mcmc.HMC{}).Sample(pre, m2, 1, mcmc.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled: err %v", err)
	}
	if c := m2.calls.Load(); c != 0 {
		t.Errorf("pre-cancelled context still evaluated %d gradients", c)
	}
}

// TestHMC_InitAndConstrain: a supplied Init is where the chain starts,
// and Result.Draws holds Constrain's values of the unchanged chain.
func TestHMC_InitAndConstrain(t *testing.T) {
	t.Parallel()

	m := &firstTheta{Model: normal{d: 2}}
	sampleHMC(t, mcmc.HMC{}, m, 3, mcmc.Options{Chains: 1, Warmup: 20, Draws: 5, Init: []float64{3.25, -7.5}})
	if m.first[0] != 3.25 || m.first[1] != -7.5 {
		t.Errorf("first point %v, want Init", m.first)
	}

	opts := mcmc.Options{Chains: 2, Warmup: 50, Draws: 40}
	raw := sampleHMC(t, mcmc.HMC{}, normal{d: 2}, 5, opts)
	opts.Constrain = func(theta, out []float64) {
		for i, x := range theta {
			out[i] = float64(2*x) + 1
		}
	}
	con := sampleHMC(t, mcmc.HMC{}, normal{d: 2}, 5, opts)
	for c := range raw.Draws {
		for j := range raw.Draws[c] {
			for i, x := range raw.Draws[c][j] {
				if got := con.Draws[c][j][i]; got != float64(2*x)+1 {
					t.Fatalf("draw [%d][%d][%d] = %g, want 2·%g+1", c, j, i, got, x)
				}
			}
		}
	}
}

// TestHMC_Refusals: bad options and models are coded errors naming
// their reason.
func TestHMC_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		hmc    mcmc.HMC
		model  mcmc.Model
		opts   mcmc.Options
		code   perr.Code
		reason string
	}{
		{"nil model", mcmc.HMC{}, nil, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "nil_model"},
		{"zero dim", mcmc.HMC{}, flat{0}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "dim"},
		{"negative chains", mcmc.HMC{}, flat{1}, mcmc.Options{Chains: -1}, perr.PULSE_MCMC_INVALID, "chains"},
		{"negative draws", mcmc.HMC{}, flat{1}, mcmc.Options{Draws: -1}, perr.PULSE_MCMC_INVALID, "draws"},
		{"negative step", mcmc.HMC{StepSize: -0.1}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "step_size"},
		{"NaN step", mcmc.HMC{StepSize: math.NaN()}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "step_size"},
		{"Inf step", mcmc.HMC{StepSize: math.Inf(1)}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "step_size"},
		{"negative path", mcmc.HMC{PathLength: -1}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "path_length"},
		{"NaN path", mcmc.HMC{PathLength: math.NaN()}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "path_length"},
		{"Inf path", mcmc.HMC{PathLength: math.Inf(1)}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "path_length"},
		{"too many steps", mcmc.HMC{StepSize: 1e-6, PathLength: 1}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "path_length"},
		{"negative workers", mcmc.HMC{Workers: -2}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "workers"},
		{"target accept 1", mcmc.HMC{TargetAccept: 1}, flat{1}, mcmc.Options{}, perr.PULSE_MCMC_INVALID, "target_accept"},
		{"init length", mcmc.HMC{}, flat{2}, mcmc.Options{Init: []float64{1}}, perr.PULSE_MCMC_INVALID, "init_length"},
		{"init density", mcmc.HMC{}, nowhere{}, mcmc.Options{Init: []float64{0}}, perr.PULSE_MCMC_INVALID, "init_density_not_finite"},
		{"no finite start", mcmc.HMC{}, nowhere{}, mcmc.Options{Chains: 1}, perr.PULSE_MCMC_FAILED, "init_not_found"},
		{"improper", mcmc.HMC{}, flat{1}, mcmc.Options{Chains: 1}, perr.PULSE_MCMC_FAILED, "step_size_unbounded"},
		// sd 1e-7 under an identity metric (warmup < 20 never adapts it)
		// adapts the step size to ~1e-7: 2π/1e-7 steps is refused.
		{"path too long", mcmc.HMC{}, normal{d: 1, sd: []float64{1e-7}}, mcmc.Options{Chains: 1, Warmup: 5, Draws: 5},
			perr.PULSE_MCMC_FAILED, "path_too_long"},
	}
	for _, tc := range cases {
		_, err := tc.hmc.Sample(context.Background(), tc.model, 1, tc.opts)
		var ce *perr.CodedError
		if !errors.As(err, &ce) || ce.Code != tc.code || ce.Details["reason"] != tc.reason {
			t.Errorf("%s: err %v, want %s reason %s", tc.name, err, tc.code, tc.reason)
		}
	}
}

// TestHMC_PanicReraised: a model panic on a chain goroutine resurfaces
// on the caller's.
func TestHMC_PanicReraised(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != "model bug" {
			t.Fatalf("recovered %v, want the model's panic", r)
		}
	}()
	_, _ = mcmc.HMC{}.Sample(context.Background(), panicky{normal{d: 1}}, 1, mcmc.Options{Chains: 3})
	t.Fatal("Sample returned instead of panicking")
}
