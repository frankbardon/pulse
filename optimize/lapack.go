package optimize

// Reference BLAS / LAPACK routines used by the SLSQP port, transcribed
// from the reference Fortran (column-major storage). Only the argument
// combinations SLSQP uses are implemented. Every product that feeds an
// addition or subtraction is an explicit float64(a*b), which the Go
// specification forbids the compiler from fusing into a multiply-add, so
// the arithmetic is identical on every architecture
// (TestOptimize_NoFMAContraction).

import "math"

const (
	lapackEps    = 0x1p-53   // dlamch('E'): relative machine epsilon
	lapackPrec   = 0x1p-52   // dlamch('P') = eps * base
	lapackSafmin = 0x1p-1022 // dlamch('S')
	lapackSmlnum = lapackSafmin / lapackEps
)

func ddot(n int, x, y []float64) float64 {
	s := 0.0
	for i := 0; i < n; i++ {
		s += float64(x[i] * y[i])
	}
	return s
}

// dnrm2 is the 2-norm with scaling against overflow and underflow.
func dnrm2(n int, x []float64) float64 {
	if n <= 0 {
		return 0
	}
	scale, ssq := 0.0, 1.0
	for i := 0; i < n; i++ {
		if x[i] != 0 {
			a := math.Abs(x[i])
			if scale < a {
				r := scale / a
				ssq = 1 + float64(ssq*r*r)
				scale = a
			} else {
				r := a / scale
				ssq += float64(r * r)
			}
		}
	}
	return scale * math.Sqrt(ssq)
}

func dscal(n int, a float64, x []float64) {
	for i := 0; i < n; i++ {
		x[i] *= a
	}
}

func daxpy(n int, a float64, x, y []float64) {
	if a == 0 {
		return
	}
	for i := 0; i < n; i++ {
		y[i] += float64(a * x[i])
	}
}

// dlapy2 is sqrt(x²+y²) avoiding unnecessary overflow.
func dlapy2(x, y float64) float64 {
	if math.IsNaN(x) {
		return x
	}
	if math.IsNaN(y) {
		return y
	}
	xa, ya := math.Abs(x), math.Abs(y)
	w, z := math.Max(xa, ya), math.Min(xa, ya)
	if z == 0 || w > math.MaxFloat64 {
		return w
	}
	return w * math.Sqrt(1+float64((z/w)*(z/w)))
}

func fsign(a, b float64) float64 {
	if b >= 0 {
		return math.Abs(a)
	}
	return -math.Abs(a)
}

// dlarfg generates an elementary reflector H with H·[alpha; x] = [beta; 0].
func dlarfg(n int, alpha *float64, x []float64) float64 {
	if n <= 1 {
		return 0
	}
	xnorm := dnrm2(n-1, x)
	if xnorm == 0 {
		return 0
	}
	beta := -fsign(dlapy2(*alpha, xnorm), *alpha)
	knt := 0
	if math.Abs(beta) < lapackSmlnum {
		rsafmn := 1 / lapackSmlnum
		for {
			knt++
			dscal(n-1, rsafmn, x)
			beta *= rsafmn
			*alpha *= rsafmn
			if !(math.Abs(beta) < lapackSmlnum && knt < 20) {
				break
			}
		}
		xnorm = dnrm2(n-1, x)
		beta = -fsign(dlapy2(*alpha, xnorm), *alpha)
	}
	tau := (beta - *alpha) / beta
	dscal(n-1, 1/(*alpha-beta), x)
	for j := 0; j < knt; j++ {
		beta *= lapackSmlnum
	}
	*alpha = beta
	return tau
}

