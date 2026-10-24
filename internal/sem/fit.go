package sem

import (
	"errors"
	"fmt"
	"math"

	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat/distuv"
)

// Solver selects the optimiser.
type Solver string

const (
	// SolverSLSQP is SciPy's SLSQP as semopy calls it (the default): the
	// same iterates, the same early stop at ftol, the same result.
	SolverSLSQP Solver = "slsqp"
	// SolverFisher is damped Fisher scoring (Levenberg–Marquardt on the
	// expected information): converges to the optimum to GradTol.
	SolverFisher Solver = "fisher"
)

// Information selects how the expected information
// tr(Σ⁻¹∂Σ_kΣ⁻¹∂Σ_l) and the gradient are computed. Both give the same
// figures up to rounding.
type Information string

const (
	// InformationDense forms every ∂Σ_k as a dense matrix, as semopy
	// does (the default).
	InformationDense Information = "dense"
	// InformationRank2 uses ∂Σ_k = Σ(a·bᵀ + b·aᵀ): every trace reduces
	// to dot products of Σ⁻¹-transformed vectors, O(k²·p) instead of
	// O(k·p³ + k²·p²).
	InformationRank2 Information = "rank2"
)

// Options configure the fit. The zero value is semopy's default setup.
type Options struct {
	// Solver is the optimiser (default SolverSLSQP).
	Solver Solver
	// Information is the derivative computation (default
	// InformationDense).
	Information Information
	// Ftol is SLSQP's ftol (default 1e-6, SciPy's and semopy's).
	Ftol float64
	// MaxIter caps the iterations (default 10000 for SLSQP — semopy's
	// setting — and 500 for Fisher scoring).
	MaxIter int
	// GradTol is Fisher scoring's stop: every free gradient entry below
	// it in absolute value (default 1e-10).
	GradTol float64
	// Level is the Wald interval's level (lavaan's level), in (0, 1);
	// 0 is 0.95. It sets ci_lower / ci_upper only (a lavaan-convention
	// fit's), never an estimate, SE or p.
	Level float64
}

// waldCrit is the Wald interval's critical value at o.Level: the
// standard normal (1 + level) / 2 quantile, the constant waldZ975 at
// the default (so a 0.95 interval keeps its bits).
func (o Options) waldCrit() float64 {
	if o.Level == 0 || o.Level == 0.95 {
		return waldZ975
	}
	return distuv.UnitNormal.Quantile((1 + o.Level) / 2)
}

func (o Options) withDefaults() Options {
	if o.Solver == "" {
		o.Solver = SolverSLSQP
	}
	if o.Information == "" {
		o.Information = InformationDense
	}
	if o.Ftol <= 0 {
		o.Ftol = 1e-6
	}
	if o.MaxIter <= 0 {
		if o.Solver == SolverSLSQP {
			o.MaxIter = 10000
		} else {
			o.MaxIter = 500
		}
	}
	if o.GradTol <= 0 {
		o.GradTol = 1e-10
	}
	return o
}

// Validate reports an unknown solver or information mode.
func (o Options) Validate() error {
	switch o.Solver {
	case "", SolverSLSQP, SolverFisher:
	default:
		return fmt.Errorf("unknown solver %q (slsqp | fisher)", o.Solver)
	}
	switch o.Information {
	case "", InformationDense, InformationRank2:
	default:
		return fmt.Errorf("unknown information %q (dense | rank2)", o.Information)
	}
	if o.Ftol < 0 || o.MaxIter < 0 || o.GradTol < 0 {
		return errors.New("ftol, max_iter and grad_tol must be positive")
	}
	if o.Level < 0 || o.Level >= 1 {
		return fmt.Errorf("interval level %v: need 0 < level < 1", o.Level)
	}
	return nil
}

// Estimate is one row of the parameter table, laid out like semopy's
// Model.inspect(std_est=True): lval op rval, the estimate, the
// standardized estimate, and (free parameters only) SE, z and p.
//
// An intercept / latent mean row has Op OpIntercept and an empty RVal.
// CILower / CIUpper are the Wald interval of a free parameter under
// lavaan conventions (Model.Lavaan), NaN otherwise.
type Estimate struct {
	LVal, Op, RVal   string
	Value            float64
	StdValue         float64
	Free             bool
	SE, Z, P         float64
	CILower, CIUpper float64
	Param            string
	// Label is the parameter's label in the description ("a*x"), ""
	// for an unlabelled one (whose Param is an auto name).
	Label string
}

// Key is "lval op rval" ("lval ~1" for an intercept row).
func (e Estimate) Key() string {
	if e.RVal == "" {
		return e.LVal + " " + e.Op
	}
	return e.LVal + " " + e.Op + " " + e.RVal
}

// waldZ975 is the standard normal 0.975 quantile (R's qnorm(0.975)):
// a 95% Wald interval is estimate ± waldZ975·SE.
const waldZ975 = 1.959963984540054

