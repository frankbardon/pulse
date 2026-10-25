package sem

import (
	"errors"
	"fmt"
	"math"

	"gonum.org/v1/gonum/mat"
)

// The ordinal estimators: lavaan's DWLS and WLSMV under the delta
// parameterization, single group, no observed covariates.
//
// Stage 1 is the two-step statistics (TwoStep: thresholds, numeric means
// and variances, polychoric / polyserial / Pearson correlations) and
// their asymptotic covariance Γ (Gamma: NACOV = N·WLS.W). Stage 2 fits
// the model to s = WLS.obs by diagonally weighted least squares,
//
//	F(θ) = Σ_k v_k (s_k − σ_k(θ))²,  v_k = 1 / Γ_kk  (lavaan's WLS.VD),
//
// σ(θ) lavaan's WLS.est: each ordered variable's thresholds τ − μ (μ
// its latent response's implied mean, 0 in a single group), a numeric
// variable's −μ, the numeric implied variances, then the implied
// covariances below the diagonal. An ordered variable's residual
// variance is not a parameter: it is 1 − (ΛCΨCᵀΛᵀ)ᵢᵢ, so its implied
// variance is 1 (never read by F). The free vector is the model's free
// parameters then the thresholds (lavaan's `x | tk` rows).
//
// Standard errors are lavaan's robust.sem sandwich (DWLS's default and
// WLSMV's): with Δ = ∂σ/∂θ, V = diag(v) and E = ΔᵀVΔ,
// vcov = E⁻¹(ΔᵀVΓVΔ)E⁻¹ / (N − 1). The test statistic is
// T = 2N·fmin = (N − 1)·F (fmin = ½(N−1)/N·F); WLSMV adds the
// scaled-shifted statistic a·T + b, a = √(df / tr((UΓ)²)),
// b = df − a·tr(UΓ), U = V − VΔE⁻¹ΔᵀV (lavaan's scaling factor 1/a,
// shift b). The baseline is the independence model — thresholds,
// means and variances free, every covariance 0 — whose estimate is
// the sample's: F_b = Σ_pairs v_k s_k², and its U keeps V on the
// covariances alone.

// Estimator is a MAT_SEM estimator: maximum likelihood (the default) or
// one of lavaan's ordinal estimators.
type Estimator string

const (
	// EstimatorML is maximum likelihood on the covariance (and means).
	EstimatorML Estimator = "ml"
	// EstimatorDWLS is lavaan's estimator = "DWLS": diagonally weighted
	// least squares on the two-step statistics, robust (sandwich)
	// standard errors, the standard test.
	EstimatorDWLS Estimator = "dwls"
	// EstimatorWLSMV is lavaan's estimator = "WLSMV": DWLS plus the
	// mean-and-variance adjusted (scaled-shifted) test.
	EstimatorWLSMV Estimator = "wlsmv"
)

// Ordinal reports whether e is an ordinal (two-step) estimator.
func (e Estimator) Ordinal() bool { return e == EstimatorDWLS || e == EstimatorWLSMV }

// OrdinalFit is an ordinal fit's stage-1 pieces.
type OrdinalFit struct {
	// Estimator is the fit's estimator.
	Estimator Estimator
	// Stats are the two-step statistics; Gamma their asymptotic
	// covariance.
	Stats *TwoStepStats
	Gamma *Gamma
	// Obs is WLS.obs; Est the model's WLS.est at the estimate; Weight
	// the diagonal weight 1/diag(NACOV).
	Obs, Est, Weight []float64
	// Delta is ∂WLS.est/∂θ at the estimate (rows Obs, columns the free
	// vector: the model's free parameters then the thresholds).
	Delta [][]float64
	// VCov is the free vector's robust covariance.
	VCov *mat.SymDense
	// TraceUG and TraceUG2 are tr(UΓ) and tr((UΓ)²) (WLSMV).
	TraceUG, TraceUG2 float64
	// FreeKeys are the free vector's row keys (Estimate.Key of each
	// parameter's first row; the thresholds "x | tk"; a grouped fit's
	// free scale factors "x ~*~ x").
	FreeKeys []string
	// Joint is a grouped fit's map from this group's free vector to the
	// joint one (GroupFit.VCov's coordinates; a shared parameter one
	// joint index); nil ungrouped, where Delta and VCov are the fit's
	// own.
	Joint []int
}

