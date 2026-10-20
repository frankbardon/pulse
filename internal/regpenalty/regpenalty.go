// Package regpenalty is the one rule over a regression's penalty knobs
// (types.RegressionSpec Penalty / Alpha / L1Ratio) on REG_OLS and
// REG_GLM, shared by the no-execute predict pass (internal/descriptor)
// and the regression engines (internal/processing/regression) so the two
// refuse the same requests with the same code, message and details. It
// reads the spec only — never a schema or a record — and imports
// neither side.
//
// Both types read the penalty on one objective, glmnet's
// (standardize = FALSE): −loglik/N* + α·[(1−l1_ratio)/2·‖β‖² +
// l1_ratio·‖β‖₁], the intercept unpenalised.
package regpenalty

import (
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Families lists the REG_GLM families a penalty is defined for.
var Families = []string{"binomial", "poisson"}

// Check returns the PROCESSING_CONFIG refusal of spec's penalty knobs,
// or nil. Rules (REG_OLS and REG_GLM; every other type passes — its own
// spec check owns its knobs):
//
//   - Penalty "": Alpha and L1Ratio must be zero.
//   - Penalty "l1" / "l2": Alpha > 0 and L1Ratio == 0.
//   - Penalty "elasticnet": Alpha > 0 and 0 < L1Ratio < 1.
//   - any other Penalty is refused.
//   - REG_GLM: a penalty on a family outside Families (gamma) is
//     refused, judged after the knobs themselves (an unknown family is
//     the family check's refusal, never this one).
//   - Unpenalized (checkUnpenalized): REG_OLS with Penalty "l2" only,
//     each name a predictor, once.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || (spec.Type != types.REG_OLS && spec.Type != types.REG_GLM) {
		return nil
	}
	t := string(spec.Type)
	switch spec.Penalty {
	case "":
		if spec.Alpha != 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				t+" Alpha is only valid when Penalty is set (l1, l2, or elasticnet)",
				map[string]any{"alpha": spec.Alpha, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio != 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				t+" L1Ratio is only valid when Penalty is elasticnet",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
		return checkUnpenalized(spec)
	case "l1", "l2":
		if spec.Alpha <= 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				t+" Penalty="+spec.Penalty+" requires Alpha > 0",
				map[string]any{"alpha": spec.Alpha, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio != 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				t+" L1Ratio is only valid when Penalty=elasticnet; use Penalty=elasticnet to mix l1/l2",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
	case "elasticnet":
		if spec.Alpha <= 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				t+" Penalty=elasticnet requires Alpha > 0",
				map[string]any{"alpha": spec.Alpha, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio <= 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				t+" Penalty=elasticnet requires L1Ratio > 0 (use Penalty=l2 for pure ridge)",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio >= 1 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				t+" Penalty=elasticnet requires L1Ratio < 1 (use Penalty=l1 for pure lasso)",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
	default:
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			t+" Penalty must be one of \"\", \"l1\", \"l2\", \"elasticnet\"; got "+spec.Penalty,
			map[string]any{"penalty": spec.Penalty},
		)
	}
	if spec.Type == types.REG_GLM && spec.Family == "gamma" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM Penalty is defined for the binomial and poisson families only; fit family="+spec.Family+" unpenalised",
			map[string]any{"penalty": spec.Penalty, "family": spec.Family, "allowed_families": Families, "type": t},
		)
	}
	return checkUnpenalized(spec)
}

// checkUnpenalized is the Unpenalized rule (REG_OLS ridge only): absent
// passes; on REG_GLM, or on a REG_OLS fit whose Penalty is not "l2", it
// is refused; every name must be one of Predictors, listed once. The
// lasso / elastic-net per-coefficient penalty factor is not offered.
func checkUnpenalized(spec *types.RegressionSpec) *errors.CodedError {
	if len(spec.Unpenalized) == 0 {
		return nil
	}
	t := string(spec.Type)
	if spec.Type != types.REG_OLS {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			t+" does not accept Unpenalized; it is a REG_OLS ridge (penalty l2) knob",
			map[string]any{"unpenalized": spec.Unpenalized, "type": t},
		)
	}
	if spec.Penalty != "l2" {
		msg := "REG_OLS Unpenalized requires Penalty=l2 (ridge); "
		if spec.Penalty == "" {
			msg += "an unpenalized fit already leaves every coefficient unshrunk"
		} else {
			msg += "lasso and elastic net take no per-predictor penalty factor"
		}
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			msg,
			map[string]any{"unpenalized": spec.Unpenalized, "penalty": spec.Penalty, "type": t},
		)
	}
	seen := make(map[string]bool, len(spec.Unpenalized))
	for _, name := range spec.Unpenalized {
		if !slices.Contains(spec.Predictors, name) {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS Unpenalized names "+name+", which is not one of Predictors",
				map[string]any{"unpenalized": spec.Unpenalized, "field": name, "predictors": spec.Predictors},
			)
		}
		if seen[name] {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS Unpenalized lists "+name+" twice",
				map[string]any{"unpenalized": spec.Unpenalized, "field": name},
			)
		}
		seen[name] = true
	}
	return nil
}

