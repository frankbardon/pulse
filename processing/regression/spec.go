package regression

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ValidateRegression performs semantic spec validation that the engine
// itself needs but that the lighter descriptor.validateRegressions
// (header-only, no execution dependencies) cannot reach. Today this is
// the Penalty / Alpha / L1Ratio coupling introduced in Phase 2.
//
// Returns nil on a well-formed spec; otherwise a PROCESSING_CONFIG
// CodedError naming the offending field. The engine factory calls this
// after the schema check and before constructing the accumulator so
// invalid combos never reach the streaming path.
//
// Validation rules:
//   - Penalty=="l1"|"l2"   : require Alpha > 0 and L1Ratio == 0
//     (callers who want elastic-net should set Penalty="elasticnet").
//   - Penalty=="elasticnet": require Alpha > 0 and 0 < L1Ratio < 1.
//     L1Ratio == 0 → user should use l2; L1Ratio == 1 → use l1.
//   - Penalty=="" (unpenalized): Alpha / L1Ratio must be zero (we
//     reject silent-typo specs like Penalty="" with Alpha=0.5 — the
//     intent is ambiguous).
//   - Unknown Penalty values → reject. Descriptor's enum check normally
//     catches this but ValidateRegression is also reachable from
//     hand-built specs in tests.
func ValidateRegression(spec *types.RegressionSpec) error {
	if spec == nil {
		return nil
	}
	if spec.Type != types.REG_OLS {
		// Penalty / Alpha / L1Ratio are REG_OLS-only knobs; ignore them
		// on other engines (their own spec checks will fire when their
		// engines land in later phases).
		return nil
	}

	switch spec.Penalty {
	case "":
		if spec.Alpha != 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS Alpha is only valid when Penalty is set (l1, l2, or elasticnet)",
				map[string]any{"alpha": spec.Alpha, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio != 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS L1Ratio is only valid when Penalty is elasticnet",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
	case "l1", "l2":
		if spec.Alpha <= 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS Penalty="+spec.Penalty+" requires Alpha > 0",
				map[string]any{"alpha": spec.Alpha, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio != 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS L1Ratio is only valid when Penalty=elasticnet; use Penalty=elasticnet to mix l1/l2",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
	case "elasticnet":
		if spec.Alpha <= 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS Penalty=elasticnet requires Alpha > 0",
				map[string]any{"alpha": spec.Alpha, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio <= 0 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS Penalty=elasticnet requires L1Ratio > 0 (use Penalty=l2 for pure ridge)",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
		if spec.L1Ratio >= 1 {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_CONFIG,
				"REG_OLS Penalty=elasticnet requires L1Ratio < 1 (use Penalty=l1 for pure lasso)",
				map[string]any{"l1_ratio": spec.L1Ratio, "penalty": spec.Penalty},
			)
		}
	default:
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_OLS Penalty must be one of \"\", \"l1\", \"l2\", \"elasticnet\"; got "+spec.Penalty,
			map[string]any{"penalty": spec.Penalty},
		)
	}

	// MaxIters / Tol: zero means "use the engine default"; negative is a
	// configuration mistake.
	if spec.MaxIters < 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_OLS MaxIters must be ≥ 0 (zero selects the engine default)",
			map[string]any{"max_iters": spec.MaxIters},
		)
	}
	if spec.Tol < 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_OLS Tol must be ≥ 0 (zero selects the engine default)",
			map[string]any{"tol": spec.Tol},
		)
	}

	return nil
}
