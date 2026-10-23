package descriptor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regcov"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression_slots.go is the declare-or-refuse table for an
// extension REG_* model's shared types.RegressionSpec slots: every
// RegressionSpec field is classified exactly once (regressionFieldClass),
// and every slot an extension model may honour has one rule
// (regressionSlotRules) whose declared predicate reads the
// registration's declaration. A set slot the registration does not
// declare is PROCESSING_CONFIG with details {regression, slot} (plus
// key when the set key is a companion of the slot, e.g. alpha under
// penalty), refused identically by predict and the runtime adapter —
// both call ExtensionRegressionSlotRefusal. Flipping a slot from refused
// to accepted is a change to its declared predicate alone; the
// manifest row (extensionRegressionSlotParams) lists exactly the slots
// the predicate accepts.

// RegressionFieldClass is how an extension REG_* model treats one
// types.RegressionSpec field.
type RegressionFieldClass string

const (
	// RegressionFieldIdentity: the slot's identity or inputs (type,
	// name, target, predictors) — read by every model.
	RegressionFieldIdentity RegressionFieldClass = "identity"
	// RegressionFieldAccepted: honoured on every extension model by a
	// path of its own — params (the declared-keys allowlist),
	// multiplicity (the result's p-valued coefficients; a non-none
	// method needs PValues, the multiplicity resolver's rule), resample
	// with its knobs (the engine's refit driver) and conf_level (the
	// engine's Wald intervals).
	RegressionFieldAccepted RegressionFieldClass = "accepted"
	// RegressionFieldWeightClass: weight — the registration's declared
	// weight class (WeightAware) decides; undeclared, a weight in force
	// is PULSE_EXTENSION_NOT_WEIGHT_AWARE (the weight resolver's rule).
	RegressionFieldWeightClass RegressionFieldClass = "weight_class"
	// RegressionFieldDeclared: a shared slot honoured only when the
	// registration declares it (regressionSlotRules); otherwise refused
	// PROCESSING_CONFIG {regression, slot}.
	RegressionFieldDeclared RegressionFieldClass = "declared"
)

// regressionSlotRule is one shared slot's declare-or-refuse rule.
type regressionSlotRule struct {
	// slot is the slot's JSON key (details.slot).
	slot string
	// keys are the JSON keys the slot governs, slot first: a companion
	// key (alpha under penalty) set without its slot is refused under
	// the slot too.
	keys []string
	// set reports the JSON keys of keys that spec sets.
	set func(spec *types.RegressionSpec) []string
	// declared reports whether decl honours spec's setting of the slot
	// (spec is passed for value-dependent declarations: covariance's
	// type list). Nil = no extension model can honour the slot.
	declared func(decl ExtensionRegression, spec *types.RegressionSpec) bool
	// refusal is the custom-specific message tail (after "regression
	// <TYPE> ") naming the declaration and the fix.
	refusal func(spec *types.RegressionSpec) string
}

// setKeys returns the keys whose present() is true, in order.
func setKeys(pairs ...any) []string {
	var out []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1].(bool) {
			out = append(out, pairs[i].(string))
		}
	}
	return out
}

