// Package regsolve is the one rule over REG_OLS's solver knobs —
// types.RegressionSpec.Solver ("qr" | "normal") and Intercept (false
// fits through the origin) — shared by the no-execute predict pass
// (internal/descriptor) and the regression engines
// (internal/processing/regression), so the two refuse the same
// requests with the same code, message and details. It reads the spec
// only, never a schema or a record, and imports neither side.
package regsolve

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The REG_OLS solvers. Empty selects SolverQR.
const (
	// SolverQR solves on a streaming Givens QR of the design, at κ(X).
	SolverQR = "qr"
	// SolverNormal solves the normal equations by Cholesky on the
	// centred Gram matrix, at κ(X)².
	SolverNormal = "normal"
)

// FitsIntercept reports whether spec's fit estimates an intercept: true
// unless Intercept is explicitly false.
func FitsIntercept(spec *types.RegressionSpec) bool {
	return spec == nil || spec.Intercept == nil || *spec.Intercept
}

// Solvers lists the accepted Solver values (payload-schema enum order).
func Solvers() []string { return []string{SolverQR, SolverNormal} }

// Check returns the PROCESSING_CONFIG refusal of spec's Solver or
// Intercept, or nil. Rules (built-in types only; an extension model's
// shared-slot table refuses both slots itself):
//
//   - Solver, when set, is "qr" or "normal", on REG_OLS, without a
//     penalty or bounds (the penalised and bounded solvers read the
//     moments; they have no solver choice).
//   - Intercept false is REG_OLS only, with the classical covariance,
//     and without a penalty, bounds, selection, resample, holdout or
//     solver "normal" (each reads the centred moments, which assume an
//     intercept).
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil {
		return nil
	}
	t := string(spec.Type)
	// The engine-built types. The MMM regressions and extension models
	// route through the shared-slot table, which refuses both slots.
	builtin := spec.Type == types.REG_OLS || spec.Type == types.REG_GLM || spec.Type == types.REG_BAYES_LINEAR
	if !builtin {
		return nil
	}
	if spec.Solver != "" {
		details := map[string]any{"solver": spec.Solver, "type": t}
		switch {
		case spec.Solver != SolverQR && spec.Solver != SolverNormal:
			details["allowed"] = Solvers()
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"REG_OLS solver must be \"qr\" or \"normal\"; got "+spec.Solver, details)
		case spec.Type != types.REG_OLS:
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				t+" does not accept solver; solver is a REG_OLS knob", details)
		case spec.Penalty != "":
			details["penalty"] = spec.Penalty
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"REG_OLS solver applies to an unpenalised fit; a penalty ("+spec.Penalty+") has its own solver. Drop solver", details)
		case spec.Bounds.Active():
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"REG_OLS solver applies to an unbounded fit; bounds solve by non-negative least squares. Drop solver", details)
		}
	}
	if FitsIntercept(spec) {
		return nil
	}
	details := map[string]any{"intercept": false, "type": t}
	if spec.Type != types.REG_OLS {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			t+" does not accept intercept: false; regression through the origin is a REG_OLS knob", details)
	}
	refuse := func(slot string, value any) *errors.CodedError {
		details[slot] = value
		details["slot"] = slot
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"REG_OLS intercept: false is not offered with "+slot+"; fit through the origin unpenalised and unmodified, or keep the intercept", details)
	}
	switch {
	case spec.Penalty != "":
		return refuse("penalty", spec.Penalty)
	case spec.Bounds.Active():
		return refuse("bounds", spec.Bounds.Nonnegative)
	case spec.Selection != "":
		return refuse("selection", spec.Selection)
	case spec.Resample != "":
		return refuse("resample", spec.Resample)
	case spec.Holdout != "":
		return refuse("holdout", spec.Holdout)
	case spec.Covariance.Robust():
		return refuse("covariance", string(spec.Covariance.Type))
	case spec.Solver == SolverNormal:
		return refuse("solver", spec.Solver)
	}
	return nil
}
