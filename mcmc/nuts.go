package mcmc

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"sync"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// NUTS run defaults: a zero Options field or NUTS knob takes these.
const (
	// DefaultChains is the number of chains when Options.Chains is 0.
	DefaultChains = 4
	// DefaultWarmup is the warmup length when Options.Warmup is 0.
	DefaultWarmup = 1000
	// DefaultDraws is the retained draws per chain when Options.Draws is 0.
	DefaultDraws = 1000
	// DefaultMaxTreeDepth is NUTS.MaxTreeDepth's default: at most
	// 2^10 − 1 = 1023 leapfrog steps per transition.
	DefaultMaxTreeDepth = 10
	// DefaultTargetAccept is NUTS.TargetAccept's default, the dual
	// averaging target δ for the mean acceptance statistic.
	DefaultTargetAccept = 0.8
)

const (
	// maxDeltaH is the energy error H − H0 above which a leapfrog step
	// is a divergence (Stan's max_deltaH).
	maxDeltaH = 1000
	// initAttempts is how many Uniform(−2, 2) starting points a chain
	// tries before refusing a model with no finite density there.
	initAttempts = 100
	// pcgStream is the second PCG word every seeded draw in Pulse uses.
	pcgStream = 0x9E3779B97F4A7C15
)

// NUTS is the multinomial No-U-Turn sampler of Stan (Betancourt 2017,
// "A Conceptual Introduction to Hamiltonian Monte Carlo", appendix A),
// with a diagonal Euclidean metric, the generalised U-turn criterion
// (checked across the merged trajectory and across both subtree seams),
// biased progressive sampling at the top level and a transition that is a
// divergence when the energy error exceeds 1000.
//
// Warmup follows Stan's adaptation: dual averaging of the step size
// (Hoffman and Gelman 2014; δ = TargetAccept, γ = 0.05, t0 = 10,
// κ = 0.75, μ = log(10·ε) after the initial step-size heuristic), and a
// windowed diagonal metric estimated from the draws of each slow window
// (initial fast buffer 75, first window 25 doubling, terminal buffer 50;
// a warmup shorter than 150 uses 15% / 75% / 10% instead; below 20 the
// metric stays the identity), regularised as
// (n/(n+5))·var + 1e-3·5/(n+5). After each window the step size is
// re-initialised and dual averaging restarts; at the end of warmup the
// step size is fixed at the averaged iterate.
//
// Each chain draws from its own PCG stream, derived from (seed, chain
// index), and chains run concurrently on Workers goroutines, merged in
// chain order: the same model, seed and options give the same Result —
// bit for bit, on every architecture — whatever Workers and GOMAXPROCS
// are. The model must be deterministic (the same theta gives the same
// bits) for that to hold.
//
// The zero value is ready to use.
type NUTS struct {
	// MaxTreeDepth caps the trajectory at 2^MaxTreeDepth − 1 leapfrog
	// steps; 0 means DefaultMaxTreeDepth. A transition that reaches it
	// counts in ChainStats.TreeDepthSaturated.
	MaxTreeDepth int
	// TargetAccept is the dual-averaging target for the mean acceptance
	// statistic, in (0, 1); 0 means DefaultTargetAccept. A higher target
	// gives a smaller step size and fewer divergences at more gradient
	// evaluations per draw.
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
// A chain starts at opts.Init when it is set, otherwise at a point drawn
// Uniform(−2, 2) per coordinate from the chain's stream (up to 100
// attempts until the log density and its gradient are finite).
//
// ctx is checked before every iteration of every chain; once it is done
// Sample returns ctx.Err() and no Result. Invalid options or model shape
// are a PROCESSING_CONFIG *errors.CodedError; a model the sampler cannot
// run (no finite starting point, a step size that collapses to zero or
// grows without bound, a non-finite adapted metric) is a
// PROCESSING_RUNTIME one. Both carry details["reason"]. A panic in the
// model or in Constrain is re-raised on the caller's goroutine.
func (s NUTS) Sample(ctx context.Context, model Model, seed uint64, opts Options) (Result, error) {
	cfg, err := s.resolve(model, opts)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	outs := make([]chainOut, cfg.chains)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(cfg.workers, cfg.chains) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				outs[c] = runChainRecover(ctx, model, cfg, seed, c)
			}
		}()
	}
	for c := range cfg.chains {
		jobs <- c
	}
	close(jobs)
	wg.Wait()

	// Merge in chain order: the first panic, then the context, then the
	// first chain error, so the outcome never depends on scheduling.
	for _, o := range outs {
		if o.panicked {
			panic(o.panicVal)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	res := Result{Draws: make([][][]float64, cfg.chains)}
	res.Diagnostics.Chains = make([]ChainStats, cfg.chains)
	for c, o := range outs {
		if o.err != nil {
			return Result{}, o.err
		}
		res.Draws[c] = o.draws
		res.Diagnostics.Chains[c] = o.stats
	}
	d := cfg.dim
	res.Diagnostics.RHat = make([]float64, d)
	res.Diagnostics.ESSBulk = make([]float64, d)
	res.Diagnostics.ESSTail = make([]float64, d)
	for j := range d {
		p := res.Param(j)
		res.Diagnostics.RHat[j] = RHat(p)
		res.Diagnostics.ESSBulk[j] = ESSBulk(p)
		res.Diagnostics.ESSTail[j] = ESSTail(p)
	}
	return res, nil
}

// nutsConfig is a validated run with every default applied.
type nutsConfig struct {
	dim, chains, warmup, draws, maxDepth, workers int
	delta                                         float64
	init                                          []float64
	constrain                                     func(theta, out []float64)
}

func configErr(reason, msg string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["reason"] = reason
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG, "mcmc: "+msg, details)
}

