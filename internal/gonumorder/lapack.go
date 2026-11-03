package gonumorder

import "math"

// The LAPACK half of the SPD port (package comment: the contract). Every
// routine mirrors the lapack/gonum routine of the same name in gonum
// v0.17.0, restricted to an upper triangle and a non-unit diagonal, over
// row-major storage with leading dimension lda. The general-matrix
// routines internal/sem reaches are in ge.go.

// gonum's machine constants (lapack/gonum/lapack.go): dlamchS is the
// smallest normal number, dlamchP is eps·base = 2⁻⁵².
const (
	dlamchS = 0x1p-1022
	dlamchP = 0x1p-52
)

// DlansyUpperMaxRowSum is lapack/gonum Dlansy(MaxRowSum, Upper): the
// 1-norm (= ∞-norm) of the symmetric matrix whose upper triangle is a.
// work needs n elements.
func DlansyUpperMaxRowSum(n int, a []float64, lda int, work []float64) float64 {
	if n == 0 {
		return 0
	}
	for i := 0; i < n; i++ {
		work[i] = 0
	}
	for i := 0; i < n; i++ {
		work[i] += math.Abs(a[i*lda+i])
		for j := i + 1; j < n; j++ {
			v := math.Abs(a[i*lda+j])
			work[i] += v
			work[j] += v
		}
	}
	var maxV float64
	for i := 0; i < n; i++ {
		v := work[i]
		if math.IsNaN(v) {
			return math.NaN()
		}
		if v > maxV {
			maxV = v
		}
	}
	return maxV
}

// DpotrfUpper is lapack/gonum Dpotrf(Upper): A = Uᵀ·U in place. Up to
// lapackNB it is the unblocked Dpotf2Upper; beyond, gonum's blocked
// Dsyrk / Dpotf2 / Dgemm / Dtrsm sweep. It reports whether A is positive
// definite.
func DpotrfUpper(n int, a []float64, lda int) bool {
	if n == 0 {
		return true
	}
	nb := lapackNB
	if n <= nb {
		return Dpotf2Upper(n, a, lda)
	}
	for j := 0; j < n; j += nb {
		jb := min(nb, n-j)
		DsyrkUpper(true, jb, j, -1, a[j:], lda, 1, a[j*lda+j:], lda)
		if !Dpotf2Upper(jb, a[j*lda+j:], lda) {
			return false
		}
		if j+jb < n {
			Dgemm(true, false, jb, n-j-jb, j, -1, a[j:], lda, a[j+jb:], lda, 1, a[j*lda+j+jb:], lda)
			DtrsmLeftUpper(true, jb, n-j-jb, 1, a[j*lda+j:], lda, a[j*lda+j+jb:], lda)
		}
	}
	return true
}

// Dpotf2Upper is lapack/gonum Dpotf2(Upper): per column j, the pivot
// A[j][j] − Ddot(column j above the diagonal), its square root, then
// the row tail updated by Dgemv(Trans) and scaled by the pivot's
// reciprocal. A pivot ≤ 0 or NaN stops the factorisation (false).
func Dpotf2Upper(n int, a []float64, lda int) bool {
	for j := 0; j < n; j++ {
		ajj := a[j*lda+j]
		if j != 0 {
			ajj -= Ddot(j, a[j:], lda, a[j:], lda)
		}
		if ajj <= 0 || math.IsNaN(ajj) {
			a[j*lda+j] = ajj
			return false
		}
		ajj = math.Sqrt(ajj)
		a[j*lda+j] = ajj
		if j < n-1 {
			DgemvT(j, n-j-1, -1, a[j+1:], lda, a[j:], lda, 1, a[j*lda+j+1:], 1)
			Dscal(n-j-1, 1/ajj, a[j*lda+j+1:], 1)
		}
	}
	return true
}