// thresholdRow is one threshold parameter: column v's k-th threshold.
type thresholdRow struct {
	v, k int
}

// wlsState is the DWLS problem (fisherProblem): the model's state plus
// the thresholds appended to its free vector.
type wlsState struct {
	st     *state
	layout ScoreLayout
	pairs  []PairEstimate
	kinds  []OrdinalKind
	obs, w []float64
	th     []thresholdRow
	// sc are the free scale factors (lavaan's `x ~*~ x`, the delta
	// parameterization's Δ), after the thresholds in the free vector,
	// each an ordered column index; every other column's Δ is 1. A
	// single-group fit frees none.
	sc     []int
	nModel int
}

// scales returns every column's Δ at x: 1, or its free scale factor.
func (ws *wlsState) scales(x []float64) []float64 {
	d := make([]float64, len(ws.kinds))
	for v := range d {
		d[v] = 1
	}
	at := ws.nModel + len(ws.th)
	for t, v := range ws.sc {
		d[v] = x[at+t]
	}
	return d
}

// est returns WLS.est at x (nil when Σ is undefined): lavaan's
// lav_lisrel_th and lav_lisrel_sigma under the delta parameterization —
// a threshold (τ − μ)·Δ, a covariance Δ_i·Δ_j·σ_ij.
func (ws *wlsState) est(x []float64) ([]float64, *mat.SymDense, *mat.Dense, *mat.Dense) {
	st := ws.st
	st.update(x[:ws.nModel])
	sg, mm, c, err := st.sigma()
	if err != nil {
		return nil, nil, nil, nil
	}
	mu := st.mu(mm)
	d := ws.scales(x)
	l := ws.layout
	out := make([]float64, l.Size)
	for t, tr := range ws.th {
		out[l.Th[tr.v]+tr.k] = (x[ws.nModel+t] - mu[tr.v]) * d[tr.v]
	}
	for v, kind := range ws.kinds {
		if kind == KindNumeric {
			out[l.Th[v]] = -mu[v]
			out[l.VarIdx[v]] = sg.At(v, v)
		}
	}
	for k, p := range ws.pairs {
		out[l.Cor+k] = d[p.I] * d[p.J] * sg.At(p.I, p.J)
	}
	return out, sg, mm, c
}

func (ws *wlsState) objective(x []float64) float64 {
	e, _, _, _ := ws.est(x)
	if e == nil {
		return math.Inf(1)
	}
	f := 0.0
	for k, o := range ws.obs {
		d := o - e[k]
		f += ws.w[k] * d * d
	}
	return f
}

// delta returns Δ = ∂WLS.est/∂x at x (Size × len(x)).
func (ws *wlsState) delta(x []float64) ([][]float64, error) {
	e, _, mm, c := ws.est(x)
	if e == nil {
		return nil, errors.New("the model's (I − B) does not invert")
	}
	st := ws.st
	l := ws.layout
	dS := st.sigmaGrad(mm, c)
	dM := st.meanGrad(mm, c)
	d := ws.scales(x)
	out := make([][]float64, l.Size)
	for i := range out {
		out[i] = make([]float64, len(x))
	}
	for k := 0; k < ws.nModel; k++ {
		for _, tr := range ws.th {
			out[l.Th[tr.v]+tr.k][k] = -dM[k][tr.v] * d[tr.v]
		}
		for v, kind := range ws.kinds {
			if kind == KindNumeric {
				out[l.Th[v]][k] = -dM[k][v]
				out[l.VarIdx[v]][k] = dS[k].At(v, v)
			}
		}
		for q, p := range ws.pairs {
			out[l.Cor+q][k] = d[p.I] * d[p.J] * dS[k].At(p.I, p.J)
		}
	}
	for t, tr := range ws.th {
		out[l.Th[tr.v]+tr.k][ws.nModel+t] = d[tr.v]
	}
	if len(ws.sc) == 0 {
		return out, nil
	}
	// A scale factor Δ_v: ∂/∂Δ_v of (τ − μ_v)·Δ_v is τ − μ_v; of
	// Δ_i·Δ_j·σ_ij the other side's Δ times σ_ij.
	mu := st.mu(mm)
	sg, _, _, _ := st.sigma()
	at := ws.nModel + len(ws.th)
	col := make(map[int]int, len(ws.sc))
	for t, v := range ws.sc {
		col[v] = at + t
	}
	for t, tr := range ws.th {
		if j, ok := col[tr.v]; ok {
			out[l.Th[tr.v]+tr.k][j] = x[ws.nModel+t] - mu[tr.v]
		}
	}
	for q, p := range ws.pairs {
		if j, ok := col[p.I]; ok {
			out[l.Cor+q][j] += d[p.J] * sg.At(p.I, p.J)
		}
		if j, ok := col[p.J]; ok {
			out[l.Cor+q][j] += d[p.I] * sg.At(p.I, p.J)
		}
	}
	return out, nil
}

