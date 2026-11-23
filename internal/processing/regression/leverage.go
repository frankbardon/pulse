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
// φ; a penalized spec — E1-S4 — carries its regularized inverse
// (XᵀW*X + λ₂·P)⁻¹ over the active design instead, the same convention
// as the OLS refit's hat design), and the leverage reads that design through the bound-fit kernel
// (NewBoundInfluence) — so a refit's hat and a bound REG_GLM fit's agree
// bit for bit on the same rows, both R hatvalues on the glm. The row's
// prior weight multiplies it as w* (buildWeightedDesign's map onto w* =
// c·w, a multiply only under a probability weight), the scale the
// covariance read; the hat is invariant to that scale, as R's.
func fitGLMForAttribute(spec *types.RegressionSpec, records []Record) (*AttributeFit, error) {
	glmSpec := *spec
	// A penalized refit carries its regularized inverse instead
	// (RegressionResult.PenalizedInverse, E1-S4); l1 / elasticnet refuse
	// a vcov.
	glmSpec.Vcov = spec.Penalty == ""
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

// penalizedInverseOLS is a penalized REG_OLS fit's carried regularized
// inverse (RegressionResult.PenalizedInverse, regression-depth-2 E1-S4):
// leverageDesign's hat design over acc and coeffs, uncentred. With C⁻¹
// = (M2_AA + Σw·λ₂·P_A)⁻¹ its inverse and μ the weighted means over A,
//
//	M = [[1/Σw + μᵀC⁻¹μ, −(C⁻¹μ)ᵀ], [−C⁻¹μ, C⁻¹]]
//
// is (X̃_AᵀWX̃_A + Σw·λ₂·P̃_A)⁻¹ over [intercept, A] (the block inverse;
// the intercept is unpenalized), so x̃ᵀMx̃ = 1/Σw + (x − μ)_Aᵀ C⁻¹
// (x − μ)_A — the refit's hat (hatDesign.at) for one copy of a row, the
// form a bound fit's design kernel (BoundInfluence) reads; divided by
// the inference-basis scale c under kind probability (below). A singular
// active design (the refit's PROCESSING_REGRESSION_RANK_DEFICIENT) is
// carried with every entry NaN — null on the wire, a null leverage —
// never a fit failure. FMA-free products.
func penalizedInverseOLS(spec *types.RegressionSpec, acc *olsAccumulator, coeffs []float64) *types.MatrixValues {
	d, err := leverageDesign(spec, acc, coeffs)
	if err != nil {
		cols := make([]int, 0, len(coeffs))
		for j, c := range coeffs {
			if spec.Penalty == "l2" || c != 0 {
				cols = append(cols, j)
			}
		}
		return penalizedInverseMatrix(spec.Predictors, cols, nil)
	}
	q := len(d.Cols)
	m := make([][]float64, q+1)
	for i := range m {
		m[i] = make([]float64, q+1)
	}
	v := make([]float64, q)
	for a := 0; a < q; a++ {
		r := 0.0
		for b, j := range d.Cols {
			r += float64(d.Inverse.At(a, b) * acc.meanX[j])
		}
		v[a] = r
	}
	m00 := 1.0 / acc.sumW
	for a, j := range d.Cols {
		m00 += float64(acc.meanX[j] * v[a])
		m[0][a+1], m[a+1][0] = -v[a], -v[a]
		for b := 0; b < q; b++ {
			m[a+1][b+1] = d.Inverse.At(a, b)
		}
	}
	m[0][0] = m00
	// On the inference basis, as REG_GLM's prior weights w* = c·w
	// (buildWeightedDesign): (X̃ᵀ(cW)X̃ + c·Σw·λ₂·P̃)⁻¹ = M/c, so the
	// carriage is scale-free under kind probability and the hat a GLM
	// refit reads (w·c·x̃ᵀMx̃, glmScale) is one convention for both
	// engines. c is exactly 1 unweighted and under kind frequency (no
	// division: the bits are the hat design's).
	if c := acc.basis.Scale(acc.sumW, acc.sumWSq); c != 1 {
		for i := range m {
			for j := range m[i] {
				m[i][j] /= c
			}
		}
	}
	return penalizedInverseMatrix(spec.Predictors, d.Cols, m)
}

// carriesPenalizedInverse reports whether a fit of spec carries
// RegressionResult.PenalizedInverse: a penalized fit without resample or
// selection — a bound fit's leverage reads no other
// (model.CheckFitDesign refuses those modifiers first), so they carry
// none and keep their output. model.FitDesign's spec face is the
// same rule.
func carriesPenalizedInverse(spec *types.RegressionSpec) bool {
	return model.CarriesPenalizedInverse(spec)
}

// penalizedInverseMatrix keys inv (len(cols)+1 square over the
// intercept and predictors[cols…], fit order) as
// RegressionResult.PenalizedInverse; nil inv is the all-NaN matrix of a
// singular active design.
func penalizedInverseMatrix(predictors []string, cols []int, inv [][]float64) *types.MatrixValues {
	keys := make([]string, 0, len(cols)+1)
	keys = append(keys, InterceptKey)
	for _, j := range cols {
		keys = append(keys, predictors[j])
	}
	if inv == nil {
		inv = make([][]float64, len(keys))
		for i := range inv {
			inv[i] = make([]float64, len(keys))
			for j := range inv[i] {
				inv[i][j] = math.NaN()
			}
		}
	}
	return vcovMatrix(keys, inv)
}
