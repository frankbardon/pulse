package linalg

import (
	"math"

	perr "github.com/frankbardon/pulse/errors"
)

// Cholesky returns the lower-triangular factor L of the symmetric
// positive-definite s, with s = L·Lᵀ and zeros above the diagonal.
//
// It is a REFERENCE kernel: pure Go, FMA-free, bit-identical on every
// architecture. The operation order is the contract — Banachiewicz row
// order (i ascending, then j = 0..i), a running subtraction
// sum -= L[i][k]·L[j][k] over k ascending, a square root on the diagonal
// and a true division sum / L[j][j] off it. Only the lower triangle of s
// is read.
//
// A diagonal pivot ≤ 0 is PULSE_MATRIX_SINGULAR (details "pivot", the
// failing row, plus the singular diagnostics — "rank",
// "condition_number" and, where identifiable, "dependent_indices"; see
// singularDiagnostics). A NaN pivot is not ≤ 0 and propagates into L,
// exactly as the comparison reads. A nil s is
// PULSE_MATRIX_SHAPE_MISMATCH; an empty s factors to an empty L.
//
// The diagnostics run only on the failure path, through the
// gonum-backed SymEigen; they never touch L, so the factor's bit
// contract is unchanged.
func Cholesky(s *Sym) (*Matrix, error) {
	if s == nil {
		return nil, nilOperand("symmetric")
	}
	L, pivot := cholesky(s)
	if L == nil {
		details := map[string]any{"pivot": pivot}
		singularDiagnostics(s, details)
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
			"linalg: matrix is not positive definite (non-positive Cholesky pivot)",
			details)
	}
	return L, nil
}

// cholesky is the shared factor loop. It returns (nil, i) when row i's
// pivot is ≤ 0.
func cholesky(s *Sym) (*Matrix, int) {
	n := s.n
	L := &Matrix{rows: n, cols: n, data: make([]float64, n*n)}
	l := L.data
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := s.data[s.index(j, i)]
			for k := 0; k < j; k++ {
				sum -= float64(l[i*n+k] * l[j*n+k])
			}
			if i == j {
				if sum <= 0 {
					return nil, i
				}
				l[i*n+j] = math.Sqrt(sum)
			} else {
				l[i*n+j] = sum / l[j*n+j]
			}
		}
	}
	return L, -1
}

// RidgeSchedule is the sequence of diagonal increments CholeskyRidge
// adds between attempts: attempt t runs with the first t increments
// already added, so a schedule of length m makes m attempts. Increments
// are the caller's responsibility; they are added as given.
type RidgeSchedule []float64

// DefaultRidgeSchedule returns a fresh copy of the default schedule:
// eight attempts, increments 10^(t-6) for t = 0..7 (1e-6 … 1e1), so a
// matrix that fails every attempt reports a total ridge of 11.111111.
func DefaultRidgeSchedule() RidgeSchedule {
	out := make(RidgeSchedule, 8)
	for ridge := range out {
		out[ridge] = math.Pow(10, float64(ridge-6))
	}
	return out
}

// CholeskyRidge factors s like Cholesky, retrying a failed attempt with
// the next schedule increment added to every diagonal element (a nil or
// empty schedule means DefaultRidgeSchedule). It returns the factor and
// the ACCUMULATED ridge — the sum of the increments added before the
// successful attempt, 0 when the first attempt succeeds. s itself is
// never modified.
//
// When every attempt fails the error is PULSE_MATRIX_SINGULAR (details
// "attempts" and "ridge") and the returned ridge is the total of the
// whole schedule, including the increment added after the last attempt.
func CholeskyRidge(s *Sym, schedule RidgeSchedule) (*Matrix, float64, error) {
	if s == nil {
		return nil, 0, nilOperand("symmetric")
	}
	if len(schedule) == 0 {
		schedule = DefaultRidgeSchedule()
	}
	work := s.clone()
	total := 0.0
	for _, jitter := range schedule {
		if L, _ := cholesky(work); L != nil {
			return L, total, nil
		}
		for i := 0; i < work.n; i++ {
			work.data[work.index(i, i)] += jitter
		}
		total += jitter
	}
	return nil, total, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
		"linalg: matrix is not positive definite even after ridge regularization",
		map[string]any{"attempts": len(schedule), "ridge": total})
}

// SolveSPD solves s·x = b for the symmetric positive-definite s through
// its reference Cholesky factor: forward substitution L·y = b, then back
// substitution Lᵀ·x = y, each a running subtraction over k ascending
// followed by a true division by the diagonal. FMA-free.
//
// A nil operand or len(b) != order(s) is PULSE_MATRIX_SHAPE_MISMATCH; a
// factorisation failure is PULSE_MATRIX_SINGULAR.
func SolveSPD(s *Sym, b *Vec) (*Vec, error) {
	if s == nil {
		return nil, nilOperand("symmetric")
	}
	if b == nil {
		return nil, nilOperand("vector")
	}
	if len(b.data) != s.n {
		return nil, shapeError("right-hand side length does not equal the matrix order",
			map[string]any{"n": s.n, "len": len(b.data)})
	}
	L, err := Cholesky(s)
	if err != nil {
		return nil, err
	}
	n := s.n
	l := L.data
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := b.data[i]
		for k := 0; k < i; k++ {
			sum -= float64(l[i*n+k] * y[k])
		}
		y[i] = sum / l[i*n+i]
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := y[i]
		for k := i + 1; k < n; k++ {
			sum -= float64(l[k*n+i] * x[k])
		}
		x[i] = sum / l[i*n+i]
	}
	return &Vec{data: x}, nil
}

// InverseSPD returns s⁻¹ for the symmetric positive-definite s, computed
// as L⁻ᵀ·L⁻¹ from the reference Cholesky factor so the result is
// symmetric by construction. L⁻¹ is built column by column with a
// running subtraction over k ascending and a true division by the
// diagonal; each (i, j ≥ i) element of the inverse is then a running
// sum of L⁻¹[k][i]·L⁻¹[k][j] over k ascending from j. FMA-free.
//
// A nil s is PULSE_MATRIX_SHAPE_MISMATCH; a factorisation failure is
// PULSE_MATRIX_SINGULAR.
func InverseSPD(s *Sym) (*Sym, error) {
	if s == nil {
		return nil, nilOperand("symmetric")
	}
	L, err := Cholesky(s)
	if err != nil {
		return nil, err
	}
	n := s.n
	l := L.data
	inv := make([]float64, n*n) // L⁻¹, lower triangular
	for j := 0; j < n; j++ {
		inv[j*n+j] = 1 / l[j*n+j]
		for i := j + 1; i < n; i++ {
			sum := 0.0
			for k := j; k < i; k++ {
				sum -= float64(l[i*n+k] * inv[k*n+j])
			}
			inv[i*n+j] = sum / l[i*n+i]
		}
	}
	out := &Sym{n: n, data: make([]float64, n*(n+1)/2)}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			sum := 0.0
			for k := j; k < n; k++ {
				sum += float64(inv[k*n+i] * inv[k*n+j])
			}
			out.data[out.index(i, j)] = sum
		}
	}
	return out, nil
}
