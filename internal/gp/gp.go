package gp

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// Hyper is one setting of the kernel hyperparameters, every value > 0 and
// finite.
type Hyper struct {
	// Amplitude is the signal variance σ_f² (in standardised-y units).
	Amplitude float64
	// LengthScales holds one length scale per dimension (Space.Dims),
	// shared by every encoded column of a categorical dimension.
	LengthScales []float64
	// Noise is the white-noise variance σ_n² (standardised-y units),
	// added to the training covariance's diagonal.
	Noise float64
}

// clone returns h with its own LengthScales.
func (h Hyper) clone() Hyper {
	h.LengthScales = append([]float64(nil), h.LengthScales...)
	return h
}

func (h Hyper) validate(dims int) error {
	if len(h.LengthScales) != dims {
		return fmt.Errorf("gp: %d length scales for %d dimensions", len(h.LengthScales), dims)
	}
	if !positive(h.Amplitude) {
		return fmt.Errorf("gp: amplitude %v is not a positive finite number", h.Amplitude)
	}
	if !positive(h.Noise) {
		return fmt.Errorf("gp: noise %v is not a positive finite number", h.Noise)
	}
	for d, l := range h.LengthScales {
		if !positive(l) {
			return fmt.Errorf("gp: length scale %d (%v) is not a positive finite number", d, l)
		}
	}
	return nil
}

func positive(v float64) bool { return v > 0 && !math.IsInf(v, 1) }

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// DefaultNugget is the jitter always added to the training covariance's
// diagonal (scikit-learn's alpha default).
const DefaultNugget = 1e-10

// log2Pi is ln(2π) (numpy's np.log(2 * np.pi)).
const log2Pi = 1.8378770664093453

// fiveThirds is the Matérn-5/2 gradient's 5/3.
const fiveThirds = 5.0 / 3.0

// sqrt5 is √5, correctly rounded (math.Sqrt is IEEE-exact).
var sqrt5 = math.Sqrt(5)

// data is a validated, standardised training set.
type data struct {
	space *Space
	x     [][]float64
	yn    []float64
	yMean float64
	yStd  float64
}

// prepare validates the training set (at least one row; each row
// Space.Cols finite values; y finite, one per row) and standardises y by
// its mean and population standard deviation; a deviation below 10·ε
// reads as 1 (scikit-learn's _handle_zeros_in_scale).
func prepare(space *Space, x [][]float64, y []float64) (*data, error) {
	if space == nil {
		return nil, fmt.Errorf("gp: nil space")
	}
	n := len(x)
	if n == 0 {
		return nil, fmt.Errorf("gp: no observations")
	}
	if len(y) != n {
		return nil, fmt.Errorf("gp: %d rows but %d targets", n, len(y))
	}
	cols := space.Cols()
	d := &data{space: space, x: make([][]float64, n), yn: make([]float64, n)}
	for i, row := range x {
		if len(row) != cols {
			return nil, fmt.Errorf("gp: row %d has %d columns, the space encodes %d", i, len(row), cols)
		}
		for c, v := range row {
			if !finite(v) {
				return nil, fmt.Errorf("gp: row %d column %d is not finite", i, c)
			}
		}
		if !finite(y[i]) {
			return nil, fmt.Errorf("gp: target %d is not finite", i)
		}
		d.x[i] = append([]float64(nil), row...)
	}
	var sum float64
	for _, v := range y {
		sum += v
	}
	d.yMean = sum / float64(n)
	var ss float64
	for _, v := range y {
		dv := v - d.yMean
		ss += float64(dv * dv)
	}
	d.yStd = math.Sqrt(ss / float64(n))
	if d.yStd < 10*epsilon {
		d.yStd = 1
	}
	for i, v := range y {
		d.yn[i] = (v - d.yMean) / d.yStd
	}
	return d, nil
}

// epsilon is the float64 machine epsilon, 2⁻⁵².
const epsilon = 0x1p-52

// sqDist is the scaled squared distance Σ_c ((a_c − b_c)/ℓ_g(c))², and,
// when perDim is non-nil, each dimension's share of it (overwritten).
func (sp *Space) sqDist(a, b, ls, perDim []float64) float64 {
	for d := range perDim {
		perDim[d] = 0
	}
	var r2 float64
	for c := range a {
		t := (a[c] - b[c]) / ls[sp.group[c]]
		t2 := float64(t * t)
		r2 += t2
		if perDim != nil {
			perDim[sp.group[c]] += t2
		}
	}
	return r2
}

