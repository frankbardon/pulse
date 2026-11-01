package linalg

import "math"

// This file and gonumorder_lapack.go are an FMA-free pure-Go port of the
// operation order gonum v0.17.0 (gonum.org/v1/gonum, BSD-3-Clause; see
// THIRD_PARTY_NOTICES.md) runs on linux/amd64 for the SPD path:
// mat.Cholesky (Factorize, Cond, SolveVecTo, InverseTo) and mat.Dense.Mul.
//
// Why a port, not a call. gonum's BLAS kernels are SSE2 assembly on
// amd64 (internal/asm/f64: DotUnitary, DotInc, AxpyUnitary, AxpyInc,
// ScalUnitary, ScalInc, GemvN, GemvT) and pure Go elsewhere; on arm64
// the Go compiler contracts that pure Go into fused multiply-adds, and
// the multi-accumulator assembly sums in a different order besides. So
// the same SPD solve differed between the two in the last ulps. These
// routines reproduce the amd64 summation order exactly — including each
// assembly kernel's lane and accumulator assignment — and write every
// product as an explicit float64(...) conversion, which the Go
// specification forbids fusing. Result: amd64 computes the bits gonum
// always computed there (TestSPD_MatchesGonumOrder asserts bit equality
// against gonum on amd64), and arm64 computes the same bits.
//
// Scope. Only the shapes the SPD path reaches are ported: upper
// triangles, non-unit diagonals, positive increments, and GemvN with a
// unit-stride x. Each routine names the gonum routine (and, where it is
// assembly on amd64, the kernel) it mirrors. Never "simplify" a loop or
// drop a conversion here: the operation order IS the contract.

// gonum's level-3 block size and the minimum block count before Dgemm
// splits into (here: serially executed) blocks (blas/gonum/gonum.go),
// and the LAPACK block size Ilaenv returns for DPOTRF, DTRTRI and
// DLAUUM (lapack/gonum/ilaenv.go).
const (
	goBlockSize   = 64
	goMinParBlock = 4
	goLapackNB    = 64
)

// goDotUnitary is internal/asm/f64 DotUnitary (amd64): two SSE2
// registers hold four running sums over i mod 4 for the whole quads,
// the 1–3 element tail goes into the first, and the result is
// (s0+s2) + (s1+s3).
func goDotUnitary(x, y []float64) float64 {
	var s0, s1, s2, s3 float64
	n := len(x)
	i := 0
	for ; i+4 <= n; i += 4 {
		s0 += float64(x[i] * y[i])
		s1 += float64(x[i+1] * y[i+1])
		s2 += float64(x[i+2] * y[i+2])
		s3 += float64(x[i+3] * y[i+3])
	}
	for ; i < n; i++ {
		s0 += float64(x[i] * y[i])
	}
	return (s0 + s2) + (s1 + s3)
}

// goDotInc is internal/asm/f64 DotInc (amd64): one SSE2 register whose
// high lane sums the first element of each pair and whose low lane sums
// the second plus the odd tail; the result is low + high.
func goDotInc(x, y []float64, n, incX, incY, ix, iy int) float64 {
	var hi, lo float64
	for ; n >= 2; n -= 2 {
		hi += float64(x[ix] * y[iy])
		ix += incX
		iy += incY
		lo += float64(x[ix] * y[iy])
		ix += incX
		iy += incY
	}
	if n == 1 {
		lo += float64(x[ix] * y[iy])
	}
	return lo + hi
}

// goDdot is blas/gonum Ddot for positive increments.
func goDdot(n int, x []float64, incX int, y []float64, incY int) float64 {
	if n <= 0 {
		return 0
	}
	if incX == 1 && incY == 1 {
		return goDotUnitary(x[:n], y[:n])
	}
	return goDotInc(x, y, n, incX, incY, 0, 0)
}

// goScalRaw is internal/asm/f64 ScalUnitary / ScalInc: x[i] *= alpha
// with no zero special case.
func goScalRaw(n int, alpha float64, x []float64, incX int) {
	for i, ix := 0, 0; i < n; i, ix = i+1, ix+incX {
		x[ix] *= alpha
	}
}

