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
	if spec.Type == types.REG_GLM {
		return validateGLMSpec(spec)
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

// validateGLMSpec enforces REG_GLM-specific spec invariants. Phase 3
// rules:
//
//   - Family ∈ {"binomial", "poisson", "gamma"}. Empty / unknown values
//     surface PROCESSING_REGRESSION_INVALID_FAMILY.
//   - Link, when set, must belong to the family's allowed enum set.
//     Reserved-but-not-implemented values (binomial probit/cloglog,
//     poisson identity/sqrt, gamma log/identity) surface
//     PROCESSING_REGRESSION_INVALID_LINK with a "not yet implemented"
//     hint so callers can fall back to the family default. The engine
//     constructor (newGLMEngine) re-runs the same family/link check via
//     selectFamily; validating here keeps the error reachable from spec
//     introspection tests that never instantiate the engine.
//   - Penalty / Alpha / L1Ratio must be empty / zero. GLM regularization
//     lands in a later phase; mixing it with REG_GLM today produces
//     silent misconfiguration if we don't reject the spec.
//   - MaxIters / Tol must be ≥ 0; zero selects the engine default
//     (50 / 1e-8 respectively).
func validateGLMSpec(spec *types.RegressionSpec) error {
	switch spec.Family {
	case "binomial", "poisson", "gamma":
		// ok
	case "":
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INVALID_FAMILY,
			"REG_GLM requires Family (binomial | poisson | gamma)",
			map[string]any{"family": spec.Family},
		)
	default:
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INVALID_FAMILY,
			"REG_GLM Family must be one of binomial, poisson, gamma; got "+spec.Family,
			map[string]any{"family": spec.Family, "allowed": []string{"binomial", "poisson", "gamma"}},
		)
	}

	// Link validation: empty selects the family default; otherwise the
	// link must belong to the family's allowed set. Reserved-but-not-
	// implemented links are surfaced as INVALID_LINK so the caller can
	// drop back to the family default.
	if spec.Link != "" {
		switch spec.Family {
		case "binomial":
			switch spec.Link {
			case "logit":
				// ok — implemented this phase
			case "probit", "cloglog":
				return errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_INVALID_LINK,
					"REG_GLM Family=binomial Link="+spec.Link+" is a reserved enum value but not yet implemented; use Link=logit",
					map[string]any{"family": spec.Family, "link": spec.Link, "supported": []string{"logit"}},
				)
			default:
				return errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_INVALID_LINK,
					"REG_GLM Family=binomial requires Link in {logit, probit, cloglog}; got "+spec.Link,
					map[string]any{"family": spec.Family, "link": spec.Link, "allowed": []string{"logit", "probit", "cloglog"}},
				)
			}
		case "poisson":
			switch spec.Link {
			case "log":
				// ok — implemented this phase
			case "identity", "sqrt":
				return errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_INVALID_LINK,
					"REG_GLM Family=poisson Link="+spec.Link+" is a reserved enum value but not yet implemented; use Link=log",
					map[string]any{"family": spec.Family, "link": spec.Link, "supported": []string{"log"}},
				)
			default:
				return errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_INVALID_LINK,
					"REG_GLM Family=poisson requires Link in {log, identity, sqrt}; got "+spec.Link,
					map[string]any{"family": spec.Family, "link": spec.Link, "allowed": []string{"log", "identity", "sqrt"}},
				)
			}
		case "gamma":
			switch spec.Link {
			case "inverse":
				// ok — wired this phase, numerical tests deferred
			case "log", "identity":
				return errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_INVALID_LINK,
					"REG_GLM Family=gamma Link="+spec.Link+" is a reserved enum value but not yet implemented; use Link=inverse",
					map[string]any{"family": spec.Family, "link": spec.Link, "supported": []string{"inverse"}},
				)
			default:
				return errors.NewCodedErrorWithDetails(
					errors.PROCESSING_REGRESSION_INVALID_LINK,
					"REG_GLM Family=gamma requires Link in {inverse, log, identity}; got "+spec.Link,
					map[string]any{"family": spec.Family, "link": spec.Link, "allowed": []string{"inverse", "log", "identity"}},
				)
			}
		}
	}

	// GLM penalty / regularization deferred to a later phase. Reject any
	// penalty knob on REG_GLM today so a future user doesn't think a
	// silent fall-through produced a regularized fit.
	if spec.Penalty != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM Penalty is not supported in this phase; regularized GLM is deferred",
			map[string]any{"penalty": spec.Penalty, "type": string(spec.Type)},
		)
	}
	if spec.Alpha != 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM Alpha is not supported in this phase (no GLM penalty); use REG_OLS for regularized fits or wait for the GLM regularization phase",
			map[string]any{"alpha": spec.Alpha, "type": string(spec.Type)},
		)
	}
	if spec.L1Ratio != 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM L1Ratio is not supported in this phase (no GLM penalty)",
			map[string]any{"l1_ratio": spec.L1Ratio, "type": string(spec.Type)},
		)
	}

	if spec.MaxIters < 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM MaxIters must be ≥ 0 (zero selects the engine default of 50)",
			map[string]any{"max_iters": spec.MaxIters},
		)
	}
	if spec.Tol < 0 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM Tol must be ≥ 0 (zero selects the engine default of 1e-8)",
			map[string]any{"tol": spec.Tol},
		)
	}

	return nil
}
