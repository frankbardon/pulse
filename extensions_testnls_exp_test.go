package pulse_test

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/optimize"
	"github.com/frankbardon/pulse/types"
)

// extensions_testnls_exp_test.go is the test-only reference extension
// REG_TESTNLS_EXP: an iterative nonlinear least-squares regression
// written exactly as an embedder would write it — this file imports
// only Pulse's public packages (pulse, extend, encoding, errors,
// optimize, types) and the standard library. It proves an iterative
// fit, its Scorer, a saved model file and the engine's resample run
// through the public extension contract
// (extensions_regression_nls_test.go); docs/src/internals/
// adding-regression.md points embedders here.
//
// The model is an exponential approach to an asymptote in every
// predictor x_j, one time constant τ shared by all,
//
//	y = a + Σ_j b_j·(1 − exp(−x_j/τ)),
//
// fitted by Pulse's public Levenberg–Marquardt, optimize.LeastSquares,
// over θ = [a, b_1..b_k, ln τ] (τ stays positive), from a fixed,
// data-derived start. Coefficients are "(intercept)" (a) and each
// predictor (b_j), with no standard error (null on the wire); the
// declared Output tau is τ. The Scorer rebuilds the prediction from the
// fit alone.

// regTestNLSExp is the reference extension's registered name.
const regTestNLSExp types.RegressionType = "REG_TESTNLS_EXP"

// testNLSExpRegistration is REG_TESTNLS_EXP's registration.
func testNLSExpRegistration() pulse.RegressionRegistration {
	return pulse.RegressionRegistration{
		Name:        regTestNLSExp,
		Description: "Test-only nonlinear regression: y = a + Σ b_j·(1 − exp(−x_j/τ)) by Levenberg–Marquardt.",
		OutputSchema: []pulse.RegressionOutputMeta{
			{Key: "tau", JSONType: "number", Description: "The fitted time constant τ (> 0)."},
		},
		Factory: testNLSExpFactory,
		Scorer:  testNLSExpScorer,
		// The model reports no p-value itself; under resample the engine
		// writes the replicate ones, which the bootstrap test reads.
		PValues: true,
	}
}

func testNLSExpFactory(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
	return &nlsExpModel{target: spec.Target, predictors: spec.Predictors}, nil
}

// nlsExpModel is one slot's buffered fit.
type nlsExpModel struct {
	target     string
	predictors []string
}

// nlsExpPredict is a + Σ b_j·(1 − exp(−x_j/τ)).
func nlsExpPredict(a float64, b []float64, tau float64, x []float64) float64 {
	out := a
	for j, v := range x {
		out += float64(b[j] * (1 - math.Exp(-v/tau)))
	}
	return out
}

// nlsExpResiduals fills r (one per row) at θ and, when jac is non-nil,
// the Jacobian row by row. cols[j] is predictor j's column. The
// objective is ½·Σ r².
func nlsExpResiduals(r []float64, jac [][]float64, cols [][]float64, ys, theta []float64) {
	k := len(cols)
	a, b, tau := theta[0], theta[1:1+k], math.Exp(theta[1+k])
	for i, y := range ys {
		pred, dTau := a, 0.0
		for j := range cols {
			x := cols[j][i]
			e := math.Exp(-x / tau)
			pred += float64(b[j] * (1 - e))
			if jac != nil {
				// ∂/∂b_j = 1 − e_j; ∂/∂ln τ = −Σ b_j·e_j·x_j/τ.
				jac[i][1+j] = 1 - e
				dTau -= float64(float64(b[j]*e) * (x / tau))
			}
		}
		r[i] = pred - y
		if jac != nil {
			jac[i][0], jac[i][1+k] = 1, dTau
		}
	}
}

