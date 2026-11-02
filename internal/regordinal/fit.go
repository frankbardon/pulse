package regordinal

import (
	"math"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// fit.go is the proportional-odds maximum-likelihood fit: Newton–Raphson
// on the analytic gradient and observed information of
//
//	ℓ(β, ζ) = Σ_i w_i · log(F(ζ_{y_i} − x_iᵀβ) − F(ζ_{y_i−1} − x_iᵀβ)),
//
// ζ_0 = −∞, ζ_K = +∞ (MASS::polr's likelihood; w the prior weight w*).
// The parameter vector θ is (β_1..β_p, ζ_1..ζ_{K−1}), polr's
// c(coef, zeta) order. Rows are summed serially in record order and
// every product is barriered, so a fit is bit-identical across worker
// counts and architectures.

// design is one fit's rows: x the predictor values (n × p), y the
// 0-based level of each row (0..k−1), w its prior weight w*.
type design struct {
	x [][]float64
	y []int
	w []float64
	k int
	p int
}

// q is the parameter count p + k − 1.
func (d *design) q() int { return d.p + d.k - 1 }

// rowEval is one row's likelihood terms at θ.
type rowEval struct {
	pr                 float64 // F(a) − F(b)
	fa, fb, fpa, fpb   float64
	up, lo             bool // the row has an upper (y < K) / lower (y > 1) threshold
	upperIdx, lowerIdx int  // their θ indices
}

// eval evaluates row i at θ (eta = x_iᵀβ precomputed).
func (d *design) eval(l link, theta []float64, i int, eta float64) rowEval {
	y := d.y[i]
	r := rowEval{up: y < d.k-1, lo: y > 0, upperIdx: d.p + y, lowerIdx: d.p + y - 1}
	fA, fB := 1.0, 0.0
	if r.up {
		fA, r.fa, r.fpa = l.eval(theta[r.upperIdx] - eta)
	}
	if r.lo {
		fB, r.fb, r.fpb = l.eval(theta[r.lowerIdx] - eta)
	}
	r.pr = fA - fB
	return r
}

// linear is x_iᵀβ.
func (d *design) linear(theta []float64, i int) float64 {
	eta := 0.0
	for j, v := range d.x[i] {
		eta += float64(v * theta[j])
	}
	return eta
}

// score writes row i's score ∂ℓ_i/∂θ (unweighted) into s (len q).
func (d *design) score(r rowEval, i int, s []float64) {
	clear(s)
	g := (r.fa - r.fb) / r.pr
	for j, v := range d.x[i] {
		s[j] = -float64(g * v)
	}
	if r.up {
		s[r.upperIdx] = r.fa / r.pr
	}
	if r.lo {
		s[r.lowerIdx] = -r.fb / r.pr
	}
}

// loglik is ℓ(θ), and false when a row's probability is not positive
// or the thresholds are not strictly increasing (a step out of the
// parameter space).
func (d *design) loglik(l link, theta []float64) (float64, bool) {
	for k := d.p + 1; k < d.q(); k++ {
		if !(theta[k] > theta[k-1]) {
			return 0, false
		}
	}
	ll := 0.0
	for i := range d.y {
		r := d.eval(l, theta, i, d.linear(theta, i))
		if !(r.pr > 0) {
			return 0, false
		}
		ll += float64(d.w[i] * detmath.Log(r.pr))
	}
	if math.IsNaN(ll) || math.IsInf(ll, 0) {
		return 0, false
	}
	return ll, true
}

// gradInfo is ∂ℓ/∂θ and the observed information −∂²ℓ/∂θ∂θᵀ at θ.
func (d *design) gradInfo(l link, theta []float64) ([]float64, *linalg.Sym) {
	q := d.q()
	grad := make([]float64, q)
	info := make([][]float64, q)
	for j := range info {
		info[j] = make([]float64, q)
	}
	s := make([]float64, q)
	for i := range d.y {
		w := d.w[i]
		r := d.eval(l, theta, i, d.linear(theta, i))
		d.score(r, i, s)
		for j, v := range s {
			grad[j] += float64(w * v)
		}
		pr := r.pr
		g := (r.fa - r.fb) / pr
		x := d.x[i]
		// ββ: x xᵀ·((f′a − f′b)/p − g²); the information is its negative.
		cbb := float64(w * ((r.fpa-r.fpb)/pr - float64(g*g)))
		for j := range x {
			wj := float64(cbb * x[j])
			for k := 0; k <= j; k++ {
				info[j][k] -= float64(wj * x[k])
			}
		}
		if r.up {
			u := r.upperIdx
			ra := r.fa / pr
			info[u][u] -= float64(w * (r.fpa/pr - float64(ra*ra)))
			// βζ_upper: x·(−f′a/p + fa·g/p).
			c := float64(w * (-r.fpa/pr + float64(r.fa*g)/pr))
			for j := range x {
				info[u][j] -= float64(c * x[j])
			}
		}
		if r.lo {
			lw := r.lowerIdx
			rb := r.fb / pr
			info[lw][lw] -= float64(w * (-r.fpb/pr - float64(rb*rb)))
			// βζ_lower: x·(f′b/p − fb·g/p).
			c := float64(w * (r.fpb/pr - float64(r.fb*g)/pr))
			for j := range x {
				info[lw][j] -= float64(c * x[j])
			}
		}
		if r.up && r.lo {
			// ζ_upper ζ_lower: fa·fb/p²; upperIdx > lowerIdx.
			info[r.upperIdx][r.lowerIdx] -= float64(w * (float64(r.fa*r.fb) / float64(pr*pr)))
		}
	}
	sym, _ := linalg.NewSymFromRows(info)
	return grad, sym
}

// start is the Newton start: β = 0 and each ζ_k = F⁻¹ of the weighted
// cumulative share of the levels ≤ k — the thresholds-only MLE.
func (d *design) start(l link) []float64 {
	theta := make([]float64, d.q())
	shares := d.levelWeights()
	total, cum := 0.0, 0.0
	for _, v := range shares {
		total += v
	}
	for k := 0; k < d.k-1; k++ {
		cum += shares[k]
		theta[d.p+k] = l.quantile(cum / total)
	}
	return theta
}

// levelWeights is Σw per level.
func (d *design) levelWeights() []float64 {
	out := make([]float64, d.k)
	for i, y := range d.y {
		out[y] += d.w[i]
	}
	return out
}

// maxNewton caps the Newton iterations; stepTol is the relative step
// the fit stops below (Newton converges quadratically, so the next step
// would sit at rounding); maxHalvings caps a step's halvings.
const (
	maxNewton   = 100
	stepTol     = 1e-12
	maxHalvings = 60
)

// solution is a converged fit.
type solution struct {
	theta []float64
	ll    float64
	info  *linalg.Sym
	iters int
}

// fitFailure is why newton stopped without a solution (the caller's
// details.reason) and the iteration it stopped at.
type fitFailure struct {
	reason string
	iters  int
}

// newton maximises ℓ from the start: each step solves I·δ = ∇ℓ and is
// halved until it stays inside the parameter space (increasing
// thresholds, every row probability positive) and does not lower ℓ.
// It stops when the relative step max_j |δ_j| / max(|θ_j|, 1) falls to
// stepTol — then the information is re-evaluated at the final θ.
func (d *design) newton(l link) (*solution, *fitFailure) {
	theta := d.start(l)
	ll, ok := d.loglik(l, theta)
	if !ok {
		return nil, &fitFailure{reason: "start"}
	}
	next := make([]float64, len(theta))
	for it := 1; it <= maxNewton; it++ {
		grad, info := d.gradInfo(l, theta)
		step, err := linalg.SolveSPD(info, linalg.NewVec(grad))
		if err != nil {
			return nil, &fitFailure{reason: "singular_information", iters: it}
		}
		delta := step.Slice()
		accepted, halved := false, false
		var nll float64
		for h := 0; h < maxHalvings; h++ {
			halved = h > 0
			for j := range theta {
				next[j] = theta[j] + delta[j]
			}
			if v, ok := d.loglik(l, next); ok && v >= ll-float64(1e-12*math.Abs(ll)) {
				nll, accepted = v, true
				break
			}
			for j := range delta {
				delta[j] *= 0.5
			}
		}
		if !accepted {
			return nil, &fitFailure{reason: "step_halving", iters: it}
		}
		rel := 0.0
		for j := range theta {
			if r := math.Abs(delta[j]) / math.Max(math.Abs(next[j]), 1); r > rel {
				rel = r
			}
		}
		copy(theta, next)
		ll = nll
		// A halved step is no evidence of convergence: only a full
		// Newton step this small is.
		if rel <= stepTol && !halved {
			_, info := d.gradInfo(l, theta)
			return &solution{theta: theta, ll: ll, info: info, iters: it}, nil
		}
	}
	return nil, &fitFailure{reason: "max_iters", iters: maxNewton}
}

// probabilities is P(Y = k | x_i) for row i at θ, written into out
// (len k).
func (d *design) probabilities(l link, theta []float64, i int, out []float64) {
	eta := d.linear(theta, i)
	prev := 0.0
	for k := 0; k < d.k; k++ {
		cur := 1.0
		if k < d.k-1 {
			cur, _, _ = l.eval(theta[d.p+k] - eta)
		}
		out[k] = cur - prev
		prev = cur
	}
}

// fittedProbabilities is Σ_i w_i·P(Y = k | x_i) / Σ_i w_i per level.
func (d *design) fittedProbabilities(l link, theta []float64) []float64 {
	out := make([]float64, d.k)
	row := make([]float64, d.k)
	sw := 0.0
	for i := range d.y {
		d.probabilities(l, theta, i, row)
		for k, v := range row {
			out[k] += float64(d.w[i] * v)
		}
		sw += d.w[i]
	}
	for k := range out {
		out[k] /= sw
	}
	return out
}

// nullLoglik is the thresholds-only fit's ℓ₀ = Σ_k W_k·log(W_k / W): its
// MLE puts every row at the weighted level shares.
func (d *design) nullLoglik() float64 {
	shares := d.levelWeights()
	total := 0.0
	for _, v := range shares {
		total += v
	}
	ll := 0.0
	for _, v := range shares {
		ll += float64(v * detmath.Log(v/total))
	}
	return ll
}
