// Package regression hosts the REG_* operators that fit a regression
// model against the filtered record set. Phase 0 lands the API surface
// only — every registered operator returns
// PROCESSING_REGRESSION_NOT_IMPLEMENTED via Fit. Phases 1–5 fill in
// closed-form OLS, regularization, GLM (IRLS), Bayesian linear, and the
// Resample / Selection modifier wrappers.
//
// The package owns its own registry; the parent processing package
// consumes the Fit entry point through processing.FitRegressions
// without an import of regression-engine internals. To avoid an import
// cycle with processing.Record, this subpackage stays independent of
// concrete Record types — Phase 1 will introduce a narrow Record
// interface the same way processing/feature does.
package regression

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Factory constructs an Engine for a concrete RegressionSpec against a
// schema. Per-operator init() functions register their factory in the
// shared registry; Lookup returns the factory for a given type.
type Factory func(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error)

// Engine is the per-spec regression fit contract. Phase 1+ engines
// implement Fit to consume the filtered record set and emit a populated
// RegressionResult. Phase 0 stubs return
// PROCESSING_REGRESSION_NOT_IMPLEMENTED.
type Engine interface {
	// Fit returns the per-spec result. Phase 0 stubs always return a
	// PROCESSING_REGRESSION_NOT_IMPLEMENTED CodedError; later phases
	// populate a RegressionResult on success. A non-nil error indicates
	// the orchestrator must not partially include this slot in
	// Response.Regressions.
	Fit() (*types.RegressionResult, error)
}

// notImplementedEngine is the universal Phase 0 stub. It captures the
// spec only to surface a useful "type" / "name" pair in the error
// details map; no math is performed.
type notImplementedEngine struct {
	spec *types.RegressionSpec
}

// Fit returns a PROCESSING_REGRESSION_NOT_IMPLEMENTED CodedError. The
// orchestrator should bubble the error up; Phase 0 callers receive a
// Response envelope with an empty Regressions slice and an error code
// they can route through pulse_errors_lookup.
func (e *notImplementedEngine) Fit() (*types.RegressionResult, error) {
	details := map[string]any{
		"type": string(e.spec.Type),
	}
	if e.spec.Name != "" {
		details["name"] = e.spec.Name
	}
	if e.spec.Target != "" {
		details["target"] = e.spec.Target
	}
	return nil, errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_NOT_IMPLEMENTED,
		"regression operator "+string(e.spec.Type)+" is not implemented yet (Phase 0 stub)",
		details,
	)
}

// newNotImplemented is the Phase 0 universal factory. Each operator's
// init() wires it via register(); Phases 1–4 swap in real factories
// per-operator without touching the registry plumbing.
func newNotImplemented(spec *types.RegressionSpec, _ *encoding.Schema) (Engine, error) {
	return &notImplementedEngine{spec: spec}, nil
}