// matern52 is the unit-amplitude Matérn-5/2 correlation at scaled
// squared distance r2, (1 + a + a²/3)·exp(−a) with a = √5·√r2, and
// exp(−a) itself.
func matern52(r2 float64) (k, a, e float64) {
	a = float64(math.Sqrt(r2) * sqrt5)
	e = detmath.Exp(-a)
	poly := 1 + a + float64(a*a)/3
	return float64(poly * e), a, e
}

// state is one factored training covariance.
type state struct {
	n     int
	l     []float64 // lower Cholesky factor, row-major n×n
	alpha []float64 // K⁻¹·yn
	ridge float64
	lml   float64
}

// evaluate factors the training covariance at h and returns its log
// marginal likelihood; when grad is non-nil (len Dims+2) it is
// overwritten with the LML's gradient with respect to θ =
// (log Amplitude, log ℓ_0 … log ℓ_{D−1}, log Noise).
func evaluate(d *data, h Hyper, nugget float64, grad []float64) (*state, error) {
	sp := d.space
	n := len(d.x)
	dims := sp.Dims()
	k, err := linalg.NewSym(n, nil)
	if err != nil {
		return nil, err
	}
	diag := float64(h.Amplitude+h.Noise) + nugget
	for i := range n {
		k.Set(i, i, diag)
		for j := i + 1; j < n; j++ {
			m, _, _ := matern52(sp.sqDist(d.x[i], d.x[j], h.LengthScales, nil))
			k.Set(i, j, float64(h.Amplitude*m))
		}
	}
	lm, ridge, err := linalg.CholeskyRidge(k, nil)
	if err != nil {
		return nil, fmt.Errorf("gp: training covariance not positive definite after jitter: %w", err)
	}
	s := &state{n: n, l: lm.RowMajor(), ridge: ridge}
	s.alpha = s.backward(s.forward(d.yn))
	var dot, logDet float64
	for i := range n {
		dot += float64(d.yn[i] * s.alpha[i])
		logDet += detmath.Log(s.l[i*n+i])
	}
	s.lml = float64(-0.5*dot) - logDet - float64(float64(n)/2*log2Pi)
	if grad == nil {
		return s, nil
	}
	if len(grad) != dims+2 {
		return nil, fmt.Errorf("gp: gradient buffer has %d entries, want %d", len(grad), dims+2)
	}
	kinv := s.inverse()
	for p := range grad {
		grad[p] = 0
	}
	perDim := make([]float64, dims)
	for i := range n {
		// Diagonal: ∂K/∂log σ_f² = σ_f², ∂K/∂log σ_n² = σ_n², no
		// length-scale term (r = 0).
		w := float64(s.alpha[i]*s.alpha[i]) - kinv[i*n+i]
		grad[0] += float64(w * h.Amplitude)
		grad[dims+1] += float64(w * h.Noise)
		for j := i + 1; j < n; j++ {
			w := 2 * (float64(s.alpha[i]*s.alpha[j]) - kinv[i*n+j])
			m, a, e := matern52(sp.sqDist(d.x[i], d.x[j], h.LengthScales, perDim))
			grad[0] += float64(w * float64(h.Amplitude*m))
			// ∂K/∂log ℓ_g = σ_f²·(5/3)·D_g·(1 + a)·exp(−a).
			c := float64(h.Amplitude * float64(fiveThirds*float64((1+a)*e)))
			for g := range dims {
				grad[1+g] += float64(w * float64(c*perDim[g]))
			}
		}
	}
	for p := range grad {
		grad[p] *= 0.5
	}
	return s, nil
}

// forward solves L·z = b (running FMA-free subtraction, true division).
func (s *state) forward(b []float64) []float64 {
	n := s.n
	z := make([]float64, n)
	for i := range n {
		v := b[i]
		row := s.l[i*n:]
		for k := range i {
			v -= float64(row[k] * z[k])
		}
		z[i] = v / row[i]
	}
	return z
}

// backward solves Lᵀ·x = z.
func (s *state) backward(z []float64) []float64 {
	n := s.n
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		v := z[i]
		for k := i + 1; k < n; k++ {
			v -= float64(s.l[k*n+i] * x[k])
		}
		x[i] = v / s.l[i*n+i]
	}
	return x
}

