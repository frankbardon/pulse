// Package regression hosts the REG_* operators that fit a regression
// model against the filtered record set: closed-form and penalized OLS
// (ols.go), GLM by IRLS (glm.go), the conjugate Bayesian linear model
// (bayes_linear.go), the baseline-category multinomial logit
// (multinomial.go) and the Resample / Selection modifier wrapper
// (modifier_engine.go). The model regressions (REG_MMM_HILL,
// REG_MMM_BAYES, REG_ORDINAL) are authored on the extension contract
// and installed on this package's registry by internal/regadapt
// (RegisterModel).
//
// The package owns its own registry; the parent processing package
// consumes it through processing.FitRegressions without an import of
// regression-engine internals. To avoid an import cycle with
// processing.Record, this subpackage stays independent of concrete
// Record types and reads rows through its narrow Record interface
// (record.go), the same way internal/processing/feature does.
package regression

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Factory constructs an Engine for a concrete RegressionSpec against a
// schema. The package init (registry.go) registers each built-in
// engine's factory and RegisterModel each model regression's; Lookup
// returns the factory for a given type.
type Factory func(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error)

// Engine is the per-spec regression fit contract. Every engine in the
// tree also implements BufferedEngine (the orchestrator's entry point)
// and, when its fit folds in one pass, StreamingEngine.
type Engine interface {
	// Fit returns the per-spec result of a fit over no records: a
	// built-in engine folded no row, so it surfaces
	// PROCESSING_REGRESSION_INSUFFICIENT_DATA. The orchestrator fits
	// through FitBuffered or UpdateRow + Finalize instead. A non-nil
	// error indicates the orchestrator must not partially include this
	// slot in Response.Regressions.
	Fit() (*types.RegressionResult, error)
}
