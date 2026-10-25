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
	// parameter's first row; the thresholds "x | tk").
	FreeKeys []string
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
	nModel int
}

// est returns WLS.est at x (nil when Σ is undefined).
func (ws *wlsState) est(x []float64) ([]float64, *mat.SymDense, *mat.Dense, *mat.Dense) {
	st := ws.st
	st.update(x[:ws.nModel])
	sg, mm, c, err := st.sigma()
	if err != nil {
		return nil, nil, nil, nil
	}
	mu := st.mu(mm)
	l := ws.layout
	out := make([]float64, l.Size)
	for t, tr := range ws.th {
		out[l.Th[tr.v]+tr.k] = x[ws.nModel+t] - mu[tr.v]
	}
	for v, kind := range ws.kinds {
		if kind == KindNumeric {
			out[l.Th[v]] = -mu[v]
			out[l.VarIdx[v]] = sg.At(v, v)
		}
	}
	for k, p := range ws.pairs {
		out[l.Cor+k] = sg.At(p.I, p.J)
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
	out := make([][]float64, l.Size)
	for i := range out {
		out[i] = make([]float64, len(x))
	}
	for k := 0; k < ws.nModel; k++ {
		for _, tr := range ws.th {
			out[l.Th[tr.v]+tr.k][k] = -dM[k][tr.v]
		}
		for v, kind := range ws.kinds {
			if kind == KindNumeric {
				out[l.Th[v]][k] = -dM[k][v]
				out[l.VarIdx[v]][k] = dS[k].At(v, v)
			}
		}
		for q, p := range ws.pairs {
			out[l.Cor+q][k] = dS[k].At(p.I, p.J)
		}
	}
	for t, tr := range ws.th {
		out[l.Th[tr.v]+tr.k][ws.nModel+t] = 1
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
	if !est.Ordinal() {
		return nil, fmt.Errorf("estimator %q is not an ordinal estimator", est)
	}
	if len(m.ordered) == 0 {
		return nil, errors.New("an ordinal fit needs ordered variables (ModelOptions.Ordered)")
	}
	if len(in.Kinds) != len(m.Observed) {
		return nil, fmt.Errorf("the data have %d columns, the model %d observed variables", len(in.Kinds), len(m.Observed))
	}
	for i, v := range m.Observed {
		if (in.Kinds[i] == KindOrdered) != m.ordered.has(v) {
			return nil, fmt.Errorf("column %d (%s): its kind disagrees with the model's ordered variables", i, v)
		}
	}
	if err := opt.Validate(); err != nil {
		return nil, err
	}
	if opt.Solver == "" {
		opt.Solver = SolverFisher
	}
	if opt.Solver != SolverFisher {
		return nil, fmt.Errorf("the ordinal estimators run Fisher scoring (solver %q)", SolverFisher)
	}
	opt = opt.withDefaults()
	stats, err := TwoStep(in)
	if err != nil {
		return nil, err
	}
	gamma, err := stats.Gamma(in)
	if err != nil {
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
	sg, mm, c, err := st.sigma()
	if err != nil {
		return nil, err
	}
	for _, par := range m.params {
		if !par.delta {
			continue
		}
		i := par.locs[0].i
		v := 1 - (sg.At(i, i) - st.mats[mTheta].At(i, i))
		st.starts[par] = v
		st.setParam(par, v)
	}
	sg, mm, c, err = st.sigma()
	if err != nil {
		return nil, err
	}
	n := stats.N
	fit := &Fit{Model: m, N: n, Objective: (n - 1) / n * f, Iterations: iters, Converged: conv, Status: status,
		Options: opt, Sample: s, Implied: sg, theta: x, SampleMean: mean, ImpliedMean: st.mu(mm)}
	fit.params = st.snapshot(mean)
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
	crit := opt.waldCrit()
	fit.Estimates = st.estimates(x[:ws.nModel], se[:ws.nModel], sg, c, crit)
	for t, tr := range ws.th {
		k := ws.nModel + t
		val := x[k]
		e := Estimate{LVal: m.Observed[tr.v], Op: OpThreshold, RVal: fmt.Sprintf("t%d", tr.k+1),
			Param: fmt.Sprintf("_t%d", t+1), Free: true, Value: val, StdValue: val / math.Sqrt(sg.At(tr.v, tr.v)),
			SE: se[k], Z: val / se[k]}
		e.P = 2 * (1 - normCDF(math.Abs(e.Z)))
		e.CILower, e.CIUpper = val-crit*se[k], val+crit*se[k]
		fit.Estimates = append(fit.Estimates, e)
	}
	if len(m.defined) > 0 {
		labels := map[string]labelValue{}
		st.addLabels(labels, fit.Estimates, func(k int) int { return k })
		fit.Defined = evalDefined(m.defined, labels, x, vcov, crit)
	}
	of := &OrdinalFit{Estimator: est, Stats: stats, Gamma: gamma, Obs: obs, Est: e, Weight: w, Delta: d, VCov: vcov}
	for _, par := range st.free {
		l := par.locs[0]
		lv, rv := m.rowNames(l)
		of.FreeKeys = append(of.FreeKeys, Estimate{LVal: lv, Op: matOp[l.mat], RVal: rv}.Key())
	}
	for _, tr := range ws.th {
		of.FreeKeys = append(of.FreeKeys, Estimate{LVal: m.Observed[tr.v], Op: OpThreshold, RVal: fmt.Sprintf("t%d", tr.k+1)}.Key())
	}
	fit.Ordinal = of
	fit.Stats = wlsStats(of, info, einv, nacov, len(x), n, f)
	return fit, nil
}

// observedInformation is lavaan's observed information of a DWLS fit
// (lav_model_hessian, the bread of robust.sem on categorical data:
// information.bread "observed"): the Jacobian of lavaan's gradient
// −(N−1)/N·ΔᵀV(s − σ) at x by Richardson extrapolation with step 1e-6,
// symmetrized. It differs from the expected ΔᵀVΔ by the residuals'
// curvature term.
func (ws *wlsState) observedInformation(x []float64, n float64) (*mat.SymDense, error) {
	const h = 1e-6
	k := len(x)
	grad := func(at []float64) ([]float64, error) {
		_, g, _, err := ws.derivatives(at, false)
		if err != nil {
			return nil, err
		}
		for i := range g {
			g[i] *= (n - 1) / n / 2
		}
		return g, nil
	}
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
	ws.st.update(x[:ws.nModel])
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