// regressionSlotRules is the shared-slot table, in RegressionSpec order.
var regressionSlotRules = []regressionSlotRule{
	{
		slot: "penalty", keys: []string{"penalty", "alpha", "l1_ratio"},
		set: func(s *types.RegressionSpec) []string {
			return setKeys("penalty", s.Penalty != "", "alpha", s.Alpha != 0, "l1_ratio", s.L1Ratio != 0)
		},
		declared: func(d ExtensionRegression, _ *types.RegressionSpec) bool { return d.Penalty },
		refusal: func(*types.RegressionSpec) string {
			return "does not declare a penalty (its registration's Penalty is false); drop penalty / alpha / l1_ratio or pass the model's own regularization through params"
		},
	},
	{
		slot: "unpenalized", keys: []string{"unpenalized"},
		set: func(s *types.RegressionSpec) []string { return setKeys("unpenalized", len(s.Unpenalized) != 0) },
		refusal: func(*types.RegressionSpec) string {
			return "takes no unpenalized (a REG_OLS ridge knob); pass the model's own per-predictor penalty through params"
		},
	},
	{
		slot: "bounds", keys: []string{"bounds"},
		set: func(s *types.RegressionSpec) []string { return setKeys("bounds", s.Bounds != nil) },
		refusal: func(*types.RegressionSpec) string {
			return "takes no bounds (a REG_OLS knob); pass the model's own coefficient constraints through params"
		},
	},
	{
		slot: "family", keys: []string{"family"},
		set: func(s *types.RegressionSpec) []string { return setKeys("family", s.Family != "") },
		refusal: func(*types.RegressionSpec) string {
			return "takes no family (a REG_GLM knob); pass the model's own configuration through params"
		},
	},
	{
		slot: "link", keys: []string{"link"},
		set: func(s *types.RegressionSpec) []string { return setKeys("link", s.Link != "") },
		refusal: func(*types.RegressionSpec) string {
			return "takes no link (a REG_GLM knob); pass the model's own configuration through params"
		},
	},
	{
		slot: "max_iters", keys: []string{"max_iters"},
		set: func(s *types.RegressionSpec) []string { return setKeys("max_iters", s.MaxIters != 0) },
		refusal: func(*types.RegressionSpec) string {
			return "takes no max_iters (a built-in solver knob); pass the model's own iteration cap through params"
		},
	},
	{
		slot: "tol", keys: []string{"tol"},
		set: func(s *types.RegressionSpec) []string { return setKeys("tol", s.Tol != 0) },
		refusal: func(*types.RegressionSpec) string {
			return "takes no tol (a built-in solver knob); pass the model's own tolerance through params"
		},
	},
	{
		slot: "prior", keys: []string{"prior", "prior_mu", "prior_precision", "prior_shape", "prior_rate", "credible_level"},
		set: func(s *types.RegressionSpec) []string {
			return setKeys("prior", s.Prior != "", "prior_mu", s.PriorMu != nil, "prior_precision", !s.PriorPrecision.IsZero(),
				"prior_shape", s.PriorShape != 0, "prior_rate", s.PriorRate != 0, "credible_level", s.CredibleLevel != 0)
		},
		refusal: func(*types.RegressionSpec) string {
			return "takes no prior (REG_BAYES_LINEAR knobs); pass the model's own prior through params"
		},
	},
	{
		slot: "selection", keys: []string{"selection", "criterion"},
		set: func(s *types.RegressionSpec) []string {
			return setKeys("selection", s.Selection != "", "criterion", s.Criterion != "")
		},
		declared: func(d ExtensionRegression, _ *types.RegressionSpec) bool {
			return d.SupportsSelection
		},
		refusal: func(*types.RegressionSpec) string {
			return "does not support subset selection (its registration's SupportsSelection is false: the model cannot be refit over a predictor subset); drop selection / criterion, or use resample, which every model accepts"
		},
	},
	{
		slot: "vcov", keys: []string{"vcov"},
		set: func(s *types.RegressionSpec) []string { return setKeys("vcov", s.Vcov) },
		declared: func(d ExtensionRegression, _ *types.RegressionSpec) bool {
			return d.Vcov
		},
		refusal: func(*types.RegressionSpec) string {
			return "does not declare a coefficient covariance (its registration's Vcov is false); drop vcov: true"
		},
	},
	{
		slot: "covariance", keys: []string{"covariance"},
		set: func(s *types.RegressionSpec) []string { return setKeys("covariance", s.Covariance != nil) },
		declared: func(d ExtensionRegression, s *types.RegressionSpec) bool {
			return slices.Contains(d.CovarianceTypes, s.Covariance.EffectiveType())
		},
		refusal: func(s *types.RegressionSpec) string {
			return fmt.Sprintf("does not declare covariance type %q (its registration's CovarianceTypes); drop covariance or use a declared type", s.Covariance.EffectiveType())
		},
	},
	{
		slot: "save", keys: []string{"save"},
		set: func(s *types.RegressionSpec) []string { return setKeys("save", s.Save != "") },
		declared: func(d ExtensionRegression, _ *types.RegressionSpec) bool {
			return d.Scorer
		},
		refusal: func(*types.RegressionSpec) string {
			return "cannot be saved (its registration supplies no scorer, so a saved model could not score); drop save"
		},
	},
}