// Fit is a fitted model.
type Fit struct {
	Model      *Model
	N          float64
	Objective  float64 // the minimised Wishart ML discrepancy F
	Iterations int
	Converged  bool
	Status     string // the solver's exit message
	Options    Options
	Sample     *mat.SymDense // S over Model.Observed
	Implied    *mat.SymDense // Σ(θ̂)
	// SampleMean and ImpliedMean are ȳ and μ(θ̂) over Model.Observed
	// (Model.MeanStructure only; nil otherwise).
	SampleMean, ImpliedMean []float64
	Estimates               []Estimate
	// Defined are the := rows (defined.go), in description order, after
	// Estimates in every table; nil without a := line.
	Defined []Estimate
	Stats   Stats
	theta   []float64
	// params are the matrices at the estimate (Params).
	params *Params
}

// state holds the model matrices at a parameter vector.
type state struct {
	m          *Model
	free       []*parameter
	mats       [nMatrices]*mat.Dense
	s          *mat.SymDense
	raw        *mat.SymDense // the data's covariance the Lambda starts read
	sInvLogDet float64
	nObs, nIn  int
	starts     map[*parameter]float64
	info       Information
	// ybar is the sample mean over Observed (mean structure only).
	ybar []float64
}

func newState(m *Model, s *mat.SymDense) *state {
	st := &state{m: m, s: s, raw: s, nObs: len(m.Observed), nIn: len(m.Inner), starts: map[*parameter]float64{}}
	st.mats[mBeta] = mat.NewDense(st.nIn, st.nIn, nil)
	st.mats[mLambda] = mat.NewDense(st.nObs, st.nIn, nil)
	st.mats[mPsi] = mat.NewDense(st.nIn, st.nIn, nil)
	st.mats[mTheta] = mat.NewDense(st.nObs, st.nObs, nil)
	st.mats[mNu] = mat.NewDense(st.nObs, 1, nil)
	st.mats[mAlpha] = mat.NewDense(st.nIn, 1, nil)
	for v, j := range m.innerIndex {
		if i, ok := m.obsIndex[v]; ok {
			st.mats[mLambda].Set(i, j, 1)
		}
	}
	for _, p := range m.params {
		if p.active {
			st.free = append(st.free, p)
		}
	}
	return st
}

func (st *state) setParam(p *parameter, v float64) {
	for _, l := range p.locs {
		st.mats[l.mat].Set(l.i, l.j, v)
		if l.sym {
			st.mats[l.mat].Set(l.j, l.i, v)
		}
	}
}

// startValues applies semopy's starting-value rules to every parameter
// without an explicit start and returns the free vector.
func (st *state) startValues() []float64 {
	m := st.m
	for _, p := range m.params {
		v := p.start
		if !p.hasStart {
			v = st.startRule(p.locs[0])
		}
		st.starts[p] = v
		st.setParam(p, v)
	}
	if st.ybar != nil {
		st.meanStarts()
	}
	x := make([]float64, len(st.free))
	for k, p := range st.free {
		x[k] = st.starts[p]
	}
	return x
}

func (st *state) startRule(l location) float64 {
	m, s := st.m, st.s
	switch l.mat {
	case mBeta:
		return 0
	case mLambda:
		lval, rval := m.Observed[l.i], m.Inner[l.j]
		if !m.latent.has(rval) {
			return 0
		}
		first := rval
		for {
			if _, ok := m.obsIndex[first]; ok {
				break
			}
			next, ok := m.firstManif[first]
			if !ok || next == rval {
				return 0
			}
			first = next
		}
		// semopy's linregress slope over its mx_data: the data's
		// covariance, not a cov_nearest substitute (FitCovNearest).
		i, j := m.obsIndex[first], m.obsIndex[lval]
		return st.raw.At(i, j) / st.raw.At(i, i)
	case mPsi:
		lval, rval := m.Inner[l.i], m.Inner[l.j]
		if m.latent.has(lval) || m.latent.has(rval) {
			if l.i == l.j {
				return 0.05
			}
			return 0
		}
		i, j := m.obsIndex[lval], m.obsIndex[rval]
		if m.exogenous.has(lval) {
			return s.At(i, j)
		}
		if i == j {
			return s.At(i, j) / 2
		}
		return 0
	case mNu:
		return st.ybar[l.i]
	case mAlpha:
		// A latent mean starts at 0 (meanStarts moves a free one), an
		// observed inner intercept (or an observed exogenous mean,
		// fixed.x) at the sample mean.
		v := m.Inner[l.i]
		if m.latent.has(v) {
			return 0
		}
		return st.ybar[m.obsIndex[v]]
	default: // theta
		if l.i != l.j {
			return 0
		}
		return s.At(l.i, l.i) / 2
	}
}

