package sem

import (
	"errors"
	"math"

	"gonum.org/v1/gonum/mat"

	"gonum.org/v1/gonum/stat/distuv"
)

// Stats are semopy.calc_stats' figures, with semopy's definitions —
// including its conventions: LogLik is the minimised discrepancy F
// (semopy's calc_likelihood returns obj_mlw), AIC = 2(k − F),
// BIC = ln(N)·k − 2F, and GFI equals NFI.
type Stats struct {
	DoF          float64
	DoFBaseline  float64
	Chi2         float64
	Chi2P        float64
	Chi2Baseline float64
	CFI, GFI     float64
	AGFI, NFI    float64
	TLI, RMSEA   float64
	AIC, BIC     float64
	LogLik       float64
	NParams      int
}

// ComputeStats fills fit.Stats; desc is the model description, used to
// fit semopy's baseline (independence) model on the same covariance.
// A lavaan-convention fit (Model.Lavaan) gets lavaan's figures instead
// (lavaanStats); desc is then unused.
func ComputeStats(fit *Fit, desc string) error {
	if fit.Model.lavaan {
		return lavaanStats(fit)
	}
	opt := fit.Options
	base, err := NewModel(desc, true)
	if err != nil {
		return err
	}
	// semopy fits its baseline with the solver defaults whatever the
	// caller's options (stats.__get_chi2_base calls fit(obj=...)).
	bopt := opt
	if bopt.Solver == "" || bopt.Solver == SolverSLSQP {
		bopt = Options{Solver: SolverSLSQP, Information: opt.Information}
	}
	bfit, err := FitCov(base, fit.Sample, fit.N, bopt)
	if err != nil {
		return err
	}
	p := float64(len(fit.Model.Observed))
	k := float64(len(fit.theta))
	moments := p * (p + 1) / 2
	st := Stats{NParams: len(fit.theta)}
	st.DoF = moments - k
	st.DoFBaseline = moments - float64(len(bfit.theta))
	st.Chi2 = fit.N * fit.Objective
	st.Chi2P = 1 - chiCDF(st.Chi2, st.DoF)
	st.Chi2Baseline = fit.N * bfit.Objective
	st.LogLik = fit.Objective
	st.AIC = 2 * (k - st.LogLik)
	st.BIC = math.Log(fit.N)*k - 2*st.LogLik
	if st.Chi2 < st.DoF {
		st.RMSEA = 0
	} else {
		st.RMSEA = math.Sqrt((st.Chi2/st.DoF - 1) / (fit.N - 1))
	}
	st.CFI = 1 - (st.Chi2-st.DoF)/(st.Chi2Baseline-st.DoFBaseline)
	st.GFI = 1 - st.Chi2/st.Chi2Baseline
	if st.DoF == 0 {
		st.AGFI = math.NaN()
	} else {
		st.AGFI = 1 - st.DoFBaseline/st.DoF*(1-st.GFI)
	}
	if st.Chi2Baseline == 0 {
		st.NFI = math.NaN()
	} else {
		st.NFI = (st.Chi2Baseline - st.Chi2) / st.Chi2Baseline
	}
	if st.DoF == 0 || st.DoFBaseline == 0 {
		st.TLI = math.NaN()
	} else {
		a, b := st.Chi2/st.DoF, st.Chi2Baseline/st.DoFBaseline
		st.TLI = (b - a) / (b - 1)
	}
	fit.Stats = st
	return nil
}

func chiCDF(x, dof float64) float64 {
	if dof <= 0 {
		return math.NaN()
	}
	return distuv.ChiSquared{K: dof}.CDF(x)
}

