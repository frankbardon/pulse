package sem

import (
	"errors"
	"fmt"
	"math"

	"gonum.org/v1/gonum/mat"
)

// score.go is lavaan's lavPredict factor scores (type "lv") for an ML
// fit: each latent's score of a row y (over Model.Observed) is linear,
//
//	score = E(η_j) + w_jᵀ (y − μ),
//
// with μ the implied mean ν + ΛCα and E(η) = Cα (C = (I − B)⁻¹) under a
// mean structure — the scores then sit on the latent-mean scale — and,
// without one, μ the fit rows' sample mean and E(η) = 0: the scores are
// centered at the fit rows (lavaan's meanstructure = TRUE scores of the
// same covariance fit, which estimates the means saturated).
//
//   - regression (lavaan's default, Thurstone): w_j = Σ⁻¹ Cov(y, η_j),
//     Cov(η, y) = CΨMᵀ (M = ΛC) — E(η_j | y) under normality, so every
//     observed variable informs it, an outcome or covariate included,
//     whatever the matrices' representation.
//   - bartlett (lavaan's "Bartlett"): the GLS weights
//     (ΛᵀΣ⁻¹Λ)⁻¹ΛᵀΣ⁻¹ over lavaan's LISREL representation, in which an
//     observed variable of any `~` statement is a phantom latent (its
//     own unit column), so only the pure indicators measure a latent. A
//     latent without a direct indicator (a higher-order factor) takes
//     lavaan's fix: its column of Λ* = Λ(I − B*)⁻¹ — B* the regressions
//     of the latents that are indicators of a latent — fitted with the
//     latents it neither reaches nor is reached by.
//
// Params are the matrices a fit's scores read; a stored fit is its
// Params per group plus the description, so a scorer built from either
// gives the same bits (NewScorer is a pure function of the two).

// ScoreMethod is a factor-score method.
type ScoreMethod string

// The lavPredict methods.
const (
	ScoreRegression ScoreMethod = "regression"
	ScoreBartlett   ScoreMethod = "bartlett"
)

// ScoreMethods returns the methods, default first.
func ScoreMethods() []string { return []string{string(ScoreRegression), string(ScoreBartlett)} }

// Params are one group's parameter matrices at the estimate — Lambda
// (Observed × Inner), Beta and Psi (Inner × Inner), Theta (Observed ×
// Observed), Nu (Observed) and Alpha (Inner), the fixed values
// included — and the fit's sample mean over Observed (the center of a
// covariance-only fit's scores). Every slice is the fit's own copy.
type Params struct {
	Lambda, Beta, Psi, Theta [][]float64
	Nu, Alpha                []float64
	SampleMean               []float64
	// Thresholds are an ordinal fit's raw thresholds τ per Observed
	// variable (an ordered variable's Levels − 1, nil for a numeric
	// one); nil for an ML fit. Theta's ordered diagonal is then the
	// delta parameterization's residual 1/Δ² − common (EBM scores,
	// NewEBMScorer).
	Thresholds [][]float64
}

// snapshot copies st's matrices (the parameters as last set) and the
// sample mean ybar (nil without a mean structure — the caller's).
func (st *state) snapshot(sampleMean []float64) *Params {
	rows := func(d *mat.Dense) [][]float64 {
		r, c := d.Dims()
		out := make([][]float64, r)
		for i := range out {
			out[i] = make([]float64, c)
			for j := range out[i] {
				out[i][j] = d.At(i, j)
			}
		}
		return out
	}
	col := func(d *mat.Dense) []float64 {
		r, _ := d.Dims()
		out := make([]float64, r)
		for i := range out {
			out[i] = d.At(i, 0)
		}
		return out
	}
	return &Params{
		Lambda: rows(st.mats[mLambda]), Beta: rows(st.mats[mBeta]),
		Psi: rows(st.mats[mPsi]), Theta: rows(st.mats[mTheta]),
		Nu: col(st.mats[mNu]), Alpha: col(st.mats[mAlpha]),
		SampleMean: append([]float64(nil), sampleMean...),
	}
}