// meanStarts refines the mean-structure starts so the start's implied
// means sit near the sample's: a free latent mean whose marker
// indicator (the firstManif chain) has a fixed intercept starts at the
// marker's sample mean less that intercept, then every free observed
// intercept (Nu) without an explicit start at ȳ − (Mα)ᵢ. The optimum
// does not depend on the starts; a start far from it can stall Fisher
// scoring in a near-zero latent variance.
func (st *state) meanStarts() {
	m := st.m
	fixedNu := map[int]float64{}
	for _, p := range m.params {
		for _, l := range p.locs {
			if l.mat == mNu && !p.active {
				fixedNu[l.i] = st.starts[p]
			}
		}
	}
	for _, p := range st.free {
		l := p.locs[0]
		if l.mat != mAlpha || p.hasStart || !m.latent.has(m.Inner[l.i]) {
			continue
		}
		first := m.Inner[l.i]
		for {
			if _, ok := m.obsIndex[first]; ok {
				break
			}
			next, ok := m.firstManif[first]
			if !ok || next == m.Inner[l.i] {
				first = ""
				break
			}
			first = next
		}
		i, ok := m.obsIndex[first]
		if first == "" || !ok {
			continue
		}
		if nu, fixed := fixedNu[i]; fixed {
			st.starts[p] = st.ybar[i] - nu
			st.setParam(p, st.starts[p])
		}
	}
	_, mm, _, err := st.sigma()
	if err != nil {
		return
	}
	for _, p := range st.free {
		l := p.locs[0]
		if l.mat != mNu || p.hasStart || len(p.locs) > 1 {
			continue
		}
		v := st.ybar[l.i]
		for j := 0; j < st.nIn; j++ {
			v -= mm.At(l.i, j) * st.mats[mAlpha].At(j, 0)
		}
		st.starts[p] = v
		st.setParam(p, v)
	}
}

// mu returns the implied mean μ = ν + Mα over Observed (M = ΛC).
func (st *state) mu(mm *mat.Dense) []float64 {
	out := make([]float64, st.nObs)
	for i := range out {
		v := st.mats[mNu].At(i, 0)
		for j := 0; j < st.nIn; j++ {
			v += mm.At(i, j) * st.mats[mAlpha].At(j, 0)
		}
		out[i] = v
	}
	return out
}

// meanResidual returns r = ȳ − μ (nil without a mean structure).
func (st *state) meanResidual(mm *mat.Dense) []float64 {
	if st.ybar == nil {
		return nil
	}
	mu := st.mu(mm)
	for i := range mu {
		mu[i] = st.ybar[i] - mu[i]
	}
	return mu
}

// meanGrad returns ∂μ/∂θ_k for every free parameter: Nu (i) e_i;
// Alpha (j) M[:,j]; Lambda (i,j) e_i·(Cα)_j; Beta (i,j) M[:,i]·(Cα)_j.
func (st *state) meanGrad(mm, c *mat.Dense) [][]float64 {
	ca := make([]float64, st.nIn)
	for i := range ca {
		for j := 0; j < st.nIn; j++ {
			ca[i] += c.At(i, j) * st.mats[mAlpha].At(j, 0)
		}
	}
	n := st.nObs
	out := make([][]float64, len(st.free))
	for k, par := range st.free {
		g := make([]float64, n)
		for _, l := range par.locs {
			switch l.mat {
			case mNu:
				g[l.i]++
			case mAlpha:
				for a := 0; a < n; a++ {
					g[a] += mm.At(a, l.i)
				}
			case mLambda:
				g[l.i] += ca[l.j]
			case mBeta:
				for a := 0; a < n; a++ {
					g[a] += mm.At(a, l.i) * ca[l.j]
				}
			}
		}
		out[k] = g
	}
	return out
}

func (st *state) update(x []float64) {
	for k, p := range st.free {
		st.setParam(p, x[k])
	}
}

// sigma returns Σ, M = ΛC and C = (I − B)⁻¹.
func (st *state) sigma() (*mat.SymDense, *mat.Dense, *mat.Dense, error) {
	ib := mat.NewDense(st.nIn, st.nIn, nil)
	for i := 0; i < st.nIn; i++ {
		ib.Set(i, i, 1)
	}
	ib.Sub(ib, st.mats[mBeta])
	var c mat.Dense
	if err := c.Inverse(ib); err != nil {
		return nil, nil, nil, err
	}
	var mm, mp, sg mat.Dense
	mm.Mul(st.mats[mLambda], &c)
	mp.Mul(&mm, st.mats[mPsi])
	sg.Mul(&mp, mm.T())
	sg.Add(&sg, st.mats[mTheta])
	return symmetrize(&sg), &mm, &c, nil
}

func symmetrize(d *mat.Dense) *mat.SymDense {
	n, _ := d.Dims()
	out := mat.NewSymDense(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			out.SetSym(i, j, (d.At(i, j)+d.At(j, i))/2)
		}
	}
	return out
}

// invLogDet inverts a symmetric PD matrix via Cholesky. Like semopy's
// chol_inv / chol_inv2 (dpotrf, then dpotri), only a failed
// factorization fails: gonum's InverseTo still fills the inverse when
// it reports an ill-conditioned matrix (mat.Condition), and a
// near-singular S that factors is fitted, as semopy fits it.
func invLogDet(a *mat.SymDense) (*mat.SymDense, float64, bool) {
	var ch mat.Cholesky
	if ok := ch.Factorize(a); !ok {
		return nil, 0, false
	}
	var inv mat.SymDense
	if err := ch.InverseTo(&inv); err != nil {
		if _, ill := err.(mat.Condition); !ill {
			return nil, 0, false
		}
	}
	return &inv, ch.LogDet(), true
}

