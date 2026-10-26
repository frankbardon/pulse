package mcmc_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
)

func sample(t *testing.T, s mcmc.NUTS, m mcmc.Model, seed uint64, opts mcmc.Options) mcmc.Result {
	t.Helper()
	res, err := s.Sample(context.Background(), m, seed, opts)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	return res
}

// pooled returns the mean and the (ddof 1) variance of parameter j over
// every chain's draws, and the products' mean E[x_j x_k] when k ≥ 0.
func pooled(res mcmc.Result, j int) (mean, variance float64) {
	var s float64
	n := 0
	for _, ch := range res.Draws {
		for _, x := range ch[j] {
			s += x
			n++
		}
	}
	mean = s / float64(n)
	var ss float64
	for _, ch := range res.Draws {
		for _, x := range ch[j] {
			d := x - mean
			ss += float64(d * d)
		}
	}
	return mean, ss / float64(n-1)
}

// checkMoments asserts each parameter's mean and variance agree with the
// target's within 5 Monte Carlo standard errors (from bulk ESS) and that
// R̂ ≤ 1.01.
func checkMoments(t *testing.T, res mcmc.Result, wantVar []float64) {
	t.Helper()
	for j, v := range wantVar {
		ess := res.Diagnostics.ESSBulk[j]
		if ess < 400 {
			t.Errorf("param %d: bulk ESS %.0f < 400", j, ess)
		}
		mean, variance := pooled(res, j)
		if se := math.Sqrt(v / ess); math.Abs(mean) > 5*se {
			t.Errorf("param %d: mean %.4f, want 0 ± %.4f", j, mean, 5*se)
		}
		// Var of a sample variance of a normal is 2σ⁴/n.
		if se := v * math.Sqrt(2/ess); math.Abs(variance-v) > 5*se {
			t.Errorf("param %d: variance %.4f, want %.4f ± %.4f", j, variance, v, 5*se)
		}
		if r := res.Diagnostics.RHat[j]; !(r <= 1.01) {
			t.Errorf("param %d: R̂ %.4f > 1.01", j, r)
		}
	}
}

// TestNUTS_StandardNormalRecovery: a 10-dimensional standard normal under
// the defaults (4 chains × 1000 warmup × 1000 draws) recovers its
// moments, mixes (R̂ ≤ 1.01), hits the 0.8 acceptance target, never
// diverges and keeps short trees (a broken U-turn criterion saturates).
func TestNUTS_StandardNormalRecovery(t *testing.T) {
	res := sample(t, mcmc.NUTS{}, normal{d: 10}, 11, mcmc.Options{})
	if len(res.Draws) != mcmc.DefaultChains || len(res.Draws[0]) != 10 || len(res.Draws[0][0]) != mcmc.DefaultDraws {
		t.Fatalf("draws shape %d×%d×%d", len(res.Draws), len(res.Draws[0]), len(res.Draws[0][0]))
	}
	want := make([]float64, 10)
	for i := range want {
		want[i] = 1
	}
	checkMoments(t, res, want)
	for c, st := range res.Diagnostics.Chains {
		if st.Divergences != 0 || st.TreeDepthSaturated != 0 {
			t.Errorf("chain %d: %d divergences, %d saturated", c, st.Divergences, st.TreeDepthSaturated)
		}
		// The retained step size is the averaged iterate, smaller than
		// the last ones, so the realised statistic sits above δ (as in
		// Stan); it must still be near it.
		if a := st.MeanAcceptStat; a < mcmc.DefaultTargetAccept-0.05 || a > 0.97 {
			t.Errorf("chain %d: mean accept stat %.3f, want in [%.2f, 0.97]", c, a, mcmc.DefaultTargetAccept-0.05)
		}
		depth := 0
		for _, d := range st.TreeDepth {
			depth += d
		}
		if mean := float64(depth) / float64(len(st.TreeDepth)); mean > 4 {
			t.Errorf("chain %d: mean tree depth %.2f > 4 on an isotropic normal", c, mean)
		}
		if st.StepSize <= 0 || len(st.Metric) != 10 || st.GradientEvals <= 2*mcmc.DefaultDraws {
			t.Errorf("chain %d: stats %+v", c, st)
		}
	}
}