// DpoconUpper is lapack/gonum Dpocon(Upper): the reciprocal 1-norm
// condition estimate of A = Uᵀ·U from the factor u and anorm = ‖A‖₁
// (Hager–Higham estimator Dlacn2 over Dlatrs triangular solves).
func DpoconUpper(n int, u []float64, lda int, anorm float64) float64 {
	if n == 0 {
		return 1
	}
	if anorm == 0 {
		return 0
	}
	work := make([]float64, 3*n)
	iwork := make([]int, n)
	var (
		rcond, ainvnm float64
		normin        bool
		kase          int
		isave         [3]int
	)
	for {
		ainvnm, kase = Dlacn2(n, work[n:], work, iwork, ainvnm, kase, &isave)
		if kase == 0 {
			if ainvnm != 0 {
				rcond = (1 / ainvnm) / anorm
			}
			return rcond
		}
		sl := DlatrsUpper(true, normin, n, u, lda, work, work[2*n:])
		normin = true
		su := DlatrsUpper(false, normin, n, u, lda, work, work[2*n:])
		scale := sl * su
		if scale != 1 {
			ix := Idamax(n, work, 1)
			if scale == 0 || scale < float64(math.Abs(work[ix])*dlamchS) {
				return rcond
			}
			Drscl(n, scale, work, 1)
		}
	}
}

// Dlacn2 is lapack/gonum Dlacn2: one reverse-communication step of the
// 1-norm estimator. v and x hold n elements, isgn n ints.
func Dlacn2(n int, v, x []float64, isgn []int, est float64, kase int, isave *[3]int) (float64, int) {
	const itmax = 5
	if kase == 0 {
		for i := 0; i < n; i++ {
			x[i] = 1 / float64(n)
		}
		isave[0] = 1
		return est, 1
	}
	switch isave[0] {
	case 1:
		if n == 1 {
			v[0] = x[0]
			return math.Abs(v[0]), 0
		}
		est = Dasum(n, x, 1)
		for i := 0; i < n; i++ {
			x[i] = math.Copysign(1, x[i])
			isgn[i] = int(x[i])
		}
		isave[0] = 2
		return est, 2
	case 2:
		isave[1] = Idamax(n, x, 1)
		isave[2] = 2
		for i := 0; i < n; i++ {
			x[i] = 0
		}
		x[isave[1]] = 1
		isave[0] = 3
		return est, 1
	case 3:
		copy(v[:n], x[:n])
		estold := est
		est = Dasum(n, v, 1)
		sameSigns := true
		for i := 0; i < n; i++ {
			if int(math.Copysign(1, x[i])) != isgn[i] {
				sameSigns = false
				break
			}
		}
		if !sameSigns && est > estold {
			for i := 0; i < n; i++ {
				x[i] = math.Copysign(1, x[i])
				isgn[i] = int(x[i])
			}
			isave[0] = 4
			return est, 2
		}
	case 4:
		jlast := isave[1]
		isave[1] = Idamax(n, x, 1)
		if x[jlast] != math.Abs(x[isave[1]]) && isave[2] < itmax {
			isave[2]++
			for i := 0; i < n; i++ {
				x[i] = 0
			}
			x[isave[1]] = 1
			isave[0] = 3
			return est, 1
		}
	case 5:
		tmp := 2 * (Dasum(n, x, 1)) / float64(3*n)
		if tmp > est {
			copy(v[:n], x[:n])
			est = tmp
		}
		return est, 0
	}
	altsgn := 1.0
	for i := 0; i < n; i++ {
		x[i] = altsgn * (1 + float64(i)/float64(n-1))
		altsgn *= -1
	}
	isave[0] = 5
	return est, 1
}

// Drscl is lapack/gonum Drscl: x /= a, applied as a multiply by 1/a in
// at most a few under/overflow-safe steps.
func Drscl(n int, a float64, x []float64, incX int) {
	if n == 0 {
		return
	}
	cden := a
	cnum := 1.0
	smlnum := dlamchS
	bignum := 1 / smlnum
	for {
		cden1 := cden * smlnum
		cnum1 := cnum / bignum
		var mul float64
		var done bool
		switch {
		case cnum != 0 && math.Abs(cden1) > math.Abs(cnum):
			mul = smlnum
			cden = cden1
		case math.Abs(cnum1) > math.Abs(cden):
			mul = bignum
			cnum = cnum1
		default:
			mul = cnum / cden
			done = true
		}
		Dscal(n, mul, x, incX)
		if done {
			return
		}
	}
}

