// Package optimize is Pulse's public numerical-optimization core: solvers
// an embedder (and Pulse's own estimators) call with plain Go callbacks.
//
// Minimize is SLSQP — Kraft's Sequential Least Squares Programming, a
// statement-by-statement port of SciPy 1.18.1's C source (BSD-3-Clause;
// SCIPY_LICENSE.txt travels with the code) — for a smooth objective
// under simple bounds plus any number of equality (Fn(x) = 0) and
// inequality (Fn(x) ≥ 0) constraints. It reports the Lagrange multiplier
// of every constraint and which bounds bind. Gradients are the caller's.
//
// # Determinism
//
// The arithmetic is FMA-free: every product that feeds an addition or
// subtraction is an explicit float64(a*b), which the Go specification
// forbids the compiler from fusing into a fused multiply-add
// (TestOptimize_NoFMAContraction holds the barriers). A problem whose
// callbacks are themselves deterministic therefore gives the same bits
// on every architecture, serial or not.
//
// # Errors
//
// Bad input is a PULSE_OPTIMIZE_INVALID *errors.CodedError and no
// result. A run that stops short returns its final iterate together with
// PULSE_OPTIMIZE_NOT_CONVERGED or PULSE_OPTIMIZE_INFEASIBLE; the solver
// itself never panics.
package optimize
