package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// TEST_ANOVA_RM params.incomplete "mixed" (U69 E6-S1, #678): the REML
// random-intercept model
//
//	y = Xβ + Zb + ε,  b ~ N(0, φ_s I_m),  ε ~ N(0, φ_e I_N)
//
// over every observed (subject, condition) cell, X the intercept plus
// one treatment dummy per non-reference condition (conditions sorted;
// the F is invariant to the coding), and the F test of the condition
// effect with Satterthwaite's denominator df — lmerTest's
// anova(lmer(y ~ cond + (1 | subject)), ddf = "Satterthwaite"),
// contestMD: L C Lᵀ = P D Pᵀ, F = Σ (P_mᵀ L β)² / D_m / q, ν_m = 2 D_m² /
// (g_mᵀ A g_m), g_mj = P_mᵀ L (∂C/∂φ_j) Lᵀ P_m, ddf = 2E / (E − q),
// E = Σ ν_m / (ν_m − 2) (ν_1 when q = 1 or the ν agree within 1e-8; 2
// when any ν ≤ 2).
//
// Everything is in closed form, never by numeric differentiation:
//
//   - V = φ_s ZZᵀ + φ_e I is block diagonal; a subject's block lives in
//     the commutative algebra span{I, J} of its n_i × n_i ones matrix J,
//     so V⁻¹, and every product of V⁻¹ with V_s = ZZᵀ and V_e = I, is an
//     (αI + βJ) pair (rmBlock). Each sum over subjects then needs only
//     per-subject-size sufficient statistics (rmGroup): ΣXᵢᵀXᵢ, Σsᵢsᵢᵀ
//     (sᵢ = Xᵢᵀ1), ΣXᵢᵀyᵢ, Σsᵢtᵢ (tᵢ = 1ᵀyᵢ), Σyᵢᵀyᵢ and Σtᵢ² — the cost
//     of one evaluation is O(k p²), independent of N.
//   - The REML optimum is the root in γ = φ_s / φ_e of the profiled
//     gradient (∂REML/∂φ_s at φ_e = yᵀP_H y / (N − p), P_H at (γ, 1)),
//     bracketed by doubling from γ = 1 and found by bisection; γ = 0
//     (the boundary) when the gradient is already ≥ 0 there.
//   - A = 2 H⁻¹, H the OBSERVED Hessian of the REML deviance in φ:
//     H_jk = −tr(P V_j P V_k) + 2 yᵀP V_j P V_k P y; at the boundary
//     only φ_e is free (lmerTest's (θ, σ) Hessian is block diagonal at
//     θ = 0), which reduces ddf to N − p.
//   - ∂C/∂φ_j = C Xᵀ V⁻¹ V_j V⁻¹ X C.
//
// Every product feeding a sum is barriered (float64(a*b)) and no
// transcendental is called, so the figures are FMA-free; the only
// decomposition is linalg.SymEigen of the q × q L C Lᵀ (q ≥ 2), whose
// eigen-directions feed ddf alone (F is their invariant sum).
// Oracle: scripts/reference/gen_test_anova_rm_mixed.R.

// rmBlock is αI + βJ in the algebra of an n × n subject block.
type rmBlock struct{ a, b float64 }

// mul is x·y in the n × n block algebra (J² = nJ).
func (x rmBlock) mul(y rmBlock, n float64) rmBlock {
	cross := float64(x.a*y.b) + float64(x.b*y.a)
	return rmBlock{a: float64(x.a * y.a), b: cross + float64(n*float64(x.b*y.b))}
}

// trace is tr(αI + βJ) = n(α + β).
func (x rmBlock) trace(n float64) float64 { return float64(n * (x.a + x.b)) }

// rmGroup holds the sufficient statistics of every subject observed in
// exactly n conditions.
type rmGroup struct {
	n      float64
	count  float64 // subjects
	xtx    []float64
	sst    []float64
	xty    []float64
	st     []float64
	yy, tt float64
}

// rmDesign is the mixed model's data: p = k fixed effects, N cells.
type rmDesign struct {
	p      int
	nObs   int
	groups []*rmGroup
}