func runtimeErr(reason, msg string, chain int) error {
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_RUNTIME, "mcmc: "+msg,
		map[string]any{"reason": reason, "chain": chain})
}

func (s NUTS) resolve(model Model, opts Options) (nutsConfig, error) {
	var cfg nutsConfig
	if model == nil {
		return cfg, configErr("nil_model", "the model is nil", nil)
	}
	cfg.dim = model.Dim()
	if cfg.dim < 1 {
		return cfg, configErr("dim", fmt.Sprintf("the model has dimension %d; it must be at least 1", cfg.dim),
			map[string]any{"got": cfg.dim})
	}
	pick := func(name string, v, def int) (int, error) {
		switch {
		case v < 0:
			return 0, configErr(name, fmt.Sprintf("%s is %d; it must be positive (0 takes the default %d)", name, v, def),
				map[string]any{"got": v})
		case v == 0:
			return def, nil
		}
		return v, nil
	}
	var err error
	if cfg.chains, err = pick("chains", opts.Chains, DefaultChains); err != nil {
		return cfg, err
	}
	if cfg.warmup, err = pick("warmup", opts.Warmup, DefaultWarmup); err != nil {
		return cfg, err
	}
	if cfg.draws, err = pick("draws", opts.Draws, DefaultDraws); err != nil {
		return cfg, err
	}
	if cfg.maxDepth, err = pick("max_tree_depth", s.MaxTreeDepth, DefaultMaxTreeDepth); err != nil {
		return cfg, err
	}
	if cfg.workers, err = pick("workers", s.Workers, min(cfg.chains, runtime.GOMAXPROCS(0))); err != nil {
		return cfg, err
	}
	cfg.delta = s.TargetAccept
	if cfg.delta == 0 {
		cfg.delta = DefaultTargetAccept
	}
	if !(cfg.delta > 0 && cfg.delta < 1) {
		return cfg, configErr("target_accept", fmt.Sprintf("TargetAccept is %g; it must be in (0, 1)", s.TargetAccept),
			map[string]any{"got": s.TargetAccept})
	}
	if opts.Init != nil {
		if len(opts.Init) != cfg.dim {
			return cfg, configErr("init_length", fmt.Sprintf("Init has %d values; the model has dimension %d", len(opts.Init), cfg.dim),
				map[string]any{"want": cfg.dim, "got": len(opts.Init)})
		}
		for i, x := range opts.Init {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return cfg, configErr("init_not_finite", fmt.Sprintf("Init[%d] is %g; it must be finite", i, x),
					map[string]any{"index": i})
			}
		}
		cfg.init = append([]float64(nil), opts.Init...)
	}
	cfg.constrain = opts.Constrain
	return cfg, nil
}