// dlarfgp is dlarfg with a non-negative beta.
func dlarfgp(n int, alpha *float64, x []float64) float64 {
	if n <= 0 {
		return 0
	}
	xnorm := dnrm2(n-1, x)
	if xnorm == 0 {
		if *alpha >= 0 {
			return 0
		}
		for j := 0; j < n-1; j++ {
			x[j] = 0
		}
		*alpha = -*alpha
		return 2
	}
	beta := fsign(dlapy2(*alpha, xnorm), *alpha)
	knt := 0
	if math.Abs(beta) < lapackSmlnum {
		bignum := 1 / lapackSmlnum
		for {
			knt++
			dscal(n-1, bignum, x)
			beta *= bignum
			*alpha *= bignum
			if !(math.Abs(beta) < lapackSmlnum && knt < 20) {
				break
			}
		}
		xnorm = dnrm2(n-1, x)
		beta = fsign(dlapy2(*alpha, xnorm), *alpha)
	}
	savealpha := *alpha
	*alpha += beta
	var tau float64
	if beta < 0 {
		beta = -beta
		tau = -*alpha / beta
	} else {
		*alpha = xnorm * (xnorm / *alpha)
		tau = *alpha / beta
		*alpha = -*alpha
	}
	if math.Abs(tau) <= lapackSmlnum {
		if savealpha >= 0 {
			tau = 0
		} else {
			tau = 2
			for j := 0; j < n-1; j++ {
				x[j] = 0
			}
			beta = -savealpha
		}
	} else {
		dscal(n-1, 1 / *alpha, x)
	}
	for j := 0; j < knt; j++ {
		beta *= lapackSmlnum
	}
	*alpha = beta
	return tau
}

// dlarfLeft applies H = I − tau·v·vᵀ from the left to the m × n matrix
// c (leading dimension ldc); v has m entries (v[0] is used as stored).
func dlarfLeft(m, n int, v []float64, tau float64, c []float64, ldc int) {
	if tau == 0 {
		return
	}
	lastv := m
	for lastv > 0 && v[lastv-1] == 0 {
		lastv--
	}
	if lastv == 0 {
		return
	}
	// iladlc: last non-zero column of c(0:lastv, :)
	lastc := 0
	for j := n - 1; j >= 0; j-- {
		nz := false
		for i := 0; i < lastv; i++ {
			if c[i+j*ldc] != 0 {
				nz = true
				break
			}
		}
		if nz {
			lastc = j + 1
			break
		}
	}
	for j := 0; j < lastc; j++ {
		col := c[j*ldc:]
		w := 0.0
		for i := 0; i < lastv; i++ {
			w += float64(col[i] * v[i])
		}
		if w != 0 {
			t := -tau * w
			for i := 0; i < lastv; i++ {
				col[i] += float64(v[i] * t)
			}
		}
	}
}

// dgeqr2 computes the unblocked QR factorization of the m × n matrix a.
func dgeqr2(m, n int, a []float64, lda int, tau []float64) {
	k := min(m, n)
	for i := 0; i < k; i++ {
		tau[i] = dlarfg(m-i, &a[i+i*lda], a[min(i+1, m-1)+i*lda:])
		if i < n-1 {
			aii := a[i+i*lda]
			a[i+i*lda] = 1
			dlarfLeft(m-i, n-i-1, a[i+i*lda:], tau[i], a[i+(i+1)*lda:], lda)
			a[i+i*lda] = aii
		}
	}
}

// dorm2rLT applies Qᵀ (from dgeqr2's k reflectors) to the m × n matrix c
// from the left.
func dorm2rLT(m, n, k int, a []float64, lda int, tau, c []float64, ldc int) {
	for i := 0; i < k; i++ {
		aii := a[i+i*lda]
		a[i+i*lda] = 1
		dlarfLeft(m-i, n, a[i+i*lda:], tau[i], c[i:], ldc)
		a[i+i*lda] = aii
	}
}

// dtrsmRUNN solves X·A = B for X (A n × n upper triangular, non-unit),
// overwriting the m × n matrix b.
func dtrsmRUNN(m, n int, a []float64, lda int, b []float64, ldb int) {
	for j := 0; j < n; j++ {
		for k := 0; k < j; k++ {
			if akj := a[k+j*lda]; akj != 0 {
				for i := 0; i < m; i++ {
					b[i+j*ldb] -= float64(akj * b[i+k*ldb])
				}
			}
		}
		t := 1 / a[j+j*lda]
		for i := 0; i < m; i++ {
			b[i+j*ldb] *= t
		}
	}
}

