// Package mcmc is Pulse's public Markov chain Monte Carlo core: the
// interfaces a sampler and a target density meet at, the
// change-of-variables helpers a model author needs to put a constrained
// parameter on the unconstrained real line, and the convergence
// diagnostics every sampler's draws are judged by.
//
// A Model is a log density (up to a constant) on the unconstrained space
// R^Dim together with its gradient. A Sampler draws from a Model under a
// seed and returns the draws per chain plus Diagnostics. The package knows
// nothing about any particular statistical model.
//
// # NUTS
//
// NUTS is the package's default Sampler: Stan's multinomial No-U-Turn sampler
// with a diagonal metric, dual-averaging step-size adaptation and Stan's
// windowed metric adaptation during warmup. Each chain draws from its own
// PCG stream derived from (seed, chain index); chains run on concurrent
// goroutines and merge in chain order, so a seed reproduces its Result
// bit for bit on every architecture and for any Workers or GOMAXPROCS.
// TestNUTS_GoldenHash pins the draw bits of fixed runs.
//
// The zero value NUTS{} is usable: a zero Options field takes the exported
// default (DefaultChains 4, DefaultWarmup 1000, DefaultDraws 1000,
// DefaultMaxTreeDepth 10, DefaultTargetAccept 0.8), so Warmup cannot be
// set to 0. The realised MeanAcceptStat sits above the target (about 0.89
// at 0.8) because the retained step size is the averaged iterate, as in
// Stan. One deliberate departure from Stan's source: when Warmup < 150,
// Stan's short-warmup branch never re-initialises the slow adaptation
// window, so the metric never adapts; NUTS sets the window up as Stan
// intends so that it does. E-BFMI and per-draw energy are not recorded.
//
// Options.Constrain is called concurrently, one goroutine per chain, and
// the Model must be safe for concurrent use. A panic in the Model is
// re-raised on the caller's goroutine; ctx is checked every iteration.
// Refusals are coded errors carrying details["reason"]: invalid options,
// model shape or Init are PULSE_MCMC_INVALID, a model the sampler cannot
// run (no finite start, a step size that collapses or grows without
// bound, a non-finite adapted metric) is PULSE_MCMC_FAILED with
// details["chain"].
//
// See the Example for a complete run.
//
// # HMC
//
// HMC is static Hamiltonian Monte Carlo: a fixed path of
// ⌈PathLength/StepSize⌉ leapfrog steps per transition (PathLength
// defaults to DefaultPathLength, π/2) followed by a Metropolis
// accept/reject. Its warmup adapts the diagonal metric exactly as NUTS's
// does and, when StepSize is 0, the step size by the same dual averaging
// towards TargetAccept; a non-zero StepSize is used as given. Chain
// seeding, the worker pool, the chain-order merge and the ctx checks are
// NUTS's own code, so HMC holds the same bit-for-bit guarantee
// (TestHMC_GoldenHash). A transition whose energy error passes 1000 is a
// divergence and is rejected. HMC builds no tree, so its ChainStats carry
// a nil TreeDepth (the marker that the tree fields do not apply) and a
// TreeDepthSaturated of 0 that means nothing; Divergences and Divergent
// are recorded as for NUTS. A path that needs more than 65536 leapfrog
// steps is refused: PULSE_MCMC_INVALID reason path_length for a fixed
// StepSize, PULSE_MCMC_FAILED reason path_too_long for an adapted one
// (during warmup such a step size runs 65536 steps). Other refusals are
// NUTS's, with HMC's step_size and path_length reasons for a negative or
// non-finite knob. A static path suits a posterior the diagonal metric
// makes nearly isotropic; a correlated one wants a longer PathLength,
// and NUTS chooses the length per transition instead.
//
// # Transforms
//
// Positive, Unit and Bounded map an unconstrained x to a constrained y
// and report log|dy/dx| (the log-Jacobian a model adds to its log density
// so the sampler targets the right distribution) and its derivative, so a
// hand-written gradient can chain through them. Inverse maps back.
//
// # Diagnostics
//
// RHat is the rank-normalised split R̂ of Vehtari, Gelman, Simpson,
// Carpenter and Bürkner (2021), reported as the maximum of the bulk
// (rank-normalised) and folded (rank-normalised |θ − median|) values.
// ESSBulk is the effective sample size of the rank-normalised split
// chains; ESSTail is the smaller of the effective sample sizes of the 5%
// and 95% quantile indicators. Both use Geyer's initial positive sequence
// truncated by the initial monotone sequence over the direct (not FFT)
// autocovariance, and match ArviZ (arviz-stats 1.3.3) on the committed
// fixture chains (mcmc/testdata/gen_diagnostics.py).
//
// # Determinism
//
// The arithmetic is FMA-free: every product that feeds an addition or
// subtraction is an explicit float64(a*b), which the Go specification
// forbids the compiler from fusing (TestMCMC_NoFMAContraction holds the
// barriers), every transcendental and normal draw is linalg/detmath's,
// and every sum runs in a fixed order, so the same input gives the same
// bits on every architecture. A Model must hold to the same rules for its
// draws to be reproducible across architectures: NUTS's tree decisions
// and HMC's accept/reject compare energies, and one ulp of drift in the
// density changes a draw.
package mcmc