// DlatrsUpper is lapack/gonum Dlatrs(Upper, trans, NonUnit): solve
// op(U)·x = scale·b with scaling that guards against overflow, for a
// unit-stride x. cnorm holds the column norms of U's strict upper part
// (computed when normin is false). The common case is one Dtrsv; the
// careful per-column loop runs only when the growth bound says it must.
func DlatrsUpper(trans, normin bool, n int, a []float64, lda int, x, cnorm []float64) float64 {
	if n == 0 {
		return 1
	}
	smlnum := dlamchS / dlamchP
	bignum := 1 / smlnum
	scale := 1.0

	if !normin {
		cnorm[0] = 0
		for j := 1; j < n; j++ {
			cnorm[j] = Dasum(j, a[j:], lda)
		}
	}
	imax := Idamax(n, cnorm, 1)
	var tscal float64
	if cnorm[imax] <= bignum {
		tscal = 1
	} else {
		tmax := cnorm[imax]
		if tmax <= math.MaxFloat64 {
			tscal = 1 / float64(smlnum*tmax)
			Dscal(n, tscal, cnorm, 1)
		} else {
			tmax = 0
			for j := 1; j < n; j++ {
				tmax = math.Max(DlangeColumnMaxAbs(j, a[j:], lda), tmax)
			}
			if tmax <= math.MaxFloat64 {
				tscal = 1 / float64(smlnum*tmax)
				for j := 0; j < n; j++ {
					if cnorm[j] <= math.MaxFloat64 {
						cnorm[j] *= tscal
					} else {
						cnorm[j] = 0
						for i := 0; i < j; i++ {
							cnorm[j] += float64(tscal * math.Abs(a[i*lda+j]))
						}
					}
				}
			} else {
				DtrsvUpper(trans, n, a, lda, x)
				return scale
			}
		}
	}

	j := Idamax(n, x, 1)
	xmax := math.Abs(x[j])
	xbnd := xmax
	var grow float64
	var jfirst, jlast, jinc int
	if !trans {
		jfirst, jlast, jinc = n-1, -1, -1
		if tscal != 1 {
			grow = 0
			goto Solve
		}
		grow = 1 / math.Max(xbnd, smlnum)
		xbnd = grow
		for j := jfirst; j != jlast; j += jinc {
			if grow <= smlnum {
				goto Solve
			}
			tjj := math.Abs(a[j*lda+j])
			xbnd = math.Min(xbnd, float64(math.Min(1, tjj)*grow))
			if tjj+cnorm[j] >= smlnum {
				grow *= tjj / (tjj + cnorm[j])
			} else {
				grow = 0
			}
		}
		grow = xbnd
	} else {
		jfirst, jlast, jinc = 0, n, 1
		if tscal != 1 {
			grow = 0
			goto Solve
		}
		grow = 1 / math.Max(xbnd, smlnum)
		xbnd = grow
		for j := jfirst; j != jlast; j += jinc {
			if grow <= smlnum {
				goto Solve
			}
			xj := 1 + cnorm[j]
			grow = math.Min(grow, xbnd/xj)
			tjj := math.Abs(a[j*lda+j])
			if xj > tjj {
				xbnd *= tjj / xj
			}
		}
		grow = math.Min(grow, xbnd)
	}

Solve:
	if float64(grow*tscal) > smlnum {
		DtrsvUpper(trans, n, a, lda, x)
		if tscal != 1 {
			Dscal(n, 1/tscal, cnorm, 1)
		}
		return scale
	}

	if xmax > bignum {
		scale = bignum / xmax
		Dscal(n, scale, x, 1)
		xmax = bignum
	}
	if !trans {
		for j := jfirst; j != jlast; j += jinc {
			xj := math.Abs(x[j])
			tjjs := float64(a[j*lda+j] * tscal)
			tjj := math.Abs(tjjs)
			switch {
			case tjj > smlnum:
				if tjj < 1 && xj > float64(tjj*bignum) {
					rec := 1 / xj
					Dscal(n, rec, x, 1)
					scale *= rec
					xmax *= rec
				}
				x[j] /= tjjs
				xj = math.Abs(x[j])
			case tjj > 0:
				if xj > float64(tjj*bignum) {
					rec := float64(tjj*bignum) / xj
					if cnorm[j] > 1 {
						rec /= cnorm[j]
					}
					Dscal(n, rec, x, 1)
					scale *= rec
					xmax *= rec
				}
				x[j] /= tjjs
				xj = math.Abs(x[j])
			default:
				for i := 0; i < n; i++ {
					x[i] = 0
				}
				x[j] = 1
				xj = 1
				scale = 0
				xmax = 0
			}
			if xj > 1 {
				rec := 1 / xj
				if cnorm[j] > float64((bignum-xmax)*rec) {
					rec *= 0.5
					Dscal(n, rec, x, 1)
					scale *= rec
				}
			} else if float64(xj*cnorm[j]) > bignum-xmax {
				Dscal(n, 0.5, x, 1)
				scale *= 0.5
			}
			if j > 0 {
				Daxpy(j, float64(-x[j]*tscal), a[j:], lda, x, 1)
				i := Idamax(j, x, 1)
				xmax = math.Abs(x[i])
			}
		}
	} else {
		for j := jfirst; j != jlast; j += jinc {
			xj := math.Abs(x[j])
			uscal := tscal
			rec := 1 / math.Max(xmax, 1)
			var tjjs float64
			if cnorm[j] > float64((bignum-xj)*rec) {
				rec *= 0.5
				tjjs = float64(a[j*lda+j] * tscal)
				tjj := math.Abs(tjjs)
				if tjj > 1 {
					rec = math.Min(1, float64(rec*tjj))
					uscal /= tjjs
				}
				if rec < 1 {
					Dscal(n, rec, x, 1)
					scale *= rec
					xmax *= rec
				}
			}
			var sumj float64
			if uscal == 1 {
				sumj = Ddot(j, a[j:], lda, x, 1)
			} else {
				for i := 0; i < j; i++ {
					sumj += float64(float64(a[i*lda+j]*uscal) * x[i])
				}
			}
			if uscal == tscal {
				x[j] -= sumj
				xj := math.Abs(x[j])
				tjjs := float64(a[j*lda+j] * tscal)
				tjj := math.Abs(tjjs)
				switch {
				case tjj > smlnum:
					if tjj < 1 && xj > float64(tjj*bignum) {
						rec = 1 / xj
						Dscal(n, rec, x, 1)
						scale *= rec
						xmax *= rec
					}
					x[j] /= tjjs
				case tjj > 0:
					if xj > float64(tjj*bignum) {
						rec = float64(tjj*bignum) / xj
						Dscal(n, rec, x, 1)
						scale *= rec
						xmax *= rec
					}
					x[j] /= tjjs
				default:
					for i := 0; i < n; i++ {
						x[i] = 0
					}
					x[j] = 1
					scale = 0
					xmax = 0
				}
			} else {
				x[j] = x[j]/tjjs - sumj
			}
			xmax = math.Max(xmax, math.Abs(x[j]))
		}
	}
	scale /= tscal
	if tscal != 1 {
		Dscal(n, 1/tscal, cnorm, 1)
	}
	return scale
}

