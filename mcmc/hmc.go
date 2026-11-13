package mcmc

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// DefaultPathLength is HMC.PathLength's default: π/2, a quarter period
// of the unit-scale Gaussian the adapted diagonal metric aims at, where
// one trajectory carries a draw to a nearly independent point. (Stan's
// static HMC defaults to 2π, a full period there, which returns each
// trajectory close to its start.)
const DefaultPathLength = math.Pi / 2

// maxHMCSteps bounds the leapfrog steps of one HMC transition. Past it
// ⌈PathLength/StepSize⌉ is refused rather than run: the float-to-int
// conversion of a larger ratio is not portable, and the run would not end
// in useful time.
const maxHMCSteps = 1 << 16

// HMC is static Hamiltonian Monte Carlo (Neal 2011, "MCMC using
// Hamiltonian dynamics"): each transition draws a momentum, integrates a
// fixed path of L = ⌈PathLength/StepSize⌉ leapfrog steps (at least 1)
// under a diagonal Euclidean metric, and accepts the endpoint with the
// Metropolis probability min(1, exp(H0 − H)), otherwise stays put. A
// transition whose energy error H − H0 exceeds 1000 at any step is a
// divergence: integration stops there and the transition is rejected.
//
// Warmup adapts the diagonal metric exactly as NUTS does (Stan's windowed
// adaptation). With StepSize 0 it also adapts the step size as NUTS does
// — the initial heuristic, then dual averaging towards TargetAccept,
// re-initialised after each metric window, fixed at the averaged iterate
// at the end of warmup — and L follows the step size. A warmup step size
// whose L would exceed 65536 runs 65536 steps; a final one is refused as
// PULSE_MCMC_FAILED (reason path_too_long). A non-zero StepSize is used
// as given throughout, and TargetAccept is then unused.
//
// HMC builds no tree: ChainStats.TreeDepth is nil and
// TreeDepthSaturated is 0 for every chain. Divergences and Divergent are
// recorded as for NUTS.
//
// Chain seeding, the goroutine pool, the chain-order merge and the
// per-iteration ctx check are NUTS's, so the same model, seed and options
// give the same Result — bit for bit, on every architecture — whatever
// Workers and GOMAXPROCS are.
//
// The zero value is ready to use.
type HMC struct {
	// StepSize is the leapfrog step size; 0 adapts it during warmup. It
	// must be finite and not negative.
	StepSize float64
	// PathLength is the integration time of one trajectory; 0 means
	// DefaultPathLength. It must be finite and not negative, and with a
	// fixed StepSize ⌈PathLength/StepSize⌉ must not exceed 65536.
	PathLength float64
	// TargetAccept is the dual-averaging target for the mean acceptance
	// statistic when StepSize is 0, in (0, 1); 0 means
	// DefaultTargetAccept.
	TargetAccept float64
	// Workers is the number of goroutines chains run on; 0 means
	// min(Chains, GOMAXPROCS). It changes the wall time only, never the
	// Result.
	Workers int
}

// Sample runs opts.Chains chains of opts.Warmup adaptation iterations
// followed by opts.Draws retained ones and returns their draws with
// R̂, bulk and tail ESS per parameter and each chain's statistics.
//
// Starting points, ctx handling, panics and refusals are as for
// NUTS.Sample. HMC adds the PULSE_MCMC_INVALID reasons step_size and
// path_length (a negative or non-finite knob, or a fixed step size whose
// path needs more than 65536 steps) and the PULSE_MCMC_FAILED reason
// path_too_long (an adapted step size whose path needs more).
func (s HMC) Sample(ctx context.Context, model Model, seed uint64, opts Options) (Result, error) {
	cfg, err := s.resolve(model, opts)
	if err != nil {
		return Result{}, err
	}
	return sampleChains(ctx, model, cfg, seed, runHMCChain)
}

func (s HMC) resolve(model Model, opts Options) (runConfig, error) {
	cfg, err := resolveRun(model, opts)
	if err != nil {
		return cfg, err
	}
	if !(s.StepSize >= 0) || math.IsInf(s.StepSize, 0) {
		return cfg, configErr("step_size", fmt.Sprintf("StepSize is %g; it must be finite and not negative (0 adapts it)", s.StepSize),
			map[string]any{"got": s.StepSize})
	}
	if !(s.PathLength >= 0) || math.IsInf(s.PathLength, 0) {
		return cfg, configErr("path_length", fmt.Sprintf("PathLength is %g; it must be finite and not negative (0 takes the default)", s.PathLength),
			map[string]any{"got": s.PathLength})
	}
	cfg.stepSize = s.StepSize
	cfg.pathLength = s.PathLength
	if cfg.pathLength == 0 {
		cfg.pathLength = DefaultPathLength
	}
	if cfg.stepSize > 0 {
		if _, ok := hmcSteps(cfg.pathLength, cfg.stepSize); !ok {
			return cfg, configErr("path_length", fmt.Sprintf(
				"PathLength %g at StepSize %g needs more than %d leapfrog steps per transition", cfg.pathLength, cfg.stepSize, maxHMCSteps),
				map[string]any{"got": cfg.pathLength, "step_size": cfg.stepSize, "max_steps": maxHMCSteps})
		}
	}
	return cfg.finish(s.Workers, s.TargetAccept, opts)
}

