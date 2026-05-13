package regression

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// AttributeFit is the minimal package of post-fit quantities the
// per-row regression attributes (ATTR_REG_FITTED / ATTR_REG_RESIDUAL /
// ATTR_REG_LEVERAGE) need to emit their second-pass per-row values.
//
// The full RegressionResult / olsSolveResult bundle is intentionally
// not returned: attribute callers do not surface regression result
// blocks; they only need the coefficient vector (for ŷᵢ / residuals)
// and, in the leverage case, the centered-Gram inverse plus the
// streaming predictor means (for the standard hat-matrix identity
//
//	hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)).
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

	// GramInverse is M2_xx⁻¹ — the inverse of the centered predictor
	// Gram matrix used to compute leverage. Populated only when the
	// caller requests it (ATTR_REG_LEVERAGE); nil otherwise so the
	// regularized solvers can keep their post-fit cost free of an
	// additional inverse. Stored as a p×p gonum SymDense.
	GramInverse *mat.SymDense
}

// FitForAttribute runs the OLS streaming accumulator over the supplied
// records, then dispatches the same per-Penalty solver the engine uses
// to produce coefficients + intercept. When needLeverage is true the
// helper additionally produces the centered-Gram inverse from the
// (unpenalized) Cholesky factorisation.
//
// The helper enforces the per-attribute spec rules upstream of the
// solver: ATTR_REG_LEVERAGE accepts only unpenalized OLS (Penalty=="").
// Callers gate on that constraint before invoking with
// needLeverage=true; this function returns PROCESSING_CONFIG if the
// combination ever escapes upstream validation.
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
	if spec.Type != types.REG_OLS {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"FitForAttribute supports only REG_OLS specs; ATTR_REG_* delegates only to OLS in this phase",
			map[string]any{"type": string(spec.Type)},
		)
	}
	if err := ValidateRegression(spec); err != nil {
		return nil, err
	}
	if needLeverage && spec.Penalty != "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"ATTR_REG_LEVERAGE supports only unpenalized OLS — penalized leverage / GLM leverage not implemented",
			map[string]any{"penalty": spec.Penalty},
		)
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
	acc := newOLSAccumulator(p)
	xBuf := make([]float64, p)
	for _, rec := range records {
		y, ok := rec.NumericValue(spec.Target)
		if !ok {
			continue
		}
		skip := false
		for i, name := range spec.Predictors {
			v, ok := rec.NumericValue(name)
			if !ok {
				skip = true
				break
			}
			xBuf[i] = v
		}
		if skip {
			continue
		}
		acc.UpdateRow(xBuf, y)
	}

	// Dispatch to the same solver the engine uses. The solvers themselves
	// finalize the accumulator (mirror the upper triangle of m2XX) and
	// return the coefficient vector + intercept; we only need a subset
	// of the result for the per-row pass.
	var (
		coeffs    []float64
		intercept float64
	)
	switch spec.Penalty {
	case "":
		res, err := solveOLS(acc)
		if err != nil {
			return nil, err
		}
		coeffs = res.Coefficients
		intercept = res.Intercept
	case "l2":
		res, err := solveRidge(acc, spec.Alpha)
		if err != nil {
			return nil, err
		}
		coeffs = res.Coefficients
		intercept = res.Intercept
	case "l1":
		res, _, err := solveLasso(acc, spec.Alpha, spec.MaxIters, spec.Tol)
		if err != nil {
			return nil, err
		}
		coeffs = res.Coefficients
		intercept = res.Intercept
	case "elasticnet":
		res, _, err := solveElasticNet(acc, spec.Alpha, spec.L1Ratio, spec.MaxIters, spec.Tol)
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
	}

	if needLeverage {
		// Solve was unpenalized; the same Cholesky-based inverse the
		// solver uses for standard errors gives us M2_xx⁻¹ for the
		// hat-matrix identity. Recompute here rather than threading
		// invXX out of solveOLS to keep the solver API stable.
		sym := mat.NewSymDense(p, append([]float64(nil), acc.m2XX...))
		var chol mat.Cholesky
		if ok := chol.Factorize(sym); !ok {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"leverage requires invertible centered Gram matrix (linearly dependent or constant predictors)",
				map[string]any{"n": acc.n, "p": p},
			)
		}
		var invXX mat.SymDense
		if err := chol.InverseTo(&invXX); err != nil {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"leverage: Cholesky inverse failed for the centered Gram matrix",
				map[string]any{"gonum_error": err.Error()},
			)
		}
		// solveOLS already mirrors the upper triangle into the lower via
		// acc.finalize(); the inverse is already symmetric. Copy the
		// matrix so the per-row computation can be index-driven.
		out := mat.NewSymDense(p, nil)
		for i := 0; i < p; i++ {
			for j := i; j < p; j++ {
				out.SetSym(i, j, invXX.At(i, j))
			}
		}
		fit.GramInverse = out
	}

	return fit, nil
}

// LeverageForRow computes hᵢᵢ for a single row given a centered-Gram
// inverse and the streaming predictor means. The closed-form identity
// for OLS with an intercept and centered predictors is
//
//	hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)
//
// Returns 0 when fit is nil or fit.GramInverse is nil — callers should
// have validated upstream. Rows with any null predictor produce a
// zero leverage; the attribute layer decides whether to emit zero or
// flag missingness via the schema.
func LeverageForRow(fit *AttributeFit, x []float64) float64 {
	if fit == nil || fit.GramInverse == nil || fit.NObs == 0 {
		return 0
	}
	p := len(fit.PredictorOrder)
	// Centered predictor vector.
	dx := make([]float64, p)
	for i := 0; i < p; i++ {
		dx[i] = x[i] - fit.MeanX[i]
	}
	quad := 0.0
	for i := 0; i < p; i++ {
		row := 0.0
		for j := 0; j < p; j++ {
			row += fit.GramInverse.At(i, j) * dx[j]
		}
		quad += dx[i] * row
	}
	return 1.0/float64(fit.NObs) + quad
}