// derivatives returns F, its gradient −2ΔᵀV(s − σ) and (needH) the
// Gauss–Newton Hessian 2ΔᵀVΔ.
func (ws *wlsState) derivatives(x []float64, needH bool) (float64, []float64, *mat.SymDense, error) {
	f := ws.objective(x)
	if math.IsInf(f, 1) {
		return f, nil, nil, errors.New("the model's (I − B) does not invert")
	}
	e, _, _, _ := ws.est(x)
	d, err := ws.delta(x)
	if err != nil {
		return f, nil, nil, err
	}
	n := len(x)
	g := make([]float64, n)
	for k, row := range d {
		r := ws.w[k] * (ws.obs[k] - e[k])
		for j, v := range row {
			g[j] -= 2 * r * v
		}
	}
	if !needH {
		return f, g, nil, nil
	}
	return f, g, weightedGram(d, ws.w, 2), nil
}

// weightedGram is s·DᵀVD for V = diag(w).
func weightedGram(d [][]float64, w []float64, s float64) *mat.SymDense {
	n := 0
	if len(d) > 0 {
		n = len(d[0])
	}
	h := mat.NewSymDense(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			v := 0.0
			for k, row := range d {
				v += row[i] * w[k] * row[j]
			}
			h.SetSym(i, j, s*v)
		}
	}
	return h
}

// FitOrdinal fits m (a model with ModelOptions.Ordered) by DWLS or WLSMV
// to in, the listwise-complete rows over m.Observed (an ordered column
// holds its category code 0..Levels-1, Kinds KindOrdered exactly on
// m's ordered variables; Weights frequency weights). A refused input is
// the two-step statistics' *OrdinalError.
func FitOrdinal(m *Model, in TwoStepInput, est Estimator, opt Options) (*Fit, error) {
	return fitOrdinal(m, in, est, opt, false)
}

// ordinalOptions validates an ordinal fit's estimator, model and
// options and returns the options with their defaults.
func ordinalOptions(m *Model, est Estimator, opt Options) (Options, error) {
	if !est.Ordinal() {
		return opt, fmt.Errorf("estimator %q is not an ordinal estimator", est)
	}
	if len(m.ordered) == 0 {
		return opt, errors.New("an ordinal fit needs ordered variables (ModelOptions.Ordered)")
	}
	if err := opt.Validate(); err != nil {
		return opt, err
	}
	if opt.Solver == "" {
		opt.Solver = SolverFisher
	}
	if opt.Solver != SolverFisher {
		return opt, fmt.Errorf("the ordinal estimators run Fisher scoring (solver %q)", SolverFisher)
	}
	return opt.withDefaults(), nil
}

// wlsPart is one group's DWLS problem and its stage-1 pieces: the
// two-step statistics, Γ (NACOV), the polychoric sample matrix S, the
// numeric means and the starting values (the model's, then the
// sample thresholds).
type wlsPart struct {
	ws    *wlsState
	stats *TwoStepStats
	gamma *Gamma
	nacov [][]float64
	s     *mat.SymDense
	mean  []float64
	x0    []float64
}