// objective is semopy's obj_mlw: tr(SΣ⁻¹) − p + ln|Σ| − ln|S|, plus
// (ȳ − μ)ᵀΣ⁻¹(ȳ − μ) under a mean structure (twice lavaan's fmin).
func (st *state) objective(x []float64) float64 {
	st.update(x)
	sg, mm, _, err := st.sigma()
	if err != nil {
		return math.Inf(1)
	}
	inv, ld, ok := invLogDet(sg)
	if !ok {
		return math.Inf(1)
	}
	tr := 0.0
	for i := 0; i < st.nObs; i++ {
		for j := 0; j < st.nObs; j++ {
			tr += st.s.At(i, j) * inv.At(j, i)
		}
	}
	loss := tr - float64(st.nObs) + ld - st.sInvLogDet
	if r := st.meanResidual(mm); r != nil {
		loss += quad(inv, r, r)
	}
	if loss < 0 {
		return math.Inf(1)
	}
	return loss
}

// sigmaGrad returns ∂Σ/∂θ_k for every free parameter (semopy's
// calc_sigma_grad), each a dense nObs × nObs matrix.
func (st *state) sigmaGrad(mm, c *mat.Dense) []*mat.Dense {
	var p, d mat.Dense
	p.Mul(c, st.mats[mPsi]) // CΨ
	d.Mul(&p, mm.T())       // CΨCᵀΛᵀ
	n := st.nObs
	out := make([]*mat.Dense, len(st.free))
	for k, par := range st.free {
		g := mat.NewDense(n, n, nil)
		for _, l := range par.locs {
			switch l.mat {
			case mBeta:
				// M e_i (D row j) + transpose
				for a := 0; a < n; a++ {
					u := mm.At(a, l.i)
					if u == 0 {
						continue
					}
					for b := 0; b < n; b++ {
						v := u * d.At(l.j, b)
						g.Set(a, b, g.At(a, b)+v)
						g.Set(b, a, g.At(b, a)+v)
					}
				}
			case mLambda:
				for b := 0; b < n; b++ {
					v := d.At(l.j, b)
					g.Set(l.i, b, g.At(l.i, b)+v)
					g.Set(b, l.i, g.At(b, l.i)+v)
				}
			case mPsi:
				add := func(i, j int) {
					for a := 0; a < n; a++ {
						u := mm.At(a, i)
						if u == 0 {
							continue
						}
						for b := 0; b < n; b++ {
							g.Set(a, b, g.At(a, b)+u*mm.At(b, j))
						}
					}
				}
				add(l.i, l.j)
				if l.sym {
					add(l.j, l.i)
				}
			case mTheta:
				g.Set(l.i, l.j, g.At(l.i, l.j)+1)
				if l.sym {
					g.Set(l.j, l.i, g.At(l.j, l.i)+1)
				}
			}
		}
		out[k] = g
	}
	return out
}

// derivatives returns F, its gradient and (needH) the expected Hessian
// H_kl = tr(Σ⁻¹ ∂Σ_k Σ⁻¹ ∂Σ_l) at x.
func (st *state) derivatives(x []float64, needH bool) (float64, []float64, *mat.SymDense, error) {
	f := st.objective(x)
	if math.IsInf(f, 1) {
		return f, nil, nil, errors.New("implied covariance is not positive definite")
	}
	sg, mm, c, err := st.sigma()
	if err != nil {
		return 0, nil, nil, err
	}
	inv, _, ok := invLogDet(sg)
	if !ok {
		return 0, nil, nil, errors.New("implied covariance is not positive definite")
	}
	var w, t mat.Dense
	r := st.meanResidual(mm)
	if r == nil {
		t.Mul(inv, st.s)
	} else {
		// The mean term's covariance derivative: S → S + rrᵀ.
		sr := mat.NewDense(st.nObs, st.nObs, nil)
		for a := 0; a < st.nObs; a++ {
			for b := 0; b < st.nObs; b++ {
				sr.Set(a, b, st.s.At(a, b)+r[a]*r[b])
			}
		}
		t.Mul(inv, sr)
	}
	w.Mul(&t, inv)
	w.Sub(inv, &w) // Σ⁻¹ − Σ⁻¹SΣ⁻¹
	if st.info == InformationRank2 {
		g, h := st.rank2Derivatives(mm, c, inv, &w, needH)
		st.addMeanDerivatives(mm, c, inv, r, g, h)
		return f, g, h, nil
	}
	grads := st.sigmaGrad(mm, c)
	k, n := len(grads), st.nObs
	g := make([]float64, k)
	wd := w.RawMatrix().Data
	for i, dg := range grads {
		g[i] = floats.Dot(wd, dg.RawMatrix().Data) // tr(W∂Σ), both symmetric
	}
	if !needH {
		st.addMeanDerivatives(mm, c, inv, r, g, nil)
		return f, g, nil, nil
	}
	// tr(Σ⁻¹∂Σ_k Σ⁻¹∂Σ_l) = Σ_ab A_k[a,b]·B_l[a,b] with A = Σ⁻¹∂Σ and
	// B = ∂ΣΣ⁻¹ = Aᵀ (both factors symmetric): contiguous dot products.
	as := make([][]float64, k)
	bs := make([][]float64, k)
	for i, dg := range grads {
		var a mat.Dense
		a.Mul(inv, dg)
		ad := a.RawMatrix().Data
		bd := make([]float64, len(ad))
		for r := 0; r < n; r++ {
			for c := 0; c < n; c++ {
				bd[r*n+c] = ad[c*n+r]
			}
		}
		as[i], bs[i] = ad, bd
	}
	h := mat.NewSymDense(k, nil)
	for i := 0; i < k; i++ {
		for j := i; j < k; j++ {
			h.SetSym(i, j, floats.Dot(as[i], bs[j]))
		}
	}
	st.addMeanDerivatives(mm, c, inv, r, g, h)
	return f, g, h, nil
}

