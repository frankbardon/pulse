package sem

// A Go port of SciPy 1.18.1's SLSQP (scipy/optimize/src/slsqp.c,
// nnls.c and _slsqp_py.py; BSD-3-Clause, Copyright (c) 2001-2002
// Enthought, Inc. 2003, SciPy Developers; the algorithm is Dieter
// Kraft's), restricted to what semopy uses: simple bounds on x and no
// general equality or inequality constraints (m = meq = 0). The
// iteration — QP subproblem, L1 line search, BFGS update of the LDLᵀ
// factors, stopping rules — follows the C source statement by
// statement, so a semopy fit's early stop at its default ftol is
// reproduced rather than run past.

import "math"

// SLSQP exit modes (SciPy's messages).
const (
	slsqpSuccess   = 0
	slsqpLSQIter   = 3
	slsqpIncompat  = 4
	slsqpSingularE = 5
	slsqpSingularC = 6
	slsqpRankHFTI  = 7
	slsqpPosDir    = 8
	slsqpIterLimit = 9
)

var slsqpMessages = map[int]string{
	0: "Optimization terminated successfully",
	2: "More equality constraints than independent variables",
	3: "More than 3*n iterations in LSQ subproblem",
	4: "Inequality constraints incompatible",
	5: "Singular matrix E in LSQ subproblem",
	6: "Singular matrix C in LSQ subproblem",
	7: "Rank-deficient equality constraint subproblem HFTI",
	8: "Positive directional derivative for linesearch",
	9: "Iteration limit reached",
}

type slsqpResult struct {
	x    []float64
	fun  float64
	mode int
	nit  int
	nfev int
	njev int
}

