package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// matrix_factor_ml.go is MAT_FACTOR's maximum-likelihood extraction
// (params.extraction "ml"): Jöreskog's fit over the uniquenesses ψ,
// the objective stats::factanal and psych::fa(fm = "ml") minimise,
//
//	F(ψ) = Σ_{m>k} (e_m − 1 − log e_m),
//
// e_m the eigenvalues of S* = Ψ^{-1/2}·R·Ψ^{-1/2} (descending), over
// the box ψ ∈ [faMLLower, faMLUpper] = [0.005, 1] (psych's L-BFGS-B
// bounds: factanal's lower 0.005; the upper max(SMC, 1) = 1 on a
// correlation). The start is psych's: ψ = 1 − SMC (minresStart),
// clipped into the box. The loadings are factanal's / psych's
// L = Ψ^{1/2}·U_k·√max(E_k − 1, 0), each column signed to sum positive
// and the columns ordered by descending variance accounted (psych::fa's
// sign-and-order rule; ML's eigen order need not be the variance
// order).
//
// The solver is a projected Newton iteration in θ = log ψ with the
// exact Hessian, where psych and factanal run L-BFGS-B at its default
// factr and stop early (their ψ sit up to ~2e-5 away from the optimum),
// so Pulse matches the optimum itself:
//
//   - gradient g_i = ∂F/∂θ_i = −Σ_{m>k} (e_m − 1)·u_mi²;
//   - Hessian H_ij = ½·Σ_{m,n} c_mn·(e_m + e_n)·u_mi·u_ni·u_mj·u_nj, the
//     Daleckii–Krein derivative of the spectral function
//     Σ_{m>k} (e_m − 1)·u_m·u_mᵀ, with c_mn = (h_m − h_n)/(e_m − e_n),
//     h_m = e_m − 1 beyond the k leading pairs and 0 within (c = 1
//     for two trailing pairs, 0 for two leading ones, a numerically
//     tied leading/trailing pair skipped);
//   - a ψ at a bound whose gradient points out of the box is held there
//     (the active set); the free ψ take the Newton step H_FF·δ = −g_F
//     (steepest descent when H_FF is singular or the step is not a
//     descent direction), projected into the box, halved until the
//     Armijo condition holds (or, at the rounding floor, F holds level
//     while the projected gradient falls);
//   - converged when the projected gradient's largest entry is at most
//     faMLTol, within faMLMaxIter steps.
//
// A ψ left at the 0.005 floor is the maximum-likelihood Heywood case
// (factanal's and psych's too): faFit.Bounded flags it and the
// finalizer warns PULSE_MATRIX_HEYWOOD.

// Maximum-likelihood solver settings — documented constants, never
// request knobs.
const (
	faMLLower    = 0.005
	faMLUpper    = 1.0
	faMLMaxIter  = 200
	faMLTol      = 1e-10
	faMLHalvings = 40
	faMLArmijo   = 1e-4
)

// faMLPoint is the objective, gradient (in θ = log ψ) and eigen-system
// of S* at one ψ.
type faMLPoint struct {
	psi    []float64
	f      float64
	g      []float64
	values []float64   // e, descending
	vecs   [][]float64 // vecs[m][i] = component i of eigenvector m
}

// faMLEval evaluates F, its θ-gradient and the eigen-system at psi. A
// non-positive trailing eigenvalue (a singular R) gives F = +Inf.
func faMLEval(r *linalg.Sym, psi []float64, k int) (faMLPoint, error) {
	p := r.N()
	s, _ := linalg.NewSym(p, nil)
	sq := make([]float64, p)
	for i := range sq {
		sq[i] = math.Sqrt(psi[i])
	}
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			s.Set(i, j, r.At(i, j)/float64(sq[i]*sq[j]))
		}
	}
	eig, err := linalg.SymEigen(s)
	if err != nil {
		return faMLPoint{}, err
	}
	pt := faMLPoint{psi: psi, values: eig.Values.Slice(), vecs: make([][]float64, p), g: make([]float64, p)}
	for m := 0; m < p; m++ {
		pt.vecs[m] = make([]float64, p)
		for i := 0; i < p; i++ {
			pt.vecs[m][i] = eig.Vectors.At(i, m)
		}
	}
	for m := k; m < p; m++ {
		e := pt.values[m]
		if !(e > 0) {
			pt.f = math.Inf(1)
			continue
		}
		pt.f += (e - 1) - detmath.Log1p(e-1)
		for i := 0; i < p; i++ {
			u := pt.vecs[m][i]
			pt.g[i] -= float64((e - 1) * float64(u*u))
		}
	}
	return pt, nil
}