// UnpenalizedMask returns, for the design's predictor columns, which the
// ridge penalty skips (true = unpenalized): spec.Unpenalized matched by
// name, so a refit over a predictor subset (selection) keeps each name's
// own rule. Nil when spec lists none — every column penalised, the
// historical ridge.
func UnpenalizedMask(spec *types.RegressionSpec, predictors []string) []bool {
	if spec == nil || len(spec.Unpenalized) == 0 {
		return nil
	}
	mask := make([]bool, len(predictors))
	for j, name := range predictors {
		mask[j] = slices.Contains(spec.Unpenalized, name)
	}
	return mask
}

// ApproximateSE reports whether spec selects its predictors from the
// data: an l1 / elasticnet REG_OLS or REG_GLM fit. Such a fit has no
// analytical sampling distribution — the active set is itself a random
// outcome — so it has no coefficient covariance (VcovRefusal) and, unless
// a Resample modifier replaces them, its standard errors and p-values are
// undefined (InferenceUndefined).
func ApproximateSE(spec *types.RegressionSpec) bool {
	return spec != nil && (spec.Type == types.REG_OLS || spec.Type == types.REG_GLM) &&
		(spec.Penalty == "l1" || spec.Penalty == "elasticnet")
}

// InferenceUndefined reports whether spec's standard errors, p-values
// and confidence intervals are undefined: an l1 / elasticnet fit
// (ApproximateSE) without a Resample modifier. The engines report every
// one of them null (key kept, intercept included) and the fit carries
// the PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED advisory; predict
// raises the same advisory from the same predicate. Under Resample the
// replicate standard errors replace them and nothing fires.
func InferenceUndefined(spec *types.RegressionSpec) bool {
	return ApproximateSE(spec) && spec.Resample == ""
}

// InferenceAdvisoryCode is the advisory an InferenceUndefined fit
// carries.
const InferenceAdvisoryCode = errors.PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED

// InferenceAdvisory is the message and details of the
// PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED advisory for spec at the
// slot path slot ("regressions[0]", "requests[1].regressions[0]", …) —
// the one wording predict and the runtime result share. Details:
// {slot, operator, penalty, suggested: {"resample": "bootstrap"}}.
// Call it only when InferenceUndefined(spec).
func InferenceAdvisory(spec *types.RegressionSpec, slot string) (string, map[string]any) {
	msg := string(spec.Type) + " at " + slot + " fits penalty " + spec.Penalty +
		", which selects its predictors from the data, so its standard errors, p-values and confidence intervals have no valid analytical form and are null; " +
		"set resample to bootstrap or jackknife for replicate standard errors, or fit penalty l2 or no penalty for analytical inference."
	return msg, map[string]any{
		"slot":      slot,
		"operator":  string(spec.Type),
		"penalty":   spec.Penalty,
		"suggested": map[string]any{"resample": "bootstrap"},
	}
}

// VcovRefusal returns the PROCESSING_REGRESSION_VCOV_UNSUPPORTED refusal
// of vcov: true on a fit with no coefficient covariance — lasso /
// elastic net on REG_OLS or REG_GLM (the l1 active set is
// data-dependent; Pulse never approximates a covariance), alone or under
// a Resample / Selection modifier — or nil.
func VcovRefusal(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || !spec.Vcov || !ApproximateSE(spec) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED,
		string(spec.Type)+" with penalty="+spec.Penalty+" has no coefficient covariance; drop vcov or fit without penalty",
		map[string]any{"reason": "penalty", "penalty": spec.Penalty, "type": string(spec.Type)},
	)
}