// DlangeColumnMaxAbs is lapack/gonum Dlange(MaxAbs) over the m×1
// column a[0], a[lda], …: the largest |a[i·lda]|, NaN-propagating via
// math.Max.
func DlangeColumnMaxAbs(m int, a []float64, lda int) float64 {
	var value float64
	for i := 0; i < m; i++ {
		value = math.Max(value, math.Abs(a[i*lda]))
	}
	return value
}

// DpotrsUpperVec is lapack/gonum Dpotrs(Upper) for one right-hand
// side: b ← A⁻¹·b through Uᵀ·y = b then U·x = y (two Dtrsm calls).
func DpotrsUpperVec(n int, u []float64, lda int, b []float64) {
	if n == 0 {
		return
	}
	DtrsmLeftUpper(true, n, 1, 1, u, lda, b, 1)
	DtrsmLeftUpper(false, n, 1, 1, u, lda, b, 1)
}

// DpotriUpper is lapack/gonum Dpotri(Upper): overwrite the factor U
// with the upper triangle of A⁻¹ = U⁻¹·U⁻ᵀ (Dtrtri, then Dlauum). False
// when U has a zero diagonal.
func DpotriUpper(n int, a []float64, lda int) bool {
	if n == 0 {
		return true
	}
	if !DtrtriUpper(n, a, lda) {
		return false
	}
	DlauumUpper(n, a, lda)
	return true
}