// newRMDesign builds the sufficient statistics over the sorted subjects
// and conditions (a deterministic summation order).
func newRMDesign(cells map[string]map[string]float64, subjects, conditions []string) *rmDesign {
	p := len(conditions)
	bySize := map[int]*rmGroup{}
	nObs := 0
	x := make([]float64, p)
	for _, subj := range subjects {
		row := cells[subj]
		n := len(row)
		g := bySize[n]
		if g == nil {
			g = &rmGroup{n: float64(n), xtx: make([]float64, p*p), sst: make([]float64, p*p), xty: make([]float64, p), st: make([]float64, p)}
			bySize[n] = g
		}
		g.count++
		nObs += n
		s := make([]float64, p)
		var t float64
		for j, cond := range conditions {
			v, ok := row[cond]
			if !ok {
				continue
			}
			for i := range x {
				x[i] = 0
			}
			x[0] = 1
			if j > 0 {
				x[j] = 1
			}
			for a := 0; a < p; a++ {
				if x[a] == 0 {
					continue
				}
				s[a] += x[a]
				g.xty[a] += v
				for b := 0; b < p; b++ {
					g.xtx[a*p+b] += float64(x[a] * x[b])
				}
			}
			t += v
			g.yy += float64(v * v)
		}
		for a := 0; a < p; a++ {
			g.st[a] += float64(s[a] * t)
			for b := 0; b < p; b++ {
				g.sst[a*p+b] += float64(s[a] * s[b])
			}
		}
		g.tt += float64(t * t)
	}
	sizes := make([]int, 0, len(bySize))
	for n := range bySize {
		sizes = append(sizes, n)
	}
	sort.Ints(sizes)
	d := &rmDesign{p: p, nObs: nObs}
	for _, n := range sizes {
		d.groups = append(d.groups, bySize[n])
	}
	return d
}

// rmState is the model at one φ: C = (XᵀV⁻¹X)⁻¹, β and the per-group
// residual statistics (eI = Σeᵢᵀeᵢ, eJ = ΣEᵢ², xeI = ΣXᵢᵀeᵢ,
// xeJ = ΣsᵢEᵢ with eᵢ = yᵢ − Xᵢβ, Eᵢ = 1ᵀeᵢ).
type rmState struct {
	d          *rmDesign
	phiS, phiE float64
	c          []float64 // p × p
	beta       []float64
	q          []rmBlock // V⁻¹ per group
	eI, eJ     []float64
	xeI, xeJ   [][]float64
}

// addXtX adds Σ over group g of Xᵢᵀ(αI + βJ)Xᵢ into out.
func (d *rmDesign) addXtX(out []float64, g *rmGroup, x rmBlock) {
	for i := range out {
		out[i] += float64(x.a*g.xtx[i]) + float64(x.b*g.sst[i])
	}
}

func (d *rmDesign) state(phiS, phiE float64) (*rmState, error) {
	p := d.p
	st := &rmState{d: d, phiS: phiS, phiE: phiE}
	info := make([]float64, p*p)
	rhs := make([]float64, p)
	for _, g := range d.groups {
		c := phiS / (phiE + float64(g.n*phiS))
		q := rmBlock{a: 1 / phiE, b: -c / phiE}
		st.q = append(st.q, q)
		d.addXtX(info, g, q)
		for a := 0; a < p; a++ {
			rhs[a] += float64(q.a*g.xty[a]) + float64(q.b*g.st[a])
		}
	}
	packed := make([]float64, 0, p*(p+1)/2)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			packed = append(packed, info[i*p+j])
		}
	}
	sym, err := linalg.NewSym(p, packed)
	if err != nil {
		return nil, err
	}
	inv, err := linalg.InverseSPD(sym)
	if err != nil {
		return nil, err
	}
	st.c = make([]float64, p*p)
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			st.c[i*p+j] = inv.At(i, j)
		}
	}
	st.beta = rmMatVec(st.c, rhs, p)
	for _, g := range d.groups {
		ab := rmMatVec(g.xtx, st.beta, p)
		sb := rmMatVec(g.sst, st.beta, p)
		st.eI = append(st.eI, g.yy-2*rmDot(st.beta, g.xty)+rmDot(st.beta, ab))
		st.eJ = append(st.eJ, g.tt-2*rmDot(st.beta, g.st)+rmDot(st.beta, sb))
		xeI := make([]float64, p)
		xeJ := make([]float64, p)
		for a := 0; a < p; a++ {
			xeI[a] = g.xty[a] - ab[a]
			xeJ[a] = g.st[a] - sb[a]
		}
		st.xeI = append(st.xeI, xeI)
		st.xeJ = append(st.xeJ, xeJ)
	}
	return st, nil
}

