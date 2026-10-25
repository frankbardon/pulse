package sem

import (
	"errors"
	"fmt"
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// ebm.go is lavaan's lavPredict factor scores of an ordinal fit (DWLS /
// WLSMV): empirical Bayes modal scores (method "EBM",
// lav_predict_eta_ebm_ml), the default — and the meaning of method
// "regression" — whenever an indicator is ordered. A row's scores are
// the mode of the latents' posterior given the row,
//
//	F(η) = ½ (η − μ)ᵀ V⁻¹ (η − μ) − Σ_i log f_i(y_i | ŷ_i),  ŷ = ν + Λη,
//
// μ = Cα and V = CΨCᵀ the latents' implied mean and covariance
// (C = (I − B)⁻¹), f_i an ordered indicator's category probability
// Φ((τ_k − ŷ_i)/s_i) − Φ((τ_{k−1} − ŷ_i)/s_i) (floored at the machine
// epsilon, as lavaan) and a numeric indicator's normal density
// N(y_i; ŷ_i, s_i²), s_i = √Θ_ii. As lavaan, only Θ's diagonal enters
// (local independence: a residual covariance moves no score), the
// thresholds are the raw τ and an ordered indicator's Θ_ii the delta
// parameterization's residual 1/Δ² − common — the latent response
// scale lavaan's own scorer reads (delta = FALSE).
//
// An observed inner variable d (an observed mediator: an observed
// variable with an outgoing arrow) is lavaan's ov.y dummy latent, and
// the split is lavaan's: η, μ and V are over the latents alone (the
// latent block of Cα and CΨCᵀ, so d's paths and variance shape the
// prior), and d enters the likelihood as a numeric indicator with
// ŷ_d = α_d + B_d,lat η and s_d = √Ψ_dd — its own row's value, never
// conditioned on in the prior. Every effect OF an observed variable on
// an observed one (B_d,d′, an output's Λ_i,d — lavaan makes such an
// output a dummy too, whose dummy inputs its scorer zeroes) is dropped
// from ŷ, exactly as lav_lisrel_eyetax does.
//
// The optimisation is deterministic: Newton's method from the fixed
// start μ with a halving (Armijo) line search — the full step once the
// Newton decrement is below ebmQuadratic — stopping when the decrement
// falls below ebmDecrement, when no halving descends, or after
// ebmMaxIter steps. F is
// strictly convex wherever no probability sits on its floor (V⁻¹ is
// positive definite and −log f_i convex in ŷ_i — the normal CDF
// difference is log-concave), so the mode is unique and the stop is
// reached in a handful of steps. lavaan's own stop (BFGS, reltol 1e-8)
// sits up to ~4e-4 off the mode on the brand survey; the oracle holds
// Pulse to lavaan's objective minimised to reltol 1e-15.
//
// The file is FMA-free (every product that feeds a sum barriered
// float64(a*b)) and calls only detmath through normal.go and
// math.Sqrt, so a score is a function of the row and the parameters
// alone (TestPolychoric_NoFusibleProducts scans it).

const (
	// ebmMaxIter bounds the Newton steps of one row.
	ebmMaxIter = 100
	// ebmDecrement is the stopping Newton decrement gᵀH⁻¹g: twice the
	// objective's distance to the mode, to second order.
	ebmDecrement = 1e-24
	// ebmQuadratic is the decrement below which the full Newton step is
	// taken without a line search (the objective's change, ½ the
	// decrement, is then below its rounding at any realistic F).
	ebmQuadratic = 1e-12
	// ebmFloor is lavaan's floor on a category probability
	// (.Machine$double.eps).
	ebmFloor = 2.220446049250313e-16
)

// ErrEBMResidualVariance refuses EBM scores of a fit with a residual
// variance (an ordered indicator's delta residual, a numeric indicator's
// Θ_ii) that is not positive: the indicator's density is undefined
// (lavaan refuses the same).
var ErrEBMResidualVariance = errors.New("a residual variance is not positive; ordinal (EBM) factor scores are undefined")

// EBMScorer is one group's EBM scoring rule (NewEBMScorer).
type EBMScorer struct {
	nLat, j int
	mu      []float64
	v, vinv [][]float64
	lam     [][]float64
	nu, sd  []float64
	th      [][]float64
	ordered []bool
}

// NewEBMScorer builds the EBM rule of latent from one group's
// parameters p of the ordinal model m (ModelOptions.Ordered; p carries
// the thresholds, Params.Thresholds). An observed inner variable is a
// dummy latent (the file comment); ErrScoreSingular when the latents'
// implied covariance is not positive definite; ErrEBMResidualVariance
// for a non-positive residual variance (an observed inner variable's
// Ψ_dd included).
func NewEBMScorer(m *Model, p Params, latent string) (*EBMScorer, error) {
	nObs, nIn := len(m.Observed), len(m.Inner)
	j, ok := m.innerIndex[latent]
	if !ok || !m.latent.has(latent) {
		return nil, fmt.Errorf("%q is not a latent variable of the model", latent)
	}
	if len(m.ordered) == 0 {
		return nil, errors.New("EBM scores need an ordinal model (ordered variables)")
	}
	// The latents lead Inner (classify); the observed inner variables
	// follow them.
	nLat := 0
	for _, v := range m.Inner {
		if m.latent.has(v) {
			nLat++
		} else if m.exogenous.has(v) {
			// An ordinal fit refuses an observed exogenous variable
			// (OrdinalSpecExogenous): lavaan conditions on it.
			return nil, fmt.Errorf("observed exogenous variable %q: ordinal (EBM) factor scores condition on no observed covariate", v)
		}
	}
	if err := p.check(nObs, nIn); err != nil {
		return nil, err
	}
	if len(p.Thresholds) != nObs {
		return nil, fmt.Errorf("the fit carries thresholds for %d observed variables, the model has %d", len(p.Thresholds), nObs)
	}
	s := &EBMScorer{nLat: nLat, j: j, nu: append([]float64(nil), p.Nu...), sd: make([]float64, nObs),
		th: make([][]float64, nObs), ordered: make([]bool, nObs), lam: make([][]float64, nObs)}
	for i, v := range m.Observed {
		s.ordered[i] = m.ordered.has(v)
		if s.ordered[i] {
			if len(p.Thresholds[i]) == 0 {
				return nil, fmt.Errorf("ordered variable %q carries no thresholds", v)
			}
			s.th[i] = append([]float64(nil), p.Thresholds[i]...)
		} else if p.Thresholds[i] != nil {
			return nil, fmt.Errorf("numeric variable %q carries thresholds", v)
		}
		t := p.Theta[i][i]
		s.lam[i] = append([]float64(nil), p.Lambda[i][:nLat]...)
		if d, inner := m.innerIndex[v]; inner {
			// A dummy latent (lavaan's ov.y dummy): Λ_d is the identity
			// row, ŷ_d = ν_d + α_d + B_d,lat η (its paths from observed
			// variables dropped), s_d² = Ψ_dd (lavaan's Θ for a dummy).
			s.nu[i] += p.Alpha[d]
			for a := 0; a < nLat; a++ {
				s.lam[i][a] += p.Beta[d][a]
			}
			t = p.Psi[d][d]
		}
		if !(t > 0) || math.IsInf(t, 0) {
			return nil, ErrEBMResidualVariance
		}
		s.sd[i] = math.Sqrt(t)
	}
	c, ok := invertGeneral(identityMinus(p.Beta))
	if !ok {
		return nil, ErrScoreSingular
	}
	// μ = Cα, V = CΨCᵀ.
	s.mu = make([]float64, nIn)
	for a := 0; a < nIn; a++ {
		v := 0.0
		for b := 0; b < nIn; b++ {
			v += float64(c[a][b] * p.Alpha[b])
		}
		s.mu[a] = v
	}
	cp := make([][]float64, nIn)
	for a := range cp {
		cp[a] = make([]float64, nIn)
		for b := 0; b < nIn; b++ {
			v := 0.0
			for k := 0; k < nIn; k++ {
				v += float64(c[a][k] * p.Psi[k][b])
			}
			cp[a][b] = v
		}
	}
	vm := make([][]float64, nIn)
	for a := range vm {
		vm[a] = make([]float64, nIn)
	}
	for a := 0; a < nIn; a++ {
		for b := 0; b <= a; b++ {
			v := 0.0
			for k := 0; k < nIn; k++ {
				v += float64(cp[a][k] * c[b][k])
			}
			vm[a][b], vm[b][a] = v, v
		}
	}
	// The latents' block: an observed inner variable is no coordinate
	// of the solve (lavaan's remove_dummy_lv).
	s.mu = s.mu[:nLat]
	vm = vm[:nLat]
	for a := range vm {
		vm[a] = vm[a][:nLat]
	}
	l, ok := cholesky(vm)
	if !ok {
		return nil, ErrScoreSingular
	}
	s.v = vm
	s.vinv = make([][]float64, nLat)
	e := make([]float64, nLat)
	for a := 0; a < nLat; a++ {
		for b := range e {
			e[b] = 0
		}
		e[a] = 1
		s.vinv[a] = cholSolve(l, e)
	}
	for a := 0; a < nLat; a++ {
		for b := 0; b < a; b++ {
			v := float64(float64(s.vinv[a][b]+s.vinv[b][a]) / 2)
			s.vinv[a][b], s.vinv[b][a] = v, v
		}
	}
	for a := range s.vinv {
		for _, v := range s.vinv[a] {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, ErrScoreSingular
			}
		}
	}
	return s, nil
}