// chainOut is one chain's outcome, written by exactly one worker.
type chainOut struct {
	draws    [][]float64 // [param][draw]
	stats    ChainStats
	err      error
	panicked bool
	panicVal any
}

func runChainRecover(ctx context.Context, model Model, cfg nutsConfig, seed uint64, c int) (out chainOut) {
	defer func() {
		if r := recover(); r != nil {
			out = chainOut{panicked: true, panicVal: r}
		}
	}()
	return runChain(ctx, model, cfg, seed, c)
}

// chainSeed is the PCG seed chain c draws from under seed: a SplitMix64
// finaliser over seed + (c+1)·0x9E3779B97F4A7C15, so neighbouring seeds
// and chains get unrelated streams. The chain's generator is
// rand.NewPCG(s, s ^ 0x9E3779B97F4A7C15) with s = chainSeed(seed, c).
func chainSeed(seed uint64, c int) uint64 {
	z := seed + uint64(c+1)*pcgStream
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func runChain(ctx context.Context, model Model, cfg nutsConfig, seed uint64, c int) chainOut {
	s := chainSeed(seed, c)
	ch := newChain(model, cfg, rand.New(rand.NewPCG(s, s^pcgStream)), c)
	if err := ch.initPoint(cfg.init); err != nil {
		return chainOut{err: err}
	}
	if err := ch.initStepSize(); err != nil {
		return chainOut{err: err}
	}
	da := newDualAveraging(cfg.delta, ch.eps)
	va := newVarAdaptation(cfg.warmup, cfg.dim)
	for range cfg.warmup {
		if err := ctx.Err(); err != nil {
			return chainOut{err: err}
		}
		accept := ch.transition()
		ch.eps = da.learn(accept)
		updated, ok := va.learn(ch.invMetric, ch.z.q)
		if !ok {
			return chainOut{err: runtimeErr("metric_not_finite",
				"the adapted metric is not finite; the posterior is likely improper or the model's gradient wrong", c)}
		}
		if updated {
			if err := ch.initStepSize(); err != nil {
				return chainOut{err: err}
			}
			da.restart(ch.eps)
		}
	}
	ch.eps = da.final()

	out := chainOut{draws: make([][]float64, cfg.dim)}
	for j := range out.draws {
		out.draws[j] = make([]float64, cfg.draws)
	}
	st := &out.stats
	st.TreeDepth = make([]int, cfg.draws)
	st.Divergent = make([]bool, cfg.draws)
	val := make([]float64, cfg.dim)
	var sumAccept float64
	for i := range cfg.draws {
		if err := ctx.Err(); err != nil {
			return chainOut{err: err}
		}
		sumAccept += ch.transition()
		if cfg.constrain != nil {
			cfg.constrain(ch.z.q, val)
		} else {
			copy(val, ch.z.q)
		}
		for j, v := range val {
			out.draws[j][i] = v
		}
		st.TreeDepth[i] = ch.depth
		st.Divergent[i] = ch.divergent
		if ch.divergent {
			st.Divergences++
		}
		if ch.depth >= ch.maxDepth {
			st.TreeDepthSaturated++
		}
	}
	st.StepSize = ch.eps
	st.Metric = append([]float64(nil), ch.invMetric...)
	st.MeanAcceptStat = sumAccept / float64(cfg.draws)
	st.GradientEvals = ch.evals
	return out
}

// point is a phase-space point: position, momentum, and the log density
// and its gradient at the position.
type point struct {
	q, p, g []float64
	logp    float64
}

func newPoint(d int) point {
	return point{q: make([]float64, d), p: make([]float64, d), g: make([]float64, d)}
}

func (z *point) set(o *point) {
	copy(z.q, o.q)
	copy(z.p, o.p)
	copy(z.g, o.g)
	z.logp = o.logp
}

// treeLevel is the scratch one build-tree frame at a given depth needs.
// At most one frame per depth is live at a time (a frame's two children
// run one after the other), so one set per depth suffices.
type treeLevel struct {
	pInitEnd, pSharpInitEnd, rhoInit  []float64
	pFinalBeg, pSharpFinalBeg, rhoFin []float64
	rhoSub, rhoExt                    []float64
	zFinal                            point
}

// chain is one chain's sampler state.
type chain struct {
	model     Model
	rng       *rand.Rand
	index     int
	dim       int
	maxDepth  int
	invMetric []float64
	eps       float64
	evals     int

	z         point // the integrator's current point
	depth     int
	divergent bool

	// transition-level buffers
	zFwd, zBck, zSample, zPropose, zInit         point
	pFwdFwd, pSharpFwdFwd, pFwdBck, pSharpFwdBck []float64
	pBckFwd, pSharpBckFwd, pBckBck, pSharpBckBck []float64
	rho, rhoFwd, rhoBck, rhoExt                  []float64
	levels                                       []treeLevel
	logPoint8                                    float64
}

func newChain(model Model, cfg nutsConfig, rng *rand.Rand, index int) *chain {
	d := cfg.dim
	v := func() []float64 { return make([]float64, d) }
	ch := &chain{
		model: model, rng: rng, index: index, dim: d, maxDepth: cfg.maxDepth,
		invMetric: v(), eps: 1,
		z: newPoint(d), zFwd: newPoint(d), zBck: newPoint(d), zSample: newPoint(d),
		zPropose: newPoint(d), zInit: newPoint(d),
		pFwdFwd: v(), pSharpFwdFwd: v(), pFwdBck: v(), pSharpFwdBck: v(),
		pBckFwd: v(), pSharpBckFwd: v(), pBckBck: v(), pSharpBckBck: v(),
		rho: v(), rhoFwd: v(), rhoBck: v(), rhoExt: v(),
		levels:    make([]treeLevel, cfg.maxDepth),
		logPoint8: detmath.Log(0.8),
	}
	for i := range ch.invMetric {
		ch.invMetric[i] = 1
	}
	for k := range ch.levels {
		ch.levels[k] = treeLevel{
			pInitEnd: v(), pSharpInitEnd: v(), rhoInit: v(),
			pFinalBeg: v(), pSharpFinalBeg: v(), rhoFin: v(),
			rhoSub: v(), rhoExt: v(), zFinal: newPoint(d),
		}
	}
	return ch
}

func (ch *chain) eval(z *point) {
	z.logp = ch.model.LogDensityGrad(z.q, z.g)
	ch.evals++
}

func finitePoint(z *point) bool {
	if math.IsNaN(z.logp) || math.IsInf(z.logp, 0) {
		return false
	}
	for _, g := range z.g {
		if math.IsNaN(g) || math.IsInf(g, 0) {
			return false
		}
	}
	return true
}

// initPoint sets the chain's starting point: init when given, else
// Uniform(−2, 2) per coordinate from the chain's stream.
func (ch *chain) initPoint(init []float64) error {
	if init != nil {
		copy(ch.z.q, init)
		ch.eval(&ch.z)
		if !finitePoint(&ch.z) {
			return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
				"mcmc: the log density or its gradient is not finite at Init",
				map[string]any{"reason": "init_density_not_finite", "chain": ch.index})
		}
		return nil
	}
	for range initAttempts {
		for i := range ch.z.q {
			ch.z.q[i] = float64(4*ch.rng.Float64()) - 2
		}
		ch.eval(&ch.z)
		if finitePoint(&ch.z) {
			return nil
		}
	}
	return runtimeErr("init_not_found", fmt.Sprintf(
		"no Uniform(-2, 2) starting point in %d attempts has a finite log density and gradient; supply Options.Init", initAttempts), ch.index)
}