// copyParams is a deep copy of p.
func copyParams(p *Params) Params {
	cp := func(m [][]float64) [][]float64 {
		out := make([][]float64, len(m))
		for i := range m {
			out[i] = append([]float64(nil), m[i]...)
		}
		return out
	}
	out := Params{Lambda: cp(p.Lambda), Beta: cp(p.Beta), Psi: cp(p.Psi), Theta: cp(p.Theta),
		Nu: append([]float64(nil), p.Nu...), Alpha: append([]float64(nil), p.Alpha...),
		SampleMean: append([]float64(nil), p.SampleMean...)}
	if p.Thresholds != nil {
		out.Thresholds = make([][]float64, len(p.Thresholds))
		for i, t := range p.Thresholds {
			if t != nil {
				out.Thresholds[i] = append([]float64(nil), t...)
			}
		}
	}
	return out
}

// Params returns the fit's parameter matrices (a copy); false when the
// fit carries none (a fit not produced by this package's fitters).
// SampleMean is the fit's under a mean structure and nil otherwise — a
// covariance-only fit never reads the means, so a scorer's caller sets
// the fit rows' mean.
func (f *Fit) Params() (Params, bool) {
	if f == nil || f.params == nil {
		return Params{}, false
	}
	return copyParams(f.params), true
}

// Latents returns the model's latent variables in Inner order.
func (m *Model) Latents() []string {
	var out []string
	for _, v := range m.Inner {
		if m.latent.has(v) {
			out = append(out, v)
		}
	}
	return out
}

// ErrScoreSingular reports a fit whose scores are undefined: the
// implied covariance (or Bartlett's ΛᵀΣ⁻¹Λ) cannot be inverted.
var ErrScoreSingular = errors.New("the fit's implied covariance is singular; factor scores need its inverse")

// Scorer is one latent's linear scoring rule in one group:
// score = Base + Σ_i Weights[i]·(y_i − Center[i]), y over Observed.
type Scorer struct {
	Weights []float64
	Center  []float64
	Base    float64
}

// Score is the row's score (y over Observed, every value present).
func (s *Scorer) Score(y []float64) float64 {
	sum := 0.0
	for i, w := range s.Weights {
		sum += float64(w * (y[i] - s.Center[i]))
	}
	return s.Base + sum
}

// NewScorer builds latent's scoring rule under method from one group's
// parameter matrices p of model m (the group's own model under a
// multi-group fit). ErrScoreSingular when the scores are undefined.
func NewScorer(m *Model, p Params, latent string, method ScoreMethod) (*Scorer, error) {
	nObs, nIn := len(m.Observed), len(m.Inner)
	j, ok := m.innerIndex[latent]
	if !ok || !m.latent.has(latent) {
		return nil, fmt.Errorf("%q is not a latent variable of the model", latent)
	}
	if err := p.check(nObs, nIn); err != nil {
		return nil, err
	}
	lambda := dense(p.Lambda)
	ib := mat.NewDense(nIn, nIn, nil)
	for i := 0; i < nIn; i++ {
		ib.Set(i, i, 1)
	}
	ib.Sub(ib, dense(p.Beta))
	var c mat.Dense
	if !inverted(c.Inverse(ib)) {
		return nil, ErrScoreSingular
	}
	var mm, mp, sg mat.Dense
	mm.Mul(lambda, &c)
	mp.Mul(&mm, dense(p.Psi))
	sg.Mul(&mp, mm.T())
	sg.Add(&sg, dense(p.Theta))
	sigma := symmetrize(&sg)
	var ch mat.Cholesky
	if !ch.Factorize(sigma) {
		return nil, ErrScoreSingular
	}
	var sinv mat.SymDense
	if err := ch.InverseTo(&sinv); err != nil {
		if _, ill := err.(mat.Condition); !ill {
			return nil, ErrScoreSingular
		}
	}
	s := &Scorer{Center: make([]float64, nObs)}
	if m.MeanStructure {
		alpha := mat.NewVecDense(nIn, append([]float64(nil), p.Alpha...))
		var ca, mu mat.VecDense
		ca.MulVec(&c, alpha)
		mu.MulVec(&mm, alpha)
		for i := range s.Center {
			s.Center[i] = p.Nu[i] + mu.AtVec(i)
		}
		s.Base = ca.AtVec(j)
	} else {
		copy(s.Center, p.SampleMean)
	}
	switch method {
	case ScoreRegression:
		// Cov(y, η_j) = M Ψ Cᵀ e_j, then w = Σ⁻¹ Cov(y, η_j).
		var cpsi mat.Dense
		cpsi.Mul(&c, dense(p.Psi))
		cov := make([]float64, nObs)
		for i := 0; i < nObs; i++ {
			v := 0.0
			for k := 0; k < nIn; k++ {
				v += mm.At(i, k) * cpsi.At(j, k)
			}
			cov[i] = v
		}
		s.Weights = symMulVec(&sinv, cov)
	case ScoreBartlett:
		w, err := m.bartlettWeights(p, lambda, &sinv, latent)
		if err != nil {
			return nil, err
		}
		s.Weights = w
	default:
		return nil, fmt.Errorf("unknown score method %q (regression | bartlett)", method)
	}
	for _, w := range s.Weights {
		if math.IsNaN(w) || math.IsInf(w, 0) {
			return nil, ErrScoreSingular
		}
	}
	return s, nil
}