// slsqpMinimize minimises f with gradient grad subject to xl ≤ x ≤ xu
// (NaN marks an absent bound), as scipy.optimize.minimize(method=
// "SLSQP", jac=grad, bounds=..., options={"ftol": acc, "maxiter": itermax}).
func slsqpMinimize(f func([]float64) float64, grad func([]float64) []float64, x0, xl, xu []float64, acc float64, itermax int) slsqpResult {
	n := len(x0)
	x := make([]float64, n+1) // +1 for the augmented variable
	for i := 0; i < n; i++ {
		v := x0[i]
		if !math.IsNaN(xl[i]) && v < xl[i] {
			v = xl[i]
		}
		if !math.IsNaN(xu[i]) && v > xu[i] {
			v = xu[i]
		}
		x[i] = v
	}
	res := slsqpResult{}
	fx := f(x[:n])
	res.nfev++
	g := grad(x[:n])
	res.njev++

	n1 := n + 1
	bfgs := make([]float64, n1*n/2)
	x0s := make([]float64, n)
	s := make([]float64, n1)
	u := make([]float64, n1)
	v := make([]float64, n1)
	mult := make([]float64, 2*n+2)

	acc = math.Abs(acc)
	tol := 10 * acc
	iter, reset := 0, 0
	var alpha, f0, gs, h1, h2, h3, h4, t0 float64
	var mode int
	badlin := false

	finish := func(m int) slsqpResult {
		res.x = append([]float64(nil), x[:n]...)
		res.fun, res.mode, res.nit = fx, m, iter
		return res
	}

resetBFGS:
	reset++
	if reset > 5 {
		// LABEL255: relaxed convergence after repeated resets.
		h3 = 0
		if (math.Abs(fx-f0) < tol || dnrm2(n, s) < tol) && h3 < tol && !badlin && fx == fx {
			return finish(slsqpSuccess)
		}
		return finish(slsqpPosDir)
	}
	for i := range bfgs {
		bfgs[i] = 0
	}
	for i, j := 0, 0; i < n; i++ {
		bfgs[j] = 1
		j += n - i
	}

iterStart:
	if iter >= itermax {
		return finish(slsqpIterLimit)
	}
	iter++
	for i := 0; i < n; i++ {
		u[i] = -x[i] + xl[i]
		v[i] = -x[i] + xu[i]
	}
	h4 = 1
	mode = lsq(n, false, 0, bfgs, g, u, v, s, mult)
	badlin = false
	if mode == slsqpIncompat {
		badlin = true
		for i := 0; i < n; i++ {
			s[i] = 0
		}
		h3 = 0
		rho := 100.0
		inconsistent := 0
		for {
			mode = lsq(n, true, rho, bfgs, g, u, v, s, mult)
			h4 = 1 - s[n]
			if mode == slsqpIncompat {
				rho *= 10
				inconsistent++
				if inconsistent > 5 {
					return finish(mode)
				}
				continue
			} else if mode != 1 {
				return finish(mode)
			}
			break
		}
	} else if mode != 1 {
		return finish(mode)
	}
	// Multipliers for the L1 test (no general constraints: v = g).
	copy(v, g)
	f0 = fx
	copy(x0s, x[:n])
	gs = ddot(n, g, s)
	h1 = math.Abs(gs)
	h2 = 0
	if h1 < acc && h2 < acc && !badlin && fx == fx {
		return finish(slsqpSuccess)
	}
	h1 = 0
	t0 = fx + h1
	h3 = gs - h1*h4
	if h3 >= 0 {
		goto resetBFGS
	}
	alpha = 1
	for line := 1; ; line++ {
		h3 = alpha * h3
		dscal(n, alpha, s)
		copy(x[:n], x0s)
		daxpy(n, 1, s, x)
		fx = f(x[:n])
		res.nfev++
		h1 = fx - t0
		if !(h1 <= h3/10 || line > 10) {
			alpha = math.Max(h3/(2*(h3-h1)), 0.1)
			continue
		}
		break
	}
	h3 = 0
	if (math.Abs(fx-f0) < acc || dnrm2(n, s) < acc) && h3 < acc && !badlin && fx == fx {
		return finish(slsqpSuccess)
	}
	g = grad(x[:n])
	res.njev++
	// BFGS update of the LDLᵀ factors.
	for i := 0; i < n; i++ {
		u[i] = g[i] - v[i]
	}
	copy(v[:n], s[:n])
	dtpmvLTU(n, bfgs, v)
	for i, j := 0, 0; i < n; i++ {
		v[i] = bfgs[j] * v[i]
		j += n - i
	}
	dtpmvLNU(n, bfgs, v)
	h1 = ddot(n, s, u)
	h2 = ddot(n, s, v)
	h3 = 0.2 * h2
	if h1 < h3 {
		h4 = (h2 - h3) / (h2 - h1)
		h1 = h3
		dscal(n, h4, u)
		daxpy(n, 1-h4, v, u)
	}
	if h1 == 0 || h2 == 0 {
		goto resetBFGS
	}
	ldlUpdate(n, bfgs, u, 1/h1, v)
	ldlUpdate(n, bfgs, v, -1/h2, u)
	goto iterStart
}

