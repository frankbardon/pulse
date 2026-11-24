package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/types"
)

// AttributeFit is the minimal package of post-fit quantities the
// per-row regression attributes (ATTR_REG_FITTED / ATTR_REG_RESIDUAL /
// ATTR_REG_LEVERAGE) need to emit their second-pass per-row values.
//
// The full RegressionResult / olsSolveResult bundle is intentionally
// not returned: attribute callers do not surface regression result
// blocks; they only need the coefficient vector (for ŷᵢ / residuals)
// and, in the leverage case, the refit's hat design (leverage.go) plus
// the streaming predictor means (for the hat-matrix identity
//
//	hᵢᵢ = wᵢ·(1/Σw + (xᵢ − μ_x)_Aᵀ · (M2_AA + Σw·λ₂·P_A)⁻¹ · (xᵢ − μ_x)_A),
//
// the unpenalized 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x) at A = every
// predictor, λ₂ = 0).
//
// The struct is per-attribute state captured at finalize time; the
// attribute then iterates records a second time and computes the
// per-row scalar.
type AttributeFit struct {
	// Coefficients are the fitted slopes in the same order as
	// PredictorOrder. Length equals len(PredictorOrder).
	Coefficients []float64

	// Intercept is the fitted intercept term β₀.
	Intercept float64

	// PredictorOrder echoes the spec's Predictors slice. Provided so the
	// attribute's pass-2 loop reads predictors in the same order the
	// solver used. Owned by the caller.
	PredictorOrder []string

	// MeanX is the streaming mean of each predictor over the rows that
	// contributed to the fit (listwise-deleted predictor/target nulls).
	// Length matches PredictorOrder. Populated for every fit.
	MeanX []float64

	// NObs is the number of rows that contributed to the fit (n in the
	// hat-matrix identity 1/n + …).
	NObs int

	// SumW is Σw over the rows that entered the fit — the exact float
	// of NObs unweighted (the hat-matrix identity's n).
	SumW float64

	// RSS is the refit's (w-scale) residual sum of squares Σw·e², from
	// the same streaming QR solve as the coefficients, and DF its
	// residual degrees of freedom N* − p − 1 (Σw − p − 1 under a
	// frequency weight, n − p − 1 unweighted). Set for an unpenalized
	// fit only (NaN otherwise): the influence figures read σ² = RSS/DF
	// (InfluenceForRow).
	RSS float64
	DF  float64

	// weights is the refit's row weight (zero value: unweighted).
	weights rowWeights

	// design is the refit's hat design (leverageDesign): the columns
	// the hat reads and (M2_AA + Σw·λ₂·P_A)⁻¹ over them. Populated only
	// when the caller requests it (ATTR_REG_LEVERAGE, the influence
	// figures); nil otherwise so FITTED / RESIDUAL keep their post-fit
	// cost free of an inverse.
	design *hatDesign

	// glm is a GLM refit's design (fitGLMForAttribute): the bound-fit
	// kernel over the refit's own result, and glmScale the prior-weight
	// map w* = c·w (c = 1 but under a probability weight) its
	// covariance reads. Nil on the OLS refit.
	glm      *BoundInfluence
	glmScale float64
}

