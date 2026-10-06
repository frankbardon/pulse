package linalg

import (
	"math"
	"sort"

	perr "github.com/frankbardon/pulse/errors"
	"gonum.org/v1/gonum/mat"
)

// The routines in this file are gonum-backed and carry NO bit contract:
// gonum's assembly kernels and the compiler's fused multiply-add choices
// differ by architecture, so results may differ in the last ulps between
// arm64 and amd64. What IS a contract is the policy layered on top —
// order, sign and the rank tolerance — which every routine applies after
// gonum returns, so two architectures agree on every decision that is
// not inside rounding.

// DominanceTolerance is the relative slack used to decide which
// component of a vector is its LARGEST-MAGNITUDE (dominant) one: every
// component whose magnitude is at least (1 - DominanceTolerance) times
// the largest magnitude is tied for dominance, and the tie goes to the
// lowest index. The slack (2⁻²⁶ = √Epsilon) keeps a decision between two
// components that are equal in exact arithmetic — 1/√2 and -1/√2, say —
// from flipping on a last-ulp difference between architectures.
const DominanceTolerance = 0x1p-26

// SymEigenResult is the eigen-decomposition s = V·diag(Values)·Vᵀ of a
// symmetric matrix. Column k of Vectors is the unit eigenvector of
// Values[k].
//
// Order: Values are descending. Eigenvalues within the rank tolerance
// RankTolerance(n, n, max|λ|) of their neighbour form one tie cluster;
// inside a cluster the vectors are ordered by the index of their
// dominant component (original variable order), lowest first.
//
// Sign: every eigenvector is flipped so its dominant component (see
// DominanceTolerance; a tie goes to the lowest index) is positive.
type SymEigenResult struct {
	Values  *Vec
	Vectors *Matrix
}

// SymEigen returns the eigen-decomposition of the symmetric s under the
// order and sign policy documented on SymEigenResult. A nil s is
// PULSE_MATRIX_SHAPE_MISMATCH; a non-finite element, or a decomposition
// that does not converge, is PULSE_MATRIX_SINGULAR (details "reason").
// An empty s decomposes to empty Values and Vectors.
func SymEigen(s *Sym) (*SymEigenResult, error) {
	if s == nil {
		return nil, nilOperand("symmetric")
	}
	n := s.n
	if n == 0 {
		return &SymEigenResult{Values: &Vec{}, Vectors: &Matrix{}}, nil
	}
	if !allFinite(s.data) {
		return nil, nonFinite()
	}
	full := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			full[i*n+j] = s.data[s.index(i, j)]
		}
	}
	var eig mat.EigenSym
	if !eig.Factorize(mat.NewSymDense(n, full), true) {
		return nil, noConvergence("eigen-decomposition")
	}
	var vecs mat.Dense
	eig.VectorsTo(&vecs)
	values := eig.Values(nil) // ascending
	cols := make([][]float64, n)
	for k := range cols {
		cols[k] = mat.Col(nil, k, &vecs)
	}
	// Descending start: reverse gonum's ascending order.
	for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
		cols[i], cols[j] = cols[j], cols[i]
	}
	maxAbs := 0.0
	for _, v := range values {
		maxAbs = math.Max(maxAbs, math.Abs(v))
	}
	values, cols, _ = canonicalize(values, cols, nil, RankTolerance(n, n, maxAbs))
	return &SymEigenResult{Values: &Vec{data: values}, Vectors: columnsToMatrix(n, cols)}, nil
}

// SVDResult is the THIN singular value decomposition m = U·diag(Values)·Vᵀ
// of a rows×cols matrix, with k = min(rows, cols): U is rows×k, V is
// cols×k, both with orthonormal columns.
//
// Order: Values are descending; values within RankTolerance(rows, cols,
// σ_max) of their neighbour form one tie cluster ordered by the dominant
// component index of the V column (original variable order).
//
// Sign: each pair (U column k, V column k) is flipped TOGETHER so the
// dominant component of the V column is positive (DominanceTolerance; a
// tie goes to the lowest index). Flipping both keeps U·Σ·Vᵀ unchanged.
type SVDResult struct {
	U      *Matrix
	Values *Vec
	V      *Matrix
}