func rmMatVec(m, v []float64, p int) []float64 {
	out := make([]float64, p)
	for i := 0; i < p; i++ {
		var s float64
		for j := 0; j < p; j++ {
			s += float64(m[i*p+j] * v[j])
		}
		out[i] = s
	}
	return out
}

func rmDot(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += float64(a[i] * b[i])
	}
	return s
}

// rmTraceProd is tr(A·B) of two p × p row-major matrices.
func rmTraceProd(a, b []float64, p int) float64 {
	var s float64
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			s += float64(a[i*p+j] * b[j*p+i])
		}
	}
	return s
}

func rmMatMul(a, b []float64, p int) []float64 {
	out := make([]float64, p*p)
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			var s float64
			for k := 0; k < p; k++ {
				s += float64(a[i*p+k] * b[k*p+j])
			}
			out[i*p+j] = s
		}
	}
	return out
}

// vBlock is V_s = J (j == 0) or V_e = I (j == 1).
func vBlock(j int) rmBlock {
	if j == 0 {
		return rmBlock{b: 1}
	}
	return rmBlock{a: 1}
}

// ete is Σ eᵢᵀ(αI + βJ)eᵢ over group gi.
func (st *rmState) ete(gi int, x rmBlock) float64 {
	return float64(x.a*st.eI[gi]) + float64(x.b*st.eJ[gi])
}

// addXte adds Σ Xᵢᵀ(αI + βJ)eᵢ over group gi into out.
func (st *rmState) addXte(out []float64, gi int, x rmBlock) {
	for a := range out {
		out[a] += float64(x.a*st.xeI[gi][a]) + float64(x.b*st.xeJ[gi][a])
	}
}

// gradS is ∂REML/∂φ_s = tr(P V_s) − yᵀP V_s P y.
func (st *rmState) gradS() float64 {
	p := st.d.p
	g := make([]float64, p*p)
	var tr, quad float64
	for gi, grp := range st.d.groups {
		q := st.q[gi]
		tr += float64(grp.count * q.mul(vBlock(0), grp.n).trace(grp.n))
		qvq := q.mul(vBlock(0), grp.n).mul(q, grp.n)
		st.d.addXtX(g, grp, qvq)
		quad += st.ete(gi, qvq)
	}
	return tr - rmTraceProd(st.c, g, p) - quad
}

// rmMixedFit is the fitted model and its F test.
type rmMixedFit struct {
	f, df1, ddf, p  float64
	phiS, phiE      float64
	conditionMeans  []float64
	subjects, nObs  int
	completeSubject int
}

func rmInsufficient(msg string, details map[string]any) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N, "TEST_ANOVA_RM (incomplete mixed): "+msg, details)
}

// fitRMMixed fits the REML model over cells (subjects and conditions
// sorted) and returns its Satterthwaite F test.
func fitRMMixed(cells map[string]map[string]float64, subjects, conditions []string) (*rmMixedFit, error) {
	d := newRMDesign(cells, subjects, conditions)
	m, k := len(subjects), len(conditions)
	q := k - 1
	resDF := d.nObs - d.p
	if m < 2 || d.nObs-m-q < 1 {
		return nil, rmInsufficient("needs ≥ 2 subjects and a within-subject residual df (cells − subjects − (k − 1)) ≥ 1",
			map[string]any{"subjects": m, "n_obs": d.nObs, "k": k})
	}
	// profiled state at γ: φ_e = yᵀP_H y / (N − p).
	at := func(gamma float64) (*rmState, error) {
		h, err := d.state(gamma, 1)
		if err != nil {
			return nil, err
		}
		var rss float64
		for gi := range d.groups {
			rss += h.ete(gi, h.q[gi])
		}
		s2 := rss / float64(resDF)
		if !(s2 > 0) || math.IsInf(s2, 0) {
			return nil, rmInsufficient("degenerate residual variance", map[string]any{"residual_variance": s2})
		}
		return d.state(float64(gamma*s2), s2)
	}
	gradAt := func(gamma float64) (float64, error) {
		st, err := at(gamma)
		if err != nil {
			return 0, err
		}
		return st.gradS(), nil
	}
	gamma := 0.0
	g0, err := gradAt(0)
	if err != nil {
		return nil, err
	}
	if g0 < 0 {
		lo, hi := 0.0, 1.0
		for {
			gh, err := gradAt(hi)
			if err != nil {
				return nil, err
			}
			if gh >= 0 {
				break
			}
			lo = hi
			hi *= 2
			if hi > 1<<60 {
				return nil, rmInsufficient("the subject variance has no finite REML optimum (no within-subject variation)",
					map[string]any{"variance_ratio": hi})
			}
		}
		for range 2000 {
			mid := lo + float64((hi-lo)*0.5)
			if mid <= lo || mid >= hi {
				break
			}
			gm, err := gradAt(mid)
			if err != nil {
				return nil, err
			}
			if gm < 0 {
				lo = mid
			} else {
				hi = mid
			}
		}
		gamma = lo + float64((hi-lo)*0.5)
	}
	st, err := at(gamma)
	if err != nil {
		return nil, err
	}
	return st.satterthwaite(gamma > 0, subjects, cells, conditions)
}