// hmcSteps is ⌈pathLength/eps⌉, at least 1; ok is false (and the count
// maxHMCSteps) when the ratio exceeds maxHMCSteps or is not a number.
func hmcSteps(pathLength, eps float64) (n int, ok bool) {
	r := math.Ceil(pathLength / eps)
	if !(r <= maxHMCSteps) {
		return maxHMCSteps, false
	}
	if r < 1 {
		return 1, true
	}
	return int(r), true
}

func runHMCChain(ctx context.Context, model Model, cfg runConfig, seed uint64, c int) chainOut {
	s := chainSeed(seed, c)
	ch := newChain(model, cfg, rand.New(rand.NewPCG(s, s^pcgStream)), c)
	if err := ch.initPoint(cfg.init); err != nil {
		return chainOut{err: err}
	}
	adapt := cfg.stepSize == 0
	var da *dualAveraging
	if adapt {
		if err := ch.initStepSize(); err != nil {
			return chainOut{err: err}
		}
		da = newDualAveraging(cfg.delta, ch.eps)
	} else {
		ch.eps = cfg.stepSize
	}
	va := newVarAdaptation(cfg.warmup, cfg.dim)
	for range cfg.warmup {
		if err := ctx.Err(); err != nil {
			return chainOut{err: err}
		}
		steps, _ := hmcSteps(cfg.pathLength, ch.eps)
		accept := ch.hmcTransition(steps)
		if adapt {
			ch.eps = da.learn(accept)
		}
		updated, ok := va.learn(ch.invMetric, ch.z.q)
		if !ok {
			return chainOut{err: runtimeErr("metric_not_finite",
				"the adapted metric is not finite; the posterior is likely improper or the model's gradient wrong", c)}
		}
		if updated && adapt {
			if err := ch.initStepSize(); err != nil {
				return chainOut{err: err}
			}
			da.restart(ch.eps)
		}
	}
	if adapt {
		ch.eps = da.final()
	}
	steps, ok := hmcSteps(cfg.pathLength, ch.eps)
	if !ok {
		return chainOut{err: runtimeErr("path_too_long", fmt.Sprintf(
			"the adapted step size %g needs more than %d leapfrog steps to cover PathLength %g; the posterior is likely badly scaled or its gradient wrong",
			ch.eps, maxHMCSteps, cfg.pathLength), c)}
	}

	out := chainOut{draws: make([][]float64, cfg.dim)}
	for j := range out.draws {
		out.draws[j] = make([]float64, cfg.draws)
	}
	st := &out.stats
	st.Divergent = make([]bool, cfg.draws)
	val := make([]float64, cfg.dim)
	var sumAccept float64
	for i := range cfg.draws {
		if err := ctx.Err(); err != nil {
			return chainOut{err: err}
		}
		sumAccept += ch.hmcTransition(steps)
		if cfg.constrain != nil {
			cfg.constrain(ch.z.q, val)
		} else {
			copy(val, ch.z.q)
		}
		for j, v := range val {
			out.draws[j][i] = v
		}
		st.Divergent[i] = ch.divergent
		if ch.divergent {
			st.Divergences++
		}
	}
	st.StepSize = ch.eps
	st.Metric = append([]float64(nil), ch.invMetric...)
	st.MeanAcceptStat = sumAccept / float64(cfg.draws)
	st.GradientEvals = ch.evals
	return out
}

// hmcTransition draws one static HMC transition of steps leapfrog steps
// from ch.z, leaves the retained point in ch.z (ch.divergent says whether
// the trajectory diverged) and returns the acceptance statistic
// min(1, exp(H0 − H)), 0 for a divergence.
func (ch *chain) hmcTransition(steps int) float64 {
	ch.sampleMomentum(ch.z.p)
	ch.zInit.set(&ch.z)
	h0 := ch.hamiltonian(&ch.z)
	h := h0
	ch.divergent = false
	for range steps {
		ch.leapfrog(&ch.z, ch.eps)
		h = ch.hamiltonian(&ch.z)
		if h-h0 > maxDeltaH {
			ch.divergent = true
			break
		}
	}
	if ch.divergent {
		ch.z.set(&ch.zInit)
		return 0
	}
	accept := 1.0
	if d := h0 - h; !(d > 0) {
		accept = detmath.Exp(d)
	}
	if accept < 1 && ch.rng.Float64() > accept {
		ch.z.set(&ch.zInit)
	}
	return accept
}
