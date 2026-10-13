package linalg

import (
	"math"

	perr "github.com/frankbardon/pulse/errors"
)

// LUFactor is the reference LU factorisation P·A = L·U of a general
// (not necessarily symmetric or positive-definite) square matrix, built
// by FactorLU with partial pivoting and reused across any number of
// Solve, SolveMatrix and Inverse calls. L is unit lower triangular, U
// upper triangular, P the row permutation chosen by the pivoting. It
// owns its storage: mutating the Matrix it was built from does not
// affect it.
//
// The factor and every solve against it are a REFERENCE kernel: pure
// Go, FMA-free, bit-identical on every architecture (see FactorLU for
// the operation order).
type LUFactor struct {
	n    int
	lu   []float64 // L strictly below the diagonal (unit diagonal implied), U on and above
	perm []int     // perm[i] is the row of A that sits in row i of P·A
	cond float64
}

// FactorLU returns the reference LU factorisation of the square matrix
// a, P·a = L·U, by Doolittle elimination with partial pivoting.
//
// Operation order (the bit contract): for each column k ascending, the
// pivot is the row i ≥ k with the largest |a[i][k]| (a magnitude tie
// goes to the lowest row); that row is swapped into row k; then for every
// row i > k ascending the multiplier l = a[i][k] / a[k][k] is a true
// division, and a[i][j] -= l·a[k][j] runs over j > k ascending, one
// FMA-free term at a time.
//
// Singularity policy — the ONE rank tolerance: a is refused when it is
// rank-deficient under RankTolerance(n, n, σ_max), i.e. exactly when
// ConditionNumber(a) is +Inf, and (defensively) when a pivot is exactly
// zero. The decision rides the gonum-backed singular values, so — like
// every Rank decision in this package — it is portable except within an
// ulp of the tolerance itself; the factor and solution VALUES are
// FMA-free and bit-identical everywhere. A full-rank but ill-conditioned
// matrix still factors: a solve against it loses roughly log10(κ)
// digits, and LUFactor.ConditionNumber reports κ for a caller that
// wants a stricter reliability gate.
//
// Errors: a nil a, or a non-square a, is PULSE_MATRIX_SHAPE_MISMATCH. A
// NaN or infinite element is PULSE_MATRIX_SINGULAR with details
// "reason" = "non_finite". A rank-deficient a is PULSE_MATRIX_SINGULAR
// with details "reason" = "singular", "n", "rank" (Rank(a, 0)) and
// "condition_number" (+Inf, null on the wire); a zero pivot adds
// "pivot" (the failing column). A refused matrix returns an error, never
// a NaN-bearing factor or solution. An empty a factors to an empty
// factor.
func FactorLU(a *Matrix) (*LUFactor, error) {
	if a == nil {
		return nil, nilOperand("matrix")
	}
	if a.rows != a.cols {
		return nil, shapeError("LU factorisation needs a square matrix",
			map[string]any{"rows": a.rows, "cols": a.cols})
	}
	n := a.rows
	f := &LUFactor{n: n, lu: a.RowMajor(), perm: make([]int, n), cond: 1}
	for i := range f.perm {
		f.perm[i] = i
	}
	if n == 0 {
		return f, nil
	}
	values, err := singularValues(a) // non_finite refusal included
	if err != nil {
		return nil, err
	}
	if rank := rankOf(values, n, n, 0); rank < n {
		return nil, luSingular(n, rank, -1)
	}
	f.cond = values[0] / values[n-1]
	lu := f.lu
	for k := 0; k < n; k++ {
		p := k
		best := math.Abs(lu[k*n+k])
		for i := k + 1; i < n; i++ {
			if v := math.Abs(lu[i*n+k]); v > best {
				p, best = i, v
			}
		}
		if best == 0 {
			rank, _ := Rank(a, 0)
			return nil, luSingular(n, rank, k)
		}
		if p != k {
			for j := 0; j < n; j++ {
				lu[k*n+j], lu[p*n+j] = lu[p*n+j], lu[k*n+j]
			}
			f.perm[k], f.perm[p] = f.perm[p], f.perm[k]
		}
		pivot := lu[k*n+k]
		for i := k + 1; i < n; i++ {
			l := lu[i*n+k] / pivot
			lu[i*n+k] = l
			for j := k + 1; j < n; j++ {
				lu[i*n+j] -= float64(l * lu[k*n+j])
			}
		}
	}
	return f, nil
}