// goDscal is blas/gonum Dscal for a positive increment: alpha == 0
// stores zeros (so 0·Inf is 0, not NaN); otherwise it scales.
func goDscal(n int, alpha float64, x []float64, incX int) {
	if n < 1 {
		return
	}
	if alpha == 0 {
		for i, ix := 0, 0; i < n; i, ix = i+1, ix+incX {
			x[ix] = 0
		}
		return
	}
	goScalRaw(n, alpha, x, incX)
}

// goAxpyRaw is internal/asm/f64 AxpyUnitary / AxpyInc: y[i] += alpha·x[i]
// element by element (a multiply, then an add — never fused), with no
// alpha == 0 shortcut.
func goAxpyRaw(n int, alpha float64, x []float64, incX int, y []float64, incY int) {
	for i, ix, iy := 0, 0, 0; i < n; i, ix, iy = i+1, ix+incX, iy+incY {
		y[iy] += float64(alpha * x[ix])
	}
}

// goDaxpy is blas/gonum Daxpy for positive increments (alpha == 0 is a
// no-op).
func goDaxpy(n int, alpha float64, x []float64, incX int, y []float64, incY int) {
	if n < 1 || alpha == 0 {
		return
	}
	goAxpyRaw(n, alpha, x, incX, y, incY)
}

// goDasum is blas/gonum Dasum for a positive increment.
func goDasum(n int, x []float64, incX int) float64 {
	var sum float64
	for i := 0; i < n; i++ {
		sum += math.Abs(x[i*incX])
	}
	return sum
}

// goIdamax is blas/gonum Idamax for a positive increment: the first
// index of the largest |x[i]| (a NaN never wins a comparison), -1 for
// n == 0.
func goIdamax(n int, x []float64, incX int) int {
	if n < 2 {
		if n == 1 {
			return 0
		}
		return -1
	}
	idx := 0
	maxV := math.Abs(x[0])
	for i, ix := 1, incX; i < n; i, ix = i+1, ix+incX {
		if v := math.Abs(x[ix]); v > maxV {
			maxV = v
			idx = i
		}
	}
	return idx
}

// goDgemvPrologue is the shared head of blas/gonum Dgemv: it reports
// whether the kernel must still run, after applying the alpha == 0
// shortcuts (y *= beta via Dscal) itself.
func goDgemvPrologue(m, n, lenY int, alpha, beta float64, y []float64, incY int) bool {
	if m == 0 || n == 0 {
		return false
	}
	if alpha == 0 && beta == 1 {
		return false
	}
	if alpha == 0 {
		goDscal(lenY, beta, y, incY)
		return false
	}
	return true
}

// goDgemvT is blas/gonum Dgemv(Trans) → internal/asm/f64 GemvT (amd64):
// y = beta·y + alpha·Aᵀ·x for the m×n A. The kernel first scales y by
// beta (skipped for 1, a clear for 0), then folds the rows in order:
// for row r it forms t = alpha·x[r] once and adds A[r][c]·t into y[c].
// Its 4/2/1-row and 4/2/1-column blocking never reorders the additions
// any single y[c] receives.
func goDgemvT(m, n int, alpha float64, a []float64, lda int, x []float64, incX int, beta float64, y []float64, incY int) {
	if !goDgemvPrologue(m, n, n, alpha, beta, y, incY) {
		return
	}
	if beta != 1 {
		for c, iy := 0, 0; c < n; c, iy = c+1, iy+incY {
			if beta == 0 {
				y[iy] = 0
			} else {
				y[iy] *= beta
			}
		}
	}
	for r, ix := 0, 0; r < m; r, ix = r+1, ix+incX {
		t := float64(alpha * x[ix])
		row := a[r*lda : r*lda+n]
		for c, iy := 0, 0; c < n; c, iy = c+1, iy+incY {
			y[iy] += float64(row[c] * t)
		}
	}
}