// regressionFieldClasses classifies every types.RegressionSpec JSON key
// that no shared-slot rule governs.
var regressionFieldClasses = map[string]RegressionFieldClass{
	"type":         RegressionFieldIdentity,
	"name":         RegressionFieldIdentity,
	"target":       RegressionFieldIdentity,
	"predictors":   RegressionFieldIdentity,
	"params":       RegressionFieldAccepted,
	"multiplicity": RegressionFieldAccepted,
	// resample and its knobs: the engine's jackknife / bootstrap driver
	// refits any model over row subsets, so every extension model
	// accepts them (internal/processing/regression.RunExtensionModifiers).
	"resample":         RegressionFieldAccepted,
	"jackknife_groups": RegressionFieldAccepted,
	"jackknife_by":     RegressionFieldAccepted,
	"bootstrap_iters":  RegressionFieldAccepted,
	"rng_seed":         RegressionFieldAccepted,
	// conf_level: the engine builds every model's Wald intervals from
	// the reported (or replicate) standard errors
	// (regression.AttachExtensionIntervals), so every model accepts it.
	"conf_level": RegressionFieldAccepted,
	// holdout: the engine partitions the rows, fits the model on the
	// train rows and scores the held-out rows through the model's own
	// scorer, so every model accepts it; one whose registration supplies
	// no Scorer is refused PULSE_MODEL_SCORE_INVALID no_scorer by the
	// field-reference walk (model.HoldoutScorerRefusal).
	"holdout": RegressionFieldAccepted,
	"weight":  RegressionFieldWeightClass,
}

// RegressionFieldClassOf classifies the types.RegressionSpec JSON key:
// its class and, for a declared slot, the slot that governs it. ok is
// false for a key nothing classifies — the gate
// (TestExtensions_RegressionSharedSlotsRefused) fails on one, so a new
// RegressionSpec field cannot reach an extension model unclassified.
func RegressionFieldClassOf(key string) (class RegressionFieldClass, slot string, ok bool) {
	if c, ok := regressionFieldClasses[key]; ok {
		return c, "", true
	}
	for _, r := range regressionSlotRules {
		if slices.Contains(r.keys, key) {
			return RegressionFieldDeclared, r.slot, true
		}
	}
	return "", "", false
}

// ExtensionRegressionSlotRefusal is the declare-or-refuse rule for an
// extension REG_* model's shared RegressionSpec slots — ONE function
// predict (validateRegressions) and the runtime adapter both call, so
// the two refuse identically. The first set slot (regressionSlotRules
// order) the registration does not declare is PROCESSING_CONFIG with
// details {regression, slot} (regression = the spec's type; name when
// the spec carries one; key when the set key is a companion of the
// slot). A declared covariance then passes the shared shape rule
// (regcov.CheckDeclared). Nil when every set slot is declared.
func ExtensionRegressionSlotRefusal(spec *types.RegressionSpec, decl ExtensionRegression) *errors.CodedError {
	if spec == nil {
		return nil
	}
	for _, r := range regressionSlotRules {
		keys := r.set(spec)
		if len(keys) == 0 {
			continue
		}
		if r.declared != nil && r.declared(decl, spec) {
			continue
		}
		details := map[string]any{"regression": string(spec.Type), "slot": r.slot}
		if keys[0] != r.slot {
			details["key"] = keys[0]
		}
		if spec.Name != "" {
			details["name"] = spec.Name
		}
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"regression "+string(spec.Type)+" "+r.refusal(spec), details)
	}
	return regcov.CheckDeclared(spec)
}