// check refuses matrices whose shapes are not the model's.
func (p Params) check(nObs, nIn int) error {
	shape := func(name string, m [][]float64, r, c int) error {
		if len(m) != r {
			return fmt.Errorf("%s has %d rows, the model needs %d", name, len(m), r)
		}
		for _, row := range m {
			if len(row) != c {
				return fmt.Errorf("%s has a row of %d entries, the model needs %d", name, len(row), c)
			}
		}
		return nil
	}
	for _, e := range []error{shape("lambda", p.Lambda, nObs, nIn), shape("beta", p.Beta, nIn, nIn),
		shape("psi", p.Psi, nIn, nIn), shape("theta", p.Theta, nObs, nObs)} {
		if e != nil {
			return e
		}
	}
	if len(p.Nu) != nObs || len(p.Alpha) != nIn || len(p.SampleMean) != nObs {
		return fmt.Errorf("nu, alpha and the sample mean need %d, %d and %d entries", nObs, nIn, nObs)
	}
	return nil
}

// inverted reports a gonum Inverse that produced its result: no error,
// or only an ill-conditioning note (the inverse is still filled).
func inverted(err error) bool {
	if err == nil {
		return true
	}
	_, ill := err.(mat.Condition)
	return ill
}

func dense(rows [][]float64) *mat.Dense {
	r := len(rows)
	c := 0
	if r > 0 {
		c = len(rows[0])
	}
	if r == 0 || c == 0 {
		// gonum refuses a zero dimension: a 1×1 zero stands in (never
		// read — no model has an empty Observed or Inner).
		return mat.NewDense(max(r, 1), max(c, 1), nil)
	}
	d := mat.NewDense(r, c, nil)
	for i, row := range rows {
		d.SetRow(i, row)
	}
	return d
}

func symMulVec(a *mat.SymDense, x []float64) []float64 {
	n := a.SymmetricDim()
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		v := 0.0
		for k := 0; k < n; k++ {
			v += a.At(i, k) * x[k]
		}
		out[i] = v
	}
	return out
}

// latentIndicators reports the latents that are indicators of another
// latent (lavaan's lv.ind): the rows of a loading in Beta.
func (m *Model) latentIndicators() stringSet {
	out := stringSet{}
	for _, p := range m.params {
		for _, l := range p.locs {
			if l.mat == mBeta && l.kind == kindLoading && m.latent.has(m.Inner[l.i]) {
				out.add(m.Inner[l.i])
			}
		}
	}
	return out
}