// lsq solves the bound-constrained QP subproblem
//
//	min |A·x − b|, xl ≤ x ≤ xu,
//
// with A = sqrt(D)·Lᵀ and b = −sqrt(D)⁻¹·L⁻¹·g from the packed LDLᵀ
// factor lf; augment adds the inconsistency variable with weight
// augWeight. x and y (multipliers) are outputs; xl, xu (length n+1)
// receive the augmented bounds.
func lsq(n int, augment bool, augWeight float64, lf, gradx, xl, xu, x, y []float64) int {
	orign := n
	ld := n
	if augment {
		ld = n + 1
		x[n] = 1
		xl[n] = 0
		xu[n] = 1
	}
	wA := make([]float64, ld*ld)
	wb := make([]float64, ld)
	cursor := 0
	for j := 0; j < n; j++ {
		diag := math.Sqrt(lf[cursor])
		cursor++
		wA[j+j*ld] = diag
		for i := j + 1; i < n; i++ {
			wA[j+i*ld] = lf[cursor] * diag
			cursor++
		}
	}
	copy(wb, gradx[:n])
	dtpsvLNU(n, lf, wb)
	cursor = 0
	for i := 0; i < n; i++ {
		wb[i] /= -math.Sqrt(lf[cursor])
		cursor += n - i
	}
	if augment {
		wA[ld*ld-1] = augWeight
		n++
	}
	// Bounds as G·x ≥ h: rows +I for finite xl, −I for finite xu.
	var wh []float64
	nan := 0
	for i := 0; i < n; i++ {
		if math.IsNaN(xl[i]) {
			nan++
		} else {
			wh = append(wh, xl[i])
		}
	}
	for i := 0; i < n; i++ {
		if math.IsNaN(xu[i]) {
			nan++
		} else {
			wh = append(wh, -xu[i])
		}
	}
	rows := 2*n - nan
	wG := make([]float64, rows*n)
	nrow := 0
	for i := 0; i < n; i++ {
		if !math.IsNaN(xl[i]) {
			wG[nrow+i*rows] = 1
			nrow++
		}
	}
	for i := 0; i < n; i++ {
		if !math.IsNaN(xu[i]) {
			wG[nrow+i*rows] = -1
			nrow++
		}
	}
	_ = orign
	mults, mode := lsei(ld, rows, n, wA, wb, wG, wh, x)
	if mode == 1 {
		_ = mults // general-constraint multipliers; none without constraints
		for i := 0; i < 2*n; i++ {
			y[i] = math.NaN()
		}
	}
	for i := 0; i < n; i++ {
		if !math.IsNaN(xl[i]) && x[i] < xl[i] {
			x[i] = xl[i]
		} else if !math.IsNaN(xu[i]) && x[i] > xu[i] {
			x[i] = xu[i]
		}
	}
	return mode
}

// lsei solves min |A·x − b| s.t. G·x ≥ h (no equality rows): the LSEI
// routine with me = 0, which reduces to LSI.
func lsei(ma, mg, n int, a, b, g, h, x []float64) ([]float64, int) {
	for i := 0; i < n; i++ {
		x[i] = 0
	}
	wb := append([]float64(nil), b[:ma]...)
	a2 := append([]float64(nil), a[:ma*n]...)
	g2 := append([]float64(nil), g[:mg*n]...)
	if mg == 0 {
		// Unconstrained least squares (SciPy calls dgelsy here; A is the
		// square triangular factor, so a QR solve gives the same x).
		tau := make([]float64, min(ma, n))
		dgeqr2(ma, n, a2, ma, tau)
		dorm2rLT(ma, 1, min(ma, n), a2, ma, tau, wb, ma)
		for i := 0; i < n; i++ {
			if !(math.Abs(a2[i+i*ma]) >= math.Sqrt(2.220446049250313e-16)*math.Abs(a2[0])) {
				return nil, slsqpRankHFTI
			}
		}
		copy(x, wb[:n])
		dtrsvUNN(n, a2, ma, x)
		return nil, 1
	}
	return lsi(ma, mg, n, a2, wb, g2, h, x)
}

// lsi solves min |A·x − b| s.t. G·x ≥ h via QR of A and an LDP problem.
func lsi(ma, mg, n int, a, b, g, h, x []float64) ([]float64, int) {
	const epsmach = 2.220446049250313e-16
	k := min(ma, n)
	tau := make([]float64, k)
	dgeqr2(ma, n, a, ma, tau)
	dorm2rLT(ma, 1, k, a, ma, tau, b, ma)
	for i := 0; i < k; i++ {
		if !(math.Abs(a[i+i*ma]) >= epsmach) {
			return nil, slsqpSingularE
		}
	}
	dtrsmRUNN(mg, n, a, ma, g, mg)
	dgemvN(mg, n, -1, g, mg, b, 1, h)
	mults, mode := ldp(mg, n, g, h, x)
	if mode != 1 {
		return mults, mode
	}
	daxpy(n, 1, b, x)
	dtrsvUNN(n, a, ma, x)
	return mults, mode
}

