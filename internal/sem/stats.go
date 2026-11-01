package sem

import (
	"errors"
	"math"

	"gonum.org/v1/gonum/mat"

	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg/detmath"
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
	// Scaled is WLSMV's scaled-shifted test and scaled indices (nil on
	// every other fit).
	Scaled *ScaledStats
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
	moments := float64(float64(p*(p+1)) / 2)
	st := Stats{NParams: len(fit.theta)}
	st.DoF = moments - k
	st.DoFBaseline = moments - float64(len(bfit.theta))
	st.Chi2 = float64(fit.N * fit.Objective)
	st.Chi2P = 1 - chiCDF(st.Chi2, st.DoF)
	st.Chi2Baseline = float64(fit.N * bfit.Objective)
	st.LogLik = fit.Objective
	st.AIC = 2 * (k - st.LogLik)
	st.BIC = float64(detmath.Log(fit.N)*k) - 2*st.LogLik
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
		st.AGFI = 1 - float64(st.DoFBaseline/st.DoF*(1-st.GFI))
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
	return statdist.ChiSquareCDF(x, dof)
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
	part, err := lavaanGroupPart(fit.Model, fit.Sample, fit.Implied, fit.SampleMean, fit.ImpliedMean, fit.N)
	if err != nil {
		return err
	}
	fit.Stats = lavaanCombine([]lavaanPart{part}, []float64{fit.N * fit.Objective}, len(fit.theta))
	return nil
}

// lavaanPart is one group's share of lavaan's statistics: its counted
// moments (ndat, and nel, every moment the GFI weighs), the baseline's
// free parameters and discrepancy F_b, the h0 log-likelihood and the
// group's gfi_lisrel, with its row count n.
type lavaanPart struct {
	ndat, nel, baseParams float64
	fb, logLik, gfi, n    float64
}

// lavaanGroupPart computes one group's lavaanPart from its sample and
// implied moments (the means nil without a mean structure).
func lavaanGroupPart(m *Model, s, implied *mat.SymDense, sampleMean, impliedMean []float64, n float64) (lavaanPart, error) {
	p := len(m.Observed)
	pf := float64(p)
	var xIdx, yIdx []int
	for i, v := range m.Observed {
		if m.isObsExo(v) {
			xIdx = append(xIdx, i)
		} else {
			yIdx = append(yIdx, i)
		}
	}
	nx, ny := float64(len(xIdx)), float64(len(yIdx))
	ndat := float64(float64(pf*(pf+1)) / 2)
	nel := ndat
	xMoments := float64(float64(nx*(nx+1)) / 2)
	baseParams := ny
	if m.MeanStructure {
		ndat += pf
		nel += pf
		xMoments += nx
		baseParams += ny
	}
	ndat -= xMoments
	part := lavaanPart{ndat: ndat, nel: nel, baseParams: baseParams, n: n}

	_, ldS, ok := invLogDet(s)
	if !ok {
		return part, errLavaanStats
	}
	fb := -ldS
	ldSxx := 0.0
	for _, i := range yIdx {
		fb += detmath.Log(s.At(i, i))
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
			return part, errLavaanStats
		}
		fb += ldX
		ldSxx = ldX
	}
	part.fb = fb

	inv, ldSigma, ok := invLogDet(implied)
	if !ok {
		return part, errLavaanStats
	}
	// h0 log-likelihood: −N/2 [p ln 2π + ln|Σ| + tr(SΣ⁻¹) + rᵀΣ⁻¹r].
	tr := 0.0
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			tr += float64(s.At(i, j) * inv.At(j, i))
		}
	}
	var r []float64
	if m.MeanStructure {
		r = make([]float64, p)
		for i := range r {
			r[i] = sampleMean[i] - impliedMean[i]
		}
		tr += quad(inv, r, r)
	}
	part.logLik = float64(-n / 2 * (float64(pf*detmath.Log(2*math.Pi)) + ldSigma + tr))
	if len(xIdx) > 0 {
		// fixed.x: lavaan's logl is conditional on the exogenous block,
		// so its (saturated) marginal log-likelihood leaves it.
		part.logLik += float64(n / 2 * (float64(nx*detmath.Log(2*math.Pi)) + ldSxx + nx))
	}
	part.gfi = gfiLisrel(s, implied, sampleMean, inv, xIdx, r)
	return part, nil
}

