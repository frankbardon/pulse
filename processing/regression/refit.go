package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// refitResult is the slim summary the modifier drivers (resample,
// selection) consume after refitting an inner OLS or GLM engine on a
// record subset under a predictor subset.
//
// Slopes is in the *predictor-subset* order — the caller (modifier
// driver) remembers which schema-level predictor each slot in
// activePredictors corresponded to. RSS and Deviance are the OLS and
// GLM goodness-of-fit summaries respectively; only one is populated
// per family (the other stays zero).
type refitResult struct {
	Intercept float64
	Slopes    []float64 // length len(activePredictors)
	N         int       // observations used after listwise null deletion
	RSS       float64   // OLS only
	Deviance  float64   // GLM only
	// Mean-y so selection can fall back to the intercept-only RSS without
	// instantiating an engine when activePredictors is empty.
	MeanY float64
}

// olsFitter fits an OLS variant (unpenalized or any regularized form)
// over a record subset restricted to the given predictor subset.
//
// The fitter reuses the existing per-Penalty solver (solveOLS,
// solveRidge, solveLasso, solveElasticNet) so the modifier path picks
// up regularization behavior automatically. Operating on the
// (target, activePredictors) subset means the accumulator is sized to
// len(activePredictors), not len(spec.Predictors), which is what the
// selection driver wants when comparing nested models.
//
// activePredictors is allowed to be empty: the intercept-only fit
// short-circuits without touching the solver — its RSS is just m2YY
// and its β is ȳ.
//
// Returns:
//   - refitResult on success.
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA when n < len(activePredictors) + 1.
//   - any solver error (rank deficient, etc.) bubbled untouched.
func olsFitter(records []Record, target string, activePredictors []string, spec *types.RegressionSpec) (*refitResult, error) {
	p := len(activePredictors)
	if p == 0 {
		// Intercept-only model: β₀ = ȳ; RSS = Σ(y − ȳ)² = m2YY.
		acc := newOLSAccumulator(0)
		buf := make([]float64, 0)
		for _, rec := range records {
			y, ok := rec.NumericValue(target)
			if !ok {
				continue
			}
			acc.UpdateRow(buf, y)
		}
		if acc.n == 0 {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
				"regression refit has zero observations after listwise null deletion",
				map[string]any{"target": target, "active_predictors": activePredictors},
			)
		}
		return &refitResult{
			Intercept: acc.meanY,
			Slopes:    nil,
			N:         acc.n,
			RSS:       acc.m2YY,
			MeanY:     acc.meanY,
		}, nil
	}

	acc := newOLSAccumulator(p)
	xBuf := make([]float64, p)
	for _, rec := range records {
		y, ok := rec.NumericValue(target)
		if !ok {
			continue
		}
		skip := false
		for j, name := range activePredictors {
			v, ok := rec.NumericValue(name)
			if !ok {
				skip = true
				break
			}
			xBuf[j] = v
		}
		if skip {
			continue
		}
		acc.UpdateRow(xBuf, y)
	}
	if acc.n < p+1 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"regression refit requires n ≥ p + 1 observations after listwise null deletion",
			map[string]any{"n": acc.n, "p": p, "required": p + 1, "active_predictors": activePredictors},
		)
	}

	// Route to the same solver the unmodified spec would have used.
	var solve *olsSolveResult
	var err error
	switch spec.Penalty {
	case "":
		solve, err = solveOLS(acc)
	case "l2":
		solve, err = solveRidge(acc, spec.Alpha)
	case "l1":
		solve, _, err = solveLasso(acc, spec.Alpha, spec.MaxIters, spec.Tol)
	case "elasticnet":
		solve, _, err = solveElasticNet(acc, spec.Alpha, spec.L1Ratio, spec.MaxIters, spec.Tol)
	default:
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_OLS refit: unknown Penalty "+spec.Penalty,
			map[string]any{"penalty": spec.Penalty},
		)
	}
	if err != nil {
		return nil, err
	}
	slopes := make([]float64, p)
	copy(slopes, solve.Coefficients)
	return &refitResult{
		Intercept: solve.Intercept,
		Slopes:    slopes,
		N:         acc.n,
		RSS:       solve.RSS,
		MeanY:     acc.meanY,
	}, nil
}