// ldp solves the least distance problem min ½|x|² s.t. G·x ≥ h through
// its NNLS dual.
func ldp(m, n int, g, h, x []float64) ([]float64, int) {
	if n <= 0 {
		return nil, 2
	}
	for i := 0; i < n; i++ {
		x[i] = 0
	}
	if m == 0 {
		return nil, 1
	}
	a := make([]float64, (n+1)*m)
	for j := 0; j < m; j++ {
		for i := 0; i < n; i++ {
			a[i+j*(n+1)] = g[j+i*m]
		}
		a[n+j*(n+1)] = h[j]
	}
	b := make([]float64, n+1)
	b[n] = 1
	y := make([]float64, m)
	rnorm, mode := nnls(n+1, m, a, b, y, 3*m)
	if mode != 1 {
		return nil, mode
	}
	if rnorm <= 0 {
		return nil, slsqpIncompat
	}
	fac := 1 - ddot(m, h, y)
	if !((1+fac)-1 > 0) {
		return nil, slsqpIncompat
	}
	fac = 1 / fac
	dgemvT(m, n, fac, g, m, y, 0, x)
	mults := make([]float64, m)
	for i := range mults {
		mults[i] = fac * y[i]
	}
	return mults, 1
}

// nnls solves min |A·x − b|, x ≥ 0 (Lawson–Hanson), A m × n
// column-major, overwriting a and b. It returns the residual norm and a
// mode (1 success, 3 iteration limit).
func nnls(m, n int, a, b, x []float64, maxiter int) (float64, int) {
	if m <= 0 || n <= 0 {
		return 0, 2
	}
	info := 1
	w := make([]float64, n)
	zz := make([]float64, m)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	for i := 0; i < n; i++ {
		x[i] = 0
	}
	indz, iteration, izmax, jj := 0, 0, 0, 0
	var tau, pivot float64
outer:
	for indz < min(m, n) {
		for i := indz; i < n; i++ {
			j := idx[i]
			w[j] = ddot(m-indz, a[indz+j*m:], b[indz:])
		}
		var j, iz int
		for {
			wmax := 0.0
			for k := indz; k < n; k++ {
				jk := idx[k]
				if w[jk] > wmax {
					wmax = w[jk]
					izmax = k
				}
			}
			if wmax <= 0 {
				break outer
			}
			iz = izmax
			j = idx[iz]
			pivot = a[indz+j*m]
			tau = dlarfgp(m-indz, &pivot, a[indz+1+j*m:])
			unorm := 0.0
			if indz > 0 {
				unorm = dnrm2(indz, a[j*m:])
			}
			spacing := 0.0
			if unorm > 0 {
				spacing = math.Nextafter(unorm, 2*unorm) - unorm
			}
			if math.Abs(pivot) > 100*spacing {
				copy(zz, b[:m])
				pivot2 := a[indz+j*m]
				a[indz+j*m] = 1
				dlarfLeft(m-indz, 1, a[indz+j*m:], tau, zz[indz:], m-indz)
				ztest := zz[indz] / pivot
				if ztest > 0 {
					break
				}
				a[indz+j*m] = pivot2
			}
			w[j] = 0
		}
		copy(b, zz[:m])
		idx[iz] = idx[indz]
		idx[indz] = j
		indz++
		if indz < n {
			for k := indz; k < n; k++ {
				jj = idx[k]
				dlarfLeft(m-indz+1, 1, a[indz-1+j*m:], tau, a[indz-1+jj*m:], m-indz+1)
			}
		}
		a[indz-1+j*m] = pivot
		for i := indz; i < m; i++ {
			a[j*m+i] = 0
		}
		w[j] = 0
		for k := 0; k < indz; k++ {
			ip := indz - 1 - k
			if k != 0 {
				for i := 0; i <= ip; i++ {
					zz[i] -= a[i+jj*m] * zz[ip+1]
				}
			}
			jj = idx[ip]
			zz[ip] /= a[ip+jj*m]
		}
		for {
			iteration++
			if iteration >= maxiter {
				info = 3
				break outer
			}
			alpha := 2.0
			for ip := 0; ip < indz; ip++ {
				k := idx[ip]
				if zz[ip] <= 0 {
					t := -x[k] / (zz[ip] - x[k])
					if alpha > t {
						alpha = t
						jj = ip
					}
				}
			}
			if alpha == 2.0 {
				break
			}
			for ip := 0; ip < indz; ip++ {
				k := idx[ip]
				x[k] += alpha * (zz[ip] - x[k])
			}
			i := idx[jj]
			for {
				x[i] = 0
				if jj != indz-1 {
					jj++
					for jn := jj; jn < indz; jn++ {
						ii := idx[jn]
						idx[jn-1] = ii
						cc, ss, r := dlartgp(a[jn-1+ii*m], a[jn+ii*m])
						a[jn-1+ii*m] = r
						a[jn+ii*m] = 0
						for k := 0; k < n; k++ {
							if k != ii {
								t := a[jn-1+k*m]
								a[jn-1+k*m] = cc*t + ss*a[jn+k*m]
								a[jn+k*m] = -ss*t + cc*a[jn+k*m]
							}
						}
						t := b[jn-1]
						b[jn-1] = cc*t + ss*b[jn]
						b[jn] = -ss*t + cc*b[jn]
					}
				}
				indz--
				idx[indz] = i
				nobreak := false
				for jj = 0; jj < indz; jj++ {
					i = idx[jj]
					if x[i] <= 0 {
						break
					}
					if jj == indz-1 {
						nobreak = true
					}
				}
				if nobreak {
					break
				}
			}
			copy(zz, b[:m])
			for k := 0; k < indz; k++ {
				ip := indz - 1 - k
				if k != 0 {
					for i := 0; i <= ip; i++ {
						zz[i] -= a[i+jj*m] * zz[ip+1]
					}
				}
				jj = idx[ip]
				zz[ip] /= a[ip+jj*m]
			}
		}
		for k := 0; k < indz; k++ {
			x[idx[k]] = zz[k]
		}
	}
	if indz < m {
		return dnrm2(m-indz, b[indz:]), info
	}
	return 0, info
}

