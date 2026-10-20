// Package regbounds is the one rule over a regression's coefficient
// bounds (types.RegressionSpec.Bounds: bounds.nonnegative on REG_OLS),
// shared by the no-execute predict pass (internal/descriptor) and the
// regression engines (internal/processing/regression) so the two refuse
// the same requests with the same code, message and details, and so the
// result's PULSE_ADVISORY_CONSTRAINT_ACTIVE reads one wording. It reads
// the spec only — never a schema or a record — and imports neither side.
//
// The bounded fit is non-negative least squares on the named slopes
// (Lawson–Hanson on the centred Gram, the intercept and every other
// predictor free). Whether a bound binds depends on the data, so the
// advisory is result-only: predict validates the request, never the
// outcome.
package regbounds

import (
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// AdvisoryCode is the advisory a fit whose bound binds carries (without
// a Resample modifier).
const AdvisoryCode = errors.PULSE_ADVISORY_CONSTRAINT_ACTIVE

// Check returns the PROCESSING_CONFIG refusal of spec's Bounds, or nil.
// Rules (built-in types only; an extension model's shared-slot table
// refuses the slot itself):
//
//   - absent, or an empty Nonnegative list: passes (no constraint).
//   - REG_GLM and REG_BAYES_LINEAR refuse it: bounds is a REG_OLS knob.
//   - REG_OLS with a Penalty refuses it: a penalised constrained fit is
//     not offered.
//   - REG_OLS with a Selection refuses it: the criterion search refits
//     unconstrained sub-models.
//   - every name must be one of Predictors, listed once.
//
// A robust Covariance alongside Bounds is internal/regcov's refusal
// (PROCESSING_REGRESSION_VCOV_UNSUPPORTED, reason "bounds").
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || !spec.Bounds.Active() {
		return nil
	}
	t := string(spec.Type)
	nn := spec.Bounds.Nonnegative
	switch spec.Type {
	case types.REG_OLS:
	case types.REG_GLM, types.REG_BAYES_LINEAR:
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			t+" does not accept bounds; bounds.nonnegative is a REG_OLS knob",
			map[string]any{"bounds": map[string]any{"nonnegative": nn}, "type": t},
		)
	default:
		return nil
	}
	if spec.Penalty != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_OLS bounds is not offered with a penalty (penalty "+spec.Penalty+"); fit the bounded model unpenalised, or drop bounds",
			map[string]any{"bounds": map[string]any{"nonnegative": nn}, "penalty": spec.Penalty, "type": t},
		)
	}
	if spec.Selection != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_OLS bounds is not offered with selection (selection "+spec.Selection+"); choose the predictors first, or drop bounds",
			map[string]any{"bounds": map[string]any{"nonnegative": nn}, "selection": spec.Selection, "type": t},
		)
	}
	seen := make(map[string]bool, len(nn))
	for _, name := range nn {
		if !slices.Contains(spec.Predictors, name) {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS bounds.nonnegative names "+name+", which is not one of Predictors",
				map[string]any{"bounds": map[string]any{"nonnegative": nn}, "field": name, "predictors": spec.Predictors},
			)
		}
		if seen[name] {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS bounds.nonnegative lists "+name+" twice",
				map[string]any{"bounds": map[string]any{"nonnegative": nn}, "field": name},
			)
		}
		seen[name] = true
	}
	return nil
}

// Mask returns, for the design's predictor columns, which carry a
// non-negativity bound (true = β ≥ 0), matched by name so a refit over a
// predictor subset keeps each name's own rule. Nil when spec bounds
// nothing — the plain OLS solve.
func Mask(spec *types.RegressionSpec, predictors []string) []bool {
	if spec == nil || !spec.Bounds.Active() {
		return nil
	}
	mask := make([]bool, len(predictors))
	bounded := false
	for j, name := range predictors {
		mask[j] = slices.Contains(spec.Bounds.Nonnegative, name)
		bounded = bounded || mask[j]
	}
	if !bounded {
		return nil
	}
	return mask
}

// InferenceUndefined reports whether a fit of spec whose bound bound
// (active non-empty) reports null analytical inference: no Resample
// modifier replaces the standard errors.
func InferenceUndefined(spec *types.RegressionSpec, active []string) bool {
	return spec != nil && spec.Bounds.Active() && spec.Resample == "" && len(active) != 0
}

// Advisory is the message and details of PULSE_ADVISORY_CONSTRAINT_ACTIVE
// for spec at the slot path slot ("regressions[0]", …), active the
// predictors held at their bound. Details: {slot, operator, active,
// suggested: {"resample": "bootstrap"}}.
func Advisory(spec *types.RegressionSpec, slot string, active []string) (string, map[string]any) {
	names := ""
	for i, a := range active {
		if i > 0 {
			names += ", "
		}
		names += a
	}
	msg := string(spec.Type) + " at " + slot + " holds " + names + " at the bounds.nonnegative bound (coefficient 0), so the estimator is constrained and its standard errors, p-values, confidence intervals and covariance have no valid classical form and are null; " +
		"the coefficients and R² stand. Set resample to bootstrap for replicate standard errors and intervals of the bounded fit."
	return msg, map[string]any{
		"slot":      slot,
		"operator":  string(spec.Type),
		"active":    append([]string{}, active...),
		"suggested": map[string]any{"resample": "bootstrap"},
	}
}
