package linalg

import (
	"math"

	perr "github.com/frankbardon/pulse/errors"
)

// The routines in this file are the SPD path the regression engine
// runs: a Cholesky factorisation that is solved against and inverted,
// plus the dense product used to sandwich an inverse. They are DISTINCT
// from the reference kernels (Cholesky, SolveSPD, InverseSPD), which use
// a different (Banachiewicz) operation order.
//
// Bit contract: identical bits on every architecture. Each routine is an
// FMA-free pure-Go port of the operation order gonum v0.17.0's
// mat.Cholesky (Factorize, Cond, SolveVecTo, InverseTo) and
// mat.Dense.Mul run on linux/amd64 — LAPACK Dlansy, Dpotrf, Dpocon,
// Dpotrs, Dpotri over gonum's BLAS and its SSE2 assembly kernels
// (gonumorder_blas.go, gonumorder_lapack.go). So on amd64 every output
// equals the raw gonum call bit for bit (TestSPD_MatchesGonumOrder), and
// arm64 — where gonum's pure Go was contracted into fused multiply-adds
// — now computes those same bits. The refusals are gonum's too: the
// same pivot test and the same Dpocon condition estimate against
// ConditionTolerance.

// ConditionTolerance is the condition-number ceiling of the SPD path: a
// factor whose estimated condition number exceeds it still factors, but
// its Solve and Inverse report PULSE_MATRIX_SINGULAR because their
// results carry no reliable digit. It equals gonum's own
// threshold.
const ConditionTolerance = 1e16

// SPDFactor is the Cholesky factorisation (A = Uᵀ·U) of a symmetric
// positive-definite matrix, built by FactorSPD and reused across any
// number of Solve and Inverse calls. It owns its storage: mutating the
// Sym it was built from does not affect it.
type SPDFactor struct {
	n    int
	u    []float64 // n×n row-major; the upper triangle holds U
	cond float64   // 1 / Dpocon's reciprocal condition estimate
}

// FactorSPD returns the Cholesky factorisation of the symmetric
// positive-definite s (gonum's Dpotrf order, see the file comment). Only
// the upper triangle is consulted, which is all a Sym stores.
//
// A matrix that is not positive definite — a pivot that is ≤ 0 or NaN,
// which covers a singular, indefinite or NaN-bearing matrix — is
// PULSE_MATRIX_SINGULAR (details "reason" = "not_positive_definite",
// "n", plus the singular diagnostics "rank", "condition_number" and,
// where identifiable, "dependent_indices" — see singularDiagnostics;
// none of them for a NaN-bearing matrix).
// There is no separate finiteness pre-check: an infinite element is
// handed to the factorisation as-is. A nil s is
// PULSE_MATRIX_SHAPE_MISMATCH; an empty s factors to an empty factor.
func FactorSPD(s *Sym) (*SPDFactor, error) {
	if s == nil {
		return nil, nilOperand("symmetric")
	}
	f := &SPDFactor{n: s.n}
	if s.n == 0 {
		return f, nil
	}
	n := s.n
	u := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			u[i*n+j] = s.data[s.index(i, j)]
		}
	}
	norm := goDlansyUpperMaxRowSum(n, u, n, make([]float64, n))
	if !goDpotrfUpper(n, u, n) {
		details := map[string]any{"reason": "not_positive_definite", "n": n}
		singularDiagnostics(s, details)
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
			"linalg: matrix is not positive definite (Cholesky factorisation failed)",
			details)
	}
	f.u = u
	f.cond = 1 / goDpoconUpper(n, u, n, norm)
	return f, nil
}

// N returns the order of the factored matrix.
func (f *SPDFactor) N() int { return f.n }

// ConditionNumber returns the 1-norm estimate (LAPACK Dpocon, gonum's
// estimator) of the factored matrix's condition number (1 for an empty
// factor). Solve and Inverse fail when it exceeds ConditionTolerance.
func (f *SPDFactor) ConditionNumber() float64 {
	if f.n == 0 {
		return 1
	}
	return f.cond
}

// Solve returns x with A·x = b for the factored A. A nil b or
// len(b) != N() is PULSE_MATRIX_SHAPE_MISMATCH. A factor whose condition
// number exceeds ConditionTolerance is PULSE_MATRIX_SINGULAR, with the
// estimate in details "condition_number".
func (f *SPDFactor) Solve(b *Vec) (*Vec, error) {
	if b == nil {
		return nil, nilOperand("vector")
	}
	if len(b.data) != f.n {
		return nil, shapeError("right-hand side length does not equal the matrix order",
			map[string]any{"n": f.n, "len": len(b.data)})
	}
	if f.n == 0 {
		return &Vec{}, nil
	}
	x := b.Slice()
	goDpotrsUpperVec(f.n, f.u, f.n, x)
	if f.cond > ConditionTolerance {
		return nil, conditionError(f.cond, "solve")
	}
	return &Vec{data: x}, nil
}

// Inverse returns A⁻¹ for the factored A. A factor whose condition
// number exceeds ConditionTolerance, or whose triangular inverse breaks
// down, is PULSE_MATRIX_SINGULAR with the condition estimate in details
// "condition_number" (+Inf for a breakdown).
func (f *SPDFactor) Inverse() (*Sym, error) {
	if f.n == 0 {
		return &Sym{}, nil
	}
	n := f.n
	inv := make([]float64, n*n)
	for i := 0; i < n; i++ {
		copy(inv[i*n+i:(i+1)*n], f.u[i*n+i:(i+1)*n])
	}
	if !goDpotriUpper(n, inv, n) {
		return nil, conditionError(math.Inf(1), "inverse")
	}
	if f.cond > ConditionTolerance {
		return nil, conditionError(f.cond, "inverse")
	}
	out := &Sym{n: n, data: make([]float64, n*(n+1)/2)}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			out.data[out.index(i, j)] = inv[i*n+j]
		}
	}
	return out, nil
}

// Mul returns the dense product a·b in gonum Dgemm's operation order
// (identical bits on every architecture; see the file comment). A nil
// operand or a.Cols() != b.Rows() is PULSE_MATRIX_SHAPE_MISMATCH; a zero dimension yields the zero matrix
// of shape a.Rows()×b.Cols().
func Mul(a, b *Matrix) (*Matrix, error) {
	if a == nil {
		return nil, nilOperand("left matrix")
	}
	if b == nil {
		return nil, nilOperand("right matrix")
	}
	if a.cols != b.rows {
		return nil, shapeError("inner dimensions of a product must agree",
			map[string]any{"left_cols": a.cols, "right_rows": b.rows})
	}
	out := &Matrix{rows: a.rows, cols: b.cols, data: make([]float64, a.rows*b.cols)}
	if a.rows == 0 || a.cols == 0 || b.cols == 0 {
		return out, nil
	}
	goDgemm(false, false, a.rows, b.cols, a.cols, 1, a.data, a.cols, b.data, b.cols, 0, out.data, b.cols)
	return out, nil
}

// conditionError is PULSE_MATRIX_SINGULAR for a solve or inverse whose
// factor's condition estimate exceeds ConditionTolerance (or, +Inf, whose
// triangular inverse broke down), carrying details "condition_number".
func conditionError(cond float64, what string) error {
	return perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
		"linalg: SPD "+what+" is unreliable: the matrix is singular or near-singular",
		map[string]any{"reason": "ill_conditioned", "condition_number": cond})
}