// addMeanDerivatives adds the mean term's share to the gradient,
// −2·∂μ_kᵀΣ⁻¹r, and (h non-nil) to the expected information,
// 2·∂μ_kᵀΣ⁻¹∂μ_l. A no-op without a mean structure (r nil).
func (st *state) addMeanDerivatives(mm, c *mat.Dense, inv *mat.SymDense, r, g []float64, h *mat.SymDense) {
	if r == nil {
		return
	}
	dm := st.meanGrad(mm, c)
	for k := range g {
		g[k] -= 2 * quad(inv, dm[k], r)
	}
	if h == nil {
		return
	}
	for i := range dm {
		for j := i; j < len(dm); j++ {
			h.SetSym(i, j, h.At(i, j)+2*quad(inv, dm[i], dm[j]))
		}
	}
}

// quad is aᵀKb.
func quad(k *mat.SymDense, a, b []float64) float64 {
	n := len(a)
	s := 0.0
	for i := 0; i < n; i++ {
		if a[i] == 0 {
			continue
		}
		row := 0.0
		for j := 0; j < n; j++ {
			row += k.At(i, j) * b[j]
		}
		s += a[i] * row
	}
	return s
}

// gradient is semopy's grad_mlw: +Inf everywhere when Σ is not PD.
func (st *state) gradient(x []float64) []float64 {
	_, g, _, err := st.derivatives(x, false)
	if err != nil {
		g = make([]float64, len(x))
		for i := range g {
			g[i] = math.Inf(1)
		}
	}
	return g
}

// minimize runs bound-constrained Fisher scoring with Levenberg–
// Marquardt damping (variances are bounded below by 0, like semopy's):
// each step solves (H + μ·diag H) Δ = −g over the parameters not pinned
// at a bound, μ shrinking after an accepted step and growing after a
// rejected one.
func (st *state) minimize(x []float64, opt Options) ([]float64, float64, int, bool, error) {
	lower := make([]float64, len(st.free))
	for k, p := range st.free {
		lower[k] = p.lower
	}
	return fisherMinimize(st, lower, x, opt)
}

// fisherProblem is what Fisher scoring minimises: one group's state, or
// a joint multi-group fit (jointState).
type fisherProblem interface {
	objective(x []float64) float64
	derivatives(x []float64, needH bool) (float64, []float64, *mat.SymDense, error)
}

// fisherMinimize is the damped Fisher scoring of minimize over any
// fisherProblem, lower the per-parameter bounds.
func fisherMinimize(st fisherProblem, lower, x []float64, opt Options) ([]float64, float64, int, bool, error) {
	f, g, h, err := st.derivatives(x, true)
	if err != nil {
		return x, f, 0, false, fmt.Errorf("at the starting values: %w", err)
	}
	freeSet := func() ([]int, float64) {
		var idx []int
		maxG := 0.0
		for k := range x {
			if x[k] <= lower[k] && g[k] > 0 {
				continue // pinned at its bound
			}
			idx = append(idx, k)
			maxG = math.Max(maxG, math.Abs(g[k]))
		}
		return idx, maxG
	}
	mu := 1e-3
	xn := make([]float64, len(x))
	iter := 0
	for ; iter < opt.MaxIter; iter++ {
		idx, maxG := freeSet()
		if maxG < opt.GradTol {
			return x, f, iter, true, nil
		}
		accepted := false
		var fn float64
		for tries := 0; tries < 60; tries++ {
			step := solveLM(h, g, idx, mu)
			if step == nil {
				mu = math.Max(mu*10, 1e-8)
				continue
			}
			copy(xn, x)
			for t, k := range idx {
				xn[k] = math.Max(lower[k], x[k]+step[t])
			}
			fn = st.objective(xn)
			if fn <= f {
				accepted = true
				mu = math.Max(mu/3, 0)
				if mu < 1e-12 {
					mu = 0
				}
				break
			}
			mu = math.Max(mu*4, 1e-8)
		}
		if !accepted {
			return x, f, iter, maxG < 1e-6, nil
		}
		small := f-fn <= 1e-16*(1+math.Abs(f))
		x, xn = xn, x
		f, g, h, err = st.derivatives(x, true)
		if err != nil {
			return x, f, iter + 1, false, err
		}
		if small {
			_, maxG := freeSet()
			return x, f, iter + 1, maxG < 1e-6, nil
		}
	}
	_, maxG := freeSet()
	return x, f, iter, maxG < opt.GradTol, nil
}