// lavaanStats fills fit.Stats with lavaan's (0.7) ML figures, under
// fixed.x: the observed exogenous variables' moments are the sample's
// and leave the counted statistics.
//
//   - DoF = ndat − k, ndat = p(p+1)/2 (+ p under a mean structure)
//     less the exogenous block's nx(nx+1)/2 (+ nx);
//   - Chi2 = N·F (F the minimised objective, twice lavaan's fmin);
//   - the baseline is the independence model in closed form —
//     variances (and means) free, covariances 0, the exogenous block
//     fixed: F_b = ln|S_xx| + Σ ln S_yy − ln|S|;
//   - CFI, TLI, NFI, RMSEA (divisor N) as lav_fit_cfi / _tli / _nfi /
//     _rmsea; GFI / AGFI are lavaan's LISREL pair gfi_lisrel /
//     agfi_lisrel (lavaan's own "agfi"), the normal-theory weight at
//     Σ(θ̂) with the exogenous block's rows zeroed;
//   - LogLik the h0 log-likelihood (less the exogenous block's
//     saturated marginal, as lavaan's fixed.x logl), AIC = −2LL + 2k, BIC = −2LL + k·ln N.
func lavaanStats(fit *Fit) error {
	m := fit.Model
	p := len(m.Observed)
	pf, k, n := float64(p), float64(len(fit.theta)), fit.N
	var xIdx, yIdx []int
	for i, v := range m.Observed {
		if m.isObsExo(v) {
			xIdx = append(xIdx, i)
		} else {
			yIdx = append(yIdx, i)
		}
	}
	nx, ny := float64(len(xIdx)), float64(len(yIdx))
	ndat := pf * (pf + 1) / 2
	nel := ndat
	xMoments := nx * (nx + 1) / 2
	baseParams := ny
	if m.MeanStructure {
		ndat += pf
		nel += pf
		xMoments += nx
		baseParams += ny
	}
	ndat -= xMoments
	st := Stats{NParams: len(fit.theta)}
	st.DoF = ndat - k
	st.DoFBaseline = ndat - baseParams
	st.Chi2 = n * fit.Objective
	st.Chi2P = 1 - chiCDF(st.Chi2, st.DoF)

	s := fit.Sample
	_, ldS, ok := invLogDet(s)
	if !ok {
		return errLavaanStats
	}
	fb := -ldS
	ldSxx := 0.0
	for _, i := range yIdx {
		fb += math.Log(s.At(i, i))
	}
	if len(xIdx) > 0 {
		sxx := mat.NewSymDense(len(xIdx), nil)
		for a, i := range xIdx {
			for b := a; b < len(xIdx); b++ {
				sxx.SetSym(a, b, s.At(i, xIdx[b]))
			}
		}
		_, ldX, ok := invLogDet(sxx)
		if !ok {
			return errLavaanStats
		}
		fb += ldX
		ldSxx = ldX
	}
	st.Chi2Baseline = n * fb

	inv, ldSigma, ok := invLogDet(fit.Implied)
	if !ok {
		return errLavaanStats
	}
	// h0 log-likelihood: −N/2 [p ln 2π + ln|Σ| + tr(SΣ⁻¹) + rᵀΣ⁻¹r].
	tr := 0.0
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			tr += s.At(i, j) * inv.At(j, i)
		}
	}
	var r []float64
	if m.MeanStructure {
		r = make([]float64, p)
		for i := range r {
			r[i] = fit.SampleMean[i] - fit.ImpliedMean[i]
		}
		tr += quad(inv, r, r)
	}
	st.LogLik = -n / 2 * (pf*math.Log(2*math.Pi) + ldSigma + tr)
	if len(xIdx) > 0 {
		// fixed.x: lavaan's logl is conditional on the exogenous block,
		// so its (saturated) marginal log-likelihood leaves it.
		st.LogLik += n / 2 * (nx*math.Log(2*math.Pi) + ldSxx + nx)
	}
	st.AIC = -2*st.LogLik + 2*k
	st.BIC = -2*st.LogLik + k*math.Log(n)

	if st.DoF > 0 {
		st.RMSEA = math.Sqrt(math.Max((st.Chi2/n)/st.DoF-1/n, 0))
	}
	t1 := math.Max(st.Chi2-st.DoF, 0)
	t2 := math.Max(math.Max(st.Chi2-st.DoF, st.Chi2Baseline-st.DoFBaseline), 0)
	if nearlyZero(t1) && nearlyZero(t2) {
		st.CFI = 1
	} else {
		st.CFI = 1 - t1/t2
	}
	a, b := (st.Chi2-st.DoF)*st.DoFBaseline, (st.Chi2Baseline-st.DoFBaseline)*st.DoF
	switch {
	case st.DoF > 0 && math.Abs(b) > 0:
		st.TLI = 1 - a/b
	case math.IsInf(a, 0) || math.IsNaN(a) || math.IsInf(b, 0) || math.IsNaN(b):
		st.TLI = math.NaN()
	default:
		st.TLI = 1
	}
	if st.Chi2Baseline == 0 {
		st.NFI = math.NaN()
	} else {
		st.NFI = (st.Chi2Baseline - st.Chi2) / st.Chi2Baseline
	}
	st.GFI = gfiLisrel(fit, inv, xIdx, r)
	if st.DoF > 0 {
		st.AGFI = 1 - nel/st.DoF*(1-st.GFI)
	} else {
		st.AGFI = 1
	}
	fit.Stats = st
	return nil
}

var errLavaanStats = errors.New("the fit statistics are undefined: a covariance is not positive definite")

// nearlyZero is R's isTRUE(all.equal(x, 0)): |x| below 1.5e-8.
func nearlyZero(x float64) bool { return math.Abs(x) < 1.5e-8 }

// gfiLisrel is lavaan's gfi_lisrel for ML: 1 − (s−σ)ᵀV(s−σ) / sᵀVs over
// the means (under a mean structure) and the vech of the covariance,
// V the normal-theory weight at Σ(θ̂) — Σ⁻¹ on the means, ½D'(Σ⁻¹⊗Σ⁻¹)D
// on the covariance — with the exogenous block zeroed (fixed.x). On a
// symmetric A, vech(A)ᵀ½D'(K⊗K)D vech(A) = ½tr(KAKA).
func gfiLisrel(fit *Fit, inv *mat.SymDense, xIdx []int, r []float64) float64 {
	p := len(fit.Model.Observed)
	isX := make([]bool, p)
	for _, i := range xIdx {
		isX[i] = true
	}
	form := func(a *mat.Dense, d []float64) float64 {
		var ka, kaka mat.Dense
		ka.Mul(inv, a)
		kaka.Mul(&ka, &ka)
		v := 0.5 * mat.Trace(&kaka)
		if d != nil {
			v += quad(inv, d, d)
		}
		return v
	}
	diff := mat.NewDense(p, p, nil)
	obs := mat.NewDense(p, p, nil)
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			if isX[i] && isX[j] {
				continue
			}
			diff.Set(i, j, fit.Sample.At(i, j)-fit.Implied.At(i, j))
			obs.Set(i, j, fit.Sample.At(i, j))
		}
	}
	var dm, om []float64
	if r != nil {
		dm, om = make([]float64, p), make([]float64, p)
		for i := 0; i < p; i++ {
			if isX[i] {
				continue
			}
			dm[i], om[i] = r[i], fit.SampleMean[i]
		}
	}
	return 1 - form(diff, dm)/form(obs, om)
}
