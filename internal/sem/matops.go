package sem

import (
	"math"

	"gonum.org/v1/gonum/blas/blas64"
	"gonum.org/v1/gonum/mat"

	"github.com/frankbardon/pulse/internal/gonumorder"
)

// The model arithmetic of every fit, statistic and score runs here, not
// in gonum's mat methods: gonum computes mat.Dense.Mul, VecDense.MulVec,
// Dense.Inverse and the Cholesky factor / inverse / solve / LogDet with
// SSE2 assembly on amd64 and FMA-contracted pure Go on arm64, so the
// same fit differed in its last ulps between the two and the optimiser
// carried the difference into its stop (a different iteration count, or
// a different converged flag). Each helper below replays the gonum
// method's own dispatch over internal/gonumorder, the FMA-free port of
// gonum v0.17.0's amd64 operation order: amd64 computes exactly the bits
// the gonum call gave there (TestSEM_MatOpsMatchGonum), and arm64 now
// computes the same bits. gonum's mat types stay the containers (the
// Fit fields, SymDense moments); only their arithmetic moved.

// conditionTolerance is gonum's mat.ConditionTolerance: past it an
// inverse or solve still fills its result but reports mat.Condition.
const conditionTolerance = 1e16

// untranspose is gonum's untransposeExtract for the operands SEM
// passes: a Transpose (Dense.T()) unwraps with trans true; a SymDense's
// T() is itself.
func untranspose(a mat.Matrix) (mat.Matrix, bool) {
	if u, ok := a.(mat.Untransposer); ok {
		return u.Untranspose(), true
	}
	return a, false
}

// mul returns a·b exactly as mat.Dense.Mul computes it on amd64: a
// Dense·Dense product is Dgemm (either operand transposed), a product
// with one symmetric and one dense operand is Dsymm (transposes handled
// through a workspace, as gonum does), and any other pair — two
// symmetric operands — gonum's generic row-times-column loop. A
// triangular, band or vector operand panics: SEM passes none, and gonum
// routes them elsewhere.
func mul(a, b mat.Matrix) *mat.Dense {
	ar, ac := a.Dims()
	br, bc := b.Dims()
	if ac != br {
		panic(mat.ErrShape)
	}
	aU, aTrans := untranspose(a)
	bU, bTrans := untranspose(b)
	out := mat.NewDense(ar, bc, nil)
	c := out.RawMatrix()
	if ad, ok := aU.(*mat.Dense); ok {
		am := ad.RawMatrix()
		switch bt := bU.(type) {
		case *mat.Dense:
			bm := bt.RawMatrix()
			if aTrans && bTrans {
				panic("sem: mul: Trans·Trans is not ported")
			}
			gonumorder.Dgemm(aTrans, bTrans, ar, bc, ac, 1, am.Data, am.Stride, bm.Data, bm.Stride, 0, c.Data, c.Stride)
			return out
		case *mat.SymDense:
			bs := bt.RawSymmetric()
			if aTrans {
				// C = aUᵀ·B = (B·aU)ᵀ: Symm(Left) into ac×ar, then copy
				// the transpose.
				w := make([]float64, ac*ar)
				gonumorder.DsymmUpper(true, ac, ar, 1, bs.Data, bs.Stride, am.Data, am.Stride, 0, w, ar)
				transposeInto(c, w, ac, ar)
				return out
			}
			gonumorder.DsymmUpper(false, ar, bc, 1, bs.Data, bs.Stride, am.Data, am.Stride, 0, c.Data, c.Stride)
			return out
		}
	}
	if bd, ok := bU.(*mat.Dense); ok {
		if as, ok := aU.(*mat.SymDense); ok {
			bm := bd.RawMatrix()
			sm := as.RawSymmetric()
			if bTrans {
				// C = A·bUᵀ = (bU·A)ᵀ: Symm(Right) into bc×br.
				w := make([]float64, bc*br)
				gonumorder.DsymmUpper(false, bc, br, 1, sm.Data, sm.Stride, bm.Data, bm.Stride, 0, w, br)
				transposeInto(c, w, bc, br)
				return out
			}
			gonumorder.DsymmUpper(true, ar, bc, 1, sm.Data, sm.Stride, bm.Data, bm.Stride, 0, c.Data, c.Stride)
			return out
		}
	}
	for _, x := range []mat.Matrix{aU, bU} {
		switch x.(type) {
		case *mat.Dense, *mat.SymDense:
		default:
			panic("sem: mul: operand type is not ported")
		}
	}
	// gonum's fallback: each row of a against each column of b, in
	// order.
	row := make([]float64, ac)
	for r := 0; r < ar; r++ {
		for i := range row {
			row[i] = a.At(r, i)
		}
		for col := 0; col < bc; col++ {
			var v float64
			for i, e := range row {
				v += float64(e * b.At(i, col))
			}
			c.Data[r*c.Stride+col] = v
		}
	}
	return out
}