// sampleMomentum draws p ~ N(0, M) for the diagonal metric M = diag(1/invMetric).
func (ch *chain) sampleMomentum(p []float64) {
	for i := range p {
		p[i] = detmath.NormFloat64(ch.rng) / math.Sqrt(ch.invMetric[i])
	}
}

// hamiltonian is H = ½ pᵀM⁻¹p − log p(q); a NaN energy is +Inf.
func (ch *chain) hamiltonian(z *point) float64 {
	var s float64
	for i, p := range z.p {
		s += float64(float64(p*p) * ch.invMetric[i])
	}
	h := float64(0.5*s) - z.logp
	if math.IsNaN(h) {
		return math.Inf(1)
	}
	return h
}

// pSharp writes M⁻¹p, the velocity the U-turn criterion projects on.
func (ch *chain) pSharp(p, out []float64) {
	for i := range p {
		out[i] = float64(ch.invMetric[i] * p[i])
	}
}

// leapfrog advances z one step of size eps (negative runs backwards).
func (ch *chain) leapfrog(z *point, eps float64) {
	half := float64(0.5 * eps)
	for i := range z.p {
		z.p[i] += float64(half * z.g[i])
	}
	for i := range z.q {
		z.q[i] += float64(eps * float64(ch.invMetric[i]*z.p[i]))
	}
	ch.eval(z)
	for i := range z.p {
		z.p[i] += float64(half * z.g[i])
	}
}