// FitForAttribute runs the OLS streaming accumulator over the supplied
// records, then dispatches the same per-Penalty solver the engine uses
// to produce coefficients + intercept. When needLeverage is true the
// helper additionally builds the refit's hat design (leverageDesign) —
// ATTR_REG_LEVERAGE on any penalty (the ridge hat, or the active-set hat
// under l1 / elasticnet), and the influence figures (ATTR_REG_INFLUENCE,
// the studentized residuals), which read it beside RSS / DF and which
// model.CheckRefit admits unpenalized only (RSS / DF are NaN on a
// penalized refit).
//
// Listwise deletion mirrors olsEngine.UpdateRow: rows whose target or
// any predictor is null contribute nothing. The returned NObs reflects
// the rows that actually folded into the accumulator.
func FitForAttribute(
	spec *types.RegressionSpec,
	records []Record,
	needLeverage bool,
) (*AttributeFit, error) {
	if spec == nil {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"FitForAttribute called with nil spec",
		)
	}
	if spec.Type == types.REG_GLM && needLeverage {
		// ATTR_REG_LEVERAGE params.family: the GLM refit's hat.
		return fitGLMForAttribute(spec, records)
	}
	if spec.Type != types.REG_OLS {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"FitForAttribute refits REG_OLS, or REG_GLM for ATTR_REG_LEVERAGE (params.family) only",
			map[string]any{"type": string(spec.Type)},
		)
	}
	if err := ValidateRegression(spec); err != nil {
		return nil, err
	}
	if len(spec.Predictors) == 0 {
		return nil, errors.NewCodedError(
			errors.SERVICE_VALIDATION,
			"regression attribute requires at least one predictor",
		)
	}
	if spec.Target == "" {
		return nil, errors.NewCodedError(
			errors.SERVICE_VALIDATION,
			"regression attribute requires a Target field",
		)
	}

	p := len(spec.Predictors)
	rw := newRowWeights(spec)
	acc := newOLSAccumulator(p)
	acc.basis = rw.basis
	xBuf := make([]float64, p)
	for _, rec := range records {
		y, ok := InputValue(rec, spec.Target)
		if !ok {
			continue
		}
		skip := false
		for i, name := range spec.Predictors {
			v, ok := InputValue(rec, name)
			if !ok {
				skip = true
				break
			}
			xBuf[i] = v
		}
		if skip {
			continue
		}
		// A weighted refit (the slot's stamped weight): a row whose
		// weight is invalid or zero is excluded, as in olsEngine.
		w, ok := rw.of(rec)
		if !ok {
			continue
		}
		acc.UpdateRowWeighted(xBuf, y, w)
	}

	// Dispatch to the same solver the engine uses. The solvers themselves
	// finalize the accumulator (mirror the upper triangle of m2XX) and
	// return the coefficient vector + intercept; we only need a subset
	// of the result for the per-row pass.
	var (
		coeffs    []float64
		intercept float64
		rss, df   = math.NaN(), math.NaN()
	)
	switch spec.Penalty {
	case "":
		res, err := solveOLS(acc)
		if err != nil {
			return nil, err
		}
		coeffs = res.Coefficients
		intercept = res.Intercept
		rss, df = res.RSS, res.DF
	case "l2":
		res, err := solveRidge(acc, spec.Alpha, regpenalty.UnpenalizedMask(spec, spec.Predictors))
		if err != nil {
			return nil, err
		}
		coeffs = res.Coefficients
		intercept = res.Intercept
	case "l1":
		res, _, err := solveLasso(acc, spec.Alpha, regpenalty.UnpenalizedMask(spec, spec.Predictors), spec.MaxIters, spec.Tol)
		if err != nil {
			return nil, err
		}
		coeffs = res.Coefficients
		intercept = res.Intercept
	case "elasticnet":
		res, _, err := solveElasticNet(acc, spec.Alpha, spec.L1Ratio, regpenalty.UnpenalizedMask(spec, spec.Predictors), spec.MaxIters, spec.Tol)
		if err != nil {
			return nil, err
		}
		coeffs = res.Coefficients
		intercept = res.Intercept
	default:
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"unknown Penalty for regression attribute: "+spec.Penalty,
			map[string]any{"penalty": spec.Penalty},
		)
	}

	fit := &AttributeFit{
		Coefficients:   append([]float64(nil), coeffs...),
		Intercept:      intercept,
		PredictorOrder: append([]string(nil), spec.Predictors...),
		MeanX:          append([]float64(nil), acc.meanX...),
		NObs:           acc.n,
		SumW:           acc.sumW,
		RSS:            rss,
		DF:             df,
		weights:        rw,
	}

	if needLeverage {
		d, err := leverageDesign(spec, acc, coeffs)
		if err != nil {
			return nil, err
		}
		fit.design = d
	}

	return fit, nil
}

// RowWeight is the weight rec carried in the refit: exactly 1
// unweighted; ok=false when its weight is invalid or zero (the row was
// excluded from the fit).
func (f *AttributeFit) RowWeight(rec Record) (float64, bool) { return f.weights.of(rec) }

// LeverageForRow computes hᵢᵢ for a single row of weight w from the
// refit's hat design (leverage.go) and the streaming predictor means:
//
//	hᵢᵢ = wᵢ · (1/Σw + (xᵢ − μ_x)_Aᵀ · (M2_AA + Σw·λ₂·P_A)⁻¹ · (xᵢ − μ_x)_A)
//
// — unpenalized, the diagonal of W½X(XᵀWX)⁻¹XᵀW½ (R hatvalues on a
// weighted lm), with μ_x the weighted means and M2_xx the Σw-weighted
// centered Gram; penalized, the ridge hat (l2) or the active-set hat
// (l1, elasticnet with its λ₂ term). It is invariant to rescaling the
// weights, so identical across kinds; w = 1 (unweighted, Σw = n) is the
// unweighted 1/n + quad bit for bit.
//
// The quadratic form is FMA-free (explicit float64(...) products,
// TestOLSQR_NoFMAContraction); the inverse itself is a gonum Cholesky
// inverse, whose last bits may differ across architectures.
//
// A GLM refit (params.family) reads its own design instead:
// hᵢᵢ = w*ᵢ·(dμ/dη)²/V(μ)·x̃ᵀ(XᵀW*X)⁻¹x̃ at the final IRLS working
// weights, R hatvalues on the glm (fitGLMForAttribute).
//
// Returns 0 when fit is nil or carries no design — callers should have
// validated upstream.
func LeverageForRow(fit *AttributeFit, x []float64, w float64) float64 {
	if fit != nil && fit.glm != nil {
		// hᵢᵢ = w*ᵢ·wᵢ·x̃ᵀ(XᵀW*X)⁻¹x̃ at the refit's final IRLS working
		// weight wᵢ (glmLeverage).
		h, _, _ := fit.glm.at(x)
		return float64(float64(w*fit.glmScale) * h)
	}
	if fit == nil || fit.design == nil || fit.NObs == 0 {
		return 0
	}
	return fit.design.at(x, fit.MeanX, w, fit.SumW)
}
