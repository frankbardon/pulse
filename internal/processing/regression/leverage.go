package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// leverage.go: the ONE hat-matrix design of an OLS refit with an
// intercept (ATTR_REG_LEVERAGE and the influence figures), unpenalized
// or penalized (regression-depth-2 E1-S2, #677). With A the columns the
// hat reads, μ_x the (weighted) means, M2_AA the Σw-weighted centred
// Gram over A, P_A the penalty mask (glmnet penalty.factor; the
// intercept is never penalized, so centring is exact) and λ₂ the ridge
// term of the fit's objective,
//
//	hᵢᵢ = wᵢ · (1/Σw + (xᵢ − μ_x)_Aᵀ · (M2_AA + Σw·λ₂·P_A)⁻¹ · (xᵢ − μ_x)_A)
//
// — the diagonal of W½X_A(X_AᵀWX_A + Σw·λ₂·P̃_A)⁻¹X_AᵀW½:
//
//	unpenalized  A = every predictor, λ₂ = 0 (R hatvalues on lm)
//	l2 (ridge)   A = every predictor, λ₂ = α — the exact ridge hat
//	l1 (lasso)   A = the active set {j : βⱼ ≠ 0}, λ₂ = 0 — the hat of the
//	             unpenalized OLS on the active set, whose trace is
//	             1 + |A| (Zou, Hastie & Tibshirani's df(lasso) = |A|)
//	elasticnet   A = the active set, λ₂ = α·(1 − l1_ratio)
//
// The Σw scale is the objective's (1/2Σw)·Σw·r² (solveRidge,
// coordinateDescent), so the hat is the one the solver's normal
// equations define. An empty active set leaves hᵢᵢ = wᵢ/Σw. The design
// is a function of the rows' weights only through Σw and the weighted
// Gram, so a frequency-weighted bound fit's design (E1-S3) reads the
// same kernel.

// hatDesign is the hat-matrix design of a refit: Cols indexes the
// predictors (fit order) the hat reads, Inverse is
// (M2_AA + Σw·λ₂·P_A)⁻¹ over them (nil when Cols is empty).
type hatDesign struct {
	Cols    []int
	Inverse *linalg.Sym
}

// leverageDesign builds the hat design of the refit spec fitted on acc
// with slope coefficients coeffs (fit order). acc must be finalized
// (the solver did it). A design whose system is not positive-definite
// (linearly dependent or constant predictors in A) is
// PROCESSING_REGRESSION_RANK_DEFICIENT.
func leverageDesign(spec *types.RegressionSpec, acc *olsAccumulator, coeffs []float64) (*hatDesign, error) {
	p := acc.p
	ridge := 0.0
	active := func(int) bool { return true }
	switch spec.Penalty {
	case "l2":
		ridge = spec.Alpha
	case "l1":
		active = func(j int) bool { return coeffs[j] != 0 }
	case "elasticnet":
		ridge = float64(spec.Alpha * (1 - spec.L1Ratio))
		active = func(j int) bool { return coeffs[j] != 0 }
	}
	cols := make([]int, 0, p)
	for j := 0; j < p; j++ {
		if active(j) {
			cols = append(cols, j)
		}
	}
	d := &hatDesign{Cols: cols}
	q := len(cols)
	if q == 0 {
		return d, nil
	}
	pen := regpenalty.PenalizedTerms(spec, spec.Predictors)
	scaled := float64(acc.sumW * ridge)
	sub := make([]float64, q*q)
	for a, i := range cols {
		for b, j := range cols {
			sub[a*q+b] = acc.m2XX[i*p+j]
		}
		if ridge != 0 && pen[i+1] {
			sub[a*q+a] += scaled
		}
	}
	chol, ok := factorSPD(q, sub)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"leverage requires invertible centered Gram matrix (linearly dependent or constant predictors)",
			map[string]any{"n": acc.n, "p": p, "penalty": spec.Penalty, "active": q},
		)
	}
	inv, err := chol.Inverse()
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"leverage: Cholesky inverse failed for the centered Gram matrix",
			map[string]any{"gonum_error": backendErrorText(err)},
		)
	}
	d.Inverse = inv
	return d, nil
}

// at is hᵢᵢ at predictors x (fit order) for a row of weight w, given
// the means mean and Σw sumW. FMA-free: every product feeding an add is
// wrapped in float64(...).
func (d *hatDesign) at(x, mean []float64, w, sumW float64) float64 {
	q := len(d.Cols)
	dx := make([]float64, q)
	for a, j := range d.Cols {
		dx[a] = x[j] - mean[j]
	}
	quad := 0.0
	for a := 0; a < q; a++ {
		row := 0.0
		for b := 0; b < q; b++ {
			row += float64(d.Inverse.At(a, b) * dx[b])
		}
		quad += float64(dx[a] * row)
	}
	return w * (1.0/sumW + quad)
}

// fitGLMForAttribute is ATTR_REG_LEVERAGE's GLM refit (params.family,
// regression-depth-2 E1-S2): the REG_GLM engine fits spec (vcov on, so
// the result carries φ·(XᵀW*X)⁻¹ at the final IRLS working weights and
// φ), and the leverage reads that design through the bound-fit kernel
// (NewBoundInfluence) — so a refit's hat and a bound REG_GLM fit's agree
// bit for bit on the same rows, both R hatvalues on the glm. The row's
// prior weight multiplies it as w* (buildWeightedDesign's map onto w* =
// c·w, a multiply only under a probability weight), the scale the
// covariance read; the hat is invariant to that scale, as R's.
func fitGLMForAttribute(spec *types.RegressionSpec, records []Record) (*AttributeFit, error) {
	glmSpec := *spec
	glmSpec.Vcov = true
	eng, err := newGLMEngine(&glmSpec, nil)
	if err != nil {
		return nil, err
	}
	res, err := eng.(*glmEngine).FitBuffered(records)
	if err != nil {
		return nil, err
	}
	b, err := model.Bind("", &glmSpec, res)
	if err != nil {
		return nil, err
	}
	d, err := NewBoundInfluence(b)
	if err != nil {
		return nil, err
	}
	rw := newRowWeights(&glmSpec)
	_, _, _, _, sumW, sumWSq := buildWeightedDesign(records, glmSpec.Predictors, glmSpec.Target, rw, nil)
	return &AttributeFit{
		Coefficients:   append([]float64(nil), b.Coefficients...),
		Intercept:      b.Intercept,
		PredictorOrder: append([]string(nil), glmSpec.Predictors...),
		NObs:           res.NObs,
		SumW:           sumW,
		RSS:            math.NaN(),
		DF:             math.NaN(),
		weights:        rw,
		glm:            d,
		glmScale:       rw.basis.Scale(sumW, sumWSq),
	}, nil
}