// newWLSPart computes group's (-1: ungrouped) two-step statistics and Γ
// from in and builds m's DWLS problem over them.
func newWLSPart(m *Model, in TwoStepInput, group int) (*wlsPart, error) {
	if len(in.Kinds) != len(m.Observed) {
		return nil, fmt.Errorf("the data have %d columns, the model %d observed variables", len(in.Kinds), len(m.Observed))
	}
	for i, v := range m.Observed {
		if (in.Kinds[i] == KindOrdered) != m.ordered.has(v) {
			return nil, fmt.Errorf("column %d (%s): its kind disagrees with the model's ordered variables", i, v)
		}
	}
	stats, err := twoStep(in, group)
	if err != nil {
		return nil, err
	}
	gamma, err := stats.Gamma(in)
	if err != nil {
		if oe, ok := err.(*OrdinalError); ok && group >= 0 {
			oe.Group = group
		}
		return nil, err
	}
	nacov := gamma.NACOV()
	obs := stats.WLSObs()
	l := stats.Layout()
	w := make([]float64, l.Size)
	for k := range w {
		w[k] = 1 / nacov[k][k]
	}
	p := len(m.Observed)
	s := mat.NewSymDense(p, nil)
	mean := make([]float64, p)
	for v := 0; v < p; v++ {
		s.SetSym(v, v, stats.Var[v])
		if stats.Kinds[v] == KindNumeric {
			mean[v] = stats.Mean[v]
		}
	}
	for k, pe := range stats.Pairs {
		s.SetSym(pe.I, pe.J, stats.pairCov(k, pe.Rho))
	}
	st := newState(m, s)
	st.info = InformationDense
	st.ybar = mean
	ws := &wlsState{st: st, layout: l, pairs: stats.Pairs, kinds: stats.Kinds, obs: obs, w: w}
	x0 := st.startValues()
	ws.nModel = len(x0)
	for v, kind := range stats.Kinds {
		if kind != KindOrdered {
			continue
		}
		for k, t := range stats.Thresholds[v] {
			ws.th = append(ws.th, thresholdRow{v: v, k: k})
			x0 = append(x0, t)
		}
	}
	return &wlsPart{ws: ws, stats: stats, gamma: gamma, nacov: nacov, s: s, mean: mean, x0: x0}, nil
}

// setDeltaResiduals sets every ordered variable's residual variance at
// x to the delta parameterization's remainder 1/Δ² − (ΛCΨCᵀΛᵀ)ᵢᵢ, so
// its latent response's implied variance is 1/Δ² (Δ·Σ·Δ has a unit
// diagonal), and returns Σ, the mean matrix and C there.
func (ws *wlsState) setDeltaResiduals(x []float64) (*mat.SymDense, *mat.Dense, *mat.Dense, error) {
	st := ws.st
	st.update(x[:ws.nModel])
	sg, _, _, err := st.sigma()
	if err != nil {
		return nil, nil, nil, err
	}
	d := ws.scales(x)
	for _, par := range st.m.params {
		if !par.delta {
			continue
		}
		i := par.locs[0].i
		v := 1/(d[i]*d[i]) - (sg.At(i, i) - st.mats[mTheta].At(i, i))
		st.starts[par] = v
		st.setParam(par, v)
	}
	return st.sigma()
}

// scaledImplied is Δ·Σ·Δ, the latent responses' implied covariance (Σ
// itself when every Δ is 1).
func (ws *wlsState) scaledImplied(x []float64, sg *mat.SymDense) *mat.SymDense {
	if len(ws.sc) == 0 {
		return sg
	}
	d := ws.scales(x)
	p := sg.SymmetricDim()
	out := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			out.SetSym(i, j, d[i]*d[j]*sg.At(i, j))
		}
	}
	return out
}

// thresholdRows are a fit's `x | tk` rows: value, robust SE (se[k] the
// free vector's), z, p and the Wald interval; std_estimate the value
// over the latent response's implied standard deviation.
func (ws *wlsState) thresholdRows(x, se []float64, sg *mat.SymDense, crit float64) []Estimate {
	m := ws.st.m
	out := make([]Estimate, 0, len(ws.th))
	for t, tr := range ws.th {
		k := ws.nModel + t
		val := x[k]
		e := Estimate{LVal: m.Observed[tr.v], Op: OpThreshold, RVal: fmt.Sprintf("t%d", tr.k+1),
			Param: fmt.Sprintf("_t%d", t+1), Free: true, Value: val, StdValue: val / math.Sqrt(sg.At(tr.v, tr.v)),
			SE: se[k], Z: val / se[k]}
		e.P = 2 * (1 - normCDF(math.Abs(e.Z)))
		e.CILower, e.CIUpper = val-crit*se[k], val+crit*se[k]
		out = append(out, e)
	}
	return out
}