func (m *nlsExpModel) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	n, k := rows.Len(), len(m.predictors)
	if n <= k+2 {
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			fmt.Sprintf("REG_TESTNLS_EXP needs more than %d rows, got %d", k+2, n))
	}
	ys, cols := make([]float64, n), make([][]float64, k)
	for j := range cols {
		cols[j] = make([]float64, n)
	}
	xMax, yMin, yMax := math.Inf(-1), math.Inf(1), math.Inf(-1)
	for i := range n {
		rec := rows.At(i)
		ys[i], _ = rec.NumericValue(m.target)
		yMin, yMax = math.Min(yMin, ys[i]), math.Max(yMax, ys[i])
		for j, name := range m.predictors {
			cols[j][i], _ = rec.NumericValue(name)
			xMax = math.Max(xMax, cols[j][i])
		}
	}
	if !(xMax > 0) {
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"REG_TESTNLS_EXP: no predictor rises above 0")
	}
	// The fixed start: the observed range shared out as the gains, τ a
	// fifth of the largest predictor value.
	x0 := make([]float64, k+2)
	x0[0], x0[k+1] = yMin, math.Log(xMax/5)
	for j := range k {
		x0[1+j] = (yMax - yMin) / float64(k)
	}
	r, scratch := make([]float64, n), make([]float64, n)
	jac := make([][]float64, n)
	for i := range jac {
		jac[i] = make([]float64, k+2)
	}
	res, err := optimize.LeastSquares(
		func(th []float64) []float64 { nlsExpResiduals(r, nil, cols, ys, th); return r },
		func(th []float64) [][]float64 { nlsExpResiduals(scratch, jac, cols, ys, th); return jac },
		x0, nil, nil, optimize.LeastSquaresOptions{})
	switch {
	case perr.HasCode(err, perr.PULSE_OPTIMIZE_NOT_CONVERGED):
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_NO_CONVERGE,
			fmt.Sprintf("REG_TESTNLS_EXP did not converge in %d iterations", res.Iterations))
	case err != nil:
		return extend.RegressionOutput{}, err
	}
	a, b, tau := res.X[0], res.X[1:1+k], math.Exp(res.X[1+k])
	coefs := []extend.RegressionCoefficient{{Name: "(intercept)", Estimate: a}}
	for j, name := range m.predictors {
		coefs = append(coefs, extend.RegressionCoefficient{Name: name, Estimate: b[j]})
	}
	var ssr, my, sst float64
	x := make([]float64, k)
	for i, y := range ys {
		for j := range cols {
			x[j] = cols[j][i]
		}
		d := y - nlsExpPredict(a, b, tau, x)
		ssr += d * d
		my += y
	}
	my /= float64(n)
	for _, y := range ys {
		sst += (y - my) * (y - my)
	}
	return extend.RegressionOutput{
		Coefficients: coefs,
		Fit: extend.RegressionFit{
			R2:             extend.Figure(1 - ssr/sst),
			ResidualStdErr: extend.Figure(math.Sqrt(ssr / float64(n-k-2))),
			Deviance:       extend.Figure(ssr),
			Iterations:     res.Iterations,
		},
		Outputs: map[string]any{"tau": tau},
	}, nil
}

// nlsExpScorer scores a + Σ b_j·(1 − exp(−x_j/τ)) from a fit alone.
type nlsExpScorer struct {
	predictors []string
	a, tau     float64
	b          []float64
}

func (s nlsExpScorer) Columns() []string { return s.predictors }

func (s nlsExpScorer) Score(row extend.ScoreRow) (float64, bool) {
	x := make([]float64, len(s.predictors))
	for j, name := range s.predictors {
		v, ok := row.NumericValue(name)
		if !ok {
			return 0, false
		}
		x[j] = v
	}
	return nlsExpPredict(s.a, s.b, s.tau, x), true
}

// testNLSExpScorer is the registration's Scorer.
func testNLSExpScorer(fit extend.FittedRegression) (extend.RowScorer, error) {
	tau, ok := fit.Outputs["tau"].(float64)
	if !ok {
		return nil, fmt.Errorf("REG_TESTNLS_EXP scorer: no tau output (%v)", fit.Outputs["tau"])
	}
	s := nlsExpScorer{predictors: fit.Predictors, a: fit.Coefficients["(intercept)"], tau: tau}
	for _, name := range fit.Predictors {
		s.b = append(s.b, fit.Coefficients[name])
	}
	return s, nil
}
