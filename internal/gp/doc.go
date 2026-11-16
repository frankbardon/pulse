// Package gp is the Gaussian-process surrogate behind the compose sweep's
// Bayesian-optimisation mode: a zero-mean GP over a mixed search space
// (continuous unit-interval dimensions plus one-hot categorical ones),
// with a Matérn-5/2 ARD kernel, hyperparameters fitted by maximum
// marginal likelihood (ML-II), and the posterior mean and variance an
// acquisition function reads.
//
// It is INTERNAL (not part of the frozen public surface) and a leaf over
// the standard library, linalg, linalg/detmath, optimize and
// internal/regresample (TestGP_ImportBoundary).
//
// # Model
//
// The model is scikit-learn's GaussianProcessRegressor with
// normalize_y=True, alpha = Nugget and the kernel
//
//	ConstantKernel(Amplitude) * Matern(LengthScales, nu=2.5) + WhiteKernel(Noise)
//
// over the encoded inputs (Space.Encode): y is standardised by its mean
// and population standard deviation (a deviation below 10·ε reads as 1),
// the covariance of two encoded points at scaled distance
// r = sqrt(Σ_c ((x_c − x'_c)/ℓ_g(c))²) is
// Amplitude·(1 + √5·r + 5r²/3)·exp(−√5·r), and the training covariance adds
// Noise + Nugget on its diagonal. A categorical dimension's one-hot
// columns share ONE length scale (a tied ARD group), so a dimension —
// continuous or categorical — is one hyperparameter, whatever its level
// count.
//
// The training covariance is factored by linalg.CholeskyRidge: a factor
// that fails (duplicate points under a tiny noise) is retried with the
// default ridge schedule added to the diagonal, and Model.Ridge reports
// the accumulated jitter. Duplicate observations are therefore accepted.
//
// # Fitting
//
// Fit maximises the log marginal likelihood over the hyperparameters'
// logarithms θ = (log Amplitude, log ℓ_0 … log ℓ_{D−1}, log Noise) within
// box bounds, by optimize.Minimize (SLSQP) on −LML with its analytic
// gradient, from a fixed set of starts: the Initial hyperparameters, then
// Restarts−1 points drawn uniformly in the log-bounds box from one PCG
// stream seeded (Seed, Seed ^ regresample.PCGStream), raw words only.
// The start with the highest final LML wins; a tie keeps the earlier
// start.
//
// # Determinism
//
// Every product that feeds an addition or subtraction is an explicit
// float64(a*b), exp and log are linalg/detmath's, the factor is linalg's
// reference Cholesky and the solver is FMA-free SLSQP, all serial and in
// a fixed order: a fit's bits are a function of its inputs alone, on
// every architecture (internal/fingerprint pins them).
package gp