// freeKeys are the free vector's row keys (Estimate.Key): the model's
// free parameters, the thresholds, then the free scale factors.
func (ws *wlsState) freeKeys() []string {
	m := ws.st.m
	var out []string
	for _, par := range ws.st.free {
		l := par.locs[0]
		lv, rv := m.rowNames(l)
		out = append(out, Estimate{LVal: lv, Op: matOp[l.mat], RVal: rv}.Key())
	}
	for _, tr := range ws.th {
		out = append(out, Estimate{LVal: m.Observed[tr.v], Op: OpThreshold, RVal: fmt.Sprintf("t%d", tr.k+1)}.Key())
	}
	for _, v := range ws.sc {
		out = append(out, Estimate{LVal: m.Observed[v], Op: OpScale, RVal: m.Observed[v]}.Key())
	}
	return out
}

// fitOrdinal is FitOrdinal; pointOnly (a bootstrap replicate) stops at
// the estimates — every SE, z, p and interval NaN, no statistics.
func fitOrdinal(m *Model, in TwoStepInput, est Estimator, opt Options, pointOnly bool) (*Fit, error) {
	opt, err := ordinalOptions(m, est, opt)
	if err != nil {
		return nil, err
	}
	part, err := newWLSPart(m, in, -1)
	if err != nil {
		return nil, err
	}
	ws, stats, gamma, nacov, s, mean := part.ws, part.stats, part.gamma, part.nacov, part.s, part.mean
	st, w, obs := ws.st, ws.w, ws.obs
	x0 := part.x0
	lower := make([]float64, len(x0))
	for k := range lower {
		lower[k] = math.Inf(-1)
	}
	x, f, iters, conv, err := fisherMinimize(ws, lower, x0, opt)
	if err != nil {
		return nil, err
	}
	status := "Fisher scoring stopped: gradient below grad_tol"
	if !conv {
		status = "Fisher scoring stopped before grad_tol"
	}
	e, _, _, _ := ws.est(x)
	d, err := ws.delta(x)
	if err != nil {
		return nil, err
	}
	// The ordered residual variances at the estimate: 1 − (ΛCΨCᵀΛᵀ)ᵢᵢ.
	sg, mm, c, err := ws.setDeltaResiduals(x)
	if err != nil {
		return nil, err
	}
	n := stats.N
	fit := &Fit{Model: m, N: n, Objective: (n - 1) / n * f, Iterations: iters, Converged: conv, Status: status,
		Options: opt, Sample: s, Implied: sg, theta: x, SampleMean: mean, ImpliedMean: st.mu(mm)}
	fit.params = st.snapshot(mean)
	crit := opt.waldCrit()
	if pointOnly {
		se := nanSlice(len(x))
		fit.Estimates = append(st.estimates(x[:ws.nModel], se[:ws.nModel], sg, c, crit), ws.thresholdRows(x, se, sg, crit)...)
		if len(m.defined) > 0 {
			labels := map[string]labelValue{}
			st.addLabels(labels, fit.Estimates, func(k int) int { return k })
			fit.Defined = evalDefined(m.defined, labels, x, mat.NewSymDense(len(x), nil), crit)
		}
		return fit, nil
	}
	info := weightedGram(d, w, 1)
	_, einv := invertInformation(info, true)
	hobs, err := ws.observedInformation(x, n)
	if err != nil {
		return nil, err
	}
	_, bread := invertInformation(hobs, true)
	meat := sandwichMeat(d, w, nacov)
	vcov := mat.NewSymDense(len(x), nil)
	{
		var t1, t2 mat.Dense
		t1.Mul(bread, meat)
		t2.Mul(&t1, bread)
		for i := 0; i < len(x); i++ {
			for j := i; j < len(x); j++ {
				vcov.SetSym(i, j, (t2.At(i, j)+t2.At(j, i))/2/(n-1))
			}
		}
	}
	se := make([]float64, len(x))
	for i := range se {
		se[i] = math.Sqrt(vcov.At(i, i))
	}
	fit.Estimates = append(st.estimates(x[:ws.nModel], se[:ws.nModel], sg, c, crit), ws.thresholdRows(x, se, sg, crit)...)
	if len(m.defined) > 0 {
		labels := map[string]labelValue{}
		st.addLabels(labels, fit.Estimates, func(k int) int { return k })
		fit.Defined = evalDefined(m.defined, labels, x, vcov, crit)
	}
	of := &OrdinalFit{Estimator: est, Stats: stats, Gamma: gamma, Obs: obs, Est: e, Weight: w, Delta: d, VCov: vcov,
		FreeKeys: ws.freeKeys()}
	fit.Ordinal = of
	fit.Stats = wlsStats(of, info, einv, nacov, len(x), n, f)
	return fit, nil
}

