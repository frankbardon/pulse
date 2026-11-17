// Package regmultinom is the one rule over a REG_MULTINOMIAL spec (the
// baseline-category logit, U65 E4-S2), shared by the no-execute predict
// pass (internal/descriptor) and the engine (internal/processing/
// regression) so the two refuse the same requests with the same code,
// message and details. It reads the spec and the cohort schema only and
// imports neither side.
//
// Every refusal is PROCESSING_CONFIG with details.slot (the spec key)
// and details.requires naming what the request must change:
//
//   - `reference` on any other regression (requires "type
//     REG_MULTINOMIAL");
//   - a RegressionSpec knob the multinomial fit does not offer: a GLM
//     family / link / theta / dispersion ("no_family"), `reference`
//     beside a penalty ("unpenalized": the penalized fit is symmetric,
//     U68 E2-S3), bounds ("unbounded"), the Bayesian prior
//     ("no_prior"), solver ("no_solver"), intercept false ("intercept"),
//     a robust covariance ("classical_covariance"), margins
//     ("no_margins"), collinearity ("no_collinearity") — each a
//     follow-up, never a silent no-op — and a negative max_iters / tol
//     ("max_iters ≥ 0", "tol ≥ 0");
//   - CheckTarget: the target is a categorical cohort field (its
//     dictionary order is the category order; requires
//     "categorical_target"), and `reference` one of its labels (requires
//     "a level of the target").
//
// Resample and selection run (U68 E2-S2: every refit keeps the full
// fit's categories and reference; selection is term-level), so the
// shared rules (criterion, the jackknife knobs, weights) are theirs.
// The penalty runs (U68 E2-S3: glmnet's symmetric multinomial, every
// category its own coefficients, no reference): its knobs' coupling is
// internal/regpenalty's.
//
// `save` writes a model file carrying the fit's categories (U68 E1-S2:
// the fit scores one category per ATTR_REG_PREDICT slot). A holdout
// scores each held-out row's category probabilities (U68 E1-S3:
// regression.HoldoutOutcomeFigures — log_loss, brier, accuracy). A
// numeric schema target is refused by the accepted-types table
// (internal/fieldaccept).
package regmultinom

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Key is the result key of category's coefficient on term:
// "<category>:<term>", nnet::multinom's vcov row name (term
// "(intercept)" for the intercept).
func Key(category, term string) string { return category + ":" + term }

func refuse(spec *types.RegressionSpec, slot, msg, requires string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"type": string(spec.Type), "slot": slot, "requires": requires}
	for k, v := range extra {
		details[k] = v
	}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, string(spec.Type)+" "+msg, details)
}

// unsupported is one RegressionSpec knob REG_MULTINOMIAL refuses: set
// reports whether the spec carries it.
type unsupported struct {
	slot, requires, why string
	set                 func(*types.RegressionSpec) bool
}

// unsupportedKnobs are checked in this order; the first set one refuses.
var unsupportedKnobs = []unsupported{
	{"family", "no_family", "the multinomial logit is its own likelihood, not a GLM family", func(s *types.RegressionSpec) bool { return s.Family != "" }},
	{"link", "no_family", "the multinomial logit's link is the baseline-category logit", func(s *types.RegressionSpec) bool { return s.Link != "" }},
	{"theta", "no_family", "theta is the negative binomial family's", func(s *types.RegressionSpec) bool { return s.Theta != 0 }},
	{"dispersion", "no_family", "dispersion is a fixed-theta negative binomial's", func(s *types.RegressionSpec) bool { return s.Dispersion != "" }},
	{"reference", "unpenalized", "a penalized fit is symmetric (every category carries coefficients, none is the reference)", func(s *types.RegressionSpec) bool { return s.Penalty != "" && s.Reference != "" }},
	{"bounds", "unbounded", "coefficient bounds are a REG_OLS knob", func(s *types.RegressionSpec) bool { return s.Bounds.Active() }},
	{"prior", "no_prior", "a prior is REG_BAYES_LINEAR's", func(s *types.RegressionSpec) bool { return s.Prior != "" }},
	{"prior_mu", "no_prior", "a prior is REG_BAYES_LINEAR's", func(s *types.RegressionSpec) bool { return len(s.PriorMu) != 0 }},
	{"prior_precision", "no_prior", "a prior is REG_BAYES_LINEAR's", func(s *types.RegressionSpec) bool { return !s.PriorPrecision.IsZero() }},
	{"prior_shape", "no_prior", "a prior is REG_BAYES_LINEAR's", func(s *types.RegressionSpec) bool { return s.PriorShape != 0 }},
	{"prior_rate", "no_prior", "a prior is REG_BAYES_LINEAR's", func(s *types.RegressionSpec) bool { return s.PriorRate != 0 }},
	{"credible_level", "no_prior", "credible_level is a posterior's", func(s *types.RegressionSpec) bool { return s.CredibleLevel != 0 }},
	{"solver", "no_solver", "solver picks REG_OLS's least-squares method", func(s *types.RegressionSpec) bool { return s.Solver != "" }},
	{"intercept", "intercept", "every category's linear predictor carries an intercept", func(s *types.RegressionSpec) bool { return s.Intercept != nil && !*s.Intercept }},
	{"covariance", "classical_covariance", "a robust multinomial covariance is not built (the coefficient covariance is the inverse information)", func(s *types.RegressionSpec) bool { return s.Covariance.Robust() }},
	{"margins", "no_margins", "marginal effects are reported by REG_OLS and REG_GLM", func(s *types.RegressionSpec) bool { return s.Margins != nil }},
	{"collinearity", "no_collinearity", "the variance inflation block is reported by REG_OLS and REG_GLM", func(s *types.RegressionSpec) bool { return s.Collinearity != nil }},
}