// TestNUTS_CorrelatedGaussianRecovery: an AR(1) Gaussian with neighbour
// correlation 0.9 (condition number ≈ 70, beyond what a diagonal metric
// removes) recovers its marginal moments and the lag-1 correlation.
func TestNUTS_CorrelatedGaussianRecovery(t *testing.T) {
	const d, rho = 5, 0.9
	res := sample(t, mcmc.NUTS{}, gaussian{d: d, prec: ar1Precision(d, rho)}, 12, mcmc.Options{})
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
	for c, st := range res.Diagnostics.Chains {
		if st.Divergences != 0 {
			t.Errorf("chain %d: %d divergences", c, st.Divergences)
		}
	}
}

// TestNUTS_MetricAdaptation: on independent normals with scales spanning
// three orders of magnitude the adapted inverse metric tracks the
// variances, so the trees stay short.
func TestNUTS_MetricAdaptation(t *testing.T) {
	sd := []float64{0.1, 1, 10, 100}
	res := sample(t, mcmc.NUTS{}, normal{d: 4, sd: sd}, 13, mcmc.Options{Chains: 2})
	for c, st := range res.Diagnostics.Chains {
		for i, m := range st.Metric {
			if r := m / float64(sd[i]*sd[i]); r < 0.6 || r > 1.6 {
				t.Errorf("chain %d: metric[%d] = %.4g, variance %.4g (ratio %.2f)", c, i, m, sd[i]*sd[i], r)
			}
		}
		if st.TreeDepthSaturated != 0 {
			t.Errorf("chain %d: %d saturated trees", c, st.TreeDepthSaturated)
		}
	}
	vars := make([]float64, len(sd))
	for i, s := range sd {
		vars[i] = s * s
	}
	checkMoments(t, res, vars)
}

// TestNUTS_FunnelDivergences: Neal's funnel in the centred
// parameterisation diverges in its neck; every divergence is recorded
// per draw and counted.
func TestNUTS_FunnelDivergences(t *testing.T) {
	res := sample(t, mcmc.NUTS{}, funnel{d: 10}, 14, mcmc.Options{Warmup: 500, Draws: 500})
	total := 0
	for c, st := range res.Diagnostics.Chains {
		n := 0
		for _, dv := range st.Divergent {
			if dv {
				n++
			}
		}
		if n != st.Divergences {
			t.Errorf("chain %d: Divergences %d, Divergent marks %d", c, st.Divergences, n)
		}
		total += n
	}
	if total == 0 {
		t.Error("no divergence on the centred funnel")
	}
}

// TestNUTS_MaxTreeDepth: a depth cap of 2 on a strongly correlated
// target bounds every tree and counts the saturated ones.
func TestNUTS_MaxTreeDepth(t *testing.T) {
	res := sample(t, mcmc.NUTS{MaxTreeDepth: 2}, gaussian{d: 5, prec: ar1Precision(5, 0.99)}, 15,
		mcmc.Options{Chains: 2, Warmup: 300, Draws: 300})
	sat := 0
	for _, st := range res.Diagnostics.Chains {
		n := 0
		for _, d := range st.TreeDepth {
			if d > 2 {
				t.Fatalf("tree depth %d beyond the cap 2", d)
			}
			if d == 2 {
				n++
			}
		}
		if n != st.TreeDepthSaturated {
			t.Errorf("TreeDepthSaturated %d, depth-2 trees %d", st.TreeDepthSaturated, n)
		}
		sat += n
	}
	if sat == 0 {
		t.Error("a depth cap of 2 never saturated on a 0.99-correlated target")
	}
}

// TestNUTS_TargetAccept: raising the dual-averaging target shrinks the
// adapted step size and raises the realised acceptance statistic.
func TestNUTS_TargetAccept(t *testing.T) {
	m := gaussian{d: 5, prec: ar1Precision(5, 0.9)}
	mean := func(res mcmc.Result) (eps, acc float64) {
		for _, st := range res.Diagnostics.Chains {
			eps += st.StepSize
			acc += st.MeanAcceptStat
		}
		n := float64(len(res.Diagnostics.Chains))
		return eps / n, acc / n
	}
	loEps, loAcc := mean(sample(t, mcmc.NUTS{}, m, 16, mcmc.Options{}))
	hiEps, hiAcc := mean(sample(t, mcmc.NUTS{TargetAccept: 0.99}, m, 16, mcmc.Options{}))
	if !(hiEps < 0.6*loEps) {
		t.Errorf("mean step size %.4f at δ=0.99, %.4f at δ=0.8", hiEps, loEps)
	}
	if !(hiAcc > 0.975 && hiAcc > loAcc) {
		t.Errorf("mean accept stat %.3f at δ=0.99, %.3f at δ=0.8", hiAcc, loAcc)
	}
}

