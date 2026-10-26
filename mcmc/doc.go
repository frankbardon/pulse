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
// barriers), and every transcendental is linalg/detmath's, so the same
// input gives the same bits on every architecture.
package mcmc