// glmFitter fits a GLM (family + link) over a record subset restricted
// to the given predictor subset.
//
// This is the same IRLS loop that drives glmEngine.FitBuffered, factored
// into a free function so the modifier drivers can call it without
// recreating the whole engine. The intercept-only baseline (p=0)
// short-circuits: the saturated null fit has μᵢ = ȳ for every row and
// its deviance is the engine's NullDeviance.
//
// Caller responsibilities:
//   - selectFamily(family, link) has already been validated.
//   - spec.MaxIters / spec.Tol are usable (defaults applied upstream).
//
// Returns:
//   - refitResult with Deviance populated (RSS untouched).
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA when n < p + 1.
//   - PROCESSING_REGRESSION_NO_CONVERGE / RANK_DEFICIENT bubbled untouched.
func glmFitter(records []Record, target string, activePredictors []string, fam glmFamily, maxIters int, tol float64) (*refitResult, error) {
	if maxIters <= 0 {
		maxIters = defaultGLMMaxIters
	}
	if tol <= 0 {
		tol = defaultGLMTol
	}

	p := len(activePredictors)
	xRows, yVec := buildDesign(records, activePredictors, target)
	n := len(yVec)
	if n < p+1 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"REG_GLM refit requires n ≥ p + 1 observations after listwise null deletion",
			map[string]any{"n": n, "p": p, "required": p + 1, "active_predictors": activePredictors},
		)
	}
	yBar := mean(yVec)

	if p == 0 {
		// Intercept-only fit: β₀ = g(ȳ), deviance = NullDeviance.
		mu0 := fam.SafeMean(yBar)
		nullDev := 0.0
		for i := 0; i < n; i++ {
			nullDev += fam.Deviance(yVec[i], mu0)
		}
		nullDev *= 2
		return &refitResult{
			Intercept: fam.LinkFunc(mu0),
			Slopes:    nil,
			N:         n,
			Deviance:  nullDev,
			MeanY:     yBar,
		}, nil
	}

	// Run IRLS — same recurrence as glmEngine.FitBuffered, simplified to
	// drop the SE bookkeeping (selection / resample don't need final
	// XᵀWX⁻¹; just β, deviance, and n).
	beta, conv, err := irlsFit(xRows, yVec, p, fam, maxIters, tol)
	if err != nil {
		return nil, err
	}
	_ = conv

	// Compute deviance at the converged β.
	eta := make([]float64, n)
	for i := 0; i < n; i++ {
		s := 0.0
		xi := xRows[i*(p+1) : (i+1)*(p+1)]
		for a := 0; a < p+1; a++ {
			s += xi[a] * beta[a]
		}
		eta[i] = s
	}
	deviance := 0.0
	for i := 0; i < n; i++ {
		mu := fam.InverseLink(eta[i])
		deviance += fam.Deviance(yVec[i], mu)
	}
	deviance *= 2

	intercept := beta[0]
	slopes := make([]float64, p)
	copy(slopes, beta[1:])
	return &refitResult{
		Intercept: intercept,
		Slopes:    slopes,
		N:         n,
		Deviance:  deviance,
		MeanY:     yBar,
	}, nil
}

// finiteOrInf returns the value unchanged unless it is NaN, in which
// case it returns +Inf. Used by the criterion-driven selection drivers
// so a numerically-degenerate candidate never accidentally wins the
// "lowest score" tournament.
func finiteOrInf(v float64) float64 {
	if math.IsNaN(v) {
		return math.Inf(1)
	}
	return v
}