// Score is the row's score on the scorer's latent: the posterior mode's
// coordinate (Mode). y is over Observed, every value present: an ordered
// indicator's category code (0 the lowest category), a numeric
// indicator's value.
func (s *EBMScorer) Score(y []float64) float64 {
	eta := make([]float64, s.nLat)
	s.Mode(y, eta)
	return eta[s.j]
}

// Mode writes the posterior mode of every latent (Inner order) for the
// row y into eta and returns the Newton steps taken.
func (s *EBMScorer) Mode(y, eta []float64) int {
	n := s.nLat
	copy(eta, s.mu)
	g := make([]float64, n)
	h := make([][]float64, n)
	for a := range h {
		h[a] = make([]float64, n)
	}
	trial := make([]float64, n)
	f := s.eval(y, eta, g, h)
	it := 0
	for ; it < ebmMaxIter; it++ {
		l, ok := cholesky(h)
		var d []float64
		if ok {
			d = cholSolve(l, g)
		} else {
			// A Hessian that lost definiteness (a probability on its
			// floor): the prior-preconditioned gradient step.
			d = make([]float64, n)
			for a := 0; a < n; a++ {
				v := 0.0
				for b := 0; b < n; b++ {
					v += float64(s.v[a][b] * g[b])
				}
				d[a] = v
			}
		}
		dec := 0.0
		for a := 0; a < n; a++ {
			dec += float64(g[a] * d[a])
		}
		if !(dec > ebmDecrement) {
			break
		}
		step := 1.0
		if dec > ebmQuadratic {
			// Far from the mode: halve the step until the objective
			// drops by the Armijo margin; none left is the rounding
			// floor.
			for {
				for a := 0; a < n; a++ {
					trial[a] = eta[a] - float64(step*d[a])
				}
				if s.objective(y, trial) <= f-float64(1e-4*float64(step*dec)) {
					break
				}
				if step = float64(step / 2); step < 1e-10 {
					return it
				}
			}
		} else {
			// Inside the quadratic region the full Newton step is taken
			// as is: the objective's change sits below its rounding.
			for a := 0; a < n; a++ {
				trial[a] = eta[a] - d[a]
			}
		}
		copy(eta, trial)
		f = s.eval(y, eta, g, h)
	}
	return it
}

