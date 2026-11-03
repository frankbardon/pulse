package gonumorder

import "math"

// The general-matrix half of the port (package comment: the contract):
// mat.Dense.Inverse — Dlange(MaxRowSum), Dgetrf, Dgecon(MaxRowSum),
// Dgetri — and the BLAS routines only it reaches (Dswap, Dger, the
// lower-triangular Dtrsm, Dtrsv and Dlatrs variants). Every routine
// mirrors the gonum v0.17.0 routine of the same name over row-major
// storage with leading dimension lda.

// Dswap is blas/gonum Dswap for positive increments: an exact exchange.
func Dswap(n int, x []float64, incX int, y []float64, incY int) {
	for i, ix, iy := 0, 0, 0; i < n; i, ix, iy = i+1, ix+incX, iy+incY {
		x[ix], y[iy] = y[iy], x[ix]
	}
}

// Dlaswp is lapack/gonum Dlaswp with incX = 1: the row interchanges
// ipiv[k1..k2] applied in order to the n columns of a.
func Dlaswp(n int, a []float64, lda int, k1, k2 int, ipiv []int) {
	if n == 0 {
		return
	}
	for k := k1; k <= k2; k++ {
		if k == ipiv[k] {
			continue
		}
		Dswap(n, a[k*lda:], 1, a[ipiv[k]*lda:], 1)
	}
}

// Dger is blas/gonum Dger → internal/asm/f64 Ger (amd64) for positive
// increments: A += alpha·x·yᵀ. Per row the kernel forms t = alpha·x[i]
// once and adds y[j]·t into A[i][j]; every element is independent, so
// its 4/2/1 blocking never reorders an addition.
func Dger(m, n int, alpha float64, x []float64, incX int, y []float64, incY int, a []float64, lda int) {
	if m == 0 || n == 0 || alpha == 0 {
		return
	}
	for i, ix := 0, 0; i < m; i, ix = i+1, ix+incX {
		t := alpha * x[ix]
		row := a[i*lda : i*lda+n]
		for j, iy := 0, 0; j < n; j, iy = j+1, iy+incY {
			row[j] += float64(y[iy] * t)
		}
	}
}

// Dgetf2 is lapack/gonum Dgetf2: the unblocked LU factorisation with
// partial pivoting of the m×n a, in place (unit lower L below the
// diagonal, U on and above it), ipiv[j] the row swapped with row j. A
// zero pivot leaves ok false and the sweep continues, as gonum does.
func Dgetf2(m, n int, a []float64, lda int, ipiv []int) (ok bool) {
	mn := min(m, n)
	if mn == 0 {
		return true
	}
	ok = true
	for j := 0; j < mn; j++ {
		jp := j + Idamax(m-j, a[j*lda+j:], lda)
		ipiv[j] = jp
		if a[jp*lda+j] == 0 {
			ok = false
		} else {
			if jp != j {
				Dswap(n, a[j*lda:], 1, a[jp*lda:], 1)
			}
			if j < m-1 {
				aj := a[j*lda+j]
				if math.Abs(aj) >= dlamchS {
					Dscal(m-j-1, 1/aj, a[(j+1)*lda+j:], lda)
				} else {
					// gonum's tiny-pivot branch, element for element
					// (it rescales the first subdiagonal entry each
					// time).
					for i := 0; i < m-j-1; i++ {
						a[(j+1)*lda+j] = a[(j+1)*lda+j] / a[lda*j+j]
					}
				}
			}
		}
		if j < mn-1 {
			Dger(m-j-1, n-j-1, -1, a[(j+1)*lda+j:], lda, a[j*lda+j+1:], 1, a[(j+1)*lda+j+1:], lda)
		}
	}
	return ok
}

