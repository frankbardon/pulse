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
// NUTS is the package's Sampler: Stan's multinomial No-U-Turn sampler
// with a diagonal metric, dual-averaging step-size adaptation and Stan's
// windowed metric adaptation during warmup. Each chain draws from its own
// PCG stream derived from (seed, chain index); chains run on concurrent
// goroutines and merge in chain order, so a seed reproduces its Result
// bit for bit on every architecture and for any Workers or GOMAXPROCS.
// TestNUTS_GoldenHash pins the draw bits of fixed runs.
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
// compare energies, and one ulp of drift in the density changes a draw.
package mcmc
