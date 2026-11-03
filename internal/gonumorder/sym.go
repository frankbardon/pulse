package gonumorder

import "math"

// The symmetric-operand routines internal/sem reaches through
// mat.Dense.Mul (Dsymm) and mat.VecDense.MulVec (Dsymv), the
// mat.Cholesky.LogDet sum, and the amd64 assembly math.Log it calls
// (package comment: the contract). A gonum SymDense stores its upper
// triangle, so only the Upper forms are ported.

// DsymmUpper is blas/gonum Dsymm(side, Upper): C = alpha·A·B + beta·C
// (left: A m×m symmetric) or C = alpha·B·A + beta·C (right: A n×n
// symmetric), for the m×n B and C, A read from its upper triangle.
// Pure Go in gonum except the left side's row updates, which run the
// AxpyUnitary kernel (element by element).
func DsymmUpper(left bool, m, n int, alpha float64, a []float64, lda int, b []float64, ldb int, beta float64, c []float64, ldc int) {
	if m == 0 || n == 0 {
		return
	}
	if alpha == 0 && beta == 1 {
		return
	}
	if beta == 0 {
		for i := 0; i < m; i++ {
			ctmp := c[i*ldc : i*ldc+n]
			for j := range ctmp {
				ctmp[j] = 0
			}
		}
	}
	if alpha == 0 {
		if beta != 0 {
			for i := 0; i < m; i++ {
				ctmp := c[i*ldc : i*ldc+n]
				for j := 0; j < n; j++ {
					ctmp[j] = float64(ctmp[j] * beta)
				}
			}
		}
		return
	}
	if left {
		for i := 0; i < m; i++ {
			atmp := alpha * a[i*lda+i]
			btmp := b[i*ldb : i*ldb+n]
			ctmp := c[i*ldc : i*ldc+n]
			for j, v := range btmp {
				ctmp[j] = float64(ctmp[j] * beta)
				ctmp[j] += float64(atmp * v)
			}
			for k := 0; k < i; k++ {
				atmp := a[k*lda+i]
				atmp *= alpha
				AxpyRaw(n, atmp, b[k*ldb:k*ldb+n], 1, ctmp, 1)
			}
			for k := i + 1; k < m; k++ {
				atmp := a[i*lda+k]
				atmp *= alpha
				AxpyRaw(n, atmp, b[k*ldb:k*ldb+n], 1, ctmp, 1)
			}
		}
		return
	}
	for i := 0; i < m; i++ {
		for j := n - 1; j >= 0; j-- {
			tmp := alpha * b[i*ldb+j]
			var tmp2 float64
			atmp := a[j*lda+j+1 : j*lda+n]
			btmp := b[i*ldb+j+1 : i*ldb+n]
			ctmp := c[i*ldc+j+1 : i*ldc+n]
			for k, v := range atmp {
				ctmp[k] += float64(tmp * v)
				tmp2 += float64(btmp[k] * v)
			}
			c[i*ldc+j] = float64(c[i*ldc+j] * beta)
			c[i*ldc+j] += float64(tmp*a[j*lda+j]) + float64(alpha*tmp2)
		}
	}
}

// DsymvUpper is blas/gonum Dsymv(Upper) for unit-stride x and y:
// y = alpha·A·x + beta·y for the n×n symmetric A read from its upper
// triangle (pure Go in gonum, the beta scaling the ScalUnitary kernel).
func DsymvUpper(n int, alpha float64, a []float64, lda int, x []float64, beta float64, y []float64) {
	if n == 0 {
		return
	}
	if alpha == 0 && beta == 1 {
		return
	}
	if beta != 1 {
		if beta == 0 {
			for i := range y[:n] {
				y[i] = 0
			}
		} else {
			ScalRaw(n, beta, y, 1)
		}
	}
	if alpha == 0 {
		return
	}
	if n == 1 {
		y[0] += float64(alpha * a[0] * x[0])
		return
	}
	for i := 0; i < n; i++ {
		xv := x[i] * alpha
		sum := float64(x[i] * a[i*lda+i])
		atmp := a[i*lda+i+1 : i*lda+n]
		for j, v := range atmp {
			jp := j + i + 1
			sum += float64(x[jp] * v)
			y[jp] += float64(xv * v)
		}
		y[i] += float64(alpha * sum)
	}
}

// CholeskyLogDet is mat.Cholesky.LogDet: Σ 2·log(U[i][i]) over the
// factor's diagonal in order, each logarithm LogAMD64 (the amd64 math.Log
// gonum calls there).
func CholeskyLogDet(n int, u []float64, lda int) float64 {
	var det float64
	for i := 0; i < n; i++ {
		det += float64(2 * LogAMD64(u[i*lda+i]))
	}
	return det
}

// LogAMD64 is the Go 1.26 standard library's amd64 assembly natural
// logarithm (math/log_amd64.s, archLog; Go BSD notice in
// THIRD_PARTY_NOTICES.md) in portable Go, step for step: the exponent
// and mantissa split by bit mask (so a subnormal argument is not
// renormalised), the k / f1 adjustment taken when f1 ≤ √2/2, then the
// same rational approximation in the same operation order. Every
// product is barriered. linalg/detmath.Log ports the pure-Go math.Log
// instead, which differs from this only at f1 = √2/2 exactly and for a
// subnormal argument.
func LogAMD64(x float64) float64 {
	const (
		hSqrt2 = 7.07106781186547524401e-01
		ln2Hi  = 6.93147180369123816490e-01
		ln2Lo  = 1.90821492927058770002e-10
		l1     = 6.666666666666735130e-01
		l2     = 3.999999999940941908e-01
		l3     = 2.857142874366239149e-01
		l4     = 2.222219843214978396e-01
		l5     = 1.818357216161805012e-01
		l6     = 1.531383769920937332e-01
		l7     = 1.479819860511658591e-01
	)
	bx := math.Float64bits(x)
	switch {
	case bx&^(1<<63) == 0:
		return math.Inf(-1)
	case int64(bx) < 0:
		return math.Float64frombits(0x7FF8000000000001)
	case int64(bx) >= 0x7FF0000000000000:
		return x // +Inf or NaN
	}
	f1 := math.Float64frombits(bx&0x000FFFFFFFFFFFFF | 0x3FE0000000000000)
	k := float64(int32(bx>>52&0x7FF) - 0x3FE)
	if !(hSqrt2 < f1) {
		k--
		f1 *= 2
	}
	f := f1 - 1
	s := f / (2 + f)
	s2 := s * s
	s4 := s2 * s2
	t1 := float64(s2 * (l1 + float64(s4*(l3+float64(s4*(l5+float64(s4*l7)))))))
	t2 := float64(s4 * (l2 + float64(s4*(l4+float64(s4*l6)))))
	r := t1 + t2
	hfsq := float64(float64(0.5*f) * f)
	return float64(k*ln2Hi) - ((hfsq - (float64(s*(hfsq+r)) + float64(k*ln2Lo))) - f)
}