// DtrtriUpper is lapack/gonum Dtrtri(Upper, NonUnit): U ← U⁻¹ in
// place, blocked by lapackNB.
func DtrtriUpper(n int, a []float64, lda int) bool {
	for i := 0; i < n; i++ {
		if a[i*lda+i] == 0 {
			return false
		}
	}
	nb := lapackNB
	if nb > n {
		Dtrti2Upper(n, a, lda)
		return true
	}
	for j := 0; j < n; j += nb {
		jb := min(nb, n-j)
		DtrmmLeftUpperNoTrans(j, jb, 1, a, lda, a[j:], lda)
		DtrsmRightUpperNoTrans(j, jb, -1, a[j*lda+j:], lda, a[j:], lda)
		Dtrti2Upper(jb, a[j*lda+j:], lda)
	}
	return true
}

// Dtrti2Upper is lapack/gonum Dtrti2(Upper, NonUnit): the unblocked
// triangular inverse, column by column.
func Dtrti2Upper(n int, a []float64, lda int) {
	for j := 0; j < n; j++ {
		ajj := 1 / a[j*lda+j]
		a[j*lda+j] = ajj
		ajj *= -1
		DtrmvUpperNoTrans(j, a, lda, a[j:], lda)
		Dscal(j, ajj, a[j:], lda)
	}
}

// DlauumUpper is lapack/gonum Dlauum(Upper): U ← U·Uᵀ (upper
// triangle) in place, blocked by lapackNB.
func DlauumUpper(n int, a []float64, lda int) {
	if n == 0 {
		return
	}
	nb := lapackNB
	if n <= nb {
		Dlauu2Upper(n, a, lda)
		return
	}
	for i := 0; i < n; i += nb {
		ib := min(nb, n-i)
		DtrmmRightUpperTrans(i, ib, 1, a[i*lda+i:], lda, a[i:], lda)
		Dlauu2Upper(ib, a[i*lda+i:], lda)
		if n-i-ib > 0 {
			Dgemm(false, true, i, ib, n-i-ib, 1, a[i+ib:], lda, a[i*lda+i+ib:], lda, 1, a[i:], lda)
			DsyrkUpper(false, ib, n-i-ib, 1, a[i*lda+i+ib:], lda, 1, a[i*lda+i:], lda)
		}
	}
}

// Dlauu2Upper is lapack/gonum Dlauu2(Upper): the unblocked U·Uᵀ.
func Dlauu2Upper(n int, a []float64, lda int) {
	for i := 0; i < n; i++ {
		aii := a[i*lda+i]
		if i < n-1 {
			a[i*lda+i] = Ddot(n-i, a[i*lda+i:], 1, a[i*lda+i:], 1)
			DgemvN(i, n-i-1, 1, a[i+1:], lda, a[i*lda+i+1:], 1, aii, a[i:], lda)
		} else {
			Dscal(i+1, aii, a[i:], lda)
		}
	}
}