// goDgemvN is blas/gonum Dgemv(NoTrans) → internal/asm/f64 GemvN (amd64)
// for a unit-stride x (the only shape the SPD path passes):
// y = beta·y + alpha·A·x for the m×n A. Per row the kernel keeps two
// SSE2 lanes — even columns and odd columns, each summed in ascending
// order. Rows in the 4- and 2-row blocks add the lanes and then the odd
// last column; the final single row (m odd) adds the odd last column to
// the even lane BEFORE the horizontal add. The store is
// beta·y + alpha·sum (y cleared first when beta == 0).
func goDgemvN(m, n int, alpha float64, a []float64, lda int, x []float64, incX int, beta float64, y []float64, incY int) {
	if incX != 1 {
		panic("linalg: goDgemvN supports a unit-stride x only")
	}
	if !goDgemvPrologue(m, n, m, alpha, beta, y, incY) {
		return
	}
	pairs := n &^ 1
	for r, iy := 0, 0; r < m; r, iy = r+1, iy+incY {
		row := a[r*lda : r*lda+n]
		var even, odd float64
		for c := 0; c < pairs; c += 2 {
			even += float64(row[c] * x[c])
			odd += float64(row[c+1] * x[c+1])
		}
		var sum float64
		single := m%2 == 1 && r == m-1
		switch {
		case n%2 == 1 && single:
			even += float64(row[n-1] * x[n-1])
			sum = even + odd
		case n%2 == 1:
			sum = even + odd
			sum += float64(row[n-1] * x[n-1])
		default:
			sum = even + odd
		}
		if beta == 0 {
			y[iy] = 0
		}
		y[iy] = float64(beta*y[iy]) + float64(alpha*sum)
	}
}

// goDtrsvUpper is blas/gonum Dtrsv(Upper, trans, NonUnit) for a
// unit-stride x (pure Go in gonum on every architecture; barriered
// here).
func goDtrsvUpper(trans bool, n int, a []float64, lda int, x []float64) {
	if n == 0 {
		return
	}
	if n == 1 {
		x[0] /= a[0]
		return
	}
	if !trans {
		for i := n - 1; i >= 0; i-- {
			var sum float64
			atmp := a[i*lda+i+1 : i*lda+n]
			for j, v := range atmp {
				sum += float64(x[i+j+1] * v)
			}
			x[i] -= sum
			x[i] /= a[i*lda+i]
		}
		return
	}
	for i := 0; i < n; i++ {
		x[i] /= a[i*lda+i]
		xi := x[i]
		atmp := a[i*lda+i+1 : i*lda+n]
		for j, v := range atmp {
			x[i+j+1] -= float64(v * xi)
		}
	}
}

// goDtrmvUpperNoTrans is blas/gonum Dtrmv(Upper, NoTrans, NonUnit) for a
// positive increment: x[i] = A[i][i]·x[i] + (row tail · x tail), the dot
// in DotUnitary order for a unit stride and DotInc order otherwise.
func goDtrmvUpperNoTrans(n int, a []float64, lda int, x []float64, incX int) {
	if n == 0 {
		return
	}
	if n == 1 {
		x[0] *= a[0]
		return
	}
	if incX == 1 {
		for i := 0; i < n; i++ {
			il := i * lda
			tmp := float64(a[il+i] * x[i])
			x[i] = tmp + goDotUnitary(a[il+i+1:il+n], x[i+1:n])
		}
		return
	}
	ix := 0
	for i := 0; i < n; i++ {
		il := i * lda
		tmp := float64(a[il+i] * x[ix])
		x[ix] = tmp + goDotInc(x, a[il+i+1:il+n], n-i-1, incX, 1, ix+incX, 0)
		ix += incX
	}
}

// goDtrsmLeftUpper is blas/gonum Dtrsm(Left, Upper, trans, NonUnit):
// solve op(A)·X = alpha·B in place for the m×m upper A and the m×n B.
// The diagonal is applied as a multiply by its reciprocal, as gonum
// does.
func goDtrsmLeftUpper(trans bool, m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		goZeroBlock(m, n, b, ldb)
		return
	}
	if !trans {
		for i := m - 1; i >= 0; i-- {
			btmp := b[i*ldb : i*ldb+n]
			if alpha != 1 {
				goScalRaw(n, alpha, btmp, 1)
			}
			for ka, va := range a[i*lda+i+1 : i*lda+m] {
				if va != 0 {
					k := ka + i + 1
					goAxpyRaw(n, -va, b[k*ldb:k*ldb+n], 1, btmp, 1)
				}
			}
			goScalRaw(n, 1/a[i*lda+i], btmp, 1)
		}
		return
	}
	for k := 0; k < m; k++ {
		btmpk := b[k*ldb : k*ldb+n]
		goScalRaw(n, 1/a[k*lda+k], btmpk, 1)
		for ia, va := range a[k*lda+k+1 : k*lda+m] {
			if va != 0 {
				i := ia + k + 1
				goAxpyRaw(n, -va, btmpk, 1, b[i*ldb:i*ldb+n], 1)
			}
		}
		if alpha != 1 {
			goScalRaw(n, alpha, btmpk, 1)
		}
	}
}

