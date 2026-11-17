package regordinal

import (
	"math"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// nominal.go is the partial proportional-odds model and the
// likelihood-ratio test of proportional odds built on it
// (params.nominal_test; ordinal::nominal_test, which refits clm with
// nominal = ~term). In the partial model a NON-PARALLEL (nominal)
// predictor carries its own slope at every cumulative split,
//
//	P(Y ≤ k | x) = F(η_k),  η_k = ζ_k − Σ_j x_j β_{j,k},
//
// β_{j,k} = β_j for a parallel predictor. The parameter vector holds,
// per predictor in spec order, one slope (parallel) or K − 1 split
// slopes (nominal), then the K − 1 thresholds. Every η_k is linear in
// θ, so with A = ∂η_upper/∂θ and B = ∂η_lower/∂θ a row's score is
// s = (f(a)·A − f(b)·B) / p and its observed information
// s sᵀ − (f′(a)·A Aᵀ − f′(b)·B Bᵀ) / p (p = F(a) − F(b)). The fit is
// Newton–Raphson (maximise) from the proportional-odds MLE — each
// nominal predictor's split slopes start at its common slope — on any
// link. Rows are summed serially in record order and every product is
// barriered, so the figures are bit-identical across worker counts and
// architectures.
//
// The test of predictor j is 2·(ℓ_partial − ℓ_po) on (K − 2) df, j
// alone non-parallel; the omnibus makes every predictor non-parallel,
// (K − 2)·p df. A partial fit that does not converge (a split a
// predictor separates sends its slope to infinity), or whose fitted
// cumulative probabilities cross at a fitted row (η_k ≥ η_{k+1}: a
// negative level probability the observed rows never priced), leaves
// that figure NaN — null on the wire — with a coded warning; the
// ordinal fit stands.

// nominalFigure is one likelihood-ratio test: its statistic, df and
// p-value (NaN when the partial fit failed).
type nominalFigure struct {
	stat, df, p float64
}

// nominalFailure says why a partial fit gave no figure: reason
// "nominal_test_no_converge" (cause the Newton failure) or
// "nominal_test_crossing".
type nominalFailure struct {
	term   int // predictor index; −1 = the omnibus
	reason string
	cause  string
	iters  int
}

// nominalResult is the omnibus test, each predictor's (spec order) and
// the failed fits, terms first then the omnibus.
type nominalResult struct {
	omnibus  nominalFigure
	terms    []nominalFigure
	failures []nominalFailure
}

// The nominal_test warning reasons (details.reason of
// PROCESSING_REGRESSION_DIAGNOSTIC).
const (
	ReasonNominalNoConverge = "nominal_test_no_converge"
	ReasonNominalCrossing   = "nominal_test_crossing"
)

// ppo is the partial proportional-odds parameterisation of a design.
type ppo struct {
	d *design
	// idx[j][k] is predictor j's slope index at split k (one index for
	// every k when j is parallel).
	idx  [][]int
	zeta int // the first threshold's index
	q    int
}

// newPPO lays out θ for the nominal set (nominal[j]: predictor j has
// its own slope per split).
func newPPO(d *design, nominal []bool) *ppo {
	m := &ppo{d: d, idx: make([][]int, d.p)}
	at := 0
	for j := range d.p {
		m.idx[j] = make([]int, d.k-1)
		for k := range m.idx[j] {
			if nominal[j] {
				m.idx[j][k] = at + k
			} else {
				m.idx[j][k] = at
			}
		}
		if nominal[j] {
			at += d.k - 1
		} else {
			at++
		}
	}
	m.zeta = at
	m.q = at + d.k - 1
	return m
}

// start maps the proportional-odds θ (β_1..β_p, ζ) onto the layout: a
// nominal predictor's split slopes all start at its common slope.
func (m *ppo) start(po []float64) []float64 {
	theta := make([]float64, m.q)
	for j, row := range m.idx {
		for _, at := range row {
			theta[at] = po[j]
		}
	}
	copy(theta[m.zeta:], po[m.d.p:])
	return theta
}

// eta is row i's linear predictor at split k: ζ_k − Σ_j x_ij β_{j,k}.
func (m *ppo) eta(theta []float64, i, k int) float64 {
	s := 0.0
	for j, v := range m.d.x[i] {
		s += float64(v * theta[m.idx[j][k]])
	}
	return theta[m.zeta+k] - s
}

// loglik is ℓ(θ), false when a fitted row's probability is not
// positive or ℓ is not finite.
func (m *ppo) loglik(l link, theta []float64) (float64, bool) {
	d := m.d
	ll := 0.0
	for i, y := range d.y {
		fA, fB := 1.0, 0.0
		if y < d.k-1 {
			fA, _, _ = l.eval(m.eta(theta, i, y))
		}
		if y > 0 {
			fB, _, _ = l.eval(m.eta(theta, i, y-1))
		}
		pr := fA - fB
		if !(pr > 0) {
			return 0, false
		}
		ll += float64(d.w[i] * detmath.Log(pr))
	}
	if math.IsNaN(ll) || math.IsInf(ll, 0) {
		return 0, false
	}
	return ll, true
}

// gradInfo is ∂ℓ/∂θ and the observed information at θ. A and B are
// sparse (a threshold and one slope per predictor), so a row costs
// O(p²), not O(q²).
func (m *ppo) gradInfo(l link, theta []float64) ([]float64, *linalg.Sym) {
	d := m.d
	q := m.q
	grad := make([]float64, q)
	info := make([][]float64, q)
	for j := range info {
		info[j] = make([]float64, q)
	}
	// add accumulates into the lower triangle.
	add := func(a, b int, v float64) {
		if a < b {
			a, b = b, a
		}
		info[a][b] += v
	}
	s := make([]float64, q)
	mark := make([]bool, q)
	var nz []int
	// dir fills the nonzero entries of ∂η_k/∂θ for row i: +1 at ζ_k,
	// −x_ij at each slope (indices distinct).
	at := make([][]int, 2)
	val := make([][]float64, 2)
	dir := func(slot, i, k int) {
		at[slot] = append(at[slot][:0], m.zeta+k)
		val[slot] = append(val[slot][:0], 1)
		for j, v := range d.x[i] {
			at[slot] = append(at[slot], m.idx[j][k])
			val[slot] = append(val[slot], -v)
		}
	}
	for i, y := range d.y {
		w := d.w[i]
		up, lo := y < d.k-1, y > 0
		fA, fB := 1.0, 0.0
		var fa, fb, fpa, fpb float64
		at[0], at[1] = at[0][:0], at[1][:0]
		if up {
			fA, fa, fpa = l.eval(m.eta(theta, i, y))
			dir(0, i, y)
		}
		if lo {
			fB, fb, fpb = l.eval(m.eta(theta, i, y-1))
			dir(1, i, y-1)
		}
		pr := fA - fB
		ga, gb := fa/pr, fb/pr
		nz = nz[:0]
		for slot, g := range [2]float64{ga, -gb} {
			for x, a := range at[slot] {
				if !mark[a] {
					mark[a] = true
					nz = append(nz, a)
				}
				s[a] += float64(g * val[slot][x])
			}
		}
		for x, a := range nz {
			grad[a] += float64(w * s[a])
			ws := float64(w * s[a])
			for _, b := range nz[:x+1] {
				add(a, b, float64(ws*s[b]))
			}
		}
		// − f′(a)/p·A Aᵀ + f′(b)/p·B Bᵀ.
		for slot, c := range [2]float64{-float64(w * (fpa / pr)), float64(w * (fpb / pr))} {
			for x, a := range at[slot] {
				cv := float64(c * val[slot][x])
				for z, b := range at[slot][:x+1] {
					add(a, b, float64(cv*val[slot][z]))
				}
			}
		}
		for _, a := range nz {
			s[a] = 0
			mark[a] = false
		}
	}
	sym, _ := linalg.NewSymFromRows(info)
	return grad, sym
}

// crosses reports whether the fitted cumulative probabilities cross at
// a fitted row: η_k ≥ η_{k+1} for some row and split.
func (m *ppo) crosses(theta []float64) bool {
	for i := range m.d.y {
		prev := m.eta(theta, i, 0)
		for k := 1; k < m.d.k-1; k++ {
			e := m.eta(theta, i, k)
			if !(e > prev) {
				return true
			}
			prev = e
		}
	}
	return false
}

// fit maximises the partial model's ℓ from the proportional-odds MLE
// po: the converged ℓ, or the failure reason / cause / iterations.
func (m *ppo) fit(l link, po []float64) (float64, *nominalFailure) {
	sol, fail := maximise(m.start(po), objective{
		loglik:   func(theta []float64) (float64, bool) { return m.loglik(l, theta) },
		gradInfo: func(theta []float64) ([]float64, *linalg.Sym) { return m.gradInfo(l, theta) },
	})
	if fail != nil {
		return 0, &nominalFailure{reason: ReasonNominalNoConverge, cause: fail.reason, iters: fail.iters}
	}
	if m.crosses(sol.theta) {
		return 0, &nominalFailure{reason: ReasonNominalCrossing, iters: sol.iters}
	}
	return sol.ll, nil
}

// nominalTest runs the per-predictor and omnibus likelihood-ratio tests
// against the proportional-odds fit sol (link l). k ≥ 3.
func (d *design) nominalTest(l link, sol *solution) nominalResult {
	p, k := d.p, d.k
	res := nominalResult{terms: make([]nominalFigure, p)}
	run := func(term int, nominal []bool) nominalFigure {
		df := float64((k - 2) * countTrue(nominal))
		ll, fail := newPPO(d, nominal).fit(l, sol.theta)
		if fail != nil {
			fail.term = term
			res.failures = append(res.failures, *fail)
			return nominalFigure{stat: math.NaN(), df: df, p: math.NaN()}
		}
		st := float64(2 * (ll - sol.ll))
		return nominalFigure{stat: st, df: df, p: chiSquareP(st, df)}
	}
	for a := range p {
		nominal := make([]bool, p)
		nominal[a] = true
		res.terms[a] = run(a, nominal)
	}
	all := make([]bool, p)
	for a := range all {
		all[a] = true
	}
	res.omnibus = run(-1, all)
	return res
}

// countTrue counts the set flags.
func countTrue(v []bool) int {
	n := 0
	for _, b := range v {
		if b {
			n++
		}
	}
	return n
}
