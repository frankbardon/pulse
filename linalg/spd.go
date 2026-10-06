package linalg

import (
	perr "github.com/frankbardon/pulse/errors"
	"gonum.org/v1/gonum/mat"
)

// The routines in this file are the gonum-backed SPD path: a Cholesky
// factorisation that is solved against and inverted, plus the dense
// product used to sandwich an inverse. They are DISTINCT from the
// reference kernels (Cholesky, SolveSPD, InverseSPD), which are pure Go
// and FMA-free.
//
// Bit contract: none across architectures — gonum's assembly kernels and
// the compiler's fused multiply-add choices differ between arm64 and
// amd64. On any ONE machine, however, every routine here is a thin
// wrapper over the corresponding gonum call on the same operands, so its
// output equals the raw gonum output bit for bit. Pulse's regression
// engine relies on that to route through linalg without moving a single
// output bit.

// ConditionTolerance is the condition-number ceiling of the gonum-backed
// SPD path: a factor whose estimated condition number exceeds it still
// factors, but its Solve and Inverse report PULSE_MATRIX_SINGULAR because
// their results carry no reliable digit. It equals gonum's own
// threshold.
const ConditionTolerance = 1e16

// SPDFactor is the gonum-backed Cholesky factorisation of a symmetric
// positive-definite matrix, built by FactorSPD and reused across any
// number of Solve and Inverse calls. It owns its storage: mutating the
// Sym it was built from does not affect it.
type SPDFactor struct {
	n    int
	chol mat.Cholesky
}

// FactorSPD returns the gonum-backed Cholesky factorisation of the
// symmetric positive-definite s. Only the upper triangle is consulted,
// which is all a Sym stores.
//
// A matrix that is not positive definite — a pivot that is ≤ 0 or NaN,
// which covers a singular, indefinite or NaN-bearing matrix — is
// PULSE_MATRIX_SINGULAR (details "reason" = "not_positive_definite").
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
	full := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			full[i*n+j] = s.data[s.index(i, j)]
		}
	}
	if !f.chol.Factorize(mat.NewSymDense(n, full)) {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
			"linalg: matrix is not positive definite (Cholesky factorisation failed)",
			map[string]any{"reason": "not_positive_definite", "n": n})
	}
	return f, nil
}

// N returns the order of the factored matrix.
func (f *SPDFactor) N() int { return f.n }

// ConditionNumber returns gonum's 1-norm estimate of the factored
// matrix's condition number (1 for an empty factor). Solve and Inverse
// fail when it exceeds ConditionTolerance.
func (f *SPDFactor) ConditionNumber() float64 {
	if f.n == 0 {
		return 1
	}
	return f.chol.Cond()
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
	var x mat.VecDense
	if err := f.chol.SolveVecTo(&x, mat.NewVecDense(f.n, b.Slice())); err != nil {
		return nil, conditionError(err, "solve")
	}
	out := make([]float64, f.n)
	for i := range out {
		out[i] = x.AtVec(i)
	}
	return &Vec{data: out}, nil
}

// Inverse returns A⁻¹ for the factored A. A factor whose condition
// number exceeds ConditionTolerance, or whose triangular inverse breaks
// down, is PULSE_MATRIX_SINGULAR with the condition estimate in details
// "condition_number" (+Inf for a breakdown).
func (f *SPDFactor) Inverse() (*Sym, error) {
	if f.n == 0 {
		return &Sym{}, nil
	}
	var inv mat.SymDense
	if err := f.chol.InverseTo(&inv); err != nil {
		return nil, conditionError(err, "inverse")
	}
	n := f.n
	out := &Sym{n: n, data: make([]float64, n*(n+1)/2)}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			out.data[out.index(i, j)] = inv.At(i, j)
		}
	}
	return out, nil
}

// Mul returns the dense product a·b, gonum-backed (no cross-architecture
// bit contract). A nil operand or a.Cols() != b.Rows() is
// PULSE_MATRIX_SHAPE_MISMATCH; a zero dimension yields the zero matrix
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
	var p mat.Dense
	p.Mul(mat.NewDense(a.rows, a.cols, a.RowMajor()), mat.NewDense(b.rows, b.cols, b.RowMajor()))
	for i := 0; i < a.rows; i++ {
		for j := 0; j < b.cols; j++ {
			out.data[i*b.cols+j] = p.At(i, j)
		}
	}
	return out, nil
}

// conditionError translates gonum's condition report into
// PULSE_MATRIX_SINGULAR carrying details "condition_number".
func conditionError(err error, what string) error {
	cond, ok := err.(mat.Condition)
	if !ok {
		return perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
			"linalg: SPD "+what+" failed: "+err.Error(),
			map[string]any{"reason": "backend_error"})
	}
	return perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
		"linalg: SPD "+what+" is unreliable: the matrix is singular or near-singular",
		map[string]any{"reason": "ill_conditioned", "condition_number": float64(cond)})
}