// Dgetrf is lapack/gonum Dgetrf: the LU factorisation with partial
// pivoting of the m×n a. Up to lapackNB it is Dgetf2; beyond, gonum's
// blocked Dgetf2 / Dlaswp / Dtrsm / Dgemm sweep. ok is false when U has
// an exact zero pivot.
func Dgetrf(m, n int, a []float64, lda int, ipiv []int) (ok bool) {
	mn := min(m, n)
	if mn == 0 {
		return true
	}
	nb := lapackNB
	if nb <= 1 || mn <= nb {
		return Dgetf2(m, n, a, lda, ipiv)
	}
	ok = true
	for j := 0; j < mn; j += nb {
		jb := min(mn-j, nb)
		if !Dgetf2(m-j, jb, a[j*lda+j:], lda, ipiv[j:j+jb]) {
			ok = false
		}
		for i := j; i <= min(m-1, j+jb-1); i++ {
			ipiv[i] = j + ipiv[i]
		}
		Dlaswp(j, a, lda, j, j+jb-1, ipiv[:j+jb])
		if j+jb < n {
			Dlaswp(n-j-jb, a[j+jb:], lda, j, j+jb-1, ipiv[:j+jb])
			DtrsmLeftLowerUnit(jb, n-j-jb, 1, a[j*lda+j:], lda, a[j*lda+j+jb:], lda)
			if j+jb < m {
				Dgemm(false, false, m-j-jb, n-j-jb, jb, -1,
					a[(j+jb)*lda+j:], lda,
					a[j*lda+j+jb:], lda,
					1, a[(j+jb)*lda+j+jb:], lda)
			}
		}
	}
	return ok
}

// DlangeMaxRowSum is lapack/gonum Dlange(MaxRowSum): the ∞-norm of the
// m×n a, each row's absolute sum in column order, NaN-propagating via
// math.Max.
func DlangeMaxRowSum(m, n int, a []float64, lda int) float64 {
	if m == 0 || n == 0 {
		return 0
	}
	var value float64
	for i := 0; i < m; i++ {
		var sum float64
		for j := 0; j < n; j++ {
			sum += math.Abs(a[i*lda+j])
		}
		value = math.Max(value, sum)
	}
	return value
}

// DtrsmLeftLowerUnit is blas/gonum Dtrsm(Left, Lower, NoTrans, Unit):
// solve L·X = alpha·B in place for the m×m unit lower L and the m×n B.
func DtrsmLeftLowerUnit(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
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
		for k, va := range a[i*lda : i*lda+i] {
			if va != 0 {
				AxpyRaw(n, -va, b[k*ldb:k*ldb+n], 1, btmp, 1)
			}
		}
	}
}

// DtrsmRightLowerUnit is blas/gonum Dtrsm(Right, Lower, NoTrans, Unit):
// solve X·L = alpha·B in place for the n×n unit lower L and the m×n B.
func DtrsmRightLowerUnit(m, n int, alpha float64, a []float64, lda int, b []float64, ldb int) {
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
		for k := n - 1; k >= 0; k-- {
			if btmp[k] == 0 {
				continue
			}
			AxpyRaw(k, -btmp[k], a[k*lda:k*lda+k], 1, btmp[:k], 1)
		}
	}
}

// Dtrsv is blas/gonum Dtrsv for a unit-stride x: solve op(A)·x = b in
// place for the n×n triangular A (upper or lower, unit or non-unit
// diagonal). Pure Go in gonum on every architecture; barriered here.
func Dtrsv(upper, trans, unit bool, n int, a []float64, lda int, x []float64) {
	if n == 0 {
		return
	}
	nonUnit := !unit
	if n == 1 {
		if nonUnit {
			x[0] /= a[0]
		}
		return
	}
	if !trans {
		if upper {
			for i := n - 1; i >= 0; i-- {
				var sum float64
				atmp := a[i*lda+i+1 : i*lda+n]
				for j, v := range atmp {
					sum += float64(x[i+j+1] * v)
				}
				x[i] -= sum
				if nonUnit {
					x[i] /= a[i*lda+i]
				}
			}
			return
		}
		for i := 0; i < n; i++ {
			var sum float64
			atmp := a[i*lda : i*lda+i]
			for j, v := range atmp {
				sum += float64(x[j] * v)
			}
			x[i] -= sum
			if nonUnit {
				x[i] /= a[i*lda+i]
			}
		}
		return
	}
	if upper {
		for i := 0; i < n; i++ {
			if nonUnit {
				x[i] /= a[i*lda+i]
			}
			xi := x[i]
			atmp := a[i*lda+i+1 : i*lda+n]
			for j, v := range atmp {
				x[j+i+1] -= float64(v * xi)
			}
		}
		return
	}
	for i := n - 1; i >= 0; i-- {
		if nonUnit {
			x[i] /= a[i*lda+i]
		}
		xi := x[i]
		atmp := a[i*lda : i*lda+i]
		for j, v := range atmp {
			x[j] -= float64(v * xi)
		}
	}
}