// satterthwaite is the F test of the condition contrasts at the fitted
// state; interior frees (φ_s, φ_e), the boundary φ_e alone.
func (st *rmState) satterthwaite(interior bool, subjects []string, cells map[string]map[string]float64, conditions []string) (*rmMixedFit, error) {
	d := st.d
	p, q := d.p, d.p-1
	free := []int{1}
	if interior {
		free = []int{0, 1}
	}
	nf := len(free)
	gj := make([][]float64, nf) // Xᵀ Q V_j Q X
	wj := make([][]float64, nf) // Xᵀ Q V_j Q e
	h := make([]float64, nf*nf)
	for a, j := range free {
		gj[a] = make([]float64, p*p)
		wj[a] = make([]float64, p)
		for gi, grp := range d.groups {
			qvq := st.q[gi].mul(vBlock(j), grp.n).mul(st.q[gi], grp.n)
			d.addXtX(gj[a], grp, qvq)
			st.addXte(wj[a], gi, qvq)
		}
	}
	for a, j := range free {
		for b, l := range free {
			kjl := make([]float64, p*p)
			var trQQ, quad float64
			for gi, grp := range d.groups {
				qv := st.q[gi].mul(vBlock(j), grp.n)
				qvqv := qv.mul(st.q[gi], grp.n).mul(vBlock(l), grp.n)
				trQQ += float64(grp.count * qvqv.trace(grp.n))
				qvqvq := qvqv.mul(st.q[gi], grp.n)
				d.addXtX(kjl, grp, qvqvq)
				quad += st.ete(gi, qvqvq)
			}
			trPP := trQQ - 2*rmTraceProd(st.c, kjl, p) + rmTraceProd(rmMatMul(st.c, gj[a], p), rmMatMul(st.c, gj[b], p), p)
			quadP := quad - rmDot(wj[a], rmMatVec(st.c, wj[b], p))
			h[a*nf+b] = -trPP + 2*quadP
		}
	}
	// A = 2 H⁻¹.
	acov := make([]float64, nf*nf)
	if nf == 1 {
		if !(h[0] > 0) {
			return nil, rmInsufficient("the REML Hessian is not positive definite", map[string]any{"hessian": h[0]})
		}
		acov[0] = 2 / h[0]
	} else {
		det := float64(h[0]*h[3]) - float64(h[1]*h[2])
		if !(h[0] > 0) || !(det > 0) {
			return nil, rmInsufficient("the REML Hessian is not positive definite", map[string]any{"determinant": det})
		}
		acov[0], acov[1], acov[2], acov[3] = 2*h[3]/det, -2*h[1]/det, -2*h[2]/det, 2*h[0]/det
	}
	// ∂C/∂φ_j = C G_j C, restricted to the condition block.
	dcj := make([][]float64, nf)
	for a := range free {
		dcj[a] = rmMatMul(rmMatMul(st.c, gj[a], p), st.c, p)
	}
	values := make([]float64, q)
	vectors := make([][]float64, q) // vectors[m] = m-th eigenvector
	if q == 1 {
		values[0] = st.c[1*p+1]
		vectors[0] = []float64{1}
	} else {
		packed := make([]float64, 0, q*(q+1)/2)
		for i := 1; i <= q; i++ {
			for j := i; j <= q; j++ {
				packed = append(packed, st.c[i*p+j])
			}
		}
		sym, err := linalg.NewSym(q, packed)
		if err != nil {
			return nil, err
		}
		eig, err := linalg.SymEigen(sym)
		if err != nil {
			return nil, err
		}
		for mm := 0; mm < q; mm++ {
			values[mm] = eig.Values.At(mm)
			vectors[mm] = make([]float64, q)
			for i := 0; i < q; i++ {
				vectors[mm][i] = eig.Vectors.At(i, mm)
			}
		}
	}
	betaC := st.beta[1:]
	var fsum float64
	nu := make([]float64, q)
	for mm := 0; mm < q; mm++ {
		dm := values[mm]
		if !(dm > 0) {
			return nil, rmInsufficient("the condition contrasts' covariance is singular", map[string]any{"eigenvalue": dm})
		}
		pm := vectors[mm]
		lb := rmDot(pm, betaC)
		fsum += float64(lb*lb) / dm
		grad := make([]float64, nf)
		for a := range free {
			var s float64
			for i := 0; i < q; i++ {
				for j := 0; j < q; j++ {
					s += float64(float64(pm[i]*dcj[a][(i+1)*p+(j+1)]) * pm[j])
				}
			}
			grad[a] = s
		}
		var den float64
		for a := 0; a < nf; a++ {
			for b := 0; b < nf; b++ {
				den += float64(float64(grad[a]*acov[a*nf+b]) * grad[b])
			}
		}
		nu[mm] = 2 * float64(dm*dm) / den
	}
	ddf := satterthwaiteFDDF(nu)
	f := fsum / float64(q)
	means := make([]float64, p)
	means[0] = st.beta[0]
	for j := 1; j < p; j++ {
		means[j] = st.beta[0] + st.beta[j]
	}
	complete := 0
	for _, s := range subjects {
		if len(cells[s]) == len(conditions) {
			complete++
		}
	}
	return &rmMixedFit{
		f: f, df1: float64(q), ddf: ddf, p: fSurvival(f, float64(q), ddf),
		phiS: st.phiS, phiE: st.phiE, conditionMeans: means,
		subjects: len(subjects), nObs: d.nObs, completeSubject: complete,
	}, nil
}

