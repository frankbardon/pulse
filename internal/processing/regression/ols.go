package regression

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regbounds"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/internal/regsolve"
	"github.com/frankbardon/pulse/types"
)

// olsEngine is the Phase 1 ordinary-least-squares fit engine. It
// implements three flavors of the same closed-form solve:
//
//   - UpdateRow / Finalize (StreamingEngine): driven row-by-row by the
//     single-pass orchestrator. Sufficient stats accumulate in O(p²)
//     state regardless of n.
//   - FitBuffered (BufferedEngine): consumes a materialized record
//     slice and feeds it through the same accumulator. Identical math;
//     this is the path the buffered orchestrator takes.
//   - Fit (Engine): the Phase 0 signature still in use by callers that
//     pre-date Phase 1. Returns INSUFFICIENT_DATA since no records were
//     supplied; kept here so the universal-stub registry path doesn't
//     mis-fire when a request arrives with the streaming gate off.
//
// olsEngine handles ordinary least squares with optional ridge / lasso /
// elasticnet regularization. The streaming Welford accumulator is the
// same across all four solver flavors — only the finalize-time solve
// branches on Penalty. The Bayes/GLM operators (Phases 3-4) stay on
// the notImplementedEngine path until their dedicated engines land.
type olsEngine struct {
	spec   *types.RegressionSpec
	schema *encoding.Schema
	acc    *olsAccumulator
	// weights is the slot's stamped weight (zero value: unweighted).
	weights rowWeights
	// robust is set when spec.Covariance selects a non-classical
	// estimator: rows then also need their cluster / order_by values, and
	// finalize runs the second pass (ols_covariance.go).
	robust bool
	// finalized flips when Finalize / FitBuffered has run; further
	// UpdateRow calls would corrupt the fit and are rejected.
	finalized bool
	// convergedIters records the number of coordinate-descent cycles for
	// the regularized solvers. Zero for the closed-form paths (unpenalized
	// and ridge).
	convergedIters int
	// approximateSE flags the L1-penalized fits (l1 / elasticnet) whose
	// standard errors, p-values and intervals are undefined (NaN on
	// every key); the service attaches
	// PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED to the result.
	approximateSE bool
	// activeBounds are the RegressionSpec.Bounds slope indices a bounded
	// fit held at 0 (empty: no bound binds, the fit is plain OLS).
	activeBounds []int
}

// newOLSEngine validates the spec and constructs an empty accumulator
// sized to the predictor count. The spec is assumed already to be
// type-checked against the schema by descriptor.validateRegressions.
func newOLSEngine(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error) {
	if spec.Type != types.REG_OLS {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"olsEngine constructed for non-OLS spec",
			map[string]any{"type": string(spec.Type)},
		)
	}
	// Phase 2: regularized variants land. Spec validation rejects
	// invalid penalty/alpha/l1_ratio combos before the accumulator is
	// even constructed.
	if err := ValidateRegression(spec); err != nil {
		return nil, err
	}
	if spec.Resample != "" || spec.Selection != "" {
		// Phase 5: resample / selection modifiers dispatch through the
		// modifierEngine wrapper which orchestrates LOO / bootstrap
		// refits and AIC/BIC-driven subset search around the inner OLS
		// solver. Both modifiers force the buffered path.
		if len(spec.Predictors) == 0 {
			return nil, errors.NewCodedErrorWithDetails(
				errors.SERVICE_VALIDATION,
				"REG_OLS requires at least one predictor",
				map[string]any{"name": spec.Name},
			)
		}
		if spec.Target == "" {
			return nil, errors.NewCodedErrorWithDetails(
				errors.SERVICE_VALIDATION,
				"REG_OLS requires a Target field",
				map[string]any{"name": spec.Name},
			)
		}
		return newModifierEngine(spec, schema)
	}
	if len(spec.Predictors) == 0 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"REG_OLS requires at least one predictor",
			map[string]any{"name": spec.Name},
		)
	}
	if spec.Target == "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"REG_OLS requires a Target field",
			map[string]any{"name": spec.Name},
		)
	}
	weights := newRowWeights(spec)
	acc := newOLSAccumulator(len(spec.Predictors))
	acc.basis = weights.basis
	if !regsolve.FitsIntercept(spec) {
		// intercept: false (internal/regsolve admits it only unpenalised,
		// unbounded, classical, on the QR solver).
		acc.fitThroughOrigin()
	}
	return &olsEngine{
		spec:    spec,
		schema:  schema,
		acc:     acc,
		weights: weights,
		robust:  spec.Covariance.Robust(),
	}, nil
}