func dot(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += float64(a[i] * b[i])
	}
	return s
}

// uturnFree is the generalised no-U-turn criterion between the two ends
// of a trajectory with summed momentum rho.
func uturnFree(pSharpMinus, pSharpPlus, rho []float64) bool {
	return dot(pSharpPlus, rho) > 0 && dot(pSharpMinus, rho) > 0
}

// logSumExp is log(e^a + e^b), exact at the infinities.
func logSumExp(a, b float64) float64 {
	if math.IsInf(a, -1) {
		return b
	}
	if math.IsInf(a, 1) && math.IsInf(b, 1) {
		return a
	}
	if a > b {
		return a + detmath.Log1p(detmath.Exp(b-a))
	}
	return b + detmath.Log1p(detmath.Exp(a-b))
}

// initStepSize is Stan's step-size heuristic: double or halve eps until
// a single leapfrog step's acceptance crosses 0.8. The chain's point is
// left unchanged.
func (ch *chain) initStepSize() error {
	if ch.eps == 0 || ch.eps > 1e7 || math.IsNaN(ch.eps) {
		return nil
	}
	ch.zInit.set(&ch.z)
	trial := func() float64 {
		ch.z.set(&ch.zInit)
		ch.sampleMomentum(ch.z.p)
		h0 := ch.hamiltonian(&ch.z)
		ch.leapfrog(&ch.z, ch.eps)
		return h0 - ch.hamiltonian(&ch.z)
	}
	direction := -1
	if trial() > ch.logPoint8 {
		direction = 1
	}
	for {
		dH := trial()
		if direction == 1 && !(dH > ch.logPoint8) {
			break
		}
		if direction == -1 && !(dH < ch.logPoint8) {
			break
		}
		if direction == 1 {
			ch.eps = float64(2 * ch.eps)
		} else {
			ch.eps = float64(0.5 * ch.eps)
		}
		if ch.eps > 1e7 {
			ch.z.set(&ch.zInit)
			return runtimeErr("step_size_unbounded",
				"the step-size heuristic grew past 1e7; the posterior is likely improper", ch.index)
		}
		if ch.eps == 0 {
			ch.z.set(&ch.zInit)
			return runtimeErr("step_size_zero",
				"no acceptably small step size exists; the log density is likely discontinuous or its gradient wrong", ch.index)
		}
	}
	ch.z.set(&ch.zInit)
	return nil
}