// inverse is K⁻¹ = L⁻ᵀ·L⁻¹, dense row-major and symmetric.
func (s *state) inverse() []float64 {
	n := s.n
	// linv[k*n+j] = (L⁻¹)[k][j], lower triangular, column by column.
	linv := make([]float64, n*n)
	for j := range n {
		for i := j; i < n; i++ {
			var v float64
			if i == j {
				v = 1
			}
			for k := j; k < i; k++ {
				v -= float64(s.l[i*n+k] * linv[k*n+j])
			}
			linv[i*n+j] = v / s.l[i*n+i]
		}
	}
	out := make([]float64, n*n)
	for i := range n {
		for j := i; j < n; j++ {
			var v float64
			for k := j; k < n; k++ {
				v += float64(linv[k*n+i] * linv[k*n+j])
			}
			out[i*n+j], out[j*n+i] = v, v
		}
	}
	return out
}

// LogMarginalLikelihood is the log marginal likelihood of the
// standardised targets under h (and nugget > 0 on the diagonal), and its
// gradient with respect to θ = (log Amplitude, log ℓ_0 … log ℓ_{D−1},
// log Noise). x holds encoded rows (Space.Encode).
func LogMarginalLikelihood(space *Space, x [][]float64, y []float64, h Hyper, nugget float64) (float64, []float64, error) {
	d, err := prepare(space, x, y)
	if err != nil {
		return 0, nil, err
	}
	if err := h.validate(space.Dims()); err != nil {
		return 0, nil, err
	}
	if !positive(nugget) {
		return 0, nil, fmt.Errorf("gp: nugget %v is not a positive finite number", nugget)
	}
	grad := make([]float64, space.Dims()+2)
	s, err := evaluate(d, h, nugget, grad)
	if err != nil {
		return 0, nil, err
	}
	return s.lml, grad, nil
}

// Model is a GP conditioned on a training set at fixed hyperparameters.
// Immutable and safe for concurrent use.
type Model struct {
	d      *data
	hyper  Hyper
	nugget float64
	st     *state
}

// NewModel conditions the GP on x (encoded rows) and y at the fixed
// hyperparameters h, with nugget > 0 added to the training diagonal.
func NewModel(space *Space, x [][]float64, y []float64, h Hyper, nugget float64) (*Model, error) {
	d, err := prepare(space, x, y)
	if err != nil {
		return nil, err
	}
	if err := h.validate(space.Dims()); err != nil {
		return nil, err
	}
	if !positive(nugget) {
		return nil, fmt.Errorf("gp: nugget %v is not a positive finite number", nugget)
	}
	return newModel(d, h, nugget)
}

func newModel(d *data, h Hyper, nugget float64) (*Model, error) {
	s, err := evaluate(d, h, nugget, nil)
	if err != nil {
		return nil, err
	}
	return &Model{d: d, hyper: h.clone(), nugget: nugget, st: s}, nil
}

// Hyper is the model's hyperparameters (a copy).
func (m *Model) Hyper() Hyper { return m.hyper.clone() }

// LogMarginalLikelihood is the standardised targets' log marginal
// likelihood under the model.
func (m *Model) LogMarginalLikelihood() float64 { return m.st.lml }

// Ridge is the jitter CholeskyRidge added on top of Noise + Nugget to
// factor the training covariance: 0 unless duplicate (or near-duplicate)
// points made it numerically singular.
func (m *Model) Ridge() float64 { return m.st.ridge }

// N is the training-set size.
func (m *Model) N() int { return m.st.n }

// Predict is the posterior mean and LATENT variance (the noise-free
// function's, without Noise) at one encoded point, in the targets'
// original units. A variance that rounds below 0 is 0.
func (m *Model) Predict(x []float64) (mean, variance float64, err error) {
	sp := m.d.space
	if len(x) != sp.Cols() {
		return 0, 0, fmt.Errorf("gp: point has %d columns, the space encodes %d", len(x), sp.Cols())
	}
	for c, v := range x {
		if !finite(v) {
			return 0, 0, fmt.Errorf("gp: point column %d is not finite", c)
		}
	}
	n := m.st.n
	ks := make([]float64, n)
	var mu float64
	for i := range n {
		k, _, _ := matern52(sp.sqDist(x, m.d.x[i], m.hyper.LengthScales, nil))
		ks[i] = float64(m.hyper.Amplitude * k)
		mu += float64(ks[i] * m.st.alpha[i])
	}
	v := m.st.forward(ks)
	vv := m.hyper.Amplitude
	for _, e := range v {
		vv -= float64(e * e)
	}
	if vv < 0 {
		vv = 0
	}
	mean = float64(mu*m.d.yStd) + m.d.yMean
	variance = float64(vv * float64(m.d.yStd*m.d.yStd))
	return mean, variance, nil
}
