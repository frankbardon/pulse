package gonumorder

import "math"

// The BLAS half of the port (package comment: the contract). Each
// routine is the blas/gonum routine — or, on amd64, the internal/asm/f64
// assembly kernel — of the same name.

// gonum's level-3 block size and the minimum block count before Dgemm
// splits into (here: serially executed) blocks (blas/gonum/gonum.go),
// and the LAPACK block size Ilaenv returns for DPOTRF, DTRTRI and
// DLAUUM (lapack/gonum/ilaenv.go).
const (
	blockSize   = 64
	minParBlock = 4
	lapackNB    = 64
)

// DotUnitary is internal/asm/f64 DotUnitary (amd64): two SSE2
// registers hold four running sums over i mod 4 for the whole quads,
// the 1–3 element tail goes into the first, and the result is
// (s0+s2) + (s1+s3).
func DotUnitary(x, y []float64) float64 {
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

// DotInc is internal/asm/f64 DotInc (amd64): one SSE2 register whose
// high lane sums the first element of each pair and whose low lane sums
// the second plus the odd tail; the result is low + high.
func DotInc(x, y []float64, n, incX, incY, ix, iy int) float64 {
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

// Ddot is blas/gonum Ddot for positive increments.
func Ddot(n int, x []float64, incX int, y []float64, incY int) float64 {
	if n <= 0 {
		return 0
	}
	if incX == 1 && incY == 1 {
		return DotUnitary(x[:n], y[:n])
	}
	return DotInc(x, y, n, incX, incY, 0, 0)
}

// ScalRaw is internal/asm/f64 ScalUnitary / ScalInc: x[i] *= alpha
// with no zero special case.
func ScalRaw(n int, alpha float64, x []float64, incX int) {
	for i, ix := 0, 0; i < n; i, ix = i+1, ix+incX {
		x[ix] *= alpha
	}
}

// Dscal is blas/gonum Dscal for a positive increment: alpha == 0
// stores zeros (so 0·Inf is 0, not NaN); otherwise it scales.
func Dscal(n int, alpha float64, x []float64, incX int) {
	if n < 1 {
		return
	}
	if alpha == 0 {
		for i, ix := 0, 0; i < n; i, ix = i+1, ix+incX {
			x[ix] = 0
		}
		return
	}
	ScalRaw(n, alpha, x, incX)
}

// AxpyRaw is internal/asm/f64 AxpyUnitary / AxpyInc: y[i] += alpha·x[i]
// element by element (a multiply, then an add — never fused), with no
// alpha == 0 shortcut.
func AxpyRaw(n int, alpha float64, x []float64, incX int, y []float64, incY int) {
	for i, ix, iy := 0, 0, 0; i < n; i, ix, iy = i+1, ix+incX, iy+incY {
		y[iy] += float64(alpha * x[ix])
	}
}

// Daxpy is blas/gonum Daxpy for positive increments (alpha == 0 is a
// no-op).
func Daxpy(n int, alpha float64, x []float64, incX int, y []float64, incY int) {
	if n < 1 || alpha == 0 {
		return
	}
	AxpyRaw(n, alpha, x, incX, y, incY)
}

// Dasum is blas/gonum Dasum for a positive increment.
func Dasum(n int, x []float64, incX int) float64 {
	var sum float64
	for i := 0; i < n; i++ {
		sum += math.Abs(x[i*incX])
	}
	return sum
}

// Idamax is blas/gonum Idamax for a positive increment: the first
// index of the largest |x[i]| (a NaN never wins a comparison), -1 for
// n == 0.
func Idamax(n int, x []float64, incX int) int {
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

// dgemvPrologue is the shared head of blas/gonum Dgemv: it reports
// whether the kernel must still run, after applying the alpha == 0
// shortcuts (y *= beta via Dscal) itself.
func dgemvPrologue(m, n, lenY int, alpha, beta float64, y []float64, incY int) bool {
	if m == 0 || n == 0 {
		return false
	}
	if alpha == 0 && beta == 1 {
		return false
	}
	if alpha == 0 {
		Dscal(lenY, beta, y, incY)
		return false
	}
	return true
}

// DgemvT is blas/gonum Dgemv(Trans) → internal/asm/f64 GemvT (amd64):
// y = beta·y + alpha·Aᵀ·x for the m×n A. The kernel first scales y by
// beta (skipped for 1, a clear for 0), then folds the rows in order:
// for row r it forms t = alpha·x[r] once and adds A[r][c]·t into y[c].
// Its 4/2/1-row and 4/2/1-column blocking never reorders the additions
// any single y[c] receives.
func DgemvT(m, n int, alpha float64, a []float64, lda int, x []float64, incX int, beta float64, y []float64, incY int) {
	if !dgemvPrologue(m, n, n, alpha, beta, y, incY) {
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

// DgemvN is blas/gonum Dgemv(NoTrans) → internal/asm/f64 GemvN (amd64)
// for a unit-stride x (the only shape the SPD path passes):
// y = beta·y + alpha·A·x for the m×n A. Per row the kernel keeps two
// SSE2 lanes — even columns and odd columns, each summed in ascending
// order. Rows in the 4- and 2-row blocks add the lanes and then the odd
// last column; the final single row (m odd) adds the odd last column to
// the even lane BEFORE the horizontal add. The store is
// beta·y + alpha·sum (y cleared first when beta == 0).
func DgemvN(m, n int, alpha float64, a []float64, lda int, x []float64, incX int, beta float64, y []float64, incY int) {
	if incX != 1 {
		panic("gonumorder: DgemvN supports a unit-stride x only")
	}
	if !dgemvPrologue(m, n, m, alpha, beta, y, incY) {
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

// DtrsvUpper is blas/gonum Dtrsv(Upper, trans, NonUnit) for a
// unit-stride x (pure Go in gonum on every architecture; barriered
// here).
func DtrsvUpper(trans bool, n int, a []float64, lda int, x []float64) {
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

// DtrmvUpperNoTrans is blas/gonum Dtrmv(Upper, NoTrans, NonUnit) for a
// positive increment: x[i] = A[i][i]·x[i] + (row tail · x tail), the dot
// in DotUnitary order for a unit stride and DotInc order otherwise.
func DtrmvUpperNoTrans(n int, a []float64, lda int, x []float64, incX int) {
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
			x[i] = tmp + DotUnitary(a[il+i+1:il+n], x[i+1:n])
		}
		return
	}
	ix := 0
	for i := 0; i < n; i++ {
		il := i * lda
		tmp := float64(a[il+i] * x[ix])
		x[ix] = tmp + DotInc(x, a[il+i+1:il+n], n-i-1, incX, 1, ix+incX, 0)
		ix += incX
	}
}

// DtrsmLeftUpper is blas/gonum Dtrsm(Left, Upper, trans, NonUnit):
// solve op(A)·X = alpha·B in place for the m×m upper A and the m×n B.
// The diagonal is applied as a multiply by its reciprocal, as gonum
// does.
func DtrsmLeftUpper(trans bool, m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		zeroBlock(m, n, b, ldb)
		return
	}
	if !trans {
		for i := m - 1; i >= 0; i-- {
			btmp := b[i*ldb : i*ldb+n]
			if alpha != 1 {
				ScalRaw(n, alpha, btmp, 1)
			}
			for ka, va := range a[i*lda+i+1 : i*lda+m] {
				if va != 0 {
					k := ka + i + 1
					AxpyRaw(n, -va, b[k*ldb:k*ldb+n], 1, btmp, 1)
				}
			}
			ScalRaw(n, 1/a[i*lda+i], btmp, 1)
		}
		return
	}
	for k := 0; k < m; k++ {
		btmpk := b[k*ldb : k*ldb+n]
		ScalRaw(n, 1/a[k*lda+k], btmpk, 1)
		for ia, va := range a[k*lda+k+1 : k*lda+m] {
			if va != 0 {
				i := ia + k + 1
				AxpyRaw(n, -va, btmpk, 1, b[i*ldb:i*ldb+n], 1)
			}
		}
		if alpha != 1 {
			ScalRaw(n, alpha, btmpk, 1)
		}
	}
}

// DtrsmRightUpperNoTrans is blas/gonum Dtrsm(Right, Upper, NoTrans,
// NonUnit): solve X·A = alpha·B in place for the n×n upper A and the
// m×n B.
func DtrsmRightUpperNoTrans(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		zeroBlock(m, n, b, ldb)
		return
	}
	for i := 0; i < m; i++ {
		btmp := b[i*ldb : i*ldb+n]
		if alpha != 1 {
			ScalRaw(n, alpha, btmp, 1)
		}
		for k, vb := range btmp {
			if vb == 0 {
				continue
			}
			btmp[k] /= a[k*lda+k]
			AxpyRaw(n-k-1, -btmp[k], a[k*lda+k+1:k*lda+n], 1, btmp[k+1:n], 1)
		}
	}
}

// DtrmmLeftUpperNoTrans is blas/gonum Dtrmm(Left, Upper, NoTrans,
// NonUnit): B = alpha·A·B in place for the m×m upper A and the m×n B.
func DtrmmLeftUpperNoTrans(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		zeroBlock(m, n, b, ldb)
		return
	}
	for i := 0; i < m; i++ {
		tmp := alpha
		tmp *= a[i*lda+i]
		btmp := b[i*ldb : i*ldb+n]
		ScalRaw(n, tmp, btmp, 1)
		for ka, va := range a[i*lda+i+1 : i*lda+m] {
			k := ka + i + 1
			if va != 0 {
				AxpyRaw(n, float64(alpha*va), b[k*ldb:k*ldb+n], 1, btmp, 1)
			}
		}
	}
}

// DtrmmRightUpperTrans is blas/gonum Dtrmm(Right, Upper, Trans,
// NonUnit): B = alpha·B·Aᵀ in place for the n×n upper A and the m×n B.
func DtrmmRightUpperTrans(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 {
		zeroBlock(m, n, b, ldb)
		return
	}
	for i := 0; i < m; i++ {
		btmp := b[i*ldb : i*ldb+n]
		for j, vb := range btmp {
			tmp := float64(vb * a[j*lda+j])
			tmp += DotUnitary(a[j*lda+j+1:j*lda+n], btmp[j+1:n])
			btmp[j] = alpha * tmp
		}
	}
}

// DsyrkUpper is blas/gonum Dsyrk(Upper, trans): the upper triangle of
// C = alpha·A·Aᵀ + beta·C (NoTrans, A n×k) or alpha·Aᵀ·A + beta·C
// (Trans, A k×n).
func DsyrkUpper(trans bool, n, k int, alpha float64, a []float64, lda int, beta float64, c []float64, ldc int) {
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
				d := DotUnitary(atmp, a[j*lda:j*lda+k])
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
				AxpyRaw(n-i, tmp, a[l*lda+i:l*lda+n], 1, ctmp, 1)
			}
		}
	}
}