// regressionSlotParamsFor lists the manifest Params (Slot) of the
// shared slots decl accepts beyond target / predictors, in table order
// — exactly the slots ExtensionRegressionSlotRefusal lets through.
// covariance's value-dependent rule is probed per declared type.
func regressionSlotParamsFor(decl ExtensionRegression) []descriptor.Param {
	var out []descriptor.Param
	accepts := func(slot string, spec *types.RegressionSpec) bool {
		for _, r := range regressionSlotRules {
			if r.slot == slot {
				return r.declared != nil && r.declared(decl, spec)
			}
		}
		return false
	}
	probe := &types.RegressionSpec{}
	if accepts("penalty", probe) {
		out = append(out,
			descriptor.Param{Name: "penalty", Type: "string", Description: "The model's regularization scheme, read by the model itself; a top-level key of the regression entry.", Slot: true},
			descriptor.Param{Name: "alpha", Type: "float", Description: "The model's regularization strength, read by the model itself; a top-level key of the regression entry.", Slot: true},
			descriptor.Param{Name: "l1_ratio", Type: "float", Description: "The model's l1 / l2 mixing parameter, read by the model itself; a top-level key of the regression entry.", Slot: true},
		)
	}
	// resample: every extension model accepts it (an accepted field).
	out = append(out, descriptor.Param{Name: "resample", Type: "enum", EnumValues: []string{"", "bootstrap", "jackknife"}, Description: "Resampling layer: the engine refits the model over row subsets (jackknife_groups, jackknife_by, bootstrap_iters and rng_seed tune it); a top-level key of the regression entry.", Slot: true})
	// conf_level: every extension model accepts it (an accepted field).
	out = append(out, descriptor.Param{Name: "conf_level", Type: "float", Default: 0.95, Description: "Coverage of ci_lower / ci_upper, 0 < conf_level < 1: the engine's Wald interval coefficient ∓ q·SE on the model's reported standard error — q the Student-t quantile on the fit's reported df, else the standard normal; the normal on the replicate SE under resample; null where the SE is. A top-level key of the regression entry.", Slot: true})
	// multiplicity: an accepted field, listed when the model declares
	// PValues (without them only method none is accepted).
	if decl.PValues {
		out = append(out, descriptor.Param{Name: "multiplicity", Type: "object", Description: "{method, alpha, coefficients}: multiple-comparison correction over the coefficients carrying a p-value (the intercept excluded unless coefficients names it); a top-level key of the regression entry.", Slot: true})
	}
	if accepts("selection", probe) {
		out = append(out,
			descriptor.Param{Name: "selection", Type: "enum", EnumValues: []string{"", "backward", "forward", "stepwise"}, Description: "Subset-selection wrapper: the engine refits the model over predictor subsets; a top-level key of the regression entry.", Slot: true},
			descriptor.Param{Name: "criterion", Type: "enum", EnumValues: []string{"aic", "bic"}, Description: "Information criterion driving selection; a top-level key of the regression entry.", Slot: true},
		)
	}
	if accepts("vcov", probe) {
		out = append(out, descriptor.Param{Name: "vcov", Type: "bool", Default: false, Description: "Emit the model's coefficient covariance (vcov) and its correlation; a top-level key of the regression entry.", Slot: true})
	}
	var covTypes []string
	for _, ct := range types.AllCovarianceTypes() {
		if accepts("covariance", &types.RegressionSpec{Covariance: &types.RegressionCovariance{Type: ct}}) {
			covTypes = append(covTypes, string(ct))
		}
	}
	if len(covTypes) > 0 {
		out = append(out, descriptor.Param{Name: "covariance", Type: "object", Description: "{type, cluster, order_by, lags}: the covariance estimator the model computes; declared types: " + strings.Join(covTypes, ", ") + ". A top-level key of the regression entry.", Slot: true})
	}
	// holdout: an accepted field, listed when the model can be scored.
	if decl.Scorer {
		out = append(out, descriptor.Param{Name: "holdout", Type: "string", Description: holdoutParamDescription, Slot: true})
	}
	if accepts("save", probe) {
		out = append(out, descriptor.Param{Name: "save", Type: "string", Description: "Write the finished fit as a model file (.pulse-model.json); a top-level key of the regression entry.", Slot: true})
	}
	return out
}

// RegressionSelectionCriterionRefusal is the rule that a selection
// names its criterion — SERVICE_VALIDATION, details {selection}, when
// spec sets selection without criterion. Predict applies it to every
// regression; the extension adapter applies the same call before an
// extension model's selection driver runs.
func RegressionSelectionCriterionRefusal(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Selection == "" || spec.Criterion != "" {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
		"regression Selection requires a Criterion (aic | bic)",
		map[string]any{"selection": spec.Selection})
}