// faMLHessian is the exact θ-Hessian of F at pt (see the file comment).
func faMLHessian(pt faMLPoint, k int) [][]float64 {
	p := len(pt.psi)
	h := make([][]float64, p)
	for i := range h {
		h[i] = make([]float64, p)
	}
	hm := func(m int) float64 {
		if m < k {
			return 0
		}
		return pt.values[m] - 1
	}
	w := make([]float64, p)
	for m := 0; m < p; m++ {
		for n := m; n < p; n++ {
			var c float64
			switch {
			case m < k && n < k:
				continue
			case m >= k && n >= k:
				c = 1
			default:
				gap := pt.values[m] - pt.values[n]
				if !(math.Abs(gap) > 1e-12*math.Max(1, math.Abs(pt.values[m]))) {
					continue
				}
				c = (hm(m) - hm(n)) / gap
			}
			a := float64(0.5 * float64(c*(pt.values[m]+pt.values[n])))
			if m != n {
				a = 2 * a
			}
			for i := 0; i < p; i++ {
				w[i] = float64(pt.vecs[m][i] * pt.vecs[n][i])
			}
			for i := 0; i < p; i++ {
				if w[i] == 0 {
					continue
				}
				ai := float64(a * w[i])
				for j := i; j < p; j++ {
					h[i][j] += float64(ai * w[j])
				}
			}
		}
	}
	for i := 0; i < p; i++ {
		for j := 0; j < i; j++ {
			h[i][j] = h[j][i]
		}
	}
	return h
}

// faMLActive reports whether ψ_i is held at a bound (its gradient
// points out of the box).
func faMLActive(psi, g float64) bool {
	return (psi <= faMLLower && g > 0) || (psi >= faMLUpper && g < 0)
}

// faMLProjGrad is the largest projected-gradient entry.
func faMLProjGrad(pt faMLPoint) float64 {
	out := 0.0
	for i, g := range pt.g {
		if faMLActive(pt.psi[i], g) {
			continue
		}
		if a := math.Abs(g); a > out || math.IsNaN(g) {
			out = a
		}
	}
	return out
}

// faML is the maximum-likelihood fit (see the file comment).
func faML(r *linalg.Sym, k int) (faFit, error) {
	return faMLSolve(r, k, faMLMaxIter)
}

func faMLSolve(r *linalg.Sym, k, maxIter int) (faFit, error) {
	p := r.N()
	h, _ := minresStart(r)
	psi := make([]float64, p)
	for i := range psi {
		psi[i] = math.Min(faMLUpper, math.Max(faMLLower, 1-h[i]))
	}
	fit := faFit{Solver: "fa_ml", MaxIterations: maxIter, Tolerance: faMLTol}
	pt, err := faMLEval(r, psi, k)
	if err != nil {
		return faFit{}, err
	}
	for {
		pg := faMLProjGrad(pt)
		if pg <= faMLTol {
			fit.Converged = true
			break
		}
		if fit.Iterations >= maxIter {
			break
		}
		next, ok, err := faMLStep(r, k, pt, pg)
		if err != nil {
			return faFit{}, err
		}
		fit.Iterations++
		if !ok {
			// No step lowers F or the projected gradient: the
			// iterate is at its rounding floor short of faMLTol.
			break
		}
		pt = next
	}
	return faMLFinish(pt, k, fit), nil
}

// faMLStep is one projected Newton step from pt; ok is false when no
// step size improves on pt.
func faMLStep(r *linalg.Sym, k int, pt faMLPoint, pg float64) (faMLPoint, bool, error) {
	p := len(pt.psi)
	var free []int
	for i := 0; i < p; i++ {
		if !faMLActive(pt.psi[i], pt.g[i]) {
			free = append(free, i)
		}
	}
	hess := faMLHessian(pt, k)
	nf := len(free)
	sub := make([][]float64, nf)
	rhs := make([]float64, nf)
	for a, i := range free {
		sub[a] = make([]float64, nf)
		for b, j := range free {
			sub[a][b] = hess[i][j]
		}
		rhs[a] = -pt.g[i]
	}
	delta, ok := gaussSolve(sub, rhs)
	if ok {
		slope := 0.0
		for a := range delta {
			slope += float64(delta[a] * -rhs[a])
		}
		ok = slope < 0
	}
	if !ok {
		delta = rhs
	}
	t := 1.0
	for range faMLHalvings + 1 {
		trial := append([]float64(nil), pt.psi...)
		for a, i := range free {
			trial[i] = math.Min(faMLUpper, math.Max(faMLLower, float64(pt.psi[i]*detmath.Exp(float64(t*delta[a])))))
		}
		next, err := faMLEval(r, trial, k)
		if err != nil {
			return faMLPoint{}, false, err
		}
		decrease := 0.0
		for i := range trial {
			decrease += float64(pt.g[i] * (detmath.Log(trial[i]) - detmath.Log(pt.psi[i])))
		}
		if next.f <= pt.f+float64(faMLArmijo*decrease) {
			return next, true, nil
		}
		if math.Abs(next.f-pt.f) <= 1e-14*(1+math.Abs(pt.f)) && faMLProjGrad(next) < pg {
			return next, true, nil
		}
		t /= 2
	}
	return pt, false, nil
}