// objective is F at eta.
func (s *EBMScorer) objective(y, eta []float64) float64 {
	f := s.priorTerm(eta)
	for i := range s.nu {
		ll, _, _ := s.logDensity(i, y[i], s.yhat(i, eta))
		f -= ll
	}
	return f
}

// eval is F at eta with its gradient g and Hessian h.
func (s *EBMScorer) eval(y, eta, g []float64, h [][]float64) float64 {
	n := s.nLat
	dev := make([]float64, n)
	for a := 0; a < n; a++ {
		dev[a] = eta[a] - s.mu[a]
	}
	for a := 0; a < n; a++ {
		v := 0.0
		for b := 0; b < n; b++ {
			v += float64(s.vinv[a][b] * dev[b])
			h[a][b] = s.vinv[a][b]
		}
		g[a] = v
	}
	f := s.priorTerm(eta)
	for i := range s.nu {
		ll, d1, c := s.logDensity(i, y[i], s.yhat(i, eta))
		f -= ll
		li := s.lam[i]
		for a := 0; a < n; a++ {
			if li[a] == 0 {
				continue
			}
			g[a] -= float64(d1 * li[a])
			ca := float64(c * li[a])
			for b := 0; b < n; b++ {
				h[a][b] += float64(ca * li[b])
			}
		}
	}
	return f
}

// priorTerm is ½ (η − μ)ᵀ V⁻¹ (η − μ).
func (s *EBMScorer) priorTerm(eta []float64) float64 {
	q := 0.0
	for a := 0; a < s.nLat; a++ {
		da := eta[a] - s.mu[a]
		v := 0.0
		for b := 0; b < s.nLat; b++ {
			v += float64(s.vinv[a][b] * float64(eta[b]-s.mu[b]))
		}
		q += float64(da * v)
	}
	return float64(q / 2)
}

// yhat is ν_i + Λ_i η.
func (s *EBMScorer) yhat(i int, eta []float64) float64 {
	v := s.nu[i]
	for a, l := range s.lam[i] {
		v += float64(l * eta[a])
	}
	return v
}

