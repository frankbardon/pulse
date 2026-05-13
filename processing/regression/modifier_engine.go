package regression

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// modifierEngine wraps an inner regression engine with the Resample
// and Selection modifiers. Both modifiers force the buffered path —
// they refit the model many times. The engine implements Engine
// (Fit returns INSUFFICIENT_DATA when no records arrive) and
// BufferedEngine (FitBuffered runs the modifier dispatch).
//
// Dispatch order: Selection is applied first (picks the active set);
// the chosen subset feeds Resample if both are set. When only one
// modifier is set the other branch is a no-op.
//
// The wrapper is built by newOLSEngine / newGLMEngine when the spec
// has a non-empty Resample or Selection. Bayes never reaches this
// path: validateBayesLinearSpec rejects modifier-bearing specs at
// validation time.
type modifierEngine struct {
	spec      *types.RegressionSpec
	schema    *encoding.Schema
	family    string // "OLS" or "GLM"
	glmFam    glmFamily
	finalized bool
}

// newModifierEngine constructs a modifier wrapper for a REG_OLS or
// REG_GLM spec. Family selection (GLM only) is resolved here so the
// modifier driver can drive IRLS without re-parsing the spec.
func newModifierEngine(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error) {
	switch spec.Type {
	case types.REG_OLS:
		return &modifierEngine{
			spec:   spec,
			schema: schema,
			family: "OLS",
		}, nil
	case types.REG_GLM:
		fam, err := selectFamily(spec.Family, spec.Link)
		if err != nil {
			return nil, err
		}
		return &modifierEngine{
			spec:   spec,
			schema: schema,
			family: "GLM",
			glmFam: fam,
		}, nil
	default:
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"modifierEngine constructed for unsupported regression type",
			map[string]any{"type": string(spec.Type)},
		)
	}
}

// Fit satisfies the Engine interface for callers that never feed rows.
// Modifier dispatch needs every record; with no records, return
// INSUFFICIENT_DATA so the response shape matches the inner engine's
// no-row contract.
func (e *modifierEngine) Fit() (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"modifierEngine.Fit called after Finalize",
		)
	}
	e.finalized = true
	return nil, errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
		"regression with Resample / Selection modifier requires the buffered orchestrator path; engine received no records",
		map[string]any{"name": e.spec.Name, "type": string(e.spec.Type)},
	)
}

// FitBuffered is the modifier dispatch. Order:
//
//  1. Selection (if set) — picks the active predictor subset using the
//     AIC/BIC-driven greedy search. The final fit at the selected set
//     populates baseResult.
//  2. Resample (if set) — refits the inner engine many times to derive
//     SE / p-values that replace the analytical values from step 1.
//  3. If neither modifier is set we wouldn't be in this engine, but
//     defensively run the unmodified inner fit.
func (e *modifierEngine) FitBuffered(records []Record) (*types.RegressionResult, error) {
	if e.finalized {
		return nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"modifierEngine.FitBuffered called after Finalize",
		)
	}
	e.finalized = true

	// Predictor universe for selection / refit.
	predictors := e.spec.Predictors
	activePredictors := predictors

	// Selection phase: pick the active subset.
	if e.spec.Selection != "" {
		critFn := e.makeCriterionFn()
		fitter := e.makeRefitFitter()
		finalizeFn := e.makeFinalizeFn()
		out, err := runSelection(e.spec, records, predictors, critFn, fitter, finalizeFn)
		if err != nil {
			return nil, err
		}
		// If Resample is not set, return the selection-only result.
		if e.spec.Resample == "" {
			return out, nil
		}
		// Resample reuses the selected predictor subset.
		activePredictors = out.SelectedFeatures
		// Pre-populate baseResult for the resample driver.
		return e.runResampleWith(records, activePredictors, out)
	}

	// No selection: just resample around the inner fit.
	if e.spec.Resample != "" {
		baseResult, err := e.finalizeOnPredictors(records, activePredictors)
		if err != nil {
			return nil, err
		}
		return e.runResampleWith(records, activePredictors, baseResult)
	}

	// No modifier; finalize as the unmodified inner engine would.
	return e.finalizeOnPredictors(records, activePredictors)
}