// faMLFinish turns the solver's last point into a faFit: factanal's
// loadings L = Ψ^{1/2}·U_k·√max(E_k − 1, 0), each column signed to sum
// positive, columns by descending variance accounted (stable).
func faMLFinish(pt faMLPoint, k int, fit faFit) faFit {
	p := len(pt.psi)
	cols := make([][]float64, k)
	variance := make([]float64, k)
	for c := 0; c < k; c++ {
		root := math.Sqrt(math.Max(pt.values[c]-1, 0))
		col := make([]float64, p)
		sum := 0.0
		for i := 0; i < p; i++ {
			col[i] = float64(math.Sqrt(pt.psi[i]) * float64(pt.vecs[c][i]*root))
			sum += col[i]
		}
		if sum < 0 {
			for i := range col {
				col[i] = -col[i]
			}
		}
		for _, v := range col {
			variance[c] += float64(v * v)
		}
		cols[c] = col
	}
	order := make([]int, k)
	for c := range order {
		order[c] = c
	}
	sort.SliceStable(order, func(a, b int) bool { return variance[order[a]] > variance[order[b]] })
	fit.Loadings = make([][]float64, p)
	for i := 0; i < p; i++ {
		fit.Loadings[i] = make([]float64, k)
		for c, src := range order {
			fit.Loadings[i][c] = cols[src][i]
		}
	}
	fit.Communalities = rowSquares(fit.Loadings)
	fit.Psi = append([]float64(nil), pt.psi...)
	fit.Bounded = make([]bool, p)
	for i, v := range pt.psi {
		fit.Bounded[i] = v <= faMLLower
	}
	return fit
}

// faMLFit is the likelihood fit of a k-factor solution with loadings l
// (psych::fa.stats): the objective F_m = tr(M⁻¹R) − log det(M⁻¹R) − p
// over the model M = LLᵀ with R's own diagonal; the Bartlett-corrected
// chi-square F_m·n_m, n_m = (N* − 1) − (2p + 5)/6 − 2k/3 (0 when
// negative), on df; its upper tail; the Tucker-Lewis index
// (M_0 − M_m)/(M_0 − 1/n_m) with M_0 = F_0·2/(p(p − 1)), F_0 = tr(R) −
// log det R − p the null model's objective and M_m = F_m/df; the RMSEA
// point estimate √max(χ²/(df·N*) − 1/(N* − 1), 0); and BIC χ² −
// df·log N*; the RMSEA interval at coverage conf (faRMSEAInterval).
// p, tli, rmsea, its interval and bic are null at df ≤ 0; every figure
// is null when n_m ≤ 0 (insufficient), N* is not finite, or R or M is
// not positive definite (defined false).
type faMLFitStats struct {
	ChiSq, P, TLI, RMSEA, BIC float64
	RMSEALower, RMSEAUpper    float64
	Insufficient              bool
	MinNStar                  float64
}

func faMLFitIndices(r *linalg.Sym, l [][]float64, nStar, conf float64) faMLFitStats {
	p := r.N()
	k := 0
	if p > 0 {
		k = len(l[0])
	}
	nan := math.NaN()
	st := faMLFitStats{ChiSq: nan, P: nan, TLI: nan, RMSEA: nan, BIC: nan, RMSEALower: nan, RMSEAUpper: nan,
		MinNStar: float64(2*p+4*k+11) / 6} // 1 + (2p + 5)/6 + 2k/3
	nm := nStar - st.MinNStar
	if math.IsNaN(nStar) || math.IsInf(nStar, 0) {
		return st
	}
	if !(nm > 0) {
		st.Insufficient = true
		return st
	}
	model, _ := linalg.NewSym(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			v := r.At(i, i)
			if j != i {
				v = 0
				for c := 0; c < k; c++ {
					v += float64(l[i][c] * l[j][c])
				}
			}
			model.Set(i, j, v)
		}
	}
	logDetR, okR := faLogDet(r)
	logDetM, okM := faLogDet(model)
	inv, err := linalg.InverseSPD(model)
	if !okR || !okM || err != nil {
		return st
	}
	tr, trR := 0.0, 0.0
	for i := 0; i < p; i++ {
		trR += r.At(i, i)
		for j := 0; j < p; j++ {
			tr += float64(inv.At(i, j) * r.At(j, i))
		}
	}
	fm := tr - (logDetR - logDetM) - float64(p)
	f0 := trR - logDetR - float64(p)
	df := float64(p*(p-1)/2 - p*k + k*(k-1)/2)
	st.ChiSq = math.Max(float64(fm*nm), 0)
	if df > 0 {
		st.P = chiSquareSurvival(st.ChiSq, df)
		m0 := f0 * 2 / float64(p*(p-1))
		st.TLI = (m0 - fm/df) / (m0 - 1/nm)
		st.RMSEA = math.Sqrt(math.Max(st.ChiSq/float64(df*nStar)-1/(nStar-1), 0))
		st.BIC = st.ChiSq - float64(df*detmath.Log(nStar))
		st.RMSEALower, st.RMSEAUpper = faRMSEAInterval(st.ChiSq, df, nStar, st.RMSEA, conf)
	}
	return st
}