// nanSlice is n NaNs.
func nanSlice(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	return out
}

// observedInformation is lavaan's observed information of a DWLS fit
// (lav_model_hessian, the bread of robust.sem on categorical data:
// information.bread "observed"): the Jacobian of lavaan's gradient
// −(N−1)/N·ΔᵀV(s − σ) at x by Richardson extrapolation with step 1e-6,
// symmetrized. It differs from the expected ΔᵀVΔ by the residuals'
// curvature term.
func (ws *wlsState) observedInformation(x []float64, n float64) (*mat.SymDense, error) {
	out, err := richardsonInformation(x, func(at []float64) ([]float64, error) {
		_, g, _, err := ws.derivatives(at, false)
		if err != nil {
			return nil, err
		}
		for i := range g {
			g[i] *= (n - 1) / n / 2
		}
		return g, nil
	})
	ws.st.update(x[:ws.nModel])
	return out, err
}

// richardsonInformation is the Jacobian of grad at x by Richardson
// extrapolation with step 1e-6 (lav_model_hessian), symmetrized.
func richardsonInformation(x []float64, grad func([]float64) ([]float64, error)) (*mat.SymDense, error) {
	const h = 1e-6
	k := len(x)
	cols := make([][]float64, k)
	xp := make([]float64, k)
	for j := 0; j < k; j++ {
		var gs [4][]float64
		for s, step := range [4]float64{-2 * h, -h, h, 2 * h} {
			copy(xp, x)
			xp[j] = x[j] + step
			g, err := grad(xp)
			if err != nil {
				return nil, fmt.Errorf("the observed information: %w", err)
			}
			gs[s] = g
		}
		col := make([]float64, k)
		for i := range col {
			col[i] = (gs[0][i] - 8*gs[1][i] + 8*gs[2][i] - gs[3][i]) / (12 * h)
		}
		cols[j] = col
	}
	out := mat.NewSymDense(k, nil)
	for i := 0; i < k; i++ {
		for j := i; j < k; j++ {
			out.SetSym(i, j, (cols[j][i]+cols[i][j])/2)
		}
	}
	return out, nil
}

// sandwichMeat is ΔᵀVΓVΔ for V = diag(w).
func sandwichMeat(d [][]float64, w []float64, gamma [][]float64) *mat.SymDense {
	rows := len(d)
	n := 0
	if rows > 0 {
		n = len(d[0])
	}
	wd := mat.NewDense(rows, n, nil)
	for k, row := range d {
		for j, v := range row {
			wd.Set(k, j, w[k]*v)
		}
	}
	g := mat.NewDense(rows, rows, nil)
	for i := range gamma {
		for j := range gamma[i] {
			g.Set(i, j, gamma[i][j])
		}
	}
	var gw, out mat.Dense
	gw.Mul(g, wd)
	out.Mul(wd.T(), &gw)
	m := mat.NewSymDense(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			m.SetSym(i, j, (out.At(i, j)+out.At(j, i))/2)
		}
	}
	return m
}