// goDtrsmRightUpperNoTrans is blas/gonum Dtrsm(Right, Upper, NoTrans,
// NonUnit): solve X·A = alpha·B in place for the n×n upper A and the
// m×n B.
func goDtrsmRightUpperNoTrans(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		goZeroBlock(m, n, b, ldb)
		return
	}
	for i := 0; i < m; i++ {
		btmp := b[i*ldb : i*ldb+n]
		if alpha != 1 {
			goScalRaw(n, alpha, btmp, 1)
		}
		for k, vb := range btmp {
			if vb == 0 {
				continue
			}
			btmp[k] /= a[k*lda+k]
			goAxpyRaw(n-k-1, -btmp[k], a[k*lda+k+1:k*lda+n], 1, btmp[k+1:n], 1)
		}
	}
}

// goDtrmmLeftUpperNoTrans is blas/gonum Dtrmm(Left, Upper, NoTrans,
// NonUnit): B = alpha·A·B in place for the m×m upper A and the m×n B.
func goDtrmmLeftUpperNoTrans(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		goZeroBlock(m, n, b, ldb)
		return
	}
	for i := 0; i < m; i++ {
		tmp := alpha
		tmp *= a[i*lda+i]
		btmp := b[i*ldb : i*ldb+n]
		goScalRaw(n, tmp, btmp, 1)
		for ka, va := range a[i*lda+i+1 : i*lda+m] {
			k := ka + i + 1
			if va != 0 {
				goAxpyRaw(n, float64(alpha*va), b[k*ldb:k*ldb+n], 1, btmp, 1)
			}
		}
	}
}

// goDtrmmRightUpperTrans is blas/gonum Dtrmm(Right, Upper, Trans,
// NonUnit): B = alpha·B·Aᵀ in place for the n×n upper A and the m×n B.
func goDtrmmRightUpperTrans(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		goZeroBlock(m, n, b, ldb)
		return
	}
	for i := 0; i < m; i++ {
		btmp := b[i*ldb : i*ldb+n]
		for j, vb := range btmp {
			tmp := float64(vb * a[j*lda+j])
			tmp += goDotUnitary(a[j*lda+j+1:j*lda+n], btmp[j+1:n])
			btmp[j] = alpha * tmp
		}
	}
}

// goDsyrkUpper is blas/gonum Dsyrk(Upper, trans): the upper triangle of
// C = alpha·A·Aᵀ + beta·C (NoTrans, A n×k) or alpha·Aᵀ·A + beta·C
// (Trans, A k×n).
func goDsyrkUpper(trans bool, n, k int, alpha float64, a []float64, lda int, beta float64, c []float64, ldc int) {
	if n == 0 {
		return
	}
	if alpha == 0 {
		for i := 0; i < n; i++ {
			ctmp := c[i*ldc+i : i*ldc+n]
			for j := range ctmp {
				if beta == 0 {
					ctmp[j] = 0
				} else {
					ctmp[j] *= beta
				}
			}
		}
		return
	}
	if !trans {
		for i := 0; i < n; i++ {
			ctmp := c[i*ldc+i : i*ldc+n]
			atmp := a[i*lda : i*lda+k]
			for jc, vc := range ctmp {
				j := jc + i
				d := goDotUnitary(atmp, a[j*lda:j*lda+k])
				if beta == 0 {
					ctmp[jc] = alpha * d
				} else {
					ctmp[jc] = float64(vc*beta) + float64(alpha*d)
				}
			}
		}
		return
	}
	for i := 0; i < n; i++ {
		ctmp := c[i*ldc+i : i*ldc+n]
		if beta == 0 {
			for j := range ctmp {
				ctmp[j] = 0
			}
		} else if beta != 1 {
			for j := range ctmp {
				ctmp[j] *= beta
			}
		}
		for l := 0; l < k; l++ {
			tmp := alpha * a[l*lda+i]
			if tmp != 0 {
				goAxpyRaw(n-i, tmp, a[l*lda+i:l*lda+n], 1, ctmp, 1)
			}
		}
	}
}