// faRMSEAInterval is the RMSEA confidence interval at coverage conf
// (psych::fa's RMSEA lower / upper, psych::fa.stats; psych::fa's alpha
// = 0.1 is conf 0.9): with tail = (1 − conf)/2 and F(λ) the noncentral
// χ²(df, λ) CDF at the statistic chi (decreasing in λ), the bounds
// invert F for the noncentrality and rescale it by (N* − 1)·df —
// psych's denominator, where the point estimate rmsea reads df·N*:
//
//   - upper = √(λ_U/((N* − 1)·df)), F(λ_U) = tail, when F(0) > tail;
//     else 0;
//   - lower = √(λ_L/((N* − 1)·df)), F(λ_L) = 1 − tail, when F(0) >
//     1 − tail; else 0.
//
// psych searches λ on [0, max(N*, chi) + 2·N*] (stats::uniroot); a root
// beyond it is null (an upper bound whose point estimate is 0 is 0
// instead), and a bound on the wrong side of the point estimate is
// null. Pulse solves each root by bisection to the last bit, where
// psych's uniroot stops at its default tolerance (about 1.2e-4 on λ, so
// psych's bounds sit up to ~1e-7 from the exact ones).
func faRMSEAInterval(chi, df, nStar, rmsea, conf float64) (lower, upper float64) {
	nan := math.NaN()
	if !(df > 0) || !(nStar > 1) || math.IsNaN(chi) || math.IsNaN(rmsea) || !(conf > 0 && conf < 1) {
		return nan, nan
	}
	tail := float64((1 - conf) / 2)
	hi := math.Max(nStar, chi) + 2*nStar
	f0 := noncentralChiSquareCDF(chi, df, 0)
	scale := func(lambda float64) float64 {
		return math.Sqrt(lambda / (nStar - 1) / df)
	}
	upper = 0
	if f0 > tail {
		if lambda, ok := ncChiSquareRoot(chi, df, tail, hi); ok {
			upper = scale(lambda)
		} else if rmsea > 0 {
			upper = nan
		}
	}
	lower = 0
	if f0 > 1-tail {
		if lambda, ok := ncChiSquareRoot(chi, df, 1-tail, hi); ok {
			lower = scale(lambda)
		} else {
			lower = nan
		}
	}
	if upper < rmsea {
		upper = nan
	}
	if lower > rmsea {
		lower = nan
	}
	return lower, upper
}

// ncChiSquareRoot is the noncentrality λ ∈ [0, hi] at which the
// noncentral χ²(df, λ) CDF at chi equals target, by bisection to
// adjacent doubles; ok is false when the CDF at hi is still above
// target (no root in the bracket, psych's uniroot error). The caller
// guarantees the CDF at 0 is above target.
func ncChiSquareRoot(chi, df, target, hi float64) (float64, bool) {
	f := func(lambda float64) float64 { return noncentralChiSquareCDF(chi, df, lambda) - target }
	fhi := f(hi)
	if fhi > 0 {
		return 0, false
	}
	if fhi == 0 {
		return hi, true
	}
	lo := 0.0
	for range 2000 {
		mid := lo + float64((hi-lo)/2)
		if mid <= lo || mid >= hi {
			break
		}
		if v := f(mid); v > 0 {
			lo = mid
		} else if v < 0 {
			hi = mid
		} else {
			return mid, true
		}
	}
	return lo + float64((hi-lo)/2), true
}

// faLogDet is log det s from the reference Cholesky (2·Σ log L_ii);
// ok is false when s is not positive definite.
func faLogDet(s *linalg.Sym) (float64, bool) {
	l, err := linalg.Cholesky(s)
	if err != nil {
		return math.NaN(), false
	}
	out := 0.0
	for i := 0; i < s.N(); i++ {
		out += float64(2 * detmath.Log(l.At(i, i)))
	}
	return out, true
}