// transition draws one NUTS transition from ch.z, leaves the selected
// point in ch.z (with ch.depth and ch.divergent describing the tree) and
// returns the transition's mean acceptance statistic.
func (ch *chain) transition() float64 {
	ch.sampleMomentum(ch.z.p)
	ch.zFwd.set(&ch.z)
	ch.zBck.set(&ch.z)
	ch.zSample.set(&ch.z)
	ch.zPropose.set(&ch.z)

	ch.pSharp(ch.z.p, ch.pSharpFwdFwd)
	for _, v := range [][]float64{ch.pFwdFwd, ch.pFwdBck, ch.pBckFwd, ch.pBckBck} {
		copy(v, ch.z.p)
	}
	for _, v := range [][]float64{ch.pSharpFwdBck, ch.pSharpBckFwd, ch.pSharpBckBck} {
		copy(v, ch.pSharpFwdFwd)
	}
	copy(ch.rho, ch.z.p)

	logSumWeight := 0.0 // log e^(H0 − H0)
	h0 := ch.hamiltonian(&ch.z)
	nLeapfrog := 0
	var sumMetroProb float64
	ch.depth = 0
	ch.divergent = false

	for ch.depth < ch.maxDepth {
		clear(ch.rhoFwd)
		clear(ch.rhoBck)
		logSumWeightSubtree := math.Inf(-1)
		var valid bool
		if ch.rng.Float64() > 0.5 {
			// Extend forward: the trajectory so far is the backward subtree.
			ch.z.set(&ch.zFwd)
			copy(ch.rhoBck, ch.rho)
			copy(ch.pBckFwd, ch.pFwdFwd)
			copy(ch.pSharpBckFwd, ch.pSharpFwdFwd)
			valid = ch.buildTree(ch.depth, &ch.zPropose, ch.pSharpFwdBck, ch.pSharpFwdFwd, ch.rhoFwd,
				ch.pFwdBck, ch.pFwdFwd, h0, 1, &nLeapfrog, &logSumWeightSubtree, &sumMetroProb)
			ch.zFwd.set(&ch.z)
		} else {
			// Extend backward: the trajectory so far is the forward subtree.
			ch.z.set(&ch.zBck)
			copy(ch.rhoFwd, ch.rho)
			copy(ch.pFwdBck, ch.pBckBck)
			copy(ch.pSharpFwdBck, ch.pSharpBckBck)
			valid = ch.buildTree(ch.depth, &ch.zPropose, ch.pSharpBckFwd, ch.pSharpBckBck, ch.rhoBck,
				ch.pBckFwd, ch.pBckBck, h0, -1, &nLeapfrog, &logSumWeightSubtree, &sumMetroProb)
			ch.zBck.set(&ch.z)
		}
		if !valid {
			break
		}
		ch.depth++

		// Biased progressive sampling of the new subtree.
		if logSumWeightSubtree > logSumWeight {
			ch.zSample.set(&ch.zPropose)
		} else if ch.rng.Float64() < detmath.Exp(logSumWeightSubtree-logSumWeight) {
			ch.zSample.set(&ch.zPropose)
		}
		logSumWeight = logSumExp(logSumWeight, logSumWeightSubtree)

		for i := range ch.rho {
			ch.rho[i] = ch.rhoBck[i] + ch.rhoFwd[i]
		}
		persist := uturnFree(ch.pSharpBckBck, ch.pSharpFwdFwd, ch.rho)
		for i := range ch.rhoExt {
			ch.rhoExt[i] = ch.rhoBck[i] + ch.pFwdBck[i]
		}
		persist = persist && uturnFree(ch.pSharpBckBck, ch.pSharpFwdBck, ch.rhoExt)
		for i := range ch.rhoExt {
			ch.rhoExt[i] = ch.rhoFwd[i] + ch.pBckFwd[i]
		}
		persist = persist && uturnFree(ch.pSharpBckFwd, ch.pSharpFwdFwd, ch.rhoExt)
		if !persist {
			break
		}
	}
	ch.z.set(&ch.zSample)
	return sumMetroProb / float64(nLeapfrog)
}