// bartlettWeights is lavaan's Bartlett row for latent
// (lav_predict_eta_normal, method "Bartlett", with its higher-order
// fix) over lavaan's LISREL Λ (lav_lisrel_lambda with its dummy
// latents handled), written over Inner: an output's row is its Lambda
// row (an observed outcome's regressions included, lavaan's ov.y dummy
// row), an exogenous observed inner variable's its unit column, and an
// endogenous one's (a mediator, or an indicator with an outgoing arrow)
// its Beta row. A column without an entry drops out of the GLS
// (lavaan's ginv of the singular ΛᵀΣ⁻¹Λ).
func (m *Model) bartlettWeights(p Params, lambda *mat.Dense, sinv *mat.SymDense, latent string) ([]float64, error) {
	nObs, nIn := len(m.Observed), len(m.Inner)
	lam := mat.DenseCopyOf(lambda)
	for i, y := range m.Observed {
		k, inner := m.innerIndex[y]
		if !inner || m.exogenous.has(y) {
			continue
		}
		lam.SetRow(i, p.Beta[k])
	}
	j := m.innerIndex[latent]
	empty := func(d *mat.Dense, col int) bool {
		for i := 0; i < nObs; i++ {
			if d.At(i, col) != 0 {
				return false
			}
		}
		return true
	}
	if !empty(lam, j) {
		return glsRow(lam, nonEmpty(lam, nIn, empty), sinv, j)
	}
	// A latent without a direct indicator: zero weights (lavaan's ginv
	// row) unless the higher-order fix applies.
	zero := make([]float64, nObs)
	ind := m.latentIndicators()
	if len(ind) == 0 {
		return zero, nil
	}
	bstar := mat.NewDense(nIn, nIn, nil)
	for a, v := range m.Inner {
		if ind.has(v) {
			bstar.SetRow(a, p.Beta[a])
		}
	}
	ibs := mat.NewDense(nIn, nIn, nil)
	for a := 0; a < nIn; a++ {
		ibs.Set(a, a, 1)
	}
	ibs.Sub(ibs, bstar)
	var ibInv mat.Dense
	if !inverted(ibInv.Inverse(ibs)) {
		return nil, ErrScoreSingular
	}
	var lstar mat.Dense
	lstar.Mul(lam, &ibInv)
	if empty(&lstar, j) {
		return zero, nil
	}
	reach := func(a, b int) bool { return a != b && math.Abs(ibInv.At(a, b)) > 1e-12 }
	var allowed []int
	for k := 0; k < nIn; k++ {
		if !reach(j, k) && !reach(k, j) {
			allowed = append(allowed, k)
		}
	}
	var tcols []int
	for _, k := range allowed {
		reaches := false
		for _, l := range allowed {
			if reach(k, l) {
				reaches = true
				break
			}
		}
		if !reaches && !empty(&lstar, k) {
			tcols = append(tcols, k)
		}
	}
	return glsRow(&lstar, tcols, sinv, j)
}

// nonEmpty lists the columns of d (n of them) with an entry.
func nonEmpty(d *mat.Dense, n int, empty func(*mat.Dense, int) bool) []int {
	var cols []int
	for a := 0; a < n; a++ {
		if !empty(d, a) {
			cols = append(cols, a)
		}
	}
	return cols
}

// glsRow is row j of (LᵀΣ⁻¹L)⁻¹LᵀΣ⁻¹ over L's columns cols (j among
// them).
func glsRow(l *mat.Dense, cols []int, sinv *mat.SymDense, j int) ([]float64, error) {
	nObs, _ := l.Dims()
	k := len(cols)
	sub := mat.NewDense(nObs, k, nil)
	row := -1
	for a, c := range cols {
		if c == j {
			row = a
		}
		for i := 0; i < nObs; i++ {
			sub.Set(i, a, l.At(i, c))
		}
	}
	if row < 0 {
		return nil, ErrScoreSingular
	}
	var ls mat.Dense // Lᵀ Σ⁻¹ (k × nObs)
	ls.Mul(sub.T(), sinv)
	var t mat.Dense
	t.Mul(&ls, sub)
	var tinv mat.Dense
	if !inverted(tinv.Inverse(&t)) {
		return nil, ErrScoreSingular
	}
	out := make([]float64, nObs)
	for i := 0; i < nObs; i++ {
		v := 0.0
		for a := 0; a < k; a++ {
			v += tinv.At(row, a) * ls.At(a, i)
		}
		out[i] = v
	}
	return out, nil
}
