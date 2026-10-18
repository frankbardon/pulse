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
	// The slot's stamped weight (the weighted — JK1 — jackknife; nil on
	// selection / bootstrap, which the weight resolver refuses): each
	// listwise-complete row folds with its weight exactly as the
	// olsEngine does; unweighted every row weighs exactly 1, which is
	// UpdateRow bit for bit.
	rw := newRowWeights(spec)
	if p == 0 {
		// Intercept-only model: β₀ = ȳ; RSS = Σ(y − ȳ)² = m2YY.
		acc := newOLSAccumulator(0)
		acc.basis = rw.basis
		buf := make([]float64, 0)
		for _, rec := range records {
			y, ok := rec.NumericValue(target)
			if !ok {
				continue
			}
			w, ok := rw.of(rec)
			if !ok {
				continue
			}
			acc.UpdateRowWeighted(buf, y, w)
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
	acc.basis = rw.basis
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
		w, ok := rw.of(rec)
		if !ok {
			continue
		}
		acc.UpdateRowWeighted(xBuf, y, w)
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
// It runs the one IRLS loop glmEngine.FitBuffered runs (irlsFit), so
// the modifier drivers refit without recreating the whole engine. The intercept-only baseline (p=0)
// short-circuits: the saturated null fit has μᵢ = ȳ for every row and
// its deviance is the engine's NullDeviance.
//
// Caller responsibilities:
//   - selectFamily(family, link) has already been validated.
//   - spec.MaxIters / spec.Tol are usable (defaults applied upstream).
//   - knobs, when non-nil, is the spec whose Penalty / Alpha / L1Ratio
//     penalise every refit (glm_penalty.go; nil or Penalty "" = the MLE);
//     nothing else on it is read.
//
// Returns:
//   - refitResult with Deviance populated (RSS untouched; the
//     unpenalised deviance at the penalised estimate).
//   - PROCESSING_REGRESSION_INSUFFICIENT_DATA when n < p + 1.
//   - PROCESSING_REGRESSION_RANK_DEFICIENT / SEPARATION / NO_CONVERGE
//     bubbled untouched.
func glmFitter(records []Record, target string, activePredictors []string, fam glmFamily, maxIters int, tol float64, knobs *types.RegressionSpec) (*refitResult, error) {
	return glmRefit(records, target, activePredictors, fam, maxIters, tol, knobs, nil)
}

// glmRefit is glmFitter with an optional warm start (irlsProblem.Start:
// the intercept then one coefficient per active predictor; nil starts
// from the family's per-row values). knobs' stamped Weight, when set,
// weights the refit exactly as glmEngine.FitBuffered weights the full
// fit (buildWeightedDesign: prior weights w*, the penalty's N* = Σw*) —
// the weighted (JK1) jackknife; selection and bootstrap never carry one
// (the weight resolver refuses it).
func glmRefit(records []Record, target string, activePredictors []string, fam glmFamily, maxIters int, tol float64, knobs *types.RegressionSpec, start []float64) (*refitResult, error) {
	if maxIters <= 0 {
		maxIters = defaultGLMMaxIters
	}
	if tol <= 0 {
		tol = defaultGLMTol
	}

	p := len(activePredictors)
	var rw rowWeights
	if knobs != nil {
		rw = newRowWeights(knobs)
	}
	var xRows, yVec, prior []float64
	if rw.weighted() {
		xRows, yVec, prior, _, _, _ = buildWeightedDesign(records, activePredictors, target, rw, nil)
	} else {
		xRows, yVec = buildDesign(records, activePredictors, target)
	}
	n := len(yVec)
	if n < p+1 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"REG_GLM refit requires n ≥ p + 1 observations after listwise null deletion",
			map[string]any{"n": n, "p": p, "required": p + 1, "active_predictors": activePredictors},
		)
	}
	yBar := mean(yVec)
	if prior != nil {
		yBar = weightedMean(yVec, prior)
	}

	if p == 0 {
		// Intercept-only fit: β₀ = g(ȳ), deviance = NullDeviance.
		mu0 := fam.SafeMean(yBar)
		nullDev := 0.0
		for i := 0; i < n; i++ {
			if prior != nil {
				nullDev += prior[i] * fam.Deviance(yVec[i], mu0)
				continue
			}
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

	// The same weighted IRLS loop glmEngine.FitBuffered runs (every
	// prior weight 1 unless the slot is weighted), without the final
	// (XᵀWX)⁻¹: selection / resample need only β, deviance and n.
	pr := &irlsProblem{
		X: xRows, Y: yVec, Prior: prior, K: p + 1,
		Columns:  append([]string{InterceptKey}, activePredictors...),
		Family:   fam,
		MaxIters: maxIters, Tol: tol,
		Start: start,
	}
	// The spec's penalty, N* = Σw* (n unweighted: every prior weight 1).
	nStar := float64(n)
	if prior != nil {
		nStar = sum(prior)
	}
	if pen := newGLMPenalty(knobs, nStar); pen != nil {
		pr.Penalty = pen
	}
	sol, err := irlsFit(pr)
	if err != nil {
		return nil, err
	}
	beta, deviance := sol.Beta, sol.Deviance

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
