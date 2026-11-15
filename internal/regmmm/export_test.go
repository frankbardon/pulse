package regmmm

import (
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
)

// export_test.go opens the model's internals to the external test
// package (regmmm_test), which imports the root package to run the
// model end to end and so cannot live in this package.

// HillCurve is the curve the model fits (hillCurve).
var HillCurve = hillCurve

// MaxIterations is the solver's default iteration cap.
const MaxIterations = maxIterations

// SolveColumns fits the model directly over columns — y, the channel
// columns a and the control columns z (named channels / controls) —
// under nonnegative betas and maxIter solver iterations, bypassing the
// engine: the start, the solve and the output mapping, exactly as Fit
// runs them (kRef the channel mean, summed then divided).
func SolveColumns(channels, controls []string, y []float64, a, z [][]float64, maxIter int) (extend.RegressionOutput, error) {
	prob := &problem{y: y, a: a, z: z, kRef: make([]float64, len(a)), priorIdx: -1}
	for c := range a {
		for _, v := range a[c] {
			prob.kRef[c] += v
		}
		prob.kRef[c] /= float64(len(y))
	}
	x0, err := prob.start(true)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	res, err := prob.solve(x0, true, maxIter)
	if err != nil {
		return extend.RegressionOutput{}, err
	}
	m := &model{target: "y", channels: channels, controls: controls, nonneg: true}
	return m.output(prob, res), nil
}

// HillStart is REG_MMM_HILL's fixed starting vector over columns (the
// coefficient table's order, k and s in their own units: k_c =
// kRef_c·exp θk, s_c = exp θs), under nonnegative betas and, when
// priorChannel ≥ 0, a prior on that channel's beta.
func HillStart(y []float64, a, z [][]float64, priorChannel int, priorMean, priorPrecision float64) ([]float64, error) {
	prob := &problem{y: y, a: a, z: z, kRef: make([]float64, len(a)), priorIdx: priorChannel}
	if priorChannel >= 0 {
		prob.prior = hillPrior{Mean: priorMean, Precision: priorPrecision}
	}
	for c := range a {
		for _, v := range a[c] {
			prob.kRef[c] += v
		}
		prob.kRef[c] /= float64(len(y))
	}
	x0, err := prob.start(true)
	if err != nil {
		return nil, err
	}
	_, k, s, _ := prob.curve(x0)
	nc, nz := len(a), len(z)
	copy(x0[1+nc+nz:], k)
	copy(x0[1+2*nc+nz:], s)
	return x0, nil
}

// HillAdstockStart is an in-model adstocked fit's start over one channel
// of raw spend x and controls z (the coefficient table's order, k and
// s in their own units) and its channel's k scale kRef, exactly as Fit
// reaches them: initAdstock, then the startAdstock scan.
func HillAdstockStart(kernel string, maxLag int, y, x []float64, z [][]float64) (x0 []float64, kRef float64, err error) {
	k, reason := hillKernelFor(kernel, maxLag)
	if reason != "" {
		return nil, 0, perr.NewCodedError(perr.PROCESSING_CONFIG, reason)
	}
	prob := &problem{y: y, a: [][]float64{append([]float64(nil), x...)}, z: z, kRef: make([]float64, 1), priorIdx: -1,
		ad: &hillAdstock{kernel: k, spend: make([][]float64, 1), da: make([][][]float64, 1)}}
	prob.initAdstock()
	x0, err = prob.startAdstock(true)
	if err != nil {
		return nil, 0, err
	}
	_, kk, s, _ := prob.curve(x0)
	nz := len(z)
	x0[2+nz], x0[3+nz] = kk[0], s[0]
	return x0, prob.kRef[0], nil
}