// lavaanCombine is lavaan's statistics over the groups' parts (one
// part: a single-group fit), chi2[g] each group's n_g·F_g and k the
// free-parameter count: the counts, chi-squares, log-likelihoods and
// baseline sum over the groups, N is the total row count, RMSEA carries
// lavaan's multiple-group factor √G, and gfi_lisrel is the groups'
// n-weighted mean.
func lavaanCombine(parts []lavaanPart, chi2 []float64, nParams int) Stats {
	k := float64(nParams)
	var ndat, nel, baseParams, n, chi, chiB, ll, gfi float64
	for g, pt := range parts {
		ndat += pt.ndat
		nel += pt.nel
		baseParams += pt.baseParams
		n += pt.n
		chi += chi2[g]
		chiB += float64(pt.n * pt.fb)
		ll += pt.logLik
		gfi += float64(pt.n * pt.gfi)
	}
	if len(parts) == 1 {
		gfi = parts[0].gfi
	} else {
		gfi /= n
	}
	st := Stats{NParams: nParams}
	st.DoF = ndat - k
	st.DoFBaseline = ndat - baseParams
	st.Chi2 = chi
	st.Chi2P = 1 - chiCDF(st.Chi2, st.DoF)
	st.Chi2Baseline = chiB
	st.LogLik = ll
	st.AIC = float64(-2*st.LogLik) + float64(2*k)
	st.BIC = float64(-2*st.LogLik) + float64(k*detmath.Log(n))

	st.CFI, st.TLI, st.RMSEA = lavaanIndices(st.Chi2, st.DoF, st.Chi2Baseline, st.DoFBaseline, n, len(parts))
	st.NFI = lavaanNFI(st.Chi2, st.DoF, st.Chi2Baseline, st.DoFBaseline)
	st.GFI = gfi
	if st.DoF > 0 {
		st.AGFI = 1 - float64(nel/st.DoF*(1-st.GFI))
	} else {
		st.AGFI = 1
	}
	return st
}

// lavaanIndices are lav_fit_cfi, lav_fit_tli and lav_fit_rmsea (divisor
// n, the multiple-group factor √g) of a test statistic x2 on df against
// the baseline's x2b on dfb.
func lavaanIndices(x2, df, x2b, dfb, n float64, g int) (cfi, tli, rmsea float64) {
	if df > 0 {
		rmsea = math.Sqrt(math.Max((x2/n)/df-1/n, 0))
		if g > 1 {
			rmsea *= math.Sqrt(float64(g))
		}
	}
	t1 := math.Max(x2-df, 0)
	t2 := math.Max(math.Max(x2-df, x2b-dfb), 0)
	if nearlyZero(t1) && nearlyZero(t2) {
		cfi = 1
	} else {
		cfi = 1 - t1/t2
	}
	a, b := float64((x2-df)*dfb), float64((x2b-dfb)*df)
	switch {
	case df > 0 && math.Abs(b) > 0:
		tli = 1 - a/b
	case math.IsInf(a, 0) || math.IsNaN(a) || math.IsInf(b, 0) || math.IsNaN(b):
		tli = math.NaN()
	default:
		tli = 1
	}
	return cfi, tli, rmsea
}

// lavaanNFI is lav_fit_nfi: undefined past the baseline's degrees of
// freedom (a model more restricted than independence) or on a zero
// baseline, 1 when saturated.
func lavaanNFI(x2, df, x2b, dfb float64) float64 {
	switch {
	case df > dfb || nearlyZero(x2b):
		return math.NaN()
	case df > 0:
		return (x2b - x2) / x2b
	}
	return 1
}

var errLavaanStats = errors.New("the fit statistics are undefined: a covariance is not positive definite")

// nearlyZero is R's isTRUE(all.equal(x, 0)): |x| below 1.5e-8.
func nearlyZero(x float64) bool { return math.Abs(x) < 1.5e-8 }

// gfiLisrel is lavaan's gfi_lisrel for ML: 1 − (s−σ)ᵀV(s−σ) / sᵀVs over
// the means (under a mean structure) and the vech of the covariance,
// V the normal-theory weight at Σ(θ̂) — Σ⁻¹ on the means, ½D'(Σ⁻¹⊗Σ⁻¹)D
// on the covariance — with the exogenous block zeroed (fixed.x). On a
// symmetric A, vech(A)ᵀ½D'(K⊗K)D vech(A) = ½tr(KAKA).
func gfiLisrel(sample, implied *mat.SymDense, sampleMean []float64, inv *mat.SymDense, xIdx []int, r []float64) float64 {
	p := sample.SymmetricDim()
	isX := make([]bool, p)
	for _, i := range xIdx {
		isX[i] = true
	}
	form := func(a *mat.Dense, d []float64) float64 {
		var ka, kaka mat.Dense
		ka.Mul(inv, a)
		kaka.Mul(&ka, &ka)
		v := float64(0.5 * mat.Trace(&kaka))
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
			diff.Set(i, j, sample.At(i, j)-implied.At(i, j))
			obs.Set(i, j, sample.At(i, j))
		}
	}
	var dm, om []float64
	if r != nil {
		dm, om = make([]float64, p), make([]float64, p)
		for i := 0; i < p; i++ {
			if isX[i] {
				continue
			}
			dm[i], om[i] = r[i], sampleMean[i]
		}
	}
	return 1 - form(diff, dm)/form(obs, om)
}