// runResampleWith dispatches the Resample driver around a baseResult.
// activePredictors is the predictor universe the resample operates on
// (post-selection or original spec). baseResult carries the point
// estimate (β); the resample replaces only StdErrors / PValues.
func (e *modifierEngine) runResampleWith(
	records []Record,
	activePredictors []string,
	baseResult *types.RegressionResult,
) (*types.RegressionResult, error) {
	innerFitter, pValueFn := e.makeResampleFitter(activePredictors)
	out, err := runResample(e.spec, records, activePredictors, baseResult, innerFitter, pValueFn)
	if err != nil {
		return nil, err
	}
	// Suppress the L1 approximate-SE warning when Resample is set —
	// bootstrap/jackknife is the rigorous answer. The warning is emitted
	// by descriptor.validateRegressions on the predict path; this engine
	// path doesn't re-emit it. Document the suppression on the result by
	// echoing Resample alongside the L1 Penalty.
	return out, nil
}

// makeCriterionFn returns the (refit, k0) → criterion closure used by
// selection. Reads spec.Criterion ("aic" | "bic") and routes to the
// appropriate aicBIC helper for the engine family.
func (e *modifierEngine) makeCriterionFn() func(*refitResult, int) float64 {
	wantBIC := e.spec.Criterion == "bic"
	if e.family == "GLM" {
		return func(fit *refitResult, k0 int) float64 {
			aic, bic, _ := aicBICFromGLM(fit.Deviance, fit.N, k0)
			if wantBIC {
				return bic
			}
			return aic
		}
	}
	return func(fit *refitResult, k0 int) float64 {
		aic, bic, _ := aicBICFromOLS(fit.RSS, fit.N, k0)
		if wantBIC {
			return bic
		}
		return aic
	}
}

// makeRefitFitter returns a refit closure used by selection — it fits
// the inner engine on (records, activePredictors) and returns the
// summary refitResult.
func (e *modifierEngine) makeRefitFitter() func([]Record, []string) (*refitResult, error) {
	if e.family == "GLM" {
		return func(recs []Record, active []string) (*refitResult, error) {
			return glmFitter(recs, e.spec.Target, active, e.glmFam, e.spec.MaxIters, e.spec.Tol)
		}
	}
	return func(recs []Record, active []string) (*refitResult, error) {
		return olsFitter(recs, e.spec.Target, active, e.spec)
	}
}

// makeResampleFitter returns the per-replicate refit closure for the
// resample driver plus the analytical p-value function. Both honor the
// active predictor subset chosen by Selection (or the full spec
// predictors when Selection is unset).
func (e *modifierEngine) makeResampleFitter(activePredictors []string) (func([]Record) (*refitResult, error), func(coef, se float64) float64) {
	if e.family == "GLM" {
		return func(recs []Record) (*refitResult, error) {
				return glmFitter(recs, e.spec.Target, activePredictors, e.glmFam, e.spec.MaxIters, e.spec.Tol)
			}, func(coef, se float64) float64 {
				return waldZTwoSidedP(coef, se)
			}
	}
	// OLS resample: use a Wald-z p-value form (β/SE under standard
	// normal). The resample-derived SE is already the load-bearing
	// output; the asymptotic-t correction from df = N − k − 1 is a
	// small refinement on top, and at the n-large regime where
	// jackknife / bootstrap is meaningful the t and z forms coincide.
	// The Student-t variant is available via pValueForCoefficient
	// should a future tighter interpretation demand it.
	return func(recs []Record) (*refitResult, error) {
			return olsFitter(recs, e.spec.Target, activePredictors, e.spec)
		}, func(coef, se float64) float64 {
			return waldZTwoSidedP(coef, se)
		}
}

// makeFinalizeFn returns the closure selection uses to fit the *final*
// model on the chosen active set and package a full RegressionResult.
// This is what surfaces back to the response when Selection is the only
// modifier; when Resample is also set, the resample driver overwrites
// SE/PValues on this result.
func (e *modifierEngine) makeFinalizeFn() func([]Record, []string) (*types.RegressionResult, error) {
	return func(recs []Record, active []string) (*types.RegressionResult, error) {
		return e.finalizeOnPredictors(recs, active)
	}
}

