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
//     family / link / theta / dispersion ("no_family"), a penalty
//     ("unpenalized"), bounds ("unbounded"), selection ("no_selection"),
//     a resample and its knobs ("no_resample"), the Bayesian prior
//     ("no_prior"), solver ("no_solver"), intercept false ("intercept"),
//     a robust covariance ("classical_covariance"), margins
//     ("no_margins"), collinearity ("no_collinearity"), save
//     ("no_save") — each a follow-up, never a silent no-op — and a
//     negative max_iters / tol ("max_iters ≥ 0", "tol ≥ 0");
//   - CheckTarget: the target is a categorical cohort field (its
//     dictionary order is the category order; requires
//     "categorical_target"), and `reference` one of its labels (requires
//     "a level of the target").
//
// A holdout is refused by the shared scorer rule (nothing scores a
// multinomial fit: model.HoldoutScorerRefusal), a numeric schema target
// by the accepted-types table (internal/fieldaccept).
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
	{"penalty", "unpenalized", "a penalized multinomial fit is not built", func(s *types.RegressionSpec) bool { return s.Penalty != "" }},
	{"alpha", "unpenalized", "alpha is a penalty's strength", func(s *types.RegressionSpec) bool { return s.Alpha != 0 }},
	{"l1_ratio", "unpenalized", "l1_ratio is an elastic-net penalty's mix", func(s *types.RegressionSpec) bool { return s.L1Ratio != 0 }},
	{"unpenalized", "unpenalized", "unpenalized is a ridge penalty's", func(s *types.RegressionSpec) bool { return len(s.Unpenalized) != 0 }},
	{"bounds", "unbounded", "coefficient bounds are a REG_OLS knob", func(s *types.RegressionSpec) bool { return s.Bounds.Active() }},
	{"selection", "no_selection", "subset selection over a multinomial fit is not built", func(s *types.RegressionSpec) bool { return s.Selection != "" }},
	{"criterion", "no_selection", "criterion drives a selection", func(s *types.RegressionSpec) bool { return s.Criterion != "" }},
	{"resample", "no_resample", "a resampled multinomial fit is not built", func(s *types.RegressionSpec) bool { return s.Resample != "" }},
	{"jackknife_groups", "no_resample", "jackknife_groups is a jackknife's", func(s *types.RegressionSpec) bool { return s.JackknifeGroups != 0 }},
	{"jackknife_by", "no_resample", "jackknife_by is a jackknife's", func(s *types.RegressionSpec) bool { return s.JackknifeBy != "" }},
	{"bootstrap_iters", "no_resample", "bootstrap_iters is a bootstrap's", func(s *types.RegressionSpec) bool { return s.BootstrapIters != 0 }},
	{"rng_seed", "no_resample", "rng_seed seeds a resample", func(s *types.RegressionSpec) bool { return s.RNGSeed != 0 }},
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
	{"save", "no_save", "a multinomial fit has no model file", func(s *types.RegressionSpec) bool { return s.Save != "" }},
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