// SVD returns the thin singular value decomposition of m under the
// order and sign policy documented on SVDResult. A nil m is
// PULSE_MATRIX_SHAPE_MISMATCH; a non-finite element or a decomposition
// that does not converge is PULSE_MATRIX_SINGULAR (details "reason"). A
// matrix with a zero dimension decomposes to empty factors.
func SVD(m *Matrix) (*SVDResult, error) {
	if m == nil {
		return nil, nilOperand("matrix")
	}
	r, c := m.rows, m.cols
	if r == 0 || c == 0 {
		return &SVDResult{U: &Matrix{rows: r}, Values: &Vec{}, V: &Matrix{rows: c}}, nil
	}
	svd, err := factorSVD(m, mat.SVDThin)
	if err != nil {
		return nil, err
	}
	var u, v mat.Dense
	svd.UTo(&u)
	svd.VTo(&v)
	values := svd.Values(nil) // descending
	k := len(values)
	uCols := make([][]float64, k)
	vCols := make([][]float64, k)
	for j := 0; j < k; j++ {
		uCols[j] = mat.Col(nil, j, &u)
		vCols[j] = mat.Col(nil, j, &v)
	}
	values, vCols, uCols = canonicalize(values, vCols, uCols, RankTolerance(r, c, values[0]))
	return &SVDResult{
		U:      columnsToMatrix(r, uCols),
		Values: &Vec{data: values},
		V:      columnsToMatrix(c, vCols),
	}, nil
}

// QRResult is the THIN QR decomposition m = Q·R of a rows×cols matrix
// with rows ≥ cols: Q is rows×cols with orthonormal columns and R is
// cols×cols upper triangular.
//
// Sign: each diagonal element of R is non-negative (a negative one has
// its R row and Q column flipped together), which makes the
// decomposition of a full-column-rank matrix unique.
type QRResult struct {
	Q *Matrix
	R *Matrix
}

// QR returns the thin QR decomposition of m under the sign policy
// documented on QRResult. A nil m or rows < cols is
// PULSE_MATRIX_SHAPE_MISMATCH; a non-finite element is
// PULSE_MATRIX_SINGULAR (details "reason"). A matrix with zero columns
// decomposes to a rows×0 Q and a 0×0 R.
func QR(m *Matrix) (*QRResult, error) {
	if m == nil {
		return nil, nilOperand("matrix")
	}
	r, c := m.rows, m.cols
	if r < c {
		return nil, shapeError("QR requires rows >= cols",
			map[string]any{"rows": r, "cols": c})
	}
	if c == 0 {
		return &QRResult{Q: &Matrix{rows: r}, R: &Matrix{}}, nil
	}
	if !allFinite(m.data) {
		return nil, nonFinite()
	}
	var qr mat.QR
	qr.Factorize(mat.NewDense(r, c, m.RowMajor()))
	var qFull, rFull mat.Dense
	qr.QTo(&qFull)
	qr.RTo(&rFull)
	Q := &Matrix{rows: r, cols: c, data: make([]float64, r*c)}
	R := &Matrix{rows: c, cols: c, data: make([]float64, c*c)}
	for j := 0; j < c; j++ {
		sign := 1.0
		if rFull.At(j, j) < 0 {
			sign = -1
		}
		for i := 0; i < r; i++ {
			Q.data[i*c+j] = sign * qFull.At(i, j)
		}
		for k := j; k < c; k++ {
			R.data[j*c+k] = sign * rFull.At(j, k)
		}
	}
	return &QRResult{Q: Q, R: R}, nil
}

// Rank returns the numerical rank of m: the number of singular values
// strictly greater than tol. A tol that is not > 0 (zero, negative or
// NaN) selects the ONE documented default, RankTolerance(rows, cols,
// σ_max). A nil m is PULSE_MATRIX_SHAPE_MISMATCH; a non-finite element
// is PULSE_MATRIX_SINGULAR (details "reason"). An empty m has rank 0.
func Rank(m *Matrix, tol float64) (int, error) {
	if m == nil {
		return 0, nilOperand("matrix")
	}
	values, err := singularValues(m)
	if err != nil {
		return 0, err
	}
	return rankOf(values, m.rows, m.cols, tol), nil
}