// finalizeOnPredictors fits the inner engine on the given predictor
// subset and returns the full RegressionResult (β, SE, p-values, R²,
// etc.). For OLS this routes through the unmodified olsEngine path;
// for GLM through glmEngine.FitBuffered. The wrapped spec is shallow-
// copied with Resample / Selection / Criterion blanked so the inner
// engine doesn't re-enter modifierEngine via the registry.
func (e *modifierEngine) finalizeOnPredictors(records []Record, active []string) (*types.RegressionResult, error) {
	inner := *e.spec
	inner.Predictors = active
	inner.Resample = ""
	inner.Selection = ""
	inner.Criterion = ""

	switch e.family {
	case "OLS":
		// Handle the intercept-only edge case: olsEngine rejects an
		// empty predictor list. Build a degenerate result manually.
		if len(active) == 0 {
			return e.fitOLSInterceptOnly(records)
		}
		eng, err := newOLSEngine(&inner, e.schema)
		if err != nil {
			return nil, err
		}
		be, ok := eng.(BufferedEngine)
		if !ok {
			return nil, errors.NewCodedError(
				errors.PROCESSING_INTERNAL,
				"olsEngine did not yield BufferedEngine for inner finalize",
			)
		}
		return be.FitBuffered(records)
	case "GLM":
		if len(active) == 0 {
			return e.fitGLMInterceptOnly(records)
		}
		eng, err := newGLMEngine(&inner, e.schema)
		if err != nil {
			return nil, err
		}
		be, ok := eng.(BufferedEngine)
		if !ok {
			return nil, errors.NewCodedError(
				errors.PROCESSING_INTERNAL,
				"glmEngine did not yield BufferedEngine for inner finalize",
			)
		}
		return be.FitBuffered(records)
	default:
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"modifierEngine.finalizeOnPredictors: unsupported family",
			map[string]any{"family": e.family},
		)
	}
}

// fitOLSInterceptOnly produces the degenerate intercept-only OLS fit
// for selection paths that converge on no predictors. β₀ = ȳ; SE on
// the intercept is σ̂ / √n with σ̂² = m2YY / (n − 1).
func (e *modifierEngine) fitOLSInterceptOnly(records []Record) (*types.RegressionResult, error) {
	fit, err := olsFitter(records, e.spec.Target, nil, e.spec)
	if err != nil {
		return nil, err
	}
	n := fit.N
	if n < 2 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
			"intercept-only OLS fit requires n ≥ 2",
			map[string]any{"n": n},
		)
	}
	sigma2 := fit.RSS / float64(n-1)
	se0 := 0.0
	if sigma2 > 0 {
		se0 = math.Sqrt(sigma2 / float64(n))
	}
	coefMap := map[string]float64{InterceptKey: fit.Intercept}
	seMap := map[string]float64{InterceptKey: se0}
	pMap := map[string]float64{}
	if se0 > 0 {
		pMap[InterceptKey] = pValueForCoefficient(fit.Intercept, se0, n-1)
	}
	residualStdErr := 0.0
	if sigma2 > 0 {
		residualStdErr = math.Sqrt(sigma2)
	}
	return &types.RegressionResult{
		Name:           e.spec.Name,
		Type:           types.REG_OLS,
		Penalty:        e.spec.Penalty,
		Alpha:          e.spec.Alpha,
		L1Ratio:        e.spec.L1Ratio,
		Coefficients:   coefMap,
		StdErrors:      seMap,
		PValues:        pMap,
		NObs:           n,
		ResidualStdErr: residualStdErr,
	}, nil
}

// fitGLMInterceptOnly produces the degenerate intercept-only GLM fit.
// β₀ = g(ȳ); deviance = NullDeviance for the family.
func (e *modifierEngine) fitGLMInterceptOnly(records []Record) (*types.RegressionResult, error) {
	fit, err := glmFitter(records, e.spec.Target, nil, e.glmFam, e.spec.MaxIters, e.spec.Tol)
	if err != nil {
		return nil, err
	}
	return &types.RegressionResult{
		Name:         e.spec.Name,
		Type:         types.REG_GLM,
		Family:       e.glmFam.Name,
		Link:         e.glmFam.Link,
		Coefficients: map[string]float64{InterceptKey: fit.Intercept},
		StdErrors:    map[string]float64{},
		PValues:      map[string]float64{},
		Deviance:     fit.Deviance,
		NullDeviance: fit.Deviance,
		PseudoR2:     0,
		NObs:         fit.N,
	}, nil
}