// ldlUpdate updates the packed LDLᵀ factors a by sigma·z·zᵀ (Fletcher
// and Powell's composite-t method); z and w are overwritten.
func ldlUpdate(n int, a, z []float64, sigma float64, w []float64) {
	const epsmach = 2.220446049250313e-16
	if sigma == 0 {
		return
	}
	ij := 0
	t := 1 / sigma
	if sigma <= 0 {
		copy(w[:n], z[:n])
		for i := 0; i < n; i++ {
			v := w[i]
			t += v * v / a[ij]
			for j := i + 1; j < n; j++ {
				ij++
				w[j] -= v * a[ij]
			}
			ij++
		}
		if t >= 0 {
			t = epsmach / sigma
		}
		for i := 0; i < n; i++ {
			j := n - i - 1
			ij -= i + 1
			u := w[j]
			w[j] = t
			t -= u * u / a[ij]
		}
	}
	for i := 0; i < n; i++ {
		v := z[i]
		delta := v / a[ij]
		var tp float64
		if sigma < 0 {
			tp = w[i]
		} else {
			tp = t + delta*v
		}
		alpha := tp / t
		a[ij] = alpha * a[ij]
		if i == n-1 {
			return
		}
		beta := delta / tp
		if alpha <= 4 {
			for j := i + 1; j < n; j++ {
				ij++
				z[j] -= v * a[ij]
				a[ij] += beta * z[j]
			}
		} else {
			gamma := t / tp
			for j := i + 1; j < n; j++ {
				ij++
				u := a[ij]
				a[ij] = gamma*u + beta*z[j]
				z[j] -= v * u
			}
		}
		ij++
		t = tp
	}
}
