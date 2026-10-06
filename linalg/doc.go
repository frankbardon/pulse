// Package linalg is Pulse's one linear-algebra core: Pulse-owned dense
// (Matrix), symmetric (Sym) and vector (Vec) types plus the kernels the
// engine and the synthetic-data generator factor, solve and invert with.
//
// # Two backends
//
// The REFERENCE kernels — Cholesky, CholeskyRidge, SolveSPD and
// InverseSPD — are pure Go and FMA-free: every product that feeds an
// addition or subtraction is written as an explicit float64(a*b)
// conversion, which the Go specification forbids the compiler from
// fusing into a fused multiply-add. Their output is therefore
// bit-identical on every architecture, and a caller with a bit-level
// contract (same seed, same bytes) can rely on it.
//
// Routines with no bit contract (eigen-decomposition, SVD, QR, rank,
// condition number) may be backed by gonum and may differ in the last
// ulps across architectures. No gonum type ever appears on this
// package's exported surface: gonum is pre-1.0, and Pulse's public API
// is frozen.
//
// # Reference Cholesky
//
// Cholesky is the lower-triangular Banachiewicz (row-by-row) factor
// S = L·Lᵀ. For row i and column j ≤ i it starts from S[i][j],
// subtracts L[i][k]·L[j][k] for k ascending one term at a time, and then
// either takes the square root (diagonal) or divides by L[j][j] with a
// true division (off-diagonal). It fails iff a diagonal pivot is ≤ 0.
// That operation order is a contract: changing it changes bits.
//
// CholeskyRidge retries a failed factorisation with a growing diagonal
// ridge (DefaultRidgeSchedule: eight tries, increments 1e-6 … 1e-1) and
// returns the ACCUMULATED ridge, so the figure reads as "how far from
// positive definite was this", not as an iteration count.
//
// # Errors
//
// Every failure is a *errors.CodedError: PULSE_MATRIX_SHAPE_MISMATCH for
// operands whose dimensions disagree (or a nil operand), and
// PULSE_MATRIX_SINGULAR for a matrix the kernel cannot factor or invert.
//
// # Imports
//
// linalg imports only the standard library, gonum and Pulse's errors
// package (gated by TestLinalgImportBoundary).
package linalg