// solveLM solves (H_ff + μ·diag H_ff) Δ = −g_f over the index set; nil
// when the damped system is not positive definite.
func solveLM(h *mat.SymDense, g []float64, idx []int, mu float64) []float64 {
	n := len(idx)
	if n == 0 {
		return []float64{}
	}
	a := mat.NewSymDense(n, nil)
	rhs := mat.NewVecDense(n, nil)
	for p, i := range idx {
		rhs.SetVec(p, -g[i])
		for q := p; q < n; q++ {
			a.SetSym(p, q, h.At(i, idx[q]))
		}
		d := h.At(i, i)
		if d <= 0 {
			d = 1e-12
		}
		a.SetSym(p, p, h.At(i, i)+mu*d)
	}
	var ch mat.Cholesky
	if !ch.Factorize(a) {
		return nil
	}
	var out mat.VecDense
	if err := ch.SolveVecTo(&out, rhs); err != nil {
		return nil
	}
	return out.RawVector().Data
}

// FitCov fits the model to a sample covariance matrix S (over
// m.Observed, divisor N — semopy's biased covariance) from n rows.
func FitCov(m *Model, s *mat.SymDense, n float64, opt Options) (*Fit, error) {
	if m.MeanStructure {
		return nil, errors.New("a mean-structure model needs the sample means: use FitMoments")
	}
	return fitCov(m, s, s, nil, n, opt)
}

// FitCovNearest fits the model to adj, the CovNearest substitute for
// the data's covariance raw, as semopy fits it after load_cov's
// fallback: the objective, the Psi / Theta starts and every statistic
// read adj (semopy's mx_cov), while the Lambda starts are linregress
// slopes over the rows (mx_data), so they read raw.
func FitCovNearest(m *Model, adj, raw *mat.SymDense, n float64, opt Options) (*Fit, error) {
	if m.MeanStructure {
		return nil, errors.New("a mean-structure model needs the sample means: use FitMoments")
	}
	return fitCov(m, adj, raw, nil, n, opt)
}

// FitMoments fits the model to the sample moments: S (adjusted, as
// FitCovNearest's adj, or the data's), the data's covariance raw and
// the sample mean (over m.Observed; required under a mean structure,
// ignored otherwise). A covariance-only model fits exactly as
// FitCovNearest.
func FitMoments(m *Model, s, raw *mat.SymDense, mean []float64, n float64, opt Options) (*Fit, error) {
	if !m.MeanStructure {
		return fitCov(m, s, raw, nil, n, opt)
	}
	if len(mean) != len(m.Observed) {
		return nil, fmt.Errorf("sample mean has %d entries, the model has %d observed variables", len(mean), len(m.Observed))
	}
	return fitCov(m, s, raw, mean, n, opt)
}

func fitCov(m *Model, s, raw *mat.SymDense, mean []float64, n float64, opt Options) (*Fit, error) {
	if err := opt.Validate(); err != nil {
		return nil, err
	}
	if m.lavaan && opt.Solver == "" {
		// lavaan conventions converge to the optimum.
		opt.Solver = SolverFisher
	}
	opt = opt.withDefaults()
	if r, _ := s.Dims(); r != len(m.Observed) {
		return nil, fmt.Errorf("sample covariance is %d×%d, the model has %d observed variables", r, r, len(m.Observed))
	}
	st := newState(m, s)
	st.raw = raw
	st.info = opt.Information
	if m.MeanStructure {
		st.ybar = append([]float64(nil), mean...)
	}
	_, ld, ok := invLogDet(s)
	if !ok {
		return nil, errors.New("sample covariance matrix is not positive definite")
	}
	st.sInvLogDet = ld
	x0 := st.startValues()
	var (
		x      []float64
		f      float64
		iters  int
		conv   bool
		status string
	)
	switch opt.Solver {
	case SolverFisher:
		var err error
		x, f, iters, conv, err = st.minimize(x0, opt)
		if err != nil {
			return nil, err
		}
		status = "Fisher scoring stopped: gradient below grad_tol"
		if !conv {
			status = "Fisher scoring stopped before grad_tol"
		}
	default:
		xl := make([]float64, len(st.free))
		xu := make([]float64, len(st.free))
		for k, p := range st.free {
			xl[k], xu[k] = math.NaN(), math.NaN()
			if !math.IsInf(p.lower, -1) {
				xl[k] = p.lower
			}
		}
		res := slsqpMinimize(st.objective, st.gradient, x0, xl, xu, opt.Ftol, opt.MaxIter)
		x, f, iters, conv, status = res.x, res.fun, res.nit, res.mode == slsqpSuccess, slsqpMessages[res.mode]
	}
	st.update(x)
	sg, mm, c, err := st.sigma()
	if err != nil {
		return nil, err
	}
	fit := &Fit{Model: m, N: n, Objective: f, Iterations: iters, Converged: conv, Status: status, Options: opt, Sample: s, Implied: sg, theta: x}
	if m.MeanStructure {
		fit.SampleMean, fit.ImpliedMean = st.ybar, st.mu(mm)
	}
	fit.params = st.snapshot(st.ybar)
	se, vcov, err := st.standardErrors(x, n)
	if err != nil {
		return nil, err
	}
	fit.Estimates = st.estimates(x, se, sg, c, opt.waldCrit())
	if len(m.defined) > 0 {
		labels := map[string]labelValue{}
		st.addLabels(labels, fit.Estimates, func(k int) int { return k })
		fit.Defined = evalDefined(m.defined, labels, x, vcov, opt.waldCrit())
	}
	return fit, nil
}

