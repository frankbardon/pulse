package regression

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
// olsEngine handles unpenalized OLS only (Penalty == ""). Penalized
// variants (Phase 2) and the Bayes/GLM operators (Phases 3-4) stay on
// the notImplementedEngine path until their dedicated engines land.
type olsEngine struct {
	spec   *types.RegressionSpec
	schema *encoding.Schema
	acc    *olsAccumulator
	// finalized flips when Finalize / FitBuffered has run; further
	// UpdateRow calls would corrupt the fit and are rejected.
	finalized bool
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
	if spec.Penalty != "" {
		// Phase 2 will route penalized OLS to a regularized engine.
		// Phase 1 hands these back to the not-implemented stub so the
		// rest of the contract (error code, manifest, predict) stays
		// stable.
		return newNotImplemented(spec, schema)
	}
	if spec.Resample != "" || spec.Selection != "" {
		// Modifier wrappers land in Phase 5; until then, route to the
		// stub so the response carries PROCESSING_REGRESSION_NOT_IMPLEMENTED
		// with the operator name in details (same as Phase 0).
		return newNotImplemented(spec, schema)
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
	return &olsEngine{
		spec:   spec,
		schema: schema,
		acc:    newOLSAccumulator(len(spec.Predictors)),
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
	y, ok := rec.NumericValue(e.spec.Target)
	if !ok {
		return nil
	}
	x := make([]float64, len(e.spec.Predictors))
	for i, name := range e.spec.Predictors {
		v, ok := rec.NumericValue(name)
		if !ok {
			return nil
		}
		x[i] = v
	}
	e.acc.UpdateRow(x, y)
	return nil
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
	return e.finalizeFromAccumulator()
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
	return e.finalizeFromAccumulator()
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
	return e.finalizeFromAccumulator()
}

// finalizeFromAccumulator solves the system and packages the result.
// Shared by every entry point so the post-solve formatting lives in
// exactly one place.
func (e *olsEngine) finalizeFromAccumulator() (*types.RegressionResult, error) {
	solve, err := solveOLS(e.acc)
	if err != nil {
		return nil, err
	}

	predictors := e.spec.Predictors
	coefMap := make(map[string]float64, len(predictors)+1)
	seMap := make(map[string]float64, len(predictors)+1)
	pMap := make(map[string]float64, len(predictors)+1)

	coefMap[InterceptKey] = solve.Intercept
	seMap[InterceptKey] = solve.StdErrors[0]
	pMap[InterceptKey] = pValueForCoefficient(solve.Intercept, solve.StdErrors[0], solve.DF)

	for i, name := range predictors {
		coefMap[name] = solve.Coefficients[i]
		seMap[name] = solve.StdErrors[i+1]
		pMap[name] = pValueForCoefficient(solve.Coefficients[i], solve.StdErrors[i+1], solve.DF)
	}

	return &types.RegressionResult{
		Name:           e.spec.Name,
		Type:           types.REG_OLS,
		Penalty:        "", // unpenalized fit
		Coefficients:   coefMap,
		StdErrors:      seMap,
		PValues:        pMap,
		R2:             solve.R2,
		AdjR2:          solve.AdjR2,
		NObs:           e.acc.n,
		ResidualStdErr: solve.ResidualStdErr,
	}, nil
}

// InterceptKey is the map key used for the synthesized intercept entry
// in RegressionResult.Coefficients / StdErrors / PValues. Picked to be
// human-readable and impossible to collide with a real field name (no
// schema field carries parentheses).
const InterceptKey = "(intercept)"
