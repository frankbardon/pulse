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
		return nil
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
	return nil
}

// ApproximateSE reports whether spec's analytical standard errors are a
// plug-in over a data-dependent active set: an l1 / elasticnet REG_OLS
// or REG_GLM fit.
func ApproximateSE(spec *types.RegressionSpec) bool {
	return spec != nil && (spec.Type == types.REG_OLS || spec.Type == types.REG_GLM) &&
		(spec.Penalty == "l1" || spec.Penalty == "elasticnet")
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