// wlsStats are an ordinal fit's statistics (lavaan's DWLS / WLSMV fit
// measures). The ML-only figures (log-likelihood, AIC, BIC) are NaN.
func wlsStats(of *OrdinalFit, info, einv *mat.SymDense, nacov [][]float64, nParams int, n, f float64) Stats {
	size := len(of.Obs)
	npairs := len(of.Stats.Pairs)
	cor := size - npairs
	st := Stats{NParams: nParams, LogLik: math.NaN(), AIC: math.NaN(), BIC: math.NaN(), Chi2P: math.NaN()}
	st.DoF = float64(size - nParams)
	st.DoFBaseline = float64(npairs)
	st.Chi2 = (n - 1) * f
	fb := 0.0
	for k := cor; k < size; k++ {
		fb += of.Weight[k] * of.Obs[k] * of.Obs[k]
	}
	st.Chi2Baseline = (n - 1) * fb
	st.CFI, st.TLI, st.RMSEA = lavaanIndices(st.Chi2, st.DoF, st.Chi2Baseline, st.DoFBaseline, n, 1)
	st.NFI = lavaanNFI(st.Chi2, st.DoF, st.Chi2Baseline, st.DoFBaseline)
	t1, t2 := 0.0, 0.0
	for k, o := range of.Obs {
		dd := o - of.Est[k]
		t1 += dd * dd * of.Weight[k]
		t2 += o * o * of.Weight[k]
	}
	st.GFI = 1 - t1/t2
	st.AGFI = 1
	if st.DoF > 0 {
		st.AGFI = 1 - float64(size)/st.DoF*(1-st.GFI)
	}
	if of.Estimator != EstimatorWLSMV {
		return st
	}
	// U = V − VΔE⁻¹ΔᵀV, UΓ and its traces.
	d := of.Delta
	k := nParams
	w := of.Weight
	vd := mat.NewDense(size, k, nil)
	for i := 0; i < size; i++ {
		for j := 0; j < k; j++ {
			vd.Set(i, j, w[i]*d[i][j])
		}
	}
	var a, u mat.Dense
	a.Mul(vd, einv)
	u.Mul(&a, vd.T())
	g := mat.NewDense(size, size, nil)
	for i := range nacov {
		for j := range nacov[i] {
			g.Set(i, j, nacov[i][j])
		}
	}
	um := mat.NewDense(size, size, nil)
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			v := -u.At(i, j)
			if i == j {
				v += w[i]
			}
			um.Set(i, j, v)
		}
	}
	var ug mat.Dense
	ug.Mul(um, g)
	trUG, trUG2 := 0.0, 0.0
	for i := 0; i < size; i++ {
		trUG += ug.At(i, i)
		for j := 0; j < size; j++ {
			trUG2 += ug.At(i, j) * ug.At(j, i)
		}
	}
	of.TraceUG, of.TraceUG2 = trUG, trUG2
	sc := scaledShifted(st.Chi2, st.DoF, trUG, trUG2)
	// The baseline's U is V on the covariances (its Δ spans the
	// thresholds, means and variances).
	bTr, bTr2 := 0.0, 0.0
	for i := cor; i < size; i++ {
		bTr += w[i] * nacov[i][i]
		for j := cor; j < size; j++ {
			bTr2 += w[i] * nacov[i][j] * w[j] * nacov[j][i]
		}
	}
	bsc := scaledShifted(st.Chi2Baseline, st.DoFBaseline, bTr, bTr2)
	st.Scaled = &ScaledStats{
		Chi2: sc.stat, ScalingFactor: sc.scaling, ShiftParameter: sc.shift,
		Chi2Baseline: bsc.stat, BaselineScalingFactor: bsc.scaling,
	}
	st.Scaled.Chi2P = 1 - chiCDF(sc.stat, st.DoF)
	st.Scaled.CFI, st.Scaled.TLI, st.Scaled.RMSEA = lavaanIndices(sc.stat, st.DoF, bsc.stat, st.DoFBaseline, n, 1)
	return st
}

type scaledTest struct{ stat, scaling, shift float64 }

// scaledShifted is lav_test_sb's scaled.shifted: a = √(df / tr(UΓ)²),
// stat a·T + (df − a·tr(UΓ)), scaling factor 1/a.
func scaledShifted(t, df, tr, tr2 float64) scaledTest {
	a := math.Sqrt(df / tr2)
	if a < 0 || math.IsNaN(a) {
		a = math.NaN()
	}
	shift := df - a*tr
	return scaledTest{stat: t*a + shift, scaling: 1 / a, shift: shift}
}

// ScaledStats are WLSMV's scaled-shifted test and the indices it scales
// (lavaan's chisq.scaled, chisq.scaling.factor, shift.parameter,
// baseline.chisq.scaled, cfi.scaled, tli.scaled, rmsea.scaled).
type ScaledStats struct {
	Chi2, Chi2P                   float64
	ScalingFactor, ShiftParameter float64
	Chi2Baseline                  float64
	BaselineScalingFactor         float64
	CFI, TLI, RMSEA               float64
}