// UpdateRow folds one record into the running sufficient statistics.
// A row with any null predictor or null target is dropped from the fit
// (listwise deletion) — the same policy R / numpy.lstsq apply when
// receiving missing values. Returns nil unconditionally; missingness is
// not an error, just a non-contribution.
func (e *olsEngine) UpdateRow(rec Record) error {
	if e.finalized {
		return errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"olsEngine.UpdateRow called after Finalize",
		)
	}
	x, y, w, ok := e.fitRow(rec)
	if !ok {
		return nil
	}
	e.acc.UpdateRowWeighted(x, y, w)
	return nil
}

// fitRow reads rec's predictors, target and weight, ok=false when the
// row does not enter the fit: a null target or predictor (listwise),
// under a robust covariance a null cluster / order_by value, or an
// invalid or zero weight (judged only once the row is otherwise
// complete). The robust second pass re-reads the fitted rows through
// it, so both passes see exactly the same rows.
func (e *olsEngine) fitRow(rec Record) (x []float64, y, w float64, ok bool) {
	y, ok = InputValue(rec, e.spec.Target)
	if !ok {
		return nil, 0, 0, false
	}
	x = make([]float64, len(e.spec.Predictors))
	for i, name := range e.spec.Predictors {
		v, ok := InputValue(rec, name)
		if !ok {
			return nil, 0, 0, false
		}
		x[i] = v
	}
	if e.robust {
		if _, _, ok := e.robustKeys(rec); !ok {
			return nil, 0, 0, false
		}
	}
	w, ok = e.weights.of(rec)
	if !ok {
		return nil, 0, 0, false
	}
	return x, y, w, true
}

// Finalize closes the streaming fit and returns the populated result.
// Idempotent in the failure case: a rank-deficient fit returns the
// same error on every invocation.
func (e *olsEngine) Finalize() (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"olsEngine.Finalize called twice",
		)
	}
	e.finalized = true
	return e.finalizeFromAccumulator(nil)
}

// FitBuffered runs the same accumulator pipeline over an in-memory
// record slice. The buffered orchestrator path uses this entry point;
// the math is bit-for-bit identical to the streaming path because both
// drive the same accumulator with the same Welford recurrence.
func (e *olsEngine) FitBuffered(records []Record) (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"olsEngine.FitBuffered called after Finalize",
		)
	}
	for _, rec := range records {
		if err := e.UpdateRow(rec); err != nil {
			return nil, err
		}
	}
	e.finalized = true
	return e.finalizeFromAccumulator(records)
}

// Fit satisfies the Phase 0 Engine interface for code paths that
// instantiate an engine without any records. With no rows folded into
// the accumulator the fit is degenerate; surface
// PROCESSING_REGRESSION_INSUFFICIENT_DATA so callers receive the same
// error shape as a real underfilled request. Real usage flows through
// UpdateRow/Finalize or FitBuffered; this method exists so the registry
// dispatch matches the Phase 0 contract.
func (e *olsEngine) Fit() (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"olsEngine.Fit called after Finalize",
		)
	}
	e.finalized = true
	return e.finalizeFromAccumulator(nil)
}