// goDgemm is blas/gonum Dgemm: C = alpha·op(A)·op(B) + beta·C for the
// m×n C and inner dimension k. Beyond goMinParBlock 64×64 blocks gonum
// splits C into blocks and walks k in 64-wide chunks; the blocks run in
// parallel there but write disjoint parts of C, so running them in turn
// reproduces their bits. The k-chunking matters only for NoTrans·Trans,
// whose kernel adds one dot product per chunk.
func goDgemm(aTrans, bTrans bool, m, n, k int, alpha float64, a []float64, lda int, b []float64, ldb int, beta float64, c []float64, ldc int) {
	if m == 0 || n == 0 {
		return
	}
	if (alpha == 0 || k == 0) && beta == 1 {
		return
	}
	if beta != 1 {
		for i := 0; i < m; i++ {
			ctmp := c[i*ldc : i*ldc+n]
			for j := range ctmp {
				if beta == 0 {
					ctmp[j] = 0
				} else {
					ctmp[j] *= beta
				}
			}
		}
	}
	if goBlocks(m)*goBlocks(n) < goMinParBlock {
		goDgemmSerial(aTrans, bTrans, m, n, k, a, lda, b, ldb, c, ldc, alpha)
		return
	}
	for i := 0; i < m; i += goBlockSize {
		leni := min(goBlockSize, m-i)
		for j := 0; j < n; j += goBlockSize {
			lenj := min(goBlockSize, n-j)
			cSub := c[i*ldc+j:]
			for l := 0; l < k; l += goBlockSize {
				lenk := min(goBlockSize, k-l)
				var aSub, bSub []float64
				if aTrans {
					aSub = a[l*lda+i:]
				} else {
					aSub = a[i*lda+l:]
				}
				if bTrans {
					bSub = b[j*ldb+l:]
				} else {
					bSub = b[l*ldb+j:]
				}
				goDgemmSerial(aTrans, bTrans, leni, lenj, lenk, aSub, lda, bSub, ldb, cSub, ldc, alpha)
			}
		}
	}
}

// goBlocks is blas/gonum blocks: the number of goBlockSize divisions of
// dim.
func goBlocks(dim int) int { return (dim + goBlockSize - 1) / goBlockSize }

// goDgemmSerial is blas/gonum dgemmSerial{NotNot,TransNot,NotTrans}
// (TransTrans is never reached by the SPD path).
func goDgemmSerial(aTrans, bTrans bool, m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	switch {
	case !aTrans && !bTrans:
		for i := 0; i < m; i++ {
			ctmp := c[i*ldc : i*ldc+n]
			for l, v := range a[i*lda : i*lda+k] {
				if tmp := alpha * v; tmp != 0 {
					goAxpyRaw(n, tmp, b[l*ldb:l*ldb+n], 1, ctmp, 1)
				}
			}
		}
	case aTrans && !bTrans:
		for l := 0; l < k; l++ {
			btmp := b[l*ldb : l*ldb+n]
			for i, v := range a[l*lda : l*lda+m] {
				if tmp := alpha * v; tmp != 0 {
					goAxpyRaw(n, tmp, btmp, 1, c[i*ldc:i*ldc+n], 1)
				}
			}
		}
	case !aTrans && bTrans:
		for i := 0; i < m; i++ {
			atmp := a[i*lda : i*lda+k]
			ctmp := c[i*ldc : i*ldc+n]
			for j := 0; j < n; j++ {
				ctmp[j] += float64(alpha * goDotUnitary(atmp, b[j*ldb:j*ldb+k]))
			}
		}
	default:
		panic("linalg: goDgemmSerial: Trans·Trans is not ported")
	}
}

// goZeroBlock zeroes the m×n block b (the alpha == 0 branch of the
// level-3 routines).
func goZeroBlock(m, n int, b []float64, ldb int) {
	for i := 0; i < m; i++ {
		btmp := b[i*ldb : i*ldb+n]
		for j := range btmp {
			btmp[j] = 0
		}
	}
}