// ConditionNumber returns the 2-norm condition number σ_max / σ_min of
// m over its min(rows, cols) singular values. A matrix that is
// rank-deficient under the default tolerance (Rank(m, 0) < min(rows,
// cols)) — the zero matrix included — has condition number +Inf; a
// near-singular matrix above the tolerance returns its large finite
// ratio. A nil or empty m is PULSE_MATRIX_SHAPE_MISMATCH (the condition
// number of an empty matrix is undefined); a non-finite element is
// PULSE_MATRIX_SINGULAR (details "reason").
func ConditionNumber(m *Matrix) (float64, error) {
	if m == nil {
		return 0, nilOperand("matrix")
	}
	if m.rows == 0 || m.cols == 0 {
		return 0, shapeError("condition number of an empty matrix is undefined",
			map[string]any{"rows": m.rows, "cols": m.cols})
	}
	values, err := singularValues(m)
	if err != nil {
		return 0, err
	}
	if rankOf(values, m.rows, m.cols, 0) < len(values) {
		return math.Inf(1), nil
	}
	return values[0] / values[len(values)-1], nil
}

func rankOf(values []float64, rows, cols int, tol float64) int {
	if len(values) == 0 {
		return 0
	}
	if !(tol > 0) {
		tol = RankTolerance(rows, cols, values[0])
	}
	rank := 0
	for _, v := range values {
		if v > tol {
			rank++
		}
	}
	return rank
}

// singularValues returns the descending singular values of m (none for
// an empty m).
func singularValues(m *Matrix) ([]float64, error) {
	if m.rows == 0 || m.cols == 0 {
		return nil, nil
	}
	svd, err := factorSVD(m, mat.SVDNone)
	if err != nil {
		return nil, err
	}
	return svd.Values(nil), nil
}

func factorSVD(m *Matrix, kind mat.SVDKind) (*mat.SVD, error) {
	if !allFinite(m.data) {
		return nil, nonFinite()
	}
	var svd mat.SVD
	if !svd.Factorize(mat.NewDense(m.rows, m.cols, m.RowMajor()), kind) {
		return nil, noConvergence("singular value decomposition")
	}
	return &svd, nil
}

// canonicalize applies the shared order and sign policy to a
// descending-sorted spectrum. vecs are the vectors that decide order
// and sign; partners (may be nil) are flipped and permuted with them.
// Adjacent values within tol of each other chain into one tie cluster,
// which is re-ordered (stably) by the dominant-component index of vecs.
func canonicalize(values []float64, vecs, partners [][]float64, tol float64) ([]float64, [][]float64, [][]float64) {
	k := len(values)
	dom := make([]int, k)
	for j := 0; j < k; j++ {
		dom[j] = dominantIndex(vecs[j])
		if vecs[j][dom[j]] < 0 {
			negate(vecs[j])
			if partners != nil {
				negate(partners[j])
			}
		}
	}
	perm := make([]int, k)
	for j := range perm {
		perm[j] = j
	}
	for start := 0; start < k; {
		end := start + 1
		for end < k && values[end-1]-values[end] <= tol {
			end++
		}
		cluster := perm[start:end]
		sort.SliceStable(cluster, func(a, b int) bool { return dom[cluster[a]] < dom[cluster[b]] })
		start = end
	}
	outValues := make([]float64, k)
	outVecs := make([][]float64, k)
	var outPartners [][]float64
	if partners != nil {
		outPartners = make([][]float64, k)
	}
	for j, p := range perm {
		outValues[j] = values[p]
		outVecs[j] = vecs[p]
		if partners != nil {
			outPartners[j] = partners[p]
		}
	}
	return outValues, outVecs, outPartners
}

// dominantIndex is the lowest index whose magnitude is within
// DominanceTolerance (relative) of the largest magnitude in x.
func dominantIndex(x []float64) int {
	maxAbs := 0.0
	for _, v := range x {
		maxAbs = math.Max(maxAbs, math.Abs(v))
	}
	cut := maxAbs * (1 - DominanceTolerance)
	for i, v := range x {
		if math.Abs(v) >= cut {
			return i
		}
	}
	return 0
}

func negate(x []float64) {
	for i := range x {
		x[i] = -x[i]
	}
}

func columnsToMatrix(rows int, cols [][]float64) *Matrix {
	c := len(cols)
	out := &Matrix{rows: rows, cols: c, data: make([]float64, rows*c)}
	for j, col := range cols {
		for i, v := range col {
			out.data[i*c+j] = v
		}
	}
	return out
}

func allFinite(x []float64) bool {
	for _, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func nonFinite() error {
	return perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
		"linalg: matrix has a NaN or infinite element",
		map[string]any{"reason": "non_finite"})
}

func noConvergence(what string) error {
	return perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR,
		"linalg: "+what+" did not converge",
		map[string]any{"reason": "no_convergence"})
}