// finalizeFromAccumulator solves the system and packages the result.
// Shared by every entry point so the post-solve formatting lives in
// exactly one place. Dispatches to one of four solvers based on
// spec.Penalty:
//
//	""           → solveOLS    (Phase 1, closed-form, full SE)
//	"l2"         → solveRidge  (closed-form on augmented Gram, full SE)
//	"l1"         → solveLasso  (coordinate descent, SE undefined: NaN)
//	"elasticnet" → solveElasticNet (coordinate descent, SE undefined: NaN)
//
// All four solvers consume the same streaming Welford accumulator;
// regularized variants iterate at finalize, not over rows.
//
// records are the rows a buffered fit folded (nil on the streaming
// path). A robust RegressionSpec.Covariance reads them a second time
// (olsSandwich): its covariance replaces the classical one behind the
// standard errors, p-values, intervals and Vcov, and a cluster fit's
// inference runs on G − 1 df. RegressionSpec.Streamable keeps a robust
// spec off the streaming path, so records is never nil for one.
func (e *olsEngine) finalizeFromAccumulator(records []Record) (*types.RegressionResult, error) {
	solve, iters, approxSE, err := e.dispatchSolve()
	if err != nil {
		return nil, err
	}
	e.convergedIters = iters
	e.approximateSE = approxSE

	var robust *olsRobust
	infDF := solve.DF
	if e.robust {
		if records == nil {
			return nil, errors.NewCodedError(
				errors.PROCESSING_INTERNAL,
				"REG_OLS robust covariance needs the buffered path (RegressionSpec.Streamable)",
			)
		}
		if robust, err = e.olsSandwich(solve, records); err != nil {
			return nil, err
		}
		solve.StdErrors = robust.stdErrors
		if robust.fit.hasDF {
			infDF = robust.fit.df
		}
	}

	predictors := e.spec.Predictors
	coefMap := make(map[string]float64, len(predictors)+1)
	seMap := make(map[string]float64, len(predictors)+1)
	pMap := make(map[string]float64, len(predictors)+1)

	coefMap[InterceptKey] = solve.Intercept
	seMap[InterceptKey] = solve.StdErrors[0]
	pMap[InterceptKey] = pValueForCoefficient(solve.Intercept, solve.StdErrors[0], infDF)

	for i, name := range predictors {
		coefMap[name] = solve.Coefficients[i]
		// An l1 / elasticnet solve reports NaN standard errors on every
		// coefficient (dropped or kept) and the intercept, so every key
		// is present with a NaN p-value — null on the wire, never a p of
		// 0 that would read as maximally significant.
		seMap[name] = solve.StdErrors[i+1]
		pMap[name] = pValueForCoefficient(solve.Coefficients[i], solve.StdErrors[i+1], infDF)
	}

	res := &types.RegressionResult{
		Name:           e.spec.Name,
		Type:           types.REG_OLS,
		Penalty:        e.spec.Penalty,
		Alpha:          e.spec.Alpha,
		L1Ratio:        e.spec.L1Ratio,
		Coefficients:   coefMap,
		StdErrors:      seMap,
		PValues:        pMap,
		R2:             solve.R2,
		AdjR2:          solve.AdjR2,
		NObs:           e.acc.n,
		ResidualStdErr: solve.ResidualStdErr,
		ConvergedIters: e.convergedIters,
	}
	e.weights.note(res, e.acc.sumW, e.acc.sumWSq)
	if e.spec.Penalty == "" && len(e.activeBounds) == 0 && logLikDefined(e.weights.basis) {
		// logLik.lm's maximised Gaussian likelihood and AIC (loglik.go):
		// the coefficients plus σ, on N* (n, or Σw — the rep() expansion
		// — under a frequency weight); absent under a probability weight,
		// a penalty or a binding bound (a bounds fit no bound binds is
		// the plain fit, bit for bit).
		rank := len(predictors) + 1
		if solve.throughOrigin {
			rank--
		}
		ll, df := olsLogLik(solve.RSS, e.acc.nStar(), rank)
		attachLogLik(res, ll, df)
	}
	// t on the residual df N* − p − 1 (the N* rule, weighting.md): the
	// same df the p-values read. Lasso / elastic net intervals are NaN
	// on every key (their standard errors are).
	// A cluster fit reads G − 1 df instead.
	attachIntervals(res, resolvedConfLevel(e.spec), infDF, true)
	if e.spec.Margins != nil {
		// OLS's identity link: every marginal effect is its coefficient,
		// on the coefficient table's t (margins.go) — no row pass, so the
		// block streams with the fit; mem's point is the accumulator's
		// (weighted) predictor means.
		res.Margins = coefficientMargins(e.spec, res, marginsInference{level: resolvedConfLevel(e.spec), df: infDF, studentT: true}, e.acc.meanX)
	}
	if len(e.activeBounds) != 0 {
		// A binding bound (RegressionSpec.Bounds): the standard errors,
		// p-values and intervals above are NaN on every key; the service
		// attaches PULSE_ADVISORY_CONSTRAINT_ACTIVE from ActiveBounds.
		res.ActiveBounds = make([]string, len(e.activeBounds))
		for i, j := range e.activeBounds {
			res.ActiveBounds[i] = predictors[j]
		}
	}
	switch {
	case robust != nil:
		res.Covariance = robust.fit.info
		if e.spec.Vcov {
			attachVcov(res, predictors, robust.fit.v)
		}
	case e.spec.Vcov && len(e.activeBounds) != 0:
		attachVcov(res, predictors, boundedVcov(len(predictors)))
		// σ̂² stays defined under a binding bound: the face predict
		// reads off the spec (model.FitDesign) carries it either way.
		res.Dispersion = solve.sigma2Gram
	case e.spec.Vcov:
		attachVcov(res, predictors, olsVcov(solve, e.acc.meanX))
		if e.spec.Penalty == "" && res.Vcov != nil {
			// The σ̂² the classical vcov is scaled by (Vcov/φ̂ =
			// (XᵀX)⁻¹ — what a bound fit's influence figures read).
			res.Dispersion = solve.sigma2Gram
		}
	}
	return res, nil
}