// buildTree builds a subtree of 2^depth leapfrog steps in direction sign
// from ch.z, multinomially sampling zPropose within it. "beg" is the end
// adjacent to the existing trajectory, "end" the far end; rho accumulates
// the subtree's summed momentum. It returns false when the subtree
// diverged or U-turned, which ends the transition.
func (ch *chain) buildTree(depth int, zPropose *point, pSharpBeg, pSharpEnd, rho, pBeg, pEnd []float64,
	h0, sign float64, nLeapfrog *int, logSumWeight, sumMetroProb *float64) bool {
	if depth == 0 {
		ch.leapfrog(&ch.z, sign*ch.eps)
		*nLeapfrog++
		h := ch.hamiltonian(&ch.z)
		if h-h0 > maxDeltaH {
			ch.divergent = true
		}
		*logSumWeight = logSumExp(*logSumWeight, h0-h)
		if h0-h > 0 {
			*sumMetroProb += 1
		} else {
			*sumMetroProb += detmath.Exp(h0 - h)
		}
		zPropose.set(&ch.z)
		ch.pSharp(ch.z.p, pSharpBeg)
		copy(pSharpEnd, pSharpBeg)
		for i, p := range ch.z.p {
			rho[i] += p
		}
		copy(pBeg, ch.z.p)
		copy(pEnd, ch.z.p)
		return !ch.divergent
	}

	lv := &ch.levels[depth]
	logSumWeightInit := math.Inf(-1)
	clear(lv.rhoInit)
	if !ch.buildTree(depth-1, zPropose, pSharpBeg, lv.pSharpInitEnd, lv.rhoInit, pBeg, lv.pInitEnd,
		h0, sign, nLeapfrog, &logSumWeightInit, sumMetroProb) {
		return false
	}

	lv.zFinal.set(&ch.z)
	logSumWeightFinal := math.Inf(-1)
	clear(lv.rhoFin)
	if !ch.buildTree(depth-1, &lv.zFinal, lv.pSharpFinalBeg, pSharpEnd, lv.rhoFin, lv.pFinalBeg, pEnd,
		h0, sign, nLeapfrog, &logSumWeightFinal, sumMetroProb) {
		return false
	}

	// Multinomial sample from the final subtree.
	logSumWeightSubtree := logSumExp(logSumWeightInit, logSumWeightFinal)
	*logSumWeight = logSumExp(*logSumWeight, logSumWeightSubtree)
	if logSumWeightFinal > logSumWeightSubtree {
		zPropose.set(&lv.zFinal)
	} else if ch.rng.Float64() < detmath.Exp(logSumWeightFinal-logSumWeightSubtree) {
		zPropose.set(&lv.zFinal)
	}

	for i := range lv.rhoSub {
		lv.rhoSub[i] = lv.rhoInit[i] + lv.rhoFin[i]
		rho[i] += lv.rhoSub[i]
	}
	persist := uturnFree(pSharpBeg, pSharpEnd, lv.rhoSub)
	for i := range lv.rhoExt {
		lv.rhoExt[i] = lv.rhoInit[i] + lv.pFinalBeg[i]
	}
	persist = persist && uturnFree(pSharpBeg, lv.pSharpFinalBeg, lv.rhoExt)
	for i := range lv.rhoExt {
		lv.rhoExt[i] = lv.rhoFin[i] + lv.pInitEnd[i]
	}
	persist = persist && uturnFree(lv.pSharpInitEnd, pSharpEnd, lv.rhoExt)
	return persist
}
