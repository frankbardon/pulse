package optimize

// A Go port of SciPy 1.18.1's SLSQP (scipy/optimize/src/slsqp.c,
// nnls.c and _slsqp_py.py; BSD-3-Clause, Copyright (c) 2001-2002
// Enthought, Inc. 2003, SciPy Developers — see SCIPY_LICENSE.txt; the
// algorithm is Dieter Kraft's): simple bounds on x plus m general
// constraints, the first meq of them equalities c(x) = 0 and the rest
// inequalities c(x) ≥ 0. The iteration — QP subproblem (LSQ → LSEI →
// LSI → LDP → NNLS), the L1 merit line search with its penalty weights,
// the BFGS update of the LDLᵀ factors, the reset and relaxed stopping
// rules — follows the C source statement by statement. With m = 0 every
// constraint term is an empty loop, so the bounds-only path is the
// arithmetic the structural-equation fit has always run.

import "math"

// SLSQP exit modes (SciPy's).
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

// problem is the solver's view of a minimisation: the objective, its
// gradient and the m constraints (equalities first) with their normals.
// grad fills g (n); cons fills d (m); jac fills the m × n column-major
// normal matrix c. cons and jac are never called when m = 0.
type problem struct {
	n, m, meq int
	f         func(x []float64) float64
	grad      func(x, g []float64)
	cons      func(x, d []float64)
	jac       func(x, c []float64)
}

type slsqpResult struct {
	x    []float64
	fun  float64
	mult []float64 // the m constraint multipliers of the last QP
	mode int
	nit  int
	nfev int
	njev int
}