// logDensity is log f_i(y | ŷ) with its first derivative in ŷ and the
// negated second derivative (the curvature c ≥ 0): an ordered
// indicator's log category probability (floored at ebmFloor, where the
// derivatives are 0), a numeric indicator's log normal density less
// its constant.
func (s *EBMScorer) logDensity(i int, y, yh float64) (float64, float64, float64) {
	sd := s.sd[i]
	if !s.ordered[i] {
		z := float64(y-yh) / sd
		return -float64(float64(z*z)/2) - detmath.Log(sd), float64(z / sd), float64(1 / float64(sd*sd))
	}
	th := s.th[i]
	k := int(y)
	zu, zl := math.Inf(1), math.Inf(-1)
	if k < len(th) {
		zu = float64(th[k]-yh) / sd
	}
	if k > 0 {
		zl = float64(th[k-1]-yh) / sd
	}
	var p float64
	if zl > 0 {
		// Both limits in the upper tail: the complements keep the
		// difference's precision.
		p = stdNormCDF(-zl) - stdNormCDF(-zu)
	} else {
		p = stdNormCDF(zu) - stdNormCDF(zl)
	}
	if !(p >= ebmFloor) {
		return detmath.Log(ebmFloor), 0, 0
	}
	pu, pl := stdNormPDF(zu), stdNormPDF(zl)
	zpu, zpl := 0.0, 0.0
	if !math.IsInf(zu, 0) {
		zpu = float64(zu * pu)
	}
	if !math.IsInf(zl, 0) {
		zpl = float64(zl * pl)
	}
	// p' = (φ(z_lo) − φ(z_up)) / s, p'' = (z_lo φ(z_lo) − z_up φ(z_up)) / s².
	r1 := float64(float64(pl-pu)/sd) / p
	r2 := float64(float64(zpl-zpu)/float64(sd*sd)) / p
	return detmath.Log(p), r1, float64(r1*r1) - r2
}

// identityMinus is I − b.
func identityMinus(b [][]float64) [][]float64 {
	n := len(b)
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, n)
		for k := 0; k < n; k++ {
			out[i][k] = -b[i][k]
		}
		out[i][i]++
	}
	return out
}

// invertGeneral inverts a by Gauss–Jordan elimination with partial
// pivoting (false when singular).
func invertGeneral(a [][]float64) ([][]float64, bool) {
	n := len(a)
	w := make([][]float64, n)
	for i := range w {
		w[i] = make([]float64, 2*n)
		copy(w[i], a[i])
		w[i][n+i] = 1
	}
	for col := 0; col < n; col++ {
		piv := col
		for r := col + 1; r < n; r++ {
			if math.Abs(w[r][col]) > math.Abs(w[piv][col]) {
				piv = r
			}
		}
		if !(math.Abs(w[piv][col]) > 1e-300) {
			return nil, false
		}
		w[col], w[piv] = w[piv], w[col]
		d := w[col][col]
		for k := range w[col] {
			w[col][k] /= d
		}
		for r := 0; r < n; r++ {
			if r == col || w[r][col] == 0 {
				continue
			}
			fct := w[r][col]
			for k := range w[r] {
				w[r][k] -= float64(fct * w[col][k])
			}
		}
	}
	out := make([][]float64, n)
	for i := range out {
		out[i] = append([]float64(nil), w[i][n:]...)
	}
	return out, true
}

// cholesky is the lower Cholesky factor of the symmetric a (false when a
// is not positive definite).
func cholesky(a [][]float64) ([][]float64, bool) {
	n := len(a)
	l := make([][]float64, n)
	for i := range l {
		l[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for k := 0; k <= i; k++ {
			v := a[i][k]
			for m := 0; m < k; m++ {
				v -= float64(l[i][m] * l[k][m])
			}
			if i == k {
				if !(v > 0) {
					return nil, false
				}
				l[i][i] = math.Sqrt(v)
			} else {
				l[i][k] = v / l[k][k]
			}
		}
	}
	return l, true
}

// cholSolve solves (L Lᵀ) x = b.
func cholSolve(l [][]float64, b []float64) []float64 {
	n := len(l)
	z := make([]float64, n)
	for i := 0; i < n; i++ {
		v := b[i]
		for k := 0; k < i; k++ {
			v -= float64(l[i][k] * z[k])
		}
		z[i] = v / l[i][i]
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		v := z[i]
		for k := i + 1; k < n; k++ {
			v -= float64(l[k][i] * x[k])
		}
		x[i] = v / l[i][i]
	}
	return x
}
