package regression

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regbayes"
	"github.com/frankbardon/pulse/internal/regbounds"
	"github.com/frankbardon/pulse/internal/regcov"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/internal/regsolve"
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
//
// A spec is then held to validateConfLevel (0 < conf_level < 1, zero
// the default), when it sets Vcov to validateVcov, and to the shared
// covariance rule regcov.Check.
func ValidateRegression(spec *types.RegressionSpec) error {
	if err := validateRegressionSpec(spec); err != nil {
		return err
	}
	if spec != nil {
		if err := validateConfLevel(spec); err != nil {
			return err
		}
	}
	if err := validateVcov(spec); err != nil {
		return err
	}
	// bounds.nonnegative (REG_OLS only; no penalty, no selection, each
	// name a predictor once) — the one rule predict applies too
	// (internal/regbounds).
	if cerr := regbounds.Check(spec); cerr != nil {
		return cerr
	}
	// solver / intercept (REG_OLS only; their combinations) — the one
	// rule predict applies too (internal/regsolve).
	if cerr := regsolve.Check(spec); cerr != nil {
		return cerr
	}
	// The covariance block: shape, then the robust-type refusals
	// (penalty, resample, selection, a type without the estimators) —
	// the one rule predict applies too (internal/regcov).
	if cerr := regcov.Check(spec); cerr != nil {
		return cerr
	}
	// The jackknife knobs (jackknife_groups / jackknife_by) — the one
	// rule predict applies too (internal/regresample).
	if cerr := regresample.Check(spec); cerr != nil {
		return cerr
	}
	return nil
}

// validateVcov refuses Vcov on a fit with no coefficient covariance to
// report: lasso / elastic net on REG_OLS or REG_GLM (the l1 active set
// is data-dependent, so no sampling covariance exists, and Pulse never
// approximates one) — alone or under a Resample / Selection modifier.
// Every other fit reports one: a Resample fit the replicate covariance
// its SEs are the square roots of the diagonal of, a Selection fit the
// inner engine's covariance from the final refit on the selected
// predictors, an l2 fit its penalised sandwich. The one rule predict
// applies too (internal/regpenalty.VcovRefusal).
func validateVcov(spec *types.RegressionSpec) error {
	if cerr := regpenalty.VcovRefusal(spec); cerr != nil {
		return cerr
	}
	return nil
}

// validateRegressionSpec is ValidateRegression's per-type rule set.
func validateRegressionSpec(spec *types.RegressionSpec) error {
	if spec == nil {
		return nil
	}
	if spec.Type == types.REG_GLM {
		return validateGLMSpec(spec)
	}
	if spec.Type == types.REG_BAYES_LINEAR {
		return validateBayesLinearSpec(spec)
	}
	if spec.Type != types.REG_OLS {
		// Penalty / Alpha / L1Ratio are REG_OLS-only knobs; ignore them
		// on other engines (their own spec checks will fire when their
		// engines land in later phases).
		return nil
	}

	// Penalty / Alpha / L1Ratio: the rule predict applies too
	// (internal/regpenalty).
	if cerr := regpenalty.Check(spec); cerr != nil {
		return cerr
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
//   - Penalty / Alpha / L1Ratio follow the shared rule
//     (internal/regpenalty.Check): the OLS coupling, and no penalty on
//     the gamma family.
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

	// Penalty / Alpha / L1Ratio (gamma refuses a penalty): the rule
	// predict applies too (internal/regpenalty).
	if cerr := regpenalty.Check(spec); cerr != nil {
		return cerr
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

// validateBayesLinearSpec enforces REG_BAYES_LINEAR-specific spec
// invariants: the rule predict applies too (internal/regbayes.Check —
// the modifiers, the prior knobs incl. a per-coefficient PriorPrecision,
// the other engines' knobs). Its remedy clauses render the default
// (nil) form; ExtensionRegistry.ScopeRefusal scopes them to the
// instance.
func validateBayesLinearSpec(spec *types.RegressionSpec) error {
	if cerr := regbayes.Check(spec, nil); cerr != nil {
		return cerr
	}
	return nil
}