// addLabels records the value of every labelled parameter of st not yet
// in labels: a free one by its index into the (joint) free vector
// (index maps st's free position k to it), a fixed one by its value;
// rows are st's estimates (the std_estimate a free label reads).
func (st *state) addLabels(labels map[string]labelValue, rows []Estimate, index func(k int) int) {
	freeIdx := map[*parameter]int{}
	for k, p := range st.free {
		freeIdx[p] = k
	}
	std := map[string]float64{}
	for _, e := range rows {
		if _, ok := std[e.Param]; !ok {
			std[e.Param] = e.StdValue
		}
	}
	for _, p := range st.m.params {
		if !p.user {
			continue
		}
		if _, ok := labels[p.name]; ok {
			continue
		}
		if k, ok := freeIdx[p]; ok {
			s, ok := std[p.name]
			if !ok {
				s = math.NaN()
			}
			labels[p.name] = labelValue{free: index(k), std: s}
			continue
		}
		v := st.starts[p]
		labels[p.name] = labelValue{free: -1, value: v, std: v}
	}
}

// standardErrors inverts the expected Fisher information
// (N/2)·tr(Σ⁻¹∂Σ_kΣ⁻¹∂Σ_l) — semopy's calc_se(information="expected").
// The inverse itself (the free parameters' covariance, a := line's
// delta method) is returned too when the model defines a parameter,
// nil otherwise.
func (st *state) standardErrors(x []float64, n float64) ([]float64, *mat.SymDense, error) {
	_, _, h, err := st.derivatives(x, true)
	if err != nil {
		return nil, nil, err
	}
	k := len(x)
	fim := mat.NewSymDense(k, nil)
	for i := 0; i < k; i++ {
		for j := i; j < k; j++ {
			fim.SetSym(i, j, n/2*h.At(i, j))
		}
	}
	diag, vcov := invertInformation(fim, len(st.m.defined) > 0)
	se := make([]float64, k)
	for i, v := range diag {
		switch {
		case st.m.lavaan:
			// lavaan takes the square root as is (NaN below 0).
		case v < 0 && v > -1e-1:
			v = 1e-12
		case v < 0:
			v = math.NaN()
		}
		se[i] = math.Sqrt(v)
	}
	return se, vcov, nil
}

// invertInformation returns the diagonal of the information's inverse
// (its Moore–Penrose inverse when it does not factor) and, when full,
// the whole inverse (nil otherwise).
func invertInformation(fim *mat.SymDense, full bool) ([]float64, *mat.SymDense) {
	k := fim.SymmetricDim()
	if inv, _, ok := invLogDet(fim); ok {
		diag := make([]float64, k)
		for i := range diag {
			diag[i] = inv.At(i, i)
		}
		if !full {
			inv = nil
		}
		return diag, inv
	}
	if !full {
		return pinvDiag(fim), nil
	}
	pinv := pinvFull(fim)
	diag := make([]float64, k)
	for i := range diag {
		diag[i] = pinv.At(i, i)
	}
	return diag, pinv
}

// pinvFull is the Moore–Penrose inverse of a symmetric matrix
// (pinvDiag's cut).
func pinvFull(a *mat.SymDense) *mat.SymDense {
	k := a.SymmetricDim()
	var svd mat.SVD
	svd.Factorize(a, mat.SVDFull)
	vals := svd.Values(nil)
	var u, v mat.Dense
	svd.UTo(&u)
	svd.VTo(&v)
	cut := 1e-15 * float64(k) * vals[0]
	out := mat.NewSymDense(k, nil)
	for i := 0; i < k; i++ {
		for j := i; j < k; j++ {
			s := 0.0
			for t, sv := range vals {
				if sv > cut {
					s += v.At(i, t) * u.At(j, t) / sv
				}
			}
			out.SetSym(i, j, s)
		}
	}
	return out
}

// pinvDiag returns the diagonal of the Moore–Penrose inverse of a
// symmetric matrix (numpy.linalg.pinv's default rcond).
func pinvDiag(a *mat.SymDense) []float64 {
	k := a.SymmetricDim()
	var svd mat.SVD
	svd.Factorize(a, mat.SVDFull)
	vals := svd.Values(nil)
	var u, v mat.Dense
	svd.UTo(&u)
	svd.VTo(&v)
	cut := 1e-15 * float64(k) * vals[0]
	out := make([]float64, k)
	for i := 0; i < k; i++ {
		s := 0.0
		for t, sv := range vals {
			if sv > cut {
				s += v.At(i, t) * u.At(i, t) / sv
			}
		}
		out[i] = s
	}
	return out
}