// drawHash is the SHA-256 of every bit a run produces: the draws in
// [chain][param][draw] order, then each chain's step size, metric, mean
// acceptance statistic, gradient count, tree depths and divergences.
func drawHash(res mcmc.Result) string {
	h := sha256.New()
	var b [8]byte
	put := func(x uint64) {
		binary.LittleEndian.PutUint64(b[:], x)
		h.Write(b[:])
	}
	for _, ch := range res.Draws {
		for _, p := range ch {
			for _, x := range p {
				put(math.Float64bits(x))
			}
		}
	}
	for _, st := range res.Diagnostics.Chains {
		put(math.Float64bits(st.StepSize))
		for _, m := range st.Metric {
			put(math.Float64bits(m))
		}
		put(math.Float64bits(st.MeanAcceptStat))
		put(uint64(st.GradientEvals))
		for i, d := range st.TreeDepth {
			v := uint64(d) << 1
			if st.Divergent[i] {
				v |= 1
			}
			put(v)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// goldenRuns are small runs whose hashes are pinned below. The funnel
// diverges and runs detmath.Exp in its density; the Gaussian uses the
// short-warmup (15/75/10) window schedule and a supplied Init; the
// standard normal the full 75/25/50 schedule.
var goldenRuns = []struct {
	name  string
	model mcmc.Model
	nuts  mcmc.NUTS
	seed  uint64
	opts  mcmc.Options
	hash  string
}{
	{"funnel", funnel{d: 4}, mcmc.NUTS{}, 101,
		mcmc.Options{Chains: 3, Warmup: 120, Draws: 100},
		"2d4cb40fcccd91a097388b0e15b29fc89bc1fe3565cc1e6071f4b6b553881818"},
	{"ar1", gaussian{d: 3, prec: ar1Precision(3, 0.8)}, mcmc.NUTS{TargetAccept: 0.9, MaxTreeDepth: 6}, 202,
		mcmc.Options{Chains: 2, Warmup: 80, Draws: 60, Init: []float64{0.5, -1, 1.5}},
		"0818c9e040138f236cdaa153ff430259b73f60cac9df30c6b40c63bc361e3cb9"},
	{"normal_full_schedule", normal{d: 2, sd: []float64{0.5, 3}}, mcmc.NUTS{}, 303,
		mcmc.Options{Chains: 2, Warmup: 1000, Draws: 50},
		"5600959d6f8360595740166b1002bd3824e7a7fd2dd310d439a7685a04b64069"},
}

// TestNUTS_GoldenHash pins the exact bits of each golden run. The hashes
// were computed on darwin/arm64 and reproduced on linux/amd64; every
// architecture must reproduce them, so an FMA contraction or a platform
// transcendental anywhere in the sampler or the test models fails this
// test on amd64 CI (it never skips).
func TestNUTS_GoldenHash(t *testing.T) {
	for _, g := range goldenRuns {
		got := drawHash(sample(t, g.nuts, g.model, g.seed, g.opts))
		if got != g.hash {
			t.Errorf("%s: draw hash %s, golden %s (GOARCH %s)", g.name, got, g.hash, runtime.GOARCH)
		}
	}
}

// TestNUTS_Deterministic: the same seed gives the same bits across runs,
// worker counts and GOMAXPROCS; another seed gives other bits.
func TestNUTS_Deterministic(t *testing.T) {
	m := funnel{d: 4}
	opts := mcmc.Options{Chains: 5, Warmup: 150, Draws: 100}
	base := drawHash(sample(t, mcmc.NUTS{}, m, 7, opts))
	for _, w := range []int{1, 2, 3, 5, 16} {
		if got := drawHash(sample(t, mcmc.NUTS{Workers: w}, m, 7, opts)); got != base {
			t.Errorf("Workers=%d: hash %s, want %s", w, got, base)
		}
	}
	prev := runtime.GOMAXPROCS(1)
	one := drawHash(sample(t, mcmc.NUTS{}, m, 7, opts))
	runtime.GOMAXPROCS(prev)
	if one != base {
		t.Errorf("GOMAXPROCS=1: hash %s, want %s", one, base)
	}
	if got := drawHash(sample(t, mcmc.NUTS{}, m, 8, opts)); got == base {
		t.Error("seed 8 reproduced seed 7's draws")
	}
	// Chains are independent streams: no two chains share a draw.
	res := sample(t, mcmc.NUTS{}, m, 7, opts)
	for a := range res.Draws {
		for b := a + 1; b < len(res.Draws); b++ {
			if res.Draws[a][0][0] == res.Draws[b][0][0] {
				t.Errorf("chains %d and %d start their draws identically", a, b)
			}
		}
	}
}

// cancelAfter cancels its context after n gradient evaluations and
// counts every evaluation.
type cancelAfter struct {
	mcmc.Model
	n      int64
	calls  atomic.Int64
	after  atomic.Int64
	cancel context.CancelFunc
}

func (m *cancelAfter) LogDensityGrad(theta, grad []float64) float64 {
	if c := m.calls.Add(1); c == m.n {
		m.cancel()
	} else if c > m.n {
		m.after.Add(1)
	}
	return m.Model.LogDensityGrad(theta, grad)
}

// TestNUTS_ContextCancel: a context cancelled mid-run returns ctx.Err()
// within one transition per chain; an already-cancelled context returns
// before any gradient.
func TestNUTS_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &cancelAfter{Model: normal{d: 3}, n: 500, cancel: cancel}
	const chains, depth = 4, 10
	start := time.Now()
	_, err := mcmc.NUTS{}.Sample(ctx, m, 1, mcmc.Options{Chains: chains, Warmup: 100000, Draws: 100000})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
	if after := m.after.Load(); after > chains*(1<<depth) {
		t.Errorf("%d gradients after cancel; at most one transition per chain (%d) may finish", after, chains*(1<<depth))
	}
	if el := time.Since(start); el > 10*time.Second {
		t.Errorf("cancel took %v", el)
	}

	pre, preCancel := context.WithCancel(context.Background())
	preCancel()
	m2 := &cancelAfter{Model: normal{d: 3}, n: -1, cancel: func() {}}
	if _, err := (mcmc.NUTS{}).Sample(pre, m2, 1, mcmc.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled: err %v", err)
	}
	if c := m2.calls.Load(); c != 0 {
		t.Errorf("pre-cancelled context still evaluated %d gradients", c)
	}
}

// firstTheta records the first point a single chain evaluates.
type firstTheta struct {
	mcmc.Model
	first []float64
}

func (m *firstTheta) LogDensityGrad(theta, grad []float64) float64 {
	if m.first == nil {
		m.first = append([]float64(nil), theta...)
	}
	return m.Model.LogDensityGrad(theta, grad)
}

// TestNUTS_InitPoint: a supplied Init is where the chain starts; without
// one each coordinate is drawn from (−2, 2).
func TestNUTS_InitPoint(t *testing.T) {
	opts := mcmc.Options{Chains: 1, Warmup: 20, Draws: 5, Init: []float64{3.25, -7.5}}
	m := &firstTheta{Model: normal{d: 2}}
	sample(t, mcmc.NUTS{}, m, 3, opts)
	if m.first[0] != 3.25 || m.first[1] != -7.5 {
		t.Errorf("first point %v, want Init", m.first)
	}
	opts.Init = nil
	m = &firstTheta{Model: normal{d: 200}}
	sample(t, mcmc.NUTS{}, m, 3, opts)
	var lo, hi float64
	for _, x := range m.first {
		if !(x > -2 && x < 2) {
			t.Fatalf("random init coordinate %g outside (−2, 2)", x)
		}
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	if lo > -1.9 || hi < 1.9 {
		t.Errorf("200 Uniform(−2, 2) coordinates span only [%g, %g]", lo, hi)
	}
}

// TestNUTS_Constrain: Result.Draws holds Constrain's values, and
// Constrain never changes the chain (the same seed's raw draws map onto
// them exactly).
func TestNUTS_Constrain(t *testing.T) {
	opts := mcmc.Options{Chains: 2, Warmup: 50, Draws: 40}
	raw := sample(t, mcmc.NUTS{}, normal{d: 2}, 5, opts)
	opts.Constrain = func(theta, out []float64) {
		for i, x := range theta {
			out[i] = mcmc.Positive{}.Forward(x).Y
		}
	}
	con := sample(t, mcmc.NUTS{}, normal{d: 2}, 5, opts)
	for c := range raw.Draws {
		for j := range raw.Draws[c] {
			for i, x := range raw.Draws[c][j] {
				if got := con.Draws[c][j][i]; got != detmath.Exp(x) {
					t.Fatalf("draw [%d][%d][%d] = %g, want exp(%g)", c, j, i, got, x)
				}
			}
		}
	}
}

type flat struct{ d int }

func (m flat) Dim() int                                     { return m.d }
func (m flat) LogDensityGrad(theta, grad []float64) float64 { clear(grad); return 0 }

type nowhere struct{}

func (nowhere) Dim() int                                     { return 1 }
func (nowhere) LogDensityGrad(theta, grad []float64) float64 { return math.Inf(-1) }

// TestNUTS_Refusals: bad options and models are coded errors naming
// their reason.
func TestNUTS_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		nuts   mcmc.NUTS
		model  mcmc.Model
		opts   mcmc.Options
		code   perr.Code
		reason string
	}{
		{"nil model", mcmc.NUTS{}, nil, mcmc.Options{}, perr.PROCESSING_CONFIG, "nil_model"},
		{"zero dim", mcmc.NUTS{}, flat{0}, mcmc.Options{}, perr.PROCESSING_CONFIG, "dim"},
		{"negative chains", mcmc.NUTS{}, flat{1}, mcmc.Options{Chains: -1}, perr.PROCESSING_CONFIG, "chains"},
		{"negative warmup", mcmc.NUTS{}, flat{1}, mcmc.Options{Warmup: -1}, perr.PROCESSING_CONFIG, "warmup"},
		{"negative draws", mcmc.NUTS{}, flat{1}, mcmc.Options{Draws: -1}, perr.PROCESSING_CONFIG, "draws"},
		{"negative depth", mcmc.NUTS{MaxTreeDepth: -1}, flat{1}, mcmc.Options{}, perr.PROCESSING_CONFIG, "max_tree_depth"},
		{"negative workers", mcmc.NUTS{Workers: -2}, flat{1}, mcmc.Options{}, perr.PROCESSING_CONFIG, "workers"},
		{"target accept 1", mcmc.NUTS{TargetAccept: 1}, flat{1}, mcmc.Options{}, perr.PROCESSING_CONFIG, "target_accept"},
		{"target accept NaN", mcmc.NUTS{TargetAccept: math.NaN()}, flat{1}, mcmc.Options{}, perr.PROCESSING_CONFIG, "target_accept"},
		{"init length", mcmc.NUTS{}, flat{2}, mcmc.Options{Init: []float64{1}}, perr.PROCESSING_CONFIG, "init_length"},
		{"init NaN", mcmc.NUTS{}, flat{1}, mcmc.Options{Init: []float64{math.NaN()}}, perr.PROCESSING_CONFIG, "init_not_finite"},
		{"init density", mcmc.NUTS{}, nowhere{}, mcmc.Options{Init: []float64{0}}, perr.PROCESSING_CONFIG, "init_density_not_finite"},
		{"no finite start", mcmc.NUTS{}, nowhere{}, mcmc.Options{Chains: 1}, perr.PROCESSING_RUNTIME, "init_not_found"},
		{"improper", mcmc.NUTS{}, flat{1}, mcmc.Options{Chains: 1}, perr.PROCESSING_RUNTIME, "step_size_unbounded"},
	}
	for _, tc := range cases {
		_, err := tc.nuts.Sample(context.Background(), tc.model, 1, tc.opts)
		var ce *perr.CodedError
		if !errors.As(err, &ce) || ce.Code != tc.code || ce.Details["reason"] != tc.reason {
			t.Errorf("%s: err %v, want %s reason %s", tc.name, err, tc.code, tc.reason)
		}
	}
}

type panicky struct{ normal }

func (panicky) LogDensityGrad(theta, grad []float64) float64 { panic("model bug") }

// TestNUTS_PanicReraised: a model panic on a chain goroutine resurfaces
// on the caller's, where an embedder's recover can catch it.
func TestNUTS_PanicReraised(t *testing.T) {
	defer func() {
		if r := recover(); r != "model bug" {
			t.Fatalf("recovered %v, want the model's panic", r)
		}
	}()
	_, _ = mcmc.NUTS{}.Sample(context.Background(), panicky{normal{d: 1}}, 1, mcmc.Options{Chains: 3})
	t.Fatal("Sample returned instead of panicking")
}