// Dlatrs is lapack/gonum Dlatrs for a unit-stride x: solve op(A)·x =
// scale·b with scaling that guards against overflow, for the n×n
// triangular A (upper or lower, unit or non-unit diagonal). cnorm holds
// the column norms of A's strict triangle (computed when normin is
// false). DlatrsUpper is the upper, non-unit specialisation the SPD
// path runs; this is the general routine Dgecon needs.
func Dlatrs(upper, trans, unit, normin bool, n int, a []float64, lda int, x, cnorm []float64) (scale float64) {
	if n == 0 {
		return 1
	}
	nonUnit := !unit
	smlnum := dlamchS / dlamchP
	bignum := 1 / smlnum
	scale = 1

	if !normin {
		if upper {
			cnorm[0] = 0
			for j := 1; j < n; j++ {
				cnorm[j] = Dasum(j, a[j:], lda)
			}
		} else {
			for j := 0; j < n-1; j++ {
				cnorm[j] = Dasum(n-j-1, a[(j+1)*lda+j:], lda)
			}
			cnorm[n-1] = 0
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
			if upper {
				for j := 1; j < n; j++ {
					tmax = math.Max(DlangeColumnMaxAbs(j, a[j:], lda), tmax)
				}
			} else {
				for j := 0; j < n-1; j++ {
					tmax = math.Max(DlangeColumnMaxAbs(n-j-1, a[(j+1)*lda+j:], lda), tmax)
				}
			}
			if tmax <= math.MaxFloat64 {
				tscal = 1 / float64(smlnum*tmax)
				for j := 0; j < n; j++ {
					if cnorm[j] <= math.MaxFloat64 {
						cnorm[j] *= tscal
					} else {
						cnorm[j] = 0
						if upper {
							for i := 0; i < j; i++ {
								cnorm[j] += float64(tscal * math.Abs(a[i*lda+j]))
							}
						} else {
							for i := j + 1; i < n; i++ {
								cnorm[j] += float64(tscal * math.Abs(a[i*lda+j]))
							}
						}
					}
				}
			} else {
				Dtrsv(upper, trans, unit, n, a, lda, x)
				return
			}
		}
	}

	j := Idamax(n, x, 1)
	xmax := math.Abs(x[j])
	xbnd := xmax
	var grow float64
	var jfirst, jlast, jinc int
	if !trans {
		if upper {
			jfirst = n - 1
			jlast = -1
			jinc = -1
		} else {
			jfirst = 0
			jlast = n
			jinc = 1
		}
		if tscal != 1 {
			grow = 0
			goto Solve
		}
		if nonUnit {
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
			grow = math.Min(1, 1/math.Max(xbnd, smlnum))
			for j := jfirst; j != jlast; j += jinc {
				if grow <= smlnum {
					goto Solve
				}
				grow *= 1 / (1 + cnorm[j])
			}
		}
	} else {
		if upper {
			jfirst = 0
			jlast = n
			jinc = 1
		} else {
			jfirst = n - 1
			jlast = -1
			jinc = -1
		}
		if tscal != 1 {
			grow = 0
			goto Solve
		}
		if nonUnit {
			grow = 1 / (math.Max(xbnd, smlnum))
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
		} else {
			grow = math.Min(1, 1/math.Max(xbnd, smlnum))
			for j := jfirst; j != jlast; j += jinc {
				if grow <= smlnum {
					goto Solve
				}
				xj := 1 + cnorm[j]
				grow /= xj
			}
		}
	}

Solve:
	if float64(grow*tscal) > smlnum {
		Dtrsv(upper, trans, unit, n, a, lda, x)
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
			var tjj, tjjs float64
			if nonUnit {
				tjjs = float64(a[j*lda+j] * tscal)
			} else {
				tjjs = tscal
				if tscal == 1 {
					goto Skip1
				}
			}
			tjj = math.Abs(tjjs)
			if tjj > smlnum {
				if tjj < 1 {
					if xj > float64(tjj*bignum) {
						rec := 1 / xj
						Dscal(n, rec, x, 1)
						scale *= rec
						xmax *= rec
					}
				}
				x[j] /= tjjs
				xj = math.Abs(x[j])
			} else if tjj > 0 {
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
			} else {
				for i := 0; i < n; i++ {
					x[i] = 0
				}
				x[j] = 1
				xj = 1
				scale = 0
				xmax = 0
			}
		Skip1:
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
			if upper {
				if j > 0 {
					Daxpy(j, float64(-x[j]*tscal), a[j:], lda, x, 1)
					i := Idamax(j, x, 1)
					xmax = math.Abs(x[i])
				}
			} else {
				if j < n-1 {
					Daxpy(n-j-1, float64(-x[j]*tscal), a[(j+1)*lda+j:], lda, x[j+1:], 1)
					i := j + Idamax(n-j-1, x[j+1:], 1)
					xmax = math.Abs(x[i])
				}
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
				if nonUnit {
					tjjs = float64(a[j*lda+j] * tscal)
				} else {
					tjjs = tscal
				}
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
				if upper {
					sumj = Ddot(j, a[j:], lda, x, 1)
				} else if j < n-1 {
					sumj = Ddot(n-j-1, a[(j+1)*lda+j:], lda, x[j+1:], 1)
				}
			} else {
				if upper {
					for i := 0; i < j; i++ {
						sumj += float64(float64(a[i*lda+j]*uscal) * x[i])
					}
				} else if j < n {
					for i := j + 1; i < n; i++ {
						sumj += float64(float64(a[i*lda+j]*uscal) * x[i])
					}
				}
			}
			if uscal == tscal {
				x[j] -= sumj
				xj := math.Abs(x[j])
				var tjjs float64
				if nonUnit {
					tjjs = float64(a[j*lda+j] * tscal)
				} else {
					tjjs = tscal
					if tscal == 1 {
						goto Skip2
					}
				}
				tjj := math.Abs(tjjs)
				if tjj > smlnum {
					if tjj < 1 {
						if xj > float64(tjj*bignum) {
							rec = 1 / xj
							Dscal(n, rec, x, 1)
							scale *= rec
							xmax *= rec
						}
					}
					x[j] /= tjjs
				} else if tjj > 0 {
					if xj > float64(tjj*bignum) {
						rec = float64(tjj*bignum) / xj
						Dscal(n, rec, x, 1)
						scale *= rec
						xmax *= rec
					}
					x[j] /= tjjs
				} else {
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
		Skip2:
			xmax = math.Max(xmax, math.Abs(x[j]))
		}
	}
	scale /= tscal
	if tscal != 1 {
		Dscal(n, 1/tscal, cnorm, 1)
	}
	return scale
}

// DgeconMaxRowSum is lapack/gonum Dgecon(MaxRowSum): the reciprocal
// ∞-norm condition estimate of A from its Dgetrf factors a and anorm =
// ‖A‖∞ (Hager–Higham estimator Dlacn2 over Dlatrs solves with L and U).
func DgeconMaxRowSum(n int, a []float64, lda int, anorm float64) float64 {
	if n == 0 {
		return 1
	}
	switch {
	case anorm == 0:
		return 0
	case math.IsNaN(anorm):
		return anorm
	case math.IsInf(anorm, 1):
		return 0
	}
	work := make([]float64, 4*n)
	iwork := make([]int, n)
	var (
		rcond, ainvnm float64
		kase          int
		normin        bool
		isave         [3]int
	)
	// The ∞-norm estimate solves with A on kase 2 (gonum's kase1 for
	// a MaxRowSum norm) and with Aᵀ on kase 1.
	const kase1 = 2
	for {
		ainvnm, kase = Dlacn2(n, work[n:], work, iwork, ainvnm, kase, &isave)
		if kase == 0 {
			if ainvnm != 0 {
				rcond = (1 / ainvnm) / anorm
			}
			return rcond
		}
		var sl, su float64
		if kase == kase1 {
			sl = Dlatrs(false, false, true, normin, n, a, lda, work, work[2*n:])
			su = Dlatrs(true, false, false, normin, n, a, lda, work, work[3*n:])
		} else {
			su = Dlatrs(true, true, false, normin, n, a, lda, work, work[3*n:])
			sl = Dlatrs(false, true, true, normin, n, a, lda, work, work[2*n:])
		}
		scale := sl * su
		normin = true
		if scale != 1 {
			ix := Idamax(n, work, 1)
			if scale == 0 || scale < float64(math.Abs(work[ix])*dlamchS) {
				return rcond
			}
			Drscl(n, scale, work, 1)
		}
	}
}

// Dgetri is lapack/gonum Dgetri with the workspace mat.Dense.Inverse
// gives it (n·lapackNB, so the block size never shrinks): A⁻¹ from the
// Dgetrf factors a and ipiv, in place — U⁻¹ by Dtrtri, then the solve
// for A⁻¹·L = U⁻¹ (column by column up to lapackNB, blocked beyond),
// then the column interchanges in reverse. False when U has a zero
// diagonal.
func Dgetri(n int, a []float64, lda int, ipiv []int) bool {
	if n == 0 {
		return true
	}
	if !DtrtriUpper(n, a, lda) {
		return false
	}
	nb := lapackNB
	const nbmin = 2
	ldwork := nb
	work := make([]float64, max(n*nb, 1))
	if nb < nbmin || n <= nb {
		for j := n - 1; j >= 0; j-- {
			for i := j + 1; i < n; i++ {
				work[i] = a[i*lda+j]
				a[i*lda+j] = 0
			}
			if j < n-1 {
				DgemvN(n, n-j-1, -1, a[(j+1):], lda, work[(j+1):], 1, 1, a[j:], lda)
			}
		}
	} else {
		nn := ((n - 1) / nb) * nb
		for j := nn; j >= 0; j -= nb {
			jb := min(nb, n-j)
			for jj := j; jj < j+jb; jj++ {
				for i := jj + 1; i < n; i++ {
					work[i*ldwork+(jj-j)] = a[i*lda+jj]
					a[i*lda+jj] = 0
				}
			}
			if j+jb < n {
				Dgemm(false, false, n, jb, n-j-jb, -1, a[(j+jb):], lda, work[(j+jb)*ldwork:], ldwork, 1, a[j:], lda)
			}
			DtrsmRightLowerUnit(n, jb, 1, work[j*ldwork:], ldwork, a[j:], lda)
		}
	}
	for j := n - 2; j >= 0; j-- {
		jp := ipiv[j]
		if jp != j {
			Dswap(n, a[j:], lda, a[jp:], lda)
		}
	}
	return true
}

// Inverse is the numeric core of mat.Dense.Inverse: a (n×n, row-major,
// leading dimension lda) is overwritten in place and the condition
// estimate is returned. It mirrors gonum's steps and stops exactly:
//
//   - the ∞-norm (Dlange), then Dgetrf; an exact zero pivot returns +Inf
//     at once, leaving a holding the LU factors (gonum returns
//     Condition(+Inf) there without inverting);
//   - otherwise Dgecon's estimate and Dgetri, which always runs; a
//     Dgetri failure or a zero reciprocal estimate returns +Inf,
//     anything else 1/rcond.
//
// The caller reports mat.Condition(cond) when cond exceeds the
// tolerance (gonum's ConditionTolerance, 1e16), as gonum does — a is
// filled either way.
func Inverse(n int, a []float64, lda int) (cond float64) {
	if n == 0 {
		return 1
	}
	norm := DlangeMaxRowSum(n, n, a, lda)
	ipiv := make([]int, n)
	if !Dgetrf(n, n, a, lda, ipiv) {
		return math.Inf(1)
	}
	rcond := DgeconMaxRowSum(n, a, lda, norm)
	if !Dgetri(n, a, lda, ipiv) || rcond == 0 {
		return math.Inf(1)
	}
	return 1 / rcond
}