// dtrsvUNN solves A·x = b (A upper triangular, non-unit) in place.
func dtrsvUNN(n int, a []float64, lda int, x []float64) {
	for j := n - 1; j >= 0; j-- {
		if x[j] != 0 {
			x[j] /= a[j+j*lda]
			t := x[j]
			for i := j - 1; i >= 0; i-- {
				x[i] -= float64(t * a[i+j*lda])
			}
		}
	}
}

// dgemvN computes y = alpha·A·x + beta·y for the m × n matrix A.
func dgemvN(m, n int, alpha float64, a []float64, lda int, x []float64, beta float64, y []float64) {
	if beta != 1 {
		for i := 0; i < m; i++ {
			if beta == 0 {
				y[i] = 0
			} else {
				y[i] *= beta
			}
		}
	}
	for j := 0; j < n; j++ {
		t := alpha * x[j]
		for i := 0; i < m; i++ {
			y[i] += float64(t * a[i+j*lda])
		}
	}
}

// dgemvT computes y = alpha·Aᵀ·x + beta·y for the m × n matrix A.
func dgemvT(m, n int, alpha float64, a []float64, lda int, x []float64, beta float64, y []float64) {
	for j := 0; j < n; j++ {
		t := 0.0
		for i := 0; i < m; i++ {
			t += float64(a[i+j*lda] * x[i])
		}
		if beta == 0 {
			y[j] = alpha * t
		} else {
			y[j] = float64(beta*y[j]) + float64(alpha*t)
		}
	}
}

// dtpsvLNU solves L·x = b, L unit lower triangular in packed storage.
func dtpsvLNU(n int, ap, x []float64) {
	kk := 0
	for j := 0; j < n; j++ {
		if x[j] != 0 {
			t := x[j]
			k := kk + 1
			for i := j + 1; i < n; i++ {
				x[i] -= float64(t * ap[k])
				k++
			}
		}
		kk += n - j
	}
}

// dtpmvLTU computes x = Lᵀ·x, L unit lower triangular in packed storage.
func dtpmvLTU(n int, ap, x []float64) {
	kk := 0
	for j := 0; j < n; j++ {
		t := x[j]
		k := kk + 1
		for i := j + 1; i < n; i++ {
			t += float64(ap[k] * x[i])
			k++
		}
		x[j] = t
		kk += n - j
	}
}

// dtpmvLNU computes x = L·x, L unit lower triangular in packed storage.
func dtpmvLNU(n int, ap, x []float64) {
	kk := n*(n+1)/2 - 1
	for j := n - 1; j >= 0; j-- {
		if x[j] != 0 {
			t := x[j]
			k := kk
			for i := n - 1; i > j; i-- {
				x[i] += float64(t * ap[k])
				k--
			}
		}
		kk -= n - j
	}
}

// dlartgp generates a plane rotation with a non-negative r.
func dlartgp(f, g float64) (cs, sn, r float64) {
	const safmn2 = 0x1p-484
	const safmx2 = 1 / safmn2
	switch {
	case g == 0:
		return fsign(1, f), 0, math.Abs(f)
	case f == 0:
		return 0, fsign(1, g), math.Abs(g)
	}
	f1, g1 := f, g
	scale := math.Max(math.Abs(f1), math.Abs(g1))
	switch {
	case scale >= safmx2:
		count := 0
		for {
			count++
			f1 *= safmn2
			g1 *= safmn2
			scale = math.Max(math.Abs(f1), math.Abs(g1))
			if !(scale >= safmx2 && count < 20) {
				break
			}
		}
		r = math.Sqrt(float64(f1*f1) + float64(g1*g1))
		cs, sn = f1/r, g1/r
		for i := 0; i < count; i++ {
			r *= safmx2
		}
	case scale <= safmn2:
		count := 0
		for {
			count++
			f1 *= safmx2
			g1 *= safmx2
			scale = math.Max(math.Abs(f1), math.Abs(g1))
			if !(scale <= safmn2) {
				break
			}
		}
		r = math.Sqrt(float64(f1*f1) + float64(g1*g1))
		cs, sn = f1/r, g1/r
		for i := 0; i < count; i++ {
			r *= safmn2
		}
	default:
		r = math.Sqrt(float64(f1*f1) + float64(g1*g1))
		cs, sn = f1/r, g1/r
	}
	if r < 0 {
		cs, sn, r = -cs, -sn, -r
	}
	return cs, sn, r
}