// Dgemm is blas/gonum Dgemm: C = alpha·op(A)·op(B) + beta·C for the
// m×n C and inner dimension k. Beyond minParBlock 64×64 blocks gonum
// splits C into blocks and walks k in 64-wide chunks; the blocks run in
// parallel there but write disjoint parts of C, so running them in turn
// reproduces their bits. The k-chunking matters only for NoTrans·Trans,
// whose kernel adds one dot product per chunk.
func Dgemm(aTrans, bTrans bool, m, n, k int, alpha float64, a []float64, lda int, b []float64, ldb int, beta float64, c []float64, ldc int) {
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
	if blocks(m)*blocks(n) < minParBlock {
		dgemmSerial(aTrans, bTrans, m, n, k, a, lda, b, ldb, c, ldc, alpha)
		return
	}
	for i := 0; i < m; i += blockSize {
		leni := min(blockSize, m-i)
		for j := 0; j < n; j += blockSize {
			lenj := min(blockSize, n-j)
			cSub := c[i*ldc+j:]
			for l := 0; l < k; l += blockSize {
				lenk := min(blockSize, k-l)
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
				dgemmSerial(aTrans, bTrans, leni, lenj, lenk, aSub, lda, bSub, ldb, cSub, ldc, alpha)
			}
		}
	}
}