// Check returns the PROCESSING_CONFIG refusal of spec's knobs, or nil:
// `reference` on another regression, or a knob REG_MULTINOMIAL does not
// offer (the package comment). The engine factory and predict
// (validateRegressions) both call it.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil {
		return nil
	}
	if spec.Type != types.REG_MULTINOMIAL {
		if spec.Reference != "" {
			return refuse(spec, "reference", "does not accept reference; it names REG_MULTINOMIAL's baseline category",
				"type REG_MULTINOMIAL", map[string]any{"reference": spec.Reference})
		}
		return nil
	}
	for _, k := range unsupportedKnobs {
		if k.set(spec) {
			return refuse(spec, k.slot, "does not accept "+k.slot+": "+k.why+"; drop it", k.requires, nil)
		}
	}
	// Zero selects the engine default; a negative value is a mistake.
	if spec.MaxIters < 0 {
		return refuse(spec, "max_iters", "max_iters must be ≥ 0 (zero selects the default 50)",
			"max_iters ≥ 0", map[string]any{"max_iters": spec.MaxIters})
	}
	if spec.Tol < 0 {
		return refuse(spec, "tol", "tol must be ≥ 0 (zero selects the default 1e-8)",
			"tol ≥ 0", map[string]any{"tol": spec.Tol})
	}
	return nil
}

// Levels returns the target's dictionary labels in dictionary order —
// the multinomial category order — and true when target names a
// categorical field of schema.
func Levels(schema *encoding.Schema, target string) ([]string, bool) {
	if schema == nil {
		return nil, false
	}
	f := schema.Field(target)
	if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
		return nil, false
	}
	return f.Dictionary.Values(), true
}

// CheckTarget returns the PROCESSING_CONFIG refusal of a REG_MULTINOMIAL
// spec's target against schema, or nil: the target must be a
// categorical cohort field (a derived column carries no dictionary, so
// no category order), and a `reference` one of its labels. A schema
// field of another type is the accepted-types table's refusal
// (internal/fieldaccept), raised before this; an empty target is the
// missing-target refusal's. Any other regression passes.
func CheckTarget(spec *types.RegressionSpec, schema *encoding.Schema) *errors.CodedError {
	if spec == nil || spec.Type != types.REG_MULTINOMIAL || spec.Target == "" {
		return nil
	}
	f := schema.Field(spec.Target)
	if f == nil {
		return refuse(spec, "target", "target "+spec.Target+" is not a cohort field: the categories and their order are the target's dictionary, which a derived column does not carry",
			"categorical_target", map[string]any{"target": spec.Target})
	}
	if !f.Type.IsCategorical() {
		// The accepted-types table refuses it (PULSE_FIELD_TYPE_UNSUPPORTED).
		return nil
	}
	if spec.Reference == "" {
		return nil
	}
	levels, _ := Levels(schema, spec.Target)
	for _, l := range levels {
		if l == spec.Reference {
			return nil
		}
	}
	return refuse(spec, "reference", "reference "+spec.Reference+" is not a level of the target "+spec.Target,
		"a level of the target", map[string]any{"reference": spec.Reference, "target": spec.Target})
}