func (st *state) estimates(x, se []float64, sg *mat.SymDense, c *mat.Dense, crit float64) []Estimate {
	m := st.m
	// Standardization scales: latent/inner SDs from CΨCᵀ, observed SDs
	// from Σ (semopy inspect std_est=True).
	var cp, w mat.Dense
	cp.Mul(c, st.mats[mPsi])
	w.Mul(&cp, c.T())
	stds := make([]float64, st.nIn)
	for i := range stds {
		stds[i] = math.Sqrt(w.At(i, i))
	}
	full := make([]float64, st.nObs)
	for i := range full {
		full[i] = math.Sqrt(sg.At(i, i))
	}
	freeIdx := map[*parameter]int{}
	for k, p := range st.free {
		freeIdx[p] = k
	}
	var out []Estimate
	for mi := 0; mi < nMatrices; mi++ {
		for _, p := range m.params {
			for _, l := range p.locs {
				if l.mat != mi {
					continue
				}
				e := Estimate{Op: matOp[mi], Param: p.name, Label: p.userLabel(), SE: math.NaN(), Z: math.NaN(), P: math.NaN(),
					CILower: math.NaN(), CIUpper: math.NaN()}
				e.LVal, e.RVal = m.rowNames(l)
				_, isFree := freeIdx[p]
				if !isFree && m.hiddenRow(mi, e.LVal, e.RVal) {
					continue // semopy's inspect hides the fixed sample moments of observed exogenous variables
				}
				if k, ok := freeIdx[p]; ok {
					e.Free, e.Value = true, x[k]
					e.SE = se[k]
					if m.lavaan {
						e.Z = e.Value / e.SE
						e.CILower, e.CIUpper = e.Value-crit*e.SE, e.Value+crit*e.SE
					} else {
						e.Z = e.Value / (e.SE + 1e-12)
					}
					e.P = 2 * (1 - normCDF(math.Abs(e.Z)))
				} else {
					e.Value = st.starts[p]
				}
				switch mi {
				case mBeta:
					e.StdValue = e.Value / stds[l.i] * stds[l.j]
				case mLambda:
					e.StdValue = e.Value * stds[l.j] / full[l.i]
				case mPsi:
					e.StdValue = e.Value / stds[l.i] / stds[l.j]
					if m.lavaan && l.i != l.j {
						e.StdValue = e.Value / math.Sqrt(st.mats[mPsi].At(l.i, l.i)*st.mats[mPsi].At(l.j, l.j))
					}
				case mNu:
					e.StdValue = e.Value / full[l.i]
				case mAlpha:
					e.StdValue = e.Value / stds[l.i]
				default:
					e.StdValue = e.Value / full[l.i] / full[l.j]
					if m.lavaan && l.i != l.j {
						// lavaan's std.all of a covariance is the
						// residuals' correlation.
						e.StdValue = e.Value / math.Sqrt(st.mats[mTheta].At(l.i, l.i)*st.mats[mTheta].At(l.j, l.j))
					}
				}
				out = append(out, e)
			}
		}
	}
	return out
}

// userLabel is the parameter's description label, "" when unlabelled.
func (p *parameter) userLabel() string {
	if p.user {
		return p.name
	}
	return ""
}

func normCDF(z float64) float64 { return 0.5 * math.Erfc(-z/math.Sqrt2) }

// SpecifiedRows lists the parameter-table rows of an unfitted model,
// in Fit.Estimates' order, with every figure NaN: the rows an undefined
// input still reports.
func SpecifiedRows(m *Model) []Estimate {
	var out []Estimate
	for mi := 0; mi < nMatrices; mi++ {
		for _, p := range m.params {
			for _, l := range p.locs {
				if l.mat != mi {
					continue
				}
				lv, rv := m.rowNames(l)
				e := Estimate{Op: matOp[mi], Param: p.name, Label: p.userLabel(), LVal: lv, RVal: rv, Free: p.active,
					Value: math.NaN(), StdValue: math.NaN(), SE: math.NaN(), Z: math.NaN(), P: math.NaN(),
					CILower: math.NaN(), CIUpper: math.NaN()}
				if !p.active && m.hiddenRow(mi, e.LVal, e.RVal) {
					continue
				}
				out = append(out, e)
			}
		}
	}
	return out
}

// rowNames are a location's lval and rval (an intercept row's rval is
// empty).
func (m *Model) rowNames(l location) (string, string) {
	switch l.mat {
	case mBeta, mPsi:
		return m.Inner[l.i], m.Inner[l.j]
	case mLambda:
		return m.Observed[l.i], m.Inner[l.j]
	case mNu:
		return m.Observed[l.i], ""
	case mAlpha:
		return m.Inner[l.i], ""
	default:
		return m.Observed[l.i], m.Observed[l.j]
	}
}

// hiddenRow reports a fixed row the table hides: the sample moments of
// observed exogenous variables — their (co)variances, as semopy's
// inspect hides them, and their fixed.x means.
func (m *Model) hiddenRow(mi int, lval, rval string) bool {
	switch mi {
	case mPsi:
		return m.isObsExo(lval) && m.isObsExo(rval)
	case mAlpha, mNu:
		return m.isObsExo(lval)
	}
	return false
}