// blocks is blas/gonum blocks: the number of blockSize divisions of
// dim.
func blocks(dim int) int { return (dim + blockSize - 1) / blockSize }

// dgemmSerial is blas/gonum dgemmSerial{NotNot,TransNot,NotTrans}
// (TransTrans is never reached by the SPD path).
func dgemmSerial(aTrans, bTrans bool, m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	switch {
	case !aTrans && !bTrans:
		for i := 0; i < m; i++ {
			ctmp := c[i*ldc : i*ldc+n]
			for l, v := range a[i*lda : i*lda+k] {
				if tmp := alpha * v; tmp != 0 {
					AxpyRaw(n, tmp, b[l*ldb:l*ldb+n], 1, ctmp, 1)
				}
			}
		}
	case aTrans && !bTrans:
		for l := 0; l < k; l++ {
			btmp := b[l*ldb : l*ldb+n]
			for i, v := range a[l*lda : l*lda+m] {
				if tmp := alpha * v; tmp != 0 {
					AxpyRaw(n, tmp, btmp, 1, c[i*ldc:i*ldc+n], 1)
				}
			}
		}
	case !aTrans && bTrans:
		for i := 0; i < m; i++ {
			atmp := a[i*lda : i*lda+k]
			ctmp := c[i*ldc : i*ldc+n]
			for j := 0; j < n; j++ {
				ctmp[j] += float64(alpha * DotUnitary(atmp, b[j*ldb:j*ldb+k]))
			}
		}
	default:
		panic("gonumorder: dgemmSerial: Trans·Trans is not ported")
	}
}

// zeroBlock zeroes the m×n block b (the alpha == 0 branch of the
// level-3 routines).
func zeroBlock(m, n int, b []float64, ldb int) {
	for i := 0; i < m; i++ {
		btmp := b[i*ldb : i*ldb+n]
		for j := range btmp {
			btmp[j] = 0
		}
	}
}