// transposeInto writes the rows×cols row-major w, transposed, into the
// cols×rows c (gonum's strictCopy of the workspace's T()).
func transposeInto(c blas64.General, w []float64, rows, cols int) {
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			c.Data[j*c.Stride+i] = w[i*cols+j]
		}
	}
}

// mulVec returns a·x as mat.VecDense.MulVec computes it on amd64: Dgemv
// for a dense a, Dsymv for a symmetric one.
func mulVec(a mat.Matrix, x []float64) []float64 {
	r, cols := a.Dims()
	if cols != len(x) {
		panic(mat.ErrShape)
	}
	y := make([]float64, r)
	aU, trans := untranspose(a)
	switch t := aU.(type) {
	case *mat.Dense:
		m := t.RawMatrix()
		if trans {
			gonumorder.DgemvT(m.Rows, m.Cols, 1, m.Data, m.Stride, x, 1, 0, y, 1)
		} else {
			gonumorder.DgemvN(m.Rows, m.Cols, 1, m.Data, m.Stride, x, 1, 0, y, 1)
		}
	case *mat.SymDense:
		s := t.RawSymmetric()
		gonumorder.DsymvUpper(s.N, 1, s.Data, s.Stride, x, 0, y)
	default:
		panic("sem: mulVec: operand type is not ported")
	}
	return y
}

// inverse returns a⁻¹ as mat.Dense.Inverse computes it on amd64 (LU:
// Dlange, Dgetrf, Dgecon, Dgetri): nil error, or mat.Condition when the
// estimated condition number exceeds conditionTolerance (+Inf for an
// exactly singular a) — the result filled either way, as gonum fills it.
func inverse(a mat.Matrix) (*mat.Dense, error) {
	r, c := a.Dims()
	if r != c {
		panic(mat.ErrSquare)
	}
	out := mat.DenseCopyOf(a)
	m := out.RawMatrix()
	if cond := gonumorder.Inverse(r, m.Data, m.Stride); cond > conditionTolerance {
		return out, mat.Condition(cond)
	}
	return out, nil
}

// spdFactor is mat.Cholesky over gonumorder's port of its amd64 order:
// A = Uᵀ·U with U upper triangular (row-major, stride n), plus gonum's
// condition estimate.
type spdFactor struct {
	n    int
	u    []float64
	cond float64
}

// factorize is mat.Cholesky.Factorize: the upper triangle of a factored
// by Dpotrf, the 1-norm condition estimate by Dpocon; false when a is
// not positive definite.
func factorize(a mat.Symmetric) (*spdFactor, bool) {
	n := a.SymmetricDim()
	u := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			u[i*n+j] = a.At(i, j)
		}
	}
	norm := gonumorder.DlansyUpperMaxRowSum(n, u, n, make([]float64, n))
	if !gonumorder.DpotrfUpper(n, u, n) {
		return nil, false
	}
	return &spdFactor{n: n, u: u, cond: 1 / gonumorder.DpoconUpper(n, u, n, norm)}, true
}

// inverse is mat.Cholesky.InverseTo: A⁻¹ by Dpotri, mat.Condition when
// the condition estimate exceeds conditionTolerance (+Inf for a
// triangular-inverse breakdown) — the result filled either way.
func (c *spdFactor) inverse() (*mat.SymDense, error) {
	n := c.n
	inv := make([]float64, n*n)
	for i := 0; i < n; i++ {
		copy(inv[i*n+i:(i+1)*n], c.u[i*n+i:(i+1)*n])
	}
	ok := gonumorder.DpotriUpper(n, inv, n)
	out := mat.NewSymDense(n, inv)
	if !ok {
		return out, mat.Condition(math.Inf(1))
	}
	if c.cond > conditionTolerance {
		return out, mat.Condition(c.cond)
	}
	return out, nil
}

// logDet is mat.Cholesky.LogDet: ln|A| = Σ 2·ln U[i][i].
func (c *spdFactor) logDet() float64 {
	return gonumorder.CholeskyLogDet(c.n, c.u, c.n)
}

// solveVec is mat.Cholesky.SolveVecTo: x with A·x = b by Dpotrs,
// mat.Condition when the condition estimate exceeds conditionTolerance
// (x filled either way).
func (c *spdFactor) solveVec(b []float64) ([]float64, error) {
	if len(b) != c.n {
		panic(mat.ErrShape)
	}
	x := append([]float64(nil), b...)
	gonumorder.DpotrsUpperVec(c.n, c.u, c.n, x)
	if c.cond > conditionTolerance {
		return x, mat.Condition(c.cond)
	}
	return x, nil
}
