package sem

import (
	"math"

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
func ComputeStats(fit *Fit, desc string, opt Options) error {
	opt = fit.Options
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