// satterthwaiteFDDF is lmerTest's get_Fstat_ddf: ν_1 for one
// direction or when every ν is within 1e-8 of ν_1; 2 when any ν ≤ 2;
// else 2E / (E − q), E = Σ ν / (ν − 2).
func satterthwaiteFDDF(nu []float64) float64 {
	if len(nu) == 1 {
		return nu[0]
	}
	same := true
	for _, v := range nu {
		if math.Abs(v-nu[0]) >= 1e-8 {
			same = false
			break
		}
	}
	if same {
		return nu[0]
	}
	var e float64
	for _, v := range nu {
		if !(v > 2) {
			return 2
		}
		e += v / (v - 2)
	}
	return 2 * e / (e - float64(len(nu)))
}

// finalizeMixed is Finalize under params.incomplete "mixed": every
// observed cell enters, no subject is dropped.
func (a *anovaRMRow) finalizeMixed(conditions []string) (*types.TestResult, error) {
	subjects := append([]string(nil), a.subjectOrder...)
	sort.Strings(subjects)
	fit, err := fitRMMixed(a.cells, subjects, conditions)
	if err != nil {
		return nil, err
	}
	res := &types.TestResult{
		Label:      testLabel(a.spec),
		Type:       types.TEST_ANOVA_RM,
		Variant:    "mixed",
		Statistic:  fit.f,
		DF:         fit.df1,
		PValue:     fit.p,
		Alpha:      a.alpha,
		RejectNull: fit.p < a.alpha,
		Details: map[string]any{
			"conditions":          conditions,
			"subjects":            fit.subjects,
			"complete_subjects":   fit.completeSubject,
			"incomplete_subjects": fit.subjects - fit.completeSubject,
			"dropped_subjects":    0,
			"n_obs":               fit.nObs,
			"condition_means":     fit.conditionMeans,
			"df_treatment":        fit.df1,
			"df_error":            fit.ddf,
			"var_subject":         fit.phiS,
			"var_residual":        fit.phiE,
		},
	}
	return res, nil
}