// The routines below serve the equality-constrained LSEI path (me > 0),
// which the bounds-only path never reaches: the RQ factorisation of the
// equality rows and its application to A, G and the solution.

// dnrm2s is dnrm2 over n entries of x at stride incx.
func dnrm2s(n int, x []float64, incx int) float64 {
	if n <= 0 {
		return 0
	}
	scale, ssq := 0.0, 1.0
	for i := 0; i < n; i++ {
		if xi := x[i*incx]; xi != 0 {
			a := math.Abs(xi)
			if scale < a {
				r := scale / a
				ssq = 1 + float64(ssq*r*r)
				scale = a
			} else {
				r := a / scale
				ssq += float64(r * r)
			}
		}
	}
	return scale * math.Sqrt(ssq)
}

// dscals is dscal over n entries of x at stride incx.
func dscals(n int, a float64, x []float64, incx int) {
	for i := 0; i < n; i++ {
		x[i*incx] = float64(x[i*incx] * a)
	}
}

// dlarfgs is dlarfg with x at stride incx (the RQ reflectors run along
// a row of a column-major matrix).
func dlarfgs(n int, alpha *float64, x []float64, incx int) float64 {
	if n <= 1 {
		return 0
	}
	xnorm := dnrm2s(n-1, x, incx)
	if xnorm == 0 {
		return 0
	}
	beta := -fsign(dlapy2(*alpha, xnorm), *alpha)
	knt := 0
	if math.Abs(beta) < lapackSmlnum {
		rsafmn := 1 / lapackSmlnum
		for {
			knt++
			dscals(n-1, rsafmn, x, incx)
			beta = float64(beta * rsafmn)
			*alpha = float64(*alpha * rsafmn)
			if !(math.Abs(beta) < lapackSmlnum && knt < 20) {
				break
			}
		}
		xnorm = dnrm2s(n-1, x, incx)
		beta = -fsign(dlapy2(*alpha, xnorm), *alpha)
	}
	tau := (beta - *alpha) / beta
	dscals(n-1, 1/(*alpha-beta), x, incx)
	for j := 0; j < knt; j++ {
		beta = float64(beta * lapackSmlnum)
	}
	*alpha = beta
	return tau
}

// dlarfRight applies H = I − tau·v·vᵀ from the right to the m × n
// matrix c (leading dimension ldc); v has n entries at stride incv.
func dlarfRight(m, n int, v []float64, incv int, tau float64, c []float64, ldc int) {
	if tau == 0 {
		return
	}
	lastv := n
	for lastv > 0 && v[(lastv-1)*incv] == 0 {
		lastv--
	}
	if lastv == 0 {
		return
	}
	// iladlr: last non-zero row of c(:, 0:lastv)
	lastc := 0
	for i := m - 1; i >= 0 && lastc == 0; i-- {
		for j := 0; j < lastv; j++ {
			if c[i+j*ldc] != 0 {
				lastc = i + 1
				break
			}
		}
	}
	if lastc == 0 {
		return
	}
	// w = C·v (dgemv 'N'), then C −= tau·w·vᵀ (dger).
	w := make([]float64, lastc)
	for j := 0; j < lastv; j++ {
		t := v[j*incv]
		for i := 0; i < lastc; i++ {
			w[i] += float64(t * c[i+j*ldc])
		}
	}
	for j := 0; j < lastv; j++ {
		if vj := v[j*incv]; vj != 0 {
			t := float64(-tau * vj)
			for i := 0; i < lastc; i++ {
				c[i+j*ldc] += float64(w[i] * t)
			}
		}
	}
}