// luSingular builds the PULSE_MATRIX_SINGULAR refusal; pivot < 0 means
// the rank test refused before elimination began.
func luSingular(n, rank, pivot int) error {
	details := map[string]any{"reason": "singular", "n": n, "rank": rank,
		"condition_number": math.Inf(1)}
	if pivot >= 0 {
		details["pivot"] = pivot
	}
	return perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
		"linalg: matrix is singular or singular to working precision (rank-deficient under the rank tolerance)",
		details)
}

// ConditionNumber returns the 2-norm condition number σ_max / σ_min of
// the factored matrix, as ConditionNumber computed it at factor time
// (gonum-backed, so it may differ in the last ulps across
// architectures; 1 for an empty factor). It is always finite: a matrix
// whose condition number is +Inf is refused by FactorLU.
func (f *LUFactor) ConditionNumber() float64 { return f.cond }

// N returns the order of the factored matrix.
func (f *LUFactor) N() int { return f.n }

// Solve returns x with A·x = b for the factored A: forward substitution
// L·y = P·b, then back substitution U·x = y, each a running subtraction
// over k ascending (back substitution: k ascending from i+1) and, for U,
// a true division by the diagonal. FMA-free. A nil b or len(b) != N() is
// PULSE_MATRIX_SHAPE_MISMATCH.
func (f *LUFactor) Solve(b *Vec) (*Vec, error) {
	if b == nil {
		return nil, nilOperand("vector")
	}
	if len(b.data) != f.n {
		return nil, shapeError("right-hand side length does not equal the matrix order",
			map[string]any{"n": f.n, "len": len(b.data)})
	}
	return &Vec{data: f.solveColumn(b.data, 1, 0)}, nil
}

// SolveMatrix returns X with A·X = B for the factored A, solving each
// column of B exactly as Solve does (so column j of X equals Solve on
// column j of B, bit for bit). A nil B or B.Rows() != N() is
// PULSE_MATRIX_SHAPE_MISMATCH; a B with zero columns yields an N()×0
// matrix.
func (f *LUFactor) SolveMatrix(b *Matrix) (*Matrix, error) {
	if b == nil {
		return nil, nilOperand("right-hand side matrix")
	}
	if b.rows != f.n {
		return nil, shapeError("right-hand side row count does not equal the matrix order",
			map[string]any{"n": f.n, "rows": b.rows})
	}
	out := &Matrix{rows: f.n, cols: b.cols, data: make([]float64, f.n*b.cols)}
	for j := 0; j < b.cols; j++ {
		x := f.solveColumn(b.data, b.cols, j)
		for i, v := range x {
			out.data[i*b.cols+j] = v
		}
	}
	return out, nil
}

// Inverse returns A⁻¹ for the factored A, column j being the solve
// against the j-th unit vector (SolveMatrix on the identity).
func (f *LUFactor) Inverse() (*Matrix, error) {
	id := &Matrix{rows: f.n, cols: f.n, data: make([]float64, f.n*f.n)}
	for i := 0; i < f.n; i++ {
		id.data[i*f.n+i] = 1
	}
	return f.SolveMatrix(id)
}

// solveColumn solves A·x = b where b's element i is src[i*stride+col].
func (f *LUFactor) solveColumn(src []float64, stride, col int) []float64 {
	n := f.n
	lu := f.lu
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := src[f.perm[i]*stride+col]
		for k := 0; k < i; k++ {
			sum -= float64(lu[i*n+k] * y[k])
		}
		y[i] = sum
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := y[i]
		for k := i + 1; k < n; k++ {
			sum -= float64(lu[i*n+k] * x[k])
		}
		x[i] = sum / lu[i*n+i]
	}
	return x
}

// Solve solves the general square system a·x = b through the reference
// LU factorisation (FactorLU, then LUFactor.Solve): pure Go, FMA-free,
// bit-identical on every architecture. Use it for a non-symmetric or
// indefinite a; for a symmetric positive-definite a, SolveSPD is the
// cheaper reference route. To reuse one factorisation across several
// right-hand sides, call FactorLU once and Solve / SolveMatrix on the
// factor.
//
// A nil operand, a non-square a or len(b) != a.Rows() is
// PULSE_MATRIX_SHAPE_MISMATCH; a singular a (see FactorLU's policy) or a
// NaN / infinite element is PULSE_MATRIX_SINGULAR — never a NaN result.
func Solve(a *Matrix, b *Vec) (*Vec, error) {
	if a == nil {
		return nil, nilOperand("matrix")
	}
	if b == nil {
		return nil, nilOperand("vector")
	}
	if a.rows == a.cols && len(b.data) != a.rows {
		return nil, shapeError("right-hand side length does not equal the matrix order",
			map[string]any{"n": a.rows, "len": len(b.data)})
	}
	f, err := FactorLU(a)
	if err != nil {
		return nil, err
	}
	return f.Solve(b)
}