// slsqp minimises p.f subject to xl ≤ x ≤ xu (NaN marks an absent
// bound) and p's constraints, as scipy.optimize.minimize(method=
// "SLSQP", jac=grad, bounds=..., constraints=..., options={"ftol": acc,
// "maxiter": itermax}).
func slsqp(p problem, x0, xl, xu []float64, acc float64, itermax int) slsqpResult {
	n, m, meq := p.n, p.m, p.meq
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
	fx := p.f(x[:n])
	res.nfev++
	g := make([]float64, n)
	p.grad(x[:n], g)
	res.njev++
	d := make([]float64, m)
	cm := make([]float64, m*n)
	if m > 0 {
		p.cons(x[:n], d)
		p.jac(x[:n], cm)
	}
	lda := max(m, 1)

	n1 := n + 1
	bfgs := make([]float64, n1*n/2)
	x0s := make([]float64, n)
	mu := make([]float64, m)
	s := make([]float64, n1)
	u := make([]float64, n1)
	v := make([]float64, n1)
	mult := make([]float64, m+2*n+2)

	acc = math.Abs(acc)
	tol := 10 * acc
	iter, reset := 0, 0
	var alpha, f0, gs, h1, h2, h3, h4, t0 float64
	var mode int
	badlin := false

	// violation is the L1 constraint violation Σ max(−d_j, d_j or 0).
	violation := func() float64 {
		t := 0.0
		for j := 0; j < m; j++ {
			e := 0.0
			if j < meq {
				e = d[j]
			}
			t += math.Max(-d[j], e)
		}
		return t
	}

	finish := func(md int) slsqpResult {
		res.x = append([]float64(nil), x[:n]...)
		res.mult = append([]float64(nil), mult[:m]...)
		res.fun, res.mode, res.nit = fx, md, iter
		return res
	}

resetBFGS:
	reset++
	if reset > 5 {
		// LABEL255: relaxed convergence after repeated resets.
		h3 = violation()
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
	mode = lsq(m, meq, n, false, 0, bfgs, g, cm, d, u, v, s, mult)
	badlin = false
	if mode == slsqpSingularC && n == meq {
		mode = slsqpIncompat
	}
	if mode == slsqpIncompat {
		badlin = true
		for i := 0; i < n; i++ {
			s[i] = 0
		}
		h3 = 0
		rho := 100.0
		inconsistent := 0
		for {
			mode = lsq(m, meq, n, true, rho, bfgs, g, cm, d, u, v, s, mult)
			h4 = 1 - s[n]
			if mode == slsqpIncompat {
				rho = float64(rho * 10)
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
	// Multipliers for the L1 test: v = g − Cᵀ·mult.
	copy(v, g)
	dgemvTAcc(m, n, -1, cm, lda, mult, v)
	f0 = fx
	copy(x0s, x[:n])
	gs = ddot(n, g, s)
	h1 = math.Abs(gs)
	h2 = 0
	for j := 0; j < m; j++ {
		if j < meq {
			h3 = d[j]
		} else {
			h3 = 0
		}
		h2 += math.Max(-d[j], h3)
		h3 = math.Abs(mult[j])
		mu[j] = math.Max(h3, (mu[j]+h3)/2)
		h1 += float64(h3 * math.Abs(d[j]))
	}
	if h1 < acc && h2 < acc && !badlin && fx == fx {
		return finish(slsqpSuccess)
	}
	h1 = 0
	for j := 0; j < m; j++ {
		if j < meq {
			h3 = d[j]
		} else {
			h3 = 0
		}
		h1 += float64(mu[j] * math.Max(-d[j], h3))
	}
	t0 = fx + h1
	h3 = gs - float64(h1*h4)
	if h3 >= 0 {
		goto resetBFGS
	}
	alpha = 1
	for line := 1; ; line++ {
		h3 = float64(alpha * h3)
		dscal(n, alpha, s)
		copy(x[:n], x0s)
		daxpy(n, 1, s, x)
		fx = p.f(x[:n])
		res.nfev++
		if m > 0 {
			p.cons(x[:n], d)
		}
		t := fx
		for j := 0; j < m; j++ {
			e := 0.0
			if j < meq {
				e = d[j]
			}
			t += float64(mu[j] * math.Max(-d[j], e))
		}
		h1 = t - t0
		if !(h1 <= h3/10 || line > 10) {
			alpha = math.Max(h3/(2*(h3-h1)), 0.1)
			continue
		}
		break
	}
	h3 = violation()
	if (math.Abs(fx-f0) < acc || dnrm2(n, s) < acc) && h3 < acc && !badlin && fx == fx {
		return finish(slsqpSuccess)
	}
	p.grad(x[:n], g)
	res.njev++
	if m > 0 {
		p.jac(x[:n], cm)
	}
	// BFGS update of the LDLᵀ factors: u = g − Cᵀ·mult − v.
	copy(u[:n], g)
	dgemvTAcc(m, n, -1, cm, lda, mult, u)
	for i := 0; i < n; i++ {
		u[i] = u[i] - v[i]
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
	h3 = float64(0.2 * h2)
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

// lsq solves the QP subproblem
//
//	min |A·x − b|, E·x = f, G·x ≥ h, xl ≤ x ≤ xu,
//
// with A = sqrt(D)·Lᵀ and b = −sqrt(D)⁻¹·L⁻¹·g from the packed LDLᵀ
// factor lf, E the first meq rows of the m × n column-major constraint
// normals cm (f = −d) and G the remaining rows (h = −d) stacked over the
// bound rows; augment adds the inconsistency variable with weight
// augWeight. x and y (multipliers: m constraint rows, then 2·n NaN bound
// slots) are outputs; xl, xu (length n+1) receive the augmented bounds.
func lsq(m, meq, n int, augment bool, augWeight float64, lf, gradx, cm, d, xl, xu, x, y []float64) int {
	orign := n
	mineq := m - meq
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
	// Equality rows E (meq × n) and f = −d.
	var wE, wf []float64
	if meq > 0 {
		wE = make([]float64, meq*n)
		wf = make([]float64, meq)
		for j := 0; j < n-1; j++ {
			for i := 0; i < meq; i++ {
				wE[i+j*meq] = cm[i+j*m]
			}
		}
		for i := 0; i < meq; i++ {
			if augment {
				wE[i+(n-1)*meq] = -d[i]
			} else {
				wE[i+(n-1)*meq] = cm[i+(n-1)*m]
			}
			wf[i] = -d[i]
		}
	}
	// Inequality rows then the bounds as G·x ≥ h: rows +I for finite xl,
	// −I for finite xu.
	var wh []float64
	for i := 0; i < mineq; i++ {
		wh = append(wh, -d[meq+i])
	}
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
	rows := mineq + 2*n - nan
	wG := make([]float64, rows*n)
	for j := 0; j < orign; j++ {
		for i := 0; i < mineq; i++ {
			wG[i+j*rows] = cm[meq+i+j*m]
		}
	}
	if augment {
		for i := 0; i < mineq; i++ {
			wG[i+orign*rows] = math.Max(-d[meq+i], 0)
		}
	}
	nrow := mineq
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
	gmults, emults, mode := lsei(ld, meq, rows, n, wA, wb, wE, wf, wG, wh, x)
	if mode == 1 {
		// Multipliers: equalities, then inequalities; the bound slots are
		// NaN (SciPy reports no bound multipliers).
		for i := 0; i < meq; i++ {
			y[i] = emults[i]
		}
		for i := 0; i < mineq; i++ {
			y[meq+i] = gmults[i]
		}
		for i := 0; i < 2*n; i++ {
			y[m+i] = math.NaN()
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

// lsei solves min |A·x − b| s.t. E·x = f, G·x ≥ h, returning the
// inequality and equality multipliers and a mode. With me = 0 it is
// the bounds-only routine the structural-equation fit has always run
// (a QR solve, or LSI); with me > 0 it is SciPy's RQ reduction: E·Qᵀ =
// [0 | R] fixes the last me coordinates, the reduced problem over the
// first n − me is solved by QR (no G) or LSI, and the solution and
// multipliers are mapped back to the original basis.
func lsei(ma, me, mg, n int, a, b, e, f, g, h, x []float64) ([]float64, []float64, int) {
	if me == 0 {
		gm, mode := lseiNoEq(ma, mg, n, a, b, g, h, x)
		return gm, nil, mode
	}
	const epsmach = 2.220446049250313e-16
	for i := 0; i < n; i++ {
		x[i] = 0
	}
	if me > n {
		return nil, nil, 2
	}
	nvars := n - me
	ldg := max(mg, 1)
	tau := make([]float64, me)
	dgerq2(me, n, e, me, tau)
	dormr2RT(ma, n, me, e, me, tau, a, ma)
	dormr2RT(mg, n, me, e, me, tau, g, ldg)
	for i := 0; i < me; i++ {
		if !(math.Abs(e[i+(nvars+i)*me]) >= epsmach) {
			return nil, nil, slsqpSingularC
		}
	}
	copy(x[nvars:nvars+me], f[:me])
	dtrsvUNN(me, e[nvars*me:], me, x[nvars:])
	mode := 1
	gmults := make([]float64, mg)
	if me < n {
		wb := append([]float64(nil), b[:ma]...)
		dgemvN(ma, me, -1, a[ma*nvars:], ma, x[nvars:], 1, wb)
		a2 := append([]float64(nil), a[:ma*nvars]...)
		if mg == 0 {
			// SciPy calls dgelsy here; for a full-rank A2 a QR solve gives
			// the same least-squares x, and a rank deficiency is mode 7.
			k := min(ma, nvars)
			t := make([]float64, k)
			dgeqr2(ma, nvars, a2, ma, t)
			dorm2rLT(ma, 1, k, a2, ma, t, wb, ma)
			for i := 0; i < nvars; i++ {
				if !(math.Abs(a2[i+i*ma]) >= math.Sqrt(epsmach)*math.Abs(a2[0])) {
					return nil, nil, slsqpRankHFTI
				}
			}
			copy(x, wb[:nvars])
			dtrsvUNN(nvars, a2, ma, x)
		} else {
			g2 := append([]float64(nil), g[:mg*nvars]...)
			dgemvN(mg, me, -1, g[mg*nvars:], ldg, x[nvars:], 1, h)
			var gm []float64
			gm, mode = lsi(ma, mg, nvars, a2, wb, g2, h, x)
			copy(gmults, gm)
			if mode != 1 {
				return gmults, nil, mode
			}
		}
	}
	// Back to the original basis: b = A·x − b; f = A1ᵀ·b − G1ᵀ·gmults;
	// x = Qᵀ·x; Rᵀ·emults = f.
	dgemvN(ma, n, 1, a, ma, x, -1, b)
	dgemvT(ma, me, 1, a[nvars*ma:], ma, b, 0, f)
	dgemvTAcc(mg, me, -1, g[nvars*mg:], ldg, gmults, f)
	dormr2LT(n, 1, me, e, me, tau, x, n)
	emults := append([]float64(nil), f[:me]...)
	dtrsvUTN(me, e[(n-me)*me:], me, emults)
	return gmults, emults, mode
}

// lseiNoEq solves min |A·x − b| s.t. G·x ≥ h (no equality rows): the
// LSEI routine with me = 0, which reduces to LSI.
func lseiNoEq(ma, mg, n int, a, b, g, h, x []float64) ([]float64, int) {
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
					zz[i] -= float64(a[i+jj*m] * zz[ip+1])
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
				x[k] += float64(alpha * (zz[ip] - x[k]))
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
								a[jn-1+k*m] = float64(cc*t) + float64(ss*a[jn+k*m])
								a[jn+k*m] = float64(-ss*t) + float64(cc*a[jn+k*m])
							}
						}
						t := b[jn-1]
						b[jn-1] = float64(cc*t) + float64(ss*b[jn])
						b[jn] = float64(-ss*t) + float64(cc*b[jn])
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
						zz[i] -= float64(a[i+jj*m] * zz[ip+1])
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
				w[j] -= float64(v * a[ij])
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
			tp = t + float64(delta*v)
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
				z[j] -= float64(v * a[ij])
				a[ij] += float64(beta * z[j])
			}
		} else {
			gamma := t / tp
			for j := i + 1; j < n; j++ {
				ij++
				u := a[ij]
				a[ij] = float64(gamma*u) + float64(beta*z[j])
				z[j] -= float64(v * u)
			}
		}
		ij++
		t = tp
	}
}