// dlarfLeftS applies H = I − tau·v·vᵀ from the left to the m × n matrix
// c (leading dimension ldc); v has m entries at stride incv.
func dlarfLeftS(m, n int, v []float64, incv int, tau float64, c []float64, ldc int) {
	if tau == 0 {
		return
	}
	lastv := m
	for lastv > 0 && v[(lastv-1)*incv] == 0 {
		lastv--
	}
	if lastv == 0 {
		return
	}
	lastc := 0
	for j := n - 1; j >= 0 && lastc == 0; j-- {
		for i := 0; i < lastv; i++ {
			if c[i+j*ldc] != 0 {
				lastc = j + 1
				break
			}
		}
	}
	for j := 0; j < lastc; j++ {
		w := 0.0
		for i := 0; i < lastv; i++ {
			w += float64(c[i+j*ldc] * v[i*incv])
		}
		if w != 0 {
			t := float64(-tau * w)
			for i := 0; i < lastv; i++ {
				c[i+j*ldc] += float64(v[i*incv] * t)
			}
		}
	}
}

// dgerq2 computes the unblocked RQ factorisation A = R·Q of the m × n
// matrix a (m ≤ n): R lands in the last m columns, the reflectors
// H(i) in the rows to its left, Q = H(1)·H(2)···H(m).
func dgerq2(m, n int, a []float64, lda int, tau []float64) {
	k := min(m, n)
	for i := k - 1; i >= 0; i-- {
		row := m - k + i
		col := n - k + i
		tau[i] = dlarfgs(col+1, &a[row+col*lda], a[row:], lda)
		aii := a[row+col*lda]
		a[row+col*lda] = 1
		dlarfRight(row, col+1, a[row:], lda, tau[i], a, lda)
		a[row+col*lda] = aii
	}
}

// dormr2RT computes C·Qᵀ for the m × n matrix c, Q from dgerq2's k
// reflectors stored in the k × n matrix a.
func dormr2RT(m, n, k int, a []float64, lda int, tau, c []float64, ldc int) {
	if m == 0 || n == 0 || k == 0 {
		return
	}
	for i := k - 1; i >= 0; i-- {
		ni := n - k + i + 1
		aii := a[i+(n-k+i)*lda]
		a[i+(n-k+i)*lda] = 1
		dlarfRight(m, ni, a[i:], lda, tau[i], c, ldc)
		a[i+(n-k+i)*lda] = aii
	}
}

// dormr2LT computes Qᵀ·C for the m × n matrix c, Q from dgerq2's k
// reflectors stored in the k × m matrix a.
func dormr2LT(m, n, k int, a []float64, lda int, tau, c []float64, ldc int) {
	if m == 0 || n == 0 || k == 0 {
		return
	}
	for i := 0; i < k; i++ {
		mi := m - k + i + 1
		aii := a[i+(m-k+i)*lda]
		a[i+(m-k+i)*lda] = 1
		dlarfLeftS(mi, n, a[i:], lda, tau[i], c, ldc)
		a[i+(m-k+i)*lda] = aii
	}
}

// dtrsvUTN solves Aᵀ·x = b (A upper triangular, non-unit) in place.
func dtrsvUTN(n int, a []float64, lda int, x []float64) {
	for j := 0; j < n; j++ {
		t := x[j]
		for i := 0; i < j; i++ {
			t -= float64(a[i+j*lda] * x[i])
		}
		x[j] = t / a[j+j*lda]
	}
}

// dgemvTAcc computes y += alpha·Aᵀ·x for the m × n matrix A (dgemv 'T'
// with beta = 1; a quick return when m or n is zero).
func dgemvTAcc(m, n int, alpha float64, a []float64, lda int, x, y []float64) {
	if m == 0 || n == 0 {
		return
	}
	for j := 0; j < n; j++ {
		t := 0.0
		for i := 0; i < m; i++ {
			t += float64(a[i+j*lda] * x[i])
		}
		y[j] += float64(alpha * t)
	}
}
