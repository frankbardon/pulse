// Package linalg is Pulse's one linear-algebra core: Pulse-owned dense
// (Matrix), symmetric (Sym) and vector (Vec) types plus the kernels the
// engine and the synthetic-data generator factor, solve and invert with.
//
// # Two backends
//
// The REFERENCE kernels — Cholesky, CholeskyRidge, SolveSPD,
// InverseSPD and the CoMoment accumulator's Add and Merge — are pure Go
// and FMA-free: every product that feeds an addition or subtraction is
// written as an explicit float64(a*b) conversion, which the Go
// specification forbids the compiler from fusing into a fused
// multiply-add. Their output is therefore bit-identical on every
// architecture, and a caller with a bit-level contract (same seed, same
// bytes) can rely on it.
//
// Routines with no bit contract — SymEigen, SVD, QR, Rank and
// ConditionNumber, plus the gonum-backed SPD path FactorSPD (an
// SPDFactor solved against and inverted) and the dense product Mul —
// are backed by gonum and may differ in the last ulps across
// architectures. On one machine the SPD path and Mul equal the raw gonum
// calls bit for bit (they are thin wrappers, with no policy layered on
// top), which is what lets the regression engine route through them
// with byte-identical output. No gonum type ever appears on this
// package's exported surface: gonum is pre-1.0, and Pulse's public API
// is frozen.
//
// # Order, sign and rank policy
//
// gonum's raw output is canonicalised so every decision above rounding
// is the same on every architecture:
//
//   - Order: eigenvalues and singular values are descending. Values
//     within the rank tolerance of their neighbour form a tie cluster,
//     ordered by original variable order — the index of each vector's
//     dominant component, lowest first.
//   - Sign: every eigenvector, and every right singular vector, is
//     flipped so its largest-magnitude (dominant) component is positive;
//     a magnitude tie (within DominanceTolerance, relative) goes to the
//     lowest index. A singular pair flips U and V together. QR flips so
//     R's diagonal is non-negative.
//   - Rank: one tolerance, RankTolerance = max(rows, cols)·Epsilon·σ_max;
//     a singular value at or below it is zero. Rank uses it unless the
//     caller passes an explicit tol > 0, and ConditionNumber reports
//     +Inf for a matrix that is rank-deficient under it.
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
// ridge (DefaultRidgeSchedule: eight tries, increments 1e-6 … 1e1) and
// returns the ACCUMULATED ridge, so the figure reads as "how far from
// positive definite was this", not as an iteration count.
//
// # Co-moments
//
// CoMoment accumulates weighted means and covariances of p variables,
// Listwise (complete rows) or Pairwise (per-pair complete rows), and
// merges exactly (Chan–Golub–LeVeque), so partial accumulators built by
// workers combine to the serial figures. Weights follow Pulse's
// weighting contract: NaN, ±Inf and negative weights are skipped and
// counted (NWeightInvalid); a zero weight counts toward N but carries
// no mass.
//
// # Errors
//
// Every failure is a *errors.CodedError: PULSE_MATRIX_SHAPE_MISMATCH for
// operands whose dimensions disagree (or a nil operand), and
// PULSE_MATRIX_SINGULAR for a matrix the kernel cannot factor or invert.
// A gonum-backed routine handed a NaN or infinite element, or whose
// iteration does not converge, also reports PULSE_MATRIX_SINGULAR, with
// details "reason" = "non_finite" / "no_convergence".
//
// # Imports
//
// linalg imports only the standard library, gonum and Pulse's errors
// package (gated by TestLinalgImportBoundary).
package linalg