// dispatchSolve routes to the per-Penalty solver. Returns
// (solve, iterations, approximateSE, err). iterations is non-zero only
// for the coordinate-descent solvers (lasso / elasticnet).
// approximateSE is true for the L1-penalized fits, whose standard
// errors are undefined (NaN).
func (e *olsEngine) dispatchSolve() (*olsSolveResult, int, bool, error) {
	switch e.spec.Penalty {
	case "":
		if mask := regbounds.Mask(e.spec, e.spec.Predictors); mask != nil {
			res, active, err := solveNNLS(e.acc, mask)
			e.activeBounds = active
			return res, 0, false, err
		}
		if e.spec.Solver == regsolve.SolverNormal {
			res, err := solveOLSNormal(e.acc)
			return res, 0, false, err
		}
		res, err := solveOLS(e.acc)
		return res, 0, false, err
	case "l2":
		res, err := solveRidge(e.acc, e.spec.Alpha, regpenalty.UnpenalizedMask(e.spec, e.spec.Predictors))
		return res, 0, false, err
	case "l1":
		res, iters, err := solveLasso(e.acc, e.spec.Alpha, e.spec.MaxIters, e.spec.Tol)
		return res, iters, true, err
	case "elasticnet":
		res, iters, err := solveElasticNet(e.acc, e.spec.Alpha, e.spec.L1Ratio, e.spec.MaxIters, e.spec.Tol)
		return res, iters, true, err
	default:
		// Spec validation rejects unknown values upstream; the default
		// branch exists so a future Penalty enum extension surfaces a
		// clear error if the validator is bypassed.
		return nil, 0, false, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_OLS Penalty must be one of \"\", \"l1\", \"l2\", \"elasticnet\"; got "+e.spec.Penalty,
			map[string]any{"penalty": e.spec.Penalty},
		)
	}
}

// HasApproximateStdErrors reports whether the engine produced a fit
// whose StdErrors / PValues are undefined (NaN on every key): exactly
// the L1-penalized fits (Penalty=="l1" or "elasticnet").
func (e *olsEngine) HasApproximateStdErrors() bool {
	return e.approximateSE
}

// InterceptKey is the map key used for the synthesized intercept entry
// in RegressionResult.Coefficients / StdErrors / PValues. Picked to be
// human-readable and impossible to collide with a real field name (no
// schema field carries parentheses).
const InterceptKey = "(intercept)"
